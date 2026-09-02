package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"kairo/internal/sysutil"
)

// localRevealReq /api/local/reveal-file 的请求体
//
// v0.5-F P1-12：让前端能"在文件管理器里显示"下载好的日志，
// 用户体验：下完文件后 → 一键跳到 Finder/Explorer 看到它，
// 再拖到聊天工具/邮件，整个流程不用切窗口。
//
// 安全：path 必须以 cfg.DownloadDir() 为根（白名单），
// 或者以请求体里 `folder` 字段（即下载任务实际落地的目录）为根，
// 防止任意目录被 reveal。
type localRevealReq struct {
	Path   string `json:"path"`   // 绝对路径（前端从 Item.AbsPath 拿）
	Folder string `json:"folder"` // 可选：下载任务实际落地目录（自定义 target_dir 时非空）
}

// P0-4 修复：localReveal/localOpenFolder 原来只认 cfg.DownloadDir()，
// 用户用自定义 target_dir 下载后，"打开目录"会被 403 拒绝。
// 改为：允许 cfg.DownloadDir() 或请求体里 folder（下载任务实际目录）。
func allowAnyRoot(allowRoot, customFolder, target string) error {
	// 先试默认目录
	if err := openPathAllowed(allowRoot, target); err == nil {
		return nil
	}
	// 再试自定义目录（下载任务里用户指定的 target_dir）
	if customFolder != "" {
		if err := openPathAllowed(customFolder, target); err == nil {
			return nil
		}
	}
	// 都不行，返回默认目录的报错信息
	return openPathAllowed(allowRoot, target)
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
	// 路径白名单：默认 cfg.DownloadDir() + 自定义 folder（下载任务的实际目录）
	allowRoot := s.cur().DownloadDir()
	if err := allowAnyRoot(allowRoot, req.Folder, req.Path); err != nil {
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
	Path   string `json:"path"`   // 绝对目录路径
	Folder string `json:"folder"` // 可选：下载任务实际落地目录（自定义 target_dir 时非空）
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
	// 路径白名单：默认 cfg.DownloadDir() + 自定义 folder（P0-4 修复）
	allowRoot := s.cur().DownloadDir()
	if err := allowAnyRoot(allowRoot, req.Folder, req.Path); err != nil {
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

// localOpenWithReq /api/local/open-with 的请求体
//
// v0.8 新增：用用户配置的外部程序打开下载文件。
//   - opener：用户在 config.yaml external_openers 里配的 Name（不允许传路径！）
//   - name：下载文件名（可含日期子目录如 "20260624/app.log"）
//   - folder：可选，自定义下载目标目录（跟 reveal/open-folder 一致）
//
// 安全：
//   - opener 必须命中 cfg.App.ExternalOpeners 里的某条 → 防止任意程序执行；
//   - name 必须落在 cfg.DownloadDir()（或 folder）下 → 防止任意文件被打开；
//   - opener.path 直接 exec.Command(...).Start() 异步启动，不等返回（GUI 程序会阻塞）。
type localOpenWithReq struct {
	Opener string `json:"opener"` // external_openers 的 Name
	Name   string `json:"name"`   // 下载文件名（含日期子目录可）
	Folder string `json:"folder"` // 可选：自定义下载落点
}

func (s *Server) handleLocalOpenWith(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req localOpenWithReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	if strings.TrimSpace(req.Opener) == "" {
		writeErr(w, 400, errors.New("opener 不能为空"))
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, 400, errors.New("name 不能为空"))
		return
	}

	cur := s.cur()
	// 1) opener 必须白名单（这是最关键的安全闸）
	op := cur.App.FindOpener(req.Opener)
	if op == nil {
		writeErr(w, 404, fmt.Errorf("未找到打开器 %q（请先在系统配置里添加）", req.Opener))
		return
	}
	if strings.TrimSpace(op.Path) == "" {
		writeErr(w, 500, errors.New("打开器 path 为空（配置异常）"))
		return
	}

	// 2) name 反向防注入：拒绝 .. / 反斜杠 / NUL（同 open-dir）
	if strings.ContainsAny(req.Name, "\\\x00") || hasPathTraversal(req.Name) {
		writeErr(w, 400, errors.New("name 含非法字符"))
		return
	}

	// 3) 文件路径必须落在白名单目录（下载目录或自定义 folder）
	dlDir := cur.DownloadDir()
	absPath := filepath.Join(dlDir, req.Name)
	if !fileExists(absPath) {
		writeErr(w, 404, fmt.Errorf("下载文件不存在: %s", req.Name))
		return
	}
	// 路径白名单（兜底）：默认目录或自定义 folder 二选一
	allowRoot := cur.DownloadDir()
	if err := allowAnyRoot(allowRoot, req.Folder, absPath); err != nil {
		writeErr(w, 403, err)
		return
	}

	// 4) 启动外部程序（不等待返回）
	cmd := exec.Command(op.Path, absPath)
	sysutil.HideConsoleWindow(cmd) // 用户配置的 opener 一般是 GUI 程序（VSCode/Sublime/Notepad++ 等），加 CREATE_NO_WINDOW 更干净
	if err := cmd.Start(); err != nil {
		s.audit.Write("local.open_with", "opener", req.Opener, "name", req.Name, "result", "fail", "err", err.Error())
		writeErrSanitized(w, 500, fmt.Errorf("启动打开器 %q 失败: %v", req.Opener, err))
		return
	}
	go func() { _ = cmd.Wait() }()

	s.audit.Write("local.open_with", "opener", req.Opener, "name", req.Name, "result", "ok", "platform", runtime.GOOS)
	writeJSON(w, 200, map[string]any{
		"ok":     true,
		"opener": req.Opener,
		"path":   absPath,
		"cmd":    op.Path,
	})
}

type choosePathReq struct {
	Initial string `json:"initial"`
}

func (s *Server) handleChooseFile(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rv := recover(); rv != nil {
			writeErr(w, 500, fmt.Errorf("文件选择器异常: %v", rv))
		}
	}()
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req choosePathReq
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req)
	path, err := chooseFileAt(strings.TrimSpace(req.Initial))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if path == "" {
		writeJSON(w, 200, map[string]any{"ok": true, "path": ""})
		return
	}
	s.audit.Write("local.choose_file", "path", path, "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true, "path": path})
}

func (s *Server) handleChooseDir(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rv := recover(); rv != nil {
			writeErr(w, 500, fmt.Errorf("文件夹选择器异常: %v", rv))
		}
	}()
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req choosePathReq
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req)
	path, err := chooseDirAt(strings.TrimSpace(req.Initial))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if path == "" {
		writeJSON(w, 200, map[string]any{"ok": true, "path": ""})
		return
	}
	s.audit.Write("local.choose_dir", "path", path, "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true, "path": path})
}
