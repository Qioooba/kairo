package httpserver

// handlers_ssh_sftp.go — SSH 终端 tab 内嵌的 SFTP 文件浏览器后端。
//
// 与 handlers_files.go 的差别：
//   - 不走 app.free_file_roots 白名单（用户在 SSH 终端已能 cd 到任何路径，
//     限制 SFTP 路径没意义；访问控制由 SSH 账号权限承担）；
//   - 不复用 /api/files/* 路由，独立 /api/ssh/sftp/* 命名空间，便于将来
//     扩展「共享 SSH 连接」（Phase 2：在此路由加 session_id 优先级）；
//   - 凭据复用 resolveCreds helper（keyring / file / 配置默认值三级降级）；
//   - SFTP 客户端复用 sftpclient.NewAuto（SFTP + shell 兜底，兼容老 AIX）；
//   - 下载进度复用 dlmanager.Session + SSE（与 files 页一致）；
//   - 严格只读：list / preview / download / pwd，无写/删/改权限接口
//     （符合 sftpclient 的安全约束）。
//
// Phase 1（当前）：每次请求独立 Dial，不复用 ws session 的 SSH 连接。
// Phase 2（规划）：扩展 sshshell.Manager 持有 *sshclient.Client，
//                 本路由支持 session_id 优先于 (system, server)。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"

	"kairo/internal/config"
	"kairo/internal/dlmanager"
	"kairo/internal/downloads"
	"kairo/internal/sftpclient"
	"kairo/internal/sshclient"
)

// ============================================================================
// 请求/响应类型
// ============================================================================

// sshSftpListReq 列目录请求
type sshSftpListReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Username string `json:"username"` // 可选；空则用 server 配置默认
	Password string `json:"password"` // 可选；空则走 resolveCreds（keyring/file/配置）
	Path     string `json:"path"`     // 必须以 "/" 开头
}

// sshSftpEntry 目录条目（与 filesEntry 一致，便于前端复用渲染逻辑）
type sshSftpEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"isDir"`
	Mode  string `json:"mode"`
	MTime string `json:"mtime"`
}

// sshSftpListResp 列目录响应
type sshSftpListResp struct {
	Path      string         `json:"path"`
	Parent    string         `json:"parent"`
	Entries   []sshSftpEntry `json:"entries"`
	Backend   string         `json:"backend"` // "sftp" | "shell"（兜底）
	ElapsedMs int64          `json:"elapsed_ms"`
}

// sshSftpPwdReq 获取当前目录请求
type sshSftpPwdReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// sshSftpPwdResp pwd 响应
type sshSftpPwdResp struct {
	Path string `json:"path"`
}

// sshSftpPreviewReq 预览请求
type sshSftpPreviewReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
	Path     string `json:"path"`
	Encoding string `json:"encoding,omitempty"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

// sshSftpDownloadReq 下载请求
type sshSftpDownloadReq struct {
	System    string   `json:"system"`
	Server    string   `json:"server"`
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	Paths     []string `json:"paths"`
	Zip       bool     `json:"zip"`
	TargetDir string   `json:"target_dir,omitempty"`
}

// ============================================================================
// 常量（与 handlers_files.go 对齐，便于一致行为）
// ============================================================================

const (
	sshSftpMaxFilesPerTask = 100
	sshSftpPwdTimeout      = 8 * time.Second // pwd 是轻量命令，给 8s 足够（含 dial）
)

// resolveSshSftpCreds 是 /api/ssh/sftp/* 系列 handler 共用的凭据解析 helper。
// 4 个 handler（list/pwd/preview/download）入参都是 (system, server, username, password)，
// 共享「FindServer → resolveCreds → 校验 password 非空」的三段式校验。
// 成功返回 (srv, creds, true)；失败时已 writeErr 并返回 false，调用方应直接 return。
func (s *Server) resolveSshSftpCreds(
	w http.ResponseWriter,
	system, server, username, password string,
) (*config.ServerConfig, resolvedCreds, bool) {
	cur := s.cur()
	_, srv, ok := cur.FindServer(system, server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return nil, resolvedCreds{}, false
	}
	creds, err := s.resolveCreds(username, password, system, server, srv.Username, srv.Password)
	if err != nil {
		writeErr(w, 400, err)
		return nil, resolvedCreds{}, false
	}
	if creds.Password == "" {
		writeErr(w, 400, errors.New("缺少密码（请先在 SSH 终端连接时输入并保存）"))
		return nil, resolvedCreds{}, false
	}
	return srv, creds, true
}

