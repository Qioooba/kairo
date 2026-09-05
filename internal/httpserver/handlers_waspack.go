package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kairo/internal/waspack"
)

type waspackReq struct {
	ProjectDir     string `json:"project_dir"`
	OutputDir      string `json:"output_dir"`
	PackageName    string `json:"package_name"`
	Manifest       string `json:"manifest"`
	AutoPair       *bool  `json:"auto_pair"`
	OutputPolicy   string `json:"output_policy"`
	ConfirmReplace bool   `json:"confirm_replace"`
	PackType       string `json:"pack_type"`
	BatchBaseDir   string `json:"batch_base_dir"`
	ChmodMode      string `json:"chmod_mode"`
	IncludeZip     bool   `json:"include_zip"`
	StageToken     string `json:"stage_token"`
}

func (s *Server) handleWASPackPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	req, err := decodeWASPackReq(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	pv, err := waspack.PreviewRequest(s.waspackRequest(req))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.audit.Write("waspack.preview", "project", req.ProjectDir, "files", len(pv.Files), "missing", len(pv.Missing), "result", "ok")
	writeJSON(w, 200, map[string]any{
		"ok":       true,
		"files":    pv.Files,
		"missing":  pv.Missing,
		"warnings": pv.Warnings,
		"stats":    pv.Stats,
	})
}

