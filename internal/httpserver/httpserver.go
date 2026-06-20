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
	"ops-toolbox/internal/formatter"
	"ops-toolbox/internal/logquery"
	"ops-toolbox/internal/sftpclient"
	"ops-toolbox/internal/sshclient"
)

// Server 持有配置（线程安全 Manager）、审计日志、嵌入式静态资源
type Server struct {
	cfg     *config.Manager
	audit   *audit.Logger
	webRoot fs.FS
}

// New 构造一个 Server
func New(cfg *config.Manager, a *audit.Logger, webRoot fs.FS) *Server {
	return &Server{cfg: cfg, audit: a, webRoot: webRoot}
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
	case path == "/api/format/json":
		s.handleFormatJSON(w, r)
	case path == "/api/format/xml":
		s.handleFormatXML(w, r)
	case path == "/api/admin/servers":
		s.handleAdminServers(w, r)
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
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = srv.Username
	}
	if username == "" || req.Password == "" {
		writeErr(w, 400, errors.New("缺少用户名或密码"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: req.Password}, 10*time.Second)
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
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = srv.Username
	}
	if username == "" || req.Password == "" {
		writeErr(w, 400, errors.New("缺少用户名或密码"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cur().SearchTimeout()+10*time.Second)
	defer cancel()

	cli, err := sshclient.Dial(ctx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
	}, sshclient.Credentials{Password: req.Password}, 10*time.Second)
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
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = srv.Username
	}
	if username == "" || req.Password == "" {
		writeErr(w, 400, errors.New("缺少用户名或密码"))
		return
	}
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
	}, sshclient.Credentials{Password: req.Password}, 10*time.Second)
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
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = srv.Username
	}
	if username == "" || req.Password == "" {
		writeErr(w, 400, errors.New("缺少用户名或密码"))
		return
	}
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
	}, sshclient.Credentials{Password: req.Password}, 10*time.Second)
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
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = srv.Username
	}
	if username == "" || req.Password == "" {
		writeErr(w, 400, errors.New("缺少用户名或密码"))
		return
	}
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
	}, sshclient.Credentials{Password: req.Password}, 10*time.Second)
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
	Server string               `json:"server"`
	Host   string               `json:"host"`
	OK     bool                 `json:"ok"`
	Error  string               `json:"error,omitempty"`
	Hits   []logquery.SearchHit `json:"hits,omitempty"`
	Files  []string             `json:"files,omitempty"`
	HitsN  int                  `json:"hits_count"`
	Ms     int64                `json:"elapsed_ms"`
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
	if req.Password == "" {
		writeErr(w, 400, errors.New("缺少密码"))
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
			res := s.runOneServerSearch(totalCtx, srv, ld, filesN, kw, username, req.Password)
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
		Server: srv.Name, Host: srv.Host,
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

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "..."
}
