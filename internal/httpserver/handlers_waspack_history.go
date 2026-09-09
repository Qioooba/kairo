package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"kairo/internal/waspack"
)

type waspackHistorySummary struct {
	ID              string                   `json:"id"`
	CreatedAt       time.Time                `json:"created_at"`
	CompletedAt     time.Time                `json:"completed_at,omitempty"`
	Operation       string                   `json:"operation"`
	Status          string                   `json:"status"`
	PackageName     string                   `json:"package_name,omitempty"`
	OutputDir       string                   `json:"output_dir,omitempty"`
	Files           int                      `json:"files,omitempty"`
	Bytes           int64                    `json:"bytes,omitempty"`
	Warnings        []string                 `json:"warnings,omitempty"`
	Error           string                   `json:"error,omitempty"`
	Artifacts       []waspack.ArtifactDigest `json:"artifacts,omitempty"`
	SourceHistoryID string                   `json:"source_history_id,omitempty"`
}

func (s *Server) handleWASPackHistory(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	const prefix = "/api/waspack/history"
	suffix := strings.TrimPrefix(r.URL.Path, prefix)
	if suffix == "" || suffix == "/" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, errors.New("历史列表仅支持 GET"))
			return
		}
		s.handleWASPackHistoryList(w, r)
		return
	}
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	if len(parts) == 2 && parts[1] == "rebuild" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, errors.New("历史重建仅支持 POST"))
			return
		}
		s.handleWASPackHistoryRebuild(w, r, parts[0])
		return
	}
	if len(parts) != 1 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, errors.New("打包历史路径不存在"))
		return
	}
	id := parts[0]
	switch r.Method {
	case http.MethodGet:
		s.handleWASPackHistoryDetail(w, r, id)
	case http.MethodDelete:
		s.handleWASPackHistoryDelete(w, r, id)
	default:
		writeErr(w, http.StatusMethodNotAllowed, errors.New("历史详情仅支持 GET/DELETE"))
	}
}

func (s *Server) handleWASPackHistoryList(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeErr(w, http.StatusBadRequest, errors.New("limit 必须是正整数"))
			return
		}
		limit = parsed
	}
	records, err := s.waspackHistory.List(limit, r.URL.Query().Get("status"), r.URL.Query().Get("operation"))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "读取") || strings.Contains(err.Error(), "解析") || strings.Contains(err.Error(), "版本") {
			status = http.StatusInternalServerError
		}
		writeErr(w, status, err)
		return
	}
	items := make([]waspackHistorySummary, 0, len(records))
	for _, record := range records {
		items = append(items, historySummary(record))
	}
	s.audit.Write("waspack.history.list", "count", len(items), "result", "ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items, "records": items})
}

