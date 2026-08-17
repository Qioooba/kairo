# 宠物 v2 设计方案（精灵图皮肤 ×62 + 防刷 + 经验权重 + 无上限等级）

> 状态：**开发中**（2026-08-17 启动）。进度见 `docs/PET-SKINS-V2-PROGRESS.md`。
>
> 前置：v1 已实现（见 `docs/PET-FEATURE-DESIGN.md`）。本方案在 v1 骨架上：
>  1. 皮肤系统升级为**精灵图逐帧动画**（方案 C），内置 **62 款**原创 Q 版像素皮肤
>     （含奶龙/卡皮巴拉/咸鱼/噜噜等热门梗宠系列，全部可爱化重画）；
>  2. 修复 v1 的 4 个 bug（监听泄漏 / resize clamp / 气泡溢出 / skin_count 脱节）；
>  3. 体验优化（动画状态机 / 皮肤网格 / 轮询退避 / 边缘吸附）；
>  4. 经验权重重定义 + **等级无上限** + 防刷加强。
>
> 版权说明：所有形象为**原创像素风致敬**（表情包/游戏/动漫风格），命名与形象不复刻任何
> 具体 IP（如"闪电鼠"而非"皮卡丘"、"灰团子"而非"龙猫"），可安全分发。

---

## 1. 皮肤系统（方案 C：精灵图逐帧）

### 1.1 资源规格

| 项 | 规格 |
|---|---|
| 帧尺寸 | 32×32 px（CSS `image-rendering: pixelated` 放大到 64×64 显示） |
| 帧数 | 每皮肤 4 帧（idle 循环：呼吸 squash → 基础 → 眨眼 → stretch） |
| 精灵图 | 每皮肤一张 PNG，4 帧横排 → 128×32 |
| 路径 | `web/img/pet/skins/{skin-id}.png`（skin-id 为语义 id，如 `orange-cat`） |
| 清单 | `web/img/pet/skins/skins.json`：id/名称/分类/帧数/fps/解锁等级 |

### 1.2 生成器（tools/petgen）

皮肤全部**程序化生成**（Node 脚本，零外部依赖，输出 PNG）：

```
tools/petgen/
  gen.js          # 入口：node gen.js → 生成全部 PNG + skins.json
  shapes.js       # 像素绘制库：椭圆/三角/曲线/填充/描边（网格光栅化）
  species.js      # 物种模板：猫/犬/兔/龙（含奶龙 variant）/鸟（含鸭 variant）/
                  #   圆滚/水豚/鱼/幻想 9 大类的形体绘制（Q 版可爱化）
  palette.js      # 调色板与配色方案
  skins.js        # 62 款皮肤定义表（物种 × 配色 × 花纹 × 配饰 × 梗宠变体）
```

- **物种模板**：每物种一个形体函数（头/耳/身/尾/眼/嘴的形状与位置参数），
  皮肤定义 = 物种 + 调色板 + 花纹（条纹/斑点/渐层/纯色）+ 配饰（蝴蝶结/围巾/帽/铃铛）。
- **动画帧**：帧 0 基础 → 帧 1 身体 squash 1px + 耳朵上翘 → 帧 2 基础+眨眼 →
  帧 3 stretch 1px + 耳朵下压。帧差异由形体参数形变驱动，非手绘。
- **可扩展**：新增皮肤 = 在 `skins.js` 加一条定义 + 跑一次 `node gen.js`，零代码改动。

### 1.3 皮肤分类与解锁

62 款 = 10 个分类（2026-08-17 扩充：全物种可爱化重画 + 新增热门梗宠系列）：

