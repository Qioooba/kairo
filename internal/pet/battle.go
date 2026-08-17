package pet

// BattleEngine 对战引擎占位类型（v2 实现）。
//
// v1 只预留类型与 State.Battle 三围字段（atk/def/hp/spd），多人对战整体后置：
//   - 数据：pet.json 的 battle 字段已在 State 中预留；
//   - API：/api/pet/battle/* 命名空间预留，v1 不注册（404 即"未开放"）；
//   - 服务器：pets 表预留 battle_snapshot 列（nullable），v2 存异步对战快照。
//
// v2 落地时在此类型上挂载回合结算 / 快照校验逻辑即可。
type BattleEngine struct{}
