package pet

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kairo/internal/audit"
)

// newTestEngine 构造测试引擎：dataPath 指向 t.TempDir() 下的 pet.json，
// 使用固定 32 字节测试密钥（不生成 .petkey，测试可精确断言磁盘状态）。
func newTestEngine(t *testing.T, rules Rules) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pet.json")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	e, err := NewEngine(rules, path, key)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e, path
}

// mustEnable 解锁测试引擎（失败直接 fatal）。
func mustEnable(t *testing.T, e *Engine) {
	t.Helper()
	if _, err := e.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
}

// feed 向引擎喂 n 次 op（带可变的 system target，避开冷却干扰）。
func feed(t *testing.T, e *Engine, op string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		e.OnOp(audit.OpEvent{
			Op:     op,
			Fields: []any{"system", fmt.Sprintf("srv-%d", i)},
		})
	}
}

// TestOnOp_NoopGate 解锁前 OnOp 必须零记录：状态不变、不落盘。
func TestOnOp_NoopGate(t *testing.T) {
	e, path := newTestEngine(t, DefaultRules())
	// 解锁前喂 3 次
	feed(t, e, "ssh.shell.start", 3)

	st := e.State()
	if st.TotalEarned != 0 || st.Level != 1 || st.Exp != 0 {
		t.Fatalf("解锁前状态应不变, 实际 TotalEarned=%d Level=%d Exp=%d", st.TotalEarned, st.Level, st.Exp)
	}
	if len(st.Ledger) != 0 {
		t.Fatalf("解锁前流水应为空, 实际 %d 条", len(st.Ledger))
	}
	if len(st.Stats.Total) != 0 {
		t.Fatalf("解锁前统计应为空, 实际 %+v", st.Stats.Total)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("解锁前不应创建 pet.json, stat err=%v", err)
	}
}

// TestNextExp 成长曲线固定值（100 * level^1.5）。
func TestNextExp(t *testing.T) {
	cases := []struct {
		level int
		want  int64
	}{
		{1, 100},
		{2, 283},
		{3, 520},
		{4, 800},
		{5, 1118},
		{6, 1470},
	}
	for _, c := range cases {
		if got := NextExp(c.level); got != c.want {
			t.Fatalf("NextExp(%d) = %d, 期望 %d", c.level, got, c.want)
		}
	}
	// 防御：非法等级兜底为 1
	if NextExp(0) != NextExp(1) {
		t.Fatal("NextExp(0) 应兜底为 NextExp(1)")
	}
}

// TestStageForLevel 阶段区间映射（含边界）。
func TestStageForLevel(t *testing.T) {
	rules := DefaultRules()
	cases := []struct {
		level int
		want  string
	}{
		{1, "egg"},
		{5, "egg"},
		{6, "hatchling"},
		{15, "hatchling"},
		{16, "grown"},
		{30, "grown"},
		{31, "mythic"},
		{999, "mythic"},
		{1000, "egg"}, // 未匹配默认 egg
	}
	for _, c := range cases {
		if got := stageForLevel(rules.StageLevels, c.level); got != c.want {
			t.Fatalf("stageForLevel(%d) = %q, 期望 %q", c.level, got, c.want)
		}
	}
}

