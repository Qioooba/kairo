package pet

import "kairo/internal/config"

// Rules 宠物经验/成长规则集合（构造后视为不可变，不得修改其 map）。
//
// 语义：
//   - ExpRules：op → 经验；未列出的 op 一律 0 分（白名单分级）；
//   - StageLevels：进化阶段名 → 等级区间 [min,max]（含边界）；
//   - OpDailyMax：op → 每日次数上限；未列出 = 无上限。
//
// v2 调整（见 docs/PET-SKINS-V2-DESIGN.md §4/§5）：
//   - 权重按"用户价值"分 4 档（核心会话 6 / 高价值 3~4 / 中价值 2 / 轻价值 1）；
//   - 每日上限 300（配合皮肤解锁节奏）；冷却窗口 30min（降低误伤）；
//   - 阶段区间 egg 1-3 / hatchling 4-8 / grown 9-15 / mythic 16+（等级无上限，
//     mythic 上限为 MaxInt 语义上的"无穷"）；
//   - SkinCount 已删除：皮肤清单由 web/img/pet/skins/skins.json 驱动（见 skins.go）。
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
}

// mythicNoCap 神话阶段等级上限（int 近似无穷，等级无上限设计）。
const mythicNoCap = int(^uint32(0) >> 1) // MaxInt32

// DefaultRules 返回编译期内置的默认规则。
//
// 数值与 docs/PET-SKINS-V2-DESIGN.md §4/§5 对齐：
// 每日上限 300、冷却 30 分钟、会话每 10 分钟 +1（封顶 8）、统计保留 90 天 / 12 月、
// 流水环形上限 2000 条。
func DefaultRules() Rules {
	return Rules{
		DailyCap:          300,
		CooldownMinutes:   30,
		SessionExpMinutes: 10,
		NotifyUp:          true,
		ExpRules: map[string]int64{
			// 核心-会话
			"ssh.shell.start": 6,
			// 高价值
			"compare.deep_check":   4,
			"compare.folder_scan":  3,
			"compare.file_diff":    3,
			"ssh.sftp.edit.upload": 3,
			// 中价值
			"ssh.sftp.upload":   2,
			"ssh.sftp.download": 2,
			"logs.download":     2,
			"files.download":    2,
			// 轻价值
			"http.request":     1,
			"http.case.upsert": 1,
			"credentials.save": 1,
			"reminder.add":     1,
		},
		StageLevels: map[string][2]int{
			"egg":       {1, 3},
			"hatchling": {4, 8},
			"grown":     {9, 15},
			"mythic":    {16, mythicNoCap},
		},
		OpDailyMax: map[string]int64{
			"ssh.shell.start":      40,
			"ssh.session.time":     60,
			"ssh.sftp.upload":      100,
			"ssh.sftp.download":    100,
			"ssh.sftp.edit.upload": 80,
			"files.download":       60,
			"logs.download":        60,
			"compare.file_diff":    40,
			"compare.folder_scan":  30,
			"compare.deep_check":   30,
			"http.request":         150,
			"http.case.upsert":     60,
			"credentials.save":     20,
			"reminder.add":         20,
		},
		StatsKeepDays:   90,
		StatsKeepMonths: 12,
		MaxLedger:       2000,
	}
}

// RulesFromConfig 从配置构造规则：先取内置默认，再覆盖非零配置字段。
//
// 覆盖规则（对齐 config.PetConfig 的注释）：
//   - 数值字段：仅非零值覆盖（0 视为"未配置"，用默认兜底）；
//   - ExpRules / StageLevels / OpDailyMax：配置非空时整体替换默认表；
//   - NotifyUp：配置非 nil 时替换；
//   - SkinCount：v2 已废弃，配置值被忽略（皮肤清单由 skins.json 驱动）。
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
	return r
}
