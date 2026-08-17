# 宠物功能 设计方案（v1 · 隐藏彩蛋 + 桌面宠物 + 排行榜）

> 状态：**v1 已实现**（2026-08-16）。经验引擎 + 本地持久化 + 浮动宠物 + 6 次点击解锁 +
> 武林页宠物榜切换均已落地并通过单测/e2e 冒烟；服务器端 `pet_leaderboard` 接口协议已定，
> 待 Java 端联调。技能/对战保持 v2 预留。
>
> 定位：**隐藏彩蛋式陪伴宠物**。用户点击「关于」6 次后解锁，桌面右下角出现可拖动的
> 小宠物（类似 codex 宠物 / QQ 宠物）；未解锁时**完全不记录任何宠物信息**。
> 解锁后永久默认开启，使用工具自动涨经验进化，武林排行榜页可切换到隐藏的宠物排行榜。

---

## 1. 范围界定（v1 做 / 不做）

| 功能 | v1 | 说明 |
|---|---|---|
| 隐藏开关（点击「关于」6 次解锁） | ✅ | 见 §3 |
| 桌面右下角浮动宠物（可拖动） | ✅ | 见 §4 |
| 拖动晃动换皮肤 | ✅ | 皮肤池可扩展，v1 内置占位图 | 见 §4.3 |
| 宠物取名 / 说话气泡 | ✅ | 话语内容 v1 内置占位，话语池后续设计 | 见 §4.4 |
| 宠物点击交互 | ⭕ 预留入口 | 单击弹迷你面板，具体功能后面再设计 | 见 §4.5 |
| 经验获取（复用 audit，零新增埋点） | ✅ | 未解锁时引擎 no-op，零记录 | 见 §5 |
| 等级 / 进化阶段 / 外观 | ✅ | 见 §5.4 |
| 本地离线数据 pet.json + HMAC | ✅ | 见 §6 |
| 宠物排行榜（武林榜页切换，隐藏） | ✅ | 见 §8.2 |
| 防刷榜（本地规则 + 服务器校验，**无封禁**） | ✅ | 见 §7 |
| 技能（Skill） | ❌ 仅预留入口 | 见 §11 |
| 多人对战（Battle） | ❌ 仅预留入口 | 见 §11 |
| 服务器侧封禁 / 剔除账号 | ❌ 不做 | 异常增量不计入，账号保留旧分 |

---

## 2. 总体架构与数据流

```
【未解锁】点击「关于」6 次（10s 窗口）→ POST /api/pet/enable
                    │
                    ▼ 创建 data/pet.json (enabled:true)，写 audit "pet.enable"
【已解锁】启动时读 pet.json → enabled → 引擎激活 + 前端渲染浮动宠物
                    │
用户操作 ──► audit.Logger.Write(op, ...)（现有，同步写盘）
                 │ 内存回调（Subscribe；未解锁时原子 bool 判断后立即返回，零记录）
                 ▼
        internal/pet 引擎（内存，仅解锁后工作）
         ├─ op→exp 映射（config 可调）＋冷却表＋每日上限＋会话时长计时
         └─ 未同步流水 ledger（环形 ≤2000 条，仅 op/ts/exp）
                 │ 防抖写盘（30s 或退出时）
                 ▼
        data/pet.json（HMAC，本地权威：等级/皮肤/名字/位置/说话相关字段）
                 │ 切到宠物排行榜 tab & dirty=true
                 ▼
        POST 服务器 endpoint → 校验流水 → 重算认可分 → upsert 榜单
                 ▼
        回写本地：board_exp=认可分，dirty=false
```

关键原则：

1. **未解锁零痕迹**——不建 pet.json、不写任何文件、订阅回调立即返回（一次原子读，~1ns）。
2. **解锁一次永久开启**——enabled 持久化在 pet.json，之后每次启动自动激活，无需再点 6 次。
3. **本地数据永远不可信**——排行榜只认服务器重算后的认可分。
4. **失败静默**——同步失败保留 dirty，下次切榜重试，不影响任何功能。

