package comparefs

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	ftpclient "github.com/jlaffaye/ftp"
)

type FTPConfig struct {
	Host               string
	Port               int
	Username           string
	Password           string
	TLSMode            string // "", "explicit", "implicit"
	InsecureSkipVerify bool
}

type FTP struct {
	client *ftpclient.ServerConn
	name   string
	// jlaffaye/ftp's ServerConn has a single control channel and is not safe
	// for concurrent commands.  This token is part of the FTP backend's
	// protocol contract; Open holds it until the data stream is closed.
	opToken chan struct{}
}

func DialFTP(ctx context.Context, cfg FTPConfig) (*FTP, error) {
	if cfg.Port == 0 {
		if cfg.TLSMode == "implicit" {
			cfg.Port = 990
		} else {
			cfg.Port = 21
		}
	}
	addr := net.JoinHostPort(strings.TrimSpace(cfg.Host), strconv.Itoa(cfg.Port))
	opts := []ftpclient.DialOption{ftpclient.DialWithTimeout(15 * time.Second), ftpclient.DialWithContext(ctx)}
	if cfg.TLSMode != "" {
		tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.InsecureSkipVerify} // #nosec G402 -- explicit user option
		if cfg.TLSMode == "implicit" {
			opts = append(opts, ftpclient.DialWithTLS(tlsCfg))
		} else {
			opts = append(opts, ftpclient.DialWithExplicitTLS(tlsCfg))
		}
	}
	c, err := ftpclient.Dial(addr, opts...)
	if err != nil {
		return nil, err
	}
	if err := c.Login(cfg.Username, cfg.Password); err != nil {
		_ = c.Quit()
		return nil, err
	}
	return &FTP{client: c, name: "FTP " + addr, opToken: make(chan struct{}, 1)}, nil
}

func (f *FTP) Kind() string                  { return "ftp" }
func (f *FTP) DisplayName() string           { return f.name }
func (f *FTP) Clean(name string) string      { return RemoteClean(name) }
func (f *FTP) Join(base, name string) string { return path.Join(RemoteClean(base), name) }
func (f *FTP) CompareConcurrency() int       { return 1 }
func (f *FTP) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case f.opToken <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (f *FTP) release() { <-f.opToken }

func (f *FTP) Close() error {
	if err := f.acquire(context.Background()); err != nil {
		return err
	}
	defer f.release()
	return f.client.Quit()
}

func (f *FTP) Stat(ctx context.Context, name string) (Entry, error) {
	if err := f.acquire(ctx); err != nil {
		return Entry{}, err
	}
	defer f.release()
	return f.statLocked(ctx, name)
}

func (f *FTP) statLocked(ctx context.Context, name string) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	cleaned := RemoteClean(name)
	size, sizeErr := f.client.FileSize(cleaned)
	if sizeErr == nil {
		mtime, _ := f.client.GetTime(cleaned)
		return Entry{Name: path.Base(cleaned), Path: cleaned, Size: size, ModTime: mtime}, nil
	}
	parent := path.Dir(cleaned)
	entries, err := f.listLocked(ctx, parent, 0)
	if err != nil {
		return Entry{}, sizeErr
	}
	for _, entry := range entries {
		if entry.Path == cleaned {
			return entry, nil
		}
	}
	return Entry{}, ErrNotFound
}

func (f *FTP) List(ctx context.Context, dir string) ([]Entry, error) {
	if err := f.acquire(ctx); err != nil {
		return nil, err
	}
	defer f.release()
	return f.listLocked(ctx, dir, 0)
}

func (f *FTP) listLocked(ctx context.Context, dir string, max int) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items, err := f.client.List(RemoteClean(dir))
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if item.Name == "." || item.Name == ".." {
			continue
		}
		if max > 0 && len(entries) >= max {
			break
		}
		entries = append(entries, Entry{Name: item.Name, Path: path.Join(RemoteClean(dir), item.Name), Size: int64(item.Size), ModTime: item.Time, IsDir: item.Type == ftpclient.EntryTypeFolder})
	}
	return entries, nil
}

// ListLimited is intentionally conservative: FTP LIST does not expose a
// portable count without consuming the full response, so we return all entries
// and let the compare walker enforce its global cap.  The ServerConn remains
// serialized by the protocol token above.
func (f *FTP) ListLimited(ctx context.Context, dir string, max int) ([]Entry, bool, error) {
	if err := f.acquire(ctx); err != nil {
		return nil, false, err
	}
	defer f.release()
	entries, err := f.listLocked(ctx, dir, 0)
	if err != nil {
		return nil, false, err
	}
	if max > 0 && len(entries) > max {
		return entries[:max], true, nil
	}
	return entries, false, nil
}

