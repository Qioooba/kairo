// Package httpserver 负责 HTTP 路由、静态资源、以及所有 /api/* 端点。
//
// 安全：
//   - 只监听 127.0.0.1（由 main.go 保证）；
//   - 只接受来自同源 / 本地的请求（不做 CSRF 复杂校验，但禁止跨域）；
//   - 所有入参都做合法性校验后再传给 sshclient / sftpclient。
package httpserver

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ops-toolbox/internal/audit"
	"ops-toolbox/internal/config"
	"ops-toolbox/internal/credentials"
	"ops-toolbox/internal/downloads"
	"ops-toolbox/internal/formatter"
	"ops-toolbox/internal/logquery"
	"ops-toolbox/internal/sftpclient"
	"ops-toolbox/internal/sshclient"
	"ops-toolbox/internal/tailmgr"
)

// Server 持有配置（线程安全 Manager）、审计日志、嵌入式静态资源、tail 会话池、下载任务池
type Server struct {
	cfg      *config.Manager
	audit    *audit.Logger
	webRoot  fs.FS
	tails    *tailmgr.Manager
	downloads *dlManager
}

// New 构造一个 Server
func New(cfg *config.Manager, a *audit.Logger, webRoot fs.FS, tails *tailmgr.Manager) *Server {
	return &Server{cfg: cfg, audit: a, webRoot: webRoot, tails: tails, downloads: newDownloadMgr()}
}

// cur 拿一份当前 Config 的只读快照。
// 所有 handler 入口先调一次，后续直接读快照字段即可。
// （Manager 是 COW 语义，每次 Replace 整体换指针，读快照不会被撕裂。）
func (s *Server) cur() *config.Config { return s.cfg.Get() }

// ServeHTTP 入口
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 简单防跨域：只允许同源
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")

	path := r.URL.Path
	switch {
	case path == "/" || path == "/index.html":
		s.serveStatic(w, r, "index.html")
	case strings.HasPrefix(path, "/static/"):
		s.serveStatic(w, r, strings.TrimPrefix(path, "/static/"))
	case path == "/api/config":
		s.handleConfig(w, r)
	case path == "/api/ssh/test":
		s.handleSSHTest(w, r)
	case path == "/api/logs/list":
		s.handleLogsList(w, r)
	case path == "/api/logs/download-latest":
		s.handleDownloadLatest(w, r)
	case path == "/api/logs/search":
		s.handleLogsSearch(w, r)
	case path == "/api/logs/search/multi":
		s.handleLogsSearchMulti(w, r)
	case path == "/api/logs/context":
		s.handleLogsContext(w, r)
	case path == "/api/logs/tail/start":
		s.handleTailStart(w, r)
	case path == "/api/audit/recent":
		s.handleAuditRecent(w, r)
	case path == "/api/credentials/save":
		s.handleCredSave(w, r)
	case path == "/api/credentials/has":
		s.handleCredHas(w, r)
	case path == "/api/credentials/clear":
		s.handleCredClear(w, r)
	case path == "/api/downloads/list":
		s.handleDownloadsList(w, r)
	case strings.HasPrefix(path, "/api/downloads/"):
		s.handleDownloadsItem(w, r)
	case path == "/api/format/json":
		s.handleFormatJSON(w, r)
	case path == "/api/format/xml":
		s.handleFormatXML(w, r)
	case path == "/api/admin/servers":
		s.handleAdminServers(w, r)
	case path == "/api/files/list":
		s.handleFilesList(w, r)
	case path == "/api/files/download":
		s.handleFilesDownload(w, r)
	case strings.HasPrefix(path, "/api/files/download/"):
		s.handleFilesDownloadEventsOrCancel(w, r)
	case strings.HasPrefix(path, "/api/logs/tail/"):
		s.handleTailEventsOrStop(w, r)
	case strings.HasPrefix(path, "/downloads/"):
		s.serveDownload(w, r)
	default:
		http.NotFound(w, r)
	}
}

// ---------- 静态资源 ----------

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" {
		http.NotFound(w, r)
		return
	}
	// 防路径穿越
	if strings.Contains(name, "..") || strings.Contains(name, "\\") {
		http.NotFound(w, r)
		return
	}
	f, err := s.webRoot.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		http.NotFound(w, r)
		return
	}
	// 设置 content-type
	switch {
	case strings.HasSuffix(name, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(name, ".png"):
		w.Header().Set("Content-Type", "image/png")
	}
	// 短缓存
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.Copy(w, f)
}

// ---------- 通用辅助 ----------

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// ---------- /api/config ----------

type configView struct {
	App     config.AppConfig      `json:"app"`
	Systems []config.SystemConfig `json:"systems"`
	Search  config.SearchConfig   `json:"search"`
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	cur := s.cur()
	writeJSON(w, 200, configView{
		App:     cur.App,
		Systems: cur.Systems,
		Search:  cur.Search,
	})
}

// ---------- /api/ssh/test ----------

type sshTestReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleSSHTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req sshTestReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
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
	username := creds.Username

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: creds.Password}, 10*time.Second)
	if err != nil {
		clean := sshclient.SanitizeError(err.Error())
		s.audit.Write("ssh.test", "system", req.System, "server", req.Server, "result", "fail", "err", clean)
		writeErr(w, 502, errors.New(clean))
		return
	}
	defer cli.Close()
	// 跑一条无害命令确认可执行
	stdout, _, code, err := cli.Run(ctx, "echo ok", 5*time.Second, "")
	if err != nil || code != 0 || strings.TrimSpace(stdout) != "ok" {
		s.audit.Write("ssh.test", "system", req.System, "server", req.Server, "result", "fail", "err", "echo failed")
		writeErr(w, 502, fmt.Errorf("连接成功但命令执行失败: code=%d err=%v", code, err))
		return
	}
	s.audit.Write("ssh.test", "system", req.System, "server", req.Server, "result", "ok")
	writeJSON(w, 200, map[string]any{
		"ok":     true,
		"server": req.Server,
		"system": req.System,
	})
}

// ---------- /api/logs/list ----------

type logsListReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Dir      string `json:"dir"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req logsListReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	_, srv, ok := s.cur().FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return
	}
	// 找匹配的 log_dir（必须来自配置白名单）
	ld, ok := findLogDir(srv, req.Dir)
	if !ok {
		writeErr(w, 400, errors.New("目录不在白名单中"))
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
	username := creds.Username

	ctx, cancel := context.WithTimeout(r.Context(), s.cur().SearchTimeout()+10*time.Second)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: creds.Password}, 10*time.Second)
	if err != nil {
		auditErr(w, s.audit, "logs.list", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "fail", err)
		writeErr(w, 502, err)
		return
	}
	defer cli.Close()

	cmd, err := logquery.ListCommand(ld.Path, ld.Patterns, 100)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	stdout, stderr, code, err := cli.Run(ctx, cmd, s.cur().SearchTimeout(), ld.Encoding)
	if err != nil {
		auditErr(w, s.audit, "logs.list", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "fail", err)
		writeErr(w, 502, err)
		return
	}
	if code != 0 {
		s.audit.Write("logs.list", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "fail", "err", "remote exit="+strconv.Itoa(code), "stderr", trim(stderr, 200))
		writeErr(w, 502, fmt.Errorf("远程命令退出码 %d: %s", code, trim(stderr, 200)))
		return
	}
	files, err := logquery.ParseListOutput(stdout)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// 补全 FullPath
	for i := range files {
		files[i].FullPath = filepath.ToSlash(filepath.Join(ld.Path, files[i].Name))
	}
	s.audit.Write("logs.list", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "ok", "count", strconv.Itoa(len(files)))
	writeJSON(w, 200, map[string]any{
		"files": files,
		"dir":   ld.Path,
		"name":  ld.Name,
	})
}

// findLogDir 在 server 的 log_dirs 中按 name 或 path 找
func findLogDir(srv *config.ServerConfig, key string) (*config.LogDirEntry, bool) {
	k := strings.TrimSpace(key)
	for i := range srv.LogDirs {
		ld := &srv.LogDirs[i]
		if ld.Name == k || ld.Path == k {
			return ld, true
		}
	}
	// 默认第一个
	if k == "" && len(srv.LogDirs) > 0 {
		return &srv.LogDirs[0], true
	}
	return nil, false
}

// ---------- /api/logs/download-latest ----------

type downloadLatestReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Dir      string `json:"dir"`
	Username string `json:"username"`
	Password string `json:"password"`
	Latest   int    `json:"latest"` // 下载几个最新文件，默认 1
	Zip      bool   `json:"zip"`    // 是否额外打包为 zip（latest>1 时才有意义）
}

// downloadItem 表示单个下载产物（普通文件 或 zip）
type downloadItem struct {
	File   string `json:"file"`   // 远端原文件名（zip 时为 ""）
	Local  string `json:"local"`  // 本地文件名（zip 时就是 zip 的名字）
	Bytes  string `json:"bytes"`
	Remote string `json:"remote"` // 远端路径（zip 时为 ""）
	Date   string `json:"date"`
	Kind   string `json:"kind"`   // "file" 或 "zip"
}

func (s *Server) handleDownloadLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req downloadLatestReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	_, srv, ok := s.cur().FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return
	}
	ld, ok := findLogDir(srv, req.Dir)
	if !ok {
		writeErr(w, 400, errors.New("目录不在白名单中"))
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
	username := creds.Username
	latest := req.Latest
	if latest <= 0 {
		latest = 1
	}
	if latest > 5 {
		latest = 5
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: creds.Password}, 10*time.Second)
	if err != nil {
		auditErr(w, s.audit, "logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "fail", err)
		writeErr(w, 502, err)
		return
	}
	defer cli.Close()

	sftpCli, err := sftpclient.New(cli.RawConn())
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	defer sftpCli.Close()

	// 列文件
	cmd, err := logquery.ListCommand(ld.Path, ld.Patterns, latest)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	stdout, stderr, code, err := cli.Run(ctx, cmd, s.cur().SearchTimeout(), ld.Encoding)
	if err != nil || code != 0 {
		s.audit.Write("logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "fail", "err", trim(stderr, 200))
		writeErr(w, 502, fmt.Errorf("列文件失败: %v / %s", err, trim(stderr, 200)))
		return
	}
	files, err := logquery.ParseListOutput(stdout)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if len(files) == 0 {
		writeErr(w, 404, errors.New("没有匹配的文件"))
		return
	}
	if latest > len(files) {
		latest = len(files)
	}

	results := make([]downloadItem, 0, latest+1)
	now := time.Now()
	dateDir := now.Format("20060102")
	hhmm := now.Format("150405")
	// 目录别名：把 log_dir.path 的最后一段当作"dirName"，让多服务器同名日志也能区分
	dirAlias := filepath.Base(ld.Path)
	// 下载文件落点：downloads/YYYYMMDD/serverName_dirName_originalName_HHMMSS.log
	// （多台服务器同时下载同名 SystemOut.log 时不会互相覆盖）
	targetDir := filepath.Join(s.cur().DownloadDir(), dateDir)
	// 收集已经下到本地的文件路径，zip 时按这个顺序打包
	localPaths := make([]string, 0, latest)
	for i := 0; i < latest; i++ {
		f := files[i]
		remote := filepath.ToSlash(filepath.Join(ld.Path, f.Name))
		localName := fmt.Sprintf("%s_%s_%s_%s.log",
			sanitize(srv.Name), sanitize(dirAlias), sanitize(f.Name), hhmm)
		localPath := filepath.Join(targetDir, localName)
		bytes, err := sftpCli.DownloadFile(remote, localPath)
		if err != nil {
			s.audit.Write("logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "file", f.Name, "result", "fail", "err", err.Error())
			writeErr(w, 502, fmt.Errorf("下载 %s 失败: %w", f.Name, err))
			return
		}
		results = append(results, downloadItem{
			File:   f.Name,
			Local:  localName,
			Bytes:  strconv.FormatInt(bytes, 10),
			Remote: remote,
			Date:   dateDir,
			Kind:   "file",
		})
		localPaths = append(localPaths, localPath)
		// 写 sidecar 元数据 — 给"下载历史"页用
		_ = downloads.WriteMeta(localPath, downloads.Meta{
			System:   req.System,
			Server:   srv.Name,
			Host:     fmt.Sprintf("%s:%d", srv.Host, srv.Port),
			Dir:      ld.Path,
			DirAlias: dirAlias,
			File:     f.Name,
			Encoding: ld.Encoding,
			Kind:     "file",
		})
		s.audit.Write("logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "file", f.Name, "result", "ok", "bytes", bytes)
	}
	// 是否额外打 zip？
	// 规则：
	//   - latest=1 时 zip 没意义，忽略；
	//   - 显式 req.Zip=true 才打，避免默认行为变化让老用户惊喜；
	//   - zip 落点和原始文件同一个 dateDir，文件名加 _pack 后缀以示区分。
	if req.Zip && latest >= 2 {
		zipName := fmt.Sprintf("%s_%s_latest_%s.zip",
			sanitize(srv.Name), sanitize(dirAlias), hhmm)
		zipPath := filepath.Join(targetDir, zipName)
		if err := zipFiles(localPaths, zipPath); err != nil {
			s.audit.Write("logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "fail", "stage", "zip", "err", err.Error())
			writeErr(w, 502, fmt.Errorf("打包 zip 失败: %w", err))
			return
		}
		st, statErr := os.Stat(zipPath)
		var size int64
		if statErr == nil {
			size = st.Size()
		}
		results = append(results, downloadItem{
			File:   "",
			Local:  zipName,
			Bytes:  strconv.FormatInt(size, 10),
			Remote: "",
			Date:   dateDir,
			Kind:   "zip",
		})
		// 写 zip 的 sidecar：把包含的远端文件名记下来
		fileNames := make([]string, 0, len(localPaths))
		for i := 0; i < latest && i < len(files); i++ {
			fileNames = append(fileNames, files[i].Name)
		}
		_ = downloads.WriteMeta(zipPath, downloads.Meta{
			System:   req.System,
			Server:   srv.Name,
			Host:     fmt.Sprintf("%s:%d", srv.Host, srv.Port),
			Dir:      ld.Path,
			DirAlias: dirAlias,
			Files:    fileNames,
			Encoding: ld.Encoding,
			Kind:     "zip",
		})
		s.audit.Write("logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "result", "ok", "stage", "zip", "files", latest, "bytes", size)
	}
	writeJSON(w, 200, map[string]any{
		"downloads": results,
		"folder":    s.cur().DownloadDir(),
	})
}


// zipFiles 把若干已下载的本地文件打包成单个 zip。
//
// 设计要点：
//   - 用 archive/zip + DEFLATE（默认级别），纯 stdlib，无新增依赖；
//   - zip 内文件名只保留"原始名"（不带 downloads/YYYYMMDD/server_dir_ 前缀），
//     这样在 Windows 资源管理器里双击打开能直接看到干净的日志；
//   - 写入时若任一文件打开失败，整体报错，不留半截 zip。
func zipFiles(srcPaths []string, destPath string) error {
	if len(srcPaths) == 0 {
		return errors.New("没有可打包的文件")
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("创建目标目录失败: %w", err)
	}
	dst, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("创建 zip 文件失败: %w", err)
	}
	zw := zip.NewWriter(dst)
	closed := false
	defer func() {
		if !closed {
			_ = zw.Close()
			_ = dst.Close()
		}
	}()
	for _, p := range srcPaths {
		// 拒绝对 zip 路径外的相对穿越（其实 zip 是新文件名，不带路径，但保险起见校验一次）
		if strings.Contains(p, "..") || strings.ContainsAny(p, "\\") {
			return fmt.Errorf("非法源文件路径: %s", p)
		}
		f, err := os.Open(p)
		if err != nil {
			return fmt.Errorf("打开源文件 %s 失败: %w", p, err)
		}
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("stat %s 失败: %w", p, err)
		}
		// zip 内只保留原始文件名（去前缀、去目录），避免解压后路径嵌套太深
		name := filepath.Base(p)
		// 同名文件避免覆盖：第二个起加 _N 后缀
		header := &zip.FileHeader{
			Name:     name,
			Method:   zip.Deflate,
			Modified: st.ModTime(),
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("写入 zip 头失败: %w", err)
		}
		if _, err := io.Copy(w, f); err != nil {
			_ = f.Close()
			return fmt.Errorf("写入 zip 内容失败: %w", err)
		}
		_ = f.Close()
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("关闭 zip writer 失败: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("关闭 zip 文件失败: %w", err)
	}
	closed = true
	return nil
}

// sanitize 把字符串清成安全文件名片段
func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "x"
	}
	bad := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", " "}
	for _, b := range bad {
		s = strings.ReplaceAll(s, b, "_")
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// ---------- /api/logs/search ----------

type logsSearchReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Dir      string `json:"dir"`
	Files    int    `json:"files"`    // 选最近几个文件
	Query    string `json:"query"`    // 搜索表达式
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogsSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req logsSearchReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	_, srv, ok := s.cur().FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return
	}
	ld, ok := findLogDir(srv, req.Dir)
	if !ok {
		writeErr(w, 400, errors.New("目录不在白名单中"))
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
	username := creds.Username
	kw, err := logquery.ParseQuery(req.Query)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	filesN := req.Files
	if filesN <= 0 {
		filesN = s.cur().Search.DefaultLatestFiles
	}
	if filesN > 10 {
		filesN = 10
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cur().SearchTimeout()+15*time.Second)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: creds.Password}, 10*time.Second)
	if err != nil {
		auditErr(w, s.audit, "logs.search", "system", req.System, "server", req.Server, "dir", ld.Path, "query", req.Query, "result", "fail", err)
		writeErr(w, 502, err)
		return
	}
	defer cli.Close()

	// 列 N 个最新文件，再过滤 pattern 匹配的
	cmd, err := logquery.ListCommand(ld.Path, ld.Patterns, filesN)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	stdout, stderr, code, err := cli.Run(ctx, cmd, s.cur().SearchTimeout(), ld.Encoding)
	if err != nil || code != 0 {
		writeErr(w, 502, fmt.Errorf("列文件失败: %v", err))
		return
	}
	files, err := logquery.ParseListOutput(stdout)
	if err != nil || len(files) == 0 {
		writeJSON(w, 200, map[string]any{"hits": []any{}})
		return
	}
	fileNames := make([]string, 0, len(files))
	for _, f := range files {
		fileNames = append(fileNames, f.Name)
	}

	cmd, err = logquery.SearchCommand(ld.Path, fileNames, kw, s.cur().Search.MaxMatches, s.cur().Search.TimeoutSeconds, ld.Encoding)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	stdout, stderr, code, err = cli.Run(ctx, cmd, s.cur().SearchTimeout()+5*time.Second, ld.Encoding)
	if err != nil {
		s.audit.Write("logs.search", "system", req.System, "server", req.Server, "dir", ld.Path, "query", req.Query, "result", "fail", "err", err.Error())
		writeErr(w, 502, err)
		return
	}
	if code != 0 {
		// grep 没匹配到返回 1，是正常的
		if code == 1 && strings.TrimSpace(stdout) == "" {
			writeJSON(w, 200, map[string]any{"hits": []any{}})
			return
		}
		s.audit.Write("logs.search", "system", req.System, "server", req.Server, "dir", ld.Path, "query", req.Query, "result", "fail", "err", "exit="+strconv.Itoa(code), "stderr", trim(stderr, 200))
		writeErr(w, 502, fmt.Errorf("搜索失败: exit=%d %s", code, trim(stderr, 200)))
		return
	}

	hits := parseSearchOutput(stdout, srv.Name, ld.Path, files)
	s.audit.Write("logs.search", "system", req.System, "server", req.Server, "dir", ld.Path, "query", req.Query, "result", "ok", "hits", len(hits))
	writeJSON(w, 200, map[string]any{
		"hits":  hits,
		"files": fileNames,
	})
}

func parseSearchOutput(out string, server, dir string, files []logquery.FileEntry) []logquery.SearchHit {
	// 建立 name -> file 映射（mtime 倒序）
	byName := make(map[string]logquery.FileEntry, len(files))
	for _, f := range files {
		byName[f.Name] = f
	}
	var hits []logquery.SearchHit
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// 形如 "filename:lineno:content"
		idx1 := strings.Index(line, ":")
		if idx1 < 0 {
			continue
		}
		idx2 := strings.Index(line[idx1+1:], ":")
		if idx2 < 0 {
			continue
		}
		file := line[:idx1]
		lineNoStr := line[idx1+1 : idx1+1+idx2]
		content := line[idx1+1+idx2+1:]
		ln, err := strconv.Atoi(strings.TrimSpace(lineNoStr))
		if err != nil {
			continue
		}
		hits = append(hits, logquery.SearchHit{
			Server:   server,
			Dir:      dir,
			File:     file,
			FullPath: filepath.ToSlash(filepath.Join(dir, file)),
			LineNo:   ln,
			Content:  content,
		})
	}
	return hits
}

// ---------- /api/logs/context ----------

type logsContextReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Dir      string `json:"dir"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Before   int    `json:"before"`
	After    int    `json:"after"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogsContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req logsContextReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	_, srv, ok := s.cur().FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return
	}
	ld, ok := findLogDir(srv, req.Dir)
	if !ok {
		writeErr(w, 400, errors.New("目录不在白名单中"))
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
	username := creds.Username
	before := req.Before
	if before <= 0 {
		before = s.cur().Search.DefaultContextLines
	}
	after := req.After
	if after <= 0 {
		after = s.cur().Search.DefaultContextLines
	}
	if before > 500 {
		before = 500
	}
	if after > 500 {
		after = 500
	}
	cmd, err := logquery.ContextCommand(ld.Path, req.File, req.Line, before, after, s.cur().Search.TimeoutSeconds)
	if err != nil {
		writeErr(w, 400, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cur().SearchTimeout()+10*time.Second)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: creds.Password}, 10*time.Second)
	if err != nil {
		auditErr(w, s.audit, "logs.context", "system", req.System, "server", req.Server, "dir", ld.Path, "file", req.File, "line", req.Line, "result", "fail", err)
		writeErr(w, 502, err)
		return
	}
	defer cli.Close()

	stdout, stderr, code, err := cli.Run(ctx, cmd, s.cur().SearchTimeout(), ld.Encoding)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	if code != 0 {
		writeErr(w, 502, fmt.Errorf("sed 退出码 %d: %s", code, trim(stderr, 200)))
		return
	}
	lines := parseContextOutput(stdout, req.Line, before)
	writeJSON(w, 200, map[string]any{
		"lines": lines,
		"file":  req.File,
		"line":  req.Line,
	})
}

func parseContextOutput(out string, hitLine, before int) []logquery.ContextLine {
	// sed -n 'a,bp' 的输出不带行号 —— 我们按输出行号重算
	var result []logquery.ContextLine
	arr := strings.Split(out, "\n")
	if len(arr) > 0 && strings.TrimRight(arr[len(arr)-1], "\r") == "" {
		arr = arr[:len(arr)-1]
	}
	startLine := hitLine - before
	if startLine < 1 {
		startLine = 1
	}
	for i, raw := range arr {
		raw = strings.TrimRight(raw, "\r")
		ln := startLine + i
		result = append(result, logquery.ContextLine{
			LineNo:  ln,
			Content: raw,
			Hit:     ln == hitLine,
		})
	}
	return result
}

// ---------- /api/format/json ----------

type formatJSONReq struct {
	Input  string `json:"input"`
	Mode   string `json:"mode"`   // format | minify | validate
	Indent string `json:"indent"` // 缩进字符串，默认 "  "
}

func (s *Server) handleFormatJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatJSONReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "format"
	}
	indent := req.Indent
	if indent == "" {
		indent = "  "
	}
	var out string
	var err error
	switch mode {
	case "format":
		out, err = formatter.FormatJSON(req.Input, indent)
	case "minify":
		out, err = formatter.MinifyJSON(req.Input)
	case "validate":
		if err = formatter.ValidateJSON(req.Input); err == nil {
			writeJSON(w, 200, map[string]any{"ok": true})
			return
		}
	default:
		writeErr(w, 400, errors.New("mode 仅支持 format/minify/validate"))
		return
	}
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "output": out})
}

// ---------- /api/format/xml ----------

type formatXMLReq struct {
	Input  string `json:"input"`
	Mode   string `json:"mode"`
	Indent string `json:"indent"`
}

func (s *Server) handleFormatXML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatXMLReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "format"
	}
	indent := req.Indent
	if indent == "" {
		indent = "  "
	}
	var out string
	var err error
	switch mode {
	case "format":
		out, err = formatter.FormatXML(req.Input, indent)
	case "minify":
		out, err = formatter.MinifyXML(req.Input)
	default:
		writeErr(w, 400, errors.New("mode 仅支持 format/minify"))
		return
	}
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "output": out})
}

// ---------- /api/admin/servers ----------
//
// GET  — 返回完整 systems 树（与 /api/config 同样的内容）。
// PUT  — 用请求体整树替换内存配置，原子写回 config.yaml。
//
// 设计要点：
//   - PUT 不接受 app/search 段（这两个是运维控制，不开放给页面改）；
//     页面只需要管 systems 树。
//   - 写盘用 Manager.Replace 内部跑 Defaults + Validate，
//     校验失败直接 400，磁盘不会被破坏。
type adminServersPutReq struct {
	Systems []config.SystemConfig `json:"systems"`
}

func (s *Server) handleAdminServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cur := s.cur()
		writeJSON(w, 200, configView{
			App:     cur.App,
			Systems: cur.Systems,
			Search:  cur.Search,
		})
	case http.MethodPut:
		var req adminServersPutReq
		if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
			writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
			return
		}
		if len(req.Systems) == 0 {
			writeErr(w, 400, errors.New("至少需要一个系统"))
			return
		}
		// 用现有 App + Search + 新的 Systems 构造新 Config
		cur := s.cur()
		newCfg := *cur // 值拷贝
		newCfg.Systems = req.Systems
		if err := s.cfg.Replace(&newCfg); err != nil {
			s.audit.Write("admin.servers.put", "result", "fail", "err", err.Error())
			writeErr(w, 400, err)
			return
		}
		s.audit.Write("admin.servers.put", "result", "ok", "systems", len(req.Systems))
		writeJSON(w, 200, map[string]any{
			"ok":      true,
			"systems": len(req.Systems),
			"path":    s.cfg.Path(),
		})
	default:
		writeErr(w, 405, errors.New("仅支持 GET / PUT"))
	}
}

// ---------- /api/logs/search/multi ----------
//
// 多服务器并行搜索，每台独立 SSH / 列文件 / 搜索 / 解析，
// 每台独立审计，超时独立计时。
//
// 并发控制：用有缓冲的 channel 当信号量，max 默认从 Search.MaxConcurrency 取，
// 若请求体里显式带 max_concurrency 字段则按它。
//
// 入参：
//   system           — 必填，业务系统名
//   servers          — 必填，多个 server name
//   dir              — 必填，日志目录的 name 或 path
//   files            — 每台搜索最近 N 个文件
//   query            — 搜索表达式
//   username/password — SSH 凭据（必须）
//   max_concurrency  — 可选，覆盖 Search.MaxConcurrency
type logsSearchMultiReq struct {
	System         string   `json:"system"`
	Servers        []string `json:"servers"`
	Dir            string   `json:"dir"`
	Files          int      `json:"files"`
	Query          string   `json:"query"`
	Username       string   `json:"username"`
	Password       string   `json:"password"`
	MaxConcurrency int      `json:"max_concurrency"`
}

type logsSearchMultiServerResult struct {
	Server   string               `json:"server"`
	Host     string               `json:"host"`
	Encoding string               `json:"encoding,omitempty"`
	OK       bool                 `json:"ok"`
	Error    string               `json:"error,omitempty"`
	Hits     []logquery.SearchHit `json:"hits,omitempty"`
	Files    []string             `json:"files,omitempty"`
	HitsN    int                  `json:"hits_count"`
	Ms       int64                `json:"elapsed_ms"`
}

func (s *Server) handleLogsSearchMulti(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req logsSearchMultiReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	if strings.TrimSpace(req.System) == "" {
		writeErr(w, 400, errors.New("system 不能为空"))
		return
	}
	if len(req.Servers) == 0 {
		writeErr(w, 400, errors.New("servers 至少一个"))
		return
	}
	if strings.TrimSpace(req.Dir) == "" {
		writeErr(w, 400, errors.New("dir 不能为空"))
		return
	}
	kw, err := logquery.ParseQuery(req.Query)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	cur := s.cur()
	filesN := req.Files
	if filesN <= 0 {
		filesN = cur.Search.DefaultLatestFiles
	}
	if filesN > 10 {
		filesN = 10
	}
	maxConc := req.MaxConcurrency
	if maxConc <= 0 {
		maxConc = cur.Search.MaxConcurrency
	}
	if maxConc <= 0 {
		maxConc = 8 // 全局兜底
	}
	if maxConc > 32 {
		maxConc = 32
	}
	// 解析凭据的用户名（页面可留空，使用配置默认）
	username := strings.TrimSpace(req.Username)
	if username == "" {
		// 优先用 servers 列表第一台的用户名作为默认值
		if _, srv0, ok := cur.FindServer(req.System, req.Servers[0]); ok && srv0.Username != "" {
			username = srv0.Username
		}
	}
	if username == "" {
		writeErr(w, 400, errors.New("缺少用户名"))
		return
	}

	// 整体超时：每台 search_timeout + 一点缓冲，再乘以 max(并发比, 1) 作为下限。
	// 实际由 ctx.WithTimeout + goroutine 内部的 Run 共同兜底。
	perServer := cur.SearchTimeout() + 15*time.Second
	totalCtx, cancel := context.WithTimeout(r.Context(), perServer+30*time.Second)
	defer cancel()

	// 信号量
	sem := make(chan struct{}, maxConc)
	results := make([]logsSearchMultiServerResult, len(req.Servers))
	var wg sync.WaitGroup

	for i, srvName := range req.Servers {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()
			// 取信号
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-totalCtx.Done():
				results[idx] = logsSearchMultiServerResult{
					Server: name, OK: false, Error: "总超时未开始",
				}
				return
			}

			_, srv, ok := cur.FindServer(req.System, name)
			if !ok {
				results[idx] = logsSearchMultiServerResult{
					Server: name, OK: false, Error: "系统或服务器不存在",
				}
				return
			}
			ld, ok := findLogDir(srv, req.Dir)
			if !ok {
				results[idx] = logsSearchMultiServerResult{
					Server: name, Host: srv.Host, OK: false, Error: "目录不在白名单中",
				}
				return
			}
			// 每台服务器独立解析凭据（password 可来自请求或本机 keyring）
			c, cErr := s.resolveCreds(req.Username, req.Password, req.System, srv.Name, srv.Username)
			if cErr != nil {
				results[idx] = logsSearchMultiServerResult{Server: name, Host: srv.Host, OK: false, Error: cErr.Error()}
				return
			}
			if c.Password == "" {
				results[idx] = logsSearchMultiServerResult{
					Server: name, Host: srv.Host, OK: false,
					Error: "缺少密码（输入或勾选「记住密码」）",
				}
				return
			}
			res := s.runOneServerSearch(totalCtx, srv, ld, filesN, kw, c.Username, c.Password)
			results[idx] = res
			// 审计
			if res.OK {
				s.audit.Write("logs.search.multi",
					"system", req.System, "server", name, "dir", ld.Path,
					"query", req.Query, "result", "ok", "hits", res.HitsN, "ms", res.Ms)
			} else {
				s.audit.Write("logs.search.multi",
					"system", req.System, "server", name, "dir", ld.Path,
					"query", req.Query, "result", "fail", "err", res.Error, "ms", res.Ms)
			}
		}(i, srvName)
	}
	wg.Wait()

	// 汇总
	okN, totalHits := 0, 0
	for _, r := range results {
		if r.OK {
			okN++
			totalHits += r.HitsN
		}
	}
	writeJSON(w, 200, map[string]any{
		"servers":       results,
		"ok_count":      okN,
		"fail_count":    len(results) - okN,
		"total_hits":    totalHits,
		"max_concurrency": maxConc,
	})
}

// runOneServerSearch 单台服务器的完整搜索流程：
// Dial → 列文件 → 过滤 patterns → 构造 search 命令 → Run → parse。
// 出错时把 err 写到结果里，不 panic；ctx 取消时优雅退出。
func (s *Server) runOneServerSearch(
	ctx context.Context,
	srv *config.ServerConfig,
	ld *config.LogDirEntry,
	filesN int,
	kw []logquery.SearchKeyword,
	username, password string,
) logsSearchMultiServerResult {
	start := time.Now()
	res := logsSearchMultiServerResult{
		Server: srv.Name, Host: srv.Host, Encoding: ld.Encoding,
	}

	// 单独给 Dial 一个短超时（10s），但仍受总 ctx 控制
	dialCtx, cancelDial := context.WithTimeout(ctx, 10*time.Second)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: password}, 10*time.Second)
	cancelDial()
	if err != nil {
		res.OK = false
		res.Error = sshclient.SanitizeError(err.Error())
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	defer cli.Close()

	cur := s.cur()

	// 列 N 个最新文件
	listCmd, err := logquery.ListCommand(ld.Path, ld.Patterns, filesN)
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	listCtx, cancelList := context.WithTimeout(ctx, cur.SearchTimeout()+5*time.Second)
	stdout, _, code, err := cli.Run(listCtx, listCmd, cur.SearchTimeout(), ld.Encoding)
	cancelList()
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	if code != 0 {
		res.OK = false
		res.Error = "列文件失败 exit=" + strconv.Itoa(code)
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	files, err := logquery.ParseListOutput(stdout)
	if err != nil || len(files) == 0 {
		// 没文件不算错，只是没结果
		res.OK = true
		res.Hits = []logquery.SearchHit{}
		res.Files = []string{}
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	fileNames := make([]string, 0, len(files))
	for _, f := range files {
		fileNames = append(fileNames, f.Name)
	}

	// 搜索
	searchCmd, err := logquery.SearchCommand(ld.Path, fileNames, kw, cur.Search.MaxMatches, cur.Search.TimeoutSeconds, ld.Encoding)
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	searchCtx, cancelSearch := context.WithTimeout(ctx, cur.SearchTimeout()+10*time.Second)
	stdout, stderr, code, err := cli.Run(searchCtx, searchCmd, cur.SearchTimeout()+5*time.Second, ld.Encoding)
	cancelSearch()
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	if code != 0 {
		// grep exit=1 是"无匹配"，是正常的
		if code == 1 && strings.TrimSpace(stdout) == "" {
			res.OK = true
			res.Hits = []logquery.SearchHit{}
			res.Files = fileNames
			res.Ms = time.Since(start).Milliseconds()
			return res
		}
		res.OK = false
		res.Error = "搜索失败 exit=" + strconv.Itoa(code) + " " + trim(stderr, 200)
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	hits := parseSearchOutput(stdout, srv.Name, ld.Path, files)
	res.OK = true
	res.Hits = hits
	res.Files = fileNames
	res.HitsN = len(hits)
	res.Ms = time.Since(start).Milliseconds()
	return res
}

// ---------- /downloads/<file> ----------

func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/downloads/")
	if name == "" || strings.Contains(name, "..") || strings.Contains(name, "\\") || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.cur().DownloadDir(), filepath.FromSlash(name))
	// 必须在 DownloadDir 下
	abs, err := filepath.Abs(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	base, err := filepath.Abs(s.cur().DownloadDir())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !strings.HasPrefix(abs, base+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	_, _ = io.Copy(w, f)
}

// ---------- utils ----------

// auditErr 把 err 写进审计日志，自动脱敏敏感字面量，并保证 502 时也写。
// 接受 http.ResponseWriter 是为了和 writeErr 风格保持一致（未来可挂中间件）。
func auditErr(w http.ResponseWriter, a *audit.Logger, op string, kv ...any) {
	if len(kv) < 2 {
		return
	}
	// 把最后一个 kv 视为 err
	errVal := kv[len(kv)-1]
	errStr, ok := errVal.(error)
	if !ok {
		errStr = fmt.Errorf("%v", errVal)
	}
	clean := sshclient.SanitizeError(errStr.Error())
	kv = append(kv[:len(kv)-1], "err", clean)
	a.Write(op, kv...)
}

// resolvedCreds SSH 凭据解析结果。Password 为空表示"需要前端提示用户输入"。
type resolvedCreds struct {
	Username       string
	Password       string
	SavedByKeyring bool // true 表示 password 来自 OS 钥匙串，不回传给前端
}

// resolveCreds 把 HTTP 请求里的凭据 + OS 钥匙串合并成一个最终值。
//   - inputUser / inputPass：HTTP 请求里的明文
//   - system / server：钥匙串的 key（system 和 server 名称）
//   - defaultUser：配置里的默认 SSH 用户名（inputUser 为空时使用）
//
// 返回规则：
//   - err != nil：无法继续（缺用户、钥匙串不可用）
//   - err == nil && Password != ""：可直接用
//   - err == nil && Password == ""：前端没传、钥匙串也没存，需用户输入
func (s *Server) resolveCreds(inputUser, inputPass, system, server, defaultUser string) (resolvedCreds, error) {
	username := strings.TrimSpace(inputUser)
	if username == "" {
		username = defaultUser
	}
	if username == "" {
		return resolvedCreds{}, errors.New("缺少用户名")
	}
	if inputPass != "" {
		return resolvedCreds{Username: username, Password: inputPass}, nil
	}
	// 尝试从 keyring 读
	pw, err := credentials.Get(system, server, username)
	if err == nil {
		return resolvedCreds{Username: username, Password: pw, SavedByKeyring: true}, nil
	}
	if errors.Is(err, credentials.ErrNotSaved) {
		return resolvedCreds{Username: username}, nil
	}
	// 其它错误（钥匙串不可用等）
	return resolvedCreds{}, fmt.Errorf("系统钥匙串不可用，请手动输入密码或检查系统配置: %w", err)
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "..."
}

// ---------- /api/logs/tail/* ----------

type tailStartReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Dir      string `json:"dir"`
	File     string `json:"file"`
	Lines    int    `json:"lines"` // 启动时先吐的最近 N 行；0 = 只追新增；最大 1000
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleTailStart 创建一个 tail 会话
//
// 注意：返回 200 立即返回，tail 的输出通过 /api/logs/tail/{id}/events 订阅。
func (s *Server) handleTailStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req tailStartReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	_, srv, ok := s.cur().FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return
	}
	ld, ok := findLogDir(srv, req.Dir)
	if !ok {
		writeErr(w, 400, errors.New("目录不在白名单中"))
		return
	}
	if strings.TrimSpace(req.File) == "" {
		writeErr(w, 400, errors.New("file 不能为空"))
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
	username := creds.Username

	// 开 SSH（30s 超时）
	dialCtx, dialCancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer dialCancel()
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: creds.Password}, 15*time.Second)
	if err != nil {
		auditErr(w, s.audit, "logs.tail", "system", req.System, "server", req.Server, "dir", ld.Path, "file", req.File, "result", "fail", err)
		writeErr(w, 502, err)
		return
	}
	// 不要在这里 Close — tail session 接管 client 生命周期
	sess, err := s.tails.Start(cli, srv.Name, srv.Host, ld.Path, req.File, ld.Encoding, req.Lines)
	if err != nil {
		_ = cli.Close()
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("logs.tail", "system", req.System, "server", req.Server, "dir", ld.Path, "file", req.File, "result", "ok", "id", sess.ID, "lines", req.Lines)
	writeJSON(w, 200, map[string]any{
		"id":     sess.ID,
		"server": srv.Name,
		"dir":    ld.Path,
		"file":   req.File,
	})
}

// handleTailEventsOrStop 根据子路径分发：
//   - GET  /api/logs/tail/{id}/events → SSE 流
//   - POST /api/logs/tail/{id}/stop    → 显式停止
func (s *Server) handleTailEventsOrStop(w http.ResponseWriter, r *http.Request) {
	// path = /api/logs/tail/{id}/events 或 /api/logs/tail/{id}/stop
	rest := strings.TrimPrefix(r.URL.Path, "/api/logs/tail/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	action := parts[1]
	switch action {
	case "events":
		s.streamTailEvents(w, r, id)
	case "stop":
		s.stopTail(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

// streamTailEvents 用 SSE 把 tail 输出推给前端
//
// SSE 格式：data: {json}\n\n
// 这里不专门做 event: 分类，前端把 data 解析为 JSON 看 kind 即可。
func (s *Server) streamTailEvents(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := s.tails.Get(id)
	if !ok {
		writeErr(w, 404, errors.New("tail 会话不存在"))
		return
	}
	// SSE 必备响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // 关 nginx 缓冲（如有反代）

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, errors.New("response writer 不支持 flush"))
		return
	}

	ch, unsub := sess.Subscribe()
	defer unsub()

	// 周期性心跳：15s 没新行就推 :keepalive
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, open := <-ch:
			if !open {
				// 会话结束，发一条最终事件后退出
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

// stopTail 显式停止一个 tail 会话
func (s *Server) stopTail(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	if err := s.tails.Stop(id); err != nil {
		writeErr(w, 404, err)
		return
	}
	s.audit.Write("logs.tail", "id", id, "result", "ok", "stage", "stop")
	writeJSON(w, 200, map[string]any{"ok": true, "id": id})
}

// ---------- /api/audit/recent ----------

// handleAuditRecent 返回 audit.log 中最近的 N 条记录。
//
// Query 参数：
//   - limit=N  最多返回 N 条（默认 200，上限 5000）
//   - op=xxx   按 op= 精确过滤（如 ssh.test / logs.list / logs.search / logs.download / logs.tail）
//   - system=xxx  按 system 包含过滤
//   - server=xxx  按 server 包含过滤
//   - result=ok|fail  按 result 过滤
//
// 返回 {records: [{ts, op, system, server, raw}], path: "..."}
func (s *Server) handleAuditRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := audit.Filter{
		Op:     q.Get("op"),
		System: q.Get("system"),
		Server: q.Get("server"),
		Result: q.Get("result"),
	}
	recs, err := s.audit.Recent(limit, f)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	out := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		row := map[string]any{
			"ts":     rec.Time.Format("2006-01-02 15:04:05.000"),
			"op":     rec.Op,
			"system": rec.KV["system"],
			"server": rec.KV["server"],
			"result": rec.KV["result"],
			"raw":    rec.Raw,
		}
		// 把其它常用字段也单独提出来
		for _, k := range []string{"dir", "file", "query", "stage", "bytes", "hits", "id", "lines", "files", "err", "count"} {
			if v, ok := rec.KV[k]; ok && v != "" {
				row[k] = v
			}
		}
		out = append(out, row)
	}
	writeJSON(w, 200, map[string]any{
		"records": out,
		"count":   len(out),
	})
}

// ---------- /api/credentials/* ----------

type credSaveReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleCredSave 保存 SSH 密码到 OS 钥匙串。
func (s *Server) handleCredSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req credSaveReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(req.Password) == "" {
		writeErr(w, 400, errors.New("password 不能为空"))
		return
	}
	if err := s.credCheckSysSrv(req.System, req.Server); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := credentials.Save(req.System, req.Server, strings.TrimSpace(req.Username), req.Password); err != nil {
		if errors.Is(err, credentials.ErrUnavailable) {
			writeErr(w, 503, err)
			return
		}
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("credentials.save",
		"system", req.System, "server", req.Server,
		"username", strings.TrimSpace(req.Username), "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleCredHas 检查指定凭据是否存在。
//
// Query: ?system=&server=&username=
// 返回: {ok, has, available}
//   - ok=true 总是
//   - has=true 表示已保存
//   - available=false 表示 keyring 在当前平台不可用（前端可提示"无法使用记住密码"）
func (s *Server) handleCredHas(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	q := r.URL.Query()
	system, server, user := q.Get("system"), q.Get("server"), q.Get("username")
	if err := s.credCheckSysSrv(system, server); err != nil {
		writeErr(w, 400, err)
		return
	}
	has, err := credentials.Has(system, server, user)
	if err != nil {
		if errors.Is(err, credentials.ErrUnavailable) {
			writeJSON(w, 200, map[string]any{"ok": true, "has": false, "available": false, "err": err.Error()})
			return
		}
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "has": has, "available": true})
}

// handleCredClear 删除指定凭据。
func (s *Server) handleCredClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req credSaveReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.credCheckSysSrv(req.System, req.Server); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := credentials.Clear(req.System, req.Server, strings.TrimSpace(req.Username)); err != nil {
		if errors.Is(err, credentials.ErrNotSaved) {
			writeJSON(w, 200, map[string]any{"ok": true, "cleared": false})
			return
		}
		if errors.Is(err, credentials.ErrUnavailable) {
			writeErr(w, 503, err)
			return
		}
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("credentials.clear",
		"system", req.System, "server", req.Server,
		"username", strings.TrimSpace(req.Username), "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true, "cleared": true})
}

// credCheckSysSrv 校验 system/server 都在配置白名单里（防止前端乱传 keyring 索引）。
func (s *Server) credCheckSysSrv(system, server string) error {
	if strings.TrimSpace(system) == "" || strings.TrimSpace(server) == "" {
		return errors.New("system 和 server 必填")
	}
	_, _, ok := s.cur().FindServer(system, server)
	if !ok {
		return errors.New("系统或服务器不存在")
	}
	return nil
}

// ---------- /api/downloads/* ----------

// handleDownloadsList 列出 downloads/ 目录下的所有下载文件 + 元数据。
//
// GET /api/downloads/list?system=&server=
//   - system / server 可选；只过滤元数据中匹配的（不会真的去访问 SSH）
//   - 返回 {count, total_bytes, files: [Entry...]}
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

// humanBytes 把字节数转成"1.2 MB"这种格式
func humanBytes(n int64) string {
	const k = 1024
	if n < k {
		return strconv.FormatInt(n, 10) + " B"
	}
	if n < k*k {
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	}
	if n < k*k*k {
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(k*k*k))
}

// ---------- 下载任务池（带 SSE 进度）----------
//
// 模型：
//
//	POST /api/logs/download                   → 创建任务（启动后台下载），立即返回 {id}
//	GET  /api/logs/download/{id}/events       → SSE 进度流
//	POST /api/logs/download/{id}/cancel       → 取消
//
// 设计要点：
//   - session 持有一个 cancel ctx；cancel 时 SSH/SFTP 流被中断，下载协程退出；
//   - 进度通过 broadcast 推给所有订阅者；
//   - 多文件串行下载（一次一个 SFTP 流）；进度事件里带当前文件名，
//     前端可按 file 字段找到表格行并更新行内进度条；
//   - 全部完成 / 失败 / 取消都广播一条 done 事件，订阅者据此关闭 SSE。

// dlSession 一个下载任务
//
// Kind 用于区分审计 op 与路由命名空间：
//   - "logs"：走 /api/logs/download/*（白名单日志目录下，审计 op=logs.download）
//   - "files"：走 /api/files/download/*（任意路径，审计 op=files.download）
// 复用同一个 dlManager，ID 全局唯一（newDLID）。

// newDLID 构造下载任务 ID（带前缀便于排查）
func newDLID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "dl-" + hex.EncodeToString(b[:])
}
type dlSession struct {
	ID        string
	Kind      string // "logs" | "files"
	System    string
	Server    string
	Dir       string // 仅 logs 模式使用，files 模式为空
	Files     []string
	Paths     []string // 仅 files 模式使用：完整远端路径
	Zip       bool
	Folder    string // 本地下载根目录（带进 done 事件，前端"本地保存目录"显示用）
	CreatedAt time.Time

	mu          sync.RWMutex
	subscribers map[chan []byte]struct{}
	cancel      context.CancelFunc
	finished    bool
	result      []downloadItem
	finalErr    error
}

// dlManager 全局下载任务池
type dlManager struct {
	mu       sync.RWMutex
	sessions map[string]*dlSession
}

func newDownloadMgr() *dlManager {
	return &dlManager{sessions: make(map[string]*dlSession)}
}

// create 建一个空 session（不启动下载）。调用方拿到 id 后再异步启动 SSH + 下载。
func (m *dlManager) create(s *dlSession) string {
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return s.ID
}

func (m *dlManager) get(id string) (*dlSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *dlManager) cancel(id string) bool {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	if s.cancel != nil {
		s.cancel()
	}
	return true
}

// idleGC 清理已结束且无订阅者的 session
func (m *dlManager) idleGC(s *dlSession) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		<-ticker.C
		s.mu.RLock()
		finished := s.finished
		subs := len(s.subscribers)
		s.mu.RUnlock()
		if finished && subs == 0 {
			m.mu.Lock()
			// 二次检查（避免 cancel 时机竞争）
			s.mu.RLock()
			still := s.finished && len(s.subscribers) == 0
			s.mu.RUnlock()
			if still {
				delete(m.sessions, s.ID)
			}
			m.mu.Unlock()
			return
		}
		// 兜底：超过 30 分钟没结束就强制 cancel
		if time.Since(s.CreatedAt) > 30*time.Minute {
			if s.cancel != nil {
				s.cancel()
			}
		}
	}
}

// Subscribe 注册订阅者。channel 在 markFinished 时会被 close，
// 订阅者用 <-ch 的第二个返回值判断是否已结束（与 tailmgr 一致）。
func (s *dlSession) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 128)
	s.mu.Lock()
	already := s.finished
	if s.subscribers == nil {
		s.subscribers = make(map[chan []byte]struct{})
	}
	if !already {
		s.subscribers[ch] = struct{}{}
	}
	s.mu.Unlock()
	if already {
		// session 已结束，新订阅者拿到的 ch 立即关闭
		close(ch)
	} else {
		// 安全检查：双重 finished 之间的竞争（markFinished 已经 close 了所有旧 ch）
		// 这里只有已经 finish 后才 close(ch)，不重复
	}
	cancel := func() {
		s.mu.Lock()
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
	return ch, cancel
}

// broadcast 推一条事件给所有订阅者
func (s *dlSession) broadcast(line []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.subscribers {
		select {
		case ch <- line:
		default:
			// 订阅者处理慢，丢这一帧（前端会被后续 done 事件收尾）
		}
	}
}

// markFinished 标记 session 结束，阻塞推 done 给所有订阅者，再 close channel。
//
// 为什么走阻塞推：进度事件可丢（前端会被下一次更新覆盖），但 done 事件
// 必到——否则前端不知道"任务结束 vs 网络断"，会卡在 99% 假死。
func (s *dlSession) markFinished(result []downloadItem, finalErr error) {
	s.mu.Lock()
	s.finished = true
	s.result = result
	s.finalErr = finalErr
	subs := s.subscribers
	s.subscribers = nil
	s.mu.Unlock()

	var ev []byte
	if finalErr != nil {
		ev = formatDLEvent("done", map[string]any{
			"ok":    false,
			"error": finalErr.Error(),
		})
	} else {
		ev = formatDLEvent("done", map[string]any{
			"ok":        true,
			"downloads": result,
			"folder":    s.Folder, // 前端下载结果卡显示用
		})
	}
	// 阻塞推 done；订阅者必然能收到
	for ch := range subs {
		ch <- ev
	}
	for ch := range subs {
		close(ch)
	}
}

// formatDLEvent 构造 SSE data: 字段（手写 JSON，避免引号转义麻烦）
func formatDLEvent(kind string, kv map[string]any) []byte {
	var b strings.Builder
	b.WriteString(`{"kind":"`)
	b.WriteString(kind)
	b.WriteString(`"`)
	for k, v := range kv {
		b.WriteString(`,"`)
		b.WriteString(k)
		b.WriteString(`":`)
		b.WriteString(jsonValue(v))
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

// jsonValue 把 Go 值序列化成最小 JSON
func jsonValue(v any) string {
	switch x := v.(type) {
	case string:
		return jsonStringVal(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []downloadItem:
		// 复用 downloadItem 的 JSON 序列化（保证字段一致）
		b, _ := json.Marshal(x)
		return string(b)
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func jsonStringVal(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			if r < 0x20 {
				out = append(out, []byte(fmt.Sprintf(`\u%04x`, r))...)
			} else {
				out = append(out, byte(r))
			}
		}
	}
	out = append(out, '"')
	return string(out)
}

// ============================================================================
// v0.3：文件浏览器（任意路径浏览 + 下载，按 SSH 账号权限放行）
// ============================================================================

// filesListReq 列目录请求体
type filesListReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
	Path     string `json:"path"` // 必须以 "/" 开头的绝对路径
}

// filesEntry 目录条目（用于前端表格）
type filesEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"isDir"`
	Mode  string `json:"mode"`  // 例如 "drwxr-xr-x"，便于 UI 显示
	MTime string `json:"mtime"` // RFC3339
}

// handleFilesList 列远端目录（任意路径）。
//
// 与 /api/logs/list 的差别：
//   - 不做白名单校验，按用户 SSH 账号的实际权限放行（v0.3 自由模式）；
//   - 返回完整目录条目（含 size/mode/mtime），UI 可直接做表格；
//   - parent 字段给出"上一级"绝对路径，没有则 null（根目录就是 null）。
func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req filesListReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
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

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: creds.Username,
	}, sshclient.Credentials{Password: creds.Password}, 10*time.Second)
	if err != nil {
		s.audit.Write("files.list", "system", req.System, "server", req.Server, "path", req.Path, "result", "fail", "stage", "dial", "err", err.Error())
		writeErr(w, 502, err)
		return
	}
	defer cli.Close()

	sftpCli, err := sftpclient.New(cli.RawConn())
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	defer sftpCli.Close()

	infos, err := sftpCli.ReadDir(req.Path)
	if err != nil {
		s.audit.Write("files.list", "system", req.System, "server", req.Server, "path", req.Path, "result", "fail", "err", err.Error())
		writeErr(w, 502, fmt.Errorf("列出目录失败: %w", err))
		return
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

	// parent：根目录的 parent 是 null
	parent := ""
	cleaned := filepath.ToSlash(filepath.Clean(req.Path))
	if cleaned != "/" && cleaned != "." {
		parent = filepath.ToSlash(filepath.Dir(cleaned))
	}

	s.audit.Write("files.list", "system", req.System, "server", req.Server, "path", req.Path, "result", "ok", "count", len(entries))
	writeJSON(w, 200, map[string]any{
		"path":    cleaned,
		"parent":  parent,
		"entries": entries,
	})
}

// filesDownloadReq 下载请求体（任意路径）
type filesDownloadReq struct {
	System   string   `json:"system"`
	Server   string   `json:"server"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	Paths    []string `json:"paths"` // 完整远端路径（绝对路径）
	Zip      bool     `json:"zip"`   // 多文件时是否额外打 zip
}

// filesDownloadLimits 下载任务的硬约束（避免误操作 / 连接卡死）
const (
	filesMaxFilesPerTask = 100              // 单次最多 100 个文件
	filesDownloadTimeout = 30 * time.Minute // 单个下载任务总超时（dlSession idleGC 也是 30min）
)

// handleFilesDownload 启动一个"任意路径下载"任务，立即返回 id。
//
// 路径不做白名单校验（v0.3 自由模式）；只校验：
//   - 每个 path 非空、以 "/" 开头；
//   - 不含 NUL/换行等控制字符；
//   - 总数 ≤ filesMaxFilesPerTask；
// 真实访问控制由 SSH 服务器端承担。
//
// 下载机制复用 dlSession + dlManager（ID 全局唯一）。
func (s *Server) handleFilesDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
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

	id := newDLID()
	sessCtx, cancel := context.WithCancel(context.Background())
	sess := &dlSession{
		ID:        id,
		Kind:      "files",
		System:    req.System,
		Server:    req.Server,
		Paths:     append([]string(nil), req.Paths...),
		Zip:       req.Zip,
		Folder:    s.cur().DownloadDir(),
		CreatedAt: time.Now(),
		cancel:    cancel,
	}
	s.downloads.create(sess)
	go s.downloads.idleGC(sess)

	writeJSON(w, 200, map[string]any{"id": id})

	// 异步执行；用独立 ctx，不依赖 r.Context()（请求结束就断开）
	go s.runFilesDownloadTask(sessCtx, sess, srv, creds.Username, creds.Password)
}