---

## 3. 隐藏开关设计（点击「关于」6 次）

### 3.1 触发规则

- 入口：home 页「其他」分区的「关于」卡片（web/pages/home.js）。
- 规则：**10 秒滑动窗口内点击累计 6 次**（超过 10 秒无点击则计数清零）。
- 第 6 次点击：调 `POST /api/pet/enable`，成功后 toast「已开启宠物功能，看看右下角～」，
  浮动宠物以浮现动画出现；`/api/pet/enable` 幂等，已解锁直接返回当前状态。
- 不干扰正常功能：点击「关于」的行为本身不变（照常打开关于页）。

### 3.2 后端 enable 语义

- 创建 `data/pet.json`：`enabled:true` + 初始宠物（蛋阶段、Lv1、默认名"小K"、皮肤 0、随机出生语）。
- 写 audit `pet.enable`（该 op 不给经验）。
- 引擎激活：从当刻起开始累计经验；**解锁前的任何操作都不追溯**。
- 后续启动：`pet.json` 存在且 `enabled=true` → 自动激活（永久默认开启）。
  用户删除 pet.json = 关闭彩蛋（可再点 6 次重开）。

### 3.3 未解锁时的保证

| 层面 | 行为 |
|---|---|
| 后端引擎 | 订阅已挂但 `enabled==false` 时立即返回：不累计、不分配、不写盘 |
| 文件 | 不创建 pet.json、pet.json.bak，磁盘零痕迹 |
| API | `/api/pet/state` 返回 `{enabled:false}`，其余 `/api/pet/*` 返回 404 |
| 前端 | 不渲染浮动宠物、不出现宠物排行榜 tab、无任何宠物 UI |
| 配置 | `config.yaml` 的 `pet.enabled: true` 作为开发者强制开关（测试/演示用），与用户开关取 OR |

---

## 4. 桌面宠物设计（浮动组件）

### 4.1 形态与定位

- 全局浮动组件（`fixed` 定位，z-index 高于所有面板），默认锚定**右下角**（bottom/right 24px）。
- 默认尺寸约 72px，展示当前皮肤图 + 等级小徽章；所有页面（含 SSH/日志页）均可见。
- **可拖动**：按住拖到 viewport 任意位置，松手后位置持久化到 `pet.json`（`pos: {x, y}`，相对视口百分比）；
  窗口 resize 时 clamp 回可视区。关闭重开从 pet.json 恢复位置。
- 实现为 `web/pages/pet.js` 挂载的全局组件（app.js 初始化时按 `/api/pet/state` 决定是否渲染），
  不走独立窗口——与 codex 宠物同形态，零新增基础设施（`internal/popup` 是 Windows 通知弹窗，不适用于常驻宠物）。

### 4.2 皮肤系统

- 皮肤池：`web/img/pet/skin-00.png … skin-NN.png`，**v1 内置 2 张占位图**，后续直接往目录加图即扩容（无需改代码）。
- 皮肤索引存 `pet.json` 的 `skin` 字段；进化阶段（§5.4）可叠加阶段配色/边框，与皮肤独立。

### 4.3 晃动换皮肤

- **仅按住拖动状态下检测**：采样拖拽方向向量，1.5s 滑动窗口内方向反转 ≥ 4 次且瞬时速度 > 阈值 → 判定「晃动」。
- 触发：皮肤循环 +1、播放 0.5s 旋转/弹跳动画、宠物说一句换肤语（§4.4）。
- 防误触：正常拖动（直线/单次变向）不触发；松手后晃动状态重置。
- 冷却 2s，防止连续晃动刷动画。

### 4.4 取名与说话

