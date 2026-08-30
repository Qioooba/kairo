package httpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kairo/internal/textcodec"
)

func TestDiffCompareIgnoreBlankRemovesBlankLines(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/diff/compare", map[string]any{
		"left":   "alpha\n\n  \nbeta",
		"right":  "alpha\nbeta",
		"ignore": map[string]any{"ignore_blank": true, "trim_space": true},
	})
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got diffCompareResp
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Stats.Added != 0 || got.Stats.Removed != 0 || got.Stats.Common != 2 {
		t.Fatalf("blank lines should be ignored: %+v", got.Stats)
	}
	if got.Lines[1].LeftNo != 4 || got.Lines[1].RightNo != 2 {
		t.Fatalf("real line numbers should survive filtering: %+v", got.Lines[1])
	}
}

func TestDiffCompareIgnoreRulesPreserveOriginalText(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/diff/compare", map[string]any{
		"left": "Foo  \nvalue", "right": "foo\nVALUE",
		"ignore": map[string]any{"trim_space": true, "ignore_case": true},
	})
	var got diffCompareResp
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Stats.Common != 2 || got.Lines[0].Text != "Foo  " {
		t.Fatalf("matching must use normalized keys while output preserves original: %+v", got)
	}
}

func TestCompareWorkbenchLocalScanAndCopy(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	left := t.TempDir()
	right := t.TempDir()
	if err := os.WriteFile(filepath.Join(left, "same.txt"), []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(right, "same.txt"), []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(left, "only-left.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	start := doRequest(srv, "POST", "/api/compare/scan", map[string]any{
		"left":  map[string]any{"kind": "local", "path": left},
		"right": map[string]any{"kind": "local", "path": right},
	})
	if start.Code != 202 {
		t.Fatalf("start status=%d body=%s", start.Code, start.Body.String())
	}
	var started struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(start.Body.Bytes(), &started)
	deadline := time.Now().Add(2 * time.Second)
	var view compareJobView
	for time.Now().Before(deadline) {
		w := doRequest(srv, "GET", "/api/compare/jobs/"+started.JobID, nil)
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		if view.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if view.Status != "completed" || view.Result == nil || view.Result.Summary.LeftOnly != 1 {
		t.Fatalf("unexpected scan result: %+v", view)
	}

	target := filepath.Join(right, "only-left.txt")
	copyResp := doRequest(srv, "POST", "/api/compare/copy", map[string]any{
		"source": map[string]any{"kind": "local", "path": filepath.Join(left, "only-left.txt")},
		"target": map[string]any{"kind": "local", "path": target},
		"backup": true,
	})
	if copyResp.Code != 200 {
		t.Fatalf("copy status=%d body=%s", copyResp.Code, copyResp.Body.String())
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "payload" {
		t.Fatalf("copied content=%q err=%v", raw, err)
	}
}

func TestCompareWorkbenchBatchSyncCreatesNestedDirectories(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	left := t.TempDir()
	right := t.TempDir()
	source := filepath.Join(left, "conf", "app", "settings.ini")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("mode=production\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	response := doRequest(srv, "POST", "/api/compare/sync", map[string]any{
		"left":      map[string]any{"kind": "local", "path": left},
		"right":     map[string]any{"kind": "local", "path": right},
		"direction": "right",
		"items":     []map[string]any{{"rel_path": "conf/app/settings.ini"}},
		"backup":    true,
	})
	if response.Code != 200 {
		t.Fatalf("sync status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Copied int `json:"copied"`
		Failed int `json:"failed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Copied != 1 || result.Failed != 0 {
		t.Fatalf("unexpected sync result: %+v", result)
	}
	raw, err := os.ReadFile(filepath.Join(right, "conf", "app", "settings.ini"))
	if err != nil || string(raw) != "mode=production\n" {
		t.Fatalf("synced content=%q err=%v", raw, err)
	}
}

func TestCompareWorkbenchWriteRejectsStaleVersion(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	target := filepath.Join(t.TempDir(), "config.txt")
	if err := os.WriteFile(target, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := doRequest(srv, "POST", "/api/compare/write", map[string]any{
		"target":  map[string]any{"kind": "local", "path": target},
		"content": "replacement",
		"expected": map[string]any{
			"size": 999,
		},
		"backup": true,
	})
	if response.Code != 409 {
		t.Fatalf("expected conflict, status=%d body=%s", response.Code, response.Body.String())
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "current" {
		t.Fatalf("stale write changed target: %q err=%v", raw, err)
	}
}

func TestCompareWorkbenchPreservesGB18030AndCRLF(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	target := filepath.Join(t.TempDir(), "应用配置.ini")
	raw, err := textcodec.Encode("环境=生产\n端口=8080\n", textcodec.Info{Encoding: "gb18030", EOL: "crlf"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	readResponse := doRequest(srv, "POST", "/api/compare/read", map[string]any{"source": map[string]any{"kind": "local", "path": target, "encoding": "auto"}})
	if readResponse.Code != 200 {
		t.Fatalf("read status=%d body=%s", readResponse.Code, readResponse.Body.String())
	}
	var read compareReadResp
	if err := json.Unmarshal(readResponse.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if read.Text != "环境=生产\n端口=8080\n" || read.Encoding != "gb18030" || read.EOL != "crlf" || read.Binary {
		t.Fatalf("unexpected decoded response: %+v", read)
	}
	writeResponse := doRequest(srv, "POST", "/api/compare/write", map[string]any{
		"target": map[string]any{"kind": "local", "path": target}, "content": "环境=生产\n端口=9090\n", "expected": read.Version,
		"encoding": read.Encoding, "eol": read.EOL, "bom": read.BOM, "backup": true,
	})
	if writeResponse.Code != 200 {
		t.Fatalf("write status=%d body=%s", writeResponse.Code, writeResponse.Body.String())
	}
	saved, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	decoded, info, binary, err := textcodec.Decode(saved, "auto")
	if err != nil || binary || decoded != "环境=生产\n端口=9090\n" || info.Encoding != "gb18030" || info.EOL != "crlf" {
		t.Fatalf("saved text=%q info=%+v binary=%v err=%v", decoded, info, binary, err)
	}
}

func TestCompareWorkbenchMarksNewerSideWithTolerance(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	left := t.TempDir()
	right := t.TempDir()
	leftFile, rightFile := filepath.Join(left, "app.conf"), filepath.Join(right, "app.conf")
	if err := os.WriteFile(leftFile, []byte("left"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightFile, []byte("rght"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	if err := os.Chtimes(rightFile, base, base); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(leftFile, base.Add(10*time.Second), base.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	start := doRequest(srv, "POST", "/api/compare/scan", map[string]any{
		"left": map[string]any{"kind": "local", "path": left}, "right": map[string]any{"kind": "local", "path": right}, "time_tolerance_seconds": 2,
	})
	var started struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(start.Body.Bytes(), &started)
	var view compareJobView
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		response := doRequest(srv, "GET", "/api/compare/jobs/"+started.JobID, nil)
		_ = json.Unmarshal(response.Body.Bytes(), &view)
		if view.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if view.Result == nil || view.Result.Summary.LeftNewer != 1 || len(view.Result.Items) != 1 || view.Result.Items[0].Status != "left_newer" {
		t.Fatalf("unexpected newer-side result: %+v", view.Result)
	}
}

func TestCompareWorkbenchBackgroundSync(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	left, right := t.TempDir(), t.TempDir()
	leftFile := filepath.Join(left, "one.txt")
	if err := os.WriteFile(leftFile, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(leftFile, wantTime, wantTime); err != nil {
		t.Fatal(err)
	}
	start := doRequest(srv, "POST", "/api/compare/sync/start", map[string]any{
		"left": map[string]any{"kind": "local", "path": left}, "right": map[string]any{"kind": "local", "path": right},
		"direction": "right", "items": []map[string]any{{"rel_path": "one.txt"}}, "backup": true,
	})
	if start.Code != 202 {
		t.Fatalf("start status=%d body=%s", start.Code, start.Body.String())
	}
	var started struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(start.Body.Bytes(), &started)
	var view compareJobView
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		response := doRequest(srv, "GET", "/api/compare/jobs/"+started.JobID, nil)
		_ = json.Unmarshal(response.Body.Bytes(), &view)
		if view.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if view.SyncResult == nil || view.SyncResult.Copied != 1 || view.SyncResult.Failed != 0 {
		t.Fatalf("unexpected sync job: %+v", view)
	}
	if raw, err := os.ReadFile(filepath.Join(right, "one.txt")); err != nil || string(raw) != "one" {
		t.Fatalf("synced content=%q err=%v", raw, err)
	}
	targetInfo, err := os.Stat(filepath.Join(right, "one.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !targetInfo.ModTime().Equal(wantTime) {
		t.Fatalf("synced mtime=%s want=%s", targetInfo.ModTime(), wantTime)
	}
}

func TestIgnoreCompareInternalArtifacts(t *testing.T) {
	for _, name := range []string{"app.conf.kairo-backup-123", "app.conf.kairo-abc.partial"} {
		if !ignoreCompareEntry(name, false, nil, nil) {
			t.Fatalf("internal artifact should be ignored: %s", name)
		}
	}
	if ignoreCompareEntry("app.conf", false, nil, nil) {
		t.Fatal("ordinary file must remain visible")
	}
}
