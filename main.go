// Kairo - Kairo入口
//
// 启动本地 HTTP 服务，默认监听 127.0.0.1:18080，
// 启动后自动打开浏览器访问首页。
package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"kairo/internal/audit"
	"kairo/internal/browserpref"
	"kairo/internal/config"
	"kairo/internal/credentials"
	"kairo/internal/desknote"
	"kairo/internal/deskpet"
	"kairo/internal/downloads"
	"kairo/internal/httpserver"
	"kairo/internal/license"
	"kairo/internal/note"
	"kairo/internal/pet"
	"kairo/internal/popup"
	"kairo/internal/reminder"
	"kairo/internal/schedtask"
	"kairo/internal/sponsor"
	"kairo/internal/sshclient"
	"kairo/internal/sshshell"
	"kairo/internal/sysutil"
	"kairo/internal/tailmgr"
	"kairo/internal/tray"
	"kairo/internal/upgrade"
	"kairo/internal/winui"
)

//go:embed web
var webFS embed.FS

//go:embed VERSION
var versionFile string

// config.yaml 是内网发行版的首次启动模板。运行时只写用户配置目录中的副本，
// 后续升级 EXE 不会覆盖用户已经生成的配置。
//
//go:embed config.yaml
var bootstrapConfigFile []byte