// TestOnOp_ExpAndLevelUp 经验累计 + 升级曲线 + 阶段进化（egg→hatchling→grown→mythic）。
// 用 1 经验的自定义 op 精确跨过每级阈值，避免 5 经验粒度跳级造成断言不稳。
func TestOnOp_ExpAndLevelUp(t *testing.T) {
	rules := DefaultRules()
	rules.DailyCap = 999999
	rules.CooldownMinutes = 0
	rules.MaxLedger = 16 // 流水上限调小，避免长循环里反复拷贝 2000 条
	rules.ExpRules["test.points"] = 1

	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	// 从 level from 升到 level to 所需总经验
	needFor := func(from, to int) int64 {
		var sum int64
		for l := from; l < to; l++ {
			sum += NextExp(l)
		}
		return sum
	}

	checkLevel := func(target int, stage string) {
		t.Helper()
		st := e.State()
		if st.Level != target {
			t.Fatalf("期望 L%d, 实际 L%d（TotalEarned=%d）", target, st.Level, st.TotalEarned)
		}
		if st.Exp != 0 {
			t.Fatalf("精确跨级后当前级经验应为 0, 实际 %d", st.Exp)
		}
		if st.Stage != stage {
			t.Fatalf("L%d 阶段应为 %q, 实际 %q", target, stage, st.Stage)
		}
		// 不变量：当前级经验永远小于下一级阈值
		if st.Exp >= NextExp(st.Level) {
			t.Fatalf("不变量破坏: Exp=%d >= NextExp(%d)=%d", st.Exp, st.Level, NextExp(st.Level))
		}
	}

	feed(t, e, "test.points", int(needFor(1, 6)))
	checkLevel(6, "hatchling")

	feed(t, e, "test.points", int(needFor(6, 16)))
	checkLevel(16, "grown")

	feed(t, e, "test.points", int(needFor(16, 31)))
	checkLevel(31, "mythic")

	if st := e.State(); st.TotalEarned != needFor(1, 31) {
		t.Fatalf("TotalEarned = %d, 期望 %d", st.TotalEarned, needFor(1, 31))
	}
}

// TestOnOp_Cooldown 同 op+target 在冷却窗口内只计首次。
func TestOnOp_Cooldown(t *testing.T) {
	rules := DefaultRules() // CooldownMinutes=60
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	ev := func(target string) audit.OpEvent {
		return audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", target}}
	}
	e.OnOp(ev("srv1"))
	e.OnOp(ev("srv1")) // 冷却内：0 分
	e.OnOp(ev("srv2")) // 不同 target：正常计分

	st := e.State()
	if st.TotalEarned != 10 {
		t.Fatalf("TotalEarned = %d, 期望 10（5+0+5）", st.TotalEarned)
	}
	if len(st.Ledger) != 2 {
		t.Fatalf("流水应只有 2 条, 实际 %d", len(st.Ledger))
	}
}

// TestOnOp_DailyCap 每日上限：到顶后不再计分。
func TestOnOp_DailyCap(t *testing.T) {
	rules := DefaultRules()
	rules.DailyCap = 10
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	// 两个不同 target 的 ssh.shell.start（各 5 分）→ 恰好 10
	e.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv1"}})
	e.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv2"}})
	// 上限已到：不同 op 也不应计分
	e.OnOp(audit.OpEvent{Op: "ssh.sftp.upload", Fields: []any{"system", "srv3"}})

	st := e.State()
	if st.TotalEarned != 10 {
		t.Fatalf("TotalEarned = %d, 期望 10", st.TotalEarned)
	}
}

// TestOnOp_OpDailyMax 单 op 每日频率上限。
func TestOnOp_OpDailyMax(t *testing.T) {
	rules := DefaultRules()
	rules.DailyCap = 999999
	rules.CooldownMinutes = 0 // 关冷却，只测频率上限
	rules.OpDailyMax = map[string]int64{"ssh.shell.start": 1}

	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	e.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv1"}})
	e.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv2"}}) // 超出频率上限：0
	e.OnOp(audit.OpEvent{Op: "ssh.sftp.upload", Fields: []any{"system", "srv1"}})

	st := e.State()
	if st.TotalEarned != 7 {
		t.Fatalf("TotalEarned = %d, 期望 7（5+0+2）", st.TotalEarned)
	}
}

