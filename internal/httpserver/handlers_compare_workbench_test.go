package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
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
