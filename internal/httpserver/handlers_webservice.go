package httpserver

// ---------- WebService 调试中心 API（v0.12）----------
//
// 路由前缀：
//   - /api/wsdl/*     WSDL 导入 / 项目 CRUD
//   - /api/soap/*     SOAP 生成 / 发送 / 模板 / 历史 / Mock 配置
//   - /api/ws/xml/*   XML 格式化 / 压缩 / 校验
//
// 全部统一进 handleWSDispatch 按 path + method 分发。
//
// 安全：
//   - URL 导入有超时（http.Client.Timeout = 30s）；
//   - 文件导入只接受请求体里的 WSDL 文本，不直接读服务器磁盘任意路径；
//   - 所有响应通过 writeJSON 走 json.Encoder，自动 HTML 转义，避免 XSS；
//   - 历史记录可一键清空（DELETE /api/soap/history）。

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"kairo/internal/webservice"
)

var sharedWsTLSConfig = &tls.Config{InsecureSkipVerify: true}

// handleWSDispatch 把 /api/wsdl/ / /api/soap/ / /api/ws/xml/ 三类前缀再细分。
func (s *Server) handleWSDispatch(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	// ---------- WSDL ----------
	case path == "/api/wsdl/import-url":
		s.handleWSDLImportURL(w, r)
	case path == "/api/wsdl/import-file":
		s.handleWSDLImportFile(w, r)
	case path == "/api/wsdl/projects":
		s.handleWSDLProjects(w, r)
	case strings.HasPrefix(path, "/api/wsdl/projects/"):
		s.handleWSDLProjectByID(w, r) // GET / DELETE
	default:
		// 继续匹配 SOAP / XML
		s.handleWSDispatchSOAP(w, r)
	}
}

// handleWSDispatchSOAP 处理 /api/soap/* 与 /api/ws/xml/*。
func (s *Server) handleWSDispatchSOAP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	// ---------- SOAP 报文生成 / 发送 ----------
	case path == "/api/soap/generate":
		s.handleSOAPGenerate(w, r)
	case path == "/api/soap/send":
		s.handleSOAPSend(w, r)
	// ---------- 模板 ----------
	case path == "/api/soap/templates":
		s.handleSOAPTemplates(w, r) // GET 列表 / POST 新增或覆盖
	case strings.HasPrefix(path, "/api/soap/templates/"):
		s.handleSOAPTemplateByID(w, r) // PUT / DELETE
	// ---------- 历史 ----------
	case path == "/api/soap/history":
		s.handleSOAPHistory(w, r) // GET 列表 / DELETE 清空
	case strings.HasPrefix(path, "/api/soap/history/"):
		s.handleSOAPHistoryByID(w, r) // POST .../replay
	// ---------- Mock 配置 ----------
	case path == "/api/soap/mocks":
		s.handleSOAPMocks(w, r) // GET 列表 / POST 新增或覆盖
	case strings.HasPrefix(path, "/api/soap/mocks/"):
		// 区分 /api/soap/mocks/records 与 /api/soap/mocks/{id}
		// 用完整路径精确匹配 records，避免 ID 恰好为 "records" 时撞路由
		if path == "/api/soap/mocks/records" {
			s.handleSOAPMockRecords(w, r) // GET / DELETE
			return
		}
		s.handleSOAPMockByID(w, r) // PUT / DELETE
	// ---------- XML 辅助 ----------
	case path == "/api/ws/xml/format":
		s.handleWSXMLFormat(w, r)
	case path == "/api/ws/xml/minify":
		s.handleWSXMLMinify(w, r)
	case path == "/api/ws/xml/validate":
		s.handleWSXMLValidate(w, r)
	default:
		http.NotFound(w, r)
	}
}

// ---------- WSDL 导入 ----------

type wsdlImportURLReq struct {
	URL     string            `json:"url"`
	Name    string            `json:"name"` // 可选，用户命名
	Headers map[string]string `json:"headers,omitempty"`
}

