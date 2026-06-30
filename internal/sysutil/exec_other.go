//go:build !windows

// Package sysutil 提供跨平台系统工具函数。
package sysutil

import "os/exec"

// HideConsoleWindow 在非 Windows 平台是 no-op（macOS/Linux exec 不会弹控制台窗口）。
// Windows 版本见 exec_windows.go。
func HideConsoleWindow(cmd *exec.Cmd) {}
