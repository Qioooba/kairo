package sftpclient

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockBackend / mockFile 用于单元测试，不依赖真实 SFTP
//
// v0.3 起扩展支持 ReadDir / Stat：
//   - files: key=文件路径, value=文件内容（用于 Open / Stat 文件）
//   - dirs:  key=目录路径, value=目录里所有条目（用于 ReadDir）
//   - 两者共同模拟一个简化的远端文件系统
type mockBackend struct {
	files    map[string][]byte
	dirs     map[string][]os.FileInfo
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
	return &mockFile{r: strings.NewReader(string(b)), size: int64(len(b))}, nil
}

func (m *mockBackend) ReadDir(path string) ([]os.FileInfo, error) {
	entries, ok := m.dirs[path]
	if !ok {
		return nil, &os.PathError{Op: "readdir", Path: path, Err: os.ErrNotExist}
	}
	return entries, nil
}

func (m *mockBackend) Stat(path string) (os.FileInfo, error) {
	if entries, ok := m.dirs[path]; ok {
		// 视为目录：取第一个条目的字段当占位（mode 用目录位）
		if len(entries) == 0 {
			return fakeFileInfo{name: filepath.Base(path), isDir: true}, nil
		}
		_ = entries
		return fakeFileInfo{name: filepath.Base(path), isDir: true, mode: os.ModeDir | 0o755}, nil
	}
	if b, ok := m.files[path]; ok {
		return fakeFileInfo{name: filepath.Base(path), size: int64(len(b)), mode: 0o644}, nil
	}
	return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
}

func (m *mockBackend) Close() error {
	m.closeN++
	return nil
}

type mockFile struct {
	r    *strings.Reader
	size int64
}

func (m *mockFile) Read(p []byte) (int, error) { return m.r.Read(p) }
func (m *mockFile) Close() error               { return nil }
func (m *mockFile) Stat() (os.FileInfo, error) {
	return fakeFileInfo{name: "mock", size: m.size}, nil
}

// fakeFileInfo 是 os.FileInfo 的最小实现。
// 字段在 Stat / ReadDir 路径里都被读到，所以都实现出来。
type fakeFileInfo struct {
	name  string
	size  int64
	isDir bool
	mode  os.FileMode
	mtime time.Time
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return f.mtime }
func (f fakeFileInfo) IsDir() bool        { return f.isDir }
func (f fakeFileInfo) Sys() interface{}   { return nil }

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

func TestDownloadFileWithProgress_Callbacks(t *testing.T) {
	dir := t.TempDir()
	// 256KB 数据，进度回调阈值 64KB，预期至少触发 3-4 次（中间 3-4 次 + 末尾 1 次）
	payload := strings.Repeat("A", 256*1024)
	backend := &mockBackend{
		files: map[string][]byte{"/remote/big.log": []byte(payload)},
	}
	c := newWithBackend(backend)
	defer c.Close()

	var (
		mu       sync.Mutex
		calls    int
		lastW    int64
		lastT    int64
		gotTotal int64 = -1
	)
	progress := func(written, total int64) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		lastW = written
		lastT = total
		if total > 0 {
			gotTotal = total
		}
	}

	local := filepath.Join(dir, "out.log")
	n, err := c.DownloadFileWithProgress("/remote/big.log", local, progress)
	if err != nil {
		t.Fatalf("DownloadFileWithProgress: %v", err)
	}
	if n != int64(len(payload)) {
		t.Errorf("bytes=%d, want %d", n, len(payload))
	}
	if gotTotal != int64(len(payload)) {
		t.Errorf("total=%d, want %d", gotTotal, len(payload))
	}
	if lastW != int64(len(payload)) {
		t.Errorf("last written=%d, want %d", lastW, len(payload))
	}
	if lastT != int64(len(payload)) {
		t.Errorf("last total=%d, want %d", lastT, len(payload))
	}
	if calls < 2 {
		t.Errorf("progress callbacks=%d, expected >= 2 (intermediate + final)", calls)
	}
}

func TestDownloadFileWithProgress_NilCallback(t *testing.T) {
	dir := t.TempDir()
	backend := &mockBackend{
		files: map[string][]byte{"/x": []byte("data")},
	}
	c := newWithBackend(backend)
	defer c.Close()

	// 传 nil 不应 panic，且行为等同 DownloadFile
	n, err := c.DownloadFileWithProgress("/x", filepath.Join(dir, "o.log"), nil)
	if err != nil {
		t.Fatalf("nil progress: %v", err)
	}
	if n != 4 {
		t.Errorf("bytes=%d, want 4", n)
	}
}

// ---------- ReadDir / Stat（v0.3 文件浏览器用） ----------

func TestReadDir_Happy(t *testing.T) {
	when := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	backend := &mockBackend{
		dirs: map[string][]os.FileInfo{
			"/opt": {
				fakeFileInfo{name: "IBM", isDir: true, mode: os.ModeDir | 0o755, mtime: when},
				fakeFileInfo{name: "readme.txt", size: 12, mode: 0o644, mtime: when},
			},
		},
	}
	c := newWithBackend(backend)
	defer c.Close()

	infos, err := c.ReadDir("/opt")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("len=%d, want 2", len(infos))
	}
	if infos[0].Name() != "IBM" || !infos[0].IsDir() {
		t.Errorf("infos[0]=%v, want IBM dir", infos[0])
	}
	if infos[1].Name() != "readme.txt" || infos[1].IsDir() {
		t.Errorf("infos[1]=%v, want readme.txt file", infos[1])
	}
}

func TestReadDir_NotExist(t *testing.T) {
	c := newWithBackend(&mockBackend{dirs: map[string][]os.FileInfo{}})
	defer c.Close()

	_, err := c.ReadDir("/nope")
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
}

func TestStat_Dir(t *testing.T) {
	backend := &mockBackend{
		dirs: map[string][]os.FileInfo{
			"/opt": {fakeFileInfo{name: "child", isDir: true}},
		},
	}
	c := newWithBackend(backend)
	defer c.Close()

	info, err := c.Stat("/opt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("IsDir=%v, want true", info.IsDir())
	}
}

func TestStat_File(t *testing.T) {
	backend := &mockBackend{
		files: map[string][]byte{"/opt/a.log": []byte("hello")},
	}
	c := newWithBackend(backend)
	defer c.Close()

	info, err := c.Stat("/opt/a.log")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.IsDir() {
		t.Errorf("IsDir=true, want false")
	}
	if info.Size() != 5 {
		t.Errorf("size=%d, want 5", info.Size())
	}
}

func TestStat_NotExist(t *testing.T) {
	c := newWithBackend(&mockBackend{})
	defer c.Close()

	_, err := c.Stat("/nope")
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestReadDir_NilClient(t *testing.T) {
	var c *Client
	_, err := c.ReadDir("/x")
	if err == nil {
		t.Fatal("nil client should fail")
	}
}

func TestStat_NilClient(t *testing.T) {
	var c *Client
	_, err := c.Stat("/x")
	if err == nil {
		t.Fatal("nil client should fail")
	}
}