func init() {
	httpserver.ApplyEmbeddedVersion(versionFile)
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetPrefix("[Kairo] ")

	// --reset-browser 工具箱启动前的一个独立处理：删掉浏览器偏好 state，
	// 让下次正常启动重新探测。流程跟"主流程"前两段一致（定位 cfg → 加载 →
	// 解析目录），但失败直接 os.Exit(1) 而不是弹托盘错误框。
	resetBrowser := flag.Bool("reset-browser", false, "重置浏览器偏好（删除 data/browser_state.json），下次启动重新探测")
	resetConfig := flag.Bool("reset-config", false, "备份当前配置并恢复官方配置（保留全部用户数据及其目录）")
	restoreUpgrade := flag.String("restore-upgrade", "", "恢复指定的升级快照，恢复前会再次备份当前状态")
	configPath := flag.String("config", "", "显式指定唯一配置文件路径（开发、测试或定制部署）")
	portable := flag.Bool("portable", false, "便携模式：配置和运行数据放在程序目录")
	flag.Parse()
	if *resetConfig && strings.TrimSpace(*restoreUpgrade) != "" {
		tray.FatalDialogf("--reset-config 与 --restore-upgrade 不能同时使用")
	}
	location, err := config.ResolveLocation(*configPath, *portable)
	if err != nil {
		tray.FatalDialogf("定位用户配置目录失败: %v", err)
	}
	bootstrap, err := config.BootstrapDistributionConfig(bootstrapConfigFile)
	if err != nil {
		tray.FatalDialogf("准备首次启动配置失败: %v", err)
	}
	resetConfigBackup := ""
	if *resetConfig {
		resetConfigBackup, err = config.ResetToDistribution(location.ConfigPath, bootstrap)
		if err != nil {
			tray.FatalDialogf("恢复官方配置失败: %v", err)
		}
	}
	opened, err := config.PrepareForUpgrade(location.ConfigPath, bootstrap)
	if err != nil {
		tray.FatalDialogf("加载配置失败 (%s): %v", location.ConfigPath, err)
	}
	runDir, cfgPath, cfg := location.RootDir, location.ConfigPath, opened.Config

	if *resetBrowser {
		if err := cfg.ResolvePaths(runDir); err != nil {
			fmt.Fprintln(os.Stderr, "解析目录失败:", err)
			os.Exit(1)
		}
		browserpref.Init(cfg.DataDir())
		if err := browserpref.Reset(); err != nil {
			fmt.Fprintln(os.Stderr, "重置浏览器偏好失败:", err)
			os.Exit(1)
		}
		fmt.Println("浏览器偏好已重置，下次启动会重新探测。")
		return
	}

	// 3. 解析相对目录为基于用户配置目录（或 --config 所在目录）的绝对路径
	if err := cfg.ResolvePaths(runDir); err != nil {
		tray.FatalDialogf("解析目录失败: %v", err)
	}

	// 4. 准备运行时目录
	if err := cfg.EnsureDirs(); err != nil {
		tray.FatalDialogf("创建运行时目录失败: %v", err)
	}
	if snapshotPath := strings.TrimSpace(*restoreUpgrade); snapshotPath != "" {
		result, err := upgrade.RestoreSnapshot(cfg.DataDir(), snapshotPath, upgrade.DefaultAssets(cfgPath, cfg.DataDir(), cfg.DownloadDir()))
		if err != nil {
			tray.FatalDialogf("恢复升级快照失败（当前数据未改变）: %v", err)
		}
		fmt.Printf("升级快照已恢复。恢复前的当前状态已备份到 %s。请重新启动 Kairo。\n", result.SafetyBackupDir)
		return
	}

	// 4.1 设置日志文件输出。
	// Windows GUI 模式（-H windowsgui）下没有 stdout/stderr，
	// 必须落盘到 logs/kairo.log 才能看到运行时日志。
	// 非 Windows 开发模式同时输出到 stderr 方便调试。
	logFilePath := filepath.Join(cfg.LogDir(), "kairo.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		tray.FatalDialogf("无法打开日志文件 %s: %v", logFilePath, err)
	}
	defer logFile.Close()
	log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	if opened.Created {
		log.Printf("首次启动配置已创建: %s", cfgPath)
	}
	if opened.Migrated && opened.Backup != "" {
		log.Printf("配置已升级到 schema_version=%d，旧文件备份: %s", config.CurrentSchemaVersion, opened.Backup)
	}
	if *resetConfig {
		log.Printf("已恢复官方配置；用户数据目录及凭据密钥保持不变；旧配置备份: %s", resetConfigBackup)
	}

	// 所有用户数据必须先完成统一升级，再交给各模块打开。升级协调器提交清单前会
	// 快照配置、宠物、便笺、提醒、任务、偏好、凭据等已知文件；任一步失败就恢复。
	upgradeResult, err := upgrade.Run(upgrade.Options{
		DataDir:        cfg.DataDir(),
		ProductVersion: httpserver.Version,
		Assets:         upgrade.DefaultAssets(cfgPath, cfg.DataDir(), cfg.DownloadDir()),
	})
	if err != nil {
		tray.FatalDialogf("升级用户数据失败（原数据已保留）: %v", err)
	}
	if upgradeResult.Adopted {
		log.Printf("用户数据已纳入升级管理，快照: %s", upgradeResult.BackupDir)
	}
	if len(upgradeResult.Migrated) > 0 {
		log.Printf("用户数据升级完成: %v；快照: %s", upgradeResult.Migrated, upgradeResult.BackupDir)
	}
	for _, warning := range upgradeResult.Warnings {
		log.Printf("WARNING: 升级预检: %s", warning)
	}
	// 统一事务已经提交，重新从磁盘加载，保证后续所有模块看到的正是落盘版本。
	committed, err := config.Open(cfgPath, nil)
	if err != nil {
		tray.FatalDialogf("重新加载升级后的配置失败: %v", err)
	}
	cfg = committed.Config
	if err := cfg.ResolvePaths(runDir); err != nil {
		tray.FatalDialogf("重新解析升级后的目录失败: %v", err)
	}

	// 4.5 凭据后端模式（项 23）—— 配置加载后立即切换，handler 后续读 Mode() 就知道走哪条路。
	// 默认值 cfg.App.CredentialStoreEnabled() = "keyring"（向后兼容）。
	credentials.SetMode(cfg.App.CredentialStoreEnabled())
	log.Printf("凭据后端: %s", credentials.Mode())

	// 4.5.1 file 模式初始化：设置数据目录和加密密钥。
	// keyring/disabled 模式下 Init 是 no-op，不影响原有流程。
	if err := credentials.Init(cfg.DataDir(), cfg.App.CredentialKey); err != nil {
		tray.FatalDialogf("初始化凭据后端失败: %v", err)
	}
	if credentials.Mode() == "file" && strings.TrimSpace(cfg.App.CredentialKey) == "" {
		log.Printf("凭据 file 模式: 密钥已自动生成并保存到 %s（请妥善备份 .credkey 文件）", filepath.Join(cfg.DataDir(), ".credkey"))
	}

	// 4.5.2 浏览器偏好：只放 cfg.DataDir() 全局，state 文件读取走
	// browserpref.Read，探测/落盘都在 openBrowser 里按需触发（startup 时不动）。
	browserpref.Init(cfg.DataDir())

	// 4.6 SSH 日志开关 + compat profile 默认值（项 9 + 项 22）
	// 默认全关 —— 避免在用户机器上无脑生成日志。
	sshclient.SetLogConfig(cfg.App.SSHDebug, cfg.App.SSHTrafficDump, cfg.App.SSHLogMaxMB, cfg.App.SSHLogKeep)
	sshclient.SetDefaultProfile(cfg.App.SSHCompatProfile)
	sshclient.SetProfileStoreDir(cfg.DataDir())
	if cfg.App.SSHDebug || cfg.App.SSHTrafficDump {
		log.Printf("SSH 日志已开启: debug=%v traffic=%v maxMB=%d keep=%d", cfg.App.SSHDebug, cfg.App.SSHTrafficDump, cfg.App.SSHLogMaxMB, cfg.App.SSHLogKeep)
	}
	log.Printf("SSH 默认 compat profile: %s", sshclient.DefaultProfileName())

	// 4.7 项 14 修复：文件浏览器（任意路径下载）开启时打 WARNING 启动日志。
	//
	// 之前 v0.3 默认开启 v0.4 也默认 true（向后兼容），但启动日志啥都没说，
	// 部署到生产后管理员容易忘它开着。这次明确打 warning：
	//   - 显式 enable_free_file_browser=true → 警告"任何路径都可下"
	//   - 显式 enable_free_file_browser=false → 提示"已关"
	//   - 没设（默认 true） → 同 true 也警告
	// 同时把 free_file_roots 白名单情况也打出来，方便管理员核对。
	if cfg.App.FreeFileBrowserEnabled() {
		roots := cfg.App.FreeFileRoots
		if len(roots) == 0 {
			log.Printf("WARNING: 文件浏览器（任意路径下载）已启用，但 free_file_roots 为空 —— 实际等价于『无限制』，请尽快补白名单")
		} else {
			log.Printf("WARNING: 文件浏览器（任意路径下载）已启用，free_file_roots 白名单=%v（仅这些前缀可访问）", roots)
		}
	} else {
		log.Printf("文件浏览器（任意路径下载）已禁用 (/api/files/* 将 403)")
	}

	// 4.8 v1.0：按 cfg.App.AutoStart 同步注册表（idempotent）；v1.1 升级场景扩展。
	//
	// 触发条件（任一为真即同步注册表到当前 exe 路径）：
	//   - 配置 auto_start=true：用户在配置页勾了"开机自启" → 正常路径；
	//   - 配置 auto_start=false 但注册表里有 KairoOpsToolboxAutoStart 值：
	//     升级前用户在旧版本勾过自启、但新版本 config.yaml 被重置成默认；
	//     或者 exe 路径在升级时变了（v0.5/ → v0.6/ 子目录），注册表还指旧路径。
	//     两种情况都接管"用户想继续自启"的意图，把注册表更新到当前 exe。
	//
	// valueName 用了独特复合命名（产品+工具+功能），不会被外部程序撞到，
	// 所以"看注册表里有没有这个值"可以无脑信任。
	//
	// 取消自启只能通过配置页取消（config=auto_start=false 且注册表无项），
	// 所以"用户主动取消过的旧版本升级上来"不会触发接管。
	//
	// 失败只 log warning，不阻断启动 —— 自启没设上不影响 HTTP 服务正常跑。
	if sysutil.Supported() {
		wantEnabled := cfg.App.AutoStart
		if !wantEnabled {
			actual, _ := sysutil.IsAutoStartEnabled()
			wantEnabled = actual
		}
		if wantEnabled {
			exe, exeErr := os.Executable()
			if exeErr != nil {
				log.Printf("WARNING: 自启同步失败，获取 exe 路径失败: %v", exeErr)
			} else {
				if resolved, err := filepath.EvalSymlinks(exe); err == nil {
					exe = resolved
				}
				if err := sysutil.SetAutoStart(true, filepath.Clean(exe)); err != nil {
					log.Printf("WARNING: 自启同步失败（%s）: %v", sysutil.AutoStartKeyName(), err)
				} else {
					log.Printf("开机自启已同步: %s = %s", sysutil.AutoStartKeyName(), filepath.Clean(exe))
				}
			}
		}
	}

	// 5. 初始化审计日志
	auditLog, err := audit.New(cfg.LogDir(), "audit.log")
	if err != nil {
		tray.FatalDialogf("初始化审计日志失败: %v", err)
	}
	defer auditLog.Close()

	// 4.9 项 4 迁移：把历史 .meta sidecar 文件合并到单文件索引 .kairo-meta.json。
	// 一次性操作，幂等。失败不致命（侧车丢了只是丢元数据，不影响下载文件本身）。
	//
	// v1.0：改为异步 + 短延迟（100ms），不阻塞启动监听：
	//   - 旧实现同步等迁移完成；下载目录有 10w .meta 文件时启动阻塞几十秒；
	//   - 新实现启动 100ms 后才扫描，期间 HTTP 服务已经 listen + 浏览器已打开；
	//   - 用户首次调 /api/downloads/list 时如迁移未完成会拿到旧视图，迁移完成后下次刷新正常。
	go func() {
		time.Sleep(100 * time.Millisecond)
		if migrated, skipped, err := downloads.MigrateSidecars(cfg.DownloadDir()); err != nil {
			log.Printf("WARNING: .meta sidecar 迁移失败: %v", err)
		} else if migrated > 0 {
			log.Printf("元数据迁移完成: 合并 %d 个 .meta 文件到单文件索引（跳过 %d 个）", migrated, skipped)
		}
	}()

	// 6. 嵌入的 web 静态资源
	webSubFS, err := fs.Sub(webFS, "web")
	if err != nil {
		tray.FatalDialogf("加载 web 资源失败: %v", err)
	}

	// 7. 构造可热替换的配置 Manager
	cfgMgr := config.NewManager(cfg, cfgPath, runDir)

	// 7.1 注入 license 包的 config provider
	// 这样 license 包能在不直接 import config (避免循环) 的情况下读取 kairo 字段 + internal_endpoints
	// SetConfigProvider 内部会立即用 snapshot 覆盖 var 池 (LicenseServerPrimary 等)
	// —— 测试代码不调 SetConfigProvider, var 池走源码默认值, 老测试零修改
	license.SetConfigProvider(func() *license.ConfigSnapshot {
		c := cfgMgr.Get()
		if c == nil {
			return &license.ConfigSnapshot{}
		}
		return &license.ConfigSnapshot{
			KairoInternalToken:       c.App.KairoInternalToken,
			LicenseActivatePrimary:   c.InternalEndpoints.LicenseActivate.Primary,
			LicenseActivateSecondary: c.InternalEndpoints.LicenseActivate.Secondary,
			LicenseActivateAuth:      c.InternalEndpoints.LicenseActivate.Auth,
		}
	})

	// 7.1.1 v0.14: 注入 sponsor 包的 endpoint config 覆盖
	//   - 从 config.yaml 的 internal_endpoints.sponsor_leaderboard 读 (主备 + auth + timeout)
	//   - 启动时立即用 var 池覆盖 (跟 license 同款模式)
	//   - 测试代码不调 InitFromConfig, var 池走源码默认值, 老测试零修改
	//   - 之前忘了调 → config 段配的 mock 地址永远不生效, 改地址必须改源码重编译
	if c := cfgMgr.Get(); c != nil {
		sponsor.InitFromConfig(
			c.InternalEndpoints.SponsorLeaderboard.Primary,
			c.InternalEndpoints.SponsorLeaderboard.Secondary,
			c.InternalEndpoints.SponsorLeaderboard.Auth,
			c.InternalEndpoints.SponsorLeaderboard.Timeout,
		)
	}

	// 7.1.2 v0.16: 注入 pet 包的 endpoint 配置覆盖（跟 sponsor 同款模式）
	//   - 默认值硬编码在源码（与武林排行榜共用同一套 Java httpInterface 网关）;
	//     开发者可在 config.yaml 的 internal_endpoints.pet_leaderboard 覆盖 (主备 + auth + timeout)
	//   - 空值不覆盖 var 池; 若显式清空 syncPrimary, /api/pet/sync 优雅降级
	//     (前端显示「未配置服务器, 仅本地展示」), 不把状态数据发到假地址
	if c := cfgMgr.Get(); c != nil {
		pet.InitFromConfig(
			c.InternalEndpoints.PetLeaderboard.Primary,
			c.InternalEndpoints.PetLeaderboard.Secondary,
			c.InternalEndpoints.PetLeaderboard.Auth,
			c.InternalEndpoints.PetLeaderboard.Timeout,
		)
	}

	// 7.2 启动时做一次 license 检查 (仅日志, 不阻止启动)
	// 前端 GET /api/license/status 时会再次检查, 这里是 fail-soft 的预检
	if err := license.Check(); err == nil {
		log.Printf("license: 启动检查通过 (开发自用 或 本地证书有效)")
	} else {
		log.Printf("license: 启动检查未通过, 前端将弹激活窗 (err=%v)", err)
	}

	// 7.3 v2 宠物彩蛋引擎：订阅 audit（未解锁时引擎内部 no-op gate 零记录），
	// 状态落盘 data/pet.json。皮肤清单读 embed 的 skins.json（50 款精灵图）。
	// 初始化失败只记日志（宠物功能关闭），不阻断启动。
	skinsJSON, skerr := webFS.ReadFile("web/img/pet/skins/skins.json")
	if skerr != nil {
		log.Printf("pet: 读取皮肤清单失败（将退回内置最小清单）: %v", skerr)
		skinsJSON = nil
	}
	petEngine, perr := pet.NewEngineWithSources(
		pet.RulesFromConfig(cfgMgr.Get().Pet),
		filepath.Join(cfgMgr.Get().DataDir(), "pet.json"),
		nil, skinsJSON,
		func() (string, bool) {
			// 宠物 ID 从激活码派生：同一激活码永远同一只宠物，
			// 用户重装/删 pet.json 也不会在排行榜上出现多个自己。
			code, ok := license.CurrentCode()
			if !ok {
				return "", false
			}
			sum := sha256.Sum256([]byte(code))
			return hex.EncodeToString(sum[:16]), true
		},
		func() (string, bool) {
			// 同步时上送激活码，服务器做"一个激活码唯一一只宠物"的硬绑定。
			return license.CurrentCode()
		},
	)
	if perr != nil {
		log.Printf("pet: 引擎初始化失败（宠物功能关闭）: %v", perr)
	} else {
		auditLog.Subscribe(petEngine)
		if cfgMgr.Get().Pet.Enabled {
			if _, err := petEngine.ForceEnable(); err != nil {
				log.Printf("pet: 强制启用失败: %v", err)
			}
		}
	}
	if petEngine != nil {
		defer petEngine.Close()
	}

	// 7.5 构造 tail 会话池
	// BE-002：idleAfter 从 config.tail_idle_minutes 读取（默认 30 分钟，最小 5 分钟）。
	tails := tailmgr.NewManager()
	tails.SetIdleAfter(cfg.App.TailIdleDuration())
	defer tails.ShutdownAll()

	// 7.6 构造 SSH shell 会话池（v0.10 SSH 终端菜单用）。
	// max=0 走 DefaultMaxSessions（32 个，约 192MB 内存上限）。
	shells := sshshell.New(0)
	defer shells.ShutdownAll()

	// 7.7 构造便笺提醒管理器（v1.0）：存储 + 事件驱动调度器。
	// 数据文件在 data/reminders.json；OnFire 按 Action.Kind 分发：
	//   popup   → popup 包弹系统提醒（默认）
	//   url     → openBrowser 打开网址
	//   command → 执行本地命令/脚本（30s 超时，结果写审计日志）
	rStore := reminder.NewStore(filepath.Join(cfg.DataDir(), "reminders.json"))
	if err := rStore.EnsurePath(); err != nil {
		log.Printf("WARNING: 准备 reminder 数据目录失败: %v", err)
	}
	rManager, err := reminder.NewManager(rStore, func(r reminder.Reminder, at time.Time) {
		// 在新 goroutine 里执行动作，避免阻塞调度器主循环
		go func() {
			defer func() {
				if rv := recover(); rv != nil {
					log.Printf("reminder: 动作执行 panic: %v", rv)
				}
			}()
			fireReminderAction(r, at, auditLog, openBrowser)
		}()
	})
	if err != nil {
		log.Printf("WARNING: 初始化 reminder manager 失败: %v", err)
		rManager = nil
	} else {
		log.Printf("便笺提醒已加载: %d 条", len(rManager.List()))
	}
	defer func() {
		if rManager != nil {
			rManager.Stop()
		}
		popup.Shutdown()
	}()

	// 7.75 构造便笺管理器。便笺与提醒是独立领域：便笺负责持续编辑和
	// 窗口状态，提醒只负责未来触发。
	nStore := note.NewStore(filepath.Join(cfg.DataDir(), "notes.json"))
	if err := nStore.EnsurePath(); err != nil {
		log.Printf("WARNING: 准备 note 数据目录失败: %v", err)
	}
	nManager, err := note.NewManager(nStore)
	if err != nil {
		log.Printf("WARNING: 初始化 note manager 失败: %v", err)
		nManager = nil
	} else {
		log.Printf("便笺已加载: %d 条", len(nManager.List(note.Filter{})))
	}

	// 7.8 构造定时任务管理器：存储 + 事件驱动调度器。
	// 任务定义在 data/sched_tasks.json，运行历史在 data/sched_task_runs.json。
	// 命令执行失败 / 落盘失败都只记日志，不影响 HTTP 服务。
	tStore := schedtask.NewStore(
		filepath.Join(cfg.DataDir(), "sched_tasks.json"),
		filepath.Join(cfg.DataDir(), "sched_task_runs.json"),
	)
	if err := tStore.EnsurePath(); err != nil {
		log.Printf("WARNING: 准备 schedtask 数据目录失败: %v", err)
	}
	tManager, err := schedtask.NewManagerWithOptions(tStore, schedtask.ManagerOptions{
		DefaultWorkDir: runDir,
	})
	if err != nil {
		log.Printf("WARNING: 初始化 schedtask manager 失败: %v", err)
		tManager = nil
	} else if n := len(tManager.List()); n > 0 {
		log.Printf("定时任务已加载: %d 条", n)
	}
	defer func() {
		if tManager != nil {
			tManager.Stop()
		}
	}()

	// 8. 构造 HTTP 服务
	srv := httpserver.New(cfgMgr, auditLog, webSubFS, tails, shells, httpserver.Dependencies{
		Reminders: rManager,
		Notes:     nManager,
		Tasks:     tManager,
		Pet:       petEngine,
	})
	defer func() {
		if err := srv.CloseDatabase(); err != nil {
			log.Printf("WARNING: 关闭数据库连接池失败: %v", err)
		}
	}()
	// 8.5 启动下载历史定期清理（启动时清理一次 + 每小时清理一次）
	srv.StartPeriodicCleanup()

	// 8.6 预热武林排行榜缓存：Java 网关冷启动时首次请求常超时（用户反馈
	// "第一次运行排行榜加载不出来，过一会才好"），启动 2s 后后台拉一次填充缓存。
	httpserver.WarmSponsorCache()

	// WriteTimeout 设为 0：SSE 长连接（/api/logs/tail/{id}/events、
	// /api/files/download/{id}/events、下载进度流等）需要任意时长的写，
	// 否则 120 秒后 server 会主动断流。
	// 普通接口的写超时由 handler 内部用 ctx 控制，不依赖 WriteTimeout。
	httpSrv := &http.Server{
		Addr:              cfg.App.ListenAddr(),
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	// 9. 监听 127.0.0.1
	ln, err := net.Listen("tcp", cfg.App.ListenAddr())
	if err != nil {
		tray.FatalDialogf("监听 %s 失败: %v", cfg.App.ListenAddr(), err)
	}

	// 所有原生窗口共用一个 Windows UI 线程；文件选择对话框也派发到此线程，
	// 避免 HTTP goroutine 把系统对话框弹到屏幕左上角。
	nativeHost := winui.New()
	if err := nativeHost.Start(); err != nil {
		tray.FatalDialogf("初始化 Windows 原生窗口线程失败: %v", err)
	}
	httpserver.SetUIInvoker(nativeHost.Invoke)
	defer func() {
		httpserver.SetUIInvoker(nil)
		nativeHost.Shutdown()
	}()

	// 10. 启动 HTTP server（goroutine）。
	// 主线程交给系统托盘（Windows）或信号等待（非 Windows）。
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			tray.FatalDialogf("HTTP 服务异常: %v", err)
		}
	}()

	// 11. 启动并自动打开浏览器
	url := fmt.Sprintf("http://%s", cfg.App.ListenAddr())
	log.Printf("工具箱已启动: %s", url)
	log.Printf("工作目录: %s", runDir)
	log.Printf("下载目录: %s", cfg.DownloadDir())
	log.Printf("审计日志: %s", filepath.Join(cfg.LogDir(), "audit.log"))
	log.Printf("运行日志: %s", logFilePath)

	if cfg.App.AutoOpenBrowserEnabled() {
		go openBrowser(url)
	}

	desktopNotes, err := desknote.New(nativeHost, nManager)
	if err != nil {
		tray.FatalDialogf("初始化桌面便笺失败: %v", err)
	}
	defer desktopNotes.Shutdown()

	// 桌面宠物：原生 Win32 透明窗口（仅 Windows 生效，非 Windows no-op）。
	// 展示、拖拽、点击、皮肤面板、经验漂浮都由本模块自绘，不再借用浏览器小窗口。
	// 皮肤 PNG 直接从 embed.FS 直读，避免逐张走 HTTP 造成面板首屏卡顿。
	if err := deskpet.Run(deskpet.Options{
		Host:    nativeHost,
		BaseURL: url,
		LoadSprite: func(id string) ([]byte, error) {
			return webFS.ReadFile("web/img/pet/skins/" + id + ".png")
		},
		DataDir: cfg.DataDir(),
	}); err != nil {
		log.Printf("deskpet: 初始化失败: %v", err)
	}
	defer deskpet.Shutdown()

	// 12. 启动系统托盘（阻塞主线程直到退出）。
	// Windows：托盘菜单"退出"触发 OnQuit。
	// 非 Windows：SIGINT/SIGTERM 触发 OnQuit。
	// Shutdown 超时用 1 秒而不是 5 秒：托盘"退出"的用户预期是立刻退，
	// SSE 长连接（WriteTimeout=0）会让 Shutdown 卡到超时才强切，
	// 1 秒足够正常请求收尾，强切的 SSE 不影响数据完整性（tail 是实时流，下载已落盘）。
	tray.Run(tray.Config{
		Tooltip:       "Kairo",
		OnOpenBrowser: func() { openBrowser(url) },
		OnNewNote: func() {
			if err := desktopNotes.NewNote(); err != nil {
				log.Printf("desknote: 新建失败: %v", err)
			}
		},
		OnToggleNotes: func() {
			if err := desktopNotes.ToggleAll(); err != nil {
				log.Printf("desknote: 切换显示失败: %v", err)
			}
		},
		OnOpenPet: func() {
			deskpet.Toggle()
		},
		OnQuit: func() {
			log.Println("收到退出请求，正在关闭服务...")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = httpSrv.Shutdown(shutdownCtx)
		},
		// 便笺提醒：暂停今日到次日 0 点 / 立即恢复。
		// pause 状态不持久化（重启后默认恢复），用户重启电脑后想再暂停需手动点一次。
		OnPauseToday: func() {
			if rManager == nil {
				return
			}
			now := time.Now()
			y, m, d := now.Date()
			until := time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())
			rManager.PauseUntil(until)
			auditLog.Write("reminder.pause.tray", "until", until.Format(time.RFC3339))
			log.Printf("提醒已暂停至 %s", until.Format("2006-01-02 15:04"))
		},
		OnResumeToday: func() {
			if rManager == nil {
				return
			}
			rManager.PauseUntil(time.Time{})
			auditLog.Write("reminder.resume.tray")
			log.Println("提醒已恢复")
		},
	})
	log.Println("服务已停止，再见。")
}

