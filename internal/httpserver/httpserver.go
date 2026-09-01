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
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	urlpkg "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kairo/internal/audit"
	"kairo/internal/config"
	"kairo/internal/dbconsole"
	"kairo/internal/dlmanager"
	"kairo/internal/downloads"
	"kairo/internal/license"
	"kairo/internal/note"
	"kairo/internal/pet"
	"kairo/internal/preferences"
	"kairo/internal/reminder"
	"kairo/internal/schedtask"
	"kairo/internal/sshshell"
	"kairo/internal/tailmgr"
	"kairo/internal/webservice"
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
	authCookieName = "kairo_token"
	authCookieTTL  = 30 * 24 * time.Hour
	authHeaderName = "Authorization"
	authQueryParam = "token"
)

// Version / BuildTime 可在构建时通过 ldflags 覆盖，例如：
//
//	go build -ldflags "-X 'kairo/internal/httpserver.Version=v0.18' \
//	  -X 'kairo/internal/httpserver.BuildTime=2026-09-01T00:00:00Z'" .
//
// 默认值是哨兵 "dev"。未注入 ldflags 时，main.go 把仓库根目录 VERSION
// 文件（go:embed）填进来；发版脚本也会用同一份 VERSION 做 ldflags。
// 前端页脚 / 关于页只读 GET /api/config 的 version，不再硬编码产品版本。
// 人类发版只改 VERSION；README 徽章和 package.json 用
// `node scripts/sync-version.js` 派生，`node scripts/check-version.js` 守门。
var (
	Version   = "dev"
	BuildTime = "unknown"
)

const embeddedVersionSentinel = "dev"

// ApplyEmbeddedVersion 在 Version 仍是哨兵时，用 VERSION 文件内容填上。
// ldflags 已写入真实版本时不会覆盖。
func ApplyEmbeddedVersion(raw string) {
	Version = applyEmbeddedVersion(Version, raw)
}

func applyEmbeddedVersion(current, raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return current
	}
	if current == "" || current == embeddedVersionSentinel {
		return v
	}
	return current
}

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

// Server 持有配置（线程安全 Manager）、审计日志、嵌入式静态资源、tail 会话池、下载任务池、SSH shell 会话池
type Server struct {
	cfg          *config.Manager
	audit        *audit.Logger
	webRoot      fs.FS
	tails        *tailmgr.Manager
	downloads    *dlmanager.Manager
	shells       *sshshell.Manager
	uploadStates *uploadStateMap // v1.1：SSH/SFTP 上传会话（独立于 downloads）
	compares     *compareJobManager
	database     *dbconsole.Manager
	databaseErr  error
	preferences  *preferences.Store

	// Optional application services are supplied together through Dependencies.
	reminders *reminder.Manager
	notes     *note.Manager

	// 定时任务：可空（nil 时 /api/tasks 返回 503）。SetTasks 在 main.go 启动 schedtask.Manager 后注入。
	tasks *schedtask.Manager

	// v0.16 宠物彩蛋：可空（nil 时 /api/pet/* 返回 404）。SetPet 在 main.go 构造 pet.Engine 后注入。
	pet *pet.Engine

	// v0.12 WebService 调试中心：WSDL/模板/历史/Mock 的本地存储 + Mock 路由注册表。
	// Store 在 New 时按当前 data 目录构造；MockRegistry 启动后 Reload 一次让已保存的 mock 生效。
	ws      *webservice.Store
	wsMocks *webservice.MockRegistry

	skipLicenseCheck bool // 测试专用: 跳过 license 网关

	cleanupMu sync.Mutex // 防止并发执行清理任务
}

// Dependencies declares optional application services at construction time.
// It replaces order-dependent SetX calls after the server has been built.
type Dependencies struct {
	Reminders *reminder.Manager
	Notes     *note.Manager
	Tasks     *schedtask.Manager
	Pet       *pet.Engine
}

