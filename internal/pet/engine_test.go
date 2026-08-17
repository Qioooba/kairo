package pet

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"kairo/internal/audit"
)

// newTestEngine 构造测试引擎：dataPath 指向 t.TempDir() 下的 pet.json，
// 使用固定 32 字节测试密钥（不生成 .petkey，测试可精确断言磁盘状态）。
// skinsJSON 传 nil（退回内置最小清单），需要真实清单的测试自行传入。
func newTestEngine(t *testing.T, rules Rules) (*Engine, string) {
	return newTestEngineSkins(t, rules, nil)
}

// newTestEngineSkins 同 newTestEngine，但允许指定 skins.json 内容。
func newTestEngineSkins(t *testing.T, rules Rules, skinsJSON []byte) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pet.json")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	e, err := NewEngine(rules, path, key, skinsJSON)
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

// feedSeq feed 的全局序号：保证跨调用 target 也不重复（避开 2s 频次检测）。
var feedSeq int64

// feed 向引擎喂 n 次 op（带可变的 system target，避开冷却/频次检测干扰）。
func feed(t *testing.T, e *Engine, op string, n int) {
	t.Helper()
	seq := atomic.AddInt64(&feedSeq, 1)
	for i := 0; i < n; i++ {
		e.OnOp(audit.OpEvent{
			Op:     op,
			Fields: []any{"system", fmt.Sprintf("srv-%d-%d", seq, i)},
		})
	}
}

