package cronx

import (
	"strings"
	"testing"
	"time"
)

func TestParseAndMatch(t *testing.T) {
	s, err := Parse("0 9 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	if s.HasSeconds() {
		t.Error("5 段表达式应 hasSeconds=false")
	}
	// 2026-07-13 是周一
	mon := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)
	if !s.Match(mon) {
		t.Error("周一 09:00 应命中")
	}
	if !s.Match(mon.Add(24 * time.Hour)) {
		t.Error("周二 09:00 应命中（1-5 工作日）")
	}
	// 周日（2026-07-19）不应命中：日字段 * 与周字段 1-5 是 AND 语义
	if s.Match(mon.Add(6 * 24 * time.Hour)) {
		t.Error("周日 09:00 不应命中")
	}
	if s.Match(mon.Add(10 * time.Hour)) {
		t.Error("周一 19:00 不应命中")
	}
}

func TestParseSixFields(t *testing.T) {
	s, err := Parse("30 5 8 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if !s.HasSeconds() {
		t.Error("6 段表达式应 hasSeconds=true")
	}
	at := time.Date(2026, 7, 13, 8, 5, 30, 0, time.UTC)
	if !s.Match(at) {
		t.Error("08:05:30 应命中")
	}
	if s.Match(at.Add(time.Second)) {
		t.Error("08:05:31 不应命中")
	}
}

func TestParseErrors(t *testing.T) {
	invalid := []string{
		"",
		"* * * *",
		"60 * * * *",
		"* * * * * * *",
		"a b c d e",
		"*/x * * * *",
		"@every 5m",
		"0 0 32 * *",
		"0 0 0 13 *",
	}
	for _, expr := range invalid {
		if _, err := Parse(expr); err == nil {
			t.Errorf("%q 应解析失败", expr)
		}
	}
}

func TestDescriptorExpansion(t *testing.T) {
	cases := map[string]string{
		"@hourly":   "0 * * * *",
		"@daily":    "0 0 * * *",
		"@midnight": "0 0 * * *",
		"@weekly":   "0 0 * * 0",
		"@monthly":  "0 0 1 * *",
		"@yearly":   "0 0 1 1 *",
		"@annually": "0 0 1 1 *",
	}
	for expr, want := range cases {
		got := ExpandDescriptor(expr)
		if got != want {
			t.Errorf("%s: got %q, want %q", expr, got, want)
		}
		s, err := Parse(expr)
		if err != nil {
			t.Errorf("%s parse: %v", expr, err)
			continue
		}
		if s.HasSeconds() {
			t.Errorf("%s 展开应为 5 段", expr)
		}
	}
	if got := ExpandDescriptor("0 9 * * *"); got != "0 9 * * *" {
		t.Errorf("非描述符应原样返回: %q", got)
	}
}

// TestNextSparseCron 验证 BE-015 跳跃算法：稀疏 cron 也能快速算出下一次。
func TestNextSparseCron(t *testing.T) {
	s, err := Parse("0 0 0 1 1 *")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	want := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	got := s.Next(now)
	if !got.Equal(want) {
		t.Fatalf("Next(2026-07-13) = %v, want %v", got, want)
	}
	// 从命中点本身开始：含 from → 返回 from
	got = s.Next(want)
	if !got.Equal(want) {
		t.Errorf("Next(命中点) 应含 from: got %v, want %v", got, want)
	}
	// 过一秒 → 明年
	got = s.Next(want.Add(time.Second))
	want2 := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want2) {
		t.Errorf("Next(命中点+1s) = %v, want %v", got, want2)
	}
}

func TestNextEveryFiveMinutes(t *testing.T) {
	s, err := Parse("*/5 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 13, 9, 3, 0, 0, time.UTC)
	got := s.Next(now)
	want := time.Date(2026, 7, 13, 9, 5, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next = %v, want %v", got, want)
	}
}

func TestNextWeekdays(t *testing.T) {
	s, err := Parse("0 9 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	// 周日 10:00 → 周一 09:00
	now := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	got := s.Next(now)
	want := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next(周日) = %v, want %v", got, want)
	}
}

func TestDayOfWeekSevenNormalized(t *testing.T) {
	// 周字段 7 = 周日
	s, err := Parse("0 9 * * 7")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-07-12 是周日
	sun := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	if !s.Match(sun) {
		t.Error("周字段 7 应命中周日")
	}
}

func TestFieldsSnapshot(t *testing.T) {
	s, _ := Parse("0 9 * * 1-5")
	fields := s.Fields()
	if len(fields) != 6 || fields[0] != "0" {
		t.Errorf("Fields 应为 6 段且首段补 0: %v", fields)
	}
	if got := strings.Join(fields, " "); got != "0 0 9 * * 1-5" {
		t.Errorf("Fields 顺序错误: %q", got)
	}
}
