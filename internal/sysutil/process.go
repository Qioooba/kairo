package sysutil

// ManagedCommand 是一次已启动命令的跨平台生命周期控制器。
// KillTree 必须终止命令以及它派生的全部子孙进程（整棵进程树）。
type ManagedCommand interface {
	Wait() error
	KillTree() error
	Close() error
}
