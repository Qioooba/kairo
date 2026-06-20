package httpserver

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZipFiles_Basic(t *testing.T) {
	dir := t.TempDir()
	srcs := []string{
		filepath.Join(dir, "a.log"),
		filepath.Join(dir, "b.log"),
		filepath.Join(dir, "c.log"),
	}
	for i, p := range srcs {
		if err := os.WriteFile(p, []byte("hello "+string(rune('a'+i))), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(dir, "out.zip")
	if err := zipFiles(srcs, dest); err != nil {
		t.Fatalf("zipFiles: %v", err)
	}
	// 验证是合法 zip + 三个文件 + 内容对得上
	r, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer r.Close()
	if got := len(r.File); got != 3 {
		t.Fatalf("期望 3 个 entry, 实际 %d", got)
	}
	want := map[string]string{"a.log": "hello a", "b.log": "hello b", "c.log": "hello c"}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if string(b) != want[f.Name] {
			t.Fatalf("entry %s 内容错: 期望 %q, 实际 %q", f.Name, want[f.Name], string(b))
		}
	}
}

func TestZipFiles_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "..", "evil.log")
	if err := zipFiles([]string{bad}, filepath.Join(dir, "out.zip")); err == nil {
		t.Fatal("期望拒绝路径穿越")
	}
}

func TestZipFiles_Empty(t *testing.T) {
	dir := t.TempDir()
	if err := zipFiles(nil, filepath.Join(dir, "out.zip")); err == nil {
		t.Fatal("空文件列表应报错")
	}
}

func TestZipFiles_RejectBackslash(t *testing.T) {
	dir := t.TempDir()
	bad := dir + `\evil.log`
	if err := zipFiles([]string{bad}, filepath.Join(dir, "out.zip")); err == nil {
		t.Fatal("含反斜杠的路径应被拒")
	}
}

func TestZipFiles_NamesClean(t *testing.T) {
	// 验证 zip 内文件名不带 downloads/.../ 前缀
	dir := t.TempDir()
	src := filepath.Join(dir, "real.log")
	if err := os.WriteFile(src, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out.zip")
	if err := zipFiles([]string{src}, dest); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.File[0].Name != "real.log" {
		t.Fatalf("entry 名应只是 basename, 实际: %s", r.File[0].Name)
	}
	if strings.Contains(r.File[0].Name, "/") || strings.Contains(r.File[0].Name, `\`) {
		t.Fatalf("entry 不应含目录分隔符: %s", r.File[0].Name)
	}
}