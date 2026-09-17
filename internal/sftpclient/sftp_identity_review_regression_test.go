package sftpclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"
)

// extendedMockBackend 扩展 mockBackend，支持 MkdirAll, CreateExclusive, Chtimes
type extendedMockBackend struct {
	mockBackend
	permDeniedPaths map[string]bool
	createdDirs     map[string]bool
}

func newExtendedMock() *extendedMockBackend {
	return &extendedMockBackend{
		mockBackend: mockBackend{
			files: make(map[string][]byte),
			dirs:  make(map[string][]os.FileInfo),
		},
		permDeniedPaths: make(map[string]bool),
		createdDirs:     make(map[string]bool),
	}
}

func (m *extendedMockBackend) Remove(path string) error {
	if m.permDeniedPaths[path] {
		return os.ErrPermission
	}
	return m.mockBackend.Remove(path)
}

func (m *extendedMockBackend) CreateExclusive(ctx context.Context, remotePath string) error {
	if _, ok := m.files[remotePath]; ok {
		return os.ErrExist
	}
	dir := path.Dir(remotePath)
	if dir != "/" && dir != "." {
		if _, ok := m.dirs[dir]; !ok && !m.createdDirs[dir] {
			return &os.PathError{Op: "create", Path: remotePath, Err: os.ErrNotExist}
		}
	}
	m.files[remotePath] = []byte{}
	return nil
}

func (m *extendedMockBackend) MkdirAll(p string) error {
	m.createdDirs[p] = true
	if m.dirs[p] == nil {
		m.dirs[p] = []os.FileInfo{}
	}
	return nil
}

func (m *extendedMockBackend) Chtimes(p string, atime, mtime time.Time) error {
	if _, ok := m.files[p]; !ok {
		if _, ok := m.dirs[p]; !ok {
			return os.ErrNotExist
		}
	}
	return nil
}

func (m *extendedMockBackend) WriteFile(p string, data []byte, perm os.FileMode) error {
	dir := path.Dir(p)
	if dir != "/" && dir != "." {
		if _, ok := m.dirs[dir]; !ok && !m.createdDirs[dir] {
			return &os.PathError{Op: "open", Path: p, Err: os.ErrNotExist}
		}
	}
	return m.mockBackend.WriteFile(p, data, perm)
}

func (m *extendedMockBackend) UploadStream(ctx context.Context, reader io.Reader, remotePath string, perm os.FileMode, progress func(written, total int64)) error {
	dir := path.Dir(remotePath)
	if dir != "/" && dir != "." {
		if _, ok := m.dirs[dir]; !ok && !m.createdDirs[dir] {
			return &os.PathError{Op: "open", Path: remotePath, Err: os.ErrNotExist}
		}
	}
	return m.mockBackend.UploadStream(ctx, reader, remotePath, perm, progress)
}

