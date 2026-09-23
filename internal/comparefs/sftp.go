package comparefs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"sync"
	"time"

	"kairo/internal/sftpclient"
)

type SFTP struct {
	client *sftpclient.Client
	closer io.Closer
	name   string

	// OTH-05：展示相对路径用于左右对齐，但 Stat/Open 必须用列表结果给出的
	// **源文件身份**（原始字节绝对路径）。否则同显示名的 UTF-8/GBK 条目
	// 会被按展示名重新解析，既可能命中错误条目，也会退化成每个文件一次完整枚举。
	//
	// 身份在 List/ListLimited/Stat 时登记；同一展示路径出现两个不同身份时
	// 显式报歧义，绝不静默覆盖。
	idMu sync.RWMutex
	ids  map[string]string
}

func NewSFTP(client *sftpclient.Client, connection io.Closer, name string) *SFTP {
	return &SFTP{client: client, closer: connection, name: name, ids: make(map[string]string)}
}

func (s *SFTP) Kind() string                  { return "sftp" }
func (s *SFTP) DisplayName() string           { return s.name }
func (s *SFTP) Clean(name string) string      { return RemoteClean(name) }
func (s *SFTP) Join(base, name string) string { return path.Join(RemoteClean(base), name) }
func (s *SFTP) CompareConcurrency() int       { return 4 }

// rememberIdentity 登记"展示相对路径 → 源文件身份"。
// 同一路径映射到两个不同身份时返回歧义错误（调用方必须上报，不能覆盖）。
func (s *SFTP) rememberIdentity(displayPath, identity string) error {
	if displayPath == "" || identity == "" {
		return nil
	}
	s.idMu.Lock()
	defer s.idMu.Unlock()
	if s.ids == nil {
		s.ids = make(map[string]string)
	}
	if prev, ok := s.ids[displayPath]; ok {
		if prev != identity {
			return fmt.Errorf("%w: 比对路径 %q 对应两个不同的源文件身份", sftpclient.ErrAmbiguousPath, displayPath)
		}
		return nil
	}
	s.ids[displayPath] = identity
	return nil
}

// identityFor 查展示路径对应的源文件身份；没有登记时返回 false（调用方回退展示路径）。
func (s *SFTP) identityFor(displayPath string) (string, bool) {
	s.idMu.RLock()
	defer s.idMu.RUnlock()
	id, ok := s.ids[displayPath]
	return id, ok
}

// resolveTarget 把"展示相对路径"换成给 sftpclient 的访问目标：
// 有身份就用身份 token（零重新解析），否则退化为展示路径（旧行为）。
func (s *SFTP) resolveTarget(name string) string {
	if id, ok := s.identityFor(name); ok {
		return sftpclient.EncodePathIdentity(id)
	}
	return name
}

// entryIdentity 取列表条目携带的原始绝对路径；外部 backend 没带时退化为展示路径。
func entryIdentity(displayPath string, info os.FileInfo) string {
	if raw := sftpclient.RawPathOf(info); raw != "" {
		return raw
	}
	return displayPath
}