// POST /api/wsdl/import-url  body: {url, name, headers}
// 拉取 WSDL URL（30s 超时），解析后返回 WSDLProject（未保存）。
func (s *Server) handleWSDLImportURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req wsdlImportURLReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	url := strings.TrimSpace(req.URL)
	if url == "" {
		writeErr(w, 400, errors.New("url 不能为空"))
		return
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		writeErr(w, 400, errors.New("url 必须以 http:// 或 https:// 开头"))
		return
	}
	if err := webservice.ValidateEndpointURL(url); err != nil {
		writeErr(w, 400, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("构造请求失败: %w", err))
		return
	}
	httpReq.Header.Set("User-Agent", "kairo-wsdl/0.1")
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	// WSDL URL 拉取：拒绝 link-local（含云元数据 169.254.169.254）/ unspecified / 组播，
	// 私有网段放行（内网 WebService 是核心场景）。DialContext 做拨号时二次校验
	// （防 DNS rebinding），重定向逐跳校验且跨源剥离凭据。
	//
	// 这里与外部 XSD 拉取共用 internal/webservice 的同一份重定向策略：可信基准是
	// 用户显式填写的根 URL 的源（凭据本来就该发给它），而不是上一跳；跨源时剥离
	// 全部用户传入头名加固定敏感头，程序设置的 User-Agent 等非敏感头保留。
	client := webservice.NewCredentialSafeHTTPClient(webservice.OriginOf(url), req.Headers)
	resp, err := client.Do(httpReq)
	if err != nil {
		writeErr(w, 502, fmt.Errorf("拉取 WSDL 失败: %w", err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		writeErr(w, 502, fmt.Errorf("WSDL URL 返回 %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)))
		return
	}
	// 限 4MB（WSDL 一般很小，4MB 足够）
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		writeErr(w, 502, fmt.Errorf("读取 WSDL 失败: %w", err))
		return
	}
	decoded, err := webservice.DecodeXMLBytes(raw, resp.Header.Get("Content-Type"))
	if err != nil {
		writeErr(w, 400, fmt.Errorf("WSDL 编码不兼容: %w", err))
		return
	}

	p := webservice.ParseWSDL(decoded, webservice.SchemaResolverConfig{
		BaseURI:     url,
		Context:     ctx,
		AuthHeaders: req.Headers,
	})
	p.Source = "url"
	p.SourceURL = url
	if req.Name != "" {
		p.Name = req.Name
	} else {
		p.Name = deriveWSDLProjectName(p, url)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "project": p})
}

// POST /api/wsdl/import-file  body: {content, name}
// 前端把上传的 .wsdl/.xsd 文件文本放进 content 字段。
// 不走 multipart：前端用 FileReader 读成字符串后 POST JSON 即可，简化处理。
func (s *Server) handleWSDLImportFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req struct {
		Content     string            `json:"content"`
		Name        string            `json:"name"`
		Attachments map[string]string `json:"attachments"`
	}
	// 单文件由前端限制为 4MB；为 WSDL + 多个外部 XSD 预留 20MB JSON 请求空间。
	if err := json.NewDecoder(io.LimitReader(r.Body, 20*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeErr(w, 400, errors.New("content 不能为空"))
		return
	}
	var p *webservice.WSDLProject
	if len(req.Attachments) > 0 {
		p = webservice.ParseWSDL(req.Content, req.Attachments)
	} else {
		p = webservice.ParseWSDL(req.Content)
	}
	p.Source = "upload"
	if req.Name != "" {
		p.Name = req.Name
	} else {
		p.Name = deriveWSDLProjectName(p, "uploaded.wsdl")
	}
	writeJSON(w, 200, map[string]any{"ok": true, "project": p})
}

// GET  /api/wsdl/projects           → 列表
// POST /api/wsdl/projects           → 新增或覆盖（body: {project: WSDLProject}）
func (s *Server) handleWSDLProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.ws.ListProjects()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if list == nil {
			list = []webservice.WSDLProject{}
		}
		writeJSON(w, 200, map[string]any{"ok": true, "projects": list})
	case http.MethodPost:
		var req struct {
			Project webservice.WSDLProject `json:"project"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024)).Decode(&req); err != nil {
			writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
			return
		}
		p := req.Project
		p.Version = webservice.DataVersion
		if p.Name == "" {
			p.Name = "未命名 WSDL"
		}
		saved, err := s.ws.SaveProject(p)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "project": saved})
	default:
		writeErr(w, 405, errors.New("仅支持 GET / POST"))
	}
}

// GET    /api/wsdl/projects/{id}  → 取单个
// DELETE /api/wsdl/projects/{id}  → 删除
func (s *Server) handleWSDLProjectByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/wsdl/projects/")
	if id == "" {
		writeErr(w, 400, errors.New("缺少 id"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, ok, err := s.ws.GetProject(id)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if !ok {
			writeErr(w, 404, errors.New("WSDL 项目不存在"))
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "project": p})
	case http.MethodDelete:
		deleted, err := s.ws.DeleteProject(id)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if !deleted {
			writeErr(w, 404, errors.New("WSDL 项目不存在"))
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 405, errors.New("仅支持 GET / DELETE"))
	}
}

// deriveWSDLProjectName 从 service 名 / URL 推导一个默认项目名。
func deriveWSDLProjectName(p *webservice.WSDLProject, fallback string) string {
	if len(p.Services) > 0 && p.Services[0].Name != "" {
		return p.Services[0].Name
	}
	// URL 最后一段
	if i := strings.LastIndex(fallback, "/"); i >= 0 && i+1 < len(fallback) {
		return fallback[i+1:]
	}
	return fallback
}

// ---------- SOAP 生成 ----------

type soapGenerateReq struct {
	Operation   webservice.Operation `json:"operation"`
	SOAPVersion string               `json:"soap_version"` // "1.1" / "1.2"
}

// POST /api/soap/generate  body: {operation, soap_version}
// 返回生成的 Envelope XML。
func (s *Server) handleSOAPGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req soapGenerateReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	env := webservice.GenerateEnvelope(req.Operation, req.SOAPVersion)
	writeJSON(w, 200, map[string]any{"ok": true, "envelope": env})
}

// ---------- SOAP 发送 ----------

// POST /api/soap/send  body: webservice.SendRequest + 可选 operation/save_history 字段
// 发送请求，把结果 + 建议关键词一起返回，并自动写一条历史。
func (s *Server) handleSOAPSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req struct {
		webservice.SendRequest
		Operation   string `json:"operation"`    // 可选，用于历史/关键词
		SaveHistory *bool  `json:"save_history"` // nil 表示未传，默认 true
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	// 默认保存历史
	// 优先级：JSON 显式值 > URL query > 默认 true
	saveHistory := true
	if req.SaveHistory != nil {
		saveHistory = *req.SaveHistory
	}
	if saveHistory && r.URL.Query().Get("save_history") == "0" {
		saveHistory = false
	}

	// 后端也做必填校验（前端已校验，但 API 直调需兜底）
	if strings.TrimSpace(req.Endpoint) == "" {
		writeErr(w, 400, errors.New("endpoint 不能为空"))
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeErr(w, 400, errors.New("请求 XML 不能为空"))
		return
	}

	resp := webservice.Send(req.SendRequest)

	// 建议搜索关键词（操作名 / SOAPAction / 请求体里的 trace 字段）
	keywords := webservice.SuggestLogKeywords(req.Operation, req.SOAPAction, req.Body)

	if saveHistory {
		entry := webservice.HistoryEntry{
			Version:      webservice.DataVersion,
			Endpoint:     req.Endpoint,
			Operation:    req.Operation,
			SOAPAction:   req.SOAPAction,
			SOAPVersion:  req.SOAPVersion,
			Encoding:     req.Encoding,
			TimeoutMs:    req.TimeoutMs,
			Headers:      req.Headers,
			RequestBody:  req.Body,
			ResponseBody: resp.Body,
			StatusCode:   resp.Status,
			DurationMs:   resp.ElapsedMs,
			Success:      resp.OK,
			Error:        resp.Error,
		}
		if _, err := s.ws.AppendHistory(entry); err != nil {
			s.audit.Write("webservice.history.save", "result", "fail", "op", "send",
				"endpoint", entry.Endpoint, "operation", entry.Operation, "err", err.Error())
		}
	}
	writeJSON(w, 200, map[string]any{
		"ok":       true,
		"response": resp,
		"keywords": keywords,
	})
}

// ---------- 模板 ----------

// GET  /api/soap/templates       → 列表
// POST /api/soap/templates       → 新增或覆盖（body: {template: Template}）
func (s *Server) handleSOAPTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.ws.ListTemplates()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if list == nil {
			list = []webservice.Template{}
		}
		writeJSON(w, 200, map[string]any{"ok": true, "templates": list})
	case http.MethodPost:
		var req struct {
			Template webservice.Template `json:"template"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024)).Decode(&req); err != nil {
			writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
			return
		}
		t := req.Template
		if strings.TrimSpace(t.Name) == "" {
			writeErr(w, 400, errors.New("模板 name 不能为空"))
			return
		}
		saved, _, err := s.ws.SaveTemplate(t)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "template": saved})
	default:
		writeErr(w, 405, errors.New("仅支持 GET / POST"))
	}
}

