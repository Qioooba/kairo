//go:build !windows

package sysutil

// Unix 侧文件锁实现：对稳定存在的文件描述符使用非阻塞排他 flock。
// 构建标签与 process_unix.go 保持一致。

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockFileExclusive 对已打开的描述符加非阻塞排他 flock。
// flock 以 open file description 为单位，因此同一进程里另行 open 得到的
// 新描述符同样会冲突——"任意时刻最多一个持有者"在同进程内也成立。
func lockFileExclusive(file *os.File) error {
	fd := int(file.Fd())
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if errors.Is(err, unix.EINTR) {
			continue // 被信号打断：非阻塞请求可以直接重试
		}
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return ErrFileLockHeld
		}
		return err
	}
}

// unlockFile 释放内核锁。绝不 unlink 锁文件：路径必须保持稳定，
// 否则新 inode 上的锁会绕过旧 inode 上仍未释放的锁。
func unlockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