// ============================================================================
// handleSshSftpList — POST /api/ssh/sftp/list
// ============================================================================

func (s *Server) handleSshSftpList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req sshSftpListReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(req.System) == "" || strings.TrimSpace(req.Server) == "" {
		writeErr(w, 400, errors.New("system / server 不能为空"))
		return
	}
	if req.Path == "" {
		writeErr(w, 400, errors.New("path 不能为空"))
		return
	}
	if !strings.HasPrefix(req.Path, "/") {
		writeErr(w, 400, fmt.Errorf("path 必须是绝对路径: %q", req.Path))
		return
	}
	if strings.ContainsAny(req.Path, "\x00\n\r") {
		writeErr(w, 400, fmt.Errorf("path 含非法字符: %q", req.Path))
		return
	}

	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}

	start := time.Now()
	entries, backend, err := s.sshSftpListOne(r.Context(), srv, creds.Username, creds.Password, req.Path)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		s.audit.Write("ssh.sftp.list", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "err", err.Error(), "elapsed_ms", ms)
		writeErrSanitized(w, 502, err)
		return
	}

	cleaned := path.Clean(req.Path)
	parent := ""
	if cleaned != "/" && cleaned != "." {
		parent = path.Dir(cleaned)
	}
	s.audit.Write("ssh.sftp.list", "system", req.System, "server", req.Server,
		"path", req.Path, "result", "ok", "count", len(entries),
		"backend", backend, "elapsed_ms", ms)
	writeJSON(w, 200, sshSftpListResp{
		Path:      cleaned,
		Parent:    parent,
		Entries:   entries,
		Backend:   backend,
		ElapsedMs: ms,
	})
}

// sshSftpListOne 实际执行：Dial → NewAuto → ReadDir → 关连接。
// 返回 (entries, backend, err)。backend 由 sftpclient.Client.Backend() 给出。
func (s *Server) sshSftpListOne(
	parentCtx context.Context,
	srv *config.ServerConfig,
	username, password, remotePath string,
) ([]sshSftpEntry, string, error) {
	ctx, cancel := context.WithTimeout(parentCtx, sshDialOuterTimeout)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: password}, sshAttemptTimeout)
	if err != nil {
		return nil, "", fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer cli.Close()

	runFn := func(c context.Context, cmd string, t time.Duration, enc string) (string, string, int, error) {
		return cli.Run(c, cmd, t, enc)
	}
	sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
	if err != nil {
		return nil, "", fmt.Errorf("SFTP 打开失败: %w", err)
	}
	defer sftpCli.Close()

	infos, err := sftpCli.ReadDir(remotePath)
	if err != nil {
		// sftpclient.ReadDir 自己已经 wrap 过错误，不再重复 wrap
		return nil, sftpCli.Backend(), err
	}

	entries := make([]sshSftpEntry, 0, len(infos))
	for _, info := range infos {
		entries = append(entries, sshSftpEntry{
			Name:  info.Name(),
			Size:  info.Size(),
			IsDir: info.IsDir(),
			Mode:  info.Mode().String(),
			MTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return entries, sftpCli.Backend(), nil
}

// ============================================================================
// handleSshSftpPwd — POST /api/ssh/sftp/pwd
// ============================================================================

func (s *Server) handleSshSftpPwd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req sshSftpPwdReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(req.System) == "" || strings.TrimSpace(req.Server) == "" {
		writeErr(w, 400, errors.New("system / server 不能为空"))
		return
	}

	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), sshSftpPwdTimeout)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: creds.Username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: creds.Password}, sshAttemptTimeout)
	if err != nil {
		s.audit.Write("ssh.sftp.pwd", "system", req.System, "server", req.Server,
			"result", "fail", "stage", "dial", "err", err.Error())
		writeErrSanitized(w, 502, err)
		return
	}
	defer cli.Close()

	// pwd 是 POSIX 标准命令，所有 shell（bash/ksh/sh/csh）都支持。
	// encoding 用 utf-8：路径里的中文（少数场景）走 UTF-8 比较稳；
	// 老系统 LANG 未设时会走 C locale，pwd 输出纯 ASCII，无编码问题。
	stdout, _, _, err := cli.Run(ctx, "pwd", 5*time.Second, "utf-8")
	if err != nil {
		s.audit.Write("ssh.sftp.pwd", "system", req.System, "server", req.Server,
			"result", "fail", "stage", "run", "err", err.Error())
		writeErrSanitized(w, 502, fmt.Errorf("执行 pwd 失败: %w", err))
		return
	}
	// pwd 输出形如 "/home/user\n"，trim 掉尾部的换行和空白
	pwd := strings.TrimSpace(stdout)
	if pwd == "" {
		s.audit.Write("ssh.sftp.pwd", "system", req.System, "server", req.Server,
			"result", "fail", "stage", "empty")
		writeErr(w, 500, errors.New("pwd 返回空"))
		return
	}
	// 防御：路径必须以 / 开头（cwd 一定是绝对路径）
	if !strings.HasPrefix(pwd, "/") {
		// 某些 shell 的 pwd 可能输出带 hostname 前缀（极少见），再 trim 一次
		if idx := strings.LastIndex(pwd, "/"); idx >= 0 {
			pwd = pwd[idx:]
		}
	}
	s.audit.Write("ssh.sftp.pwd", "system", req.System, "server", req.Server,
		"result", "ok", "path", pwd)
	writeJSON(w, 200, sshSftpPwdResp{Path: pwd})
}

