package sysutil

// 跨进程文件锁（OTH-01：升级/恢复互斥）。
//
// 升级与恢复流程必须"任意时刻最多一个持有者"。旧实现只用
// `O_CREATE|O_EXCL` 加锁文件内的 PID 存活判断来回收陈旧锁，于是
// "读取旧 PID → 判断已死 → 删除路径 → 重新创建"之间存在读后删除窗口，
// 两个进程可以同时成功持锁。
//
// 这里改为内核锁：Unix 用非阻塞排他 `flock`，Windows 用
// `LockFileEx(LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY)`。
// 内核锁随句柄关闭或进程终止自动释放，因此崩溃遗留的锁文件不再是死锁，
// 也不需要任何人去 unlink 它。
//
// 锁文件路径一旦被当作内核锁对象就保持稳定：release 只解锁并关闭句柄，
// 绝不删除文件；否则新进程会创建新 inode 并在不受旧 inode 锁约束的情况下
// 拿到"同一路径上的另一把锁"，互斥重新被破坏。
//
// PID、token、时间戳仅作为诊断信息随锁写入，不参与互斥判定。

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// ErrFileLockHeld 表示目标文件上的内核排他锁已被其他持有者占用。
var ErrFileLockHeld = errors.New("sysutil: file lock is held by another owner")

// AcquireFileLock 在 path 上获取跨进程内核排他锁（非阻塞）。
//
// 成功时返回幂等的 release：解锁并关闭句柄，不删除锁文件。
// 已被占用时返回包装了 ErrFileLockHeld 的错误。
func AcquireFileLock(path string) (release func(), err error) {
	return AcquireFileLockWithDiagnostics(path, nil)
}

// AcquireFileLockWithDiagnostics 与 AcquireFileLock 相同，另外在持锁状态下
// 把 diagnostics 写入锁文件。诊断内容只用于排障，任何互斥判定都不读它。
//
// 写入复用同一个已加锁的句柄：Windows 上被锁区间的读写只允许加锁的那个
// 文件对象，另开句柄写入会直接失败。
func AcquireFileLockWithDiagnostics(path string, diagnostics []byte) (release func(), err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("sysutil: open lock file %s: %w", path, err)
	}
	if err := lockFileExclusive(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("sysutil: lock %s: %w", path, err)
	}
	if diagnostics != nil {
		if err := writeLockDiagnostics(file, diagnostics); err != nil {
			_ = unlockFile(file)
			_ = file.Close()
			return nil, fmt.Errorf("sysutil: write lock diagnostics %s: %w", path, err)
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = unlockFile(file)
			_ = file.Close()
		})
	}, nil
}

// writeLockDiagnostics 在持锁句柄上重写锁文件内容（仅诊断用途）。
func writeLockDiagnostics(file *os.File, data []byte) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.WriteAt(data, 0); err != nil {
		return err
	}
	return nil
}