- **取名**：迷你面板 / 宠物面板内改（`POST /api/pet/name`，长度 ≤ 16，去控制字符），改名后宠物复述新名字。
- **说话**：宠物上方弹出圆角气泡（绝对定位 div），显示一句话，默认 4s 后淡出。
- v1 触发点：解锁欢迎语、升级、进化、晃动换肤、改名、打开排行榜。
- v1 话语内置 3~5 句占位（硬编码常量池），**话语池/文案系统后面再设计**；
  接口预留：前端 `pet.say(text)` 函数 + pet.json 预留 `speech` 字段，后续可配置话语池与触发规则。

### 4.5 点击宠物（预留）

- v1：单击宠物弹出**迷你面板**（等级/经验进度/皮肤数/改名入口/隐藏面板入口），点击宠物其他交互后面再设计。
- 实现上收口为单一 `onPetClick` 入口 + 迷你面板容器，后续加功能（喂食/互动/菜单）只改该处。

---

## 5. 经验系统设计

### 5.1 audit 订阅（零延迟、零新增埋点、未解锁零记录）

现有 `internal/audit` 已覆盖约 60 种 op（SSH/SFTP、日志、文件、下载、比对、HTTP 用例、
凭据、提醒、管理配置、License）。对 `audit.Logger` 增加轻量订阅：

```go
// internal/audit/audit.go 新增
type OpEvent struct { Op string; Fields []any }
type Subscriber interface { OnOp(OpEvent) }

func (l *Logger) Subscribe(s Subscriber) func()   // 返回 unsubscribe
// Write() 写完一行后（锁外）遍历 subscribers 回调；回调内 panic 被 recover，不影响审计
```

- 宠物引擎回调第一行：`if !engine.Enabled() { return }`（原子 bool 读取，~1ns，解锁前零记录）。
- 解锁后：一次 op→exp 表查询 + 加法，纳秒级，主流程零延迟，无新增磁盘 I/O。

### 5.2 op → 经验映射表（白名单分级，config 可调）

只有"有意义"的操作给经验；高频易刷操作不给分（`files.list`、`logs.search`、`ssh.test`、
`local.*`、`preferences.put` 等一律 0 分）。

```yaml
# config.yaml 新增
pet:
  enabled: false           # 开发者强制开关（默认关；用户开关在 pet.json，取 OR）
  daily_cap: 200           # 每日计入排行上限（本地可见超额，sync 时截断）
  cooldown_minutes: 60     # 同 op+target 冷却窗口
  session_exp_minutes: 10  # 会话时长每满该值 +1
  notify_up: true          # 升级/进化 toast
  exp_rules:
    ssh.shell.start: 5
    ssh.sftp.upload: 2
    ssh.sftp.download: 2
    ssh.sftp.edit.upload: 2
    files.download: 1
    logs.download: 1
    compare.file_diff: 2
    compare.folder_scan: 2
    http.request: 1
    http.case.upsert: 1
    credentials.save: 1
    reminder.add: 1
    # 未列出的 op 一律 0 分
  stage_levels:            # 进化阶段（等级区间）
    egg: [1, 5]
    hatchling: [6, 15]
    grown: [16, 30]
    mythic: [31, 999]
```

默认值编译进二进制，config 可覆盖，缺失用默认兜底。

### 5.3 防刷规则（本地层）

| 规则 | 说明 |
|---|---|
| op 白名单 | 未在 `exp_rules` 中的 op = 0 分 |
| 同 op+target 冷却 | 60 分钟内重复（如反复连同一 host）只计首次 |
| 每日上限 | 当日已得 ≥ daily_cap 后不再计入（本地显示但 sync 截断） |
| 会话时长 | `ssh.shell.start`→`end` / `logs.tail` 起止区间内，每满 10 分钟 +1，中断清零，单会话封顶 +5 |
| 单日单 op 频率 | 同 op 当日超阈值（如连接类 >100 次）超出部分不计 |

冷却与上限在**本地**执行（涉及 target 主机名，为隐私不上传服务器）。

### 5.4 成长曲线

