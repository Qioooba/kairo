package httpserver

import (
	"testing"
	"time"
)

// TestBE015_SparseCronReturnsFiveRuns 验证 BE-015：稀疏 cron 表达式
// `0 0 0 1 1 *`（每年 1 月 1 日 00:00 执行一次）的 next_runs 必须返回 5 条。
// 旧版暴力遍历上限 200 万分钟≈3.8 年，5 次≈5 年会超限返回不足 5 条。
func TestBE015_SparseCronReturnsFiveRuns(t *testing.T) {
	loc := time.UTC
	res := parseCronPreview("0 0 0 1 1 *", loc)
	if res["valid"] != true {
		t.Fatalf("expected valid=true, got %v; error=%v", res["valid"], res["error"])
	}
	nextRuns, ok := res["next_runs"].([]map[string]any)
	if !ok {
		t.Fatalf("next_runs 类型错误: %T", res["next_runs"])
	}
	if len(nextRuns) != 5 {
		t.Fatalf("BE-015: 稀疏 cron next_runs 应返回 5 条，实际 %d 条", len(nextRuns))
	}
	// 验证每条都是 1 月 1 日 00:00:00
	for i, r := range nextRuns {
		cn, _ := r["cn"].(string)
		if cn == "" {
			t.Errorf("next_runs[%d].cn 为空", i)
			continue
		}
		pt, err := time.Parse("2006-01-02 15:04:05 MST", cn)
		if err != nil {
			t.Errorf("next_runs[%d] 时间解析失败 %q: %v", i, cn, err)
			continue
		}
		if pt.Month() != time.January || pt.Day() != 1 || pt.Hour() != 0 || pt.Minute() != 0 {
			t.Errorf("next_runs[%d]=%q 不是 1月1日00:00", i, cn)
		}
	}
}

// TestBE015_NormalCronUnchanged 验证常见 cron 表达式行为不变。
func TestBE015_NormalCronUnchanged(t *testing.T) {
	loc := time.UTC
	cases := []string{
		"0 * * * *",   // 每小时
		"*/5 * * * *", // 每 5 分钟
		"0 0 * * *",   // 每天 00:00
		"0 0 * * 0",   // 每周日 00:00
	}
	for _, expr := range cases {
		res := parseCronPreview(expr, loc)
		if res["valid"] != true {
			t.Errorf("%q valid=false, error=%v", expr, res["error"])
			continue
		}
		next, _ := res["next_runs"].([]map[string]any)
		if len(next) != 5 {
			t.Errorf("%q next_runs 应 5 条，实际 %d", expr, len(next))
		}
		prev, _ := res["prev_runs"].([]map[string]any)
		if len(prev) != 3 {
			t.Errorf("%q prev_runs 应 3 条，实际 %d", expr, len(prev))
		}
	}
}
