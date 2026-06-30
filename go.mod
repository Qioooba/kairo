module kairo

// go 指令必须 ≤ 1.20：Win7 兼容构建用 Go 1.20.x（Go 1.21+ 官方放弃 Win7/8/Server 2008/2012），
// go 指令高于 1.20 会导致 Go 1.20.x 工具链拒绝编译本模块。
// Win10/11 构建用更新版本的 Go 仍可正常编译 go 1.20 模块，不受影响。
// 详见 scripts/build_windows_both.sh。
go 1.20

require (
	fyne.io/systray v1.11.0
	github.com/gorilla/websocket v1.5.3
	github.com/pkg/sftp v1.13.6
	github.com/zalando/go-keyring v0.2.8
	golang.org/x/crypto v0.31.0
	golang.org/x/sys v0.28.0
	golang.org/x/text v0.21.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/kr/fs v0.1.0 // indirect
)
