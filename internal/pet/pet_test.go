package pet

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kairo/internal/audit"
)

// TestEnable_Idempotent 解锁幂等：解锁前 false、解锁后 true、文件已创建。
func TestEnable_Idempotent(t *testing.T) {
	e, path := newTestEngine(t, DefaultRules())

	if e.Enabled() {
		t.Fatal("新引擎未解锁时 Enabled() 应为 false")
	}
	st, err := e.Enable()
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !e.Enabled() || !st.Enabled {
		t.Fatal("Enable 后 Enabled 应为 true")
	}
	if st.Level != 1 || st.Stage != "egg" || st.Name != "小K" {
		t.Fatalf("初始宠物状态异常: %+v", st)
	}

	// 幂等：再次调用不报错、状态保持
	if _, err := e.Enable(); err != nil {
		t.Fatalf("第二次 Enable: %v", err)
	}
	if !e.Enabled() {
		t.Fatal("重复 Enable 后 Enabled 仍应为 true")
	}

	// 文件已创建且含签名
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("pet.json 未创建: %v", err)
	}
	var disk State
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("pet.json 解析失败: %v", err)
	}
	if !disk.Enabled || disk.V != StateVersion {
		t.Fatalf("磁盘状态异常: %+v", disk)
	}
	if disk.Sig == "" {
		t.Fatal("磁盘状态应带 HMAC 签名")
	}
}

// TestForceEnable 开发者强制解锁与 Enable 同行为。
func TestForceEnable(t *testing.T) {
	e, _ := newTestEngine(t, DefaultRules())
	if _, err := e.ForceEnable(); err != nil {
		t.Fatalf("ForceEnable: %v", err)
	}
	if !e.Enabled() {
		t.Fatal("ForceEnable 后 Enabled 应为 true")
	}
}

// TestHMAC_Roundtrip 保存→重载状态保持；篡改回退 .bak；全损坏回退新建。
func TestHMAC_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pet.json")
	key := make([]byte, 32)
	for i := range key {
		key[i] = 0xAB
	}

	e1, err := NewEngine(DefaultRules(), path, key)
	if err != nil {
		t.Fatalf("NewEngine#1: %v", err)
	}
	if _, err := e1.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	e1.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv1"}})
	if err := e1.Close(); err != nil {
		t.Fatalf("Close#1: %v", err)
	}

	// 重载：状态保持
	e2, err := NewEngine(DefaultRules(), path, key)
	if err != nil {
		t.Fatalf("NewEngine#2: %v", err)
	}
	if !e2.Enabled() {
		t.Fatal("重载后 Enabled 应为 true")
	}
	st2 := e2.State()
	if st2.TotalEarned != 5 || st2.Name != "小K" || st2.Level != 1 {
		t.Fatalf("重载状态不一致: %+v", st2)
	}
	if err := e2.Close(); err != nil {
		t.Fatalf("Close#2: %v", err)
	}

	// 篡改主文件（改 name 但签名过期）：应回退 .bak（内容为喂经验前的状态）
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 pet.json: %v", err)
	}
	tampered := strings.ReplaceAll(string(raw), "小K", "黑客")
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatalf("写篡改文件: %v", err)
	}
	e3, err := NewEngine(DefaultRules(), path, key)
	if err != nil {
		t.Fatalf("篡改后 NewEngine 不应报错: %v", err)
	}
	st3 := e3.State()
	if !st3.Enabled {
		t.Fatal("回退 .bak 后 Enabled 应为 true")
	}
	if st3.Name != "小K" {
		t.Fatalf("回退后 name = %q, 期望未篡改的 %q", st3.Name, "小K")
	}
	if st3.TotalEarned != 0 {
		t.Fatalf(".bak 应为喂经验前状态 TotalEarned=0, 实际 %d", st3.TotalEarned)
	}
	if err := e3.Close(); err != nil {
		t.Fatalf("Close#3: %v", err)
	}

	// 主文件 + 备份全损坏：回退全新宠物，不报错
	if err := os.WriteFile(path, []byte("not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".bak", []byte("also garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	e4, err := NewEngine(DefaultRules(), path, key)
	if err != nil {
		t.Fatalf("全损坏后 NewEngine 不应报错: %v", err)
	}
	st4 := e4.State()
	if st4.Enabled {
		t.Fatal("全损坏回退后应为未解锁")
	}
	if st4.Level != 1 || st4.TotalEarned != 0 {
		t.Fatalf("回退新宠物状态异常: %+v", st4)
	}
}

// TestSigKey_AutoGenerate sigKey 为空时自动生成/复用 .petkey。
func TestSigKey_AutoGenerate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pet.json")

	e1, err := NewEngine(DefaultRules(), path, nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	keyPath := filepath.Join(dir, ".petkey")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf(".petkey 未创建: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf(".petkey 权限应为 0600, 实际 %o", info.Mode().Perm())
	}
	if _, err := e1.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := e1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 复用同一 key 重载（若 key 不一致签名校验会失败 → 回退新建 → Enabled=false）
	e2, err := NewEngine(DefaultRules(), path, nil)
	if err != nil {
		t.Fatalf("NewEngine#2: %v", err)
	}
	if !e2.Enabled() {
		t.Fatal(".petkey 应被复用，重载后 Enabled 应为 true")
	}
}

// TestState_DeepCopy State() 返回深拷贝，调用方修改不影响引擎。
func TestState_DeepCopy(t *testing.T) {
	e, _ := newTestEngine(t, DefaultRules())
	mustEnable(t, e)
	feed(t, e, "ssh.shell.start", 1)

	st1 := e.State()
	st1.Name = "被改"
	st1.TotalEarned = 999
	st1.Ledger[0].Op = "被改"
	st1.Stats.Total["ssh.shell.start"] = OpStat{Count: 999, Exp: 999}

	st2 := e.State()
	if st2.Name != "小K" || st2.TotalEarned != 5 {
		t.Fatalf("State() 应返回深拷贝, 实际被污染: %+v", st2)
	}
	if st2.Ledger[0].Op != "ssh.shell.start" {
		t.Fatalf("Ledger 被污染: %+v", st2.Ledger)
	}
	if st2.Stats.Total["ssh.shell.start"].Count != 1 {
		t.Fatalf("Stats 被污染: %+v", st2.Stats.Total)
	}
}

// TestStateView 计算字段正确。
func TestStateView(t *testing.T) {
	rules := DefaultRules()
	rules.DailyCap = 200
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)
	feed(t, e, "ssh.shell.start", 1) // +5

	v := e.StateView()
	if v["level"].(float64) != 1 {
		t.Fatalf("level = %v", v["level"])
	}
	if v["next_exp"].(float64) != float64(NextExp(1)) {
		t.Fatalf("next_exp = %v", v["next_exp"])
	}
	if v["today_earned"].(float64) != 5 {
		t.Fatalf("today_earned = %v", v["today_earned"])
	}
	if v["daily_cap"].(float64) != 200 {
		t.Fatalf("daily_cap = %v", v["daily_cap"])
	}
	if v["skin_count"].(float64) != 2 {
		t.Fatalf("skin_count = %v", v["skin_count"])
	}
}