// TestOnOp_SessionTime 会话时长奖励：21 分钟 → +2（每 10 分钟 +1，封顶 5）。
func TestOnOp_SessionTime(t *testing.T) {
	rules := DefaultRules() // SessionExpMinutes=10
	rules.DailyCap = 999999
	rules.CooldownMinutes = 0

	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	// start：正常路径 +5 并记录会话
	e.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv1"}})

	// 白盒：把会话开始时间拨回 21 分钟前（引擎内部用 time.Now，测试直接改 sessions 表）
	e.mu.Lock()
	e.sessions["srv1"] = time.Now().Add(-21 * time.Minute)
	e.mu.Unlock()

	e.OnOp(audit.OpEvent{Op: "ssh.shell.end", Fields: []any{"system", "srv1"}})

	st := e.State()
	if st.TotalEarned != 7 {
		t.Fatalf("TotalEarned = %d, 期望 7（start 5 + session 2）", st.TotalEarned)
	}
	if len(st.Ledger) != 2 {
		t.Fatalf("流水应 2 条, 实际 %d: %+v", len(st.Ledger), st.Ledger)
	}
	if st.Ledger[1].Op != "ssh.session.time" || st.Ledger[1].Exp != 2 {
		t.Fatalf("第二条流水应为 ssh.session.time +2, 实际 %+v", st.Ledger[1])
	}
	// 会话表已清理
	if len(e.sessions) != 0 {
		t.Fatalf("会话结算后应删除, 实际剩 %d", len(e.sessions))
	}
}

// TestOnOp_SessionTimeCap 会话时长奖励封顶 5。
func TestOnOp_SessionTimeCap(t *testing.T) {
	rules := DefaultRules()
	rules.DailyCap = 999999
	rules.CooldownMinutes = 0

	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	e.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv1"}})
	e.mu.Lock()
	e.sessions["srv1"] = time.Now().Add(-120 * time.Minute) // 120/10=12 → 封顶 5
	e.mu.Unlock()
	e.OnOp(audit.OpEvent{Op: "ssh.shell.end", Fields: []any{"system", "srv1"}})

	st := e.State()
	if st.TotalEarned != 10 {
		t.Fatalf("TotalEarned = %d, 期望 10（start 5 + session 封顶 5）", st.TotalEarned)
	}
}

// TestOnOp_UnknownOp 白名单外的 op 一律 0 分。
func TestOnOp_UnknownOp(t *testing.T) {
	e, _ := newTestEngine(t, DefaultRules())
	mustEnable(t, e)

	feed(t, e, "files.list", 3) // 高频易刷 op，不在白名单
	st := e.State()
	if st.TotalEarned != 0 {
		t.Fatalf("白名单外 op 应为 0 分, 实际 %d", st.TotalEarned)
	}
}

// TestOnOp_NoTargetFields 无 system/host 字段时用空 target，不 panic、正常计分。
func TestOnOp_NoTargetFields(t *testing.T) {
	rules := DefaultRules()
	rules.CooldownMinutes = 0
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	e.OnOp(audit.OpEvent{Op: "ssh.shell.start"})
	e.OnOp(audit.OpEvent{Op: "ssh.shell.start"}) // 无 target：冷却关闭时同样计分

	if st := e.State(); st.TotalEarned != 10 {
		t.Fatalf("TotalEarned = %d, 期望 10", st.TotalEarned)
	}
}

// TestOnOp_Concurrent 并发喂 op 不应数据竞争或丢计数。
func TestOnOp_Concurrent(t *testing.T) {
	rules := DefaultRules()
	rules.DailyCap = 999999
	rules.CooldownMinutes = 0
	rules.MaxLedger = 64
	rules.OpDailyMax = map[string]int64{}

	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	const goroutines = 8
	const per = 100
	done := make(chan struct{}, goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < per; i++ {
				e.OnOp(audit.OpEvent{
					Op:     "ssh.shell.start",
					Fields: []any{"system", fmt.Sprintf("g%d-srv%d", id, i)},
				})
			}
		}(g)
	}
	for g := 0; g < goroutines; g++ {
		<-done
	}
	st := e.State()
	if want := int64(goroutines * per * 5); st.TotalEarned != want {
		t.Fatalf("TotalEarned = %d, 期望 %d", st.TotalEarned, want)
	}
}