// exeDirectory 返回可执行文件所在目录（跨平台）
func exeDirectory() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		// Windows 下符号链接为 .exe，避免解析到误路径
		return filepath.Dir(exe), nil
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return filepath.Dir(exe), nil
	}
	return filepath.Dir(real), nil
}

// fireReminderAction 提醒触发后的动作分发。
//
//   - popup（默认）：popup.Show(content) 系统弹窗；
//   - url：openBrowser 打开网址（http/https，Validate 已限制 scheme）；
//   - command：exec 本地命令/脚本，30s 超时，退出码 + 输出尾巴写审计日志。
//
// 安全边界：命令以当前用户权限在本机执行，由用户自己在提醒里配置；
// 每次执行都写审计（reminder.fire.command），出问题可追溯。
func fireReminderAction(r reminder.Reminder, at time.Time, auditLog *audit.Logger, openURL func(string)) {
	kind := reminder.ActionPopup
	var act *reminder.Action
	if r.Action != nil && strings.TrimSpace(string(r.Action.Kind)) != "" {
		act = r.Action
		kind = act.Kind
	}
	switch kind {
	case reminder.ActionPopup:
		popup.Show(r.Content)
		auditLog.Write("reminder.fire.popup",
			"id", r.ID,
			"type", string(r.Type),
			"at", at.Format(time.RFC3339),
		)
	case reminder.ActionURL:
		openURL(act.URL)
		auditLog.Write("reminder.fire.url",
			"id", r.ID,
			"type", string(r.Type),
			"at", at.Format(time.RFC3339),
			"url", act.URL,
		)
		log.Printf("reminder.fire: id=%s 打开网址 %s", r.ID, act.URL)
	case reminder.ActionCommand:
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, act.Command, act.Args...)
		if act.WorkDir != "" {
			cmd.Dir = act.WorkDir
		}
		var out strings.Builder
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		output := out.String()
		if len(output) > 500 {
			output = output[len(output)-500:]
		}
		status := "ok"
		if err != nil {
			status = "error"
		}
		auditLog.Write("reminder.fire.command",
			"id", r.ID,
			"type", string(r.Type),
			"at", at.Format(time.RFC3339),
			"command", act.Command,
			"status", status,
			"err", fmt.Sprintf("%v", err),
			"output_tail", output,
		)
		if err != nil {
			log.Printf("reminder.fire: id=%s 命令执行失败 %s: %v", r.ID, act.Command, err)
		} else {
			log.Printf("reminder.fire: id=%s 命令执行成功 %s", r.ID, act.Command)
		}
	default:
		// 理论不可达（Validate 已拦截），兜底走弹窗
		popup.Show(r.Content)
		auditLog.Write("reminder.fire.popup",
			"id", r.ID,
			"type", string(r.Type),
			"at", at.Format(time.RFC3339),
		)
	}
}

