//go:build windows

package sysutil

// Windows 侧文件锁实现：对稳定文件句柄使用 LockFileEx 排他 + 立即失败模式。
// 构建标签与 process_windows.go 保持一致。

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFileExclusive 锁定整个字节范围（0 .. 0xFFFFFFFFFFFFFFFF）。
// 锁定范围可以超出文件末尾，因此"空锁文件"也能被锁定。
// 句柄保持到事务结束：进程被强杀时由内核自动释放锁。
func lockFileExclusive(file *os.File) error {
	overlapped := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		^uint32(0),
		^uint32(0),
		overlapped,
	)
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return ErrFileLockHeld
	}
	return err
}

// unlockFile 释放内核锁。绝不删除锁文件：路径必须保持稳定，
// 否则新文件对象上的锁会绕过旧文件对象上仍未释放的锁。
func unlockFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0,
		^uint32(0),
		^uint32(0),
		overlapped,
	)
}