// ============================================================================
// handleSshSftpPreview — POST /api/ssh/sftp/preview
// ============================================================================

func (s *Server) handleSshSftpPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req sshSftpPreviewReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(req.System) == "" || strings.TrimSpace(req.Server) == "" {
		writeErr(w, 400, errors.New("system / server 不能为空"))
		return
	}
	if req.Path == "" {
		writeErr(w, 400, errors.New("path 不能为空"))
		return
	}
	if !strings.HasPrefix(req.Path, "/") {
		writeErr(w, 400, fmt.Errorf("path 必须是绝对路径: %q", req.Path))
		return
	}
	if strings.ContainsAny(req.Path, "\x00\n\r") {
		writeErr(w, 400, fmt.Errorf("path 含非法字符: %q", req.Path))
		return
	}

	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}

	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = filesPreviewDefaultMax
	}
	if maxBytes > filesPreviewHardMax {
		maxBytes = filesPreviewHardMax
	}
	encoding := normalizePreviewEncoding(req.Encoding)

	raw, fileSize, err := s.sshSftpPreviewOne(r.Context(), srv, creds.Username, creds.Password, req.Path, maxBytes)
	if err != nil {
		s.audit.Write("ssh.sftp.preview", "system", req.System, "server", req.Server,
			"path", req.Path, "result", "fail", "err", err.Error())
		if strings.Contains(err.Error(), "不支持预览目录") {
			writeErr(w, 400, err)
		} else {
			writeErrSanitized(w, 502, err)
		}
		return
	}

	binary := isBinaryBytes(raw)
	var text string
	if binary {
		text = ""
	} else if encoding == "gbk" {
		decoded, derr := simplifiedchinese.GBK.NewDecoder().Bytes(raw)
		if derr != nil {
			s.audit.Write("ssh.sftp.preview", "system", req.System, "server", req.Server,
				"path", req.Path, "result", "fail", "stage", "decode", "err", derr.Error())
			writeErr(w, 400, fmt.Errorf("GBK 解码失败（文件可能不是 GBK 编码）: %w", derr))
			return
		}
		text = string(decoded)
	} else {
		text = string(raw)
	}

	resp := filesPreviewResp{
		Name:      path.Base(req.Path),
		Path:      path.Clean(req.Path),
		Encoding:  encoding,
		Size:      fileSize,
		BytesRead: len(raw),
		Truncated: fileSize > int64(len(raw)),
		IsBinary:  binary,
		Content:   text,
	}
	s.audit.Write("ssh.sftp.preview", "system", req.System, "server", req.Server,
		"path", req.Path, "result", "ok", "bytes", len(raw), "encoding", encoding)
	writeJSON(w, 200, resp)
}

