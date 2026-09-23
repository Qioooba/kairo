// Package comparefs provides the storage abstraction used by the compare workbench.
// A comparison can combine any two implementations (local, SFTP or FTP/FTPS).
package comparefs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"
)

var ErrConflict = errors.New("target changed since it was compared")

// ErrSourceConflict reports that a source file changed while a compare/sync
// operation was preparing to copy it.  It is deliberately distinct from
// ErrConflict so callers can tell the user which side changed.
var ErrSourceConflict = errors.New("source changed while it was being copied")

// ErrTypeConflict is returned when a file operation targets a directory (or a
// directory operation encounters a file).  Treating this as a normal write
// error made file/directory name collisions look like a successful sync plan.
var ErrTypeConflict = errors.New("file and directory types conflict")

// ErrPathOutsideRoot is returned by the local backend when a path (including
// the path reached through a symbolic link) escapes the configured compare
// roots.
var ErrPathOutsideRoot = errors.New("path escapes compare allowed roots")

// IsNotFound is the one place where compare handlers decide whether a failed
// Stat means an absent path.  Permission, transport and protocol errors must
// not be silently interpreted as an empty directory.
func IsNotFound(err error) bool {
	return err != nil && (errors.Is(err, ErrNotFound) || errors.Is(err, fs.ErrNotExist))
}

// ErrNotFound is used by remote adapters when they can positively establish
// that an entry is absent (for example after successfully listing its parent).
// Local and SFTP adapters retain fs.ErrNotExist compatibility through
// IsNotFound above.
var ErrNotFound = errors.New("path does not exist")

type Version struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
	// Path binds the version token to the file identity it was read from.
	// Size and mtime alone cannot tell two distinct files apart (copied files,
	// same-second writes, FTP second precision), so a token issued for A must
	// never authorize a conditional write to B.  Empty means "legacy token
	// without identity" and is only accepted where a caller has no path.
	Path string `json:"path,omitempty"`
}

type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
	Mode    uint32    `json:"mode"`
	IsDir   bool      `json:"is_dir"`
}

func (e Entry) Version() Version { return Version{Size: e.Size, ModTime: e.ModTime, Path: e.Path} }

type WriteOptions struct {
	Expected *Version
	// ExpectedMissing requires the destination to remain absent until the
	// atomic replacement.  This closes the create-after-scan race for directory
	// sync plans; a nil Expected historically meant "do not check".
	ExpectedMissing bool
	Mode            uint32
	Backup          bool
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

// SamePathIdentity reports whether two path strings address the same file
// identity.  Separators are unified, redundant segments are removed and
// Windows-style paths are compared case-insensitively, so alternative spellings
// of the same file are not mistaken for a conflict.
func SamePathIdentity(a, b string) bool {
	left := normalizePathIdentity(a)
	right := normalizePathIdentity(b)
	return left != "" && left == right
}

func normalizePathIdentity(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	windowsLike := (len(text) > 1 && text[1] == ':') || strings.Contains(text, `\`)
	text = strings.ReplaceAll(text, `\`, "/")
	text = path.Clean(text)
	if windowsLike {
		text = strings.ToLower(text)
	}
	return text
}