// TestRename 改名校验：长度、控制字符、空名。
func TestRename(t *testing.T) {
	e, _ := newTestEngine(t, DefaultRules())
	mustEnable(t, e)

	// 正常改名
	if err := e.Rename("阿福"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if st := e.State(); st.Name != "阿福" {
		t.Fatalf("name = %q, 期望 阿福", st.Name)
	}

	// 控制字符剥离 + TrimSpace
	if err := e.Rename("  喵\n\x00汪\t"); err != nil {
		t.Fatalf("Rename(控制字符): %v", err)
	}
	if st := e.State(); st.Name != "喵汪" {
		t.Fatalf("控制字符应被剥离, 实际 name = %q", st.Name)
	}

	// 恰好 16 个 rune 允许
	if err := e.Rename(strings.Repeat("喵", 16)); err != nil {
		t.Fatalf("16 个字符应允许: %v", err)
	}
	// 17 个 rune 拒绝
	if err := e.Rename(strings.Repeat("喵", 17)); err == nil {
		t.Fatal("17 个字符应被拒绝")
	}
	// 空名 / 纯空白拒绝
	if err := e.Rename("   "); err == nil {
		t.Fatal("空名应被拒绝")
	}
	if err := e.Rename("\t\r\n"); err == nil {
		t.Fatal("纯控制字符名应被拒绝")
	}
}

// TestSetPos 位置 clamp 与非法值拒绝。
func TestSetPos(t *testing.T) {
	e, _ := newTestEngine(t, DefaultRules())
	mustEnable(t, e)

	if err := e.SetPos(0.5, 0.5); err != nil {
		t.Fatalf("SetPos: %v", err)
	}
	if st := e.State(); st.Pos.X != 0.5 || st.Pos.Y != 0.5 {
		t.Fatalf("pos = %+v, 期望 {0.5,0.5}", st.Pos)
	}

	// 越界 clamp 到 [0,1]
	if err := e.SetPos(3, -2); err != nil {
		t.Fatalf("SetPos(越界): %v", err)
	}
	if st := e.State(); st.Pos.X != 1 || st.Pos.Y != 0 {
		t.Fatalf("越界应 clamp, 实际 %+v", st.Pos)
	}

	// NaN / Inf 拒绝
	if err := e.SetPos(math.NaN(), 0.5); err == nil {
		t.Fatal("NaN 应被拒绝")
	}
	if err := e.SetPos(0.5, math.Inf(1)); err == nil {
		t.Fatal("Inf 应被拒绝")
	}
}

// TestSetSkin 皮肤索引范围校验。
func TestSetSkin(t *testing.T) {
	e, _ := newTestEngine(t, DefaultRules()) // SkinCount=2
	mustEnable(t, e)

	if err := e.SetSkin(0); err != nil {
		t.Fatalf("SetSkin(0): %v", err)
	}
	if err := e.SetSkin(1); err != nil {
		t.Fatalf("SetSkin(1): %v", err)
	}
	if st := e.State(); st.Skin != 1 {
		t.Fatalf("skin = %d, 期望 1", st.Skin)
	}
	if err := e.SetSkin(2); err == nil {
		t.Fatal("SetSkin(2) 应越界")
	}
	if err := e.SetSkin(-1); err == nil {
		t.Fatal("SetSkin(-1) 应越界")
	}
}

// TestLedger_TakeLedgerDeepCopy TakeLedger 返回深拷贝且不清空。
func TestLedger_TakeLedgerDeepCopy(t *testing.T) {
	rules := DefaultRules()
	rules.CooldownMinutes = 0
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	e.OnOp(audit.OpEvent{Op: "ssh.shell.start", Fields: []any{"system", "srv1"}})
	l1 := e.TakeLedger()
	if len(l1) != 1 {
		t.Fatalf("ledger 应 1 条, 实际 %d", len(l1))
	}
	l1[0].Op = "被改"

	l2 := e.TakeLedger()
	if len(l2) != 1 || l2[0].Op != "ssh.shell.start" {
		t.Fatalf("TakeLedger 应返回深拷贝, 实际 %+v", l2)
	}
}

// TestLedger_MarkSynced 同步回写：清流水、置 BoardExp/LastSync、Dirty=false。
func TestLedger_MarkSynced(t *testing.T) {
	rules := DefaultRules()
	rules.CooldownMinutes = 0
	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	feed(t, e, "ssh.shell.start", 2)
	st := e.State()
	if !st.Dirty {
		t.Fatal("计分后 Dirty 应为 true")
	}

	e.MarkSynced(1180, "2026-08-16T10:00:00+08:00")
	st = e.State()
	if st.Dirty {
		t.Fatal("MarkSynced 后 Dirty 应为 false")
	}
	if st.BoardExp != 1180 {
		t.Fatalf("BoardExp = %d, 期望 1180", st.BoardExp)
	}
	if st.LastSync != "2026-08-16T10:00:00+08:00" {
		t.Fatalf("LastSync = %q", st.LastSync)
	}
	if len(st.Ledger) != 0 {
		t.Fatalf("MarkSynced 后流水应清空, 实际 %d", len(st.Ledger))
	}
}

// TestLedger_MaxTrim 流水环形上限：超限丢最旧、留最新。
func TestLedger_MaxTrim(t *testing.T) {
	rules := DefaultRules()
	rules.DailyCap = 999999
	rules.CooldownMinutes = 0
	rules.MaxLedger = 2

	e, _ := newTestEngine(t, rules)
	mustEnable(t, e)

	feed(t, e, "ssh.shell.start", 3)
	st := e.State()
	if len(st.Ledger) != 2 {
		t.Fatalf("流水应裁剪到 2 条, 实际 %d", len(st.Ledger))
	}
	// 保留最新 2 条：ts 递增，第 1 条最旧应被丢弃
	if st.Ledger[0].Exp != 5 || st.Ledger[1].Exp != 5 {
		t.Fatalf("流水内容异常: %+v", st.Ledger)
	}
}

// TestClose_Idempotent Close 幂等且强制落盘。
func TestClose_Idempotent(t *testing.T) {
	e, path := newTestEngine(t, DefaultRules())
	mustEnable(t, e)
	feed(t, e, "ssh.shell.start", 1)

	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("重复 Close: %v", err)
	}

	// 落盘已发生（无需等 30s 防抖）
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Close 后应已落盘: %v", err)
	}
	var disk State
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if disk.TotalEarned != 5 {
		t.Fatalf("落盘 TotalEarned = %d, 期望 5", disk.TotalEarned)
	}
}

// TestNewEngine_MissingDir 目录不存在时自动创建（密钥/数据目录）。
func TestNewEngine_MissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	path := filepath.Join(dir, "pet.json")
	key := make([]byte, 32)

	e, err := NewEngine(DefaultRules(), path, key)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()
	if _, err := e.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pet.json 未创建: %v", err)
	}
}

// TestNewEngine_EmptyDataPath 空路径直接报错。
func TestNewEngine_EmptyDataPath(t *testing.T) {
	if _, err := NewEngine(DefaultRules(), "", make([]byte, 32)); err == nil {
		t.Fatal("空 dataPath 应报错")
	}
}
