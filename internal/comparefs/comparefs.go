// Package comparefs provides the storage abstraction used by the compare workbench.
// A comparison can combine any two implementations (local, SFTP or FTP/FTPS).
package comparefs

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"time"
)

var ErrConflict = errors.New("target changed since it was compared")

type Version struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
	Mode    uint32    `json:"mode"`
	IsDir   bool      `json:"is_dir"`
}

func (e Entry) Version() Version { return Version{Size: e.Size, ModTime: e.ModTime} }

type WriteOptions struct {
	Expected *Version
	Mode     uint32
	Backup   bool
	// ModTime preserves the source timestamp during copy/synchronization. A zero
	// value keeps the backend's normal "written now" behavior used by text edits.
	ModTime time.Time
}

// FS deliberately exposes streams rather than []byte so large copies never need
// to be buffered in the HTTP server process.
type FS interface {
	Kind() string
	DisplayName() string
	Clean(name string) string
	Join(base, name string) string
	Stat(ctx context.Context, name string) (Entry, error)
	List(ctx context.Context, dir string) ([]Entry, error)
	Open(ctx context.Context, name string) (io.ReadCloser, error)
	MkdirAll(ctx context.Context, dir string, mode uint32) error
	WriteAtomic(ctx context.Context, name string, src io.Reader, opts WriteOptions) error
	Close() error
}

func RemoteClean(name string) string {
	if strings.TrimSpace(name) == "" {
		return "/"
	}
	cleaned := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

func SameVersion(a Entry, expected Version) bool {
	if a.Size != expected.Size {
		return false
	}
	// Some FTP servers only expose second/minute precision. A zero expected time
	// means the caller intentionally disabled the mtime part of conflict checking.
	return expected.ModTime.IsZero() || a.ModTime.Equal(expected.ModTime)
}
