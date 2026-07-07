// Package tray 提供系统托盘和启动错误弹框能力。
//
// Windows：双击 exe 后无控制台窗口，托盘图标常驻右下角，
// 右键菜单可"打开浏览器"或"退出"。启动失败弹 MessageBox。
//
// 非 Windows：no-op，等 SIGINT/SIGTERM 退出（开发模式用）。
package tray

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

//go:embed icon.ico
var iconBytes []byte

// Config 传递给 Run 的运行时配置
type Config struct {
	Tooltip       string // 托盘 tooltip
	OnOpenBrowser func() // "打开浏览器"菜单回调
	OnQuit        func() // "退出"菜单回调（优雅关闭 HTTP server 等）

	// v1.0 便笺提醒：托盘"暂停今日 / 恢复提醒"菜单回调。
	// 两者二选一被设置；点哪个调哪个。Pause 会自动切到 Resume 的 label。
	OnPauseToday  func() // "暂停今日提醒"菜单回调（直到次日 0 点）
	OnResumeToday func() // "恢复提醒"菜单回调
}

// Icon 返回 embed 的 ICO 图标字节
func Icon() []byte { return iconBytes }

// Run 启动系统托盘。阻塞直到用户退出。
// 非 Windows 平台：等待 SIGINT/SIGTERM。
func Run(cfg Config) { run(cfg) }

// FatalDialog 显示致命错误对话框/输出，然后退出。
// Windows：弹 MessageBox（Ctrl+C 可复制）+ 写 crash.log。
// 非 Windows：写 stderr + crash.log。
func FatalDialog(msg string) { fatalDialog(msg) }

// FatalDialogf 是 FatalDialog 的格式化版本，用法同 log.Fatalf。
func FatalDialogf(format string, args ...interface{}) {
	fatalDialog(fmt.Sprintf(format, args...))
}

// writeCrashLog 把错误信息追加到 exe 同级目录的 crash.log。
// 不依赖 log 目录（启动早期可能还没创建），也不返回 error（尽力写）。
func writeCrashLog(msg string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	path := filepath.Join(filepath.Dir(exe), "crash.log")
	ts := time.Now().Format("2006-01-02 15:04:05")
	content := ts + " " + msg + "\n"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(content)
}