func (s *Server) handleWASPackHistoryDetail(w http.ResponseWriter, _ *http.Request, id string) {
	record, err := s.waspackHistory.Get(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, errors.New("打包历史不存在"))
			return
		}
		if strings.Contains(err.Error(), "非法") {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.audit.Write("waspack.history.detail", "history_id", id, "result", "ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "record": record})
}

func (s *Server) handleWASPackHistoryDelete(w http.ResponseWriter, _ *http.Request, id string) {
	if err := s.waspackHistory.Delete(id); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, errors.New("打包历史不存在"))
			return
		}
		if strings.Contains(err.Error(), "非法") {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.audit.Write("waspack.history.delete", "history_id", id, "result", "ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func historySummary(record waspack.HistoryRecord) waspackHistorySummary {
	return waspackHistorySummary{
		ID: record.ID, CreatedAt: record.CreatedAt, CompletedAt: record.CompletedAt,
		Operation: record.Operation, Status: record.Status,
		PackageName: record.Request.PackageName, OutputDir: record.Request.OutputDir,
		Files: record.Files, Bytes: record.Bytes,
		Warnings: append([]string(nil), record.Warnings...), Error: record.Error,
		Artifacts: append([]waspack.ArtifactDigest(nil), record.Artifacts...), SourceHistoryID: record.SourceHistoryID,
	}
}

type waspackRebuildReq struct {
	OutputDir      string `json:"output_dir"`
	OutputPolicy   string `json:"output_policy"`
	ConfirmReplace bool   `json:"confirm_replace"`
	ReplaceToken   string `json:"replace_token"`
	IncludeZip     *bool  `json:"include_zip"`
}

func (s *Server) handleWASPackHistoryRebuild(w http.ResponseWriter, r *http.Request, sourceID string) {
	if sourceID == "" || len(sourceID) > 160 || strings.ContainsAny(sourceID, "/\\\x00\r\n") {
		writeErr(w, http.StatusBadRequest, errors.New("invalid history ID"))
		return
	}
	source, err := s.waspackHistory.Get(sourceID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, errors.New("打包历史不存在"))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var body waspackRebuildReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, errors.New("请求体超过 32768 字节上限"))
		} else {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("请求体解析失败: %w", err))
		}
		return
	}
	snapshot := source.Request
	if strings.TrimSpace(snapshot.ProjectDir) == "" || strings.TrimSpace(snapshot.Manifest) == "" {
		writeErr(w, http.StatusConflict, errors.New("该历史缺少工程目录或投产清单，无法安全重建"))
		return
	}
	outputDir := strings.TrimSpace(body.OutputDir)
	if outputDir == "" {
		outputDir = snapshot.OutputDir
	}
	if outputDir == "" {
		writeErr(w, http.StatusConflict, errors.New("该历史缺少输出目录，请在本次请求中指定"))
		return
	}
	policy, policyErr := rebuildOutputPolicy(body.OutputPolicy)
	if policyErr != nil {
		writeErr(w, http.StatusBadRequest, policyErr)
		return
	}
	packReq := waspack.Request{
		ProjectDir: snapshot.ProjectDir, OutputDir: outputDir, PackageName: snapshot.PackageName,
		Manifest: snapshot.Manifest, AutoPair: snapshot.AutoPair, PackType: snapshot.PackType,
		BatchBaseDir: snapshot.BatchBaseDir, ChmodMode: snapshot.ChmodMode,
		OutputPolicy: policy, ConfirmReplace: body.ConfirmReplace,
		ReplaceToken: strings.TrimSpace(body.ReplaceToken),
		MetadataDir:  s.cur().DataDir(),
	}
	if policy == waspack.OutputPolicyReplace {
		if !body.ConfirmReplace {
			writeErr(w, http.StatusBadRequest, errors.New("本次重建覆盖输出目录需要 confirm_replace=true；不会继承历史确认"))
			return
		}
		if !s.waspackReplace.consume(outputDir, body.ReplaceToken) {
			writeErr(w, http.StatusBadRequest, errors.New("覆盖输出目录需要有效的一次性确认凭据"))
			return
		}
		packReq.ReplaceAuthorized = true
	}
	includeZip := snapshot.IncludeZip
	if body.IncludeZip != nil {
		includeZip = *body.IncludeZip
	}
	packReq.IncludeZip = includeZip
	started := time.Now().UTC()
	buildRes, buildErr := waspack.Build(packReq)
	if buildErr != nil {
		if historyErr := s.recordWASPack("rebuild", packReq, started, "failure", 0, 0, nil, nil, buildErr, source.ID); historyErr != nil {
			s.audit.Write("waspack.history.append", "operation", "rebuild", "status", "failure", "result", "fail", "error", trimHistoryError(historyErr.Error()))
		}
		writeErr(w, http.StatusBadRequest, buildErr)
		return
	}
	allWarnings := append([]string(nil), buildRes.Warnings...)
	allArtifacts := append([]waspack.ArtifactDigest(nil), buildRes.Artifacts...)
	files, bytes := buildRes.Files, buildRes.Bytes
	var zipRes *waspack.ZipResult
	if includeZip {
		zipRes, err = waspack.BuildZip(packReq)
		if err != nil {
			allWarnings = append(allWarnings, "ZIP 生成失败："+err.Error())
			if historyErr := s.recordWASPack("rebuild", packReq, started, "failure", files, bytes, allWarnings, allArtifacts, err, source.ID); historyErr != nil {
				s.audit.Write("waspack.history.append", "operation", "rebuild", "status", "failure", "result", "fail", "error", trimHistoryError(historyErr.Error()))
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "build": buildRes, "error": err.Error()})
			return
		}
		allWarnings = append(allWarnings, zipRes.Warnings...)
		allArtifacts = append(allArtifacts, zipRes.Artifacts...)
		files += zipRes.Files
		bytes += zipRes.Bytes
	}
	if historyErr := s.recordWASPack("rebuild", packReq, started, "success", files, bytes, allWarnings, allArtifacts, nil, source.ID); historyErr != nil {
		buildRes.Warnings = append(buildRes.Warnings, "打包历史写入失败："+historyErr.Error())
	}
	s.audit.Write("waspack.history.rebuild", "history_id", source.ID, "output", outputDir, "include_zip", includeZip, "result", "ok")
	if zipRes != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "source_history_id": source.ID, "build": buildRes, "zip": zipRes})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "source_history_id": source.ID, "build": buildRes})
}

func rebuildOutputPolicy(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return waspack.OutputPolicyFail, nil
	}
	switch raw {
	case waspack.OutputPolicyFail, waspack.OutputPolicyCleanOwned, waspack.OutputPolicyReplace:
		return raw, nil
	default:
		return "", errors.New("output_policy 只能是 fail、clean_kairo_artifacts 或 replace")
	}
}
