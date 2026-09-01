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
	argument = strings.ReplaceAll(argument, `"`, "")
	return `explorer.exe "` + argument + `"`
}

func platformRevealCommand(path string) (*exec.Cmd, string) {
	path = strings.ReplaceAll(windowsPath(path), `"`, "")
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `explorer.exe /select,"` + path + `"`,
	}
	return cmd, "explorer.exe"
}

func platformOpenFolderCommand(dir string) (*exec.Cmd, string) {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: explorerCommandLine(windowsPath(dir)),
	}
	return cmd, "explorer.exe"
}

func platformReveal(path string) error {
	return startFileManager(platformRevealCommand(path))
}

func platformOpenFolder(dir string) error {
	return startFileManager(platformOpenFolderCommand(dir))
}
