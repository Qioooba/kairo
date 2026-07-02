//go:build !windows

// Package sysutil / autostart_other.go — 非 Windows 平台的"开机自启"实现。
//
// macOS / Linux 下 SetAutoStart / IsAutoStartEnabled 都是 no-op（return nil）。
// 内网工具箱主要在 Windows 上跑，macOS 是开发机、Linux 是部署 server ——
// 都不需要自启 GUI 到桌面，所以这里故意不做：
//   - 避免引入 LaunchAgent plist、systemd-user 等跨平台复杂度；
//   - 配置文件仍会记录用户的偏好，跨平台迁移不丢；
//   - 前端在非 Windows 平台显示提示文案，明确告知"该平台暂不支持"。
package sysutil

import "runtime"

// SetAutoStart 在非 Windows 平台是 no-op。
//
// 调用方：main.go 启动尾部 + handlers_autostart.go 的 PUT 路径。
// 返回 nil 是有意为之：调用方不应因为"非 Windows 不支持"而整体报错；
// 调用方会在响应里返回 actual=false 告知前端"未启用"。
func SetAutoStart(enabled bool, exePath string) error {
	return nil
}

// IsAutoStartEnabled 在非 Windows 平台始终返回 (false, nil)。
//
// 第二个返回值固定为 nil 是为了和 Windows 平台对称 ——
// 调用方按 (actual, err) 接收，看到 err==nil 就不会当失败处理。
func IsAutoStartEnabled() (bool, error) {
	return false, nil
}

// AutoStartKeyName 是注册表值名（在 Windows 平台有真实含义，这里给个占位避免编译期引用）。
//
// 实际只在 handlers_autostart.go 的 audit 日志里被引用一次 —— 跨平台都返回
// "<non-windows-platform>" 占位字符串，方便审计日志完整。
func AutoStartKeyName() string {
	return "KairoAutoStart:<non-windows-platform>"
}

// Supported 返回"该平台是否支持开机自启"。
//
// 当前仅 Windows 是 true；macOS / Linux 暂时不做（自用工具不上服务器 / 不需要
// 登录时自动启动 GUI）。后续要支持 macOS（LaunchAgent plist）或 Linux
// （systemd --user 或 XDG autostart），在这里加 build-tag 覆盖即可。
func Supported() bool {
	return false
}

// PlatformName 返回当前平台的简短标识（用于 /api/admin/autostart 响应的 platform 字段）。
func PlatformName() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "darwin"
	case "linux":
		return "linux"
	default:
		return runtime.GOOS
	}
}