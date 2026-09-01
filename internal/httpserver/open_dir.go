package httpserver

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// revealInFileManager 跨平台"在文件管理器里打开并选中文件"。
//
// 平台行为：
//   - macOS   `open -R <path>`     → Finder 里选中并显示
//   - Windows `explorer.exe /select,<path>` → Explorer 里选中并显示
//   - Linux   `xdg-open <dir>`     → 打开父目录（Linux 没有 -R 等价）
//
// 设计动机：
//   - 用户下完一个日志想在 Finder/Explorer 里直接看到它、拖到聊天工具，
//     比"复制完整路径 → 粘到文件管理器"省 4-5 步；
//   - 这是 P3 体验项里"让工具箱更顺手"那一类。
//
// 平台命令由 open_dir_{darwin,unix,windows}.go 在编译期选择。Windows 实现直接
// 启动 explorer.exe，不经过 cmd.exe，避免合法路径字符被解释成 shell 运算符。
//
// 不要在 server 上跑非平台命令：server 默认 macOS 上运行（开发机），
// 部署到 Linux server 后这个函数仍然有效（只是行为是 xdg-open）。
//
// 调用方负责先做路径白名单校验（见 OpenPathAllowed），本函数只负责调命令。
func revealInFileManager(path string) error {
	cmd, label := platformRevealCommand(path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s 失败: %w", label, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// openFolderInFileManager 打开目录本身（进入该文件夹），而不是在父目录里选中它。
func openFolderInFileManager(dir string) error {
	cmd, label := platformOpenFolderCommand(dir)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s 失败: %w", label, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// openPathAllowed 校验 target 是否在 allowRoot 下。
//
// 用 filepath.Abs + filepath.Rel 双保险：
//   - 1) target 必须能解析成绝对路径；
//   - 2) 相对 allowRoot 的相对路径不能以 ".." 开头（防止 ../../etc/passwd）；
//   - 3) 相对路径本身 != ".." 也不等于 "../..."
//
// allowRoot 建议传 cfg.DownloadDir()（绝对路径）。
// 返回 nil = 允许；非 nil = 拒绝及原因。
//
// 设计动机：
//   - 防止恶意或手滑的请求把 /etc /var 之类的目录 reveal 给用户；
//   - 这种攻击看起来"无害"（只是打开 GUI），但会暴露敏感目录结构；
//   - 同路径穿越校验（handlers_files.go 里 free_file_roots）保持一致风格。
func openPathAllowed(allowRoot, target string) error {
	if allowRoot == "" {
		return errors.New("allowRoot 不能为空")
	}
	if target == "" {
		return errors.New("target 不能为空")
	}
	// 路径里直接含 NUL/换行立即拒（最严重的安全漏洞）
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
	// filepath.Rel 在不同盘（Windows）下返回错误；这里宽容处理：
	// 盘符不一致就拒绝。
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return fmt.Errorf("target 不在 allowRoot 下（跨盘符或绝对路径无效）")
	}
	// rel 必须是 "foo" / "foo/bar" / "." 这种；不能以 ".." 开头
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target 越界（%s 不在 %s 下）", absTarget, absRoot)
	}
	return nil
}
