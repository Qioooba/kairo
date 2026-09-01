//go:build !darwin && !windows

package httpserver

import (
	"os/exec"
	"path/filepath"
)

func platformRevealCommand(path string) (*exec.Cmd, string) {
	return exec.Command("xdg-open", filepath.Dir(path)), "xdg-open"
}

func platformOpenFolderCommand(dir string) (*exec.Cmd, string) {
	return exec.Command("xdg-open", dir), "xdg-open"
}