- 升级经验：`next_exp(level) = 100 * level^1.5`（L1→2 需 100，L2→3 需 283…）
- 进化阶段：蛋(1-5) → 幼体(6-15) → 成体(16-30) → 神话(31+)，每阶段换配色/光环
- 升级/进化：toast + 宠物说话，不弹窗不打断

---

## 6. 本地数据模型（data/pet.json）

```json
{
  "v": 1,
  "id": "uuid",                 // 本机宠物标识（排行榜 user_id 沿用 sponsor/license 用户标识）
  "enabled": true,              // 解锁开关（用户 6 次点击开启后为 true）
  "name": "小K",
  "level": 3,
  "exp": 240,
  "stage": "hatchling",
  "skin": 0,                    // 皮肤索引（晃动换肤循环 +1）
  "pos": {"x": 0.92, "y": 0.88},// 浮动宠物位置（视口百分比，resize 时 clamp）
  "total_earned": 1240,
  "board_exp": 1180,            // 服务器最近一次认可分（回写）
  "dirty": true,
  "last_sync": "2026-08-16T10:00:00+08:00",
  "ledger": [                   // 未同步流水（环形 ≤2000 条，仅 op/ts/exp，不含 host）
    {"op": "ssh.shell.start", "ts": "2026-08-16T09:58:00+08:00", "exp": 5}
  ],
  "speech": {},                 // 预留：话语池/触发规则（后续设计）
  "skills": [],                 // 预留：技能（v2 填充）
  "battle": {"atk": 0, "def": 0, "hp": 0, "spd": 0},   // 预留：战斗三围（v2 填充）
  "sig": "hmac-sha256-hex"
}
```

- **落盘**：变化防抖 30s + 退出时写；先写 `pet.json.tmp` 再原子 rename。
- **隐私**：ledger 只含 `op/ts/exp`，**不含主机名/IP/路径**。
- **HMAC**：`HMAC-SHA256(payload, secret)`，secret 编译期嵌入（仅门槛，非防线）。
  启动校验失败 → 回退 `pet.json.bak`；仍失败 → 重置新宠物并写 audit `pet.corrupt`。
- **备份**：每次成功写盘后复制上一版为 `pet.json.bak`。

---

## 7. 防作弊设计（分层防线，无封禁）

> 本地签名/加密只是门槛；**真正的裁决在服务器**。服务器不封禁——校验失败的增量不采信，
> 榜单保留旧分。

### 7.1 本地层（提高门槛）

- pet.json HMAC 签名（§6）
- 每日上限 + op 冷却 + 频率阈值（§5.3）

### 7.2 服务器校验层（关键防线）

sync 上传**流水而非最终分数**，服务器重算裁决：

| 校验项 | 规则 |
|---|---|
| 每日增量 | 当日流水 exp 合计 ≤ daily_cap（服务器时区）；超出条目不采信 |
| 时间合法性 | 流水 ts ≤ 服务器当前 + 5min 容忍；早于 24h 前不采信（防补发旧数据） |
| op 合法性 | op 必须在服务器白名单；exp 值 ≤ 该 op 上限 |
| 限频 | 同 user_id sync 间隔 ≥ 5 分钟 |
| 单调性 | 新认可分 ≥ 旧认可分；例外：周榜重置边界允许回落 |
| op 分布异常 | 单日同 op 占比 > 80% 且总数 > 阈值 → 当日增量按比例打折 |

- 排行榜分值 = 服务器重算认可分，**回写本地 board_exp**。
- 校验失败响应：`{ok:false, reason, board_exp(旧值), rank}`——客户端展示旧榜，静默。
- 不做封禁、黑名单；异常用户榜单上就是旧分。

### 7.3 治理层

- **周榜**（周一 00:00 重置）+ **总榜**并存，刷子收益每周归零；周榜重置对应单调性例外分支。

---

## 8. 服务器侧与前端展示

