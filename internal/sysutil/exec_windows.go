//go:build windows

// Package sysutil 提供跨平台系统工具函数。
package sysutil

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// HideConsoleWindow 给 exec.Cmd 打上 CREATE_NO_WINDOW，避免双击 Kairo.exe
// （GUI subsystem，无 console）启动子进程时 Windows 临时分配 conhost 弹 cmd 黑框。
//
// 等价于 cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}（x/sys/windows 的
// SysProcAttr 是 syscall.SysProcAttr 的 type alias，见 vendor/.../windows/aliases.go）。
//
// 调用方：httpserver (open_dir / handlers_local)、portreuse (netstat/tasklist 等)、
// diagnostics (命令探测)，以及未来 SSH 终端的 exec_cmd。
func HideConsoleWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}
