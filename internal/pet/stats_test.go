package pet

import (
	"testing"
	"time"

	"kairo/internal/audit"
)

// TestStats_Bump 三层统计（Total / Daily / Monthly）正确累计。
func TestStats_Bump(t *testing.T) {
	rules := DefaultRules()
	rules.CooldownMinutes = 0
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	feed(t, e, "ssh.shell.start", 2) // 各 5 分

	day := time.Now().Format("2006-01-02")
	month := time.Now().Format("2006-01")

	st := e.State()
	// Total
	total := st.Stats.Total["ssh.shell.start"]
	if total.Count != 2 || total.Exp != 10 {
		t.Fatalf("Total = %+v, 期望 {2,10}", total)
	}
	// Daily
	daily := st.Stats.Daily[day]["ssh.shell.start"]
	if daily.Count != 2 || daily.Exp != 10 {
		t.Fatalf("Daily[%s] = %+v, 期望 {2,10}", day, daily)
	}
	// Monthly
	monthly := st.Stats.Monthly[month]["ssh.shell.start"]
	if monthly.Count != 2 || monthly.Exp != 10 {
		t.Fatalf("Monthly[%s] = %+v, 期望 {2,10}", month, monthly)
	}
}

// TestStats_MultiOp 不同 op 分开统计。
func TestStats_MultiOp(t *testing.T) {
	rules := DefaultRules()
	rules.CooldownMinutes = 0
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	e.OnOp(evOp("ssh.shell.start", "srv1")) // +5
	e.OnOp(evOp("ssh.sftp.upload", "srv1")) // +2

	day := time.Now().Format("2006-01-02")
	st := e.State()
	if st.Stats.Daily[day]["ssh.shell.start"].Exp != 5 {
		t.Fatalf("start exp = %d, 期望 5", st.Stats.Daily[day]["ssh.shell.start"].Exp)
	}
	if st.Stats.Daily[day]["ssh.sftp.upload"].Exp != 2 {
		t.Fatalf("upload exp = %d, 期望 2", st.Stats.Daily[day]["ssh.sftp.upload"].Exp)
	}
}

// TestStats_Prune 超出保留窗口的旧条目被清理（key 字典序即时间序）。
func TestStats_Prune(t *testing.T) {
	rules := DefaultRules()
	rules.CooldownMinutes = 0
	rules.StatsKeepDays = 1
	rules.StatsKeepMonths = 1
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	today := time.Now().Format("2006-01-02")
	thisMonth := time.Now().Format("2006-01")

	// 白盒注入过期条目
	e.mu.Lock()
	e.state.Stats.Daily["2000-01-01"] = map[string]OpStat{"ssh.shell.start": {Count: 9, Exp: 99}}
	e.state.Stats.Daily["2020-06-01"] = map[string]OpStat{"ssh.shell.start": {Count: 8, Exp: 88}}
	e.state.Stats.Monthly["2000-01"] = map[string]OpStat{"ssh.shell.start": {Count: 7, Exp: 77}}
	e.mu.Unlock()

	// 触发一次计分 → 顺带 prune
	feed(t, e, "ssh.shell.start", 1)

	st := e.State()
	if _, ok := st.Stats.Daily["2000-01-01"]; ok {
		t.Fatal("过期 Daily 条目应被清理")
	}
	if _, ok := st.Stats.Daily["2020-06-01"]; ok {
		t.Fatal("过期 Daily 条目应被清理")
	}
	if _, ok := st.Stats.Monthly["2000-01"]; ok {
		t.Fatal("过期 Monthly 条目应被清理")
	}
	if _, ok := st.Stats.Daily[today]; !ok {
		t.Fatal("今天的 Daily 条目不应被清理")
	}
	if _, ok := st.Stats.Monthly[thisMonth]; !ok {
		t.Fatal("本月的 Monthly 条目不应被清理")
	}
}

// TestStats_PruneBoundary 恰好等于保留窗口边界的条目保留。
func TestStats_PruneBoundary(t *testing.T) {
	rules := DefaultRules()
	rules.CooldownMinutes = 0
	rules.StatsKeepDays = 1
	rules.StatsKeepMonths = 1
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	// 昨天（恰好 keep=1 的边界内）与上个月
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	lastMonth := time.Now().AddDate(0, -1, 0).Format("2006-01")

	e.mu.Lock()
	e.state.Stats.Daily[yesterday] = map[string]OpStat{"ssh.shell.start": {Count: 1, Exp: 5}}
	e.state.Stats.Monthly[lastMonth] = map[string]OpStat{"ssh.shell.start": {Count: 1, Exp: 5}}
	e.mu.Unlock()

	feed(t, e, "ssh.shell.start", 1)

	st := e.State()
	if _, ok := st.Stats.Daily[yesterday]; !ok {
		t.Fatal("保留窗口边界内的 Daily 条目不应被清理")
	}
	if _, ok := st.Stats.Monthly[lastMonth]; !ok {
		t.Fatal("保留窗口边界内的 Monthly 条目不应被清理")
	}
}

// TestStats_DisabledNotRecorded 未解锁不记录统计（配合 no-op 快门）。
func TestStats_DisabledNotRecorded(t *testing.T) {
	e, _ := newTestEngine(t, DefaultRules())
	feed(t, e, "ssh.shell.start", 2)
	st := e.State()
	if len(st.Stats.Total) != 0 || len(st.Stats.Daily) != 0 || len(st.Stats.Monthly) != 0 {
		t.Fatalf("未解锁不应有统计: %+v", st.Stats)
	}
}

// evOp 构造带 system 字段的审计事件（测试辅助）。
func evOp(op, system string) audit.OpEvent {
	return audit.OpEvent{Op: op, Fields: []any{"system", system}}
}
