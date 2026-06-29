//go:build !windows

package tray

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// run 非 Windows 平台：等待 SIGINT/SIGTERM，收到后调用 OnQuit。
// 开发模式下（go run . / macOS / Linux）走这条路径。
func run(cfg Config) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	if cfg.OnQuit != nil {
		cfg.OnQuit()
	}
}

// fatalDialog 非 Windows 平台：写 stderr + crash.log，然后退出。
func fatalDialog(msg string) {
	writeCrashLog(msg)
	fmt.Fprintln(os.Stderr, "FATAL:", msg)
	os.Exit(1)
}