// sshSftpPreviewOne Dial → NewAuto → Stat → Open → Read(maxBytes)
func (s *Server) sshSftpPreviewOne(
	parentCtx context.Context,
	srv *config.ServerConfig,
	username, password, remotePath string,
	maxBytes int64,
) ([]byte, int64, error) {
	ctx, cancel := context.WithTimeout(parentCtx, sshDialOuterTimeout)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: password}, sshAttemptTimeout)
	if err != nil {
		return nil, 0, fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer cli.Close()

	runFn := func(c context.Context, cmd string, t time.Duration, enc string) (string, string, int, error) {
		return cli.Run(c, cmd, t, enc)
	}
	sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
	if err != nil {
		return nil, 0, fmt.Errorf("SFTP 打开失败: %w", err)
	}
	defer sftpCli.Close()

	info, err := sftpCli.Stat(remotePath)
	if err != nil {
		return nil, 0, fmt.Errorf("stat 失败: %w", err)
	}
	if info.IsDir() {
		return nil, 0, fmt.Errorf("不支持预览目录: %s", remotePath)
	}

	f, err := sftpCli.Open(remotePath)
	if err != nil {
		return nil, 0, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()

	reader := io.LimitReader(f, maxBytes)
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, info.Size(), fmt.Errorf("读取文件失败: %w", err)
	}
	return raw, info.Size(), nil
}

// ============================================================================
// handleSshSftpDownload — POST /api/ssh/sftp/download
// ============================================================================

func (s *Server) handleSshSftpDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req sshSftpDownloadReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if len(req.Paths) == 0 {
		writeErr(w, 400, errors.New("paths 不能为空"))
		return
	}
	if len(req.Paths) > sshSftpMaxFilesPerTask {
		writeErr(w, 400, fmt.Errorf("单次最多下载 %d 个文件", sshSftpMaxFilesPerTask))
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
	}

	srv, creds, ok := s.resolveSshSftpCreds(w, req.System, req.Server, req.Username, req.Password)
	if !ok {
		return
	}
	cur := s.cur()

	// target_dir 复用 validateTargetDir 的校验逻辑
	resolvedTarget, err := validateTargetDir(req.TargetDir)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if resolvedTarget != "" && !cur.App.AllowCustomDownloadDirEnabled() {
		writeErr(w, 403, errors.New("自定义下载目录已被配置关闭 (app.allow_custom_download_dir=false)"))
		return
	}
	if resolvedTarget != "" && !cur.App.TargetDirAllowed(resolvedTarget) {
		writeErr(w, 403, fmt.Errorf("target_dir %q 不在 app.allowed_download_roots 白名单中", resolvedTarget))
		return
	}
	downloadRoot := resolvedTarget
	if downloadRoot == "" {
		downloadRoot = cur.DownloadDir()
	}

	id := dlmanager.NewID()
	sessCtx, cancel := context.WithCancel(context.Background())
	sess := &dlmanager.Session{
		ID:        id,
		Kind:      "ssh-sftp",
		System:    req.System,
		Server:    req.Server,
		Paths:     append([]string(nil), req.Paths...),
		Zip:       req.Zip,
		Folder:    downloadRoot,
		CreatedAt: time.Now(),
	}
	sess.AttachCancel(cancel)
	s.downloads.Create(sess)
	go s.downloads.IdleGC(sess)

	s.audit.Write("ssh.sftp.download.start", "system", req.System, "server", req.Server,
		"files", len(req.Paths), "target_dir", downloadRoot, "zip", req.Zip)
	writeJSON(w, 200, map[string]any{
		"id":         id,
		"target_dir": downloadRoot,
	})

	go s.runSshSftpDownloadTask(sessCtx, sess, srv, creds.Username, creds.Password)
}