// PUT    /api/soap/templates/{id}  → 覆盖
// POST /api/soap/templates/{id}  → 兼容前端 POST 覆盖
// DELETE /api/soap/templates/{id}  → 删除
func (s *Server) handleSOAPTemplateByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/soap/templates/")
	if id == "" {
		writeErr(w, 400, errors.New("缺少 id"))
		return
	}
	switch r.Method {
	case http.MethodPut, http.MethodPost: // 兼容前端 POST 覆盖
		var req struct {
			Template webservice.Template `json:"template"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024)).Decode(&req); err != nil {
			writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
			return
		}
		t := req.Template
		t.ID = id
		if strings.TrimSpace(t.Name) == "" {
			writeErr(w, 400, errors.New("模板 name 不能为空"))
			return
		}
		saved, _, err := s.ws.SaveTemplate(t)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "template": saved})
	case http.MethodDelete:
		deleted, err := s.ws.DeleteTemplate(id)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if !deleted {
			writeErr(w, 404, errors.New("模板不存在"))
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 405, errors.New("仅支持 PUT / POST / DELETE"))
	}
}

// ---------- 历史 ----------

// GET    /api/soap/history  → 列表
// DELETE /api/soap/history  → 一键清空
func (s *Server) handleSOAPHistory(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.ws.ListHistory()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if list == nil {
			list = []webservice.HistoryEntry{}
		}
		writeJSON(w, 200, map[string]any{"ok": true, "history": list})
	case http.MethodDelete:
		if err := s.ws.ClearHistory(); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 405, errors.New("仅支持 GET / DELETE"))
	}
}

// POST /api/soap/history/{id}/replay → 取一条历史，重放（重新发送），并记一条新历史
// 路径形式固定为 /api/soap/history/{id}/replay
func (s *Server) handleSOAPHistoryByID(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(r.URL.Path, "/api/soap/history/")
	parts := strings.SplitN(sub, "/", 2)
	if len(parts) != 2 || parts[1] != "replay" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	h, ok, err := s.ws.GetHistory(id)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if !ok {
		writeErr(w, 404, errors.New("历史记录不存在"))
		return
	}
	resp := webservice.Send(webservice.SendRequest{
		Endpoint:    h.Endpoint,
		SOAPAction:  h.SOAPAction,
		SOAPVersion: h.SOAPVersion,
		Encoding:    h.Encoding,
		TimeoutMs:   h.TimeoutMs,
		Headers:     h.Headers,
		Body:        h.RequestBody,
	})
	keywords := webservice.SuggestLogKeywords(h.Operation, h.SOAPAction, h.RequestBody)
	newEntry := webservice.HistoryEntry{
		Version:      webservice.DataVersion,
		Endpoint:     h.Endpoint,
		Operation:    h.Operation,
		SOAPAction:   h.SOAPAction,
		SOAPVersion:  h.SOAPVersion,
		Encoding:     h.Encoding,
		TimeoutMs:    h.TimeoutMs,
		Headers:      h.Headers,
		RequestBody:  h.RequestBody,
		ResponseBody: resp.Body,
		StatusCode:   resp.Status,
		DurationMs:   resp.ElapsedMs,
		Success:      resp.OK,
		Error:        resp.Error,
	}
	if _, err := s.ws.AppendHistory(newEntry); err != nil {
		s.audit.Write("webservice.history.save", "result", "fail", "op", "replay",
			"endpoint", newEntry.Endpoint, "operation", newEntry.Operation, "err", err.Error())
	}
	writeJSON(w, 200, map[string]any{
		"ok":       true,
		"response": resp,
		"keywords": keywords,
	})
}

// ---------- Mock 配置 ----------

// GET  /api/soap/mocks  → 列表
// POST /api/soap/mocks  → 新增或覆盖（body: {mock: MockConfig}），保存后自动 Reload 路由
func (s *Server) handleSOAPMocks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.ws.ListMocks()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if list == nil {
			list = []webservice.MockConfig{}
		}
		writeJSON(w, 200, map[string]any{"ok": true, "mocks": list})
	case http.MethodPost:
		var req struct {
			Mock webservice.MockConfig `json:"mock"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024)).Decode(&req); err != nil {
			writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
			return
		}
		m := req.Mock
		if strings.TrimSpace(m.Name) == "" {
			writeErr(w, 400, errors.New("mock name 不能为空"))
			return
		}
		if strings.TrimSpace(m.Path) == "" {
			writeErr(w, 400, errors.New("mock path 不能为空"))
			return
		}
		saved, _, err := s.ws.SaveMock(m)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		// 动态生效
		_ = s.wsMocks.Reload()
		writeJSON(w, 200, map[string]any{"ok": true, "mock": saved})
	default:
		writeErr(w, 405, errors.New("仅支持 GET / POST"))
	}
}

