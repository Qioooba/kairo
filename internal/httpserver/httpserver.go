// Package httpserver 负责 HTTP 路由、静态资源、以及所有 /api/* 端点。
//
// 安全：
//   - 只监听 127.0.0.1（由 main.go 保证）；
//   - 只接受来自同源 / 本地的请求（不做 CSRF 复杂校验，但禁止跨域）；
//   - 所有入参都做合法性校验后再传给 sshclient / sftpclient。
package httpserver

import (
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
	"time"

	"ops-toolbox/internal/audit"
	"ops-toolbox/internal/config"
	"ops-toolbox/internal/formatter"
	"ops-toolbox/internal/logquery"
	"ops-toolbox/internal/sftpclient"
	"ops-toolbox/internal/sshclient"
)

// Server 持有配置、审计日志、嵌入式静态资源
type Server struct {
	cfg     *config.Config
	audit   *audit.Logger
	webRoot fs.FS
}

// New 构造一个 Server
func New(cfg *config.Config, a *audit.Logger, webRoot fs.FS) *Server {
	return &Server{cfg: cfg, audit: a, webRoot: webRoot}
}

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
	case path == "/api/logs/context":
		s.handleLogsContext(w, r)
	case path == "/api/format/json":
		s.handleFormatJSON(w, r)
	case path == "/api/format/xml":
		s.handleFormatXML(w, r)
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
	writeJSON(w, 200, configView{
		App:     s.cfg.App,
		Systems: s.cfg.Systems,
		Search:  s.cfg.Search,
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
	_, srv, ok := s.cfg.FindServer(req.System, req.Server)
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
	_, srv, ok := s.cfg.FindServer(req.System, req.Server)
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

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.SearchTimeout()+10*time.Second)
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
	stdout, stderr, code, err := cli.Run(ctx, cmd, s.cfg.SearchTimeout(), ld.Encoding)
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
	_, srv, ok := s.cfg.FindServer(req.System, req.Server)
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
	stdout, stderr, code, err := cli.Run(ctx, cmd, s.cfg.SearchTimeout(), ld.Encoding)
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

	results := make([]map[string]string, 0, latest)
	now := time.Now()
	dateDir := now.Format("20060102")
	hhmm := now.Format("150405")
	// 目录别名：把 log_dir.path 的最后一段当作"dirName"，让多服务器同名日志也能区分
	dirAlias := filepath.Base(ld.Path)
	// 下载文件落点：downloads/YYYYMMDD/serverName_dirName_originalName_HHMMSS.log
	// （多台服务器同时下载同名 SystemOut.log 时不会互相覆盖）
	targetDir := filepath.Join(s.cfg.DownloadDir(), dateDir)
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
		results = append(results, map[string]string{
			"file":   f.Name,
			"local":  localName,
			"bytes":  strconv.FormatInt(bytes, 10),
			"remote": remote,
			"date":   dateDir,
		})
		s.audit.Write("logs.download", "system", req.System, "server", req.Server, "dir", ld.Path, "file", f.Name, "result", "ok", "bytes", bytes)
	}
	writeJSON(w, 200, map[string]any{
		"downloads": results,
		"folder":    s.cfg.DownloadDir(),
	})
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
	_, srv, ok := s.cfg.FindServer(req.System, req.Server)
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
		filesN = s.cfg.Search.DefaultLatestFiles
	}
	if filesN > 10 {
		filesN = 10
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.SearchTimeout()+15*time.Second)
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
	stdout, stderr, code, err := cli.Run(ctx, cmd, s.cfg.SearchTimeout(), ld.Encoding)
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

	cmd, err = logquery.SearchCommand(ld.Path, fileNames, kw, s.cfg.Search.MaxMatches, s.cfg.Search.TimeoutSeconds, ld.Encoding)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	stdout, stderr, code, err = cli.Run(ctx, cmd, s.cfg.SearchTimeout()+5*time.Second, ld.Encoding)
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
	_, srv, ok := s.cfg.FindServer(req.System, req.Server)
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
		before = s.cfg.Search.DefaultContextLines
	}
	after := req.After
	if after <= 0 {
		after = s.cfg.Search.DefaultContextLines
	}
	if before > 500 {
		before = 500
	}
	if after > 500 {
		after = 500
	}
	cmd, err := logquery.ContextCommand(ld.Path, req.File, req.Line, before, after, s.cfg.Search.TimeoutSeconds)
	if err != nil {
		writeErr(w, 400, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.SearchTimeout()+10*time.Second)
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

	stdout, stderr, code, err := cli.Run(ctx, cmd, s.cfg.SearchTimeout(), ld.Encoding)
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

// ---------- /downloads/<file> ----------

func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/downloads/")
	if name == "" || strings.Contains(name, "..") || strings.Contains(name, "\\") || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.cfg.DownloadDir(), filepath.FromSlash(name))
	// 必须在 DownloadDir 下
	abs, err := filepath.Abs(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	base, err := filepath.Abs(s.cfg.DownloadDir())
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