### 8.1 服务器接口（复用 internal_endpoints 模式）

与 sponsor 排行榜同款：`POST /credit/httpInterface?channelID=PC&serviceID=KairoPetLeaderboardAction`，
走 `internal/endpointclient`（主备切换 + Basic Auth + Timeout）：

```yaml
internal_endpoints:
  pet_leaderboard:
    primary: "https://xxx"
    secondary: "https://xxx"
    auth: "user:pass"
    timeout: 10s
```

请求 / 响应协议与表结构同前版（§6.2/§6.3），此处从简：

```jsonc
// 请求
{ "user_id", "name", "level", "stage", "exp_total", "board_exp",
  "ledger": [{"op","ts","exp"}], "sig", "ts" }

// 响应
{ "ok", "error", "board_exp", "rank", "server_time",
  "leaderboard": [{"rank","name","level","exp"}...] }   // 周榜 Top 50
```

```sql
pets(user_id PK, name, level, stage, exp_total, board_exp, week_key, updated_at)
pet_ledger(user_id, op, ts, exp, synced_at)   -- 流水留痕（可选）
-- leaderboard 直接按 (week_key, board_exp DESC) 查 pets，无需物化
```

### 8.2 排行榜切换（武林排行榜页中间部位）

- 现有 sponsor 页中间排行榜容器内加 **tab 切换条**：`[武林排行榜] [宠物排行榜]`。
- **宠物排行榜 tab 仅当宠物已解锁时显示**（隐藏排行榜）；未解锁用户看不到该 tab。
- 切到宠物排行榜 → `dirty` 时触发 `POST /api/pet/sync` → 渲染周榜 Top50 + 「我的排名」卡片；
  失败显示上次榜单 + stale 提示；底部显示上次同步时间。
- 记住上次选择（sessionStorage），默认武林榜。
- 未配置 `pet_leaderboard` 端点：tab 内显示「未配置服务器，仅本地展示」+ 本地等级/经验。

---

## 9. 本地 API 设计（Go httpserver）

统一挂 `/api/pet/*`（`handlers_pet.go`）：

| 路由 | 方法 | 说明 |
|---|---|---|
| `/api/pet/state` | GET | 未解锁返回 `{enabled:false}`；已解锁返回状态（等级/经验/阶段/名字/皮肤/位置/board_exp/dirty） |
| `/api/pet/enable` | POST | 6 次点击触发；幂等；创建 pet.json 并激活引擎 |
| `/api/pet/name` | POST | 改名（≤16 字符，去控制字符） |
| `/api/pet/pos` | POST | 拖动松手后保存位置（防抖，仅写内存，随防抖落盘） |
| `/api/pet/skin` | POST | 晃动换肤后保存皮肤索引（服务端校验范围 0..N-1） |
| `/api/pet/sync` | POST | 触发服务器同步（带 dirty 时），返回最新榜单 + rank + 认可分 |
| `/api/pet/leaderboard` | GET | 本地缓存的上次榜单（无网络请求） |
| `/api/pet/skills/*`、`/api/pet/battle/*` | — | **v1 不注册**，404 即"未开放" |

- 全部走现有 `response.go` JSON 包装；sync 超时 10s，失败返回缓存 + `stale:true`。
- 宠物自身操作（enable/rename/pos/skin）写 audit 但不给经验。

---

## 10. 前端设计

```
web/pages/pet.js            # 浮动宠物组件：渲染/拖动/晃动检测/说话气泡/迷你面板
web/pages/pet-panel.js      # 完整宠物面板（信息/技能[隐藏]/对战[隐藏]/排行榜）——后续扩展用
web/pages/sponsor.js        # 修改：排行榜容器加 tab 切换（武林 ↔ 宠物）
web/pages/home.js           # 修改：关于卡片 6 次点击检测（10s 窗口）
web/app.js                  # 初始化：/api/pet/state → enabled 才挂载浮动组件
```

