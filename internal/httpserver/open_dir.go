package httpserver

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func startFileManager(cmd *exec.Cmd, label string) error {
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s 失败: %w", label, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// revealInFileManager 跨平台"在文件管理器里打开并选中文件"。
func revealInFileManager(path string) error {
	return platformReveal(path)
}

// openFolderInFileManager 打开目录本身（进入该文件夹），而不是在父目录里选中它。
func openFolderInFileManager(dir string) error {
	return platformOpenFolder(dir)
}

func openPathAllowed(allowRoot, target string) error {
	if allowRoot == "" {
		return errors.New("allowRoot 不能为空")
	}
	if target == "" {
		return errors.New("target 不能为空")
	}
	if strings.ContainsAny(target, "\x00\n\r") {
		return errors.New("target 含非法控制字符")
	}
	absRoot, err := filepath.Abs(allowRoot)
	if err != nil {
		return fmt.Errorf("allowRoot 解析失败: %w", err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("target 解析失败: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return fmt.Errorf("target 不在 allowRoot 下（跨盘符或绝对路径无效）")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target 越界（%s 不在 %s 下）", absTarget, absRoot)
	}
	return nil
}
