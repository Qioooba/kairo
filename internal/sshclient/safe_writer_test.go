package sshclient

import (
	"strings"
	"testing"
)

// TestSafeWriter_UnderLimit 验证：累计写入 < 上限时正常落到 builder，没有 truncated marker。
func TestSafeWriter_UnderLimit(t *testing.T) {
	var b strings.Builder
	sw := &safeWriter{w: &b, max: 1024}
	data := []byte("hello world")
	n, err := sw.Write(data)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(data) {
		t.Fatalf("期望写 %d，实际 %d", len(data), n)
	}
	if got := b.String(); got != "hello world" {
		t.Fatalf("builder 内容错: %q", got)
	}
	if sw.truncated {
		t.Fatalf("未超过上限，不应 truncated")
	}
}

// TestSafeWriter_TruncatesAtLimit 验证：累计写入 == 上限时，第二次 Write 会触发 truncated marker。
func TestSafeWriter_TruncatesAtLimit(t *testing.T) {
	var b strings.Builder
	sw := &safeWriter{w: &b, max: 8}

	// 写满 8 字节
	if _, err := sw.Write([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if b.String() != "12345678" {
		t.Fatalf("写满 8 字节后 builder: %q", b.String())
	}

	// 再写 1 字节，触发 marker
	n, err := sw.Write([]byte("X"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("Write 应返回 len(p)=1，实际 %d", n)
	}
	if !strings.Contains(b.String(), "...[truncated]...") {
		t.Fatalf("应出现 truncated marker: %q", b.String())
	}
	if !sw.truncated {
		t.Fatal("truncated 标志应被设置")
	}
}

// TestSafeWriter_NoRepeatedMarker 回归测试（P0-5）：超过上限后，**后续多次 Write 不应重复追加 marker**。
//
// 旧实现里每次 Write 越界都写 "...[truncated]..."，
// 远端命令疯狂输出时 builder 会一直涨。
// 新实现用 truncated 标志保证 marker 只写一次。
func TestSafeWriter_NoRepeatedMarker(t *testing.T) {
	var b strings.Builder
	sw := &safeWriter{w: &b, max: 4}

	// 灌到上限
	_, _ = sw.Write([]byte("AAAA"))
	// 触发第一次 marker
	_, _ = sw.Write([]byte("BB"))
	firstMarkCount := strings.Count(b.String(), "...[truncated]...")
	if firstMarkCount != 1 {
		t.Fatalf("第一次 Write 后 marker 应恰好 1 次，实际 %d: %q", firstMarkCount, b.String())
	}

	// 再灌 100 次"更多内容"
	for i := 0; i < 100; i++ {
		_, _ = sw.Write([]byte("more"))
	}
	finalMarkCount := strings.Count(b.String(), "...[truncated]...")
	if finalMarkCount != 1 {
		t.Fatalf("100 次 Write 后 marker 仍应只有 1 次，实际 %d（说明 truncated 标志没生效）\nbuilder: %q", finalMarkCount, b.String())
	}
	// 确认 builder 没有无限增长
	if b.Len() > 200 {
		t.Fatalf("builder 长度异常：%d 字节（应 ≤ 一次 marker + 写满的字节）", b.Len())
	}
}

// TestSafeWriter_DefaultCap 验证：不指定 max 时走默认 8MB 上限。
//
// 这里不真灌 8MB，只验证默认值被采用（max=0 → 8MB）。
func TestSafeWriter_DefaultCap(t *testing.T) {
	var b strings.Builder
	sw := &safeWriter{w: &b} // max 不显式设
	if sw.max != 0 {
		t.Fatalf("max 应为 0（走默认）: %d", sw.max)
	}
	// 写 1KB 远低于默认上限
	_, err := sw.Write([]byte(strings.Repeat("x", 1024)))
	if err != nil {
		t.Fatal(err)
	}
	if sw.truncated {
		t.Fatal("远低于默认上限不应 truncated")
	}
	if b.Len() != 1024 {
		t.Fatalf("builder 应有 1024 字节，实际 %d", b.Len())
	}
}