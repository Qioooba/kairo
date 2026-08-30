package schedtask

// managedCommand 是一次已启动命令的跨平台生命周期控制器。
// KillTree 必须终止 shell 以及它派生的全部子孙进程。
type managedCommand interface {
	Wait() error
	KillTree() error
	Close() error
}
