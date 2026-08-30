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
	return &FTP{client: c, name: "FTP " + addr}, nil
}

func (f *FTP) Kind() string                  { return "ftp" }
func (f *FTP) DisplayName() string           { return f.name }
func (f *FTP) Clean(name string) string      { return RemoteClean(name) }
func (f *FTP) Join(base, name string) string { return path.Join(RemoteClean(base), name) }
func (f *FTP) Close() error                  { return f.client.Quit() }

func (f *FTP) Stat(ctx context.Context, name string) (Entry, error) {
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
	entries, err := f.List(ctx, parent)
	if err != nil {
		return Entry{}, sizeErr
	}
	for _, entry := range entries {
		if entry.Path == cleaned {
			return entry, nil
		}
	}
	return Entry{}, sizeErr
}

func (f *FTP) List(ctx context.Context, dir string) ([]Entry, error) {
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
		entries = append(entries, Entry{Name: item.Name, Path: path.Join(RemoteClean(dir), item.Name), Size: int64(item.Size), ModTime: item.Time, IsDir: item.Type == ftpclient.EntryTypeFolder})
	}
	return entries, nil
}

func (f *FTP) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.client.Retr(RemoteClean(name))
}

func (f *FTP) MkdirAll(ctx context.Context, dir string, mode uint32) error {
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
			if _, statErr := f.Stat(ctx, current); statErr != nil {
				return err
			}
		}
	}
	return nil
}

func (f *FTP) WriteAtomic(ctx context.Context, name string, src io.Reader, opts WriteOptions) error {
	cleaned := RemoteClean(name)
	if opts.Expected != nil {
		current, err := f.Stat(ctx, cleaned)
		if err != nil || !SameVersion(current, *opts.Expected) {
			return ErrConflict
		}
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	partial := cleaned + ".kairo-" + stamp + ".partial"
	if err := f.client.Stor(partial, &contextReader{ctx: ctx, reader: src}); err != nil {
		_ = f.client.Delete(partial)
		return err
	}
	if opts.Backup {
		if _, err := f.Stat(ctx, cleaned); err == nil {
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
