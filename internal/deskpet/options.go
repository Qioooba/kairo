package deskpet

import "kairo/internal/winui"

// Options 桌面宠物运行参数。
type Options struct {
	// Host 是 Kairo 原生窗口共用的 UI 线程。Windows 实现要求非 nil。
	Host *winui.Host

	// BaseURL 是本地 HTTP 服务的根地址，形如 http://127.0.0.1:18080。
	// 宠物通过它拉取 /api/pet/state 与皮肤精灵图。
	BaseURL string

	// LoadSprite 可选：直接返回某款皮肤的 PNG 字节。
	// 设置后宠物不再走 HTTP 拉取皮肤，而是走该回调（例如从 embed.FS 直读），
	// 能显著加快皮肤面板首屏加载。nil 时回退到 HTTP。
	LoadSprite func(id string) ([]byte, error)

	// DataDir 可选：用于持久化「宠物是否显示」的用户偏好（deskpet.json）。
	// 为空则不持久化。设置后，用户上次显示的宠物会在下次启动时自动显示。
	DataDir string
}
