// Kairo - Kairo入口
//
// 启动本地 HTTP 服务，默认监听 127.0.0.1:18080，
// 启动后自动打开浏览器访问首页。
package main

import (
	"context"
	"embed"
	"errors"
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
	"kairo/internal/config"
	"kairo/internal/credentials"
	"kairo/internal/downloads"
	"kairo/internal/httpserver"
	"kairo/internal/sshclient"
	"kairo/internal/sshshell"
	"kairo/internal/tailmgr"
	"kairo/internal/tray"
)

//go:embed web
var webFS embed.FS

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetPrefix("[Kairo] ")

	// 1. 确定运行目录。优先用可执行文件目录；若 config.yaml 不在那，
	// 再回退到当前工作目录，兼容 `go run .` 这类临时二进制路径。
	runDir, cfgPath, err := resolveRunDir()
	if err != nil {
		tray.FatalDialogf("定位运行目录失败: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		tray.FatalDialogf("加载配置失败 (%s): %v", cfgPath, err)
	}

	// 3. 解析相对目录为基于 exeDir 的绝对路径
	if err := cfg.ResolvePaths(runDir); err != nil {
		tray.FatalDialogf("解析目录失败: %v", err)
	}

	// 4. 准备运行时目录
	if err := cfg.EnsureDirs(); err != nil {
		tray.FatalDialogf("创建运行时目录失败: %v", err)
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

	// 4.6 SSH 日志开关 + compat profile 默认值（项 9 + 项 22）
	// 默认全关 —— 避免在用户机器上无脑生成日志。
	sshclient.SetLogConfig(cfg.App.SSHDebug, cfg.App.SSHTrafficDump, cfg.App.SSHLogMaxMB, cfg.App.SSHLogKeep)
	sshclient.SetDefaultProfile(cfg.App.SSHCompatProfile)
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

	// 5. 初始化审计日志
	auditLog, err := audit.New(cfg.LogDir(), "audit.log")
	if err != nil {
		tray.FatalDialogf("初始化审计日志失败: %v", err)
	}
	defer auditLog.Close()

	// 4.9 项 4 迁移：把历史 .meta sidecar 文件合并到单文件索引 .kairo-meta.json。
	// 一次性操作，幂等。失败不致命（侧车丢了只是丢元数据，不影响下载文件本身）。
	if migrated, skipped, err := downloads.MigrateSidecars(cfg.DownloadDir()); err != nil {
		log.Printf("WARNING: .meta sidecar 迁移失败: %v", err)
	} else if migrated > 0 {
		log.Printf("元数据迁移完成: 合并 %d 个 .meta 文件到单文件索引（跳过 %d 个）", migrated, skipped)
	}

	// 6. 嵌入的 web 静态资源
	webSubFS, err := fs.Sub(webFS, "web")
	if err != nil {
		tray.FatalDialogf("加载 web 资源失败: %v", err)
	}

	// 7. 构造可热替换的配置 Manager
	cfgMgr := config.NewManager(cfg, cfgPath, runDir)

	// 7.5 构造 tail 会话池
	// BE-002：idleAfter 从 config.tail_idle_minutes 读取（默认 30 分钟，最小 5 分钟）。
	tails := tailmgr.NewManager()
	tails.SetIdleAfter(cfg.App.TailIdleDuration())
	defer tails.ShutdownAll()

	// 7.6 构造 SSH shell 会话池（v0.10 SSH 终端菜单用）。
	// max=0 走 DefaultMaxSessions（32 个，约 192MB 内存上限）。
	shells := sshshell.New(0)
	defer shells.ShutdownAll()

	// 8. 构造 HTTP 服务
	srv := httpserver.New(cfgMgr, auditLog, webSubFS, tails, shells)

	// 8.5 启动下载历史定期清理（启动时清理一次 + 每小时清理一次）
	srv.StartPeriodicCleanup()

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

	if cfg.App.AutoOpenBrowser {
		go openBrowser(url)
	}

	// 12. 启动系统托盘（阻塞主线程直到退出）。
	// Windows：托盘菜单"退出"触发 OnQuit。
	// 非 Windows：SIGINT/SIGTERM 触发 OnQuit。
	// Shutdown 超时用 1 秒而不是 5 秒：托盘"退出"的用户预期是立刻退，
	// SSE 长连接（WriteTimeout=0）会让 Shutdown 卡到超时才强切，
	// 1 秒足够正常请求收尾，强切的 SSE 不影响数据完整性（tail 是实时流，下载已落盘）。
	tray.Run(tray.Config{
		Tooltip: "Kairo",
		OnOpenBrowser: func() { openBrowser(url) },
		OnQuit: func() {
			log.Println("收到退出请求，正在关闭服务...")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = httpSrv.Shutdown(shutdownCtx)
		},
	})
	log.Println("服务已停止，再见。")
}

func resolveRunDir() (dir string, cfgPath string, err error) {
	exeDir, err := exeDirectory()
	if err != nil {
		return "", "", err
	}
	exeCfg := filepath.Join(exeDir, "config.yaml")
	if _, statErr := os.Stat(exeCfg); statErr == nil {
		return exeDir, exeCfg, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	cwdCfg := filepath.Join(cwd, "config.yaml")
	if _, statErr := os.Stat(cwdCfg); statErr == nil {
		return cwd, cwdCfg, nil
	}

	// 两边都没有时，保留原行为：优先报告可执行文件目录下的期望路径。
	return exeDir, exeCfg, nil
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

// openBrowser 跨平台打开默认浏览器
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Start()
	go func() { _ = cmd.Wait() }()
}
