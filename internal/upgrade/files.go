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

func acquireLock(path string, now time.Time) (func(), error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("upgrade: initialize lock: %w", err)
	}
	content := "pid=" + strconv.Itoa(os.Getpid()) +
		"\ntoken=" + hex.EncodeToString(nonce) +
		"\nstarted_at=" + now.UTC().Format(time.RFC3339Nano) + "\n"
	create := func() (*os.File, error) {
		return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	}
	file, err := create()
	// Age is not proof that the owner exited; never steal an upgrade lock.
	if err != nil {
		return nil, fmt.Errorf("upgrade: another upgrade may be running (%s): %w", path, err)
	}
	_, writeErr := file.WriteString(content)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("upgrade: initialize lock: %w", errors.Join(writeErr, closeErr))
	}
	return func() {
		// 锁可能已被陈旧接管而属于别的事务；只清理仍然属于自己的锁，
		// 绝不能删掉接管者的锁（否则第三个事务就能与当前事务并发）。
		if raw, readErr := os.ReadFile(path); readErr == nil && string(raw) == content {
			_ = os.Remove(path)
		}
	}, nil
}
