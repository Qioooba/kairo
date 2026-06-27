// Package httpserver 负责 HTTP 路由、静态资源、以及所有 /api/* 端点。
//
// 安全：
//   - 只监听 127.0.0.1（由 main.go 保证）；
//   - 只接受来自同源 / 本地的请求（不做 CSRF 复杂校验，但禁止跨域）；
//   - 所有入参都做合法性校验后再传给 sshclient / sftpclient。
package httpserver

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	urlpkg "net/url"
	"strings"
	"sync"
	"time"

	"ops-toolbox/internal/audit"
	"ops-toolbox/internal/config"
	"ops-toolbox/internal/dlmanager"
	"ops-toolbox/internal/downloads"
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

const (
	authCookieName = "otb_token"
	authCookieTTL  = 30 * 24 * time.Hour
	authHeaderName = "Authorization"
	authQueryParam = "token"
)

type contextKey string

const authUserKey contextKey = "authUser"

type authUser struct {
	Name  string
	Token string
	Role  string // BE-003: "admin" / "user"，空视为 "user"
}

// isAdmin v0.9 起（BE-003）：判断当前请求的认证用户是否为管理员角色。
func (u *authUser) isAdmin() bool {
	return u != nil && u.Role == "admin"
}

// requireAdmin v0.9 起（BE-003）：校验当前请求的认证用户是否为 admin 角色。
//   - auth 未启用时（无 authUser）→ 放行（本机 127.0.0.1 场景默认信任）
//   - auth 启用 + 非 admin 角色 → 返回 403 并写 false（拒绝）
//   - auth 启用 + admin 角色 → 返回 true（放行）
//
// 注意：auth 未启用时放行是向后兼容设计；如需强制 admin，
// 在 config.yaml 里启用 auth 并配置 admin token。
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	u, _ := r.Context().Value(authUserKey).(*authUser)
	if u == nil {
		// auth 未启用 → 放行
		return true
	}
	if !u.isAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "需要管理员权限（token.role=admin）",
		})
		return false
	}
	return true
}

// Server 持有配置（线程安全 Manager）、审计日志、嵌入式静态资源、tail 会话池、下载任务池
type Server struct {
	cfg       *config.Manager
	audit     *audit.Logger
	webRoot   fs.FS
	tails     *tailmgr.Manager
	downloads *dlmanager.Manager

	cleanupMu sync.Mutex // 防止并发执行清理任务
}

// New 构造一个 Server
func New(cfg *config.Manager, a *audit.Logger, webRoot fs.FS, tails *tailmgr.Manager) *Server {
	return &Server{cfg: cfg, audit: a, webRoot: webRoot, tails: tails, downloads: dlmanager.New()}
}

// TriggerCleanup 触发一次下载清理（同步执行）。
// 并发安全：同一时间只有一个清理 goroutine 在跑。
func (s *Server) TriggerCleanup() {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()

	cur := s.cur()
	retentionDays := cur.App.DownloadRetentionDaysEffective()
	maxCount := cur.App.DownloadMaxCountEffective()

	if retentionDays == 0 && maxCount == 0 {
		return // 两个都配 0，不清理
	}

	result := downloads.Cleanup(cur.DownloadDir(), retentionDays, maxCount)
	if result.TotalDeleted > 0 || len(result.Errors) > 0 {
		s.audit.Write("downloads.cleanup",
			"result", "ok",
			"retention_days", retentionDays,
			"max_count", maxCount,
			"deleted_by_age", result.DeletedByAge,
			"deleted_by_count", result.DeletedByCount,
			"total_deleted", result.TotalDeleted,
			"errors", len(result.Errors),
		)
	}
}