func (f *FTP) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := f.acquire(ctx); err != nil {
		return nil, err
	}
	r, err := f.client.Retr(RemoteClean(name))
	if err != nil {
		f.release()
		return nil, err
	}
	reader := &ftpReadCloser{ReadCloser: r, release: f.release, done: make(chan struct{})}
	go reader.watch(ctx)
	return reader, nil
}

func (f *FTP) MkdirAll(ctx context.Context, dir string, mode uint32) error {
	if err := f.acquire(ctx); err != nil {
		return err
	}
	defer f.release()
	return f.mkdirAllLocked(ctx, dir, mode)
}

func (f *FTP) mkdirAllLocked(ctx context.Context, dir string, mode uint32) error {
	cleaned := RemoteClean(dir)
	current := "/"
	for _, part := range strings.Split(strings.TrimPrefix(cleaned, "/"), "/") {
		if part == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		current = path.Join(current, part)
		if err := f.client.MakeDir(current); err != nil {
			// Many FTP servers report an error when the directory already exists.
			entry, statErr := f.statLocked(ctx, current)
			if statErr != nil {
				return err
			}
			if !entry.IsDir {
				return ErrTypeConflict
			}
		}
	}
	return nil
}

func (f *FTP) WriteAtomic(ctx context.Context, name string, src io.Reader, opts WriteOptions) error {
	if err := f.acquire(ctx); err != nil {
		return err
	}
	defer f.release()
	return f.writeAtomicLocked(ctx, name, src, opts)
}

func (f *FTP) writeAtomicLocked(ctx context.Context, name string, src io.Reader, opts WriteOptions) error {
	cleaned := RemoteClean(name)
	current, statErr := f.statLocked(ctx, cleaned)
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
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	partial := cleaned + ".kairo-" + stamp + ".partial"
	if err := f.client.Stor(partial, &contextReader{ctx: ctx, reader: src}); err != nil {
		_ = f.client.Delete(partial)
		return err
	}
	// Recheck after upload, before any backup rename.  This is required for
	// directory sync plans where ExpectedMissing protects a newly created file.
	current, statErr = f.statLocked(ctx, cleaned)
	if statErr == nil {
		if current.IsDir {
			_ = f.client.Delete(partial)
			return ErrTypeConflict
		}
		if opts.ExpectedMissing || (opts.Expected != nil && !SameVersion(current, *opts.Expected)) {
			_ = f.client.Delete(partial)
			return ErrConflict
		}
	} else if !IsNotFound(statErr) || opts.Expected != nil {
		_ = f.client.Delete(partial)
		if IsNotFound(statErr) && opts.Expected != nil {
			return ErrConflict
		}
		return statErr
	}
	if opts.Backup {
		if _, err := f.statLocked(ctx, cleaned); err == nil {
			backup := cleaned + ".kairo-backup-" + stamp
			if err := f.client.Rename(cleaned, backup); err != nil {
				_ = f.client.Delete(partial)
				return err
			}
			if err := f.client.Rename(partial, cleaned); err != nil {
				_ = f.client.Rename(backup, cleaned)
				_ = f.client.Delete(partial)
				return fmt.Errorf("ftp atomic rename: %w", err)
			}
			if !opts.ModTime.IsZero() && f.client.IsSetTimeSupported() {
				if err := f.client.SetTime(cleaned, opts.ModTime); err != nil {
					return fmt.Errorf("ftp preserve modification time: %w", err)
				}
			}
			return nil
		} else if !IsNotFound(err) {
			_ = f.client.Delete(partial)
			return err
		}
	}
	if err := f.client.Rename(partial, cleaned); err != nil {
		_ = f.client.Delete(partial)
		return fmt.Errorf("ftp atomic rename: %w", err)
	}
	if !opts.ModTime.IsZero() && f.client.IsSetTimeSupported() {
		if err := f.client.SetTime(cleaned, opts.ModTime); err != nil {
			return fmt.Errorf("ftp preserve modification time: %w", err)
		}
	}
	return nil
}

type ftpReadCloser struct {
	io.ReadCloser
	release func()
	done    chan struct{}
	once    sync.Once
}

func (r *ftpReadCloser) Close() error {
	var err error
	r.once.Do(func() {
		close(r.done)
		err = r.ReadCloser.Close()
		r.release()
	})
	return err
}

func (r *ftpReadCloser) watch(ctx context.Context) {
	select {
	case <-ctx.Done():
		_ = r.Close()
	case <-r.done:
	}
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
