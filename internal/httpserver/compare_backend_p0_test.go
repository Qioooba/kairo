package httpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kairo/internal/comparefs"
)

func TestCompareSourceChangesBeforePublishPreservesTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader := &compareVerifiedReader{reader: io.NopCloser(strings.NewReader("changed")), expectedSize: 7, verify: func() error { return comparefs.ErrSourceConflict }}
	err := comparefs.NewLocal().WriteAtomic(context.Background(), target, reader, comparefs.WriteOptions{})
	if !errors.Is(err, comparefs.ErrSourceConflict) {
		t.Fatalf("source conflict not propagated: %v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "original" {
		t.Fatalf("changed source published: %q", data)
	}
}

func TestCompareWalkDepthBoundaryIsPending(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b", "c", "leaf.txt"), []byte("leaf"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, truncated, pending, err := walkCompareFSWithMeta(context.Background(), comparefs.NewLocal(), root, compareScanReq{MaxDepth: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("small tree must not be truncated")
	}
	if _, ok := entries["a/b/c"]; !ok || !pending["a/b/c"] {
		t.Fatalf("depth-boundary directory must be visible and pending: entries=%v pending=%v", entries, pending)
	}
	if _, ok := entries["a/b/c/leaf.txt"]; ok {
		t.Fatal("depth-boundary scan must not include descendants")
	}
}