| 分类 | 款数 | 解锁 |
|---|---|---|
| 猫系（橘座/蓝猫/奶牛/三花/玄猫/仙女/重点色 + **噜噜**/**黄油小猫**） | 9 | 默认 3 款，其余 Lv4/Lv8 |
| 犬系（柴柴/柯基/二哈/金毛/边牧/泰迪/法斗） | 7 | 柴柴 Lv1，其余 Lv4/Lv8 |
| 兔系（白团/垂垂/灰灰/樱花/月兔/咖啡/薄荷） | 7 | 白团 Lv1，其余 Lv4/Lv8/Lv12 |
| 龙系（**奶龙/草莓奶龙/抹茶奶龙** + 绿龙/红龙/蓝龙/夜翼/冰霜/烈焰/彩虹） | 10 | 奶龙 Lv1，其余 Lv8/Lv12/Lv16 |
| 水豚系（**卡皮巴拉/顶橘豚/泡汤豚**） | 3 | Lv1 / Lv8 / Lv12 |
| 鱼系（**咸鱼/锦鲤**） | 2 | 咸鱼 Lv1，锦鲤 Lv12 |
| 鸟系（团子鸡/鹦鹉/呆萌/蓝雀/云雀/企鹅/猫头鹰 + **小黄鸭/摆烂鸭**） | 9 | Lv4 起 |
| 圆滚系（仓鼠/汤圆/面包/麻薯/饭团/布丁/雪球） | 7 | 默认档 |
| 幻想系（史莱姆/幽灵/星辰/云朵/岩浆/寒冰/机械） | 7 | 史莱姆 Lv1，其余 Lv12/Lv16 |
| 神话专属（金龙） | 1 | Lv20 +（对应 mythic 阶段） |

解锁分布：Lv1×11 / Lv4×9 / Lv8×19 / Lv12×14 / Lv16×8 / Lv20×1（金龙）。

- 热门梗宠均为**原创像素致敬**：奶龙 = 超圆蛋身 + 奶角 + 无辜脸（dragon `variant:milk`）；
  噜噜 = 加胖条纹橘猫（`chub` 选项）；卡皮巴拉 = 面包身 + 眯眯眼 + 顶橘/温泉毛巾梗；
  咸鱼 = 侧视死鱼眼摆烂脸；小黄鸭/摆烂鸭 = 扁嘴圆身（bird `variant:duck`，`sleepy` 下垂眼）。
- 解锁规则写进 `skins.json`（`unlock` 字段），后端下发、前端渲染锁定态。
- 默认解锁 11 款（Lv1）：橘座/蓝猫/噜噜/奶龙/卡皮巴拉/咸鱼/柴柴/白团/仓鼠/汤圆/史莱姆，开局即可换。
- 晃动换肤保留为彩蛋（仅在**已解锁**皮肤间循环）；主要入口为迷你面板皮肤网格。

### 1.4 数据结构变更

- `State.Skin` 由 `int` 改为 `string`（语义 id，如 `"orange-cat"`）；
- 加载兼容：旧 int 按 `{"0":"orange-cat","1":"blue-cat"}` 映射表转换（v1 只有 2 款）；
- `/api/pet/state` 新增 `skins` 数组（全量清单含解锁状态）与 `skin_id`；
- `POST /api/pet/skin` 参数改为 `{skin:"orange-cat"}`，服务端校验存在性 + 解锁等级。

### 1.5 前端动画状态机

Canvas 逐帧绘制（精灵图 drawImage 切帧）替代 `<img>`：

| 状态 | 触发 | 表现 |
|---|---|---|
| idle | 默认 | 精灵图 4 帧循环（fps 6），随机 4~9s 插播眨眼帧 |
| drag | 按住拖动 | 帧循环加速（fps 12）+ 朝拖动方向倾斜 |
| click | 点击松手 | squash & stretch（帧 1→3 快速往返 2 次） |
| levelup | 轮询发现升级 | 跳跃两下（translateY 弹跳）+ 光环粒子 |
| evolve | 阶段变化 | 闪白 3 帧 → 切换新阶段默认皮肤 |
| skin | 换肤成功 | 旋转 360°（保留 v1 rotate 动画叠加） |

- `requestAnimationFrame` 驱动；页面隐藏时暂停（配合轮询退避）。
- `prefers-reduced-motion: reduce` 时静态显示帧 0。

---

## 2. Bug 修复（v1 遗留）

| # | Bug | 修复 |
|---|---|---|
| 1 | document 监听泄漏（mount 多次累积 mousemove 等 4 监听） | 监听挂载移入 mount、unmount 时对称 removeEventListener |
| 2 | resize 不 clamp（宠物可停在可视区外） | window resize 监听 + clampToViewport()，与拖拽共用逻辑 |
| 3 | 气泡 nowrap 长句溢出 | `white-space:normal` + `width:max-content;max-width:220px` |
| 4 | skin_count 与实际文件脱节 → 404 破图 | 改为后端扫描 skins.json 清单下发，删除 SkinCount 配置（兼容读但忽略） |

---

## 3. 体验优化

1. **皮肤网格**：迷你面板内 7×N 网格（分类分组），点选即换；锁定款灰显 + 显示解锁等级。
2. **轮询退避**：`document.hidden` 时退避到 30s，visible 恢复 5s；`visibilitychange` 驱动。
3. **边缘吸附**：拖拽松手时若宠物中心距屏幕边缘 < 24px → 吸附贴边并半隐（opacity .55），
   点击弹回；30s 无操作自动半隐，hover/点击恢复。
4. **进化反馈**：阶段变化时 toast + 气泡 + evolve 动画 + 自动展示该阶段新解锁皮肤网格。

---

## 4. 经验权重重定义

设计原则：**按"用户价值"分级**（核心运维操作 > 高频轻操作 > 被动/低价值），
并压缩档位差距让非重度用户也能升级（配合无上限曲线，见 §5）。

| 档 | op | 经验 | 单 op 日上限 | 说明 |
|---|---|---|---|---|
| 核心-会话 | ssh.shell.start | 6 | 40 | 主入口，会话时长另计 |
| 核心-会话 | ssh.session.time | 1/10min（封顶 8/会话） | 60 | 时长奖励上限 5→8 |
| 高价值 | compare.deep_check | 4 | 30 | 深度比对最重 |
| 高价值 | compare.folder_scan | 3 | 30 | |
| 高价值 | compare.file_diff | 3 | 40 | |
| 高价值 | ssh.sftp.edit.upload | 3 | 80 | 在线编辑回传 |
| 中价值 | ssh.sftp.upload | 2 | 100 | |
| 中价值 | ssh.sftp.download | 2 | 100 | |
| 中价值 | logs.download | 2 | 60 | |
| 中价值 | files.download | 2 | 60 | |
| 轻价值 | http.request | 1 | 150 | 高频调试 |
| 轻价值 | http.case.upsert | 1 | 60 | |
| 轻价值 | credentials.save | 1 | 20 | 低频 |
| 轻价值 | reminder.add | 1 | 20 | 低频 |

- 每日总上限 200 → **300**（皮肤解锁节奏需要）。
- 冷却窗口 60min → **30min**（同 op+target 去重窗口，降低正常使用被误伤）。

---

## 5. 等级无上限

- 曲线：`NextExp(L) = round(80 × L^1.2)`。L1→2 仅 80，L10→11 约 1,266，L50→51 约 10,456。
- 满额天速查：Lv8 ≈ 4 天、Lv16 ≈ 13 天、Lv31 ≈ 42 天、Lv50 ≈ 100 天（每日 300 拉满）；
  普通用户（日均 ~80 分）Lv8 约 2 周，首个进化目标可达。
- 阶段区间调整（神话无上限）：
  `egg 1–3 / hatchling 4–8 / grown 9–15 / mythic 16+`（Lv16 即神话，比 v1 的 31 大幅提前，
  契合"等级无上限、神话只是起点"的定位）。
- Lv100+ 溢出经验转存 `TotalEarned`（排行榜继续可比），不再卡曲线。

---

## 6. 防刷加强

v1 已有：冷却表、每日总上限、单 op 上限、会话封顶、HMAC、服务器重算。
v2 新增三层：

1. **频次异常丢弃**：同一 op 连续两次间隔 < 2s 视为脚本行为，本次不计分且重置该 op
   冷却（惩罚窗口翻倍）。正常人工操作不可能 2s 内重复同一 op+target。
2. **动态冷却**：单 op 当日已计分次数超过其 OpDailyMax 的 60% 后，该 op 冷却窗口 ×2；
   超过 85% 后 ×4（越接近上限越难刷）。
3. **会话真实性**：ssh.session.time 仅当会话时长 ≥ 3min 才结算（秒开秒关不计）；
   结算经验计入独立合成 op，受每日总上限约束（原有）。

> 定位不变：本地规则是"门槛"，排行榜只认服务器重算（board_exp），
> 本地异常刷分不影响榜单公正性。

---

## 7. 代码结构变更

```
tools/petgen/                     # 新增：皮肤生成器（纯 Node，零依赖）
  gen.js / shapes.js / species.js / palette.js / skins.js
web/img/pet/skins/                # 新增：50 张精灵图 + skins.json
  {skin-id}.png × 50 + skins.json
web/pages/pet.js                  # 重构：Canvas 动画状态机 + 皮肤网格 + bug 修复
internal/pet/
  engine.go                       # 改造：频次异常/动态冷却/会话真实性/曲线/无上限
  rules.go                        # 改造：权重表/阶段区间/日上限 300/冷却 30min
  skins.go                        # 新增：清单加载/校验/解锁判断/旧 skin 迁移
internal/httpserver/handlers_pet.go  # 改造：state 下发 skins、skin 接口改语义 id
```

---

## 8. 验收清单

- [x] `node tools/petgen/gen.js` 生成 62 PNG + skins.json，全部可加载、逐帧可播放
- [x] 皮肤网格：锁定/解锁/点选/晃动彩蛋均生效，无 404（e2e S3B-5/6 实测）
- [x] 动画状态机：idle/drag/click/levelup/evolve 六状态可观察
- [x] 4 个 bug 修复验证（监听无泄漏 / resize clamp / 气泡换行 / 清单驱动）
- [x] 经验权重：新表生效，2s 内重复同 op 不计分（防刷单测覆盖）
- [x] 等级无上限：升级曲线连续，mythic 后不再有下一阶段（单测覆盖到 Lv16+）
- [x] `go test ./internal/pet/` 全绿；e2e 冒烟通过（S3A 3/3 + S3B 9/9，2026-08-18，
      含解锁→换肤→经验链路；跑法见 PET-SKINS-V2-PROGRESS.md「e2e 实测记录」）