// runFilesDownloadTask 后台执行：Dial SSH → 开 SFTP → 串行下每个路径 → （可选）打 zip
func (s *Server) runFilesDownloadTask(
	ctx context.Context,
	sess *dlSession,
	srv *config.ServerConfig,
	username, password string,
) {
	dialCtx, cancelDial := context.WithTimeout(ctx, 10*time.Second)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: password}, 10*time.Second)
	cancelDial()
	if err != nil {
		s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "fail", "stage", "dial", "err", err.Error())
		sess.markFinished(nil, fmt.Errorf("SSH 连接失败: %w", err))
		return
	}
	defer cli.Close()

	sftpCli, err := sftpclient.New(cli.RawConn())
	if err != nil {
		sess.markFinished(nil, fmt.Errorf("SFTP 打开失败: %w", err))
		return
	}
	defer sftpCli.Close()

	results, err := s.downloadSeriesFree(ctx, srv, sess.Paths, sftpCli, sess)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "fail", "stage", "cancel")
			sess.markFinished(results, fmt.Errorf("已取消"))
			return
		}
		s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "fail", "stage", "download", "err", err.Error())
		sess.markFinished(results, err)
		return
	}

	// 可选 zip（>= 2 个文件才打）
	if sess.Zip && len(results) >= 2 {
		zipName := fmt.Sprintf("%s_files_%s.zip",
			sanitize(srv.Name), time.Now().Format("150405"))
		zipPath := filepath.Join(s.cur().DownloadDir(), results[0].Date, zipName)
		localPaths := make([]string, 0, len(results))
		remoteNames := make([]string, 0, len(results))
		for _, it := range results {
			p := filepath.Join(s.cur().DownloadDir(), it.Date, it.Local)
			localPaths = append(localPaths, p)
			remoteNames = append(remoteNames, it.Remote)
		}
		if err := zipFiles(localPaths, zipPath); err != nil {
			s.audit.Write("files.download", "system", sess.System, "server", sess.Server, "result", "fail", "stage", "zip", "err", err.Error())
			sess.markFinished(results, fmt.Errorf("打包 zip 失败: %w", err))
			return
		}
		st, _ := os.Stat(zipPath)
		var size int64
		if st != nil {
			size = st.Size()
		}
		results = append(results, downloadItem{
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
	sess.markFinished(results, nil)
}

// downloadSeriesFree 串行下多个"完整路径"文件，进度通过 session 广播。
//
// 任何一个文件失败立刻返回（已下完的文件留在本地，不删）。
// 本地落点：downloads/YYYYMMDD/server_basename_HHMMSS
//   - 用远端路径 basename 当主名，保留原始文件名信息；
//   - 加 _HHMMSS 防止同一文件短时间内重复下载互相覆盖；
//   - 不强加 .log 后缀：浏览器下载任意文件都该是原始名+扩展名。
func (s *Server) downloadSeriesFree(
	ctx context.Context,
	srv *config.ServerConfig,
	paths []string,
	sftpCli *sftpclient.Client,
	sess *dlSession,
) ([]downloadItem, error) {
	now := time.Now()
	dateDir := now.Format("20060102")
	hhmm := now.Format("150405")
	targetDir := filepath.Join(s.cur().DownloadDir(), dateDir)

	results := make([]downloadItem, 0, len(paths))
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

		base := filepath.Base(remote)
		sess.broadcast(formatDLEvent("file_start", map[string]any{
			"file":  remote,
			"index": idx,
			"total": len(paths),
		}))

		localName := fmt.Sprintf("%s_%s_%s",
			sanitize(srv.Name), sanitize(base), hhmm)
		localPath := filepath.Join(targetDir, localName)

		progress := func(w, t int64) {
			sess.broadcast(formatDLEvent("progress", map[string]any{
				"file":    remote,
				"written": w,
				"total":   t,
			}))
		}

		bytes, err := sftpCli.DownloadFileWithProgress(remote, localPath, progress)
		if err != nil {
			_ = os.Remove(localPath)
			return results, fmt.Errorf("下载 %s 失败: %w", remote, err)
		}

		results = append(results, downloadItem{
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

		sess.broadcast(formatDLEvent("file_done", map[string]any{
			"file":  remote,
			"bytes": bytes,
		}))
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

// streamDownloadEvents 把下载进度 / 状态通过 SSE 推给前端（复用 dlSession 订阅模型）
func (s *Server) streamDownloadEvents(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := s.downloads.get(id)
	if !ok {
		writeErr(w, 404, errors.New("下载任务不存在"))
		return
	}
	// 已经结束：直接返回最终结果
	sess.mu.RLock()
	already := sess.finished
	sess.mu.RUnlock()
	if already {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeErr(w, 500, errors.New("response writer 不支持 flush"))
			return
		}
		var ev []byte
		sess.mu.RLock()
		finalErr := sess.finalErr
		result := sess.result
		sess.mu.RUnlock()
		if finalErr != nil {
			ev = formatDLEvent("done", map[string]any{"ok": false, "error": finalErr.Error()})
		} else {
			ev = formatDLEvent("done", map[string]any{"ok": true, "downloads": result, "folder": sess.Folder})
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
		writeErr(w, 500, errors.New("response writer 不支持 flush"))
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
	if !s.downloads.cancel(id) {
		writeErr(w, 404, errors.New("下载任务不存在"))
		return
	}
	s.audit.Write("files.download", "id", id, "result", "ok", "stage", "cancel")
	writeJSON(w, 200, map[string]any{"ok": true, "id": id})
}
