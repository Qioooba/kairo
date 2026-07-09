//go:build !windows

package iconextract

// 非 Windows 平台（开发环境 macOS / Linux）：no-op，返回 ErrUnsupported。
// 前端会 fallback 到按软件名推断颜色的 SVG 占位图，不影响使用。
func extractPNG(exePath string) ([]byte, error) {
	return nil, ErrUnsupported
}
