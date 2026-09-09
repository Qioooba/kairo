package webservice

// 持久化存储：WSDL 项目 / 模板 / 历史 / Mock 配置 / Mock 请求记录。
//
// 全部存到 data 目录下的 JSON 单文件，原子写（temp + rename），权限 0600。
// 不依赖数据库，保持单 exe 部署。
//
// 文件布局：
//   data/wsdl_projects.json   — []WSDLProject
//   data/soap_templates.json  — []Template
//   data/soap_history.json    — []HistoryEntry（默认最多 500 条）
//   data/soap_mocks.json      — []MockConfig
//   data/soap_mock_records.json — []MockRequestRecord（最多 200 条）

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxHistoryEntries 历史记录默认上限。
const MaxHistoryEntries = 500

// MaxMockRecords Mock 请求记录上限。
const MaxMockRecords = 200

const storeFileVersion = 1

// Store 线程安全的本地存储。
type Store struct {
	dir string

	mu        sync.Mutex
	projects  []WSDLProject
	templates []Template
	history   []HistoryEntry
	mocks     []MockConfig
	mockRecs  []MockRequestRecord
}

// NewStore 构造一个指向 dataDir 的 Store。数据按需 lazy 加载。
func NewStore(dataDir string) *Store {
	return &Store{dir: dataDir}
}

// ---------- 通用加载/保存 ----------

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

func loadJSON[T any](path string, out *T) (err error) {
	defer func() {
		if err != nil {
			var zero T
			*out = zero
		}
	}()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		// v0.17 及更早版本直接保存数组。
		return json.Unmarshal(trimmed, out)
	}
	var envelope struct {
		Version int             `json:"version"`
		Items   json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return err
	}
	if envelope.Version < 1 || envelope.Version > storeFileVersion {
		return fmt.Errorf("不支持的数据文件版本 %d（当前仅支持 %d）", envelope.Version, storeFileVersion)
	}
	if len(envelope.Items) == 0 || string(envelope.Items) == "null" {
		return nil
	}
	return json.Unmarshal(envelope.Items, out)
}

func saveCachedJSON[T any](path string, cache *[]T) error {
	if err := saveJSONAtomic(path, *cache); err != nil {
		*cache = nil
		return err
	}
	return nil
}

func saveJSONAtomic(path string, v any) error {
	// Check the durable file even if the store already has a warm cache.
	// A failed read or a newer format must never turn into an empty overwrite.
	var existing json.RawMessage
	if err := loadJSON(path, &existing); err != nil {
		return fmt.Errorf("现有数据不可读取，拒绝覆盖: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	envelope := struct {
		Version int `json:"version"`
		Items   any `json:"items"`
	}{Version: storeFileVersion, Items: v}
	encoded, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".ws-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("同步临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("收紧权限失败: %w", err)
	}
	// Windows 下杀毒/备份软件可能短暂锁住目标文件，os.Rename 会失败。
	// 短延迟重试 3 次（共 ~600ms）规避这个常见问题。Linux/Mac 上首次成功不触发重试。
	if err := renameWithRetry(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("原子替换失败: %w", err)
	}
	return nil
}

// renameWithRetry 包装 os.Rename，失败时短延迟重试（Windows 杀软锁定场景）。
// 非 Windows 上首次成功不触发重试。
func renameWithRetry(src, dst string) error {
	const maxAttempts = 3
	delay := 100 * time.Millisecond
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if err := os.Rename(src, dst); err == nil {
			return nil
		} else {
			lastErr = err
			// 非 Windows 上非临时错误直接退出
			if runtime.GOOS != "windows" && !isTransientRenameErr(err) {
				return err
			}
		}
		if i < maxAttempts-1 {
			time.Sleep(delay)
			delay *= 2
		}
	}
	return lastErr
}

// isTransientRenameErr 简单判断：Windows 上 ACCESS_DENIED / SHARING_VIOLATION 视为临时。
// 其他平台默认非临时。
func isTransientRenameErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Windows 常见：The process cannot access the file because it is being used by another process.
	return strings.Contains(msg, "being used by another process") ||
		strings.Contains(msg, "Access is denied")
}

// NewID 生成简短 ID（时间戳纳秒 + 4 字节随机十六进制）。
func NewID() string {
	now := time.Now().UnixNano()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("ws%016x", now)
	}
	return fmt.Sprintf("ws%016x%s", now, hex.EncodeToString(b[:]))
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// ---------- WSDL 项目 ----------

func (s *Store) projectsPath() string { return s.path("wsdl_projects.json") }

