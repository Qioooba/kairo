package schedtask

import (
	"os/exec"

	"kairo/internal/sysutil"
)

// managedCommand 是一次已启动命令的跨平台生命周期控制器。
type managedCommand = sysutil.ManagedCommand

func startManagedCommand(cmd *exec.Cmd) (managedCommand, error) {
	return sysutil.StartManagedCommand(cmd)
}
