module kairo

// v1 数据库工作台起，主线基线升级为 Go 1.24：使用当前数据库客户端，
// 并停止让 Win7 工具链约束主线依赖。Win7 版本留在 legacy 分支维护。
go 1.24.0

require (
	fyne.io/systray v1.11.0
	github.com/go-sql-driver/mysql v1.9.3
	github.com/godror/godror v0.51.4
	github.com/gorilla/websocket v1.5.3
	github.com/jlaffaye/ftp v0.2.4
	github.com/pkg/sftp v1.13.6
	github.com/redis/go-redis/v9 v9.20.0
	github.com/sijms/go-ora/v2 v2.8.24
	github.com/zalando/go-keyring v0.2.8
	golang.org/x/crypto v0.31.0
	golang.org/x/sys v0.30.0
	golang.org/x/text v0.21.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/VictoriaMetrics/easyproto v0.1.4 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/godror/knownpb v0.3.0 // indirect
	github.com/kr/fs v0.1.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/exp v0.0.0-20250506013437-ce4c2cf36ca6 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
)
