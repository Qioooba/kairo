// Package popup 提供系统级悬浮通知弹窗（Windows-only，非 Windows no-op）。
//
// 设计：
//   - Show(content string) 立即在屏幕右下角弹一个悬浮卡片，8 秒后自动 fade-out。
//   - 多条提醒排队显示，间隔 1 秒，避免堆叠。
//   - 跨 Win 7 / Win 10：用纯 GDI+ + PNG 背景图分层渲染，不依赖 DWM 圆角
//     或 Action Center 等 Win10 only API。
//   - 主题跟随系统：Win10 用 ShouldAppsUseDarkMode；Win7 始终浅色。
package popup

// Show 在屏幕右下角弹一个提醒卡片。立即返回，不阻塞调用方。
//
// 非 Windows 平台：no-op（开发模式下不弹窗，避免污染 macOS / Linux 桌面）。
// Windows 平台：实际实现见 popup_windows.go。
func Show(content string) { show(content) }

// Shutdown 退出时清理资源（关闭队列、销毁窗口）。
func Shutdown() { shutdown() }

// QueueLength 当前队列长度（调试用）。
func QueueLength() int { return queueLength() }