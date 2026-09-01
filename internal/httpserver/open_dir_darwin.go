//go:build darwin

package httpserver

import "os/exec"

func platformRevealCommand(path string) (*exec.Cmd, string) {
	return exec.Command("open", "-R", path), "open -R"
}

func platformOpenFolderCommand(dir string) (*exec.Cmd, string) {
	return exec.Command("open", dir), "open"
}