// rewindKey 白盒回拨某 key 的冷却与频次时间（模拟时间流逝，绕开真实等待）。
func rewindKey(e *Engine, key string, d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	past := time.Now().Add(-d)
	e.cooldowns[key] = past
	e.lastAward[key] = past
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

// TestNextExp 成长曲线固定值（80 * level^1.2，等级无上限）。
func TestNextExp(t *testing.T) {
	cases := []struct {
		level int
		want  int64
	}{
		{1, 80},
		{2, 184},
		{3, 299},
		{4, 422},
		{5, 552},
		{6, 687},
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
	// 等级无上限：超大等级也能出值（不 panic、单调递增）
	if NextExp(100000) <= NextExp(99999) {
		t.Fatal("曲线应单调递增")
	}
}

// TestStageForLevel 阶段区间映射（含边界）：egg 1-3 / hatchling 4-8 /
// grown 9-15 / mythic 16+（无上限）。
func TestStageForLevel(t *testing.T) {
	rules := DefaultRules()
	cases := []struct {
		level int
		want  string
	}{
		{1, "egg"},
		{3, "egg"},
		{4, "hatchling"},
		{8, "hatchling"},
		{9, "grown"},
		{15, "grown"},
		{16, "mythic"},
		{31, "mythic"},
		{999, "mythic"},
		{999999, "mythic"}, // 等级无上限，mythic 覆盖到 MaxInt32
	}
	for _, c := range cases {
		if got := stageForLevel(rules.StageLevels, c.level); got != c.want {
			t.Fatalf("stageForLevel(%d) = %q, 期望 %q", c.level, got, c.want)
		}
	}
	// 自定义区间未覆盖的等级回退 egg
	if got := stageForLevel(map[string][2]int{"a": {1, 2}}, 5); got != "egg" {
		t.Fatalf("未覆盖等级应回退 egg, 实际 %q", got)
	}
}

// TestOnOp_ExpAndLevelUp 经验累计 + 升级曲线 + 阶段进化（egg→hatchling→grown→mythic）。
// 用 1 经验的自定义 op 精确跨过每级阈值，避免多经验粒度跳级造成断言不稳。
func TestOnOp_ExpAndLevelUp(t *testing.T) {
	rules := DefaultRules()
	rules.DailyCap = 999999
	rules.CooldownMinutes = 0
	rules.MaxLedger = 16 // 流水上限调小，避免长循环里反复拷贝 2000 条
	rules.ExpRules = map[string]int64{"test.points": 1}
	rules.OpDailyMax = map[string]int64{}

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

	feed(t, e, "test.points", int(needFor(1, 4)))
	checkLevel(4, "hatchling")

	feed(t, e, "test.points", int(needFor(4, 9)))
	checkLevel(9, "grown")

	feed(t, e, "test.points", int(needFor(9, 16)))
	checkLevel(16, "mythic")

	if st := e.State(); st.TotalEarned != needFor(1, 16) {
		t.Fatalf("TotalEarned = %d, 期望 %d", st.TotalEarned, needFor(1, 16))
	}
}

// TestOnOp_Cooldown 同 op+target 在冷却窗口内只计首次（默认 30min）。
// 白盒回拨时间模拟流逝：29min 仍在窗口内（丢弃），31min 超窗（放行）。
func TestOnOp_Cooldown(t *testing.T) {
	rules := DefaultRules() // CooldownMinutes=30
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	ev := func(target string) audit.OpEvent {
		return audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", target}}
	}
	e.OnOp(ev("srv1")) // +6
	rewindKey(e, "ssh.shell.start|srv1", 29*time.Minute)
	e.OnOp(ev("srv1")) // 冷却内：0 分
	e.OnOp(ev("srv2")) // 不同 target：正常计分 +6

	st := e.State()
	if st.TotalEarned != 12 {
		t.Fatalf("TotalEarned = %d, 期望 12（6+0+6）", st.TotalEarned)
	}
	if len(st.Ledger) != 2 {
		t.Fatalf("流水应只有 2 条, 实际 %d", len(st.Ledger))
	}

	// 超窗放行
	rewindKey(e, "ssh.shell.start|srv2", 31*time.Minute)
	e.OnOp(ev("srv2")) // +6
	if st := e.State(); st.TotalEarned != 18 {
		t.Fatalf("超窗后应计分, TotalEarned = %d, 期望 18", st.TotalEarned)
	}
}

// TestOnOp_DailyCap 每日上限：到顶后不再计分（计分原子，最后一笔可越线）。
func TestOnOp_DailyCap(t *testing.T) {
	rules := DefaultRules()
	rules.DailyCap = 10
	rules.CooldownMinutes = 0
	rules.OpDailyMax = map[string]int64{}
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	// 两个不同 target 的 ssh.shell.start（各 6 分）：第二笔后 todayEarned=12 > cap=10
	e.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv1"}})
	e.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv2"}})
	// 上限已到：不同 op 也不应计分
	e.OnOp(audit.OpEvent{Op: "ssh.sftp.upload", Fields: []any{"system", "srv3"}})

	st := e.State()
	if st.TotalEarned != 12 {
		t.Fatalf("TotalEarned = %d, 期望 12（6+6，第三笔被日上限拦截）", st.TotalEarned)
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
	e.OnOp(audit.OpEvent{Op: "ssh.sftp.upload", Fields: []any{"system", "srv1"}}) // 不受限：+2

	st := e.State()
	if st.TotalEarned != 8 {
		t.Fatalf("TotalEarned = %d, 期望 8（6+0+2）", st.TotalEarned)
	}
}

// TestAward_RapidRepeat 防刷层 1：同 op+target 距上次计分 < 2s 判定脚本行为，
// 丢弃并重置冷却（惩罚窗口重新起算）。
func TestAward_RapidRepeat(t *testing.T) {
	rules := DefaultRules()
	rules.DailyCap = 999999
	rules.CooldownMinutes = 0
	rules.OpDailyMax = map[string]int64{}

	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	ev := audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv1"}}
	e.OnOp(ev) // +6
	e.OnOp(ev) // 2s 内重复：丢弃（尽管冷却已关）
	e.OnOp(ev) // 仍然 2s 内：继续丢弃

	if st := e.State(); st.TotalEarned != 6 {
		t.Fatalf("TotalEarned = %d, 期望 6（频次异常只计首次）", st.TotalEarned)
	}

	// 回拨 lastAward 到 3s 前（> 2s 窗口）：恢复计分
	rewindKey(e, "ssh.shell.start|srv1", 3*time.Second)
	e.OnOp(ev)
	if st := e.State(); st.TotalEarned != 12 {
		t.Fatalf("超频次窗口后应恢复计分, TotalEarned = %d, 期望 12", st.TotalEarned)
	}
}

// TestAward_DynamicCooldown 防刷层 2 动态冷却：单 op 当日用量 ≥60% 上限窗口×2、
// ≥85% 窗口×4。白盒设置 dailyOpCounts 与回拨冷却时间验证三档窗口。
func TestAward_DynamicCooldown(t *testing.T) {
	rules := DefaultRules() // CooldownMinutes=30
	rules.DailyCap = 999999
	rules.OpDailyMax = map[string]int64{"ssh.shell.start": 10}

	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	ev := audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv1"}}
	key := "ssh.shell.start|srv1"
	e.OnOp(ev) // +6（基础窗口 30min）

	setUsed := func(n int64) {
		e.mu.Lock()
		e.dailyOpCounts["ssh.shell.start"] = n
		e.mu.Unlock()
	}

	// 用量 6/10（≥60%）：窗口 60min。冷却已过基础 30min 但未过 60min → 拦截
	setUsed(6)
	rewindKey(e, key, 40*time.Minute)
	e.OnOp(ev)

	// 用量 9/10（≥85%）：窗口 120min。冷却已过 60min 但未过 120min → 拦截
	setUsed(9)
	rewindKey(e, key, 70*time.Minute)
	e.OnOp(ev)

	if st := e.State(); st.TotalEarned != 6 {
		t.Fatalf("动态冷却应拦截, TotalEarned = %d, 期望 6", st.TotalEarned)
	}

	// 用量回 0：窗口还原 30min。冷却已过 70min → 放行
	setUsed(0)
	rewindKey(e, key, 70*time.Minute)
	e.OnOp(ev)
	if st := e.State(); st.TotalEarned != 12 {
		t.Fatalf("低用量应按基础窗口放行, TotalEarned = %d, 期望 12", st.TotalEarned)
	}
}

// TestOnOp_SessionTime 会话时长奖励：21 分钟 → +2（每 10 分钟 +1，封顶 8）。
func TestOnOp_SessionTime(t *testing.T) {
	rules := DefaultRules() // SessionExpMinutes=10
	rules.DailyCap = 999999
	rules.CooldownMinutes = 0
	rules.OpDailyMax = map[string]int64{}

	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	// start：正常路径 +6 并记录会话
	e.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv1"}})

	// 白盒：把会话开始时间拨回 21 分钟前（引擎内部用 time.Now，测试直接改 sessions 表）
	e.mu.Lock()
	e.sessions["srv1"] = time.Now().Add(-21 * time.Minute)
	e.mu.Unlock()

	e.OnOp(audit.OpEvent{Op: "ssh.shell.end", Fields: []any{"system", "srv1"}})

	st := e.State()
	if st.TotalEarned != 8 {
		t.Fatalf("TotalEarned = %d, 期望 8（start 6 + session 2）", st.TotalEarned)
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

// TestOnOp_SessionTimeCap 会话时长奖励封顶 8。
func TestOnOp_SessionTimeCap(t *testing.T) {
	rules := DefaultRules()
	rules.DailyCap = 999999
	rules.CooldownMinutes = 0
	rules.OpDailyMax = map[string]int64{}

	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	e.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv1"}})
	e.mu.Lock()
	e.sessions["srv1"] = time.Now().Add(-120 * time.Minute) // 120/10=12 → 封顶 8
	e.mu.Unlock()
	e.OnOp(audit.OpEvent{Op: "ssh.shell.end", Fields: []any{"system", "srv1"}})

	st := e.State()
	if st.TotalEarned != 14 {
		t.Fatalf("TotalEarned = %d, 期望 14（start 6 + session 封顶 8）", st.TotalEarned)
	}
}

// TestOnOp_SessionMinDuration 防刷：会话不足 3min 不结算时长奖励（防秒开秒关）。
func TestOnOp_SessionMinDuration(t *testing.T) {
	rules := DefaultRules()
	rules.DailyCap = 999999
	rules.CooldownMinutes = 0
	rules.OpDailyMax = map[string]int64{}

	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	e.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv1"}})
	e.mu.Lock()
	e.sessions["srv1"] = time.Now().Add(-2 * time.Minute) // < 3min 门槛
	e.mu.Unlock()
	e.OnOp(audit.OpEvent{Op: "ssh.shell.end", Fields: []any{"system", "srv1"}})

	st := e.State()
	if st.TotalEarned != 6 {
		t.Fatalf("短会话不应结算时长奖励, TotalEarned = %d, 期望 6", st.TotalEarned)
	}
	if len(st.Ledger) != 1 {
		t.Fatalf("流水应只有 start 1 条, 实际 %d", len(st.Ledger))
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

// TestOnOp_NoTargetFields 无 system/host 字段时用空 target，不 panic、正常计分
// （2s 内的重复会被频次检测拦截，属预期防刷行为）。
func TestOnOp_NoTargetFields(t *testing.T) {
	rules := DefaultRules()
	rules.CooldownMinutes = 0
	rules.OpDailyMax = map[string]int64{}
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	e.OnOp(audit.OpEvent{Op: "ssh.shell.start"}) // +6

	if st := e.State(); st.TotalEarned != 6 {
		t.Fatalf("TotalEarned = %d, 期望 6", st.TotalEarned)
	}
}

// TestOnOp_Concurrent 并发喂 op 不应数据竞争或丢计数（distinct target 避开频次检测）。
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
	if want := int64(goroutines * per * 6); st.TotalEarned != want {
		t.Fatalf("TotalEarned = %d, 期望 %d", st.TotalEarned, want)
	}
}
