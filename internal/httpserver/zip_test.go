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

// TestZipFiles_SameBasenameAreDeduped 验证：同名文件不会在 zip 内互相覆盖，
// 而是加 _2 / _3 后缀（P1-9 修复）。
//
// 场景：用户在一次任务里同时下 /a/app.log 和 /b/app.log，
// 旧实现两者都叫 app.log，zip 内第二个会覆盖第一个；
// 新实现保留两份。
func TestZipFiles_SameBasenameAreDeduped(t *testing.T) {
	dir := t.TempDir()
	src1 := filepath.Join(dir, "sub1", "app.log")
	src2 := filepath.Join(dir, "sub2", "app.log")
	if err := os.MkdirAll(filepath.Dir(src1), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(src2), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src1, []byte("from-a"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte("from-b"), 0o640); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out.zip")
	if err := zipFiles([]string{src1, src2}, dest); err != nil {
		t.Fatalf("zipFiles: %v", err)
	}
	r, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer r.Close()
	if len(r.File) != 2 {
		t.Fatalf("期望 2 个 entry，实际 %d", len(r.File))
	}
	contents := map[string]string{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		contents[f.Name] = string(b)
	}
	// 第一个保持原名 app.log，第二个加 _2 后缀
	if contents["app.log"] != "from-a" {
		t.Fatalf("app.log 内容错: %q", contents["app.log"])
	}
	if contents["app_2.log"] != "from-b" {
		t.Fatalf("app_2.log 内容错: %q", contents["app_2.log"])
	}
}

// TestZipFiles_RejectNonExistent 验证：源文件不存在时 zipFiles 应报错。
//
// 早期实现里这个测试叫 PathTraversal（旧实现用 .. 检查拒绝）。
// 现在 zipFiles 不做路径穿越检查（那是上层 handler 的责任），
// 但 os.Open 失败仍要报错 —— 这一条覆盖"源文件路径无效"的所有场景。
//
// 注：失败时 zip 文件**可能**已经创建（OpenFile 在循环前就成功了，0 字节空 zip），
// 这是 zip/zip.Writer 的实现细节。但 zip 里没有任何 entry，所以不是"半截 zip"。
// 关键断言是 err != nil，调用方（handler）拿到错误后会删 zip 并报错。
func TestZipFiles_RejectNonExistent(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "does-not-exist.log")
	if err := zipFiles([]string{bad}, filepath.Join(dir, "out.zip")); err == nil {
		t.Fatal("不存在的源文件应报错")
	}
	// 即便 zip 文件被创建（0 字节），里面也不应有 entry
	dest := filepath.Join(dir, "out.zip")
	if info, err := os.Stat(dest); err == nil {
		r, zipErr := zip.OpenReader(dest)
		if zipErr == nil {
			if len(r.File) != 0 {
				t.Fatalf("失败的 zip 里不应有任何 entry，实际 %d", len(r.File))
			}
			r.Close()
		}
		if info.Size() == 0 {
			t.Logf("zip 大小为 0（符合预期 — OpenFile 成功后第一个源文件 Open 失败）")
		}
	}
}

// TestZipFiles_RejectDirectory 验证：源是目录时 zipFiles 应直接拒绝（避免 Open 后再 fail）。
func TestZipFiles_RejectDirectory(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := zipFiles([]string{subdir}, filepath.Join(dir, "out.zip")); err == nil {
		t.Fatal("源是目录时应报错")
	}
}

func TestZipFiles_Empty(t *testing.T) {
	dir := t.TempDir()
	if err := zipFiles(nil, filepath.Join(dir, "out.zip")); err == nil {
		t.Fatal("空文件列表应报错")
	}
}

// TestZipFiles_AcceptsWindowsStylePath 回归测试：Windows 本地路径（含反斜杠）应被接受。
//
// 早期实现的 strings.ContainsAny(p, "\\") 把任何含 \ 的本地路径都拒绝，
// 导致 Windows 上打 zip 失败。新实现用 filepath.Abs + os.Open 校验，
// Windows 路径天然含 \，应直接通过。
//
// Linux 上 dir + "\\evil.log" 是一段含字面 \ 的单文件路径，
// 既然源文件不存在，应走 os.Open 报错路径，而不是"路径非法"提前拒绝。
func TestZipFiles_AcceptsWindowsStylePath(t *testing.T) {
	dir := t.TempDir()
	bad := dir + string(os.PathSeparator) + "sub" + string(os.PathSeparator) + "evil.log"
	err := zipFiles([]string{bad}, filepath.Join(dir, "out.zip"))
	if err == nil {
		t.Fatal("不存在的源文件应报错（但不能因反斜杠提前拒绝）")
	}
	// 错误信息应该是 "打开源文件失败" 而不是 "非法源文件路径"
	if strings.Contains(err.Error(), "非法源文件路径") {
		t.Fatalf("不应再因为反斜杠拒绝：%v", err)
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