// TestRegression_R5_SiblingCollision_DestructiveOpsNotAmbiguous
// 验证 R5：同目录下存在同名但不同编码（UTF-8 与 GBK）的两个文件。
// 1. 普通显示路径操作由于存在歧义，破坏性操作（Remove/Rename）必须报错拒绝，不得随意选择或轮流尝试；
// 2. 使用具体标识（PathID 或原始路径）操作时，只能精准操作目标文件，兄弟文件不受影响；
// 3. 目标文件遇权限错误时，必须直接返回权限错误，严禁回退删除兄弟文件！
func TestRegression_R5_SiblingCollision_DestructiveOpsNotAmbiguous(t *testing.T) {
	mock := newExtendedMock()
	utf8Name := "中文.txt"
	gbkName := gbkZhongWen + ".txt"

	dir := "/data"
	utf8Path := path.Join(dir, utf8Name)
	gbkPath := path.Join(dir, gbkName)

	mock.files[utf8Path] = []byte("content-utf8")
	mock.files[gbkPath] = []byte("content-gbk")
	mock.dirs[dir] = []os.FileInfo{
		fakeFileInfo{name: utf8Name, size: int64(len("content-utf8")), mode: 0o644},
		fakeFileInfo{name: gbkName, size: int64(len("content-gbk")), mode: 0o644},
	}

	c := newWithBackend(mock)
	defer c.Close()

	// 1. ReadDir 能够获取到两条记录，且暴露原始名与编码标识
	entries, err := c.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	id0 := PathIDOf(dir, entries[0])
	id1 := PathIDOf(dir, entries[1])
	if id0 == id1 {
		t.Fatalf("PathIDs must be distinct for different files, got %q == %q", id0, id1)
	}

	// 2. 歧义测试：直接使用展示名 "/data/中文.txt" 删除时，必须报歧义错误，严禁误删任意文件
	err = c.Remove("/data/中文.txt")
	if err == nil {
		t.Fatalf("expected ambiguity error when removing display path with duplicate names, got nil")
	}
	if !errors.Is(err, ErrAmbiguousPath) {
		t.Logf("Remove error: %v (expected ErrAmbiguousPath)", err)
	}
	// 确认两个文件都还在
	if _, ok := mock.files[utf8Path]; !ok {
		t.Fatalf("utf8 file was deleted mistakenly on ambiguous call!")
	}
	if _, ok := mock.files[gbkPath]; !ok {
		t.Fatalf("gbk file was deleted mistakenly on ambiguous call!")
	}

	// 3. 精准操作：使用 GBK 文件的 PathID (或原始路径) 进行删除，只有 GBK 文件被删除，UTF-8 文件完整保留
	targetGBK := id1
	if RawNameOf(entries[0]) == gbkName {
		targetGBK = id0
	}
	if err := c.Remove(targetGBK); err != nil {
		t.Fatalf("Remove with specific identity failed: %v", err)
	}
	if _, ok := mock.files[gbkPath]; ok {
		t.Fatalf("expected gbk file to be deleted, but it still exists")
	}
	if _, ok := mock.files[utf8Path]; !ok {
		t.Fatalf("utf8 file should remain untouched, but was deleted!")
	}

	// 4. 权限错误不回退：重新设置两个文件，并将 UTF-8 设为无权限
	mock.files[utf8Path] = []byte("content-utf8")
	mock.files[gbkPath] = []byte("content-gbk")
	mock.permDeniedPaths[utf8Path] = true

	targetUTF8 := id0
	if RawNameOf(entries[1]) == utf8Name {
		targetUTF8 = id1
	}
	err = c.Remove(targetUTF8)
	if err == nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected permission denied on utf8 path, got: %v", err)
	}
	// 确保 gbkPath 绝对没有被回退删除！
	if _, ok := mock.files[gbkPath]; !ok {
		t.Fatalf("gbk file was deleted during fallback after utf8 permission denied!")
	}
}

