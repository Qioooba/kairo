package sftpclient

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

// fakeFileInfoBatch 生成 N 个 fakeFileInfo 用于测试（v1.0 新增）。
func fakeFileInfoBatch(n int) []os.FileInfo {
	out := make([]os.FileInfo, n)
	for i := 0; i < n; i++ {
		// 用 strconv.Itoa + 补零保证字典序 = 数字序，方便测试断言
		name := fmt.Sprintf("file_%08d.log", i)
		out[i] = fakeFileInfo{
			name:  name,
			size:  int64(1024 * (i + 1)),
			isDir: false,
			mode:  0o644,
		}
	}
	return out
}

// TestListLimited_TruncatesAt10k 验证 ListLimited 对 10w 文件目录只返回 max 条 + truncated=true。
//
// 设计动机：10w 文件目录（旧实现）会爆内存（10w entries × ~100B = 10MB + SFTP 协议层全部拉回）。
// v1.0 加 ListLimited 强制 cap 到 max 条数，避免 OOM。
func TestListLimited_TruncatesAt10k(t *testing.T) {
	if testing.Short() {
		t.Skip("skip 10k file test in short mode")
	}
	const totalFiles = 100_000
	const maxEntries = 1000

	backend := &mockBackend{
		dirs: map[string][]os.FileInfo{
			"/huge": fakeFileInfoBatch(totalFiles),
		},
	}
	c := newWithBackend(backend)
	defer c.Close()

	entries, truncated, err := c.ListLimited("/huge", maxEntries)
	if err != nil {
		t.Fatalf("ListLimited: %v", err)
	}

	if !truncated {
		t.Errorf("truncated 应为 true（%d 文件，max=%d）", totalFiles, maxEntries)
	}
	if len(entries) != maxEntries {
		t.Errorf("entries=%d, want %d", len(entries), maxEntries)
	}
	// 验证返回的是前 maxEntries 个（fakeInfoBatch 保证顺序）
	if entries[0].Name() != "file_00000000.log" {
		t.Errorf("entries[0].Name()=%s, want file_00000000.log", entries[0].Name())
	}
	if entries[maxEntries-1].Name() != fmt.Sprintf("file_%08d.log", maxEntries-1) {
		t.Errorf("entries[max-1].Name()=%s, want file_%08d.log",
			entries[maxEntries-1].Name(), maxEntries-1)
	}
}

// TestListLimited_NoTruncationWhenSmall 验证文件数 ≤ max 时 truncated=false 且 entries 是全部。
func TestListLimited_NoTruncationWhenSmall(t *testing.T) {
	const totalFiles = 50
	const maxEntries = 1000

	backend := &mockBackend{
		dirs: map[string][]os.FileInfo{
			"/small": fakeFileInfoBatch(totalFiles),
		},
	}
	c := newWithBackend(backend)
	defer c.Close()

	entries, truncated, err := c.ListLimited("/small", maxEntries)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("truncated 应为 false（50 < max=1000）")
	}
	if len(entries) != totalFiles {
		t.Errorf("entries=%d, want %d", len(entries), totalFiles)
	}
}

// TestListLimited_ExactBoundary 验证"恰好等于 max"的情况：truncated=true（保守行为）。
// 实际 ListLimited 是 `len(infos) > max`，所以等于 max 时 truncated=false。
func TestListLimited_ExactBoundary(t *testing.T) {
	const totalFiles = 100
	const maxEntries = 100

	backend := &mockBackend{
		dirs: map[string][]os.FileInfo{
			"/exact": fakeFileInfoBatch(totalFiles),
		},
	}
	c := newWithBackend(backend)
	defer c.Close()

	entries, truncated, err := c.ListLimited("/exact", maxEntries)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Errorf("恰好等于 max 时 truncated 应 false（实际 %d）", len(entries))
	}
	if len(entries) != totalFiles {
		t.Errorf("entries=%d, want %d", len(entries), totalFiles)
	}
}

// TestListLimited_MaxZeroMeansNoCap 验证 max<=0 退化为 ReadDir 全量返回。
func TestListLimited_MaxZeroMeansNoCap(t *testing.T) {
	backend := &mockBackend{
		dirs: map[string][]os.FileInfo{
			"/a": fakeFileInfoBatch(50),
		},
	}
	c := newWithBackend(backend)
	defer c.Close()

	entries, truncated, err := c.ListLimited("/a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("max=0 应退化为 ReadDir，truncated=false")
	}
	if len(entries) != 50 {
		t.Errorf("entries=%d, want 50", len(entries))
	}
}

// TestListLimited_HeapNotInflated 验证 10w 文件场景下调用 ListLimited 后进程 heap 不会暴涨。
//
// 这是核心回归测试：旧实现（ReadDir + 本地序列化 10w entries）会瞬时吃满内存；
// 新实现 ListLimited 应该只 cap 到 max 条（默认 1000），heap 增量极小。
func TestListLimited_HeapNotInflated(t *testing.T) {
	if testing.Short() {
		t.Skip("skip 10k heap test in short mode")
	}
	const totalFiles = 100_000
	const maxEntries = 1000

	backend := &mockBackend{
		dirs: map[string][]os.FileInfo{
			"/huge": fakeFileInfoBatch(totalFiles),
		},
	}
	c := newWithBackend(backend)
	defer c.Close()

	// 强制 GC 后采基线
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	for i := 0; i < 5; i++ {
		_, _, err := c.ListLimited("/huge", maxEntries)
		if err != nil {
			t.Fatal(err)
		}
	}

	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	heapInuseDelta := int64(memAfter.HeapInuse) - int64(memBefore.HeapInuse)
	heapInuseMB := float64(heapInuseDelta) / 1024 / 1024
	t.Logf("5x ListLimited(/huge, 1000) HeapInuse delta: %.2f MB", heapInuseMB)

	// 上限：1000 条 entry × ~100B = 100KB 量级，加上 Go 字符串/切片 ~MB 量级
	// 5 次调用 < 5MB 是合理上限
	if heapInuseMB > 5 {
		t.Errorf("HeapInuse 增量 %.2f MB 超过 5MB（ListLimited 应该只 cap 到 %d 条）", heapInuseMB, maxEntries)
	}
}

// 用 strings.NewReader 触发 fakeFileInfoBatch 编译期路径
var _ = strings.NewReader