func TestCompareScanMarksDepthBoundaryIncomplete(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	for _, root := range []string{left, right} {
		if err := os.MkdirAll(filepath.Join(root, "a", "b", "c"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "a", "b", "c", "leaf.txt"), []byte("same"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv, _, _, _ := newTestServer(t)
	result, err := srv.performCompareScan(context.Background(), compareScanReq{
		Left: compareSourceSpec{Kind: "local", Path: left}, Right: compareSourceSpec{Kind: "local", Path: right}, MaxDepth: 3,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.RelPath == "a/b/c" {
			if item.Status != "pending" || !item.Pending || !item.Incomplete || !result.Incomplete {
				t.Fatalf("incomplete directory was classified incorrectly: %+v result=%+v", item, result)
			}
			return
		}
	}
	t.Fatalf("missing boundary directory in result: %+v", result.Items)
}

func TestCompareWalkTruncatedOnlyWhenThereAreMoreEntries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		count     int
		truncated bool
	}{
		{name: "exact limit", count: compareMaxFiles, truncated: false},
		{name: "one over", count: compareMaxFiles + 1, truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := &syntheticCompareFS{count: tc.count}
			entries, truncated, _, err := walkCompareFSWithMeta(context.Background(), fsys, "/root", compareScanReq{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if truncated != tc.truncated {
				t.Fatalf("truncated=%v want=%v", truncated, tc.truncated)
			}
			if len(entries) != compareMaxFiles {
				t.Fatalf("entries=%d want=%d", len(entries), compareMaxFiles)
			}
		})
	}
}

func TestCompareWalkDoesNotTreatPermissionErrorAsEmpty(t *testing.T) {
	want := errors.New("permission denied")
	fsys := &syntheticCompareFS{statErr: want}
	_, _, _, err := walkCompareFSWithMetaOrEmpty(context.Background(), fsys, "/root", compareScanReq{}, nil)
	if !errors.Is(err, want) {
		t.Fatalf("permission error was swallowed: %v", err)
	}
	fsys.statErr = comparefs.ErrNotFound
	entries, truncated, pending, err := walkCompareFSWithMetaOrEmpty(context.Background(), fsys, "/root", compareScanReq{}, nil)
	if err != nil || truncated || len(entries) != 0 || len(pending) != 0 {
		t.Fatalf("only a confirmed missing root may be treated as empty: entries=%v truncated=%v pending=%v err=%v", entries, truncated, pending, err)
	}
}

func TestCompareDirectorySyncRejectsTargetTypeConflict(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(left, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(left, "conf", "app.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(right, "conf"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, _, _, _ := newTestServer(t)
	result, err := srv.performCompareSync(context.Background(), compareSyncReq{
		Left: compareSourceSpec{Kind: "local", Path: left}, Right: compareSourceSpec{Kind: "local", Path: right}, Direction: "right",
		Items: []compareSyncItem{{RelPath: "conf"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Copied != 0 || result.Failed != 1 || !strings.Contains(result.Failures[0].Error, "类型冲突") {
		t.Fatalf("type conflict was not reported clearly: %+v", result)
	}
	raw, readErr := os.ReadFile(filepath.Join(right, "conf"))
	if readErr != nil || string(raw) != "not a directory" {
		t.Fatalf("target changed despite type conflict: %q err=%v", raw, readErr)
	}
}

func TestCompareDirectoryChildUsesTargetSnapshotVersion(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.txt")
	targetPath := filepath.Join(root, "target.txt")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsys := comparefs.NewLocal()
	targetEntry, err := fsys.Stat(context.Background(), targetPath)
	if err != nil {
		t.Fatal(err)
	}
	expected := targetEntry.Version()
	if err := os.WriteFile(targetPath, []byte("changed after scan"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceEntry, err := fsys.Stat(context.Background(), sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	err = copyCompareEntry(context.Background(), fsys, fsys, sourcePath, targetPath, sourceEntry, &expected, false, false)
	if !errors.Is(err, comparefs.ErrConflict) {
		t.Fatalf("stale directory child should conflict, got %v", err)
	}
	raw, readErr := os.ReadFile(targetPath)
	if readErr != nil || string(raw) != "changed after scan" {
		t.Fatalf("stale child was overwritten: %q err=%v", raw, readErr)
	}
}

func TestCompareDirectoryChildExpectedMissingProtectsCreateRace(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.txt")
	targetPath := filepath.Join(root, "target.txt")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsys := comparefs.NewLocal()
	sourceEntry, err := fsys.Stat(context.Background(), sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("created after scan"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = copyCompareEntry(context.Background(), fsys, fsys, sourcePath, targetPath, sourceEntry, nil, true, false)
	if !errors.Is(err, comparefs.ErrConflict) {
		t.Fatalf("create-after-scan should conflict, got %v", err)
	}
	raw, readErr := os.ReadFile(targetPath)
	if readErr != nil || string(raw) != "created after scan" {
		t.Fatalf("create-after-scan target was overwritten: %q err=%v", raw, readErr)
	}
}

func TestDiffCompareRejectsTextOverLimit(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/diff/compare", map[string]any{
		"left": strings.Repeat("x", compareReadLimit+1), "right": "x",
	})
	if w.Code != 413 || !strings.Contains(w.Body.String(), "字节") {
		t.Fatalf("oversized diff should be clear 413: status=%d body=%s", w.Code, w.Body.String())
	}
	// NULs are encoded as six-byte \\u0000 escapes. This request is over the
	// old 18 MiB envelope while both decoded sides remain well below 8 MiB.
	escaped := strings.Repeat("\x00", 1600*1024)
	w = doRequest(srv, "POST", "/api/diff/compare", map[string]any{
		"left": escaped, "right": escaped,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("escaped but valid 8 MiB-per-side payload was rejected: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLocalAllowedRootsRejectSymlinkEscape(t *testing.T) {
	allowed, outside := t.TempDir(), t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(allowed, "link.txt")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	fsys := comparefs.NewLocalWithAllowedRoots([]string{allowed})
	if _, err := fsys.Stat(context.Background(), link); !errors.Is(err, comparefs.ErrPathOutsideRoot) {
		t.Fatalf("symlink escape was allowed: %v", err)
	}
	if _, err := fsys.Open(context.Background(), link); !errors.Is(err, comparefs.ErrPathOutsideRoot) {
		t.Fatalf("symlink escape was allowed for Open: %v", err)
	}
}

func TestLocalAllowedRootsMixedWildcardRemainsRestricted(t *testing.T) {
	allowed, outside := t.TempDir(), t.TempDir()
	outsideFile := filepath.Join(outside, "visible.txt")
	if err := os.WriteFile(outsideFile, []byte("visible"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, wildcard := range []string{"*", "ANY"} {
		fsys := comparefs.NewLocalWithAllowedRoots([]string{allowed, wildcard})
		if _, err := fsys.Stat(context.Background(), outsideFile); !errors.Is(err, comparefs.ErrPathOutsideRoot) {
			t.Fatalf("mixed wildcard %q must preserve restriction: %v", wildcard, err)
		}
	}
}

func TestLocalWriteAtomicRejectsDirectoryTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "folder")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	err := comparefs.NewLocal().WriteAtomic(context.Background(), target, bytes.NewBufferString("file"), comparefs.WriteOptions{})
	if !errors.Is(err, comparefs.ErrTypeConflict) {
		t.Fatalf("expected file/directory conflict, got %v", err)
	}
}

func TestCompareCopyRejectsDirectoryTarget(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	left, right := t.TempDir(), t.TempDir()
	source := filepath.Join(left, "source.txt")
	target := filepath.Join(right, "target.txt")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	response := doRequest(srv, "POST", "/api/compare/copy", map[string]any{
		"source": map[string]any{"kind": "local", "path": source},
		"target": map[string]any{"kind": "local", "path": target},
	})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "目录") {
		t.Fatalf("copy must report a file/directory conflict: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCompareJobManagerKeepsParallelJobsIndependent(t *testing.T) {
	manager := newCompareJobManager()
	first, firstCtx, err := manager.tryCreate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, secondCtx, err := manager.tryCreate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstCtx.Done():
		t.Fatal("creating a new job cancelled the previous job")
	case <-time.After(50 * time.Millisecond):
	}
	if err := firstCtx.Err(); err != nil {
		t.Fatalf("first job context=%v want active context", err)
	}
	select {
	case <-secondCtx.Done():
		t.Fatal("new job was cancelled unexpectedly")
	default:
	}
	first.release()
	second.release()
}

type syntheticCompareFS struct {
	count   int
	statErr error
}

func (f *syntheticCompareFS) Kind() string                  { return "synthetic" }
func (f *syntheticCompareFS) DisplayName() string           { return "synthetic" }
func (f *syntheticCompareFS) Clean(name string) string      { return path.Clean(name) }
func (f *syntheticCompareFS) Join(base, name string) string { return path.Join(base, name) }
func (f *syntheticCompareFS) Stat(context.Context, string) (comparefs.Entry, error) {
	if f.statErr != nil {
		return comparefs.Entry{}, f.statErr
	}
	return comparefs.Entry{Name: "root", Path: "/root", IsDir: true}, nil
}
func (f *syntheticCompareFS) List(context.Context, string) ([]comparefs.Entry, error) {
	return f.entries(), nil
}
func (f *syntheticCompareFS) ListLimited(_ context.Context, _ string, max int) ([]comparefs.Entry, bool, error) {
	entries := f.entries()
	if max > 0 && len(entries) > max {
		return entries[:max], true, nil
	}
	return entries, false, nil
}
func (f *syntheticCompareFS) entries() []comparefs.Entry {
	entries := make([]comparefs.Entry, f.count)
	for i := range entries {
		entries[i] = comparefs.Entry{Name: fmt.Sprintf("f%05d", i), Path: "/root/f", Size: 1}
	}
	return entries
}
func (f *syntheticCompareFS) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("x")), nil
}
func (f *syntheticCompareFS) MkdirAll(context.Context, string, uint32) error { return nil }
func (f *syntheticCompareFS) WriteAtomic(context.Context, string, io.Reader, comparefs.WriteOptions) error {
	return nil
}
func (f *syntheticCompareFS) Close() error { return nil }

func TestT059_ShallowVsDeepComparison(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	leftFile := filepath.Join(left, "doc.bin")
	rightFile := filepath.Join(right, "doc.bin")

	// Same size (64 bytes), same mtime, but different contents
	dataA := bytes.Repeat([]byte("A"), 64)
	dataB := bytes.Repeat([]byte("B"), 64)

	if err := os.WriteFile(leftFile, dataA, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightFile, dataB, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(leftFile, now, now)
	_ = os.Chtimes(rightFile, now, now)

	srv, _, _, _ := newTestServer(t)

	// 1. Shallow comparison (Deep: false)
	shallowRes, err := srv.performCompareScan(context.Background(), compareScanReq{
		Left:  compareSourceSpec{Kind: "local", Path: left},
		Right: compareSourceSpec{Kind: "local", Path: right},
		Deep:  false,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(shallowRes.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(shallowRes.Items))
	}
	item := shallowRes.Items[0]
	if item.Status != "same" || item.Hashed {
		t.Fatalf("shallow comparison must report same by metadata only: status=%s hashed=%v", item.Status, item.Hashed)
	}

	// 2. Deep comparison (Deep: true)
	deepRes, err := srv.performCompareScan(context.Background(), compareScanReq{
		Left:  compareSourceSpec{Kind: "local", Path: left},
		Right: compareSourceSpec{Kind: "local", Path: right},
		Deep:  true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(deepRes.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(deepRes.Items))
	}
	itemDeep := deepRes.Items[0]
	if itemDeep.Status == "same" || !itemDeep.Hashed {
		t.Fatalf("deep comparison must detect content mismatch: status=%s hashed=%v", itemDeep.Status, itemDeep.Hashed)
	}
}

func TestT060_DirectoryTruncationAndTargetConflict(t *testing.T) {
	srv, _, _, _ := newTestServer(t)

	// Sub-case 1: Target changed after preview (conflict detection)
	left, right := t.TempDir(), t.TempDir()
	sourceFile := filepath.Join(left, "file.txt")
	targetFile := filepath.Join(right, "file.txt")

	_ = os.WriteFile(sourceFile, []byte("new source data"), 0o644)
	_ = os.WriteFile(targetFile, []byte("initial target"), 0o644)

	// Take snapshot of target before modification
	fsTarget := comparefs.NewLocal()
	entry, err := fsTarget.Stat(context.Background(), targetFile)
	if err != nil {
		t.Fatal(err)
	}
	expectedVer := entry.Version()

	// Modify target externally after preview snapshot
	_ = os.WriteFile(targetFile, []byte("externally modified target!"), 0o644)

	// Sync with stale expected version
	syncRes, err := srv.performCompareSync(context.Background(), compareSyncReq{
		Left:      compareSourceSpec{Kind: "local", Path: left},
		Right:     compareSourceSpec{Kind: "local", Path: right},
		Direction: "right",
		Items: []compareSyncItem{
			{RelPath: "file.txt", Expected: &expectedVer},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if syncRes.Copied != 0 || syncRes.Conflicts != 1 || len(syncRes.Failures) != 1 {
		t.Fatalf("expected conflict failure without copy: %+v", syncRes)
	}
	if !strings.Contains(syncRes.Failures[0].Error, "目标已变化") {
		t.Fatalf("expected conflict error message, got: %s", syncRes.Failures[0].Error)
	}

	// Verify target was NOT blindly overwritten
	targetContent, _ := os.ReadFile(targetFile)
	if string(targetContent) != "externally modified target!" {
		t.Fatalf("target was overwritten despite conflict: %s", targetContent)
	}

	// Sub-case 2: Truncated directory scan refuses partial synchronization
	truncatedRes, err := srv.performCompareSync(context.Background(), compareSyncReq{
		Left:      compareSourceSpec{Kind: "synthetic", Path: "/root"},
		Right:     compareSourceSpec{Kind: "local", Path: right},
		Direction: "right",
		Items: []compareSyncItem{
			{RelPath: "subfolder"},
		},
	}, nil)
	// Verify that unsupported source kinds or failed scans report error and do not touch target
	if err == nil && (truncatedRes == nil || len(truncatedRes.Failures) == 0) {
		t.Fatalf("expected error for unsupported synthetic source kind, got none")
	}
	if _, statErr := os.Stat(filepath.Join(right, "subfolder")); !os.IsNotExist(statErr) {
		t.Fatalf("target folder should not be modified on failed sync: %v", statErr)
	}
}

func TestT061_MultiItemSyncPartialCancellation(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	left, right := t.TempDir(), t.TempDir()

	// Create 5 source files
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("file%d.txt", i)
		_ = os.WriteFile(filepath.Join(left, name), []byte(fmt.Sprintf("content %d", i)), 0o644)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	items := []compareSyncItem{
		{RelPath: "file1.txt"},
		{RelPath: "file2.txt"},
		{RelPath: "file3.txt"},
		{RelPath: "file4.txt"},
		{RelPath: "file5.txt"},
	}

	// Create a job
	job, _, err := srv.compares.tryCreate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer job.release()

	// Cancel after 2 items are processed
	copiedCount := 0
	progressFn := func(current, total int, message string) {
		copiedCount++
		if copiedCount == 2 {
			cancel()
		}
	}

	res, syncErr := srv.performCompareSync(ctx, compareSyncReq{
		Left:      compareSourceSpec{Kind: "local", Path: left},
		Right:     compareSourceSpec{Kind: "local", Path: right},
		Direction: "right",
		Items:     items,
	}, progressFn)

	if !errors.Is(syncErr, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", syncErr)
	}
	if res == nil {
		t.Fatal("expected non-nil sync result on cancellation")
	}

	// Verify that copied + cancelled equals total items
	if res.Copied < 1 || res.Cancelled < 1 || res.Copied+res.Cancelled != 5 {
		t.Fatalf("expected breakdown of copied and cancelled items: copied=%d cancelled=%d items=%+v", res.Copied, res.Cancelled, res.Items)
	}

	// Verify runCompareSync attaches SyncResult even on cancel
	srv.runCompareSync(ctx, job, compareSyncReq{
		Left:      compareSourceSpec{Kind: "local", Path: left},
		Right:     compareSourceSpec{Kind: "local", Path: right},
		Direction: "right",
		Items:     items,
	})

	view := job.view()
	if view.Status != "cancelled" {
		t.Fatalf("expected job status cancelled, got %s", view.Status)
	}
	if view.SyncResult == nil {
		t.Fatalf("expected SyncResult to be preserved on cancelled job, got view=%+v", view)
	}
	if view.SyncResult.Copied == 0 && view.SyncResult.Cancelled == 0 {
		t.Fatalf("expected preserved SyncResult with outcomes: %+v", view.SyncResult)
	}
}

