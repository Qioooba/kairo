package comparefs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Local is the local compare backend.  When allowedRoots is non-empty every
// operation validates both the lexical path and the path after resolving
// symbolic links.  The latter is important because filepath.Clean alone does
// not prevent a link inside an allowed directory from reaching outside it.
// Empty roots means unrestricted (fail-open for local/intranet use).
type Local struct {
	allowedRoots []string
	restricted   bool
}

func NewLocal() *Local { return &Local{} }
func NewLocalWithAllowedRoots(roots []string) *Local {
	hasEffectiveRoot := false
	for _, root := range roots {
		if strings.TrimSpace(root) != "" {
			hasEffectiveRoot = true
			break
		}
	}
	if !hasEffectiveRoot {
		return &Local{}
	}
	normalized := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "*" || strings.EqualFold(root, "ANY") {
			if len(roots) == 1 {
				return &Local{}
			}
			continue
		}
		if root == "" {
			continue
		}
		root = localAbsolutePath(root)
		if !containsLocalPath(normalized, root) {
			normalized = append(normalized, root)
		}
	}
	return &Local{allowedRoots: normalized, restricted: true}
}
func (l *Local) Kind() string             { return "local" }
func (l *Local) DisplayName() string      { return "本地" }
func (l *Local) Clean(name string) string { return localAbsolutePath(name) }
func (l *Local) Join(base, name string) string {
	return localAbsolutePath(filepath.Join(base, filepath.FromSlash(name)))
}
func (l *Local) Close() error { return nil }

// CompareConcurrency advertises that local files are safe to compare in
// parallel.  Remote protocols expose lower values because their control
// channels have different concurrency guarantees.
func (l *Local) CompareConcurrency() int { return 8 }

func localAbsolutePath(name string) string {
	cleaned := filepath.Clean(name)
	abs, err := filepath.Abs(cleaned)
	if err == nil {
		return abs
	}
	return cleaned
}

func (l *Local) Stat(ctx context.Context, name string) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	cleaned, err := l.safePath(name, false)
	if err != nil {
		return Entry{}, err
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		return Entry{}, err
	}
	return localEntry(cleaned, info), nil
}

func (l *Local) List(ctx context.Context, dir string) ([]Entry, error) {
	cleaned, err := l.safePath(dir, false)
	if err != nil {
		return nil, err
	}
	dirFile, err := l.openChecked(cleaned)
	if err != nil {
		return nil, err
	}
	defer dirFile.Close()
	items, err := dirFile.ReadDir(-1)
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
			// A disappearing file is still a scan error.  Silently skipping it
			// makes one side look like a clean "only on other side" result.
			return nil, err
		}
		entries = append(entries, localEntry(filepath.Join(cleaned, item.Name()), info))
	}
	return entries, nil
}

// ListLimited is used by folder scans to preserve a reliable truncation bit
// without first allocating an unbounded []DirEntry. Read at most max+1
// entries from the directory handle to determine whether it was truncated.
func (l *Local) ListLimited(ctx context.Context, dir string, max int) ([]Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	cleaned, err := l.safePath(dir, false)
	if err != nil {
		return nil, false, err
	}
	dirFile, err := l.openChecked(cleaned)
	if err != nil {
		return nil, false, err
	}
	defer dirFile.Close()
	count := -1
	if max > 0 {
		count = max + 1
	}
	items, err := dirFile.ReadDir(count)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	entries := make([]Entry, 0, minLocalEntries(len(items), max))
	truncated := false
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if max > 0 && len(entries) >= max {
			truncated = true
			break
		}
		info, err := item.Info()
		if err != nil {
			return nil, false, err
		}
		entries = append(entries, localEntry(filepath.Join(cleaned, item.Name()), info))
	}
	return entries, truncated, nil
}

func localEntry(name string, info os.FileInfo) Entry {
	return Entry{Name: info.Name(), Path: localAbsolutePath(name), Size: info.Size(), ModTime: info.ModTime(), Mode: uint32(info.Mode()), IsDir: info.IsDir()}
}

func (l *Local) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cleaned, err := l.safePath(name, false)
	if err != nil {
		return nil, err
	}
	return l.openChecked(cleaned)
}

func (l *Local) MkdirAll(ctx context.Context, dir string, mode uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleaned, err := l.safePath(dir, true)
	if err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o755
	}
	return os.MkdirAll(cleaned, os.FileMode(mode))
}

