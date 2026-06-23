package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// localRevealReq /api/local/reveal-file 的请求体
//
// v0.5-F P1-12：让前端能"在文件管理器里显示"下载好的日志，
// 用户体验：下完文件后 → 一键跳到 Finder/Explorer 看到它，
// 再拖到聊天工具/邮件，整个流程不用切窗口。
//
// 安全：path 必须以 cfg.DownloadDir() 为根（白名单），防止任意目录被 reveal。
type localRevealReq struct {
	Path string `json:"path"` // 绝对路径（前端从 Item.AbsPath 拿）
}

func (s *Server) handleLocalReveal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req localRevealReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeErr(w, 400, errors.New("path 不能为空"))
		return
	}
	// 路径白名单：必须以 cfg.DownloadDir() 为根
	allowRoot := s.cur().DownloadDir()
	if err := openPathAllowed(allowRoot, req.Path); err != nil {
		writeErr(w, 403, err)
		return
	}
	if err := revealInFileManager(req.Path); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("local.reveal", "path", req.Path, "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true, "path": req.Path})
}

// localOpenFolderReq /api/local/open-folder 的请求体
//
// 打开目录（不选中文件）；用于"下完一组文件后 → 打开当天的 downloads/YYYYMMDD 目录"。
type localOpenFolderReq struct {
	Path string `json:"path"` // 绝对目录路径
}

func (s *Server) handleLocalOpenFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req localOpenFolderReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeErr(w, 400, errors.New("path 不能为空"))
		return
	}
	// 路径白名单
	allowRoot := s.cur().DownloadDir()
	if err := openPathAllowed(allowRoot, req.Path); err != nil {
		writeErr(w, 403, err)
		return
	}
	// revealInFileManager 选的是文件；目录的话要调 open/xdg-open 父目录。
	// 直接复用 revealInFileManager：Linux 会 fallback 到 xdg-open dir，
	// macOS open -R <dir> 行为：Finder 高亮该目录。
	if err := revealInFileManager(req.Path); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("local.open_folder", "path", req.Path, "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true, "path": req.Path})
}
