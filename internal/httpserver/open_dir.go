package httpserver

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
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
// 平台选择走 build tag 也可以，但这里 runtime.GOOS 在 binary 里被编译期常量替换，
// 等同于 build tag 效果，但所有平台代码都集中在一个文件里好读。
//
// 不要在 server 上跑非平台命令：server 默认 macOS 上运行（开发机），
// 部署到 Linux server 后这个函数仍然有效（只是行为是 xdg-open）。
//
// 调用方负责先做路径白名单校验（见 OpenPathAllowed），本函数只负责调命令。
func revealInFileManager(path string) error {
	switch runtime.GOOS {
	case "darwin":
		// open -R 在 Finder 里 reveal（不打开新窗口选中）
		cmd := exec.Command("open", "-R", path)
		// 不等命令退出：open 是 fork+exec 类型，立即返回；
		// 等它会卡住当前请求。
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("open -R 失败: %w", err)
		}
		go func() { _ = cmd.Wait() }()
		return nil
	case "windows":
		// explorer.exe /select,<path>：选中文件
		// 注意 /select 后紧跟逗号，逗号后是路径；逗号必须紧贴、不能用空格分隔
		cmd := exec.Command("explorer.exe", "/select,"+path)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("explorer.exe 失败: %w", err)
		}
		go func() { _ = cmd.Wait() }()
		return nil
	default:
		// Linux：xdg-open 父目录（Linux 文件管理器没统一 reveal 协议）
		dir := filepath.Dir(path)
		cmd := exec.Command("xdg-open", dir)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("xdg-open 失败: %w", err)
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
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