// openBrowser 跨平台打开浏览器。
//
// 决策链：
//
//  1. 读 data/browser_state.json（"上次用什么浏览器打开"）
//     ├─ state.Kind=chrome + path 文件仍在 → 直接 exec(state.Path, url)
//     └─ 没有 / 失效                       → 进入 2
//
//  2. 探测链：
//     Windows + 装了 Chrome  → exec(chrome.exe, url)，kind=chrome
//     Windows 没 Chrome        → rundll32 url.dll,FileProtocolHandler，kind=default
//     macOS                    → open url，kind=default
//     Linux                    → xdg-open url，kind=default
//
//  3. 启动成功 → 把这次用的浏览器写回 state（remembered=true 时跳过写回）
//
// 资源占用：多读一次小 JSON（< 1ms），state 命中则不读注册表；探测链走
// os.Stat 常见路径（< 1ms）失败再读注册表（< 5ms）。本进程本身在 cmd.Start()
// 后立刻退出，浏览器进程与本程序解耦。
//
// Win7/10 兼容：Chrome 装路径在 Win7/10/11 三代完全一致；注册表 API
// (RegOpenKeyExW/QueryValueExW) Unicode 版本从 Win7 起行为一致；WOW6432Node
// 区分 32/64-bit 也是 Win7 x64 起就有的特性，不引入新平台差异。
func openBrowser(url string) {
	args, kind, path, remembered := chooseBrowser(url)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		log.Printf("打开浏览器失败 (%s): %v", args[0], err)
		return
	}
	go func() { _ = cmd.Wait() }()

	// 写回 state：探测链命中时记录；state 命中时跳过（避免无意义写盘）。
	if !remembered {
		if err := browserpref.Write(&browserpref.State{
			Kind: kind,
			Path: path,
		}); err != nil {
			log.Printf("保存浏览器偏好失败: %v", err)
		}
	}
}

