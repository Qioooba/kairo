// Package iconextract 从可执行文件提取图标，转为 PNG bytes。
//
// 用途：外部打开器（Notepad++ / VS Code 等）配置后，前端在配置页 / 下载历史 /
// 文件管理 / SFTP 编辑按钮处展示该软件的真实图标，提升辨识度与美观度。
//
// 平台支持：
//   - Windows：用 SHGetFileInfo 拿 HICON → GetIconInfo → GetDIBits → RGBA → PNG。
//   - 其他平台：暂不支持（开发环境为 macOS/Linux），ExtractPNG 返回 ErrUnsupported。
//     前端会 fallback 到按软件名推断颜色的 SVG 占位图，不影响使用。
package iconextract

import "errors"

// ErrUnsupported 当前平台不支持图标提取（仅 Windows 实现）。
var ErrUnsupported = errors.New("iconextract: 当前平台不支持 exe 图标提取")

// ExtractPNG 从 exePath 提取图标，返回 32x32 PNG bytes。
//
// 同一 exePath 多次调用结果一致（无随机性）；调用方应自行缓存到磁盘。
// 失败场景：平台不支持 / 文件不存在 / 无图标资源 / GDI 调用失败。
func ExtractPNG(exePath string) ([]byte, error) {
	return extractPNG(exePath)
}
