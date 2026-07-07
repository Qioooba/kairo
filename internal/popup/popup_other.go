//go:build !windows

package popup

import "log"

func show(content string) {
	// 开发模式（macOS / Linux）：打日志，不弹窗，避免污染桌面。
	log.Printf("[popup:no-op] %s", content)
}

func shutdown()             {}
func queueLength() int      { return 0 }