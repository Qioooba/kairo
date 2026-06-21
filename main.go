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
	"ops-toolbox/internal/httpserver"
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

	httpSrv := &http.Server{
		Addr:              cfg.App.ListenAddr(),
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
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
