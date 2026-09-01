package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"kairo/internal/webservice"
	"kairo/internal/wscodegen"
)

const maxFetchedWSDLBytes = 4 * 1024 * 1024

func (s *Server) handleWSCodegenDispatch(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/api/wscodegen/engines" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"ok": true, "engines": wscodegen.Profiles()})
	case path == "/api/wscodegen/detect-jdk" && r.Method == http.MethodPost:
		s.handleWSCodegenDetectJDK(w, r)
	case path == "/api/wscodegen/scan-project" && r.Method == http.MethodPost:
		s.handleWSCodegenScanProject(w, r)
	case path == "/api/wscodegen/preview" && r.Method == http.MethodPost:
		s.handleWSCodegenGenerate(w, r, true)
	case path == "/api/wscodegen/generate" && r.Method == http.MethodPost:
		s.handleWSCodegenGenerate(w, r, false)
	default:
		writeErr(w, 404, errors.New("未知 wscodegen 接口"))
	}
}

func (s *Server) handleWSCodegenDetectJDK(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JDKHome string `json:"jdk_home"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	list := wscodegen.DetectJDKs(req.JDKHome)
	s.audit.Write("wscodegen.detect_jdk", "count", fmt.Sprintf("%d", len(list)), "user_home", req.JDKHome)
	writeJSON(w, 200, map[string]any{"ok": true, "jdks": list})
}

func (s *Server) handleWSCodegenScanProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectDir string `json:"project_dir"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	if strings.TrimSpace(req.ProjectDir) == "" {
		writeErr(w, 400, errors.New("project_dir 不能为空"))
		return
	}
	res, err := wscodegen.ScanProject(req.ProjectDir)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.audit.Write("wscodegen.scan_project", "dir", req.ProjectDir, "suggested", res.SuggestedEngine, "jars", fmt.Sprintf("%d", len(res.Jars)))
	writeJSON(w, 200, map[string]any{"ok": true, "scan": res})
}

func (s *Server) handleWSCodegenGenerate(w http.ResponseWriter, r *http.Request, forcePreview bool) {
	var req wscodegen.Request
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	if forcePreview {
		req.DryRun = true
	}
	if strings.TrimSpace(req.WSDLURL) != "" && strings.TrimSpace(req.WSDLContent) == "" && strings.TrimSpace(req.WSDLFile) == "" && strings.TrimSpace(req.WSDLProjectID) == "" {
		raw, err := s.fetchWSDLURL(r, req.WSDLURL)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		req.WSDLContent = raw
	}
	res, err := wscodegen.GenerateContext(r.Context(), req, s.ws)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if req.OpenAfter && !req.DryRun && res.OutputDir != "" {
		_ = revealInFileManager(res.OutputDir)
	}
	// 预览时截断超大文件，避免把整个工程 stub 塞进 JSON。
	if req.DryRun && len(res.Files) > 40 {
		res.Files = res.Files[:40]
		res.Warnings = append(res.Warnings, "预览只返回前 40 个文件")
	}
	for i := range res.Files {
		if len(res.Files[i].Content) > 120_000 {
			res.Files[i].Content = res.Files[i].Content[:120000] + "\n/* truncated */\n"
		}
	}
	action := "wscodegen.generate"
	if req.DryRun {
		action = "wscodegen.preview"
	}
	s.audit.Write(action, "engine", res.Engine, "mode", res.Mode, "files", fmt.Sprintf("%d", len(res.Files)), "out", res.OutputDir)
	writeJSON(w, 200, map[string]any{"ok": true, "result": res})
}

func (s *Server) fetchWSDLURL(r *http.Request, rawURL string) (string, error) {
	url := strings.TrimSpace(rawURL)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", errors.New("url 必须以 http:// 或 https:// 开头")
	}
	if err := webservice.ValidateEndpointURL(url); err != nil {
		return "", err
	}
	ctx := r.Context()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("User-Agent", "kairo-wscodegen/0.1")
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:       sharedWsTLSConfig,
			ResponseHeaderTimeout: 25 * time.Second,
			DialContext:           webservice.SafeDialContext,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return webservice.ValidateEndpointURL(req.URL.String())
		},
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("拉取 WSDL 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("WSDL URL 返回 %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchedWSDLBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxFetchedWSDLBytes {
		return "", fmt.Errorf("WSDL 响应超过 %d MiB 上限", maxFetchedWSDLBytes/(1024*1024))
	}
	decoded, err := webservice.DecodeXMLBytes(raw, resp.Header.Get("Content-Type"))
	if err != nil {
		return string(raw), nil
	}
	return decoded, nil
}