// New 构造一个 Server
func New(cfg *config.Manager, a *audit.Logger, webRoot fs.FS, tails *tailmgr.Manager, shells *sshshell.Manager, deps Dependencies) *Server {
	wsStore := webservice.NewStore(cfg.Get().DataDir())
	wsMocks := webservice.NewMockRegistry(wsStore)
	// 启动时加载已保存的 mock 路由（失败只记日志，不阻断启动）
	if err := wsMocks.Reload(); err != nil {
		// 不能用 log 包（会污染测试输出），写 audit 文件 + stderr 便于 GUI 模式可见
		fmt.Fprintln(os.Stderr, "WARN: 加载已保存的 mock 路由失败:", err)
		a.Write("webservice.mock.reload", "result", "fail", "error", err.Error())
	}
	database, databaseErr := dbconsole.NewManager(cfg.Get().DataDir())
	if databaseErr != nil {
		fmt.Fprintln(os.Stderr, "WARN: 初始化数据库工作台失败:", databaseErr)
		a.Write("database.init", "result", "fail", "error", databaseErr.Error())
	}
	return &Server{
		cfg: cfg, audit: a, webRoot: webRoot,
		tails: tails, downloads: dlmanager.New(), shells: shells,
		ws: wsStore, wsMocks: wsMocks,
		uploadStates: newUploadStateMap(),
		compares:     newCompareJobManager(),
		database:     database,
		databaseErr:  databaseErr,
		preferences:  preferences.NewStore(filepath.Join(cfg.Get().DataDir(), "preferences.json")),
		reminders:    deps.Reminders,
		notes:        deps.Notes,
		tasks:        deps.Tasks,
		pet:          deps.Pet,
	}
}

// CloseDatabase drains database/sql and Redis pools. Main calls this during a
// normal shutdown; abrupt process exits are still safe because no source data
// lives inside the pools.
func (s *Server) CloseDatabase() error {
	if s.database == nil {
		return nil
	}
	return s.database.Close()
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

	s.cleanupEditTempFiles()
}

