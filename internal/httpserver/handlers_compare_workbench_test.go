package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kairo/internal/comparefs"
)

func TestCompareFileBytesEarlyExit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	leftSame := filepath.Join(dir, "left-same.bin")
	rightSame := filepath.Join(dir, "right-same.bin")
	leftDiff := filepath.Join(dir, "left-diff.bin")
	rightDiff := filepath.Join(dir, "right-diff.bin")
	payload := bytes.Repeat([]byte("kairo-compare-payload-"), 4096)
	if err := os.WriteFile(leftSame, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightSame, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	leftPayload := append([]byte{'A'}, bytes.Repeat([]byte{'x'}, 2<<20)...)
	rightPayload := append([]byte{'B'}, bytes.Repeat([]byte{'x'}, 2<<20)...)
	if err := os.WriteFile(leftDiff, leftPayload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightDiff, rightPayload, 0o644); err != nil {
		t.Fatal(err)
	}
	fsys := comparefs.NewLocal()
	same, err := compareFileBytes(context.Background(), fsys, fsys, leftSame, rightSame)
	if err != nil || !same {
		t.Fatalf("identical files: same=%v err=%v", same, err)
	}
	started := time.Now()
	same, err = compareFileBytes(context.Background(), fsys, fsys, leftDiff, rightDiff)
	elapsed := time.Since(started)
	if err != nil || same {
		t.Fatalf("different files: same=%v err=%v", same, err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("early-exit content compare took too long: %s", elapsed)
	}
}

type countingReadSeeker struct {
	f *os.File
	n *int64
}

func (c *countingReadSeeker) Read(p []byte) (int, error) {
	got, err := c.f.Read(p)
	*c.n += int64(got)
	return got, err
}
func (c *countingReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return c.f.Seek(offset, whence)
}
func (c *countingReadSeeker) Close() error { return c.f.Close() }

type noSeekReader struct{ io.ReadCloser }

func openCounted(t *testing.T, path string, n *int64) *countingReadSeeker {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return &countingReadSeeker{f: f, n: n}
}

func writeCompareBlob(t *testing.T, path string, size int, mutate func([]byte)) {
	t.Helper()
	buf := bytes.Repeat([]byte{'A'}, size)
	if mutate != nil {
		mutate(buf)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompareSampledHeadAndTailSkipMiddle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const size = 1 << 20
	leftPath := filepath.Join(dir, "left.bin")
	rightHead := filepath.Join(dir, "right-head.bin")
	rightTail := filepath.Join(dir, "right-tail.bin")
	rightMid := filepath.Join(dir, "right-mid.bin")
	writeCompareBlob(t, leftPath, size, nil)
	writeCompareBlob(t, rightHead, size, func(b []byte) { b[0] = 'B' })
	writeCompareBlob(t, rightTail, size, func(b []byte) { b[len(b)-1] = 'B' })
	writeCompareBlob(t, rightMid, size, func(b []byte) { b[size/2] = 'B' })

	assertDiff := func(t *testing.T, right string, maxRead int64) {
		t.Helper()
		var nL, nR int64
		left := openCounted(t, leftPath, &nL)
		defer left.Close()
		other := openCounted(t, right, &nR)
		defer other.Close()
		same, err := compareOpenReaders(context.Background(), left, other)
		if err != nil || same {
			t.Fatalf("path=%s same=%v err=%v", right, same, err)
		}
		if nL > maxRead || nR > maxRead {
			t.Fatalf("expected sampled I/O <= %d, got L=%d R=%d", maxRead, nL, nR)
		}
	}

	t.Run("head", func(t *testing.T) {
		assertDiff(t, rightHead, 16*1024)
	})
	t.Run("tail", func(t *testing.T) {
		assertDiff(t, rightTail, 16*1024)
	})
	t.Run("middle still detected", func(t *testing.T) {
		var nL, nR int64
		left := openCounted(t, leftPath, &nL)
		defer left.Close()
		other := openCounted(t, rightMid, &nR)
		defer other.Close()
		same, err := compareOpenReaders(context.Background(), left, other)
		if err != nil || same {
			t.Fatalf("middle diff missed: same=%v err=%v", same, err)
		}
		if nL < int64(size/2) && nR < int64(size/2) {
			t.Fatalf("middle compare returned too early: L=%d R=%d", nL, nR)
		}
	})
}

func TestCompareSampledIdenticalAndSmallFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	large := filepath.Join(dir, "large.bin")
	smallL := filepath.Join(dir, "small-l.bin")
	smallR := filepath.Join(dir, "small-r.bin")
	writeCompareBlob(t, large, 64*1024, nil)
	writeCompareBlob(t, smallL, 1024, nil)
	writeCompareBlob(t, smallR, 1024, func(b []byte) { b[10] = 'Z' })

	var nL, nR int64
	left := openCounted(t, large, &nL)
	defer left.Close()
	right := openCounted(t, large, &nR)
	defer right.Close()
	same, err := compareOpenReaders(context.Background(), left, right)
	if err != nil || !same {
		t.Fatalf("identical large: same=%v err=%v", same, err)
	}
	if nL < 64*1024 || nR < 64*1024 {
		t.Fatalf("identical files must still read the whole payload, L=%d R=%d", nL, nR)
	}

	fsys := comparefs.NewLocal()
	same, err = compareFileBytes(context.Background(), fsys, fsys, smallL, smallR)
	if err != nil || same {
		t.Fatalf("small files: same=%v err=%v", same, err)
	}
}

func TestCompareNonSeekableStillCorrect(t *testing.T) {
	t.Parallel()
	payload := bytes.Repeat([]byte{'A'}, 48*1024)
	right := append([]byte{}, payload...)
	right[len(right)-1] = 'B'
	same, err := compareOpenReaders(context.Background(), noSeekReader{io.NopCloser(bytes.NewReader(payload))}, noSeekReader{io.NopCloser(bytes.NewReader(right))})
	if err != nil || same {
		t.Fatalf("non-seekable tail diff: same=%v err=%v", same, err)
	}
}

func TestCompareTestAndDeepScan(t *testing.T) {
	leftDir := t.TempDir()
	rightDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(leftDir, "same.txt"), []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rightDir, "same.txt"), []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftDir, "only-left.txt"), []byte("left"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rightDir, "only-right.txt"), []byte("right"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftDir, "changed.txt"), []byte("left-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rightDir, "changed.txt"), []byte("right-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv, _, _, _ := newTestServer(t)
	testResp := doRequest(srv, "POST", "/api/compare/test", map[string]any{
		"left":  map[string]any{"kind": "local", "path": leftDir},
		"right": map[string]any{"kind": "local", "path": rightDir},
	})
	if testResp.Code != 200 {
		t.Fatalf("test status=%d body=%s", testResp.Code, testResp.Body.String())
	}
	var testBody struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(testResp.Body.Bytes(), &testBody); err != nil {
		t.Fatal(err)
	}
	if !testBody.OK {
		t.Fatalf("expected both sides ok: %s", testResp.Body.String())
	}

	start := doRequest(srv, "POST", "/api/compare/scan", map[string]any{
		"left":  map[string]any{"kind": "local", "path": leftDir},
		"right": map[string]any{"kind": "local", "path": rightDir},
		"deep":  true,
	})
	if start.Code != 202 {
		t.Fatalf("scan start status=%d body=%s", start.Code, start.Body.String())
	}
	var started struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		job := doRequest(srv, "GET", "/api/compare/jobs/"+started.JobID, nil)
		if job.Code != 200 {
			t.Fatalf("job status=%d body=%s", job.Code, job.Body.String())
		}
		var view struct {
			Status string `json:"status"`
			Error  string `json:"error"`
			Result *struct {
				Summary compareScanSummary `json:"summary"`
			} `json:"result"`
		}
		if err := json.Unmarshal(job.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		if view.Status == "failed" || view.Status == "cancelled" {
			t.Fatalf("scan failed: %s", view.Error)
		}
		if view.Status == "completed" {
			if view.Result == nil {
				t.Fatal("missing result")
			}
			if view.Result.Summary.Same < 1 || view.Result.Summary.LeftOnly != 1 || view.Result.Summary.RightOnly != 1 {
				t.Fatalf("unexpected summary: %+v body=%s", view.Result.Summary, job.Body.String())
			}
			if view.Result.Summary.LeftNewer+view.Result.Summary.RightNewer+view.Result.Summary.Different < 1 {
				t.Fatalf("changed file not classified: %+v", view.Result.Summary)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan did not complete: %s", job.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCompareWalkShouldEnqueue(t *testing.T) {
	t.Parallel()
	if !compareWalkShouldEnqueue(0, 0) {
		t.Fatal("max_depth 0 means unlimited")
	}
	if compareWalkShouldEnqueue(0, 1) {
		t.Fatal("max_depth 1 should stay in the current directory")
	}
	if !compareWalkShouldEnqueue(0, 2) || compareWalkShouldEnqueue(1, 2) {
		t.Fatal("max_depth 2 should list one child level only")
	}
}

func TestWalkCompareFSRespectsMaxDepth(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	deep := filepath.Join(sub, "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "child.txt"), []byte("child"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "leaf.txt"), []byte("leaf"), 0o644); err != nil {
		t.Fatal(err)
	}
	fsys := comparefs.NewLocal()
	ctx := context.Background()
	shallow, _, err := walkCompareFS(ctx, fsys, dir, compareScanReq{MaxDepth: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := shallow["root.txt"]; !ok {
		t.Fatalf("current dir file missing: %#v", shallow)
	}
	if _, ok := shallow["sub"]; !ok {
		t.Fatalf("current dir folder missing: %#v", shallow)
	}
	if _, ok := shallow["sub/child.txt"]; ok {
		t.Fatalf("max_depth 1 must not enter sub: %#v", shallow)
	}
	mid, _, err := walkCompareFS(ctx, fsys, dir, compareScanReq{MaxDepth: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mid["sub/child.txt"]; !ok {
		t.Fatalf("max_depth 2 should include child: %#v", mid)
	}
	if _, ok := mid["sub/deep/leaf.txt"]; ok {
		t.Fatalf("max_depth 2 must not reach leaf: %#v", mid)
	}
	all, _, err := walkCompareFS(ctx, fsys, dir, compareScanReq{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := all["sub/deep/leaf.txt"]; !ok {
		t.Fatalf("unlimited scan should include leaf: %#v", all)
	}
}

func TestCompareTestRejectsEmptyPath(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/compare/test", map[string]any{
		"left":  map[string]any{"kind": "local", "path": ""},
		"right": map[string]any{"kind": "local", "path": ""},
	})
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"ok":false`) && !strings.Contains(w.Body.String(), `"ok": false`) {
		t.Fatalf("expected failed test: %s", w.Body.String())
	}
}

func TestCleanCompareRel(t *testing.T) {
	t.Parallel()
	ok, err := cleanCompareRel(`sub\child`)
	if err != nil || ok != "sub/child" {
		t.Fatalf("slash normalize: %q %v", ok, err)
	}
	if _, err := cleanCompareRel("../etc"); err == nil {
		t.Fatal("expected reject traversal")
	}
	if _, err := cleanCompareRel(`..\windows`); err == nil {
		t.Fatal("expected reject backslash traversal")
	}
	if got, err := cleanCompareRel("foo/../bar"); err != nil || got != "bar" {
		t.Fatalf("in-tree parent: %q %v", got, err)
	}
}

func TestCompareScanLevelLazyHash(t *testing.T) {
	leftDir := t.TempDir()
	rightDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(leftDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rightDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	same := bytes.Repeat([]byte("S"), 64*1024)
	leftOnly := bytes.Repeat([]byte("L"), 64*1024)
	rightMid := append([]byte{}, same...)
	rightMid[len(rightMid)/2] = 'X'
	if err := os.WriteFile(filepath.Join(leftDir, "root.txt"), same, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rightDir, "root.txt"), rightMid, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftDir, "nested", "deep.txt"), leftOnly, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rightDir, "nested", "deep.txt"), leftOnly, 0o644); err != nil {
		t.Fatal(err)
	}

	srv, _, _, _ := newTestServer(t)
	root := doRequest(srv, "POST", "/api/compare/scan-level", map[string]any{
		"left":  map[string]any{"kind": "local", "path": leftDir},
		"right": map[string]any{"kind": "local", "path": rightDir},
		"deep":  true,
	})
	if root.Code != 200 {
		t.Fatalf("scan-level status=%d body=%s", root.Code, root.Body.String())
	}
	var level compareScanResult
	if err := json.Unmarshal(root.Body.Bytes(), &level); err != nil {
		t.Fatal(err)
	}
	var nested, rootFile *compareScanItem
	for i := range level.Items {
		item := &level.Items[i]
		if item.RelPath == "nested" {
			nested = item
		}
		if item.RelPath == "root.txt" {
			rootFile = item
		}
		if item.RelPath == "nested/deep.txt" {
			t.Fatal("root level must not hash nested files")
		}
	}
	if nested == nil || !nested.Pending {
		t.Fatalf("nested folder should be pending: %+v", nested)
	}
	if rootFile == nil || !rootFile.Hashed || rootFile.Status == "same" {
		t.Fatalf("root file should be hashed as different: %+v", rootFile)
	}

	child := doRequest(srv, "POST", "/api/compare/scan-level", map[string]any{
		"left":     map[string]any{"kind": "local", "path": leftDir},
		"right":    map[string]any{"kind": "local", "path": rightDir},
		"rel_path": "nested",
		"deep":     true,
	})
	if child.Code != 200 {
		t.Fatalf("nested scan-level status=%d body=%s", child.Code, child.Body.String())
	}
	var nestedLevel compareScanResult
	if err := json.Unmarshal(child.Body.Bytes(), &nestedLevel); err != nil {
		t.Fatal(err)
	}
	if len(nestedLevel.Items) != 1 || nestedLevel.Items[0].RelPath != "nested/deep.txt" || !nestedLevel.Items[0].Hashed {
		t.Fatalf("expand should hash only nested files: %+v", nestedLevel.Items)
	}
}

func TestCompareSyncExpandsDirectory(t *testing.T) {
	leftDir := t.TempDir()
	rightDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(leftDir, "only"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftDir, "only", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/compare/sync", map[string]any{
		"left":      map[string]any{"kind": "local", "path": leftDir},
		"right":     map[string]any{"kind": "local", "path": rightDir},
		"direction": "right",
		"items":     []map[string]any{{"rel_path": "only"}},
		"backup":    false,
	})
	if w.Code != 200 {
		t.Fatalf("sync status=%d body=%s", w.Code, w.Body.String())
	}
	copied := filepath.Join(rightDir, "only", "a.txt")
	body, err := os.ReadFile(copied)
	if err != nil || string(body) != "a" {
		t.Fatalf("directory expand copy failed: %v %q", err, body)
	}
}

func TestCompareThreeStageComprehensiveMatrix(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		size      int
		diffIdx   int // -1 for identical
		expectMax int64
	}{
		{"0 bytes identical", 0, -1, 0},
		{"1 byte identical", 1, -1, 1},
		{"1 byte diff", 1, 0, 1},
		{"4KB identical", 4096, -1, 4096},
		{"4KB diff at 0", 4096, 0, 4096},
		{"4KB diff at end", 4096, 4095, 4096},
		{"32KB identical", 32768, -1, 32768},
		{"32KB diff at 0", 32768, 0, 8192},
		{"32KB diff at 4095 (head)", 32768, 4095, 8192},
		{"32KB diff at 4096 (mid start)", 32768, 4096, 32768},
		{"32KB diff at 32768-4097 (mid end)", 32768, 32768 - 4097, 32768},
		{"32KB diff at 32768-4096 (tail start)", 32768, 32768 - 4096, 8192},
		{"32KB diff at 32767 (tail end)", 32768, 32767, 8192},
		{"256KB diff in middle", 256 * 1024, 128 * 1024, 256 * 1024},
		{"256KB diff at head", 256 * 1024, 100, 8192},
		{"256KB diff at tail", 256 * 1024, 256*1024 - 100, 8192},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			leftPath := filepath.Join(dir, "left.bin")
			rightPath := filepath.Join(dir, "right.bin")
			leftData := bytes.Repeat([]byte{'M'}, tc.size)
			rightData := bytes.Repeat([]byte{'M'}, tc.size)
			if tc.diffIdx >= 0 && tc.diffIdx < tc.size {
				rightData[tc.diffIdx] = 'N'
			}
			if err := os.WriteFile(leftPath, leftData, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rightPath, rightData, 0o644); err != nil {
				t.Fatal(err)
			}

			var nL, nR int64
			left := openCounted(t, leftPath, &nL)
			defer left.Close()
			right := openCounted(t, rightPath, &nR)
			defer right.Close()

			same, err := compareOpenReaders(context.Background(), left, right)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			expectedSame := (tc.diffIdx == -1)
			if same != expectedSame {
				t.Fatalf("expected same=%v, got=%v", expectedSame, same)
			}
			if tc.expectMax > 0 && (nL > tc.expectMax*2 || nR > tc.expectMax*2) {
				t.Fatalf("read too much I/O: nL=%d nR=%d max=%d", nL, nR, tc.expectMax)
			}
		})
	}
}

func TestCompareSyncDeduplicationAndSafety(t *testing.T) {
	leftDir := t.TempDir()
	rightDir := t.TempDir()

	// Nested dir: parent/child/deep.txt
	deepFile := filepath.Join(leftDir, "parent", "child", "deep.txt")
	if err := os.MkdirAll(filepath.Dir(deepFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deepFile, []byte("deep payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Root file
	rootFile := filepath.Join(leftDir, "root.txt")
	if err := os.WriteFile(rootFile, []byte("root payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Existing right file for backup test
	existingRight := filepath.Join(rightDir, "root.txt")
	if err := os.WriteFile(existingRight, []byte("old right content"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv, _, _, _ := newTestServer(t)

	// Test 1: Sync parent folder AND child file together in single batch
	// Deduplication must skip redundant child item and succeed cleanly
	w := doRequest(srv, "POST", "/api/compare/sync", map[string]any{
		"left":      map[string]any{"kind": "local", "path": leftDir},
		"right":     map[string]any{"kind": "local", "path": rightDir},
		"direction": "right",
		"items": []map[string]any{
			{"rel_path": "parent"},
			{"rel_path": "parent/child/deep.txt", "expected": map[string]any{"size": 0, "mtime": time.Now().UTC().Format(time.RFC3339)}},
			{"rel_path": "root.txt"},
		},
		"backup": true,
	})
	if w.Code != 200 {
		t.Fatalf("sync status=%d body=%s", w.Code, w.Body.String())
	}
	var res compareSyncResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Failed != 0 || len(res.Failures) != 0 {
		t.Fatalf("sync reported failures: %+v", res)
	}
	if res.Copied < 2 {
		t.Fatalf("expected at least 2 copied items, got %d", res.Copied)
	}

	// Verify deep.txt copied
	copiedDeep, err := os.ReadFile(filepath.Join(rightDir, "parent", "child", "deep.txt"))
	if err != nil || string(copiedDeep) != "deep payload" {
		t.Fatalf("copied deep file mismatch: %v %q", err, copiedDeep)
	}

	// Verify backup was created for root.txt on right
	entries, _ := os.ReadDir(rightDir)
	var backupFound bool
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "root.txt.kairo-backup-") {
			backupFound = true
			break
		}
	}
	if !backupFound {
		t.Fatalf("expected backup file for root.txt in %s", rightDir)
	}

	// Test 2: Path traversal attempt must be rejected
	badResp := doRequest(srv, "POST", "/api/compare/sync", map[string]any{
		"left":      map[string]any{"kind": "local", "path": leftDir},
		"right":     map[string]any{"kind": "local", "path": rightDir},
		"direction": "right",
		"items": []map[string]any{
			{"rel_path": "../outside.txt"},
			{"rel_path": `..\windows\system32`},
		},
		"backup": false,
	})
	if badResp.Code != 200 {
		t.Fatalf("expected 200 with failure list, got %d", badResp.Code)
	}
	var badRes compareSyncResult
	if err := json.Unmarshal(badResp.Body.Bytes(), &badRes); err != nil {
		t.Fatal(err)
	}
	if badRes.Failed != 2 {
		t.Fatalf("expected 2 traversal failures, got %d: %+v", badRes.Failed, badRes)
	}

	// Test 3: Sync from right to left
	rightNewFile := filepath.Join(rightDir, "from_right.txt")
	if err := os.WriteFile(rightNewFile, []byte("right to left"), 0o644); err != nil {
		t.Fatal(err)
	}
	rlResp := doRequest(srv, "POST", "/api/compare/sync", map[string]any{
		"left":      map[string]any{"kind": "local", "path": leftDir},
		"right":     map[string]any{"kind": "local", "path": rightDir},
		"direction": "left",
		"items": []map[string]any{
			{"rel_path": "from_right.txt"},
		},
		"backup": false,
	})
	if rlResp.Code != 200 {
		t.Fatalf("right-to-left sync status=%d body=%s", rlResp.Code, rlResp.Body.String())
	}
	leftCopied, err := os.ReadFile(filepath.Join(leftDir, "from_right.txt"))
	if err != nil || string(leftCopied) != "right to left" {
		t.Fatalf("right-to-left file mismatch: %v %q", err, leftCopied)
	}
}

func TestCompareScanLevelMultiLevelSubdirectories(t *testing.T) {
	leftDir := t.TempDir()
	rightDir := t.TempDir()

	// Structure:
	// a/b/c/leaf.txt
	// a/b/sibling.txt
	// a/other.txt
	// top.txt
	if err := os.MkdirAll(filepath.Join(leftDir, "a", "b", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rightDir, "a", "b", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftDir, "top.txt"), []byte("top"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rightDir, "top.txt"), []byte("top"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftDir, "a", "other.txt"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rightDir, "a", "other.txt"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftDir, "a", "b", "sibling.txt"), []byte("sib"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rightDir, "a", "b", "sibling.txt"), []byte("sib"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftDir, "a", "b", "c", "leaf.txt"), []byte("leaf-L"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rightDir, "a", "b", "c", "leaf.txt"), []byte("leaf-R"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv, _, _, _ := newTestServer(t)

	// Step 1: Scan root level
	res0 := doRequest(srv, "POST", "/api/compare/scan-level", map[string]any{
		"left":  map[string]any{"kind": "local", "path": leftDir},
		"right": map[string]any{"kind": "local", "path": rightDir},
		"deep":  true,
	})
	if res0.Code != 200 {
		t.Fatalf("scan level 0: %d %s", res0.Code, res0.Body.String())
	}
	var l0 compareScanResult
	if err := json.Unmarshal(res0.Body.Bytes(), &l0); err != nil {
		t.Fatal(err)
	}
	// Level 0 should have "a" (pending folder) and "top.txt" (same)
	var foundA bool
	for _, item := range l0.Items {
		if item.RelPath == "a" {
			foundA = true
			if !item.Pending {
				t.Fatalf("folder 'a' should be pending at root scan: %+v", item)
			}
		}
		if strings.HasPrefix(item.RelPath, "a/") {
			t.Fatalf("sub item leaked into level 0 scan: %s", item.RelPath)
		}
	}
	if !foundA {
		t.Fatalf("folder 'a' not found at level 0: %+v", l0.Items)
	}

	// Step 2: Expand 'a'
	resA := doRequest(srv, "POST", "/api/compare/scan-level", map[string]any{
		"left":     map[string]any{"kind": "local", "path": leftDir},
		"right":    map[string]any{"kind": "local", "path": rightDir},
		"rel_path": "a",
		"deep":     true,
	})
	if resA.Code != 200 {
		t.Fatalf("scan level 'a': %d %s", resA.Code, resA.Body.String())
	}
	var lA compareScanResult
	if err := json.Unmarshal(resA.Body.Bytes(), &lA); err != nil {
		t.Fatal(err)
	}
	// Should have "a/b" (pending) and "a/other.txt" (hashed/same)
	var foundAB, foundOther bool
	for _, item := range lA.Items {
		if item.RelPath == "a/b" {
			foundAB = true
			if !item.Pending {
				t.Fatalf("folder 'a/b' should be pending: %+v", item)
			}
		}
		if item.RelPath == "a/other.txt" {
			foundOther = true
			if item.Status != "same" {
				t.Fatalf("a/other.txt status=%s", item.Status)
			}
		}
	}
	if !foundAB || !foundOther {
		t.Fatalf("items missing in 'a': %+v", lA.Items)
	}

	// Step 3: Expand 'a/b'
	resAB := doRequest(srv, "POST", "/api/compare/scan-level", map[string]any{
		"left":     map[string]any{"kind": "local", "path": leftDir},
		"right":    map[string]any{"kind": "local", "path": rightDir},
		"rel_path": "a/b",
		"deep":     true,
	})
	if resAB.Code != 200 {
		t.Fatalf("scan level 'a/b': %d %s", resAB.Code, resAB.Body.String())
	}
	var lAB compareScanResult
	if err := json.Unmarshal(resAB.Body.Bytes(), &lAB); err != nil {
		t.Fatal(err)
	}
	// Should have "a/b/c" (pending) and "a/b/sibling.txt" (same)
	var foundABC bool
	for _, item := range lAB.Items {
		if item.RelPath == "a/b/c" {
			foundABC = true
		}
	}
	if !foundABC {
		t.Fatalf("folder 'a/b/c' missing: %+v", lAB.Items)
	}

	// Step 4: Expand 'a/b/c'
	resABC := doRequest(srv, "POST", "/api/compare/scan-level", map[string]any{
		"left":     map[string]any{"kind": "local", "path": leftDir},
		"right":    map[string]any{"kind": "local", "path": rightDir},
		"rel_path": "a/b/c",
		"deep":     true,
	})
	if resABC.Code != 200 {
		t.Fatalf("scan level 'a/b/c': %d %s", resABC.Code, resABC.Body.String())
	}
	var lABC compareScanResult
	if err := json.Unmarshal(resABC.Body.Bytes(), &lABC); err != nil {
		t.Fatal(err)
	}
	if len(lABC.Items) != 1 || lABC.Items[0].RelPath != "a/b/c/leaf.txt" {
		t.Fatalf("expected leaf.txt in a/b/c: %+v", lABC.Items)
	}
	if lABC.Items[0].Status != "different" && lABC.Items[0].Status != "left_newer" && lABC.Items[0].Status != "right_newer" {
		t.Fatalf("expected diff status for leaf: %+v", lABC.Items[0])
	}
}
