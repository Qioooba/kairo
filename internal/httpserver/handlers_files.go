package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ops-toolbox/internal/config"
	"ops-toolbox/internal/dlmanager"
	"ops-toolbox/internal/downloads"
	"ops-toolbox/internal/sftpclient"
	"ops-toolbox/internal/sshclient"
)

// sftpDialer 把 SSH 连接变成 SFTP 客户端。
//
// 生产 = sftpclient.NewAuto：优先 SFTP，失败 fallback 到 shell 命令（项 8）；
// 测试可替换。
// 用包级变量而不是 Server 字段，是为了让 handler 直接拿到（不需要传遍所有调用点），
// 且 override 仅在测试代码里发生。
//
// runFn 是 shell backend 用的命令执行器，等价于 cli.Run 的封装。
// cli 已经传进来这里只是为了拿 RawConn + 提供 Run；直接闭包 cli.Run 即可。
var sftpDialer = func(cli *sshclient.Client) (sftpClientLike, error) {
	runFn := func(ctx context.Context, command string, timeout time.Duration, encoding string) (string, string, int, error) {
		stdout, stderr, code, err := cli.Run(ctx, command, timeout, encoding)
		return stdout, stderr, code, err
	}
	return sftpclient.NewAuto(cli.RawConn(), runFn)
}

// sftpClientLike 是 *sftpclient.Client 的最小接口（让 handler 不直接依赖具体类型，
// 便于测试替换）。
type sftpClientLike interface {
	Close() error
	ReadDir(path string) ([]os.FileInfo, error)
	Stat(path string) (os.FileInfo, error)
	DownloadFile(remotePath, localPath string) (int64, error)
	DownloadFileWithProgress(remotePath, localPath string, progress func(written, total int64)) (int64, error)
}

// ============================================================================
// v0.3：文件浏览器（任意路径浏览 + 下载，按 SSH 账号权限放行）
// ============================================================================

// filesListReq 列目录请求体
//
// v0.5 起支持多服务器（修复 #7 "多服务器只展示一台"）：
//   - 单服务器模式（向后兼容）：填 `server` + `path` 字段
//   - 多服务器共享 path 模式：填 `servers []string` + `path`（项 7 主推荐）
//   - 多服务器独立 path 模式：填 `targets []` 数组（每项 {server, path}）
//   - 优先级：targets > servers > server
type filesListReq struct {
	System   string            `json:"system"`
	Server   string            `json:"server"`
	Servers  []string          `json:"servers"` // v0.5 新增：多 server 共享 path
	Targets  []filesListTarget `json:"targets"` // v0.5 新增：多 server 多 path
	Username string            `json:"username"`
	Password string            `json:"password"`
	Path     string            `json:"path"` // 必须以 "/" 开头的绝对路径
}

// filesListTarget 一个 (server, path) 列表目标
type filesListTarget struct {
	Server string `json:"server"`
	Path   string `json:"path"`
}