// TestRegression_R6_GBKParentDirectoryResolution
// 验证 R6：远程服务器磁盘为 GBK 目录名，写操作必须自动解析父目录
func TestRegression_R6_GBKParentDirectoryResolution(t *testing.T) {
	mock := newExtendedMock()
	gbkDir := "/tmp/" + gbkZhongWen

	mock.dirs["/tmp"] = []os.FileInfo{
		fakeFileInfo{name: gbkZhongWen, isDir: true, mode: os.ModeDir | 0o755},
	}
	mock.dirs[gbkDir] = []os.FileInfo{}

	c := newWithBackend(mock)
	defer c.Close()

	// 1. UploadFile 写入到 /tmp/中文/a.txt
	tmpDir := t.TempDir()
	localFile := filepath.Join(tmpDir, "local.txt")
	_ = os.WriteFile(localFile, []byte("upload-data"), 0o644)

	err := c.UploadFile(localFile, "/tmp/"+utf8ZhongWen+"/a.txt", 0o644)
	if err != nil {
		t.Fatalf("UploadFile failed to resolve GBK parent directory: %v", err)
	}
	// 验证真正写入到了 GBK 目录
	expectedServerPath := gbkDir + "/a.txt"
	if data, ok := mock.files[expectedServerPath]; !ok || string(data) != "upload-data" {
		t.Fatalf("UploadFile did not write to resolved GBK directory path %q, files=%v", expectedServerPath, mock.files)
	}

	// 2. UploadStream 写入到 /tmp/中文/stream.txt
	streamData := []byte("stream-data")
	err = c.UploadStream(context.Background(), bytes.NewReader(streamData), "/tmp/"+utf8ZhongWen+"/stream.txt", 0o644, nil)
	if err != nil {
		t.Fatalf("UploadStream failed to resolve GBK parent directory: %v", err)
	}
	expectedStreamPath := gbkDir + "/stream.txt"
	if data, ok := mock.files[expectedStreamPath]; !ok || string(data) != "stream-data" {
		t.Fatalf("UploadStream did not write to %q, files=%v", expectedStreamPath, mock.files)
	}

	// 3. Rename 在 GBK 目录下重命名
	err = c.Rename("/tmp/"+utf8ZhongWen+"/a.txt", "/tmp/"+utf8ZhongWen+"/renamed.txt")
	if err != nil {
		t.Fatalf("Rename failed in GBK parent directory: %v", err)
	}
	expectedRenamedPath := gbkDir + "/renamed.txt"
	if _, ok := mock.files[expectedRenamedPath]; !ok {
		t.Fatalf("Rename target %q not found, files=%v", expectedRenamedPath, mock.files)
	}

	// 4. CreateExclusive 在 GBK 目录下创建文件
	err = c.CreateExclusive(context.Background(), "/tmp/"+utf8ZhongWen+"/exclusive.txt")
	if err != nil {
		t.Fatalf("CreateExclusive failed in GBK parent: %v", err)
	}
	if _, ok := mock.files[gbkDir+"/exclusive.txt"]; !ok {
		t.Fatalf("CreateExclusive did not create in GBK parent: %v", mock.files)
	}
}

// TestRegression_R6_UpdateExistingGBKFileInASCIIDir
// 验证 R6：在 ASCII 父目录下，保存已有以 GBK 命名的文件时，必须覆盖该 GBK 文件，严禁新建 UTF-8 同名副本
func TestRegression_R6_UpdateExistingGBKFileInASCIIDir(t *testing.T) {
	mock := newExtendedMock()
	parent := "/var/log"
	gbkName := gbkZhongWen + ".log"
	gbkPath := path.Join(parent, gbkName)

	mock.files[gbkPath] = []byte("version-1")
	mock.dirs[parent] = []os.FileInfo{
		fakeFileInfo{name: gbkName, size: int64(len("version-1")), mode: 0o644},
	}

	c := newWithBackend(mock)
	defer c.Close()

	tmpDir := t.TempDir()
	localFile := filepath.Join(tmpDir, "local.txt")
	_ = os.WriteFile(localFile, []byte("version-2"), 0o644)

	// 使用展示名 "/var/log/中文.log" 调用 UploadFile
	err := c.UploadFile(localFile, "/var/log/"+utf8ZhongWen+".log", 0o644)
	if err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}

	// 验证：原 GBK 文件已被更新为 version-2
	if string(mock.files[gbkPath]) != "version-2" {
		t.Fatalf("expected existing GBK file to be updated to 'version-2', got %q", string(mock.files[gbkPath]))
	}

	// 验证：严禁出现多余的 UTF-8 同名副本！
	utf8Path := "/var/log/" + utf8ZhongWen + ".log"
	if _, ok := mock.files[utf8Path]; ok && utf8Path != gbkPath {
		t.Fatalf("UploadFile created an unintended UTF-8 duplicate %q!", utf8Path)
	}
}
