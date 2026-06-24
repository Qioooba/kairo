package downloads

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTestDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	return d
}

func TestWriteAndReadMeta(t *testing.T) {
	dir := setupTestDir(t)
	data := filepath.Join(dir, "test.log")
	if err := os.WriteFile(data, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := Meta{
		Server:   "node1",
		Host:     "10.0.0.1:22",
		Dir:      "/var/log",
		DirAlias: "logs",
		File:     "test.log",
		Encoding: "utf-8",
		Kind:     "file",
	}
	if err := WriteMeta(data, m); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	got, ok, err := ReadMeta(data)
	if err != nil || !ok {
		t.Fatalf("ReadMeta: ok=%v err=%v", ok, err)
	}
	if got.Server != "node1" || got.File != "test.log" || got.Kind != "file" {
		t.Errorf("Roundtrip mismatch: %+v", got)
	}
}

func TestReadMetaMissing(t *testing.T) {
	dir := setupTestDir(t)
	data := filepath.Join(dir, "nope.log")
	_, ok, err := ReadMeta(data)
	if ok || err != nil {
		t.Errorf("missing meta should return (zero,false,nil), got ok=%v err=%v", ok, err)
	}
}

func TestListOrderAndFilter(t *testing.T) {
	dir := setupTestDir(t)
	// 制造 3 个不同 mtime 的文件
	files := map[string]time.Time{
		"old.log":    time.Now().Add(-3 * time.Hour),
		"recent.log": time.Now().Add(-1 * time.Hour),
		"newer.log":  time.Now().Add(-10 * time.Minute),
	}
	for name, ts := range files {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte("x"), 0o600)
		_ = os.Chtimes(p, ts, ts)
	}
	// 加个 sidecar，但缺数据文件 — List 应该跳过
	_ = os.WriteFile(filepath.Join(dir, "orphan.log.meta"), []byte("{}"), 0o600)
	// 加个隐藏文件
	_ = os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o600)
	// 加个非 .log/.zip 后缀
	_ = os.WriteFile(filepath.Join(dir, "weird.tmp"), []byte("x"), 0o600)

	got, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 entries, got %d: %+v", len(got), got)
	}
	// 验证顺序：newer, recent, old
	want := []string{"newer.log", "recent.log", "old.log"}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name, w)
		}
	}
}

func TestListRecurseSubdir(t *testing.T) {
	dir := setupTestDir(t)
	sub := filepath.Join(dir, "20260621")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a.log"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if !strings.Contains(got[0].Name, "20260621") {
		t.Errorf("name should include subdir, got %q", got[0].Name)
	}
}

func TestListMissingRoot(t *testing.T) {
	got, err := List("/nonexistent/root/for/test")
	if err != nil {
		t.Errorf("missing root should return nil err, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil entries, got %d", len(got))
	}
}

func TestDeleteRemovesSidecar(t *testing.T) {
	dir := setupTestDir(t)
	data := filepath.Join(dir, "x.log")
	_ = os.WriteFile(data, []byte("y"), 0o600)
	if err := WriteMeta(data, Meta{Server: "s", Kind: "file", File: "x.log"}); err != nil {
		t.Fatal(err)
	}
	if err := Delete(dir, "x.log"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Errorf("data file should be gone")
	}
	// 项 4 修复：元数据从单文件索引读，Delete 后 ReadMeta 应该拿不到
	if _, ok, _ := ReadMeta(data); ok {
		t.Errorf("index should not contain deleted file")
	}
}

func TestDeleteAllCounts(t *testing.T) {
	dir := setupTestDir(t)
	for _, n := range []string{"a.log", "b.log", "c.zip"} {
		p := filepath.Join(dir, n)
		_ = os.WriteFile(p, []byte("x"), 0o600)
		_ = WriteMeta(p, Meta{Server: "s", Kind: "file"})
	}
	n, err := DeleteAll(dir)
	if err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 deleted, got %d", n)
	}
	// 再 List 应该是空的
	got, _ := List(dir)
	if len(got) != 0 {
		t.Errorf("after delete, expected 0, got %d", len(got))
	}
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	dir := setupTestDir(t)
	cases := []string{"../etc/passwd", "..", "/etc/passwd", `..\windows`}
	for _, name := range cases {
		_, err := safeJoin(dir, name)
		if err == nil {
			t.Errorf("safeJoin(%q) should fail", name)
		}
	}
}

// TestList_DateDirFallback 验证：downloads/YYYYMMDD/ 里的文件即使没 sidecar，
// 也能在 List 里出现，且 DownloadedAt 用子目录名推断（项 16）。
func TestList_DateDirFallback(t *testing.T) {
	dir := setupTestDir(t)
	sub := filepath.Join(dir, "20260621")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// 在子目录里放一个无 sidecar 的 .log 文件（模拟手工 cp 进来的）
	data := filepath.Join(sub, "manual.log")
	if err := os.WriteFile(data, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("期望 1 条，实际 %d", len(got))
	}
	e := got[0]
	if e.MetaPresent {
		t.Errorf("无 sidecar，MetaPresent 应为 false")
	}
	if e.Meta.DownloadedAt.IsZero() {
		t.Errorf("DownloadedAt 应被推断为 2026-06-21，实际零值")
	} else {
		y, m, d := e.Meta.DownloadedAt.Date()
		if y != 2026 || m != 6 || d != 21 {
			t.Errorf("日期不对: %v", e.Meta.DownloadedAt)
		}
	}
}

// TestList_DateDirFallback_DashFormat 验证 YYYY-MM-DD 形式的子目录名也能识别。
func TestList_DateDirFallback_DashFormat(t *testing.T) {
	dir := setupTestDir(t)
	sub := filepath.Join(dir, "2026-06-22")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(sub, "manual.zip")
	if err := os.WriteFile(data, []byte("PK"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("期望 1 条，实际 %d", len(got))
	}
	if got[0].Kind != "zip" {
		t.Errorf("Kind 应为 zip，实际 %s", got[0].Kind)
	}
	if got[0].Meta.DownloadedAt.IsZero() {
		t.Error("日期应被推断")
	}
}
