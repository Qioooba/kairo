package comparefs

import (
	"context"
	"io"
	"os"
	"path"
	"strconv"
	"time"

	"kairo/internal/sftpclient"
)

type SFTP struct {
	client *sftpclient.Client
	closer io.Closer
	name   string
}

func NewSFTP(client *sftpclient.Client, connection io.Closer, name string) *SFTP {
	return &SFTP{client: client, closer: connection, name: name}
}

func (s *SFTP) Kind() string                  { return "sftp" }
func (s *SFTP) DisplayName() string           { return s.name }
func (s *SFTP) Clean(name string) string      { return RemoteClean(name) }
func (s *SFTP) Join(base, name string) string { return path.Join(RemoteClean(base), name) }
func (s *SFTP) CompareConcurrency() int       { return 4 }

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
	info, err := s.client.Stat(name)
	if err != nil {
		return Entry{}, err
	}
	return sftpEntry(name, info), nil
}

func (s *SFTP) List(ctx context.Context, dir string) ([]Entry, error) {
	infos, _, err := s.client.ListLimited(dir, 0)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(infos))
	for _, info := range infos {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries = append(entries, sftpEntry(path.Join(dir, info.Name()), info))
	}
	return entries, nil
}

// ListLimited preserves the remote backend's exact truncation signal.  The
// compare walker uses this optional capability to stop safely instead of
// treating an SFTP-side cap as a complete directory.
func (s *SFTP) ListLimited(ctx context.Context, dir string, max int) ([]Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	infos, truncated, err := s.client.ListLimited(dir, max)
	if err != nil {
		return nil, false, err
	}
	entries := make([]Entry, 0, len(infos))
	for _, info := range infos {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		entries = append(entries, sftpEntry(path.Join(dir, info.Name()), info))
	}
	return entries, truncated, nil
}

func sftpEntry(name string, info os.FileInfo) Entry {
	return Entry{Name: info.Name(), Path: name, Size: info.Size(), ModTime: info.ModTime(), Mode: uint32(info.Mode()), IsDir: info.IsDir()}
}

func (s *SFTP) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.client.Open(name)
}

func (s *SFTP) MkdirAll(ctx context.Context, dir string, mode uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.client.MkdirAll(dir)
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
