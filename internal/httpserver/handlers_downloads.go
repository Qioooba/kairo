package httpserver

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
		writeErrSanitized(w, 500, err)
		return
	}
	out := make([]map[string]any, 0, len(entries))
	var total int64
	for _, e := range entries {
		if filterSys != "" && e.Meta.System != filterSys {
			continue
		}
		if filterSrv != "" && e.Meta.Server != filterSrv {
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
			"system":       e.Meta.System,
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
//   - DELETE /api/downloads/{name}              — 删除单个文件
//   - POST   /api/downloads/all                 — 清空所有
//   - POST   /api/downloads/{name}/open-dir     — 在文件管理器里 reveal 文件（B3）
//   - POST   /api/downloads/open-dir?name=...   — v0.5 起新增：name 含 "/" 时走 query，
//     避免 path 段 "/" 被前面 open-dir 拒。
func (s *Server) handleDownloadsItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/downloads/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	// 子路径分发：{name}/open-dir  或  open-dir?name=...（v0.5 新接口）
	if strings.HasSuffix(rest, "/open-dir") || rest == "open-dir" {
		// 兼容 v0.5 新接口 /api/downloads/open-dir?name=...（用于 name 含 "/" 时）
		var name string
		if strings.HasSuffix(rest, "/open-dir") {
			name = strings.TrimSuffix(rest, "/open-dir")
		} else {
			name = r.URL.Query().Get("name")
		}
		if name == "" || name == "all" {
			writeErr(w, 400, errors.New("open-dir 需要指定文件名"))
			return
		}
		s.handleDownloadsOpenDir(w, r, name)
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
			writeErrSanitized(w, 500, err)
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

// handleDownloadsOpenDir 在系统文件管理器里 reveal 一个已下载的文件（B3 新功能）。
//
// 路由：POST /api/downloads/{name}/open-dir
//
// v0.5 起 name 可走子目录（如 "20260624/app.log"），由前端用 query 参数 ?name=...
// 传入，避免 path 段 "/" 被这里判为非法字符。
//
// 行为：
//   - macOS → Finder 里 reveal 并选中（open -R）；
//   - Windows → Explorer 里 reveal 并选中（explorer.exe /select,...）；
//   - Linux → 用 xdg-open 打开父目录（Linux 文件管理器没统一 reveal 协议）。
//
// 安全：
//   - name 必须来自 List 返回的合法文件名；
//   - 拼出绝对路径后做 openPathAllowed 校验：必须落在 cfg.DownloadDir() 下，
//     否则 403 拒绝，避免越界 reveal 系统目录。
//
// 失败模式：
//   - 文件不存在（用户先在 Finder 里删了）→ 404；
//   - 越界 → 403；
//   - exec.Command.Start 报错（命令缺失 / 权限） → 500；
//   - 命令 fork 后不等返回，避免 GUI 程序阻塞请求。
func (s *Server) handleDownloadsOpenDir(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	// name 反向防注入：拒绝 .. / 反斜杠 / NUL。
	// 允许 "/" 因为 v0.5 起支持按日期子目录（"20260624/app.log"），但限制不能含穿越片段 ".."。
	if strings.ContainsAny(name, "\\\x00") || hasPathTraversal(name) {
		writeErr(w, 400, errors.New("name 含非法字符"))
		return
	}
	dlDir := s.cur().DownloadDir()
	absPath := filepath.Join(dlDir, name)
	// 文件存在性检查（不存在 → 404，避免 reveal 一个空路径触发奇怪行为）
	if !fileExists(absPath) {
		writeErr(w, 404, fmt.Errorf("下载文件不存在: %s", name))
		return
	}
	// 越界检查
	if err := openPathAllowed(dlDir, absPath); err != nil {
		s.audit.Write("downloads.open_dir", "name", name, "result", "fail", "reason", err.Error())
		writeErr(w, 403, err)
		return
	}
	// 调平台命令
	if err := revealInFileManager(absPath); err != nil {
		s.audit.Write("downloads.open_dir", "name", name, "result", "fail", "err", err.Error())
		writeErrSanitized(w, 500, err)
		return
	}
	s.audit.Write("downloads.open_dir", "name", name, "result", "ok", "platform", runtime.GOOS)
	writeJSON(w, 200, map[string]any{"ok": true, "platform": runtime.GOOS, "path": absPath})
}

// fileExists 简单存在性检查（不区分 file/dir；reveal 一个空目录也行）。
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
