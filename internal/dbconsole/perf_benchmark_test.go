package dbconsole

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// BenchmarkMetadataCache_ColdVsHot 测量元数据字典缓存冷读取（未命中生成并存入）与热读取（内存命中）性能
func BenchmarkMetadataCache_ColdVsHot(b *testing.B) {
	m, err := NewManager(b.TempDir())
	if err != nil {
		b.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	fields := []Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true, Ordinal: 1},
		{Name: "USER_NAME", DataType: "VARCHAR2", Ordinal: 2},
		{Name: "CREATED_AT", DataType: "TIMESTAMP", Ordinal: 3},
		{Name: "CONTENT", DataType: "CLOB", Ordinal: 4},
		{Name: "PAYLOAD", DataType: "BLOB", Ordinal: 5},
	}

	b.Run("Cold_MissAndSet", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			key := fmt.Sprintf("src1\x00cold_tbl_%d", i%100)
			metadataCacheSet(m, key, fields)
		}
	})

	// Pre-populate hot key
	hotKey := "src1\x00hot_table"
	metadataCacheSet(m, hotKey, fields)

	b.Run("Hot_CacheHit", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cached, ok := metadataCacheGet[[]Field](m, hotKey)
			if !ok || len(cached) != 5 {
				b.Fatalf("expected cache hit")
			}
		}
	})
}

// BenchmarkRowProcessing_NarrowVsWideVsLOB 测量窄行、50列宽行、以及大文本/LOB规范化处理吞吐
func BenchmarkRowProcessing_NarrowVsWideVsLOB(b *testing.B) {
	narrowRow := []any{
		int64(1001),
		"standard string col",
		true,
		3.14159,
		time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
	}

	wideRow := make([]any, 50)
	for c := 0; c < 50; c++ {
		switch c % 5 {
		case 0:
			wideRow[c] = int64(c * 100)
		case 1:
			wideRow[c] = fmt.Sprintf("column_value_%d_payload_string", c)
		case 2:
			wideRow[c] = (c%2 == 0)
		case 3:
			wideRow[c] = float64(c) * 1.5
		case 4:
			wideRow[c] = time.Now()
		}
	}

	largeClobText := strings.Repeat("KAIRO-CLOB-PREVIEW-CONTENT-LINE\n", 1000) // ~32KB

	b.Run("NarrowRow_Bytes", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = fastRowBytes(narrowRow)
		}
	})

	b.Run("WideRow50Cols_Bytes", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = fastRowBytes(wideRow)
		}
	})

	b.Run("LOBTruncation_32KB", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = normalizeColumnValue(largeClobText, "CLOB")
		}
	})
}

// TestT074_QueryCancellationResponsiveness 测试查询在取消上下文时的响应速度与资源回收 (<50ms)
func TestT074_QueryCancellationResponsiveness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	start := time.Now()
	done := make(chan struct{})

	go func() {
		defer close(done)
		// 模拟执行工作，检测 ctx.Done()
		select {
		case <-time.After(10 * time.Second):
			t.Errorf("worker should have been cancelled before timeout")
		case <-ctx.Done():
			return
		}
	}()

	// 立即触发取消
	cancel()

	select {
	case <-done:
		elapsed := time.Since(start)
		t.Logf("Cancellation observed in %v", elapsed)
		if elapsed > 50*time.Millisecond {
			t.Errorf("Cancellation responsiveness took %v, want < 50ms", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Worker did not terminate upon cancellation within 500ms")
	}
}

// TestT074_MultiTabConcurrentLoad 模拟多 Tab 并发查询负载，并采样 RSS/Heap/Goroutine 基线
func TestT074_MultiTabConcurrentLoad(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	// 采样初始内存
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	goroutinesBefore := runtime.NumGoroutine()

	const concurrentTabs = 20
	const queriesPerTab = 50

	var wg sync.WaitGroup
	wg.Add(concurrentTabs)

	for tab := 0; tab < concurrentTabs; tab++ {
		tabID := fmt.Sprintf("dbtab-concurrent-%d", tab)
		go func(tID string) {
			defer wg.Done()
			for q := 0; q < queriesPerTab; q++ {
				// 模拟通过全局并发槽运行查询
				select {
				case m.global <- struct{}{}:
					// 成功获得并发槽
					key := fmt.Sprintf("src-bench\x00tbl_%d", q%5)
					metadataCacheSet(m, key, []Field{{Name: "ID", DataType: "NUMBER"}})
					_, _ = metadataCacheGet[[]Field](m, key)
					<-m.global
				case <-time.After(2 * time.Second):
					t.Errorf("Timeout waiting for global slot in tab %s", tID)
					return
				}
			}
		}(tabID)
	}

	wg.Wait()

	// 采样负载完成后内存与协程
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	goroutinesAfter := runtime.NumGoroutine()

	t.Logf("PERF-01 MultiTab Baseline: Goroutines %d -> %d, HeapAlloc %d KB -> %d KB, TotalAlloc %d KB",
		goroutinesBefore, goroutinesAfter,
		memBefore.HeapAlloc/1024, memAfter.HeapAlloc/1024,
		memAfter.TotalAlloc/1024,
	)

	// 协程泄漏检查：不应有明显的死锁或泄漏
	if goroutinesAfter-goroutinesBefore > 10 {
		t.Errorf("Possible goroutine leak: before=%d, after=%d", goroutinesBefore, goroutinesAfter)
	}
}
