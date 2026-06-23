// OpsToolbox - 内网运维工具箱入口
//
// 启动本地 HTTP 服务，默认监听 127.0.0.1:18080，
// 启动后自动打开浏览器访问首页。
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"ops-toolbox/internal/audit"
	"ops-toolbox/internal/config"
	"ops-toolbox/internal/credentials"
	"ops-toolbox/internal/httpserver"
	"ops-toolbox/internal/sshclient"
	"ops-toolbox/internal/tailmgr"
)

//go:embed web
var webFS embed.FS

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetPrefix("[OpsToolbox] ")

	// 1. 确定可执行文件所在目录
	exeDir, err := exeDirectory()
	if err != nil {
		log.Fatalf("无法获取可执行文件目录: %v", err)
	}

	// 2. 加载 config.yaml
	cfgPath := filepath.Join(exeDir, "config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("加载配置失败 (%s): %v", cfgPath, err)
	}

	// 3. 解析相对目录为基于 exeDir 的绝对路径
	if err := cfg.ResolvePaths(exeDir); err != nil {
		log.Fatalf("解析目录失败: %v", err)
	}

	// 4. 准备运行时目录
	if err := cfg.EnsureDirs(); err != nil {
		log.Fatalf("创建运行时目录失败: %v", err)
	}

	// 4.5 凭据后端模式（项 23）—— 配置加载后立即切换，handler 后续读 Mode() 就知道走哪条路。
	// 默认值 cfg.App.CredentialStoreEnabled() = "keyring"（向后兼容）。
	credentials.SetMode(cfg.App.CredentialStoreEnabled())
	log.Printf("凭据后端: %s", credentials.Mode())

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
		log.Fatalf("初始化审计日志失败: %v", err)
	}
	defer auditLog.Close()

	// 6. 嵌入的 web 静态资源
	webSubFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("加载 web 资源失败: %v", err)
	}

	// 7. 构造可热替换的配置 Manager
	cfgMgr := config.NewManager(cfg, cfgPath)

	// 7.5 构造 tail 会话池
	tails := tailmgr.NewManager()
	defer tails.ShutdownAll()

	// 8. 构造 HTTP 服务
	srv := httpserver.New(cfgMgr, auditLog, webSubFS, tails)

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
		log.Fatalf("监听 %s 失败: %v", cfg.App.ListenAddr(), err)
	}

	// 10. 优雅退出
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Println("收到退出信号，正在关闭服务...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	// 11. 启动并自动打开浏览器
	url := fmt.Sprintf("http://%s", cfg.App.ListenAddr())
	log.Printf("工具箱已启动: %s", url)
	log.Printf("工作目录: %s", exeDir)
	log.Printf("下载目录: %s", cfg.DownloadDir())
	log.Printf("审计日志: %s", filepath.Join(cfg.LogDir(), "audit.log"))

	if cfg.App.AutoOpenBrowser {
		go openBrowser(url)
	}

	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("HTTP 服务异常: %v", err)
	}
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
