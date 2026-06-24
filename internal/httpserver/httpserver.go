// Package httpserver 负责 HTTP 路由、静态资源、以及所有 /api/* 端点。
//
// 安全：
//   - 只监听 127.0.0.1（由 main.go 保证）；
//   - 只接受来自同源 / 本地的请求（不做 CSRF 复杂校验，但禁止跨域）；
//   - 所有入参都做合法性校验后再传给 sshclient / sftpclient。
package httpserver

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"ops-toolbox/internal/audit"
	"ops-toolbox/internal/config"
	"ops-toolbox/internal/dlmanager"
	"ops-toolbox/internal/tailmgr"
)

// SSH Dial 超时（统一规范，所有 handler 都用这一对）
//
// 设计：
//   - sshDialOuterTimeout 包住"3 套 SSH profile 全跑完"的最坏耗时。
//     在 OpenSSH 6.2p2 + 自动 fallback 路径下，每套 profile 握手
//     可能在 12s 量级（老弱网络 + 老 sshd），3 套串联最坏 ≈ 36s，
//     外层给 45s 留 buffer。
//   - sshAttemptTimeout 是单次 profile 握手 deadline，由 sshclient 内部
//     用于 SetDeadline，握手完成后立即清掉。
//
// **禁止**：在 handler 里再传 ad-hoc 超时（如 15s / 30s），
// 否则老 sshd 自动 fallback 跑到一半会被外层 ctx 干掉。
const (
	sshDialOuterTimeout = 45 * time.Second
	sshAttemptTimeout   = 10 * time.Second
)

// Server 持有配置（线程安全 Manager）、审计日志、嵌入式静态资源、tail 会话池、下载任务池
type Server struct {
	cfg       *config.Manager
	audit     *audit.Logger
	webRoot   fs.FS
	tails     *tailmgr.Manager
	downloads *dlmanager.Manager
}

// New 构造一个 Server
func New(cfg *config.Manager, a *audit.Logger, webRoot fs.FS, tails *tailmgr.Manager) *Server {
	return &Server{cfg: cfg, audit: a, webRoot: webRoot, tails: tails, downloads: dlmanager.New()}
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
	case path == "/preview.html":
		s.serveStatic(w, r, "preview.html")
	case strings.HasPrefix(path, "/static/"):
		s.serveStatic(w, r, strings.TrimPrefix(path, "/static/"))
	case path == "/api/config":
		s.handleConfig(w, r)
	case path == "/api/ssh/test":
		s.handleSSHTest(w, r)
	case path == "/api/logs/list":
		s.handleLogsList(w, r)
	case path == "/api/logs/list/targets":
		s.handleLogsListTargets(w, r)
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
	case path == "/api/audit/export.csv":
		s.handleAuditExportCSV(w, r)
	case path == "/api/audit/export.json":
		s.handleAuditExportJSON(w, r)
	case path == "/api/diagnostics":
		s.handleDiagnostics(w, r)
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
	case path == "/api/format/yaml":
		s.handleFormatYAML(w, r)
	case path == "/api/format/url-form":
		s.handleFormatURLForm(w, r)
	case path == "/api/admin/servers":
		s.handleAdminServers(w, r)
	case path == "/api/http/cases":
		s.handleHTTPCases(w, r)
	case path == "/api/http/envs":
		s.handleHTTPEnvs(w, r)
	case path == "/api/http/request":
		s.handleHTTPRequest(w, r)
	case path == "/api/files/list":
		s.handleFilesList(w, r)
	case path == "/api/files/preview":
		s.handleFilesPreview(w, r)
	case path == "/api/files/download":
		s.handleFilesDownload(w, r)
	case path == "/api/local/reveal-file":
		s.handleLocalReveal(w, r)
	case path == "/api/preferences":
		s.handlePreferences(w, r)
	case path == "/api/local/open-folder":
		s.handleLocalOpenFolder(w, r)
	case path == "/api/local/open-with":
		s.handleLocalOpenWith(w, r)
	case path == "/api/admin/openers":
		s.handleAdminOpeners(w, r)
	case strings.HasPrefix(path, "/api/files/download/"):
		s.handleFilesDownloadEventsOrCancel(w, r)
	// 注意：/api/logs/download-latest 必须在 /api/logs/download/ 之前匹配（精确匹配优先）
	case strings.HasPrefix(path, "/api/logs/download/"):
		s.handleLogsDownloadEventsOrCancel(w, r)
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

// 通用辅助（writeJSON / writeErr）见 response.go。

// ---------- /api/config ----------

// configView 是 /api/config 的响应。
//
// Paths 是为前端展示而算出的绝对路径（download_dir / log_dir / data_dir），
// 前端可在 sidebar 或下载结果卡显示"文件落到 D:\...\downloads"之类。
// 原 App 字段保留 yaml 里的相对路径（用于配置回写）。
type configView struct {
	App     config.AppConfig      `json:"app"`
	Systems []config.SystemConfig `json:"systems"`
	Search  config.SearchConfig   `json:"search"`
	Paths   configViewPaths       `json:"paths"`
}

type configViewPaths struct {
	DownloadDir string `json:"download_dir"`
	LogDir      string `json:"log_dir"`
	DataDir     string `json:"data_dir"`
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
		Paths: configViewPaths{
			DownloadDir: cur.DownloadDir(),
			LogDir:      cur.LogDir(),
			DataDir:     cur.DataDir(),
		},
	})
}
