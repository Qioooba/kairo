package sftpclient

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockBackend / mockFile 用于单元测试，不依赖真实 SFTP
type mockBackend struct {
	files    map[string][]byte
	openErr  error
	closeN   int
	openPath string
}

func (m *mockBackend) Open(path string) (sftpFile, error) {
	m.openPath = path
	if m.openErr != nil {
		return nil, m.openErr
	}
	b, ok := m.files[path]
	if !ok {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	return &mockFile{r: strings.NewReader(string(b))}, nil
}

func (m *mockBackend) Close() error {
	m.closeN++
	return nil
}

type mockFile struct {
	r *strings.Reader
}

func (m *mockFile) Read(p []byte) (int, error) { return m.r.Read(p) }
func (m *mockFile) Close() error               { return nil }

// 用 _ 抑制未使用 import 警告
var _ = io.Copy
var _ = errors.New

func TestDownloadFile_Happy(t *testing.T) {
	dir := t.TempDir()
	backend := &mockBackend{
		files: map[string][]byte{
			"/remote/SystemOut.log": []byte("hello sftp"),
		},
	}
	c := newWithBackend(backend)
	defer c.Close()

	local := filepath.Join(dir, "sub", "out.log")
	n, err := c.DownloadFile("/remote/SystemOut.log", local)
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if n != int64(len("hello sftp")) {
		t.Errorf("bytes=%d, want %d", n, len("hello sftp"))
	}
	got, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello sftp" {
		t.Errorf("content=%q", got)
	}
}

func TestDownloadFile_OpenError(t *testing.T) {
	backend := &mockBackend{
		openErr: errors.New("boom"),
	}
	c := newWithBackend(backend)
	defer c.Close()

	_, err := c.DownloadFile("/x", "/tmp/y")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "打开远程文件失败") {
		t.Errorf("error msg: %v", err)
	}
}

func TestDownloadFile_NotExistRemote(t *testing.T) {
	backend := &mockBackend{files: map[string][]byte{}}
	c := newWithBackend(backend)
	defer c.Close()

	_, err := c.DownloadFile("/missing", "/tmp/y")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestDownloadFile_LocalDirNotWritable(t *testing.T) {
	dir := t.TempDir()
	backend := &mockBackend{
		files: map[string][]byte{"/x": []byte("data")},
	}
	c := newWithBackend(backend)
	defer c.Close()

	// 写到一个不存在的盘符（macOS 上 /nonexistent 也不行，用 /dev/null 父目录）
	bad := "/this-path-should-not-exist-12345/out.log"
	_, err := c.DownloadFile("/x", bad)
	if err == nil {
		t.Fatal("expected error for unwritable local path")
	}
	if !strings.Contains(err.Error(), "创建本地目录失败") &&
		!strings.Contains(err.Error(), "创建本地文件失败") {
		t.Errorf("error msg: %v", err)
	}
	_ = dir
}

func TestDownloadFile_TruncatesExisting(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "out.log")
	if err := os.WriteFile(local, []byte("OLD DATA OLD DATA"), 0o640); err != nil {
		t.Fatal(err)
	}
	backend := &mockBackend{
		files: map[string][]byte{"/x": []byte("NEW")},
	}
	c := newWithBackend(backend)
	defer c.Close()

	n, err := c.DownloadFile("/x", local)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("bytes=%d, want 3", n)
	}
	got, _ := os.ReadFile(local)
	if string(got) != "NEW" {
		t.Errorf("not truncated: %q", got)
	}
}

func TestClose_NilSafe(t *testing.T) {
	var c *Client
	if err := c.Close(); err != nil {
		t.Errorf("nil close: %v", err)
	}
	c = newWithBackend(&mockBackend{})
	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestDownloadFile_NilClient(t *testing.T) {
	var c *Client
	_, err := c.DownloadFile("/x", "/tmp/y")
	if err == nil {
		t.Fatal("nil client should fail")
	}
}
