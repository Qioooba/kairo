//go:build darwin

package httpserver

import "os/exec"

func platformRevealCommand(path string) (*exec.Cmd, string) {
	return exec.Command("open", "-R", path), "open -R"
}

func platformOpenFolderCommand(dir string) (*exec.Cmd, string) {
	return exec.Command("open", dir), "open"
}

func platformReveal(path string) error {
	return startFileManager(platformRevealCommand(path))
}

func platformOpenFolder(dir string) error {
	return startFileManager(platformOpenFolderCommand(dir))
}
