//go:build !windows

package schedtask

import "os/exec"

// shellCommand 在 macOS / Linux 上通过 POSIX shell 执行用户命令。
func shellCommand(command string) *exec.Cmd {
	return exec.Command("sh", "-c", command)
}
