package httpserver

import (
	"errors"
	"net/http"
	"strings"
)

// handleLogsDownloadEventsOrCancel 分发 events / cancel 子路径（对应 /api/logs/download/{id}/...）。
//
// 跟 /api/files/download/{id}/... 是同一套 dlmanager.Session 池，
// 唯一区别是 session.Kind = "logs"（用于 audit 区分）。SSE 流和 cancel
// 逻辑共用 handlers_files.go 里的 streamDownloadEvents / cancelDownload。
func (s *Server) handleLogsDownloadEventsOrCancel(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/logs/download/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	action := parts[1]
	switch action {
	case "events":
		s.streamDownloadEvents(w, r, id)
	case "cancel":
		s.cancelLogsDownload(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

// cancelLogsDownload 显式取消一个"按目录下载最近 N 个文件"任务。
//
// 跟 files 的 cancelDownload 区别：audit op 写 logs.download 而不是 files.download。
func (s *Server) cancelLogsDownload(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	if !s.downloads.Cancel(id) {
		writeErr(w, 404, errors.New("下载任务不存在"))
		return
	}
	s.audit.Write("logs.download", "id", id, "result", "ok", "stage", "cancel")
	writeJSON(w, 200, map[string]any{"ok": true, "id": id})
}
