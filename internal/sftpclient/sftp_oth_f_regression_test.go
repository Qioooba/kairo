package sftpclient

// sftp_oth_f_regression_test.go — Batch F（OTH-03/04/05/06）失败回归。
//
// 这批测试只用现有导出 API + 现有 mock 后端（mockBackend / extendedMockBackend），
// 目的是先把审核文档里的反例固化成"期望行为"的断言，再改实现。
//
//   - OTH-03：同显示名不同编码的父目录必须让写操作在**落地前**终止；
//     探针记录后端真正收到的路径字节（不是只检查错误文案）。
//   - OTH-04：GBK 已有父目录下的多级新建目录必须保留已解析前缀。
//   - OTH-05：列目录条目的 path_id 必须由"已解析的原始父路径"生成，
//     用该 path_id 调 Open 必须命中同一条物理记录。
//   - OTH-06：ASCII 文件的每次 Stat 不得重新枚举整个父目录（计数断言，不看耗时）。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// sftpProbeBackend 在 extendedMockBackend 上记录"后端真正收到的路径字节"，
// 并统计 ReadDir 调用次数 / 返回条目数。
//
// 记录原始路径是这批测试的关键：只看返回值无法区分实现是"解析到了正确物理条目"
// 还是"随便挑了第一个候选"。
type sftpProbeBackend struct {
	*extendedMockBackend

	readDirCalls   int // 成功完成的完整目录枚举次数
	readDirEntries int // 累计从后端返回的目录条目数
	statCalls      int

	writePaths  []string
	uploadPaths []string
	createPaths []string
	mkdirPaths  []string
	renamePaths [][2]string
	removePaths []string

	readDirDenied map[string]bool
}

func newSFTPProbe() *sftpProbeBackend {
	return &sftpProbeBackend{
		extendedMockBackend: newExtendedMock(),
		readDirDenied:       make(map[string]bool),
	}
}

func (p *sftpProbeBackend) ReadDir(dir string) ([]os.FileInfo, error) {
	if p.readDirDenied[dir] {
		return nil, &os.PathError{Op: "readdir", Path: dir, Err: os.ErrPermission}
	}
	entries, err := p.extendedMockBackend.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	p.readDirCalls++
	p.readDirEntries += len(entries)
	return entries, nil
}

func (p *sftpProbeBackend) Stat(target string) (os.FileInfo, error) {
	p.statCalls++
	return p.extendedMockBackend.Stat(target)
}

func (p *sftpProbeBackend) WriteFile(target string, data []byte, perm os.FileMode) error {
	p.writePaths = append(p.writePaths, target)
	return p.extendedMockBackend.WriteFile(target, data, perm)
}

func (p *sftpProbeBackend) UploadStream(ctx context.Context, reader io.Reader, remotePath string, perm os.FileMode, progress func(written, total int64)) error {
	p.uploadPaths = append(p.uploadPaths, remotePath)
	return p.extendedMockBackend.UploadStream(ctx, reader, remotePath, perm, progress)
}

func (p *sftpProbeBackend) CreateExclusive(ctx context.Context, remotePath string) error {
	p.createPaths = append(p.createPaths, remotePath)
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.extendedMockBackend.CreateExclusive(ctx, remotePath)
}

func (p *sftpProbeBackend) MkdirAll(dir string) error {
	p.mkdirPaths = append(p.mkdirPaths, dir)
	return p.extendedMockBackend.MkdirAll(dir)
}

func (p *sftpProbeBackend) Rename(oldPath, newPath string) error {
	p.renamePaths = append(p.renamePaths, [2]string{oldPath, newPath})
	return p.extendedMockBackend.Rename(oldPath, newPath)
}

func (p *sftpProbeBackend) Remove(target string) error {
	p.removePaths = append(p.removePaths, target)
	return p.extendedMockBackend.Remove(target)
}

// ---- 夹具 ----

