package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------- /api/http/cases（v0.7）----------
//
// HTTP 用例 + 环境变量组持久化。前端"HTTP 测试"页用它来保存和复用请求模板。
//
// 数据结构：
//   - cases：[]HTTPCase，每个 case 含 group/name/method/url/headers/body/timeout/follow/insecure
//   - envs：[]HTTPEnv，每个 env 含 name/vars(map)，vars 是 "key" -> "value"
//
// 全部存在 data/http_cases.json，单文件 1MB 上限，原子写。

type HTTPCase struct {
	ID             string            `json:"id"`        // uuid-like（用时间戳 + 随机片段）
	Group          string            `json:"group"`     // 分组（用户自定，如 "信贷生产" / "dev"）
	Name           string            `json:"name"`      // 用例名
	Method         string            `json:"method"`    // GET/POST/...
	URL            string            `json:"url"`       // 可含 {{var}} 占位
	Headers        map[string]string `json:"headers"`   // 一律 map
	Body           string            `json:"body"`      // form/urlencoded 时是 urlencoded 字符串；raw 时是 raw 内容
	BodyMode       string            `json:"body_mode"` // v0.7-Redesign: none | formdata | urlencoded | raw
	BodyType       string            `json:"body_type"` // raw 模式下的子类型：json | xml | html | text
	BodyForm       map[string]string `json:"body_form"` // formdata / urlencoded 模式下的字段
	TimeoutMs      int               `json:"timeout_ms"`
	FollowRedirect bool              `json:"follow_redirect"`
	InsecureTLS    bool              `json:"insecure_tls"`
	UpdatedAt      string            `json:"updated_at"`
}

type HTTPEnv struct {
	Name    string            `json:"name"`     // 组名（"dev" / "test" / "prod"）
	Vars    map[string]string `json:"vars"`     // key -> value
	IsLocal bool              `json:"is_local"` // 标记 local-only（不写文件，纯前端用）
}

type httpCasesFile struct {
	Cases []HTTPCase `json:"cases"`
	Envs  []HTTPEnv  `json:"envs"`
}

var httpCasesMu sync.Mutex

func (s *Server) handleHTTPCases(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleHTTPCasesList(w, r)
	case http.MethodPost:
		s.handleHTTPCasesCreate(w, r)
	case http.MethodDelete:
		// DELETE /api/http/cases?id=xxx
		s.handleHTTPCasesDelete(w, r)
	default:
		writeErr(w, 405, errors.New("仅支持 GET / POST / DELETE"))
	}
}

// GET /api/http/cases  → 返回所有用例 + env 组
func (s *Server) handleHTTPCasesList(w http.ResponseWriter, r *http.Request) {
	httpCasesMu.Lock()
	f, err := s.loadHTTPCases()
	httpCasesMu.Unlock()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if f.Cases == nil {
		f.Cases = []HTTPCase{}
	}
	if f.Envs == nil {
		f.Envs = []HTTPEnv{}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "cases": f.Cases, "envs": f.Envs})
}

