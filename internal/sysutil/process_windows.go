//go:build windows

package sysutil

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsManagedCommand 把命令放入 KILL_ON_JOB_CLOSE Job Object。
// Job Object 是 Windows 上唯一能可靠表达“整个任务进程树”生命周期的原生机制。
type windowsManagedCommand struct {
	cmd *exec.Cmd
	job windows.Handle
}

// StartManagedCommand 启动受监管的命令，保证超时或取消时能够终止包括子孙进程在内的整棵进程树。
func StartManagedCommand(cmd *exec.Cmd) (ManagedCommand, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	// 挂起创建，先加入 Job Object 再恢复主线程，避免派生出不受管的孙进程
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW

	job, err := createKillOnCloseJob()
	if err != nil {
		return nil, fmt.Errorf("创建 Windows Job Object 失败: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}

	proc, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_INFORMATION,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return failSuspendedCommand(cmd, job, fmt.Errorf("打开子进程句柄失败: %w", err))
	}
	err = windows.AssignProcessToJobObject(job, proc)
	_ = windows.CloseHandle(proc)
	if err != nil {
		return failSuspendedCommand(cmd, job, fmt.Errorf("加入 Windows Job Object 失败: %w", err))
	}
	if err := resumeProcess(cmd.Process.Pid); err != nil {
		return failSuspendedCommand(cmd, job, fmt.Errorf("恢复子进程主线程失败: %w", err))
	}
	return &windowsManagedCommand{cmd: cmd, job: job}, nil
}

func failSuspendedCommand(cmd *exec.Cmd, job windows.Handle, cause error) (ManagedCommand, error) {
	_ = windows.CloseHandle(job)
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
	return nil, cause
}

func resumeProcess(pid int) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		if entry.OwnerProcessID == uint32(pid) {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, resumeErr := windows.ResumeThread(thread)
			_ = windows.CloseHandle(thread)
			return resumeErr
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			return fmt.Errorf("未找到 PID=%d 的主线程: %w", pid, err)
		}
	}
}

func createKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func (p *windowsManagedCommand) Wait() error { return p.cmd.Wait() }

func (p *windowsManagedCommand) KillTree() error {
	if p.cmd.Process == nil {
		return nil
	}
	if p.job != 0 {
		if err := windows.TerminateJobObject(p.job, 1); err == nil || errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		err := windows.CloseHandle(p.job)
		p.job = 0
		return err
	}
	err := p.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (p *windowsManagedCommand) Close() error {
	if p.job == 0 {
		return nil
	}
	err := windows.CloseHandle(p.job)
	p.job = 0
	return err
}

// IsProcessAlive checks whether the process with the given pid is running.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const statusPending = 259 // STILL_ACTIVE
	return code == statusPending
}