// filesEntry 目录条目（用于前端表格）
type filesEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"isDir"`
	Mode  string `json:"mode"`  // 例如 "drwxr-xr-x"，便于 UI 显示
	MTime string `json:"mtime"` // RFC3339
}

// filesListServerResult 多服务器列目录时单台结果
type filesListServerResult struct {
	Server  string       `json:"server"`
	Host    string       `json:"host,omitempty"`
	Path    string       `json:"path,omitempty"`
	Parent  string       `json:"parent,omitempty"`
	OK      bool         `json:"ok"`
	Error   string       `json:"error,omitempty"`
	Entries []filesEntry `json:"entries,omitempty"`
	Count   int          `json:"count"`
	Ms      int64        `json:"elapsed_ms"`
}

// handleFilesList 列远端目录（任意路径）。
//
// 与 /api/logs/list 的差别：
//   - 不做白名单校验，按用户 SSH 账号的实际权限放行（v0.3 自由模式）；
//   - 返回完整目录条目（含 size/mode/mtime），UI 可直接做表格；
//   - parent 字段给出"上一级"绝对路径，没有则 null（根目录就是 null）。
//
// v0.5 起支持多服务器（修复 #7 "多服务器只展示一台"）：
//   - 单服务器模式：请求体带 `server` + `path` 字段 → 响应 {path, parent, entries}（向后兼容）
//   - 多服务器共用 path 模式：请求体带 `servers []string` + `path` → 响应 {servers: [...], ok_count, fail_count}
//   - 多服务器每台独立 path 模式：请求体带 `targets []`（每项 {server, path}）→ 响应 {servers: [...], ok_count, fail_count}
//   - 三模式互斥：targets 优先 > servers+path > 单 server；都为空则 400。
//
// 多服务器用 worker pool 并发（最多 4 路上限，5+ 台也只跑 4 路），每台独立 SSH / 独立 SFTP / 独立超时。
// 一台失败不影响其他台 —— 失败的 server 在结果里 `ok: false` + error 字符串。
func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	// 文件浏览器（任意路径下载）开关检查：
	// 配置里显式 enable_free_file_browser: false 时，整个 /api/files/* 拒绝服务。
	if !s.cur().App.FreeFileBrowserEnabled() {
		writeErr(w, 403, errors.New("文件浏览器（任意路径下载）已在配置中关闭 (app.enable_free_file_browser=false)"))
		return
	}
	var req filesListReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}

	// 决定模式：targets > servers > server（向后兼容）
	type plan struct {
		server string
		path   string
	}
	var plans []plan
	switch {
	case len(req.Targets) > 0:
		// 多服务器 + 每 target 独立 path
		seen := make(map[string]bool, len(req.Targets))
		for _, tg := range req.Targets {
			sn := strings.TrimSpace(tg.Server)
			pp := strings.TrimSpace(tg.Path)
			if sn == "" || pp == "" {
				writeErr(w, 400, errors.New("targets 每项必须有 server + path"))
				return
			}
			if !strings.HasPrefix(pp, "/") {
				writeErr(w, 400, fmt.Errorf("path 必须是绝对路径: %q", pp))
				return
			}
			key := sn + "\x00" + pp
			if seen[key] {
				continue
			}
			seen[key] = true
			plans = append(plans, plan{server: sn, path: pp})
		}
	case len(req.Servers) > 0:
		// 多服务器共享 path（项 7 主推荐用法）
		if req.Path == "" {
			writeErr(w, 400, errors.New("path 不能为空"))
			return
		}
		if !strings.HasPrefix(req.Path, "/") {
			writeErr(w, 400, errors.New("path 必须是绝对路径（以 / 开头）"))
			return
		}
		seen := make(map[string]bool, len(req.Servers))
		for _, sn := range req.Servers {
			sn = strings.TrimSpace(sn)
			if sn == "" {
				continue
			}
			if seen[sn] {
				continue
			}
			seen[sn] = true
			plans = append(plans, plan{server: sn, path: req.Path})
		}
		if len(plans) == 0 {
			writeErr(w, 400, errors.New("servers 不能全为空"))
			return
		}
	default:
		// 单服务器模式（向后兼容）
		sn := strings.TrimSpace(req.Server)
		if sn == "" {
			writeErr(w, 400, errors.New("server 不能为空（多服务器请填 servers 或 targets）"))
			return
		}
		if req.Path == "" {
			writeErr(w, 400, errors.New("path 不能为空"))
			return
		}
		if !strings.HasPrefix(req.Path, "/") {
			writeErr(w, 400, errors.New("path 必须是绝对路径（以 / 开头）"))
			return
		}
		plans = append(plans, plan{server: sn, path: req.Path})
	}

	// 项 14：free_file_roots 白名单检查（仅 list 时校验；download 也复用同一逻辑）。
	// 多服务器模式：每个 path 都要在白名单里（任意一个不通过就 403 整个请求）
	cur := s.cur()
	for _, p := range plans {
		if !cur.App.FreeFileRootsEnabled(p.path) {
			writeErr(w, 403, fmt.Errorf("path %q 不在 app.free_file_roots 白名单中", p.path))
			return
		}
	}

	// 解析 server 配置（找不到的 server → 错误结果，不让整请求 400）
	type job struct {
		idx    int
		server string
		path   string
		srv    *config.ServerConfig
	}
	results := make([]filesListServerResult, len(plans))
	jobs := make([]job, 0, len(plans))
	for i, p := range plans {
		_, sc, ok := cur.FindServer(req.System, p.server)
		if !ok {
			results[i] = filesListServerResult{
				Server: p.server,
				Path:   filepath.ToSlash(filepath.Clean(p.path)),
				OK:     false,
				Error:  "系统或服务器不存在",
				Count:  0,
				Ms:     0,
			}
			continue
		}
		results[i] = filesListServerResult{Server: p.server, Host: sc.Host, Path: filepath.ToSlash(filepath.Clean(p.path)), OK: true}
		jobs = append(jobs, job{idx: i, server: p.server, path: p.path, srv: sc})
	}

	// 单服务器模式（向后兼容）：保持原响应结构（{path, parent, entries}）
	if len(plans) == 1 && len(req.Targets) == 0 && len(req.Servers) == 0 {
		if !results[0].OK {
			s.audit.Write("files.list", "system", req.System, "server", req.Server, "path", req.Path, "result", "fail", "err", results[0].Error)
			writeErr(w, 400, errors.New(results[0].Error))
			return
		}
		entry := jobs[0].srv
		sn := jobs[0].server
		creds, err := s.resolveCreds(req.Username, req.Password, req.System, sn, entry.Username)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if creds.Password == "" {
			writeErr(w, 400, errors.New("缺少密码（输入或勾选「记住密码」）"))
			return
		}
		es, err := s.listOneServer(r.Context(), sn, entry, creds.Username, creds.Password, req.Path)
		if err != nil {
			s.audit.Write("files.list", "system", req.System, "server", sn, "path", req.Path, "result", "fail", "err", err.Error())
			writeErrSanitized(w, 502, err)
			return
		}
		cleaned := filepath.ToSlash(filepath.Clean(req.Path))
		parent := ""
		if cleaned != "/" && cleaned != "." {
			parent = filepath.ToSlash(filepath.Dir(cleaned))
		}
		s.audit.Write("files.list", "system", req.System, "server", sn, "path", req.Path, "result", "ok", "count", len(es))
		writeJSON(w, 200, map[string]any{
			"path":    cleaned,
			"parent":  parent,
			"entries": es,
		})
		return
	}

	// 多服务器模式：worker pool 并发（最多 4 路），每路跑 listOneServer
	jobCh := make(chan job, len(jobs))
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)

	concurrency := 4
	if concurrency > len(jobs) {
		concurrency = len(jobs)
	}
	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				sn := j.server
				entry := j.srv
				pp := j.path
				// 每台独立 resolve creds：username/password 可走默认（不填时用 server.Username）
				creds, err := s.resolveCreds(req.Username, req.Password, req.System, sn, entry.Username)
				if err != nil {
					results[j.idx] = filesListServerResult{Server: sn, Host: entry.Host, Path: filepath.ToSlash(filepath.Clean(pp)), OK: false, Error: err.Error(), Count: 0}
					continue
				}
				if creds.Password == "" {
					results[j.idx] = filesListServerResult{Server: sn, Host: entry.Host, Path: filepath.ToSlash(filepath.Clean(pp)), OK: false, Error: "缺少密码（输入或勾选「记住密码」）", Count: 0}
					continue
				}
				start := time.Now()
				es, err := s.listOneServer(r.Context(), sn, entry, creds.Username, creds.Password, pp)
				ms := time.Since(start).Milliseconds()
				cleaned := filepath.ToSlash(filepath.Clean(pp))
				parent := ""
				if cleaned != "/" && cleaned != "." {
					parent = filepath.ToSlash(filepath.Dir(cleaned))
				}
				if err != nil {
					results[j.idx] = filesListServerResult{Server: sn, Host: entry.Host, Path: cleaned, Parent: parent, OK: false, Error: err.Error(), Count: 0, Ms: ms}
					s.audit.Write("files.list", "system", req.System, "server", sn, "path", pp, "result", "fail", "err", err.Error())
					continue
				}
				results[j.idx] = filesListServerResult{Server: sn, Host: entry.Host, Path: cleaned, Parent: parent, OK: true, Entries: es, Count: len(es), Ms: ms}
				s.audit.Write("files.list", "system", req.System, "server", sn, "path", pp, "result", "ok", "count", len(es))
			}
		}()
	}
	wg.Wait()

	// 统计
	okCount, failCount, totalCount := 0, 0, 0
	for _, res := range results {
		if res.OK {
			okCount++
			totalCount += res.Count
		} else {
			failCount++
		}
	}

	writeJSON(w, 200, map[string]any{
		"servers":     results,
		"ok_count":    okCount,
		"fail_count":  failCount,
		"total_count": totalCount,
	})
}

// listOneServer 实际 SSH→SFTP→ReadDir 一台 server 的目录。
//
// 把 list 的核心逻辑抽出来，单服务器 / 多服务器 handler 都共用。
// 返回 entries（可能为空），错误时返回 err。审计在 caller 写。
func (s *Server) listOneServer(
	parentCtx context.Context,
	serverName string,
	srv *config.ServerConfig,
	username, password, path string,
) ([]filesEntry, error) {
	ctx, cancel := context.WithTimeout(parentCtx, sshDialOuterTimeout)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
	}, sshclient.Credentials{Password: password}, sshAttemptTimeout)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer cli.Close()

	sftpCli, err := sftpDialer(cli)
	if err != nil {
		return nil, fmt.Errorf("SFTP 打开失败: %w", err)
	}
	defer sftpCli.Close()

	infos, err := sftpCli.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("列出目录失败: %w", err)
	}

	entries := make([]filesEntry, 0, len(infos))
	for _, info := range infos {
		entries = append(entries, filesEntry{
			Name:  info.Name(),
			Size:  info.Size(),
			IsDir: info.IsDir(),
			Mode:  info.Mode().String(),
			MTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return entries, nil
}

// filesDownloadReq 下载请求体（任意路径）
type filesDownloadReq struct {
	System    string   `json:"system"`
	Server    string   `json:"server"`
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	Paths     []string `json:"paths"`                // 完整远端路径（绝对路径）
	Zip       bool     `json:"zip"`                  // 多文件时是否额外打 zip
	TargetDir string   `json:"target_dir,omitempty"` // v0.5 项 18：自定义本地落点（绝对路径），空 = 用 cfg.DownloadDir()
}

// filesDownloadLimits 下载任务的硬约束（避免误操作 / 连接卡死）
const (
	filesMaxFilesPerTask = 100              // 单次最多 100 个文件
	filesDownloadTimeout = 30 * time.Minute // 单个下载任务总超时（dlmanager.Session idleGC 也是 30min）
)

// validateTargetDir 校验并解析 target_dir（v0.5 项 18：自定义下载落点）。
//
// 返回：
//   - "" + nil：入参为空 → 调用方用 cfg.DownloadDir() 兜底
//   - 绝对路径 + nil：入参合法（已 mkdir -p，已写探针确认可写，已返回绝对路径）
//   - "" + error：入参不合法
//
// 校验要点：
//   - 必须是绝对路径（不接受相对路径，避免歧义）；
//   - 不含 NUL/换行/回车（防命令注入 / 日志伪造）；
//   - 已存在且是文件 → 400 "不是目录"（避免 MkdirAll 报一个让前端困惑的 errno 错误）；
//   - 不存在则 os.MkdirAll 创建；
//   - 写一个临时探针文件确认目录可写（写完即删，不留垃圾）。
//
// 设计动机：用户可能在 UI 选了 "/Users/me/Desktop/今天下载"，该目录通常
// 已存在；但脚本批量调用时也可能传新建路径。一律 mkdir -p + 写探针，
// 既兜底又防"目录存在但不可写"的隐蔽失败。
func validateTargetDir(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.ContainsAny(raw, "\x00\n\r") {
		return "", errors.New("target_dir 含非法控制字符")
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("target_dir 必须是绝对路径: %q", raw)
	}
	// 显式先 Stat：存在且是文件 → 直接报"不是目录"（避免 MkdirAll 抛晦涩 errno）
	if st, err := os.Stat(raw); err == nil {
		if !st.IsDir() {
			return "", fmt.Errorf("target_dir %q 不是目录", raw)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("无法访问 target_dir %q: %w", raw, err)
	}
	if err := os.MkdirAll(raw, 0o755); err != nil {
		return "", fmt.Errorf("无法创建 target_dir %q: %w", raw, err)
	}
	probe := filepath.Join(raw, ".ops-toolbox-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return "", fmt.Errorf("target_dir %q 不可写: %w", raw, err)
	}
	_ = os.Remove(probe)
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("target_dir 解析失败: %w", err)
	}
	return abs, nil
}

// handleFilesDownload 启动一个"任意路径下载"任务，立即返回 id。
//
// 路径不做白名单校验（v0.3 自由模式）；只校验：
//   - 每个 path 非空、以 "/" 开头；
//   - 不含 NUL/换行等控制字符；
//   - 总数 ≤ filesMaxFilesPerTask；
//
// 真实访问控制由 SSH 服务器端承担。
//
// 下载机制复用 dlmanager.Session + dlmanager.Manager（ID 全局唯一）。
func (s *Server) handleFilesDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	// 文件浏览器（任意路径下载）开关检查（同 handleFilesList）
	if !s.cur().App.FreeFileBrowserEnabled() {
		writeErr(w, 403, errors.New("文件浏览器（任意路径下载）已在配置中关闭 (app.enable_free_file_browser=false)"))
		return
	}
	var req filesDownloadReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if len(req.Paths) == 0 {
		writeErr(w, 400, errors.New("paths 不能为空"))
		return
	}
	if len(req.Paths) > filesMaxFilesPerTask {
		writeErr(w, 400, fmt.Errorf("单次最多下载 %d 个文件", filesMaxFilesPerTask))
		return
	}
	for _, p := range req.Paths {
		if p == "" {
			writeErr(w, 400, errors.New("path 不能为空"))
			return
		}
		if !strings.HasPrefix(p, "/") {
			writeErr(w, 400, fmt.Errorf("path 必须是绝对路径: %q", p))
			return
		}
		if strings.ContainsAny(p, "\x00\n\r") {
			writeErr(w, 400, fmt.Errorf("path 含非法字符: %q", p))
			return
		}
		// 项 14：free_file_roots 白名单检查
		if !s.cur().App.FreeFileRootsEnabled(p) {
			writeErr(w, 403, fmt.Errorf("path %q 不在 app.free_file_roots 白名单中", p))
			return
		}
	}
	_, srv, ok := s.cur().FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return
	}
	creds, err := s.resolveCreds(req.Username, req.Password, req.System, req.Server, srv.Username)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if creds.Password == "" {
		writeErr(w, 400, errors.New("缺少密码（输入或勾选「记住密码」）"))
		return
	}

	// v0.5 项 18：解析 target_dir（空 → 用 cfg.DownloadDir() 兜底）
	resolvedTarget, err := validateTargetDir(req.TargetDir)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	downloadRoot := resolvedTarget
	if downloadRoot == "" {
		downloadRoot = s.cur().DownloadDir()
	}

	id := dlmanager.NewID()
	sessCtx, cancel := context.WithCancel(context.Background())
	sess := &dlmanager.Session{
		ID:        id,
		Kind:      "files",
		System:    req.System,
		Server:    req.Server,
		Paths:     append([]string(nil), req.Paths...),
		Zip:       req.Zip,
		Folder:    downloadRoot, // ← 项 18：可被 target_dir 覆盖；SSE done 行暴露
		CreatedAt: time.Now(),
	}
	sess.AttachCancel(cancel)
	s.downloads.Create(sess)
	go s.downloads.IdleGC(sess)

	// 审计 + 响应：把 resolved target_dir 一起返给前端（成功路径走 SSE done 行时也返）
	s.audit.Write("files.download.start", "system", req.System, "server", req.Server,
		"files", len(req.Paths), "target_dir", downloadRoot, "zip", req.Zip)
	writeJSON(w, 200, map[string]any{
		"id":         id,
		"target_dir": downloadRoot, // ← 顺手返：让前端立刻知道落点（不用等 SSE done）
	})

	// 异步执行；用独立 ctx，不依赖 r.Context()（请求结束就断开）
	go s.runFilesDownloadTask(sessCtx, sess, srv, creds.Username, creds.Password)
}

// runFilesDownloadTask 后台执行：Dial SSH → 开 SFTP → 串行下每个路径 → （可选）打 zip
func (s *Server) runFilesDownloadTask(
	ctx context.Context,
	sess *dlmanager.Session,
	srv *config.ServerConfig,
	username, password string,
) {
	dialCtx, cancelDial := context.WithTimeout(ctx, sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
	}, sshclient.Credentials{Password: password}, sshAttemptTimeout)
	cancelDial()
	if err != nil {
		s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "fail", "stage", "dial", "err", err.Error())
		sess.MarkFinished(nil, fmt.Errorf("SSH 连接失败: %w", err))
		return
	}
	defer cli.Close()

	sftpCli, err := sftpDialer(cli)
	if err != nil {
		sess.MarkFinished(nil, fmt.Errorf("SFTP 打开失败: %w", err))
		return
	}
	defer sftpCli.Close()

	// 取消打断：ctx 被 cancel 时（用户点取消 / IdleGC 超时），
	// 主动关掉 SFTP / SSH 连接，正在进行的 io.Copy 会因为底层 read 返回错误而退出。
	// 没有这个，下大文件时取消可能要等好几秒网络读返回才生效。
	cancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = sftpCli.Close()
			_ = cli.Close()
		case <-cancelDone:
		}
	}()
	defer close(cancelDone)

	results, err := s.downloadSeriesFree(ctx, srv, sess.Paths, sftpCli, sess)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "fail", "stage", "cancel")
			sess.MarkFinished(results, fmt.Errorf("已取消"))
			return
		}
		s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "fail", "stage", "download", "err", err.Error())
		sess.MarkFinished(results, err)
		return
	}

	// 可选 zip（>= 2 个文件才打）
	if sess.Zip && len(results) >= 2 {
		// 跟下载文件同毫秒戳，保证 zip 命名跟里面文件保持一致。
		zipName := fmt.Sprintf("%s_files_%s.zip",
			sanitize(srv.Name), time.Now().Format("150405_000"))
		// v0.5 #18：用 sess.Folder 替代硬编码 s.cur().DownloadDir()，
		// 这样用户在请求里指定 target_dir 时 zip 也跟着落到同一目录。
		zipPath := filepath.Join(sess.Folder, results[0].Date, zipName)
		// 项 17：zip 内的文件名用远端原始 basename，下载时拿到的 zip
		// 解压后能直接看到原始文件名（而不是 server_001_app.log 这种本地化文件名）。
		sources := make([]ZipSource, 0, len(results))
		remoteNames := make([]string, 0, len(results))
		for _, it := range results {
			p := filepath.Join(sess.Folder, it.Date, it.Local)
			sources = append(sources, ZipSource{Path: p, NameInZip: filepath.Base(it.Remote)})
			remoteNames = append(remoteNames, it.Remote)
		}
		if err := zipFilesNamed(sources, zipPath); err != nil {
			s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "fail", "stage", "zip", "err", err.Error())
			sess.MarkFinished(results, fmt.Errorf("打包 zip 失败: %w", err))
			return
		}
		st, _ := os.Stat(zipPath)
		var size int64
		if st != nil {
			size = st.Size()
		}
		results = append(results, dlmanager.Item{
			Local: zipName,
			Bytes: strconv.FormatInt(size, 10),
			Date:  results[0].Date,
			Kind:  "zip",
		})
		_ = downloads.WriteMeta(zipPath, downloads.Meta{
			System: sess.System,
			Server: srv.Name,
			Host:   fmt.Sprintf("%s:%d", srv.Host, srv.Port),
			Files:  remoteNames,
			Kind:   "zip",
		})
	}

	s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "ok", "files", len(results), "selected", len(sess.Paths))
	sess.MarkFinished(results, nil)
}

// downloadSeriesFree 串行下多个"完整路径"文件，进度通过 session 广播。
//
// 任何一个文件失败立刻返回（已下完的文件留在本地，不删）。
// 本地落点：downloads/YYYYMMDD/server_<idx>_basename_HHMMSS
//   - 用远端路径 basename 当主名，保留原始文件名信息；
//   - 加 idx 前缀（001、002、...）防止同一次任务里同名文件覆盖
//     （如同时下 /a/app.log 和 /b/app.log）；
//   - 加 _HHMMSS 防止同一文件短时间内重复下载互相覆盖；
//   - 不强加 .log 后缀：浏览器下载任意文件都该是原始名+扩展名。
//
// 下载前先 Stat：目录直接报错，避免 SFTP Open 在不同 server 行为不一致。
func (s *Server) downloadSeriesFree(
	ctx context.Context,
	srv *config.ServerConfig,
	paths []string,
	sftpCli sftpClientLike,
	sess *dlmanager.Session,
) ([]dlmanager.Item, error) {
	now := time.Now()
	dateDir := now.Format("20060102")
	// 毫秒级时间戳避免同秒内重复下载互相覆盖。
	stamp := now.Format("150405_000")
	// v0.5 #18：用 sess.Folder 替代硬编码 s.cur().DownloadDir()，
	// 这样用户在请求里指定 target_dir 时文件落点跟着变。
	targetDir := filepath.Join(sess.Folder, dateDir)

	results := make([]dlmanager.Item, 0, len(paths))
	localPaths := make([]string, 0, len(paths))
	emittedPaths := make(map[string]bool, len(paths))

	for idx, remote := range paths {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		if emittedPaths[remote] {
			continue
		}
		emittedPaths[remote] = true

		// 下载前 Stat：目录不支持，Stat 失败也直接报错（避免 SFTP Open 半行为）
		info, statErr := sftpCli.Stat(remote)
		if statErr != nil {
			return results, fmt.Errorf("stat %s 失败: %w", remote, statErr)
		}
		if info.IsDir() {
			return results, fmt.Errorf("暂不支持直接下载目录: %s", remote)
		}

		base := filepath.Base(remote)
		sess.BroadcastEvent("file_start", map[string]any{
			"file":  remote,
			"index": idx,
			"total": len(paths),
		})

		// 用 idx+1 三位数（001/002/...）防止同 basename 互相覆盖
		localName := fmt.Sprintf("%s_%03d_%s_%s",
			sanitize(srv.Name), idx+1, sanitize(base), stamp)
		localPath := filepath.Join(targetDir, localName)

		progress := func(w, t int64) {
			sess.BroadcastEvent("progress", map[string]any{
				"file":    remote,
				"written": w,
				"total":   t,
			})
		}

		bytes, err := sftpCli.DownloadFileWithProgress(remote, localPath, progress)
		if err != nil {
			_ = os.Remove(localPath)
			return results, fmt.Errorf("下载 %s 失败: %w", remote, err)
		}

		results = append(results, dlmanager.Item{
			File:   base,
			Local:  localName,
			Bytes:  strconv.FormatInt(bytes, 10),
			Remote: remote,
			Date:   dateDir,
			Kind:   "file",
		})
		localPaths = append(localPaths, localPath)
		_ = downloads.WriteMeta(localPath, downloads.Meta{
			System: sess.System,
			Server: srv.Name,
			Host:   fmt.Sprintf("%s:%d", srv.Host, srv.Port),
			File:   base,
			Files:  []string{remote},
			Kind:   "file",
		})
		s.audit.Write("files.download", "system", sess.System, "server", srv.Name, "path", remote, "result", "ok", "bytes", bytes)

		sess.BroadcastEvent("file_done", map[string]any{
			"file":  remote,
			"bytes": bytes,
		})
	}
	return results, nil
}

// handleFilesDownloadEventsOrCancel 分发 events / cancel 子路径
func (s *Server) handleFilesDownloadEventsOrCancel(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/files/download/")
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
		s.cancelDownload(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

// streamDownloadEvents 把下载进度 / 状态通过 SSE 推给前端（复用 dlmanager.Session 订阅模型）
func (s *Server) streamDownloadEvents(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := s.downloads.Get(id)
	if !ok {
		writeErr(w, 404, errors.New("下载任务不存在"))
		return
	}
	// 已经结束：直接返回最终结果
	if sess.IsFinished() {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeErrSanitized(w, 500, errors.New("response writer 不支持 flush"))
			return
		}
		var ev []byte
		result, finalErr, folder := sess.Snapshot()
		if finalErr != nil {
			ev = dlmanager.FormatEvent("done", map[string]any{"ok": false, "error": finalErr.Error()})
		} else {
			ev = dlmanager.FormatEvent("done", map[string]any{"ok": true, "downloads": result, "folder": folder})
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", ev)
		_, _ = fmt.Fprintf(w, "event: done\ndata: {}\n\n")
		flusher.Flush()
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErrSanitized(w, 500, errors.New("response writer 不支持 flush"))
		return
	}

	ch, unsub := sess.Subscribe()
	defer unsub()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, open := <-ch:
			if !open {
				_, _ = fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		case <-keepalive.C:
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// cancelDownload 显式取消下载（logs 和 files 共用）
func (s *Server) cancelDownload(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	if !s.downloads.Cancel(id) {
		writeErr(w, 404, errors.New("下载任务不存在"))
		return
	}
	s.audit.Write("files.download", "id", id, "result", "ok", "stage", "cancel")
	writeJSON(w, 200, map[string]any{"ok": true, "id": id})
}