func (l *Local) WriteAtomic(ctx context.Context, name string, src io.Reader, opts WriteOptions) error {
	cleaned, err := l.safePath(name, true)
	if err != nil {
		return err
	}
	if err := l.checkWriteTarget(ctx, cleaned, opts); err != nil {
		return err
	}
	dir := filepath.Dir(cleaned)
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
	// Recheck immediately before the replacement as well as before writing the
	// temporary file.  The first check protects the expensive copy; this one
	// closes the common create/modify race while the temporary file was built.
	if err := l.checkWriteTarget(ctx, cleaned, opts); err != nil {
		return err
	}
	if opts.Backup {
		if _, err := os.Stat(cleaned); err == nil {
			backup := cleaned + ".kairo-backup-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			if err := copyLocalFile(cleaned, backup); err != nil {
				return fmt.Errorf("create backup: %w", err)
			}
		}
	}
	if err := os.Rename(tmpName, cleaned); err != nil {
		return err
	}
	ok = true
	return nil
}

func (l *Local) checkWriteTarget(ctx context.Context, name string, opts WriteOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(name)
	if err == nil {
		if info.IsDir() {
			return ErrTypeConflict
		}
		if opts.ExpectedMissing {
			return ErrConflict
		}
		if opts.Expected != nil && !SameVersion(localEntry(name, info), *opts.Expected) {
			return ErrConflict
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if opts.Expected != nil {
		return ErrConflict
	}
	return nil
}

func (l *Local) safePath(name string, forWrite bool) (string, error) {
	if strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("%w: path contains NUL", ErrPathOutsideRoot)
	}
	cleaned := localAbsolutePath(name)
	if os.PathSeparator == '\\' && strings.Contains(cleaned[len(filepath.VolumeName(cleaned)):], ":") {
		return "", fmt.Errorf("%w: alternate data stream", ErrPathOutsideRoot)
	}
	if !l.restricted {
		return cleaned, nil
	}
	if len(l.allowedRoots) == 0 {
		return "", fmt.Errorf("%w: no local roots configured", ErrPathOutsideRoot)
	}
	actual, err := resolveLocalPath(cleaned, forWrite)
	if err != nil {
		return "", err
	}
	if !localPathAllowed(cleaned, l.allowedRoots) || !localPathAllowed(actual, l.allowedRoots) {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, cleaned)
	}
	return cleaned, nil
}

func resolveLocalPath(cleaned string, forWrite bool) (string, error) {
	if actual, err := filepath.EvalSymlinks(cleaned); err == nil {
		return localAbsolutePath(actual), nil
	} else if !forWrite || !os.IsNotExist(err) {
		return "", err
	}
	// For a new file, resolve the nearest existing ancestor and append the
	// missing suffix.  This catches a symlinked parent even when the leaf does
	// not exist yet.
	probe := cleaned
	missing := make([]string, 0, 4)
	for {
		actual, err := filepath.EvalSymlinks(probe)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				actual = filepath.Join(actual, missing[i])
			}
			return localAbsolutePath(actual), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", err
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}

func localPathAllowed(candidate string, roots []string) bool {
	candidate = localAbsolutePath(candidate)
	for _, root := range roots {
		root = localAbsolutePath(root)
		prefix := root
		if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
			prefix += string(os.PathSeparator)
		}
		if sameLocalPath(candidate, root) || hasLocalPathPrefix(candidate, prefix) {
			return true
		}
	}
	return false
}

func hasLocalPathPrefix(candidate, prefix string) bool {
	if os.PathSeparator == '\\' {
		return strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(prefix))
	}
	return strings.HasPrefix(candidate, prefix)
}

func sameLocalPath(a, b string) bool {
	if os.PathSeparator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func containsLocalPath(roots []string, candidate string) bool {
	for _, root := range roots {
		if sameLocalPath(root, candidate) {
			return true
		}
	}
	return false
}

func minLocalEntries(n, max int) int {
	if max > 0 && n > max {
		return max
	}
	return n
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

// Anchor reads to an allowed directory handle so a concurrent parent-link
// replacement cannot redirect the open outside the configured root.
func (l *Local) openChecked(name string) (*os.File, error) {
	if !l.restricted {
		return os.Open(name)
	}
	for _, allowed := range l.allowedRoots {
		if !localPathAllowed(name, []string{allowed}) {
			continue
		}
		rel, err := filepath.Rel(allowed, name)
		if err != nil {
			return nil, err
		}
		root, err := os.OpenRoot(allowed)
		if err != nil {
			return nil, err
		}
		f, err := root.Open(rel)
		root.Close()
		return f, err
	}
	return nil, ErrPathOutsideRoot
}