// chooseBrowser 选浏览器并返回 (argv, kind, executablePath, remembered)。
//
// remembered=true 表示从 state 命中（无需写回）；
// remembered=false 表示探测链命中（调用方需要写回 state）。
//
// 失败兜底：state 读取失败 / 解析失败都视为"无 state"，安静进入探测链。
func chooseBrowser(url string) (args []string, kind browserpref.Kind, path string, remembered bool) {
	// 1) 读 state。Read 失败不影响主流程 —— 落到探测链。
	s, err := browserpref.Read()
	if err != nil {
		log.Printf("读取浏览器偏好失败（回落探测）: %v", err)
	}
	if s != nil && s.Kind == browserpref.KindChrome && s.Path != "" {
		if _, statErr := os.Stat(s.Path); statErr == nil {
			return []string{s.Path, url}, browserpref.KindChrome, s.Path, true
		}
		// path 失效 → 留一行 log，下次启动会重新探测。
		log.Printf("浏览器偏好中记录的 Chrome 路径已失效: %s，回落到探测", s.Path)
	}

	// 2) 探测链。
	if runtime.GOOS == "windows" {
		if chromePath, ok := sysutil.FindChrome(); ok {
			log.Printf("探测到 Chrome: %s", chromePath)
			return []string{chromePath, url}, browserpref.KindChrome, chromePath, false
		}
		// 没 Chrome → 走系统默认浏览器（rundll32 走 shell32 间接层，
		// 由 Windows 根据"设置 → 默认应用 → Web 浏览器"决定开哪个）。
		return []string{"rundll32", "url.dll,FileProtocolHandler", url}, browserpref.KindDefault, "", false
	}
	if runtime.GOOS == "darwin" {
		return []string{"open", url}, browserpref.KindDefault, "", false
	}
	// Linux + 其他 Unix。
	return []string{"xdg-open", url}, browserpref.KindDefault, "", false
}