// PUT    /api/soap/mocks/{id}  → 覆盖
// POST /api/soap/mocks/{id}  → 兼容前端 POST 覆盖
// DELETE /api/soap/mocks/{id}  → 删除
func (s *Server) handleSOAPMockByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/soap/mocks/")
	if id == "" {
		writeErr(w, 400, errors.New("缺少 id"))
		return
	}
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		var req struct {
			Mock webservice.MockConfig `json:"mock"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024)).Decode(&req); err != nil {
			writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
			return
		}
		m := req.Mock
		m.ID = id
		if strings.TrimSpace(m.Name) == "" {
			writeErr(w, 400, errors.New("mock name 不能为空"))
			return
		}
		if strings.TrimSpace(m.Path) == "" {
			writeErr(w, 400, errors.New("mock path 不能为空"))
			return
		}
		saved, _, err := s.ws.SaveMock(m)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		_ = s.wsMocks.Reload()
		writeJSON(w, 200, map[string]any{"ok": true, "mock": saved})
	case http.MethodDelete:
		deleted, err := s.ws.DeleteMock(id)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if !deleted {
			writeErr(w, 404, errors.New("mock 不存在"))
			return
		}
		_ = s.wsMocks.Reload()
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 405, errors.New("仅支持 PUT / DELETE"))
	}
}

// GET    /api/soap/mocks/records  → 最近 mock 请求记录
// DELETE /api/soap/mocks/records  → 清空记录
func (s *Server) handleSOAPMockRecords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.ws.ListMockRecords()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if list == nil {
			list = []webservice.MockRequestRecord{}
		}
		writeJSON(w, 200, map[string]any{"ok": true, "records": list})
	case http.MethodDelete:
		if err := s.ws.ClearMockRecords(); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 405, errors.New("仅支持 GET / DELETE"))
	}
}

// ---------- XML 辅助 ----------

type wsXMLReq struct {
	Input  string `json:"input"`
	Indent string `json:"indent"`
}

// POST /api/ws/xml/format
func (s *Server) handleWSXMLFormat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req wsXMLReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	out, err := webservice.FormatXML(req.Input, req.Indent)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "output": out})
}

// POST /api/ws/xml/minify
func (s *Server) handleWSXMLMinify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req wsXMLReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	out, err := webservice.MinifyXML(req.Input)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "output": out})
}

// POST /api/ws/xml/validate
func (s *Server) handleWSXMLValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req wsXMLReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	if err := webservice.ValidateXML(req.Input); err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
