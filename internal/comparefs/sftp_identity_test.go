package comparefs

// sftp_identity_test.go — Batch F（OTH-05 第 5 点）失败回归。
//
// 比对适配器的要求：展示相对路径继续用于左右对齐，但 Stat/Open 必须使用
// 列表结果给出的**源文件身份**（原始字节路径），并且同显示名冲突要显式报歧义，
// 不能在 map 里静默覆盖。
//
// 这里的替身后端是"真实协议形状"的最小实现（Client 之上的 sftpBackend），
// 因此走的是 sftpclient 的真实解析/身份代码，而不是再写一套 mock 语义。

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"kairo/internal/sftpclient"
)

const (
	sftpIdentityUTF8ZhongWen = "中文"
	sftpIdentityGBKZhongWen  = "\xd6\xd0\xce\xc4"
)

type sftpIdentityInfo struct {
	name  string
	size  int64
	isDir bool
}

func (i sftpIdentityInfo) Name() string       { return i.name }
func (i sftpIdentityInfo) Size() int64        { return i.size }
func (i sftpIdentityInfo) Mode() os.FileMode  { return 0o644 }
func (i sftpIdentityInfo) ModTime() time.Time { return time.Unix(0, 0).UTC() }
func (i sftpIdentityInfo) IsDir() bool        { return i.isDir }
func (i sftpIdentityInfo) Sys() interface{}   { return nil }

type sftpIdentityFile struct {
	r    io.Reader
	size int64
}

func (f *sftpIdentityFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *sftpIdentityFile) Close() error               { return nil }
func (f *sftpIdentityFile) Stat() (os.FileInfo, error) {
	return sftpIdentityInfo{name: "fake", size: f.size}, nil
}

// sftpIdentityBackend 实现 sftpclient 的 sftpBackend 契约（9 个方法）。
type sftpIdentityBackend struct {
	dirs         map[string][]os.FileInfo
	files        map[string][]byte
	readDirCalls int
	statPaths    []string
	openPaths    []string
	mkdirPaths   []string
}

func newSFTPIdentityBackend() *sftpIdentityBackend {
	return &sftpIdentityBackend{dirs: map[string][]os.FileInfo{}, files: map[string][]byte{}}
}

// MkdirAll 记录后端真正收到的目录路径字节（比对适配器的目录同步会走这里）。
func (b *sftpIdentityBackend) MkdirAll(p string) error {
	b.mkdirPaths = append(b.mkdirPaths, p)
	if _, ok := b.dirs[p]; !ok {
		b.dirs[p] = []os.FileInfo{}
	}
	return nil
}

func (b *sftpIdentityBackend) Open(p string) (sftpclient.SftpFile, error) {
	b.openPaths = append(b.openPaths, p)
	content, ok := b.files[p]
	if !ok {
		return nil, &os.PathError{Op: "open", Path: p, Err: os.ErrNotExist}
	}
	return &sftpIdentityFile{r: strings.NewReader(string(content)), size: int64(len(content))}, nil
}

func (b *sftpIdentityBackend) ReadDir(p string) ([]os.FileInfo, error) {
	entries, ok := b.dirs[p]
	if !ok {
		return nil, &os.PathError{Op: "readdir", Path: p, Err: os.ErrNotExist}
	}
	b.readDirCalls++
	return entries, nil
}

func (b *sftpIdentityBackend) ListLimited(p string, max int) ([]os.FileInfo, bool, error) {
	entries, err := b.ReadDir(p)
	if err != nil {
		return nil, false, err
	}
	if max > 0 && len(entries) > max {
		return entries[:max], true, nil
	}
	return entries, false, nil
}

func (b *sftpIdentityBackend) Stat(p string) (os.FileInfo, error) {
	b.statPaths = append(b.statPaths, p)
	if entries, ok := b.dirs[p]; ok {
		_ = entries
		return sftpIdentityInfo{name: path.Base(p), isDir: true}, nil
	}
	if content, ok := b.files[p]; ok {
		return sftpIdentityInfo{name: path.Base(p), size: int64(len(content))}, nil
	}
	return nil, &os.PathError{Op: "stat", Path: p, Err: os.ErrNotExist}
}

func (b *sftpIdentityBackend) WriteFile(p string, data []byte, perm os.FileMode) error {
	b.files[p] = data
	return nil
}

func (b *sftpIdentityBackend) UploadStream(ctx context.Context, r io.Reader, p string, perm os.FileMode, progress func(int64, int64)) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	b.files[p] = data
	return nil
}

func (b *sftpIdentityBackend) Rename(oldPath, newPath string) error {
	data, ok := b.files[oldPath]
	if !ok {
		return os.ErrNotExist
	}
	b.files[newPath] = data
	delete(b.files, oldPath)
	return nil
}

func (b *sftpIdentityBackend) Remove(p string) error {
	delete(b.files, p)
	return nil
}

func (b *sftpIdentityBackend) Close() error { return nil }

