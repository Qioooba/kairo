//go:build darwin

package httpserver

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func runAppleScriptPicker(script, label string) (string, error) {
	cmd := exec.Command("osascript", "-e", script)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if strings.Contains(out.String(), "User canceled") {
			return "", nil
		}
		return "", fmt.Errorf("%s失败: %w", label, err)
	}
	return strings.TrimSpace(out.String()), nil
}

func chooseFile() (string, error) {
	return runAppleScriptPicker(`POSIX path of (choose file with prompt "选择文件" default location (path to downloads folder))`, "文件选择")
}

func chooseDir() (string, error) {
	return runAppleScriptPicker(`POSIX path of (choose folder with prompt "选择文件夹" default location (path to downloads folder))`, "文件夹选择")
}