// sftpAmbiguousParentFixture：/tmp 下同时存在 UTF-8 与 GBK 两个同名目录，
// 两边各有一个同名 protected.txt（正是审核实测的场景）。
func sftpAmbiguousParentFixture() *sftpProbeBackend {
	p := newSFTPProbe()
	utf8Dir := "/tmp/" + utf8ZhongWen
	gbkDir := "/tmp/" + gbkZhongWen
	p.dirs["/"] = []os.FileInfo{fakeFileInfo{name: "tmp", isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs["/tmp"] = []os.FileInfo{
		fakeFileInfo{name: utf8ZhongWen, isDir: true, mode: os.ModeDir | 0o755},
		fakeFileInfo{name: gbkZhongWen, isDir: true, mode: os.ModeDir | 0o755},
	}
	p.dirs[utf8Dir] = []os.FileInfo{fakeFileInfo{name: "protected.txt", size: 4, mode: 0o644}}
	p.dirs[gbkDir] = []os.FileInfo{fakeFileInfo{name: "protected.txt", size: 7, mode: 0o644}}
	p.files[utf8Dir+"/protected.txt"] = []byte("utf8")
	p.files[gbkDir+"/protected.txt"] = []byte("gbk-old")
	return p
}

// sftpGBKOnlyParentFixture：/tmp 下只有 GBK 目录（不要求用户本来就有重复目录）。
func sftpGBKOnlyParentFixture() *sftpProbeBackend {
	p := newSFTPProbe()
	gbkDir := "/tmp/" + gbkZhongWen
	p.dirs["/"] = []os.FileInfo{fakeFileInfo{name: "tmp", isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs["/tmp"] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen, isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs[gbkDir] = []os.FileInfo{}
	return p
}

// sftpSnapshotFiles 记录远端文件内容快照，用于"两边文件/目录快照完全不变"的断言。
func sftpSnapshotFiles(p *sftpProbeBackend) map[string]string {
	out := make(map[string]string, len(p.files))
	for k, v := range p.files {
		out[k] = string(v)
	}
	return out
}

func sftpSnapshotDiff(t *testing.T, before, after map[string]string) {
	t.Helper()
	for k, v := range before {
		if got, ok := after[k]; !ok {
			t.Errorf("快照差异：文件 %q (内容 %q) 被删除", k, v)
		} else if got != v {
			t.Errorf("快照差异：文件 %q 内容 %q -> %q", k, v, got)
		}
	}
	for k, v := range after {
		if _, ok := before[k]; !ok {
			t.Errorf("快照差异：新增文件 %q (内容 %q)", k, v)
		}
	}
}

// ============================================================================
// OTH-03：父目录歧义必须阻止写入
// ============================================================================

func TestOTH03_UploadFileMustAbortOnAmbiguousParent(t *testing.T) {
	p := sftpAmbiguousParentFixture()
	c := newWithBackend(p)
	defer c.Close()

	before := sftpSnapshotFiles(p)
	local := filepath.Join(t.TempDir(), "local.txt")
	if err := os.WriteFile(local, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := c.UploadFile(local, "/tmp/"+utf8ZhongWen+"/protected.txt", 0o644)
	if err == nil {
		t.Fatalf("父目录同显示名不同编码，UploadFile 竟然返回 nil；后端收到写入路径 %q", p.writePaths)
	}
	if !errors.Is(err, ErrAmbiguousPath) {
		t.Errorf("期望 ErrAmbiguousPath，实际: %v", err)
	}
	if len(p.writePaths) != 0 {
		t.Fatalf("写操作在歧义未解决前就落到后端，后端收到 %q", p.writePaths)
	}
	sftpSnapshotDiff(t, before, sftpSnapshotFiles(p))
}

func TestOTH03_UploadStreamMustAbortOnAmbiguousParent(t *testing.T) {
	p := sftpAmbiguousParentFixture()
	c := newWithBackend(p)
	defer c.Close()

	before := sftpSnapshotFiles(p)
	err := c.UploadStream(context.Background(), bytes.NewReader([]byte("new")),
		"/tmp/"+utf8ZhongWen+"/protected.txt", 0o644, nil)
	if err == nil {
		t.Fatalf("父目录歧义时 UploadStream 返回 nil；后端收到上传路径 %q", p.uploadPaths)
	}
	if !errors.Is(err, ErrAmbiguousPath) {
		t.Errorf("期望 ErrAmbiguousPath，实际: %v", err)
	}
	if len(p.uploadPaths) != 0 {
		t.Fatalf("UploadStream 在歧义未解决前就落到后端，后端收到 %q", p.uploadPaths)
	}
	sftpSnapshotDiff(t, before, sftpSnapshotFiles(p))
}

func TestOTH03_CreateExclusiveMustAbortOnAmbiguousParent(t *testing.T) {
	p := sftpAmbiguousParentFixture()
	c := newWithBackend(p)
	defer c.Close()

	before := sftpSnapshotFiles(p)
	// 新文件名：目标本身还不存在，只有父目录歧义。
	err := c.CreateExclusive(context.Background(), "/tmp/"+utf8ZhongWen+"/brand-new.txt")
	if err == nil {
		t.Fatalf("父目录歧义时 CreateExclusive 返回 nil；后端收到创建路径 %q", p.createPaths)
	}
	if !errors.Is(err, ErrAmbiguousPath) {
		t.Errorf("期望 ErrAmbiguousPath，实际: %v", err)
	}
	if len(p.createPaths) != 0 {
		t.Fatalf("CreateExclusive 在歧义未解决前就落到后端，后端收到 %q", p.createPaths)
	}
	sftpSnapshotDiff(t, before, sftpSnapshotFiles(p))
}

func TestOTH03_RenameTargetMustAbortOnAmbiguousParent(t *testing.T) {
	p := sftpAmbiguousParentFixture()
	c := newWithBackend(p)
	defer c.Close()

	p.dirs["/src"] = []os.FileInfo{fakeFileInfo{name: "a.txt", size: 3, mode: 0o644}}
	p.files["/src/a.txt"] = []byte("src")
	before := sftpSnapshotFiles(p)

	err := c.Rename("/src/a.txt", "/tmp/"+utf8ZhongWen+"/moved.txt")
	if err == nil {
		t.Fatalf("重命名目标父目录歧义时返回 nil；后端收到 %v", p.renamePaths)
	}
	if !errors.Is(err, ErrAmbiguousPath) {
		t.Errorf("期望 ErrAmbiguousPath，实际: %v", err)
	}
	if len(p.renamePaths) != 0 {
		t.Fatalf("Rename 在歧义未解决前就落到后端，后端收到 %v", p.renamePaths)
	}
	sftpSnapshotDiff(t, before, sftpSnapshotFiles(p))
}

func TestOTH03_MkdirAllMustAbortOnAmbiguousParent(t *testing.T) {
	p := sftpAmbiguousParentFixture()
	c := newWithBackend(p)
	defer c.Close()

	err := c.MkdirAll("/tmp/" + utf8ZhongWen + "/newdir")
	if err == nil {
		t.Fatalf("父目录歧义时 MkdirAll 返回 nil；后端收到创建目录参数 %q", p.mkdirPaths)
	}
	if !errors.Is(err, ErrAmbiguousPath) {
		t.Errorf("期望 ErrAmbiguousPath，实际: %v", err)
	}
	if len(p.mkdirPaths) != 0 {
		t.Fatalf("MkdirAll 在歧义未解决前就落到后端，后端收到 %q", p.mkdirPaths)
	}
}

// TestOTH03_NoReadDirPermissionMustNotPickArbitraryCandidate
// 父目录没有 ReadDir 权限但 Stat 可用：两个候选都能 Stat 成功时，
// 不允许借 fallback 任选一个（审核验收明确要求）。
func TestOTH03_NoReadDirPermissionMustNotPickArbitraryCandidate(t *testing.T) {
	p := sftpAmbiguousParentFixture()
	p.readDirDenied["/tmp"] = true
	c := newWithBackend(p)
	defer c.Close()

	before := sftpSnapshotFiles(p)
	err := c.UploadStream(context.Background(), bytes.NewReader([]byte("new")),
		"/tmp/"+utf8ZhongWen+"/protected.txt", 0o644, nil)
	if err == nil {
		t.Fatalf("ReadDir 被拒 + 两个候选都存在时 UploadStream 返回 nil；后端收到 %q", p.uploadPaths)
	}
	if !errors.Is(err, ErrAmbiguousPath) {
		t.Errorf("期望 ErrAmbiguousPath（不能任选候选），实际: %v", err)
	}
	if len(p.uploadPaths) != 0 {
		t.Fatalf("UploadStream 在歧义未解决前就落到后端，后端收到 %q", p.uploadPaths)
	}
	sftpSnapshotDiff(t, before, sftpSnapshotFiles(p))
}

// TestOTH03_WriteToMissingParentMustNotSilentlyFallBack
// 父目录确定不存在时，普通文件写路径不得把失败降级成"用未解析的展示路径继续写"。
func TestOTH03_WriteToMissingParentMustNotSilentlyFallBack(t *testing.T) {
	p := newSFTPProbe()
	p.dirs["/"] = []os.FileInfo{fakeFileInfo{name: "tmp", isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs["/tmp"] = []os.FileInfo{}
	c := newWithBackend(p)
	defer c.Close()

	err := c.UploadStream(context.Background(), bytes.NewReader([]byte("new")),
		"/tmp/missing-dir/x.txt", 0o644, nil)
	if err == nil {
		t.Fatalf("父目录不存在时 UploadStream 返回 nil；后端收到 %q", p.uploadPaths)
	}
	if len(p.uploadPaths) != 0 {
		t.Fatalf("父目录不存在却仍然写入后端，后端收到 %q", p.uploadPaths)
	}
}

// ============================================================================
// OTH-04：GBK 已有父目录下新建多级目录
// ============================================================================

func TestOTH04_MkdirAllKeepsResolvedGBKPrefix(t *testing.T) {
	p := sftpGBKOnlyParentFixture()
	c := newWithBackend(p)
	defer c.Close()

	display := "/tmp/" + utf8ZhongWen + "/new/deep"
	if err := c.MkdirAll(display); err != nil {
		t.Fatalf("MkdirAll(%q) 失败: %v", display, err)
	}
	want := "/tmp/" + gbkZhongWen + "/new/deep"
	if len(p.mkdirPaths) != 1 || p.mkdirPaths[0] != want {
		t.Fatalf("后端收到的创建目录参数 = %q (hex %x)，期望 %q (hex %x)",
			p.mkdirPaths, []byte(strings.Join(p.mkdirPaths, ",")), want, []byte(want))
	}
	for _, got := range p.mkdirPaths {
		if strings.Contains(got, "/tmp/"+utf8ZhongWen) {
			t.Fatalf("MkdirAll 丢掉了已解析的 GBK 前缀，用了 UTF-8 展示路径分支: %q", got)
		}
	}
}

func TestOTH04_MkdirAllEncodesNewChineseSegmentRelativeToResolvedParent(t *testing.T) {
	p := sftpGBKOnlyParentFixture()
	c := newWithBackend(p)
	defer c.Close()

	display := "/tmp/" + utf8ZhongWen + "/" + utf8ZhongWen + "/sub"
	if err := c.MkdirAll(display); err != nil {
		t.Fatalf("MkdirAll(%q) 失败: %v", display, err)
	}
	want := "/tmp/" + gbkZhongWen + "/" + gbkZhongWen + "/sub"
	if len(p.mkdirPaths) != 1 || p.mkdirPaths[0] != want {
		t.Fatalf("后端收到的创建目录参数 = %q (hex %x)，期望 %q (hex %x)",
			p.mkdirPaths, []byte(strings.Join(p.mkdirPaths, ",")), want, []byte(want))
	}
}

func TestOTH04_MkdirAllASCIIParentUnchanged(t *testing.T) {
	p := newSFTPProbe()
	p.dirs["/"] = []os.FileInfo{fakeFileInfo{name: "data", isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs["/data"] = []os.FileInfo{}
	c := newWithBackend(p)
	defer c.Close()

	if err := c.MkdirAll("/data/a/b/c"); err != nil {
		t.Fatalf("MkdirAll(/data/a/b/c) 失败: %v", err)
	}
	if len(p.mkdirPaths) != 1 || p.mkdirPaths[0] != "/data/a/b/c" {
		t.Fatalf("纯 ASCII 父目录行为被打乱: %q", p.mkdirPaths)
	}
}

func TestOTH04_MkdirAllMustNotSwallowAmbiguity(t *testing.T) {
	p := sftpAmbiguousParentFixture()
	c := newWithBackend(p)
	defer c.Close()

	err := c.MkdirAll("/tmp/" + utf8ZhongWen + "/x")
	if err == nil {
		t.Fatalf("歧义父目录下 MkdirAll 返回 nil；后端收到 %q", p.mkdirPaths)
	}
	if !errors.Is(err, ErrAmbiguousPath) {
		t.Errorf("期望 ErrAmbiguousPath，实际: %v", err)
	}
	if len(p.mkdirPaths) != 0 {
		t.Fatalf("MkdirAll 在歧义未解决前就落到后端: %q", p.mkdirPaths)
	}
}

// TestOTH04_MkdirAllMustNotCreateWhenEnumerationIsDenied
// 已存在前缀无法枚举（ReadDir 被拒）时不能猜"这一级不存在"然后建目录。
//
// 注意：新建段必须是非 ASCII——纯 ASCII 段下 Stat 本身就是确定性结论，
// 不需要 ReadDir，那种情况下继续创建是正确行为。
func TestOTH04_MkdirAllMustNotCreateWhenEnumerationIsDenied(t *testing.T) {
	p := sftpGBKOnlyParentFixture()
	p.readDirDenied["/tmp/"+gbkZhongWen] = true
	c := newWithBackend(p)
	defer c.Close()

	err := c.MkdirAll("/tmp/" + utf8ZhongWen + "/" + utf8ZhongWen)
	if err == nil {
		t.Fatalf("无法确认目录是否存在时 MkdirAll 返回 nil；后端收到 %q", p.mkdirPaths)
	}
	if len(p.mkdirPaths) != 0 {
		t.Fatalf("枚举被拒却仍然创建目录: %q", p.mkdirPaths)
	}
}

// ============================================================================
// OTH-05：path_id 身份必须基于已解析的原始父路径
// ============================================================================

// TestOTH05_PathIDOfMustResolveNestedGBKParent
// 审核文档的 TestAuditPathIDMustIncludeResolvedParent：先用展示路径成功列目录，
// 再按 HTTP handler 的方式生成子文件 path_id，最后 Client.Open(path_id) 必须命中。
func TestOTH05_PathIDOfMustResolveNestedGBKParent(t *testing.T) {
	gbkDir := "/tmp/" + gbkZhongWen
	p := newSFTPProbe()
	p.dirs["/"] = []os.FileInfo{fakeFileInfo{name: "tmp", isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs["/tmp"] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen, isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs[gbkDir] = []os.FileInfo{
		fakeFileInfo{name: utf8ZhongWen + ".txt", size: 4, mode: 0o644},
		fakeFileInfo{name: gbkZhongWen + ".txt", size: 7, mode: 0o644},
	}
	p.files[gbkDir+"/"+utf8ZhongWen+".txt"] = []byte("utf8")
	p.files[gbkDir+"/"+gbkZhongWen+".txt"] = []byte("gbk!!!")

	c := newWithBackend(p)
	defer c.Close()

	displayParent := "/tmp/" + utf8ZhongWen
	entries, err := c.ReadDir(displayParent)
	if err != nil {
		t.Fatalf("ReadDir(%q) 失败: %v", displayParent, err)
	}
	if len(entries) != 2 {
		t.Fatalf("条目数 = %d，期望 2", len(entries))
	}

	for _, e := range entries {
		id := PathIDOf(displayParent, e)
		if id == "" {
			t.Fatalf("条目 %q 没有 path_id", e.Name())
		}
		f, err := c.Open(id)
		if err != nil {
			t.Fatalf("用 PathIDOf 生成的 path_id 无法打开条目 %q: %v (path_id 解码 = %q)",
				e.Name(), err, sftpDecodeForTest(id))
		}
		body := make([]byte, 16)
		n, _ := f.Read(body)
		_ = f.Close()
		want := string(p.files[path.Join(gbkDir, RawNameOf(e))])
		if string(body[:n]) != want {
			t.Errorf("path_id 命中了错误的物理条目: 条目 %q 读到 %q，期望 %q",
				e.Name(), body[:n], want)
		}
	}
}

func TestOTH05_ASCIIEntriesAlsoCarryUsablePathID(t *testing.T) {
	p := newSFTPProbe()
	p.dirs["/"] = []os.FileInfo{fakeFileInfo{name: "data", isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs["/data"] = []os.FileInfo{
		fakeFileInfo{name: "app.log", size: 3, mode: 0o644},
		fakeFileInfo{name: utf8ZhongWen + ".log", size: 5, mode: 0o644},
	}
	p.files["/data/app.log"] = []byte("abc")
	p.files["/data/"+utf8ZhongWen+".log"] = []byte("hello")

	c := newWithBackend(p)
	defer c.Close()

	entries, err := c.ReadDir("/data")
	if err != nil {
		t.Fatalf("ReadDir(/data) 失败: %v", err)
	}
	for _, e := range entries {
		id := PathIDOf("/data", e)
		if id == "" {
			t.Fatalf("ASCII/UTF-8 条目 %q 缺少 path_id（身份必须覆盖所有条目）", e.Name())
		}
		if _, err := c.Open(id); err != nil {
			t.Fatalf("条目 %q 的 path_id 不可用: %v", e.Name(), err)
		}
	}
}

// ============================================================================
// OTH-06：ASCII 文件的 Stat 不得反复枚举整个父目录
// ============================================================================

func TestOTH06_ASCIIStatMustNotEnumerateParent(t *testing.T) {
	const n = 1000
	p := newSFTPProbe()
	entries := make([]os.FileInfo, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("file-%04d.log", i)
		entries = append(entries, fakeFileInfo{name: name, size: 4, mode: 0o644})
		p.files["/data/"+name] = []byte("data")
	}
	p.dirs["/"] = []os.FileInfo{fakeFileInfo{name: "data", isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs["/data"] = entries

	c := newWithBackend(p)
	defer c.Close()

	for i := 0; i < n; i++ {
		if _, err := c.Stat(fmt.Sprintf("/data/file-%04d.log", i)); err != nil {
			t.Fatalf("Stat 第 %d 个文件失败: %v", i, err)
		}
	}
	if p.readDirCalls > 2 || p.readDirEntries > 2*n {
		t.Fatalf("%d 次 ASCII Stat 造成了 %d 次完整目录读、%d 条返回条目（期望接近 0；"+
			"审核文档基线为 1000 次读 / 1000000 条）",
			n, p.readDirCalls, p.readDirEntries)
	}
	t.Logf("计数结果：%d 次 ASCII Stat → %d 次完整目录读、%d 条返回条目", n, p.readDirCalls, p.readDirEntries)
}

func TestOTH06_RepeatedNonASCIIStatEnumeratesParentBoundedTimes(t *testing.T) {
	const n = 200
	p := newSFTPProbe()
	entries := make([]os.FileInfo, 0, n)
	for i := 0; i < n; i++ {
		raw := fmt.Sprintf("%s-%03d.log", gbkZhongWen, i)
		entries = append(entries, fakeFileInfo{name: raw, size: 4, mode: 0o644})
		p.files["/data/"+raw] = []byte("data")
	}
	p.dirs["/"] = []os.FileInfo{fakeFileInfo{name: "data", isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs["/data"] = entries

	c := newWithBackend(p)
	defer c.Close()

	for i := 0; i < n; i++ {
		display := fmt.Sprintf("/data/%s-%03d.log", utf8ZhongWen, i)
		if _, err := c.Stat(display); err != nil {
			t.Fatalf("Stat %q 失败: %v", display, err)
		}
	}
	if p.readDirCalls > 5 {
		t.Fatalf("%d 次中文 Stat 造成了 %d 次完整目录读（期望有界索引复用）", n, p.readDirCalls)
	}
	t.Logf("计数结果：%d 次中文 Stat → %d 次完整目录读", n, p.readDirCalls)
}

func TestOTH06_IdentityStatNeverEnumeratesParent(t *testing.T) {
	p := newSFTPProbe()
	raw := "/data/" + gbkZhongWen + ".log"
	p.dirs["/data"] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen + ".log", size: 4, mode: 0o644}}
	p.files[raw] = []byte("data")
	c := newWithBackend(p)
	defer c.Close()

	if _, err := c.Stat(EncodePathIdentity(raw)); err != nil {
		t.Fatalf("用身份 Stat 失败: %v", err)
	}
	if p.readDirCalls != 0 {
		t.Fatalf("身份路径不该触发目录枚举，实际 %d 次", p.readDirCalls)
	}
}

// sftpDecodeForTest 把 path_id 解回原始字节，便于失败信息看清"错在哪一段"。
func sftpDecodeForTest(id string) string {
	raw, _ := DecodePathIdentity(id)
	return raw
}