- 浮动宠物：`fixed` 定位默认右下角，可拖动（§4.1）；气泡/动画纯 CSS + 少量 JS。
- 迷你面板：单击宠物弹出（等级进度、皮肤数、改名、打开完整面板）。
- 皮肤目录：`web/img/pet/skin-*.png`，v1 内置 2 张占位；加图即扩容。
- 所有宠物 UI 在 `enabled:false` 时零渲染。

---

## 11. 技能 / 对战入口预留（v1 不实现，不留重构债）

| 层 | 预留方式 |
|---|---|
| 数据 | pet.json 自带 `skills: []` 与 `battle: {atk,def,hp,spd}` 字段，v2 直接填充 |
| 代码 | `internal/pet` 内定义 `Skill interface` 与 `BattleEngine` 占位类型（仅类型 + 注释） |
| API | `/api/pet/skills/*`、`/api/pet/battle/*` 命名空间预留，v1 不注册 |
| UI | 完整面板 tab 固定四格（宠物 | 技能 | 对战 | 排行榜），技能/对战由 `pet.features.*` 开关控制显隐 |
| 服务器 | pets 表预留 `battle_snapshot` 列（nullable），v2 存异步对战快照 |

---

## 12. 代码结构（internal/pet）

```
internal/pet/
  pet.go          # Pet 状态 + enabled 开关 + 加载/保存（防抖写盘、HMAC、备份回退）
  engine.go       # 引擎：audit 订阅（no-op gate）、op→exp、冷却表、每日上限、会话计时器
  rules.go        # 经验规则默认值 + config 覆盖
  ledger.go       # 未同步流水环形缓冲
  sync.go         # 服务器同步客户端（复用 internal/endpointclient）
  skill.go        # Skill interface 占位（v2）
  battle.go       # BattleEngine 占位类型（v2）
  pet_test.go     # 单测：enable 幂等、解锁前零记录、规则/冷却/上限/签名往返
  sync_test.go    # 单测：协议序列化、失败重试、单调性本地预检
```

- `main.go`：`auditLog.Subscribe(petEngine)`（引擎内部 no-op gate），启动时读 pet.json 决定激活。
- `internal/httpserver/handlers_pet.go`：§9 路由。

---

## 13. 实施阶段

| 阶段 | 内容 | 验收 |
|---|---|---|
| P1 开关+引擎 | 6 次点击检测 + /api/pet/enable + audit 订阅 no-op gate + 经验规则 + pet.json | 未解锁零文件；解锁后经验累计；`go test ./internal/pet/` 全绿 |
| P2 浮动宠物 | 浮动组件 + 拖动/位置持久化 + 皮肤系统 + 晃动换肤 + 说话气泡 + 取名 + 迷你面板 | e2e：解锁→右下角出现→可拖动→晃动换肤→气泡可见 |
| P3 排行榜 | sponsor 页 tab 切换 + sync 客户端 + 服务器联调 + stale 降级 | e2e：切宠物榜触发同步、断网静默降级、未解锁无 tab |
| P4 预留校验 | skills/battle 占位 + feature 开关显隐 | 配置开关生效，隐藏 tab 不渲染 |

---

## 14. 测试要点

- **单元**：解锁前订阅回调零副作用；enable 幂等；exp 映射缺省 0 分；冷却去重；每日上限截断；
  会话时长封顶；环形缓冲溢出；HMAC 篡改检测与 bak 回退；同步失败 dirty 保留。
- **集成**：sync 协议字段往返；服务器校验拒绝时旧分不变。
- **e2e**（复用 playwright 体系）：点 6 次关于解锁 → 宠物出现；连接 SSH → 经验增长；
  拖动/晃动换肤；改名；断网排行榜 stale；未解锁用户页面无任何宠物痕迹。
- **性能**：audit 订阅前后 Write 延迟对比（回调 < 1μs）；no-op gate 开销 ≈ 原子读。
