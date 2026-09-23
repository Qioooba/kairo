package sftpclient

// sftp_oth_f_api_test.go — Batch F 修复后的 API 级回归（OTH-05 / OTH-06 的补强）。
//
// 这些测试用到修复中新增的导出能力：
//   - FileInfo 携带"已解析原始绝对路径"（RawPathOf / WithRawPath）；
//   - Client.ResolveDirIdentity；
//   - Stat/Open/ReadDir/ListLimited/MkdirAll 的 ctx 变体（昂贵解析阶段可取消）。
//
// 前置的失败回归见 sftp_oth_f_regression_test.go（那份文件只用修复前就存在的 API）。

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path"
	"testing"
)

// TestOTH05_ListEntriesCarryResolvedRawPath
// 列目录结果必须携带完整原始绝对路径；PathIDOf 必须基于它而不是传入的展示父路径。
func TestOTH05_ListEntriesCarryResolvedRawPath(t *testing.T) {
	gbkDir := "/tmp/" + gbkZhongWen
	p := newSFTPProbe()
	p.dirs["/"] = []os.FileInfo{fakeFileInfo{name: "tmp", isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs["/tmp"] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen, isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs[gbkDir] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen + ".log", size: 3, mode: 0o644}}
	p.files[gbkDir+"/"+gbkZhongWen+".log"] = []byte("abc")

	c := newWithBackend(p)
	defer c.Close()

	displayParent := "/tmp/" + utf8ZhongWen
	entries, err := c.ReadDir(displayParent)
	if err != nil {
		t.Fatalf("ReadDir(%q) 失败: %v", displayParent, err)
	}
	if len(entries) != 1 {
		t.Fatalf("条目数 = %d，期望 1", len(entries))
	}
	e := entries[0]
	wantRaw := gbkDir + "/" + gbkZhongWen + ".log"
	if got := RawPathOf(e); got != wantRaw {
		t.Fatalf("条目携带的原始绝对路径 = %q (hex %x)，期望 %q (hex %x)",
			got, []byte(got), wantRaw, []byte(wantRaw))
	}
	// PathIDOf 必须忽略"错误地传入展示父路径"，结果只取决于条目自带的原始路径。
	if got, want := PathIDOf(displayParent, e), EncodePathIdentity(wantRaw); got != want {
		t.Fatalf("PathIDOf(展示父路径) = %q，期望 %q", got, want)
	}
	// 用身份列目录（目录自身身份）也必须可用。
	dirID, err := c.ResolveDirIdentity(displayParent)
	if err != nil {
		t.Fatalf("ResolveDirIdentity(%q) 失败: %v", displayParent, err)
	}
	if dirID != gbkDir {
		t.Fatalf("目录身份 = %q (hex %x)，期望 %q (hex %x)", dirID, []byte(dirID), gbkDir, []byte(gbkDir))
	}
	if _, err := c.ReadDir(EncodePathIdentity(dirID)); err != nil {
		t.Fatalf("用目录身份 ReadDir 失败: %v", err)
	}
}

// TestOTH05_StatCarriesResolvedRawPath：Stat 结果同样携带原始绝对路径。
func TestOTH05_StatCarriesResolvedRawPath(t *testing.T) {
	gbkDir := "/tmp/" + gbkZhongWen
	p := newSFTPProbe()
	p.dirs["/tmp"] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen, isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs[gbkDir] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen + ".log", size: 3, mode: 0o644}}
	p.files[gbkDir+"/"+gbkZhongWen+".log"] = []byte("abc")

	c := newWithBackend(p)
	defer c.Close()

	info, err := c.Stat("/tmp/" + utf8ZhongWen + "/" + utf8ZhongWen + ".log")
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if got, want := RawPathOf(info), gbkDir+"/"+gbkZhongWen+".log"; got != want {
		t.Fatalf("Stat 条目原始路径 = %q，期望 %q", got, want)
	}
}

// TestOTH05_IdentityFromListingAvoidsReResolution
// 用列表给出的身份做 Stat/Open 时不得再枚举目录（OTH-06 的"身份贯通"要求）。
func TestOTH05_IdentityFromListingAvoidsReResolution(t *testing.T) {
	gbkDir := "/tmp/" + gbkZhongWen
	p := newSFTPProbe()
	p.dirs["/tmp"] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen, isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs[gbkDir] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen + ".log", size: 3, mode: 0o644}}
	p.files[gbkDir+"/"+gbkZhongWen+".log"] = []byte("abc")

	c := newWithBackend(p)
	defer c.Close()

	entries, err := c.ReadDir("/tmp/" + utf8ZhongWen)
	if err != nil {
		t.Fatalf("ReadDir 失败: %v", err)
	}
	id := PathIDOf("/tmp/"+utf8ZhongWen, entries[0])
	readDirsAfterList := p.readDirCalls

	if _, err := c.Stat(id); err != nil {
		t.Fatalf("用身份 Stat 失败: %v", err)
	}
	f, err := c.Open(id)
	if err != nil {
		t.Fatalf("用身份 Open 失败: %v", err)
	}
	_ = f.Close()
	if p.readDirCalls != readDirsAfterList {
		t.Fatalf("身份 Stat/Open 又触发了目录枚举: %d -> %d", readDirsAfterList, p.readDirCalls)
	}
}