// runSshSftpDownloadTask 后台执行下载任务。
// 复用 dlmanager.Session 做进度广播，复用 sftpclient.NewAuto 做 SFTP/shell 兜底。
func (s *Server) runSshSftpDownloadTask(
	ctx context.Context,
	sess *dlmanager.Session,
	srv *config.ServerConfig,
	username, password string,
) {
	dialCtx, cancelDial := context.WithTimeout(ctx, sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: s.cur().App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: password}, sshAttemptTimeout)
	cancelDial()
	if err != nil {
		s.audit.Write("ssh.sftp.download", "system", sess.System, "server", sess.Server,
			"result", "fail", "stage", "dial", "err", err.Error())
		sess.MarkFinished(nil, fmt.Errorf("SSH 连接失败: %w", err))
		return
	}
	defer cli.Close()

	runFn := func(c context.Context, cmd string, t time.Duration, enc string) (string, string, int, error) {
		return cli.Run(c, cmd, t, enc)
	}
	sftpCli, err := sftpclient.NewAuto(cli.RawConn(), runFn)
	if err != nil {
		sess.MarkFinished(nil, fmt.Errorf("SFTP 打开失败: %w", err))
		return
	}
	defer sftpCli.Close()

	// 取消打断：ctx 被 cancel 时主动关连接
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

	// 复用 downloadSeriesFree 逻辑：串行下多个文件，进度广播。
	// sftpCli 是 *sftpclient.Client，已实现 sftpClientLike 接口
	// （Close/ReadDir/Stat/DownloadFile/DownloadFileWithProgress/Open），直接传入即可。
	results, err := s.downloadSeriesFree(ctx, srv, sess.Paths, sftpCli, sess)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.audit.Write("ssh.sftp.download", "system", sess.System, "server", sess.Server,
				"result", "fail", "stage", "cancel")
			sess.MarkFinished(results, fmt.Errorf("已取消"))
			return
		}
		s.audit.Write("ssh.sftp.download", "system", sess.System, "server", sess.Server,
			"result", "fail", "stage", "download", "err", err.Error())
		sess.MarkFinished(results, err)
		return
	}

	// 可选 zip（>= 2 个文件才打）
	if sess.Zip && len(results) >= 2 {
		zipName := fmt.Sprintf("%s_files_%s.zip", sanitize(srv.Name), results[0].Date)
		zipPath := filepath.Join(sess.Folder, results[0].Date, zipName)
		sources := make([]ZipSource, 0, len(results))
		remoteNames := make([]string, 0, len(results))
		for _, it := range results {
			p := filepath.Join(sess.Folder, it.Date, it.Local)
			sources = append(sources, ZipSource{Path: p, NameInZip: path.Base(it.Remote)})
			remoteNames = append(remoteNames, it.Remote)
		}
		if err := zipFilesNamed(sources, zipPath); err != nil {
			s.audit.Write("ssh.sftp.download", "system", sess.System, "server", sess.Server,
				"result", "fail", "stage", "zip", "err", err.Error())
			sess.MarkFinished(results, fmt.Errorf("打包 zip 失败: %w", err))
			return
		}
		st, _ := os.Stat(zipPath)
		var size int64
		if st != nil {
			size = st.Size()
		}
		results = append(results, dlmanager.Item{
			Local:   zipName,
			Bytes:   strconv.FormatInt(size, 10),
			Date:    results[0].Date,
			Kind:    "zip",
			AbsPath: zipPath,
		})
		_ = downloads.WriteMeta(zipPath, downloads.Meta{
			System: sess.System,
			Server: srv.Name,
			Host:   fmt.Sprintf("%s:%d", srv.Host, srv.Port),
			Files:  remoteNames,
			Kind:   "zip",
		})
	}

	s.audit.Write("ssh.sftp.download", "system", sess.System, "server", sess.Server,
		"result", "ok", "files", len(results), "selected", len(sess.Paths))
	sess.MarkFinished(results, nil)
	go s.TriggerCleanup()
}

// ============================================================================
// handleSshSftpDownloadEventsOrCancel — GET/POST /api/ssh/sftp/download/{id}/events|cancel
// ============================================================================

func (s *Server) handleSshSftpDownloadEventsOrCancel(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/ssh/sftp/download/")
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
