package pet

// Skill 技能接口占位（v2 实现）。
//
// v1 只预留类型与 State.Skills 字段（[]any），技能系统整体后置：
//   - 数据：pet.json 的 skills 字段已在 State 中预留；
//   - API：/api/pet/skills/* 命名空间预留，v1 不注册（404 即"未开放"）；
//   - UI：完整面板的技能 tab 由 feature 开关控制显隐。
//
// v2 落地时直接给技能类型实现本接口即可，引擎侧无需结构调整。
type Skill interface {
	ID() string
	Name() string
}
