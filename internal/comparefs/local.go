package comparefs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Local struct{}

func NewLocal() *Local                         { return &Local{} }
func (l *Local) Kind() string                  { return "local" }
func (l *Local) DisplayName() string           { return "本地" }
func (l *Local) Clean(name string) string      { return filepath.Clean(name) }
func (l *Local) Join(base, name string) string { return filepath.Join(base, filepath.FromSlash(name)) }
func (l *Local) Close() error                  { return nil }

func (l *Local) Stat(ctx context.Context, name string) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	info, err := os.Stat(name)
	if err != nil {
		return Entry{}, err
	}
	return localEntry(name, info), nil
}

func (l *Local) List(ctx context.Context, dir string) ([]Entry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := item.Info()
		if err != nil {
			continue
		}
		entries = append(entries, localEntry(filepath.Join(dir, item.Name()), info))
	}
	return entries, nil
}

func localEntry(name string, info os.FileInfo) Entry {
	return Entry{Name: info.Name(), Path: name, Size: info.Size(), ModTime: info.ModTime(), Mode: uint32(info.Mode()), IsDir: info.IsDir()}
}

func (l *Local) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.Open(name)
}

func (l *Local) MkdirAll(ctx context.Context, dir string, mode uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o755
	}
	return os.MkdirAll(dir, os.FileMode(mode))
}

func (l *Local) WriteAtomic(ctx context.Context, name string, src io.Reader, opts WriteOptions) error {
	if opts.Expected != nil {
		current, err := l.Stat(ctx, name)
		if err != nil || !SameVersion(current, *opts.Expected) {
			return ErrConflict
		}
	}
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".kairo-compare-*.partial")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()

	buf := make([]byte, 128*1024)
	if _, err := copyContext(ctx, tmp, src, buf); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	mode := os.FileMode(opts.Mode)
	if mode == 0 {
		mode = 0o644
	}
	if err := os.Chmod(tmpName, mode.Perm()); err != nil {
		return err
	}
	if !opts.ModTime.IsZero() {
		if err := os.Chtimes(tmpName, opts.ModTime, opts.ModTime); err != nil {
			return fmt.Errorf("preserve modification time: %w", err)
		}
	}
	if opts.Backup {
		if _, err := os.Stat(name); err == nil {
			backup := name + ".kairo-backup-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			if err := copyLocalFile(name, backup); err != nil {
				return fmt.Errorf("create backup: %w", err)
			}
		}
	}
	if err := os.Rename(tmpName, name); err != nil {
		return err
	}
	ok = true
	return nil
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader, buf []byte) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func copyLocalFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(to)
		}
	}()
	if _, err := io.CopyBuffer(out, in, make([]byte, 128*1024)); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	ok = true
	return nil
}
