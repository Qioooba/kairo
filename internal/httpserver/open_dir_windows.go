//go:build windows

package httpserver

import (
	"os/exec"
	"strings"
	"syscall"
)

func windowsPath(path string) string {
	return strings.ReplaceAll(path, "/", `\`)
}

func explorerCommandLine(argument string) string {
	// Windows 文件名不能包含双引号。这里仍显式剔除，确保自定义 CmdLine
	// 始终只有一个由 explorer.exe 解析的参数，绝不经过 cmd.exe。
	argument = strings.ReplaceAll(argument, `"`, "")
	return `explorer.exe "` + argument + `"`
}

func platformRevealCommand(path string) (*exec.Cmd, string) {
	path = strings.ReplaceAll(windowsPath(path), `"`, "")
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
		// Win7 需要 /select,"路径"，不能把 /select, 和路径整体包进引号。
		// 这是 explorer.exe 自己的命令行，不经过 cmd.exe，&/^/% 均不会执行。
		CmdLine: `explorer.exe /select,"` + path + `"`,
	}
	return cmd, "explorer.exe"
}

func platformOpenFolderCommand(dir string) (*exec.Cmd, string) {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
		CmdLine:    explorerCommandLine(windowsPath(dir)),
	}
	return cmd, "explorer.exe"
}
