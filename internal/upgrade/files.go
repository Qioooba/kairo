package upgrade

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"kairo/internal/sysutil"
)

func compareProductVersions(current, previous string) int {
	parse := func(value string) ([]int, bool) {
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		if cut := strings.IndexAny(value, "-+"); cut >= 0 {
			value = value[:cut]
		}
		parts := strings.Split(value, ".")
		if len(parts) < 2 {
			return nil, false
		}
		out := make([]int, len(parts))
		for i, part := range parts {
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 {
				return nil, false
			}
			out[i] = n
		}
		return out, true
	}
	a, okA := parse(current)
	b, okB := parse(previous)
	if !okA || !okB {
		return 0 // dev/custom builds remain usable; file schema checks still apply.
	}
	max := len(a)
	if len(b) > max {
		max = len(b)
	}
	for i := 0; i < max; i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

func writeAtomic(path string, raw []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".upgrade-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := replaceFile(tmpName, path); err != nil {
		return err
	}
	// rename 本身不保证掉电后目录项持久；补一次父目录 fsync（平台不支持时忽略）。
	if dir, dirErr := os.Open(filepath.Dir(path)); dirErr == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	ok = true
	return nil
}

func replaceFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	backup := dst + ".upgrade-replace-backup"
	_ = os.Remove(backup)
	if err := os.Rename(dst, backup); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return os.Rename(src, dst)
		}
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		_ = os.Rename(backup, dst)
		return err
	}
	_ = os.Remove(backup)
	return nil
}

// acquireLock 获取升级/恢复事务的跨进程互斥锁。
//
// 互斥只由内核锁保证（sysutil.AcquireFileLock：Unix flock / Windows LockFileEx），
// 因此：
//   - 崩溃遗留的锁文件不再是死锁：内核在进程终止时自动释放锁，下一个进程
//     直接获取即可，无需删文件，也不会出现"两个进程各自删锁再建锁"的竞争；
//   - 锁文件路径保持稳定，release 只解锁并关闭句柄、绝不 unlink，
//     避免新 inode 绕过旧 inode 上仍未释放的锁；
//   - PID/token/时间戳只写进锁文件作诊断，不参与任何互斥判定，
//     所以 PID 复用或元数据不完整都不会影响正确性。
func acquireLock(path string, now time.Time) (func(), error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("upgrade: initialize lock: %w", err)
	}
	content := []byte("pid=" + strconv.Itoa(os.Getpid()) +
		"\ntoken=" + hex.EncodeToString(nonce) +
		"\nstarted_at=" + now.UTC().Format(time.RFC3339Nano) + "\n")
	release, err := sysutil.AcquireFileLockWithDiagnostics(path, content)
	if err != nil {
		if errors.Is(err, sysutil.ErrFileLockHeld) {
			return nil, fmt.Errorf("upgrade: another upgrade may be running (%s): %w", path, err)
		}
		return nil, fmt.Errorf("upgrade: acquire lock (%s): %w", path, err)
	}
	return release, nil
}