func newSFTPIdentityAdapter(b *sftpIdentityBackend) *SFTP {
	return NewSFTP(sftpclient.NewWithBackend(b), nil, "fake")
}

// TestSFTPListReportsDisplayNameAmbiguity：同目录两条显示名相同的条目必须报歧义。
func TestSFTPListReportsDisplayNameAmbiguity(t *testing.T) {
	b := newSFTPIdentityBackend()
	b.dirs["/data"] = []os.FileInfo{
		sftpIdentityInfo{name: sftpIdentityUTF8ZhongWen + ".txt", size: 4},
		sftpIdentityInfo{name: sftpIdentityGBKZhongWen + ".txt", size: 7},
	}
	b.files["/data/"+sftpIdentityUTF8ZhongWen+".txt"] = []byte("utf8")
	b.files["/data/"+sftpIdentityGBKZhongWen+".txt"] = []byte("gbk!!!!")

	adapter := newSFTPIdentityAdapter(b)
	defer adapter.Close()

	entries, err := adapter.List(context.Background(), "/data")
	if err == nil {
		t.Fatalf("两个条目显示名相同，List 必须报歧义（不能在 map 中静默覆盖）；返回 %d 条: %+v", len(entries), entries)
	}
	if !errors.Is(err, sftpclient.ErrAmbiguousPath) {
		t.Errorf("期望 ErrAmbiguousPath，实际: %v", err)
	}
}

// TestSFTPMkdirAllKeepsResolvedGBKPrefix
// OTH-04：目录同步把 MkdirAll 交给这个适配器后，必须保留已解析的 GBK 前缀、
// 只新建 ASCII 层级，且不依赖"父目录计划先执行"。
func TestSFTPMkdirAllKeepsResolvedGBKPrefix(t *testing.T) {
	gbkDir := "/tmp/" + sftpIdentityGBKZhongWen
	b := newSFTPIdentityBackend()
	b.dirs["/tmp"] = []os.FileInfo{sftpIdentityInfo{name: sftpIdentityGBKZhongWen, isDir: true}}
	b.dirs[gbkDir] = []os.FileInfo{}

	adapter := newSFTPIdentityAdapter(b)
	defer adapter.Close()

	if err := adapter.MkdirAll(context.Background(), "/tmp/"+sftpIdentityUTF8ZhongWen+"/new/deep", 0o755); err != nil {
		t.Fatalf("MkdirAll 失败: %v", err)
	}
	want := gbkDir + "/new/deep"
	if len(b.mkdirPaths) != 1 || b.mkdirPaths[0] != want {
		t.Fatalf("后端收到的创建目录参数 = %q (hex %x)，期望 %q (hex %x)",
			b.mkdirPaths, []byte(strings.Join(b.mkdirPaths, ",")), want, []byte(want))
	}
}

// TestSFTPStatOpenReuseListIdentity：List 之后 Stat/Open 必须复用列表身份，
// 不能再按展示名重新枚举目录（否则 OTH-06 的 O(N²) 会在比较同步里放大）。
func TestSFTPStatOpenReuseListIdentity(t *testing.T) {
	rawPath := "/data/" + sftpIdentityGBKZhongWen + ".log"
	displayPath := "/data/" + sftpIdentityUTF8ZhongWen + ".log"
	b := newSFTPIdentityBackend()
	b.dirs["/data"] = []os.FileInfo{sftpIdentityInfo{name: sftpIdentityGBKZhongWen + ".log", size: 3}}
	b.files[rawPath] = []byte("gbk")

	adapter := newSFTPIdentityAdapter(b)
	defer adapter.Close()

	entries, err := adapter.List(context.Background(), "/data")
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("期望 1 条条目，得到 %d", len(entries))
	}
	// 展示相对路径继续用于对齐。
	if entries[0].Name != sftpIdentityUTF8ZhongWen+".log" || entries[0].Path != displayPath {
		t.Fatalf("展示路径应保持相对展示名: name=%q path=%q", entries[0].Name, entries[0].Path)
	}

	readDirsAfterList := b.readDirCalls
	if _, err := adapter.Stat(context.Background(), entries[0].Path); err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if b.readDirCalls != readDirsAfterList {
		t.Errorf("Stat 又枚举了目录：List 后 %d 次 -> Stat 后 %d 次", readDirsAfterList, b.readDirCalls)
	}
	if len(b.statPaths) == 0 || b.statPaths[len(b.statPaths)-1] != rawPath {
		t.Fatalf("Stat 访问的路径 = %q，期望源身份（原始字节）%q", b.statPaths, rawPath)
	}

	rc, err := adapter.Open(context.Background(), entries[0].Path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != "gbk" {
		t.Fatalf("Open 读到 %q，期望 %q", body, "gbk")
	}
	if len(b.openPaths) == 0 || b.openPaths[len(b.openPaths)-1] != rawPath {
		t.Fatalf("Open 访问的路径 = %q，期望 %q", b.openPaths, rawPath)
	}
}
