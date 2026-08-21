//go:build !windows

// Package deskpet 提供原生桌面宠物（Windows 专用）。
//
// 非 Windows 平台（macOS/Linux 开发模式）下为 no-op：只打日志，不创建窗口，
// 避免污染开发桌面。Windows 实现见 deskpet_windows.go。
package deskpet

import "log"

// Run 初始化桌面宠物（非 Windows 为 no-op）。
func Run(o Options) error {
	log.Printf("[deskpet:no-op] 桌面宠物仅支持 Windows，当前平台不启用")
	return nil
}

// Toggle 显示 / 隐藏桌面宠物（非 Windows 为 no-op）。
func Toggle() {}

// IsShown 桌面宠物当前是否可见（非 Windows 恒为 false）。
func IsShown() bool { return false }

// Shutdown 关闭桌面宠物窗口（非 Windows 为 no-op）。
func Shutdown() {}