// StartPeriodicCleanup 启动下载清理：启动时清理一次。
//
// 通常在 main.go 中 HTTP 服务启动前调用。
// 每次下载完成后也会触发检查（见 TriggerCleanup）。
func (s *Server) StartPeriodicCleanup() {
	// 启动时立即清理一次
	go func() {
		time.Sleep(500 * time.Millisecond) // 等服务初始化完
		s.TriggerCleanup()
	}()
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
	if strings.HasPrefix(path, "/api/") && !allowLocalOrigin(r) {
		writeErr(w, http.StatusForbidden, errors.New("拒绝跨源请求"))
		return
	}

	cur := s.cur()
	ip := clientIP(r)

	if path == "/api/auth/status" {
		s.handleAuthStatus(w, r, cur)
		return
	}
	if path == "/api/auth/logout" {
		s.handleAuthLogout(w, r)
		return
	}

	if cur.Auth.EffectiveEnabled() && isAPIRequest(path) {
		tokenStr := extractToken(r)
		if tokenStr == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ops-toolbox"`)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "需要认证", "auth_required": true})
			return
		}
		token := cur.Auth.LookupToken(tokenStr)
		if token == nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ops-toolbox"`)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "认证失败：无效的 token", "auth_required": true})
			return
		}
		if !token.IPAllowed(ip) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "访问被拒绝：IP 不在允许列表", "auth_required": true})
			return
		}
		ctx := context.WithValue(r.Context(), authUserKey, &authUser{Name: token.Name, Token: token.Token, Role: token.Role})
		r = r.WithContext(ctx)
	}

	switch {
	case path == "/" || path == "/index.html":
		s.serveStatic(w, r, "index.html")
	case path == "/preview.html":
		s.serveStatic(w, r, "preview.html")
	case strings.HasPrefix(path, "/static/"):
		s.serveStatic(w, r, strings.TrimPrefix(path, "/static/"))
	case path == "/api/config":
		s.handleConfig(w, r)
	case path == "/api/config/export":
		s.handleConfigExport(w, r)
	case path == "/api/config/import":
		// BE-003：导入配置是敏感写接口，仅 admin 角色。
		if !requireAdmin(w, r) {
			return
		}
		s.handleConfigImport(w, r)
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
	case path == "/api/diagnostics":
		s.handleDiagnostics(w, r)
	case path == "/api/credentials/save":
		s.handleCredSave(w, r)
	case path == "/api/credentials/has":
		s.handleCredHas(w, r)
	case path == "/api/credentials/clear":
		// BE-003：清空他人凭据是敏感写接口，仅 admin 角色。
		if !requireAdmin(w, r) {
			return
		}
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
	case path == "/api/format/timestamp":
		s.handleFormatTimestamp(w, r)
	case path == "/api/format/cron-parse":
		s.handleFormatCronParse(w, r)
	case path == "/api/format/jsonpath":
		s.handleFormatJSONPath(w, r)
	case path == "/api/diff/compare":
		s.handleDiffCompare(w, r)
	case path == "/api/admin/servers":
		// BE-003：管理员接口，仅 admin 角色。
		if !requireAdmin(w, r) {
			return
		}
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
	case path == "/api/choose-file":
		s.handleChooseFile(w, r)
	case path == "/api/choose-dir":
		s.handleChooseDir(w, r)
	case path == "/api/compare/folder-scan":
		s.handleCompareFolderScan(w, r)
	case path == "/api/compare/file-diff":
		s.handleCompareFileDiff(w, r)
	case path == "/api/admin/openers":
		// BE-003：管理员接口，仅 admin 角色。
		if !requireAdmin(w, r) {
			return
		}
		s.handleAdminOpeners(w, r)
	case path == "/api/admin/download-retention":
		// BE-003：管理员接口，仅 admin 角色。
		if !requireAdmin(w, r) {
			return
		}
		s.handleAdminDownloadRetention(w, r)
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

func allowLocalOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		return isAllowedOrigin(origin, r.Host)
	}
	referer := strings.TrimSpace(r.Header.Get("Referer"))
	if referer != "" {
		return isAllowedOrigin(referer, r.Host)
	}
	return true
}

func isAllowedOrigin(raw string, requestHost string) bool {
	u, err := urlpkg.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if isLocalWebHost(host) {
		return true
	}
	reqHost := strings.ToLower(strings.Trim(requestHost, "[]"))
	if reqHost != "" {
		if h, _, err := net.SplitHostPort(reqHost); err == nil {
			reqHost = h
		}
		if strings.EqualFold(host, reqHost) {
			return true
		}
	}
	return false
}

func isLocalWebHost(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "localhost" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func isAPIRequest(path string) bool {
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/downloads/")
}

func extractToken(r *http.Request) string {
	auth := r.Header.Get(authHeaderName)
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	if cookie, err := r.Cookie(authCookieName); err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return strings.TrimSpace(r.URL.Query().Get(authQueryParam))
}

func clientIP(r *http.Request) string {
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		parts := strings.Split(xff, ",")
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip
		}
	}
	xri := r.Header.Get("X-Real-IP")
	if xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
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
	Auth    configAuthView        `json:"auth"`
	Paths   configViewPaths       `json:"paths"`
}

type configViewPaths struct {
	DownloadDir string `json:"download_dir"`
	LogDir      string `json:"log_dir"`
	DataDir     string `json:"data_dir"`
}

type configAuthView struct {
	Enabled bool             `json:"enabled"`
	Users   []configAuthUser `json:"users,omitempty"`
}

type configAuthUser struct {
	Name string `json:"name"`
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	cur := s.cur()
	authView := configAuthView{Enabled: cur.Auth.EffectiveEnabled()}
	if cur.Auth.EffectiveEnabled() {
		for _, t := range cur.Auth.Tokens {
			authView.Users = append(authView.Users, configAuthUser{Name: t.Name})
		}
	}
	writeJSON(w, 200, configView{
		App:     cur.App,
		Systems: cur.Systems,
		Search:  cur.Search,
		Auth:    authView,
		Paths: configViewPaths{
			DownloadDir: cur.DownloadDir(),
			LogDir:      cur.LogDir(),
			DataDir:     cur.DataDir(),
		},
	})
}
