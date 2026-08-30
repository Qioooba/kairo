package schedtask

import (
	"os/exec"
	"syscall"
)

// shellCommand 在 Windows 上通过 cmd.exe 执行用户命令。
//
// /D 禁用 AutoRun，避免机器级注册表配置改变定时任务语义。
// /S 让 /C 使用一致的引号规则。cmd.exe 不使用 CommandLineToArgvW，
// 不能依赖 os/exec 的通用参数编码；这里按 cmd.exe 语法构造原始命令行。
// 最外层引号会被 /S /C 消耗，内部引号则完整保留给用户命令。
func shellCommand(command string) *exec.Cmd {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `/D /S /C "` + command + `"`}
	return cmd
}
