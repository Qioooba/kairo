package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"ops-toolbox/internal/downloads"
)

// - system / server 可选；只过滤元数据中匹配的（不会真的去访问 SSH）
// - 返回 {count, total_bytes, files: [Entry...]}
func (s *Server) handleDownloadsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	q := r.URL.Query()
	filterSys := strings.TrimSpace(q.Get("system"))
	filterSrv := strings.TrimSpace(q.Get("server"))

	entries, err := downloads.List(s.cur().DownloadDir())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	out := make([]map[string]any, 0, len(entries))
	var total int64
	for _, e := range entries {
		if filterSys != "" && e.Meta.System != "" && e.Meta.System != filterSys {
			continue
		}
		if filterSrv != "" && e.Meta.Server != "" && e.Meta.Server != filterSrv {
			continue
		}
		total += e.Size
		row := map[string]any{
			"name":         e.Name,
			"size":         e.Size,
			"size_human":   humanBytes(e.Size),
			"mod_time":     e.ModTime.Format("2006-01-02 15:04:05"),
			"kind":         e.Kind,
			"meta_present": e.MetaPresent,
			"server":       e.Meta.Server,
			"host":         e.Meta.Host,
			"dir":          e.Meta.Dir,
			"dir_alias":    e.Meta.DirAlias,
			"encoding":     e.Meta.Encoding,
		}
		if !e.Meta.DownloadedAt.IsZero() {
			row["downloaded_at"] = e.Meta.DownloadedAt.Format("2006-01-02 15:04:05")
		}
		// file（远端原始名）或 files（zip 的内容列表）
		if e.Kind == "zip" {
			row["files"] = e.Meta.Files
		} else {
			row["file"] = e.Meta.File
		}
		out = append(out, row)
	}
	writeJSON(w, 200, map[string]any{
		"files":       out,
		"count":       len(out),
		"total_bytes": total,
		"total_human": humanBytes(total),
		"folder":      s.cur().DownloadDir(),
	})
}

// handleDownloadsItem 路由分发：
//   - DELETE /api/downloads/{name}  — 删除单个文件
//   - POST   /api/downloads/all    — 清空所有
func (s *Server) handleDownloadsItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/downloads/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	// 清空全部
	if rest == "all" {
		if r.Method != http.MethodPost {
			writeErr(w, 405, errors.New("仅支持 POST"))
			return
		}
		n, err := downloads.DeleteAll(s.cur().DownloadDir())
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		s.audit.Write("downloads.clear", "result", "ok", "count", n)
		writeJSON(w, 200, map[string]any{"ok": true, "deleted": n})
		return
	}
	// 单个删除
	if r.Method != http.MethodDelete {
		writeErr(w, 405, errors.New("仅支持 DELETE"))
		return
	}
	if err := downloads.Delete(s.cur().DownloadDir(), rest); err != nil {
		writeErr(w, 400, err)
		return
	}
	s.audit.Write("downloads.delete", "result", "ok", "name", rest)
	writeJSON(w, 200, map[string]any{"ok": true})
}
