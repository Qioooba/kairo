//go:build !windows

package sysutil

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type unixManagedCommand struct {
	cmd *exec.Cmd
}

// StartManagedCommand 启动受监管的命令，在 Unix 上建立独立进程组以支持 KillTree。
func StartManagedCommand(cmd *exec.Cmd) (ManagedCommand, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &unixManagedCommand{cmd: cmd}, nil
}

func (p *unixManagedCommand) Wait() error { return p.cmd.Wait() }

func (p *unixManagedCommand) KillTree() error {
	if p.cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (p *unixManagedCommand) Close() error { return nil }

// IsProcessAlive checks whether the process with the given pid is running.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}
