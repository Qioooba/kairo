package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRecentBigFile 验证 100k 条 / 23MB 的 audit.log 不会爆内存（v1.0 修复）。
//
// 设计动机：v0.13 旧实现是 "scanner 全文件读入 allLines []string"，
// 100k 行约 23MB 一口气吃进内存。fix #2 改成环形 buffer（最多 50k 行 ≈ 10MB），
// heap 增量应远低于文件大小。
//
// 验证方式：
//   1. 生成 100k 行 audit.log（~23MB）；
//   2. 调 l.Recent(5000, Filter{}) 多次，采 heap 增量；
//   3. 断言 heap 增量 < 30MB（远低于旧实现的 ~200MB）。
func TestRecentBigFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skip large file test in short mode")
	}
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.log")
	f, err := os.Create(auditPath)
	if err != nil {
		t.Fatal(err)
	}

	// 生成 10w 条记录（每条约 230B，总计 ~23MB）
	const n = 100_000
	for i := 0; i < n; i++ {
		fmt.Fprintf(f, `{"ts":"2026-07-01T10:%02d:%02d.%06dZ","op":"logs.search","system":"sysA","server":"srv-%d","dir":"/var/log/app","file":"app.log","result":"ok","count":%d}`+"\n",
			(i/60)%60, i%60, i%1000000, i%100, i)
	}
	f.Close()

	fi, _ := os.Stat(auditPath)
	t.Logf("audit.log size: %d KB (%d lines)", fi.Size()/1024, n)

	l, err := New(dir, "audit.log")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// 多次调用 Recent 采 heap 增量（确保稳态，不是一次性尖峰）。
	//
	// 用 HeapInuse 而不是 HeapAlloc：HeapAlloc 是"当前已用"，GC 后会减回
	// （环形 buffer 实现里 GC 会回收临时分配的 ring 副本），可能下溢；
	// HeapInuse 是"已从 OS 拿到但未还给 OS 的页"，更能反映真实峰值占用。
	var memBefore, memAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memBefore)

	for i := 0; i < 5; i++ {
		recs, err := l.Recent(5000, Filter{})
		if err != nil {
			t.Fatalf("Recent #%d: %v", i, err)
		}
		if len(recs) == 0 {
			t.Fatalf("Recent #%d: 0 records", i)
		}
	}

	runtime.GC()
	runtime.ReadMemStats(&memAfter)
	// 用 TotalAlloc 累计增量（不会被 GC 减回），更能反映实际内存压力
	totalAllocDelta := memAfter.TotalAlloc - memBefore.TotalAlloc
	heapInuseDelta := int64(memAfter.HeapInuse) - int64(memBefore.HeapInuse)
	heapInuseMB := float64(heapInuseDelta) / 1024 / 1024
	totalMB := float64(totalAllocDelta) / 1024 / 1024
	t.Logf("5x Recent(5000) HeapInuse delta: %.2f MB, TotalAlloc delta: %.2f MB",
		heapInuseMB, totalMB)

	// 旧实现：allLines []string 全文件入内存 ≈ 23MB+overhead
	// 新实现：环形 buffer ≤ 50k 行 ≈ 10MB；多次调用不增长（无累积）
	// 阈值 30MB 留 buffer 给临时分配
	if heapInuseMB > 30 {
		t.Errorf("HeapInuse 增量 %.2f MB 超过 30MB 上限（环形 buffer 应该 ≤ 10MB）", heapInuseMB)
	}
	if totalMB > 200 {
		// TotalAlloc 是累计分配字节，多次调用 + parse + 拷贝字符串会有一定开销
		// 5 次调用加起来不超过 200MB 是合理上限（旧实现一次性就吃 23MB）
		t.Errorf("TotalAlloc 增量 %.2f MB 超过 200MB 上限", totalMB)
	}
}

// TestRecentBigFile_BackwardCompat 验证行为正确：倒序、limit 命中、filter 命中。
func TestRecentBigFile_BackwardCompat(t *testing.T) {
	if testing.Short() {
		t.Skip("skip in short mode")
	}
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.log")
	f, err := os.Create(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	// 写 10w 行：偶数行 op=logs.search，奇数行 op=ssh.test
	for i := 0; i < 100_000; i++ {
		op := "logs.search"
		if i%2 == 1 {
			op = "ssh.test"
		}
		fmt.Fprintf(f, `{"ts":"2026-07-01T10:%02d:%02d:%02dZ","op":%q,"system":"sysA","server":"srv-1","result":"ok","count":%d}`+"\n",
			(i/3600)%24, (i/60)%60, i%60, op, i)
	}
	f.Close()

	l, _ := New(dir, "audit.log")
	defer l.Close()

	// 1. 倒序：最新 5 条应该是 i=99999, 99998, ..., 99995
	recs, _ := l.Recent(5, Filter{})
	if len(recs) != 5 {
		t.Fatalf("got %d, want 5", len(recs))
	}
	for i, r := range recs {
		want := fmt.Sprintf("%d", 99999-i)
		if r.KV["count"] != want {
			t.Errorf("rec[%d].count=%s, want %s", i, r.KV["count"], want)
		}
	}

	// 2. filter by op=ssh.test → 只返奇数 i 的行（最近 5 条奇数 i = 99999, 99997, ...）
	sshRecs, _ := l.Recent(5, Filter{Op: "ssh.test"})
	for _, r := range sshRecs {
		if r.Op != "ssh.test" {
			t.Errorf("filter 漏掉非 ssh.test: op=%s", r.Op)
		}
	}
}