// ListProjects 返回所有项目（按更新时间倒序）。
func (s *Store) ListProjects() ([]WSDLProject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projects == nil {
		s.projects = []WSDLProject{}
		if err := loadJSON(s.projectsPath(), &s.projects); err != nil {
			return nil, err
		}
	}
	out := make([]WSDLProject, len(s.projects))
	copy(out, s.projects)
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

// GetProject 按 ID 返回单个项目。
func (s *Store) GetProject(id string) (*WSDLProject, bool, error) {
	all, err := s.ListProjects()
	if err != nil {
		return nil, false, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], true, nil
		}
	}
	return nil, false, nil
}

// SaveProject 新增或覆盖（同 ID 覆盖）。返回已赋 ID/时间戳的项目副本。
func (s *Store) SaveProject(p WSDLProject) (WSDLProject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projects == nil {
		s.projects = []WSDLProject{}
		if err := loadJSON(s.projectsPath(), &s.projects); err != nil {
			return p, err
		}
	}
	if p.ID == "" {
		p.ID = NewID()
	}
	if p.CreatedAt == "" {
		p.CreatedAt = nowRFC3339()
	}
	p.UpdatedAt = nowRFC3339()
	replaced := false
	for i := range s.projects {
		if s.projects[i].ID == p.ID {
			p.CreatedAt = s.projects[i].CreatedAt
			s.projects[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		s.projects = append(s.projects, p)
	}
	return p, saveCachedJSON(s.projectsPath(), &s.projects)
}

// DeleteProject 按 ID 删除。
func (s *Store) DeleteProject(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projects == nil {
		s.projects = []WSDLProject{}
		if err := loadJSON(s.projectsPath(), &s.projects); err != nil {
			return false, err
		}
	}
	idx := -1
	for i := range s.projects {
		if s.projects[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, nil
	}
	s.projects = append(s.projects[:idx], s.projects[idx+1:]...)
	return true, saveCachedJSON(s.projectsPath(), &s.projects)
}

// ---------- 模板 ----------

func (s *Store) templatesPath() string { return s.path("soap_templates.json") }

func (s *Store) ListTemplates() ([]Template, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.templates == nil {
		s.templates = []Template{}
		if err := loadJSON(s.templatesPath(), &s.templates); err != nil {
			return nil, err
		}
	}
	out := make([]Template, len(s.templates))
	copy(out, s.templates)
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

// SaveTemplate 新增或覆盖（同 ID 覆盖；同 group+name 视为覆盖）。
func (s *Store) SaveTemplate(t Template) (Template, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.templates == nil {
		s.templates = []Template{}
		if err := loadJSON(s.templatesPath(), &s.templates); err != nil {
			return t, false, err
		}
	}
	if t.ID == "" {
		t.ID = NewID()
	}
	t.Version = DataVersion
	if t.CreatedAt == "" {
		t.CreatedAt = nowRFC3339()
	}
	t.UpdatedAt = nowRFC3339()
	replaced := false
	for i := range s.templates {
		// 同 ID 总是覆盖。
		// 同 group+name 也覆盖 —— 但仅当 group 非空时；group 为空时两个无名分组
		// 的同名模板不应互踩（避免用户留空 group 导致意外覆盖）。
		nameMatch := s.templates[i].Name == t.Name
		groupMatch := t.Group != "" && s.templates[i].Group == t.Group
		if s.templates[i].ID == t.ID || (nameMatch && groupMatch) {
			t.ID = s.templates[i].ID
			t.CreatedAt = s.templates[i].CreatedAt
			s.templates[i] = t
			replaced = true
			break
		}
	}
	if !replaced {
		s.templates = append(s.templates, t)
	}
	return t, replaced, saveCachedJSON(s.templatesPath(), &s.templates)
}

// DeleteTemplate 按 ID 删除。
func (s *Store) DeleteTemplate(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.templates == nil {
		s.templates = []Template{}
		if err := loadJSON(s.templatesPath(), &s.templates); err != nil {
			return false, err
		}
	}
	idx := -1
	for i := range s.templates {
		if s.templates[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, nil
	}
	s.templates = append(s.templates[:idx], s.templates[idx+1:]...)
	return true, saveCachedJSON(s.templatesPath(), &s.templates)
}

// ---------- 历史 ----------

func (s *Store) historyPath() string { return s.path("soap_history.json") }

func (s *Store) ListHistory() ([]HistoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.history == nil {
		s.history = []HistoryEntry{}
		if err := loadJSON(s.historyPath(), &s.history); err != nil {
			return nil, err
		}
	}
	out := make([]HistoryEntry, len(s.history))
	copy(out, s.history)
	// 倒序：最新的在前
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// AppendHistory 追加一条历史，并按 MaxHistoryEntries 截断。
func (s *Store) AppendHistory(h HistoryEntry) (HistoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.history == nil {
		s.history = []HistoryEntry{}
		if err := loadJSON(s.historyPath(), &s.history); err != nil {
			return h, err
		}
	}
	if h.ID == "" {
		h.ID = NewID()
	}
	h.Version = DataVersion
	if h.Time == "" {
		h.Time = nowRFC3339()
	}
	s.history = append(s.history, h)
	// 超过上限截断最旧的（保留最后 MaxHistoryEntries 条）
	if len(s.history) > MaxHistoryEntries {
		s.history = s.history[len(s.history)-MaxHistoryEntries:]
	}
	return h, saveCachedJSON(s.historyPath(), &s.history)
}

// GetHistory 按 ID 取一条历史。
func (s *Store) GetHistory(id string) (*HistoryEntry, bool, error) {
	all, err := s.ListHistory()
	if err != nil {
		return nil, false, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], true, nil
		}
	}
	return nil, false, nil
}

// ClearHistory 清空全部历史。
func (s *Store) ClearHistory() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = []HistoryEntry{}
	return saveCachedJSON(s.historyPath(), &s.history)
}

// ---------- Mock 配置 ----------

func (s *Store) mocksPath() string { return s.path("soap_mocks.json") }

func (s *Store) ListMocks() ([]MockConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mocks == nil {
		s.mocks = []MockConfig{}
		if err := loadJSON(s.mocksPath(), &s.mocks); err != nil {
			return nil, err
		}
	}
	out := make([]MockConfig, len(s.mocks))
	copy(out, s.mocks)
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

// SaveMock 新增或覆盖 mock 配置。
func (s *Store) SaveMock(m MockConfig) (MockConfig, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mocks == nil {
		s.mocks = []MockConfig{}
		if err := loadJSON(s.mocksPath(), &s.mocks); err != nil {
			return m, false, err
		}
	}
	if m.ID == "" {
		m.ID = NewID()
	}
	m.Version = DataVersion
	if m.StatusCode <= 0 {
		m.StatusCode = 200
	}
	if m.CreatedAt == "" {
		m.CreatedAt = nowRFC3339()
	}
	m.UpdatedAt = nowRFC3339()
	replaced := false
	for i := range s.mocks {
		if s.mocks[i].ID == m.ID {
			m.CreatedAt = s.mocks[i].CreatedAt
			s.mocks[i] = m
			replaced = true
			break
		}
	}
	if !replaced {
		s.mocks = append(s.mocks, m)
	}
	return m, replaced, saveCachedJSON(s.mocksPath(), &s.mocks)
}

// DeleteMock 按 ID 删除。
func (s *Store) DeleteMock(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mocks == nil {
		s.mocks = []MockConfig{}
		if err := loadJSON(s.mocksPath(), &s.mocks); err != nil {
			return false, err
		}
	}
	idx := -1
	for i := range s.mocks {
		if s.mocks[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, nil
	}
	s.mocks = append(s.mocks[:idx], s.mocks[idx+1:]...)
	return true, saveCachedJSON(s.mocksPath(), &s.mocks)
}

// ---------- Mock 请求记录 ----------

func (s *Store) mockRecordsPath() string { return s.path("soap_mock_records.json") }

func (s *Store) ListMockRecords() ([]MockRequestRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mockRecs == nil {
		s.mockRecs = []MockRequestRecord{}
		if err := loadJSON(s.mockRecordsPath(), &s.mockRecs); err != nil {
			return nil, err
		}
	}
	out := make([]MockRequestRecord, len(s.mockRecs))
	copy(out, s.mockRecs)
	// 倒序：最新的在前
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// AppendMockRecord 追加一条 mock 请求记录，按 MaxMockRecords 截断。
func (s *Store) AppendMockRecord(r MockRequestRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mockRecs == nil {
		s.mockRecs = []MockRequestRecord{}
		if err := loadJSON(s.mockRecordsPath(), &s.mockRecs); err != nil {
			return err
		}
	}
	if r.Time == "" {
		r.Time = nowRFC3339()
	}
	s.mockRecs = append(s.mockRecs, r)
	if len(s.mockRecs) > MaxMockRecords {
		s.mockRecs = s.mockRecs[len(s.mockRecs)-MaxMockRecords:]
	}
	return saveCachedJSON(s.mockRecordsPath(), &s.mockRecs)
}

// ClearMockRecords 清空 mock 请求记录。
func (s *Store) ClearMockRecords() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mockRecs = []MockRequestRecord{}
	return saveCachedJSON(s.mockRecordsPath(), &s.mockRecs)
}