// TestOTH06_ResolutionHonoursContextCancellation
// ctx 取消必须贯穿昂贵解析：取消后不再发生任何目录枚举。
func TestOTH06_ResolutionHonoursContextCancellation(t *testing.T) {
	p := newSFTPProbe()
	p.dirs["/tmp"] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen, isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs["/tmp/"+gbkZhongWen] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen + ".log", size: 3, mode: 0o644}}
	p.files["/tmp/"+gbkZhongWen+"/"+gbkZhongWen+".log"] = []byte("abc")
	c := newWithBackend(p)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.StatCtx(ctx, "/tmp/"+utf8ZhongWen+"/"+utf8ZhongWen+".log"); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消的 ctx 应返回 context.Canceled，实际: %v", err)
	}
	if _, err := c.OpenCtx(ctx, "/tmp/"+utf8ZhongWen+"/"+utf8ZhongWen+".log"); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenCtx 应返回 context.Canceled，实际: %v", err)
	}
	if _, _, err := c.ListLimitedCtx(ctx, "/tmp/"+utf8ZhongWen, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListLimitedCtx 应返回 context.Canceled，实际: %v", err)
	}
	if err := c.MkdirAllCtx(ctx, "/tmp/"+utf8ZhongWen+"/x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("MkdirAllCtx 应返回 context.Canceled，实际: %v", err)
	}
	if p.readDirCalls != 0 {
		t.Fatalf("取消后仍发生目录枚举: %d 次", p.readDirCalls)
	}
}

// TestOTH06_WriteInvalidatesDirIndex：写操作必须让受影响目录的索引失效，
// 避免后续解析继续用旧索引（缓存只是性能辅助，写后必须刷新）。
func TestOTH06_WriteInvalidatesDirIndex(t *testing.T) {
	gbkDir := "/tmp/" + gbkZhongWen
	p := newSFTPProbe()
	p.dirs["/tmp"] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen, isDir: true, mode: os.ModeDir | 0o755}}
	p.dirs[gbkDir] = []os.FileInfo{fakeFileInfo{name: gbkZhongWen + ".log", size: 3, mode: 0o644}}
	p.files[gbkDir+"/"+gbkZhongWen+".log"] = []byte("abc")

	c := newWithBackend(p)
	defer c.Close()

	// 先用一次中文 Stat 建立父目录索引。
	if _, err := c.Stat("/tmp/" + utf8ZhongWen + "/" + utf8ZhongWen + ".log"); err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	c.idxMu.Lock()
	_, cached := c.idx[gbkDir]
	c.idxMu.Unlock()
	if !cached {
		t.Fatalf("前置条件失败：中文解析后应建立 %q 的目录索引", gbkDir)
	}

	if err := c.UploadStream(context.Background(), bytes.NewReader([]byte("new")),
		"/tmp/"+utf8ZhongWen+"/"+"新文件.txt", 0o644, nil); err != nil {
		t.Fatalf("UploadStream 失败: %v", err)
	}
	c.idxMu.Lock()
	_, stillCached := c.idx[gbkDir]
	c.idxMu.Unlock()
	if stillCached {
		t.Fatalf("写操作后 %q 的目录索引没有失效", gbkDir)
	}
}

// TestOTH03_ExplicitIdentityWritesOnlyTargetEntry
// 传入明确身份时只能作用于指定的物理条目（审核 OTH-03 验收第 2 条）。
func TestOTH03_ExplicitIdentityWritesOnlyTargetEntry(t *testing.T) {
	gbkDir := "/tmp/" + gbkZhongWen
	utf8Dir := "/tmp/" + utf8ZhongWen
	p := sftpAmbiguousParentFixture()
	c := newWithBackend(p)
	defer c.Close()

	id := EncodePathIdentity(gbkDir + "/protected.txt")
	if err := c.UploadStream(context.Background(), bytes.NewReader([]byte("new")), id, 0o644, nil); err != nil {
		t.Fatalf("用明确身份上传失败: %v", err)
	}
	if got := string(p.files[gbkDir+"/protected.txt"]); got != "new" {
		t.Fatalf("身份指向的 GBK 条目内容 = %q，期望 %q", got, "new")
	}
	if got := string(p.files[utf8Dir+"/protected.txt"]); got != "utf8" {
		t.Fatalf("UTF-8 兄弟条目被误改：%q", got)
	}
}

// TestOTH05_PathIDOfFallsBackForForeignFileInfo
// 外部 backend 的 FileInfo 没有携带原始路径时，PathIDOf 仍按传入父目录兜底（兼容旧调用）。
func TestOTH05_PathIDOfFallsBackForForeignFileInfo(t *testing.T) {
	fi := fakeFileInfo{name: "app.log", size: 1, mode: 0o644}
	want := EncodePathIdentity(path.Join("/data", "app.log"))
	if got := PathIDOf("/data", fi); got != want {
		t.Fatalf("兜底 PathIDOf = %q，期望 %q", got, want)
	}
	// WithRawPath 之后必须以原始路径为准。
	wrapped := WithRawPath(fi, "/data/app.log")
	if got := PathIDOf("/wrong/parent", wrapped); got != want {
		t.Fatalf("WithRawPath 后的 PathIDOf = %q，期望 %q", got, want)
	}
}