func (s *Server) handleWASPackBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	started := time.Now().UTC()
	req, err := decodeWASPackReq(r)
	if err != nil {
		s.recordWASPackFailure("build", s.waspackRequest(req), started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	packReq := s.waspackRequest(req)
	if strings.TrimSpace(req.OutputDir) == "" {
		err := errors.New("请填写要生成的文件夹路径")
		s.recordWASPackFailure("build", packReq, started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	res, err := waspack.Build(packReq)
	if err != nil {
		s.recordWASPackFailure("build", packReq, started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	if historyErr := s.recordWASPack("build", packReq, started, "success", res.Files, res.Bytes, res.Warnings, res.Artifacts, nil, ""); historyErr != nil {
		res.Warnings = append(res.Warnings, "打包历史写入失败："+historyErr.Error())
	}
	if req.IncludeZip {
		zipReq := packReq
		zipStarted := time.Now().UTC()
		zipRes, zipErr := waspack.BuildZip(zipReq)
		if zipErr != nil {
			s.recordWASPackFailure("zip", zipReq, zipStarted, zipErr, nil, nil)
			res.Warnings = append(res.Warnings, "ZIP 生成失败："+zipErr.Error())
			s.audit.Write("waspack.build", "project", req.ProjectDir, "output", res.OutputDir, "files", res.Files, "result", "partial", "zip_error", zipErr.Error())
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "build": res, "error": zipErr.Error()})
			return
		}
		if historyErr := s.recordWASPack("zip", zipReq, zipStarted, "success", zipRes.Files, zipRes.Bytes, zipRes.Warnings, zipRes.Artifacts, nil, ""); historyErr != nil {
			zipRes.Warnings = append(zipRes.Warnings, "打包历史写入失败："+historyErr.Error())
		}
		s.audit.Write("waspack.build", "project", req.ProjectDir, "output", res.OutputDir, "tar", res.TarFile, "zip", zipRes.ZipFile, "files", res.Files, "result", "ok")
		writeJSON(w, 200, map[string]any{"ok": true, "build": res, "zip": zipRes})
		return
	}
	s.audit.Write("waspack.build", "project", req.ProjectDir, "output", res.OutputDir, "tar", res.TarFile, "files", res.Files, "result", "ok")
	writeJSON(w, 200, res)
}

func (s *Server) handleWASPackExtract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	started := time.Now().UTC()
	req, err := decodeWASPackReq(r)
	if err != nil {
		s.recordWASPackFailure("extract", s.waspackRequest(req), started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	packReq := s.waspackRequest(req)
	if strings.TrimSpace(req.OutputDir) == "" {
		err := errors.New("请选择目标目录")
		s.recordWASPackFailure("extract", packReq, started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	res, err := waspack.Extract(packReq)
	if err != nil {
		s.recordWASPackFailure("extract", packReq, started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	if historyErr := s.recordWASPack("extract", packReq, started, "success", res.Files, res.Bytes, res.Warnings, nil, nil, ""); historyErr != nil {
		res.Warnings = append(res.Warnings, "打包历史写入失败："+historyErr.Error())
	}
	s.audit.Write("waspack.extract", "project", req.ProjectDir, "output", res.OutputDir, "war", res.WarDir, "files", res.Files, "result", "ok")
	writeJSON(w, 200, res)
}

func (s *Server) handleWASPackPackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	started := time.Now().UTC()
	req, err := decodeWASPackReq(r)
	if err != nil {
		s.recordWASPackFailure("package", s.waspackRequest(req), started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	packReq := s.waspackRequest(req)
	if req.OutputDir == "" {
		err := errors.New("请选择目标目录")
		s.recordWASPackFailure("package", packReq, started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(req.StageToken) == "" {
		err := errors.New("缺少 WAR 抽取阶段凭据，请重新抽取")
		s.recordWASPackFailure("package", packReq, started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	res, err := waspack.PackageExtracted(packReq)
	if err != nil {
		s.recordWASPackFailure("package", packReq, started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	if historyErr := s.recordWASPack("package", packReq, started, "success", res.Files, res.Bytes, res.Warnings, res.Artifacts, nil, ""); historyErr != nil {
		res.Warnings = append(res.Warnings, "打包历史写入失败："+historyErr.Error())
	}
	s.audit.Write("waspack.package", "output", res.OutputDir, "war", res.WarDir, "tar", res.TarFile, "files", res.Files, "result", "ok")
	writeJSON(w, 200, res)
}

func (s *Server) handleWASPackZip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	started := time.Now().UTC()
	req, err := decodeWASPackPayload(r)
	if err != nil {
		s.recordWASPackFailure("zip", s.waspackRequest(req), started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	req.OutputDir, req.PackageName = strings.TrimSpace(req.OutputDir), strings.TrimSpace(req.PackageName)
	packReq := s.waspackRequest(req)
	if req.OutputDir == "" {
		err := errors.New("请选择目标目录")
		s.recordWASPackFailure("zip", packReq, started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	if req.PackageName == "" {
		err := errors.New("请填写包名（ZIP 名与顶层文件夹名）")
		s.recordWASPackFailure("zip", packReq, started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	res, err := waspack.BuildZip(packReq)
	if err != nil {
		s.recordWASPackFailure("zip", packReq, started, err, nil, nil)
		writeErr(w, 400, err)
		return
	}
	if historyErr := s.recordWASPack("zip", packReq, started, "success", res.Files, res.Bytes, res.Warnings, res.Artifacts, nil, ""); historyErr != nil {
		res.Warnings = append(res.Warnings, "打包历史写入失败："+historyErr.Error())
	}
	s.audit.Write("waspack.zip", "output", res.OutputDir, "war", res.WarDir, "zip", res.ZipFile, "files", res.Files, "result", "ok")
	writeJSON(w, 200, res)
}

type waspackOpenReq struct {
	OutputDir string `json:"output_dir"`
}

func (s *Server) handleWASPackOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req waspackOpenReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	dir := strings.TrimSpace(req.OutputDir)
	if dir == "" {
		writeErr(w, 400, errors.New("请先填写打包文件夹路径"))
		return
	}
	if strings.ContainsAny(dir, "\x00\n\r") {
		writeErr(w, 400, errors.New("路径含非法字符"))
		return
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("路径无效: %w", err))
		return
	}
	st, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, 400, errors.New("打包文件夹还不存在，请先生成投产包"))
			return
		}
		writeErr(w, 400, err)
		return
	}
	if st.Mode()&os.ModeSymlink != 0 {
		writeErr(w, 400, errors.New("拒绝打开符号链接"))
		return
	}
	if !st.IsDir() {
		writeErr(w, 400, errors.New("路径不是文件夹"))
		return
	}
	if err := openFolderInFileManager(abs); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("waspack.open", "path", abs, "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true, "path": abs})
}

func decodeWASPackReq(r *http.Request) (waspackReq, error) {
	req, err := decodeWASPackPayload(r)
	if err != nil {
		return req, err
	}
	req.ProjectDir = strings.TrimSpace(req.ProjectDir)
	req.OutputDir = strings.TrimSpace(req.OutputDir)
	req.PackageName = strings.TrimSpace(req.PackageName)
	if req.ProjectDir == "" {
		return req, errors.New("请选择本地工程目录")
	}
	if strings.TrimSpace(req.Manifest) == "" {
		return req, errors.New("请粘贴投产清单")
	}
	return req, nil
}

func decodeWASPackPayload(r *http.Request) (waspackReq, error) {
	var req waspackReq
	const overhead = 16 * 1024
	const maxBody = waspack.MaxManifestBytes + overhead
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return req, fmt.Errorf("请求体读取失败: %w", err)
	}
	if len(raw) > maxBody {
		return req, fmt.Errorf("请求体超过 %d 字节上限", maxBody)
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, fmt.Errorf("请求体解析失败: %w", err)
	}
	return req, nil
}

func toWASPackReq(req waspackReq) waspack.Request {
	auto := true
	if req.AutoPair != nil {
		auto = *req.AutoPair
	}
	packType := strings.ToLower(strings.TrimSpace(req.PackType))
	if packType == "" {
		packType = "app"
	}
	batchBaseDir := strings.TrimSpace(req.BatchBaseDir)
	if batchBaseDir == "" {
		batchBaseDir = "/batch/credit"
	}
	return waspack.Request{
		ProjectDir:     req.ProjectDir,
		OutputDir:      req.OutputDir,
		PackageName:    req.PackageName,
		Manifest:       req.Manifest,
		AutoPair:       auto,
		OutputPolicy:   strings.TrimSpace(req.OutputPolicy),
		ConfirmReplace: req.ConfirmReplace,
		PackType:       packType,
		BatchBaseDir:   batchBaseDir,
		ChmodMode:      strings.TrimSpace(req.ChmodMode),
		IncludeZip:     req.IncludeZip,
		StageToken:     strings.TrimSpace(req.StageToken),
	}
}

func (s *Server) waspackRequest(req waspackReq) waspack.Request {
	out := toWASPackReq(req)
	if s != nil && s.cfg != nil {
		out.MetadataDir = s.cur().DataDir()
	}
	return out
}

// recordWASPack appends one audit record for a mutating operation. Error text
// is intentionally bounded and contains no request body or file contents.
func (s *Server) recordWASPack(operation string, req waspack.Request, started time.Time, status string, files int, bytes int64, warnings []string, artifacts []waspack.ArtifactDigest, operationErr error, sourceID string) error {
	if s == nil || s.waspackHistory == nil {
		return errors.New("打包历史存储未初始化")
	}
	record := waspack.NewHistoryRecord(operation, req)
	record.Status = status
	record.CreatedAt = started
	record.CompletedAt = time.Now().UTC()
	record.Files = files
	record.Bytes = bytes
	record.Warnings = append([]string(nil), warnings...)
	record.Artifacts = append([]waspack.ArtifactDigest(nil), artifacts...)
	record.SourceHistoryID = sourceID
	if operationErr != nil {
		record.Error = trimHistoryError(operationErr.Error())
	}
	if err := s.waspackHistory.Append(record); err != nil {
		return err
	}
	s.audit.Write("waspack.history.append", "operation", operation, "status", status, "history_id", record.ID, "result", "ok")
	return nil
}

func (s *Server) recordWASPackFailure(operation string, req waspack.Request, started time.Time, operationErr error, artifacts []waspack.ArtifactDigest, warnings []string) {
	if err := s.recordWASPack(operation, req, started, "failure", 0, 0, warnings, artifacts, operationErr, ""); err != nil {
		s.audit.Write("waspack.history.append", "operation", operation, "status", "failure", "result", "fail", "error", trimHistoryError(err.Error()))
	}
}

func trimHistoryError(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2000 {
		return raw[:2000] + "…"
	}
	return raw
}
