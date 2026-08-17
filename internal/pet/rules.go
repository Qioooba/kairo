package pet

import "kairo/internal/config"

// Rules 宠物经验/成长规则集合（构造后视为不可变，不得修改其 map）。
//
// 语义：
//   - ExpRules：op → 经验；未列出的 op 一律 0 分（白名单分级）；
//   - StageLevels：进化阶段名 → 等级区间 [min,max]（含边界）；
//   - OpDailyMax：op → 每日次数上限；未列出 = 无上限。
type Rules struct {
	DailyCap          int64
	CooldownMinutes   int
	SessionExpMinutes int
	NotifyUp          bool
	ExpRules          map[string]int64
	StageLevels       map[string][2]int
	OpDailyMax        map[string]int64
	StatsKeepDays     int
	StatsKeepMonths   int
	MaxLedger         int
	SkinCount         int
}

// DefaultRules 返回编译期内置的默认规则。
//
// 数值与 docs/PET-FEATURE-DESIGN.md §5.2/§5.3 对齐：
// 每日上限 200、冷却 60 分钟、会话每 10 分钟 +1、统计保留 90 天 / 12 月、
// 流水环形上限 2000 条、内置皮肤 2 张。
func DefaultRules() Rules {
	return Rules{
		DailyCap:          200,
		CooldownMinutes:   60,
		SessionExpMinutes: 10,
		NotifyUp:          true,
		ExpRules: map[string]int64{
			"ssh.shell.start":      5,
			"ssh.sftp.upload":      2,
			"ssh.sftp.download":    2,
			"ssh.sftp.edit.upload": 2,
			"files.download":       1,
			"logs.download":        1,
			"compare.file_diff":    2,
			"compare.folder_scan":  2,
			"compare.deep_check":   2,
			"http.request":         1,
			"http.case.upsert":     1,
			"credentials.save":     1,
			"reminder.add":         1,
		},
		StageLevels: map[string][2]int{
			"egg":       {1, 5},
			"hatchling": {6, 15},
			"grown":     {16, 30},
			"mythic":    {31, 999},
		},
		OpDailyMax: map[string]int64{
			"ssh.shell.start":   100,
			"ssh.sftp.upload":   200,
			"ssh.sftp.download": 200,
			"http.request":      200,
		},
		StatsKeepDays:   90,
		StatsKeepMonths: 12,
		MaxLedger:       2000,
		SkinCount:       2,
	}
}

// RulesFromConfig 从配置构造规则：先取内置默认，再覆盖非零配置字段。
//
// 覆盖规则（对齐 config.PetConfig 的注释）：
//   - 数值字段：仅非零值覆盖（0 视为"未配置"，用默认兜底）；
//   - ExpRules / StageLevels / OpDailyMax：配置非空时整体替换默认表；
//   - NotifyUp：配置非 nil 时替换。
//
// map 全部深拷贝，调用方后续修改 pc 不影响本 Rules。
func RulesFromConfig(pc config.PetConfig) Rules {
	r := DefaultRules()
	if pc.DailyCap != 0 {
		r.DailyCap = pc.DailyCap
	}
	if pc.CooldownMinutes != 0 {
		r.CooldownMinutes = pc.CooldownMinutes
	}
	if pc.SessionExpMinutes != 0 {
		r.SessionExpMinutes = pc.SessionExpMinutes
	}
	if pc.NotifyUp != nil {
		r.NotifyUp = *pc.NotifyUp
	}
	if len(pc.ExpRules) > 0 {
		r.ExpRules = make(map[string]int64, len(pc.ExpRules))
		for k, v := range pc.ExpRules {
			r.ExpRules[k] = v
		}
	}
	if len(pc.StageLevels) > 0 {
		r.StageLevels = make(map[string][2]int, len(pc.StageLevels))
		for k, v := range pc.StageLevels {
			r.StageLevels[k] = v
		}
	}
	if len(pc.OpDailyMax) > 0 {
		r.OpDailyMax = make(map[string]int64, len(pc.OpDailyMax))
		for k, v := range pc.OpDailyMax {
			r.OpDailyMax[k] = v
		}
	}
	if pc.StatsKeepDays != 0 {
		r.StatsKeepDays = pc.StatsKeepDays
	}
	if pc.StatsKeepMonths != 0 {
		r.StatsKeepMonths = pc.StatsKeepMonths
	}
	if pc.SkinCount != 0 {
		r.SkinCount = pc.SkinCount
	}
	return r
}
