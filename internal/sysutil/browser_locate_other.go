//go:build !windows

// Package sysutil / browser_locate_other.go — 非 Windows 平台的 Chrome 探测。
//
// macOS: Chrome.app 通常装在 /Applications/Google Chrome.app —— 调用方是否
//        走"打开 Chrome.app"取决于 product 定位；当前 Kairo 在 macOS 上仅
//        做开发，不做 GUI 分发，所以这里 stub 掉，避免误触发。
// Linux: Chrome 几乎不存在"系统默认浏览器"概念，distro 默认走 xdg-open
//        等图形会话的标准 handle；用户层面不会期望工具箱特殊处理 Chrome。
//
// 调用方在非 Windows 上直接走 open/xdg-open（即"系统默认浏览器"），逻辑在 main.go。
package sysutil

import "kairo/internal/browserpref"

// BrowserCandidate 描述探测到的现代浏览器信息。
type BrowserCandidate struct {
	Kind browserpref.Kind
	Name string
	Path string
}

// FindModernBrowser 在非 Windows 平台是 no-op —— 永远返回 (BrowserCandidate{}, false)。
func FindModernBrowser() (BrowserCandidate, bool) {
	return BrowserCandidate{}, false
}

// FindChrome 在非 Windows 平台是 no-op —— 永远返回 ("", false)。
//
// 调用方：main.go openBrowser() 的探测链。
func FindChrome() (string, bool) {
	return "", false
}