// POST /api/http/cases  body: {case: HTTPCase} → 新增或覆盖（同 ID 覆盖）
// 简化：id 由后端生成（如果客户端没传）；name+group 必填
func (s *Server) handleHTTPCasesCreate(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > 1*1024*1024 {
		writeErr(w, 400, errors.New("请求体太大（最大 1MB）"))
		return
	}
	var req struct {
		Case HTTPCase `json:"case"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	c := req.Case
	c.Name = strings.TrimSpace(c.Name)
	c.Group = strings.TrimSpace(c.Group)
	if c.Name == "" {
		writeErr(w, 400, errors.New("name 不能为空"))
		return
	}
	if c.Group == "" {
		c.Group = "default"
	}
	if c.Method == "" {
		c.Method = "GET"
	}
	c.Method = strings.ToUpper(c.Method)
	if c.TimeoutMs < 0 {
		c.TimeoutMs = 0
	}
	if c.ID == "" {
		c.ID = newHTTPID()
	}
	c.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	httpCasesMu.Lock()
	f, err := s.loadHTTPCases()
	if err != nil {
		httpCasesMu.Unlock()
		writeErr(w, 500, err)
		return
	}
	// 覆盖同 id；同 group+name 视为覆盖
	replaced := false
	for i, ex := range f.Cases {
		if ex.ID == c.ID || (ex.Group == c.Group && ex.Name == c.Name) {
			c.ID = ex.ID // 保留原 id
			f.Cases[i] = c
			replaced = true
			break
		}
	}
	if !replaced {
		f.Cases = append(f.Cases, c)
	}
	if err := s.saveHTTPCases(f); err != nil {
		httpCasesMu.Unlock()
		writeErr(w, 500, err)
		return
	}
	httpCasesMu.Unlock()
	s.audit.Write("http.case.upsert", "group", c.Group, "name", c.Name)
	writeJSON(w, 200, map[string]any{"ok": true, "case": c, "replaced": replaced})
}

// DELETE /api/http/cases?id=xxx  → 按 id 删；不带 id 返回 400
func (s *Server) handleHTTPCasesDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeErr(w, 405, errors.New("仅支持 DELETE"))
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, 400, errors.New("id 不能为空"))
		return
	}
	httpCasesMu.Lock()
	f, err := s.loadHTTPCases()
	if err != nil {
		httpCasesMu.Unlock()
		writeErr(w, 500, err)
		return
	}
	idx := -1
	for i, c := range f.Cases {
		if c.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		httpCasesMu.Unlock()
		writeErr(w, 404, errors.New("用例不存在: "+id))
		return
	}
	f.Cases = append(f.Cases[:idx], f.Cases[idx+1:]...)
	if err := s.saveHTTPCases(f); err != nil {
		httpCasesMu.Unlock()
		writeErr(w, 500, err)
		return
	}
	httpCasesMu.Unlock()
	s.audit.Write("http.case.delete", "id", id)
	writeJSON(w, 200, map[string]any{"ok": true, "deleted": id})
}

// ---------- /api/http/envs（v0.7）----------

func (s *Server) handleHTTPEnvs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleHTTPEnvsList(w, r)
	case http.MethodPost:
		s.handleHTTPEnvsSave(w, r)
	default:
		writeErr(w, 405, errors.New("仅支持 GET / POST"))
	}
}

func (s *Server) handleHTTPEnvsList(w http.ResponseWriter, r *http.Request) {
	httpCasesMu.Lock()
	f, err := s.loadHTTPCases()
	httpCasesMu.Unlock()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if f.Envs == nil {
		f.Envs = []HTTPEnv{}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "envs": f.Envs})
}

func (s *Server) handleHTTPEnvsSave(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > 1*1024*1024 {
		writeErr(w, 400, errors.New("请求体太大（最大 1MB）"))
		return
	}
	var req struct {
		Envs []HTTPEnv `json:"envs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	// 校验：name 非空；同 name 覆盖
	cleaned := make([]HTTPEnv, 0, len(req.Envs))
	seen := map[string]bool{}
	for _, e := range req.Envs {
		e.Name = strings.TrimSpace(e.Name)
		if e.Name == "" {
			writeErr(w, 400, errors.New("env.name 不能为空"))
			return
		}
		if seen[e.Name] {
			writeErr(w, 400, errors.New("env.name 重复: "+e.Name))
			return
		}
		seen[e.Name] = true
		if e.Vars == nil {
			e.Vars = map[string]string{}
		}
		cleaned = append(cleaned, e)
	}
	httpCasesMu.Lock()
	f, err := s.loadHTTPCases()
	if err != nil {
		httpCasesMu.Unlock()
		writeErr(w, 500, err)
		return
	}
	f.Envs = cleaned
	if err := s.saveHTTPCases(f); err != nil {
		httpCasesMu.Unlock()
		writeErr(w, 500, err)
		return
	}
	httpCasesMu.Unlock()
	s.audit.Write("http.envs.save", "count", len(cleaned))
	writeJSON(w, 200, map[string]any{"ok": true, "envs": cleaned})
}

// ---------- 存储 ----------

func (s *Server) httpCasesPath() string {
	return filepath.Join(s.cur().DataDir(), "http_cases.json")
}

func (s *Server) loadHTTPCases() (httpCasesFile, error) {
	var f httpCasesFile
	path := s.httpCasesPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil // 空文件 = 空数据
		}
		return f, fmt.Errorf("读取 http_cases 失败: %w", err)
	}
	if len(data) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("http_cases JSON 解析失败: %w", err)
	}
	return f, nil
}

func (s *Server) saveHTTPCases(f httpCasesFile) error {
	path := s.httpCasesPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建 data 目录失败: %w", err)
	}
	encoded, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 http_cases 失败: %w", err)
	}
	tmpFile, err := os.CreateTemp(dir, ".http_cases-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmp := tmpFile.Name()
	if _, err := tmpFile.Write(encoded); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("原子替换 http_cases 失败: %w", err)
	}
	return nil
}

// newHTTPID 生成一个简短 ID（时间戳纳秒 + 4 字符十六进制随机）
func newHTTPID() string {
	now := time.Now().UnixNano()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("c%016x", now)
	}
	return fmt.Sprintf("c%016x%s", now, hex.EncodeToString(b[:]))
}
