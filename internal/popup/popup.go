// Package popup 提供系统级提醒通知（Windows-only，非 Windows no-op）。
//
// 设计：
//   - Show(content string) 调用 Windows 原生通知区域消息提醒。
//   - 多条提醒排队显示，间隔约 1 秒，避免堆叠。
//   - 跨 Win 7 / Win 10/11：Win7 显示经典通知气泡，Win10/11 由系统通知接管样式。
//   - 不内嵌 PNG 资源，不自绘大背景图，避免拖大单 exe 体积。
package popup

// Show 弹出一个系统提醒。立即返回，不阻塞调用方。
//
// 非 Windows 平台：no-op（开发模式下不弹窗，避免污染 macOS / Linux 桌面）。
// Windows 平台：实际实现见 popup_windows.go。
func Show(content string) { show(content) }

// Shutdown 退出时清理资源（关闭队列、销毁窗口）。
func Shutdown() { shutdown() }

// QueueLength 当前队列长度（调试用）。
func QueueLength() int { return queueLength() }
