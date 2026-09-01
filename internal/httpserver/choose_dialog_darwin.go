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

func appleDefaultLocation(initial string) string {
	start := pickerStartDir(initial)
	if start == "" {
		return "(path to downloads folder)"
	}
	escaped := strings.ReplaceAll(start, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `(POSIX file "` + escaped + `")`
}

func chooseFile() (string, error) { return chooseFileAt("") }
func chooseDir() (string, error)  { return chooseDirAt("") }

func chooseFileAt(initial string) (string, error) {
	path, err := runAppleScriptPicker(`POSIX path of (choose file with prompt "选择文件" default location `+appleDefaultLocation(initial)+`)`, "文件选择")
	if err == nil {
		rememberPicked(path)
	}
	return path, err
}

func chooseDirAt(initial string) (string, error) {
	path, err := runAppleScriptPicker(`POSIX path of (choose folder with prompt "选择文件夹" default location `+appleDefaultLocation(initial)+`)`, "文件夹选择")
	if err == nil {
		rememberPicked(path)
	}
	return path, err
}
