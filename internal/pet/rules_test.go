package pet

import (
	"testing"

	"kairo/internal/config"
)

// TestDefaultRules 内置默认值与设计文档 §5.2/§5.3 对齐。
func TestDefaultRules(t *testing.T) {
	r := DefaultRules()
	if r.DailyCap != 200 {
		t.Fatalf("DailyCap = %d, 期望 200", r.DailyCap)
	}
	if r.CooldownMinutes != 60 {
		t.Fatalf("CooldownMinutes = %d, 期望 60", r.CooldownMinutes)
	}
	if r.SessionExpMinutes != 10 {
		t.Fatalf("SessionExpMinutes = %d, 期望 10", r.SessionExpMinutes)
	}
	if !r.NotifyUp {
		t.Fatal("NotifyUp 默认应为 true")
	}
	if r.StatsKeepDays != 90 || r.StatsKeepMonths != 12 {
		t.Fatalf("统计保留 = %d/%d, 期望 90/12", r.StatsKeepDays, r.StatsKeepMonths)
	}
	if r.MaxLedger != 2000 {
		t.Fatalf("MaxLedger = %d, 期望 2000", r.MaxLedger)
	}
	if r.SkinCount != 2 {
		t.Fatalf("SkinCount = %d, 期望 2", r.SkinCount)
	}

	// ExpRules 白名单抽查 + 未列出 = 0
	if r.ExpRules["ssh.shell.start"] != 5 {
		t.Fatalf("ssh.shell.start = %d, 期望 5", r.ExpRules["ssh.shell.start"])
	}
	if r.ExpRules["files.download"] != 1 {
		t.Fatalf("files.download = %d, 期望 1", r.ExpRules["files.download"])
	}
	if r.ExpRules["compare.deep_check"] != 2 {
		t.Fatalf("compare.deep_check = %d, 期望 2", r.ExpRules["compare.deep_check"])
	}
	if _, ok := r.ExpRules["files.list"]; ok {
		t.Fatal("files.list 不应在白名单")
	}

	// StageLevels 区间
	if r.StageLevels["egg"] != [2]int{1, 5} {
		t.Fatalf("egg = %v, 期望 [1 5]", r.StageLevels["egg"])
	}
	if r.StageLevels["mythic"] != [2]int{31, 999} {
		t.Fatalf("mythic = %v, 期望 [31 999]", r.StageLevels["mythic"])
	}

	// OpDailyMax
	if r.OpDailyMax["ssh.shell.start"] != 100 {
		t.Fatalf("ssh.shell.start 日上限 = %d, 期望 100", r.OpDailyMax["ssh.shell.start"])
	}
	if r.OpDailyMax["http.request"] != 200 {
		t.Fatalf("http.request 日上限 = %d, 期望 200", r.OpDailyMax["http.request"])
	}
}

// TestRulesFromConfig_Override 非零配置字段覆盖默认值；map 整体替换。
func TestRulesFromConfig_Override(t *testing.T) {
	notifyFalse := false
	pc := config.PetConfig{
		DailyCap:          50,
		CooldownMinutes:   5,
		SessionExpMinutes: 7,
		NotifyUp:          &notifyFalse,
		ExpRules:          map[string]int64{"custom.op": 99},
		StageLevels:       map[string][2]int{"zzz": {1, 10}},
		OpDailyMax:        map[string]int64{"custom.op": 3},
		StatsKeepDays:     30,
		StatsKeepMonths:   3,
		SkinCount:         9,
	}
	r := RulesFromConfig(pc)

	if r.DailyCap != 50 || r.CooldownMinutes != 5 || r.SessionExpMinutes != 7 {
		t.Fatalf("数值覆盖失败: %+v", r)
	}
	if r.NotifyUp {
		t.Fatal("NotifyUp 应为 false")
	}
	if r.StatsKeepDays != 30 || r.StatsKeepMonths != 3 || r.SkinCount != 9 {
		t.Fatalf("保留窗口/皮肤数覆盖失败: %+v", r)
	}
	// map 整体替换：只含配置项
	if len(r.ExpRules) != 1 || r.ExpRules["custom.op"] != 99 {
		t.Fatalf("ExpRules 应整体替换, 实际 %+v", r.ExpRules)
	}
	if len(r.StageLevels) != 1 || r.StageLevels["zzz"] != [2]int{1, 10} {
		t.Fatalf("StageLevels 应整体替换, 实际 %+v", r.StageLevels)
	}
	if len(r.OpDailyMax) != 1 || r.OpDailyMax["custom.op"] != 3 {
		t.Fatalf("OpDailyMax 应整体替换, 实际 %+v", r.OpDailyMax)
	}
	// 未配置字段保留默认
	if r.MaxLedger != 2000 {
		t.Fatalf("MaxLedger = %d, 期望保留默认 2000", r.MaxLedger)
	}
}

// TestRulesFromConfig_ZeroConfig 全零配置 = 内置默认。
func TestRulesFromConfig_ZeroConfig(t *testing.T) {
	r := RulesFromConfig(config.PetConfig{})
	d := DefaultRules()
	if r.DailyCap != d.DailyCap || r.CooldownMinutes != d.CooldownMinutes ||
		r.SessionExpMinutes != d.SessionExpMinutes || r.SkinCount != d.SkinCount ||
		r.MaxLedger != d.MaxLedger {
		t.Fatalf("零配置应等于默认, 实际 %+v", r)
	}
	if len(r.ExpRules) != len(d.ExpRules) || len(r.StageLevels) != len(d.StageLevels) {
		t.Fatalf("零配置 map 应等于默认表")
	}
}

// TestRulesFromConfig_DeepCopy map 深拷贝：修改配置不影响已构造的 Rules。
func TestRulesFromConfig_DeepCopy(t *testing.T) {
	pc := config.PetConfig{
		ExpRules:    map[string]int64{"a": 1},
		StageLevels: map[string][2]int{"s": {1, 2}},
		OpDailyMax:  map[string]int64{"a": 5},
	}
	r := RulesFromConfig(pc)

	// 修改原配置
	pc.ExpRules["b"] = 2
	delete(pc.ExpRules, "a")
	pc.StageLevels["s"] = [2]int{9, 9}
	pc.OpDailyMax["a"] = 0

	if _, ok := r.ExpRules["b"]; ok {
		t.Fatal("ExpRules 被配置污染")
	}
	if r.ExpRules["a"] != 1 {
		t.Fatalf("ExpRules[a] = %d, 期望 1", r.ExpRules["a"])
	}
	if r.StageLevels["s"] != [2]int{1, 2} {
		t.Fatalf("StageLevels 被配置污染: %v", r.StageLevels["s"])
	}
	if r.OpDailyMax["a"] != 5 {
		t.Fatalf("OpDailyMax 被配置污染: %d", r.OpDailyMax["a"])
	}
}

// TestDefaultRules_DeepCopy 默认表每次返回独立 map，避免调用方互相污染。
func TestDefaultRules_DeepCopy(t *testing.T) {
	r1 := DefaultRules()
	r1.ExpRules["ssh.shell.start"] = 1234
	r2 := DefaultRules()
	if r2.ExpRules["ssh.shell.start"] != 5 {
		t.Fatal("DefaultRules 的 map 应每次新建")
	}
}