func (s *SFTP) Close() error {
	err := s.client.Close()
	if s.closer != nil {
		if closeErr := s.closer.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

func (s *SFTP) Stat(ctx context.Context, name string) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	info, err := s.client.Stat(s.resolveTarget(name))
	if err != nil {
		return Entry{}, err
	}
	// 记下这次 Stat 解析到的源身份，后续 Open 就能直接复用（不再枚举目录）。
	identity := sftpclient.RawPathOf(info)
	if identity == "" {
		if id, ok := s.identityFor(name); ok {
			identity = id
		}
	}
	if identity != "" {
		if rerr := s.rememberIdentity(name, identity); rerr != nil {
			return Entry{}, rerr
		}
	}
	return sftpEntry(name, info), nil
}

func (s *SFTP) List(ctx context.Context, dir string) ([]Entry, error) {
	infos, _, err := s.client.ListLimitedCtx(ctx, dir, 0)
	if err != nil {
		return nil, err
	}
	return s.entriesFrom(dir, infos)
}

// ListLimited preserves the remote backend's exact truncation signal.  The
// compare walker uses this optional capability to stop safely instead of
// treating an SFTP-side cap as a complete directory.
func (s *SFTP) ListLimited(ctx context.Context, dir string, max int) ([]Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	infos, truncated, err := s.client.ListLimitedCtx(ctx, dir, max)
	if err != nil {
		return nil, false, err
	}
	entries, err := s.entriesFrom(dir, infos)
	if err != nil {
		return nil, false, err
	}
	return entries, truncated, nil
}

// entriesFrom 把原始条目转成展示路径的 Entry，同时登记每个条目的源文件身份。
// 同目录下两条显示名相同的条目会在登记时暴露为歧义错误。
func (s *SFTP) entriesFrom(dir string, infos []os.FileInfo) ([]Entry, error) {
	entries := make([]Entry, 0, len(infos))
	for _, info := range infos {
		displayPath := path.Join(dir, info.Name())
		if err := s.rememberIdentity(displayPath, entryIdentity(displayPath, info)); err != nil {
			return nil, err
		}
		entries = append(entries, sftpEntry(displayPath, info))
	}
	return entries, nil
}

func sftpEntry(name string, info os.FileInfo) Entry {
	return Entry{Name: info.Name(), Path: name, Size: info.Size(), ModTime: info.ModTime(), Mode: uint32(info.Mode()), IsDir: info.IsDir()}
}

func (s *SFTP) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.client.OpenCtx(ctx, s.resolveTarget(name))
}

func (s *SFTP) MkdirAll(ctx context.Context, dir string, mode uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// 目录创建走 sftpclient 的专用解析（OTH-04）：逐段保留已解析原始前缀，
	// 不依赖父目录计划先执行，也不会把混合 UTF-8/GBK 的整条路径整体转码。
	return s.client.MkdirAllCtx(ctx, dir)
}

func (s *SFTP) WriteAtomic(ctx context.Context, name string, src io.Reader, opts WriteOptions) error {
	current, statErr := s.Stat(ctx, name)
	if statErr == nil {
		if current.IsDir {
			return ErrTypeConflict
		}
		if opts.ExpectedMissing {
			return ErrConflict
		}
		if opts.Expected != nil && !SameVersion(current, *opts.Expected) {
			return ErrConflict
		}
	} else if !IsNotFound(statErr) {
		return statErr
	} else if opts.Expected != nil {
		return ErrConflict
	}
	mode := os.FileMode(opts.Mode)
	if mode == 0 {
		mode = 0o644
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	partial := name + ".kairo-" + stamp + ".partial"
	if err := s.client.UploadStream(ctx, src, partial, mode, nil); err != nil {
		_ = s.client.Remove(partial)
		return err
	}
	// Recheck the expected destination after uploading the temporary file.  A
	// concurrent writer must not be silently overwritten while this request was
	// transferring data.
	current, statErr = s.Stat(ctx, name)
	if statErr == nil {
		if current.IsDir {
			_ = s.client.Remove(partial)
			return ErrTypeConflict
		}
		if opts.ExpectedMissing || (opts.Expected != nil && !SameVersion(current, *opts.Expected)) {
			_ = s.client.Remove(partial)
			return ErrConflict
		}
	} else if !IsNotFound(statErr) || opts.Expected != nil {
		_ = s.client.Remove(partial)
		if IsNotFound(statErr) && opts.Expected != nil {
			return ErrConflict
		}
		return statErr
	}
	if opts.Backup {
		if _, err := s.client.Stat(name); err == nil {
			backup := name + ".kairo-backup-" + stamp
			if err := s.client.Rename(name, backup); err != nil {
				_ = s.client.Remove(partial)
				return err
			}
			if err := s.client.Rename(partial, name); err != nil {
				_ = s.client.Rename(backup, name)
				_ = s.client.Remove(partial)
				return err
			}
			if !opts.ModTime.IsZero() {
				_ = s.client.Chtimes(name, opts.ModTime, opts.ModTime)
			}
			return nil
		} else if !IsNotFound(err) {
			_ = s.client.Remove(partial)
			return err
		}
	}
	if err := s.client.Rename(partial, name); err != nil {
		_ = s.client.Remove(partial)
		return err
	}
	if !opts.ModTime.IsZero() {
		_ = s.client.Chtimes(name, opts.ModTime, opts.ModTime)
	}
	return nil
}