// StartPeriodicCleanup 启动下载清理：启动时立即清理一次 + 每 1 小时清理一次。
//
// 通常在 main.go 中 HTTP 服务启动前调用。
// 每次下载完成后也会触发检查（见 TriggerCleanup）。
//
// v1.0 行为变更：
//   - 旧实现 sleep 500ms 再触发，意图是"等服务初始化完"。
//     但 sleep 期间 Sync.Mutex 持有逻辑会让首次 /api/downloads/list 在 500ms 内阻塞；
//     实际上 TriggerCleanup 是同步执行，初始化阶段不会冲突；
//     改成"启动后立即触发 + 1h 周期"，减少首请求延迟。
//   - 清理本身仍是同步执行，cleanupMu 防止并发。
func (s *Server) StartPeriodicCleanup() {
	go func() {
		// 启动立即清理一次（不 sleep）
		s.TriggerCleanup()
		// 每小时清理一次
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			s.TriggerCleanup()
		}
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
		origin := r.Header.Get("Origin")
		referer := r.Header.Get("Referer")
		// 记录现场，便于定位「Origin/Host 不一致」到底是哪种访问方式触发的。
		// 只打 origin/referer/host/path，不含 token、body 等敏感信息。
		log.Printf("[cors] 拒绝跨源请求: path=%s origin=%q referer=%q host=%q", path, origin, referer, r.Host)
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":  "拒绝跨源请求：Origin 与 Host 不一致，已阻止访问",
			"origin": origin,
			"host":   r.Host,
		})
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
			w.Header().Set("WWW-Authenticate", `Bearer realm="kairo"`)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "需要认证", "auth_required": true})
			return
		}
		token := cur.Auth.LookupToken(tokenStr)
		if token == nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="kairo"`)
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

	// License 网关: 未激活时阻断所有 /api/* 功能端点。
	// 豁免: /api/license/* (激活流程本身) 和 /api/auth/* (已在上方处理)。
	// 静态资源 (/, /static/, /preview.html) 不受影响 → 前端能加载、弹激活框。
	if !s.skipLicenseCheck && isAPIRequest(path) && !strings.HasPrefix(path, "/api/license/") {
		if err := license.Check(); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":            "未激活，请先输入激活码",
				"license_required": true,
			})
			return
		}
	}

	switch {
	case path == "/" || path == "/index.html":
		s.serveStatic(w, r, "index.html")
	case path == "/preview.html":
		s.serveStatic(w, r, "preview.html")
	// SSH 终端独立窗口（v0.10+）：从主页 SSH 工具栏「⛶新窗口」点过来，
	// 走 /ssh.html 直接渲染，避免被 index.html 的 hash router 拦截。
	case path == "/ssh.html":
		s.serveStatic(w, r, "ssh.html")
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
	case path == "/api/ssh/shell/ws":
		s.handleSSHShellWS(w, r)
	// SSH 终端 tab 内嵌 SFTP 浏览器（v0.11+）：list / pwd / preview / download
	// 独立命名空间 /api/ssh/sftp/*，不走 free_file_roots 白名单，
	// 访问控制由 SSH 账号权限承担（用户在 SSH 终端已能 cd 到任何路径）。
	case path == "/api/ssh/sftp/list":
		s.handleSshSftpList(w, r)
	case path == "/api/ssh/sftp/pwd":
		s.handleSshSftpPwd(w, r)
	case path == "/api/ssh/sftp/preview":
		s.handleSshSftpPreview(w, r)
	case path == "/api/ssh/sftp/download":
		s.handleSshSftpDownload(w, r)
	case path == "/api/ssh/sftp/edit":
		s.handleSshSftpEdit(w, r)
	case path == "/api/ssh/sftp/upload/init":
		s.handleSshSftpUploadInit(w, r)
	case path == "/api/ssh/sftp/upload/cancel":
		s.handleSshSftpUploadCancel(w, r)
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
	case path == "/api/http/curl-parse":
		s.handleHTTPCurlParse(w, r)
	case path == "/api/http/ws/connect":
		s.handleHTTPWsConnect(w, r)
	case path == "/api/http/ws/send":
		s.handleHTTPWsSend(w, r)
	case path == "/api/http/ws/poll":
		s.handleHTTPWsPoll(w, r)
	case path == "/api/http/ws/close":
		s.handleHTTPWsClose(w, r)
	// v0.12 WebService 调试中心：WSDL 导入 / SOAP 生成发送 / 模板 / 历史 / Mock / XML 辅助。
	// 路由分发统一进 handleWSDispatch，按 path 前缀细化（见 handlers_webservice.go）。
	case strings.HasPrefix(path, "/api/wsdl/"),
		strings.HasPrefix(path, "/api/soap/"),
		strings.HasPrefix(path, "/api/ws/xml/"):
		s.handleWSDispatch(w, r)
	case strings.HasPrefix(path, "/api/wscodegen/"):
		s.handleWSCodegenDispatch(w, r)
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
	case strings.HasPrefix(path, "/api/preferences/"):
		s.handlePreferenceNamespace(w, r)
	case path == "/api/local/open-folder":
		s.handleLocalOpenFolder(w, r)
	case path == "/api/local/open-with":
		s.handleLocalOpenWith(w, r)
	case path == "/api/choose-file":
		s.handleChooseFile(w, r)
	case path == "/api/choose-dir":
		s.handleChooseDir(w, r)
	case path == "/api/waspack/preview":
		s.handleWASPackPreview(w, r)
	case path == "/api/waspack/build":
		s.handleWASPackBuild(w, r)
	case path == "/api/waspack/extract":
		s.handleWASPackExtract(w, r)
	case path == "/api/waspack/package":
		s.handleWASPackPackage(w, r)
	case path == "/api/waspack/open":
		s.handleWASPackOpen(w, r)
	case path == "/api/compare/folder-scan":
		s.handleCompareFolderScan(w, r)
	case path == "/api/compare/deep-check":
		s.handleCompareDeepCheck(w, r)
	case path == "/api/compare/file-diff":
		s.handleCompareFileDiff(w, r)
	case path == "/api/compare/connections":
		s.handleCompareConnections(w, r)
	case path == "/api/compare/list":
		s.handleCompareList(w, r)
	case path == "/api/compare/read":
		s.handleCompareRead(w, r)
	case path == "/api/compare/write":
		s.handleCompareWrite(w, r)
	case path == "/api/compare/copy":
		s.handleCompareCopy(w, r)
	case path == "/api/compare/sync":
		s.handleCompareSync(w, r)
	case path == "/api/compare/sync/start":
		s.handleCompareSyncStart(w, r)
	case path == "/api/compare/scan":
		s.handleCompareScanStart(w, r)
	case path == "/api/compare/test":
		s.handleCompareTest(w, r)
	case strings.HasPrefix(path, "/api/compare/jobs/"):
		s.handleCompareJob(w, r)
	case strings.HasPrefix(path, "/api/database/"):
		s.handleDatabaseDispatch(w, r)
	case path == "/api/license/status":
		s.handleLicenseStatus(w, r)
	case path == "/api/license/activate":
		s.handleLicenseActivate(w, r)
	case path == "/api/sponsor/leaderboard":
		// v0.14: 投喂作者排行榜
		s.handleSponsorLeaderboard(w, r)
	case strings.HasPrefix(path, "/api/pet/"):
		// v0.16: 宠物彩蛋（状态/解锁/改名/位置/皮肤/同步/榜单）
		s.handlePetDispatch(w, r)
	case path == "/api/admin/openers":
		// BE-003：管理员接口，仅 admin 角色。
		if !requireAdmin(w, r) {
			return
		}
		s.handleAdminOpeners(w, r)
	case path == "/api/admin/openers/extract-icon":
		// 预览 exe 图标，仅 admin 角色（防止任意路径探测）
		if !requireAdmin(w, r) {
			return
		}
		s.handleAdminOpenersExtractIcon(w, r)
	case path == "/api/local/opener-icon":
		// 读取已缓存的 opener 图标（GET，方便 <img src> 直接引用）
		s.handleLocalOpenerIcon(w, r)
	case path == "/api/admin/download-retention":
		// BE-003：管理员接口，仅 admin 角色。
		if !requireAdmin(w, r) {
			return
		}
		s.handleAdminDownloadRetention(w, r)
	case path == "/api/admin/autostart":
		// v1.0：自启开关。GET 不限角色（状态不敏感），PUT 限 admin。
		// 鉴权放在 handler 里（GET/PUT 分支各自判断），保持和现有 admin 路由风格一致。
		s.handleAdminAutoStart(w, r)
	case strings.HasPrefix(path, "/api/files/download/"):
		s.handleFilesDownloadEventsOrCancel(w, r)
	case strings.HasPrefix(path, "/api/ssh/sftp/download/"):
		s.handleSshSftpDownloadEventsOrCancel(w, r)
	case strings.HasPrefix(path, "/api/ssh/sftp/edit/"):
		s.handleSshSftpEditEvents(w, r)
	case strings.HasPrefix(path, "/api/ssh/sftp/upload/") && strings.HasSuffix(path, "/data"):
		s.handleSshSftpUploadData(w, r)
	// 注意：/api/logs/download-latest 必须在 /api/logs/download/ 之前匹配（精确匹配优先）
	case strings.HasPrefix(path, "/api/logs/download/"):
		s.handleLogsDownloadEventsOrCancel(w, r)
	case strings.HasPrefix(path, "/api/logs/tail/"):
		s.handleTailEventsOrStop(w, r)
	case strings.HasPrefix(path, "/api/notes"):
		s.handleNotesDispatch(w, r)
	// v1.0 便笺提醒：增删改查 / 启用切换 / 立即触发 / 元信息。
	// 统一进 handleReminderDispatch 收口，按 path 后缀再分发。
	case strings.HasPrefix(path, "/api/reminders"):
		s.handleReminderDispatch(w, r)
	// 定时任务：增删改查 / 启停 / 立即执行 / 运行历史。统一进 handleTaskDispatch 收口。
	case strings.HasPrefix(path, "/api/tasks"):
		s.handleTaskDispatch(w, r)
	case strings.HasPrefix(path, "/downloads/"):
		s.serveDownload(w, r)
	case strings.HasPrefix(path, "/mock/"):
		// v0.12 Mock WebService：外部系统直接请求 /mock/xxx，不走 /api/ 鉴权与 license 网关
		// （mock 地址是给别人调用的，不能要求带本工具箱的 token）。
		s.wsMocks.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func allowLocalOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		// 桌面工具（Postman/Apifox 等）或 file:// 页面会发字面量 "null"，
		// 这不是真实跨站源（URL 无法解析）。仅当请求目标是本机地址时放行；
		// 远程站点经 sandbox iframe 打本机 IP 时 Host 非 loopback，仍会被拦。
		if origin == "null" {
			return isLocalRequestHost(r.Host)
		}
		return isAllowedOrigin(origin, r.Host)
	}
	referer := strings.TrimSpace(r.Header.Get("Referer"))
	if referer != "" {
		return isAllowedOrigin(referer, r.Host)
	}
	return true
}

// isLocalRequestHost 判断请求 Host 是否本机地址（127.0.0.1 / localhost / ::1）。
func isLocalRequestHost(requestHost string) bool {
	h := strings.ToLower(strings.Trim(requestHost, "[]"))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return isLocalWebHost(h)
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
	App       config.AppConfig      `json:"app"`
	Systems   []config.SystemConfig `json:"systems"`
	Search    config.SearchConfig   `json:"search"`
	Auth      configAuthView        `json:"auth"`
	Paths     configViewPaths       `json:"paths"`
	Version   string                `json:"version"`
	BuildTime string                `json:"build_time,omitempty"`
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
		App:     sanitizeAppConfig(cur.App),
		Systems: cur.Systems,
		Search:  cur.Search,
		Auth:    authView,
		Paths: configViewPaths{
			DownloadDir: cur.DownloadDir(),
			LogDir:      cur.LogDir(),
			DataDir:     cur.DataDir(),
		},
		Version:   Version,
		BuildTime: BuildTime,
	})
}

// sanitizeAppConfig 返回一份去除敏感字段的 AppConfig 副本，供 /api/config 等
// 对前端输出配置时使用。CredentialKey 是 file 模式凭据加密密钥，KairoInternalToken
// 是 license 开发绕过 token，均不得下发给前端。
func sanitizeAppConfig(a config.AppConfig) config.AppConfig {
	a.CredentialKey = ""
	a.KairoInternalToken = ""
	return a
}
