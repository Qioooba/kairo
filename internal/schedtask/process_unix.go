//go:build !windows

package schedtask

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type unixManagedCommand struct {
	cmd *exec.Cmd
}

func startManagedCommand(cmd *exec.Cmd) (managedCommand, error) {
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
