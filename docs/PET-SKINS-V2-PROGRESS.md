# 宠物 v2 开发进度

> 设计文档：`docs/PET-SKINS-V2-DESIGN.md`。本文档随开发持续更新。

## 状态总览

| 阶段 | 内容 | 状态 |
|---|---|---|
| P0 | 设计文档 + 进度文档 | ✅ 完成 |
| P1 | 生成器 tools/petgen（shapes/species/palette/skins/gen） | ✅ 完成 |
| P2 | 生成 62 款皮肤 PNG + skins.json（含可爱化重画 + 梗宠系列） | ✅ 完成（浏览器目检 PASS） |
| P3 | 后端 skins.go（清单/校验/解锁/迁移）+ handlers 改造 | ✅ 完成 |
| P4 | 后端 engine/rules（权重/无上限/防刷三层） | ✅ 完成 |
| P5 | 前端 pet.js 重构（Canvas 状态机 + 4 bug 修复 + 皮肤网格 + 轮询退避 + 吸附） | ✅ 完成 |
| P6 | 单测 + e2e 适配 + 文档收尾 | ✅ 完成 |
| P7 | 用户反馈迭代：样式可爱化 + 噜噜/奶龙等热门梗宠 | ✅ 完成 |

## 详细日志

### 2026-08-17（下午 · P7 样式迭代）

用户反馈首轮皮肤"样式很丑，想要噜噜大王/奶龙之类的热门宠物"：

- **全物种可爱化重画**（species.js 重写）：
  - 大眼：3×3 圆角大眼 + 左上白高光 + 右下微光（豆豆眼 → 果冻眼）；
  - 圆胖：身体接近正圆/蛋形、重心下移（chibi 比例），`chub` 选项再加胖；
  - 柔软：粗短四肢、2×2 大腮红、阴影改为描边后绘制（不再污染描边色）。
- **新增 4 个形体**：奶龙（dragon `variant:milk`，蛋身+奶角+无辜脸）、
  水豚（面包身+眯眯眼+鼻吻）、鱼（侧视，`dead` 咸鱼脸）、鸭（bird `variant:duck`，
  扁嘴圆身，`sleepy` 摆烂下垂眼）；新增配饰 `orange`（顶橘）/`towel`（温泉毛巾）/
  `butter`（黄油吐司）。
- **新增 12 款梗宠皮肤**（50 → 62）：噜噜（Lv1 加胖橘猫）/ 黄油小猫（Lv8）/
  奶龙（Lv1）/ 草莓奶龙（Lv8）/ 抹茶奶龙（Lv8）/ 卡皮巴拉（Lv1）/ 顶橘豚（Lv8）/
  泡汤豚（Lv12）/ 咸鱼（Lv1）/ 锦鲤（Lv12）/ 小黄鸭（Lv4）/ 摆烂鸭（Lv12）。
  解锁分布更新：Lv1×11 / Lv4×9 / Lv8×19 / Lv12×14 / Lv16×8 / Lv20×1。
- **前端**：pet.js `CATEGORY_CN` 增加水豚系/鱼系；迷你面板皮肤网格加
  「皮肤（点击穿戴，摇晃宠物快速换肤）」分区标题（同时是 e2e 断言锚点）。
- **e2e 适配**：31-pet.js S3B-5 晃动换肤改为 v2 语义（skin 为语义 id，
  期望值 = 已解锁清单（state.skins 中 unlocked 项）的下一款；删除 skin_count 引用）。
- **回归测试**：新增 `TestLoadSkins_RealManifest`——真实 skins.json 必须 ≥50 款、
  字段合法、每款 PNG 存在（防"清单与文件脱节 → 404"复发）。
- **验证**：`node gen.js` 62 款全量重生成；ASCII 逐像素预览形体正确；
  浏览器目检（蒙太奇 9×7）PASS（Q 版统一/无画崩/描边阴影统一）；
  `go test ./internal/pet/... ./internal/httpserver/... ./internal/config/...` 全绿；
  pet.js / 31-pet.js `node --check` 通过。

### 2026-08-17（上午 · P1-P6 主体开发）

- **P0 完成**：设计文档定稿。要点：精灵图逐帧（32×32×4 帧）、程序化生成、
  等级无上限（80×L^1.2）、防刷三层（2s 频次丢弃 / 动态冷却 / 会话≥3min）、
  权重按用户价值分 4 档、日上限 300、冷却 30min。
- **决策**：皮肤 id 用语义字符串（`orange-cat`）；旧 int skin 加载时映射迁移
  （v1 仅 2 款 → `orange-cat` / `blue-cat`）。
- **P1/P2 完成**：生成器 5 文件（shapes/palette/species/skins/gen）+ 预览工具。
  中途发现并修复 parseColor 不支持 `rgba()` / `transparent` 的 bug（首轮目检全部同色）。
- **P3/P4 完成**：skins.go 清单加载（兜底橙座单款）/ValidateSkin 校验/CatalogView
  下发/MigrateLegacySkin 迁移；engine 经验权重 v2 + NextExp 无上限曲线 + 防刷三层；
  config.SkinCount 废弃（读但忽略）。
- **P5 完成**：pet.js 重构为 Canvas 精灵动画状态机
  （idle/drag/click/levelup/evolve/skin，requestAnimationFrame 驱动）；
  修复 v1 四 bug：document 监听泄漏（unmount 对称移除）/ resize 不 clamp /
  气泡 nowrap 溢出 / skin_count 脱节 404（改清单下发）；
  皮肤网格选择器（分类分组 + 锁定灰显 + 解锁等级角标）、
  轮询退避（失败指数退避 + 页面隐藏 30s）、边缘吸附（贴边半隐 + 30s 无操作自动半隐）。
- **P6 完成**：单测适配 v2（engine/pet/rules/sync/handlers_pet/stats）+
  新增 skins_test（加载/校验/迁移/解锁/下发视图）+ 防刷单测；
  修过的测试坑：feedSeq 全局序号防 target 撞频次检测、sessionAwardCap 8、
  mythicNoCap 语法、迁移后 Sig 置空重签。

## 待办与风险

- [x] 像素生成器输出人工目检（62 款蒙太奇浏览器目检 PASS，2026-08-17）
- [x] e2e 31-pet.js 适配 v2 皮肤接口（S3B-5 已改语义 id + unlocked 清单循环）
- [x] e2e 实测全绿（2026-08-18，见下方日志）：隔离实例 + mock sponsor，
      16 用例 = 15 通过 / 0 失败 / 1 设计性跳过（S7-2 断 mock 需中途杀服务，按其
      注释属脚本外手动验证项）
- [x] config.SkinCount 废弃保留解析（老配置兼容），日志提示忽略
- [x] 设计文档 §8 验收清单逐项核过：62 皮肤生成/网格无 404/六状态/4 bug/
      权重+2s 丢弃/无上限曲线/单测全绿/e2e 冒烟 —— 全部达成
- [ ] 后续若再换皮风：只需改 tools/petgen/{species,skins}.js + `node gen.js`，
      清单/PNG/前后端接口全自动跟进（新增回归测试会校验清单与文件不脱节）

### e2e 实测记录（2026-08-18）

```
# 隔离实例（避免动到真实 data/）+ mock sponsor 后端
mkdir -p /tmp/kairo-e2e-pet/{data,downloads,logs}     # config.yaml: port 18092 + kairo 激活码
go build -o /tmp/kairo-e2e-pet/kairo .
go build -o /tmp/kairo-e2e-pet/mock-sponsor ./cmd/mock-sponsor-server
cd /tmp/kairo-e2e-pet && ./mock-sponsor &             # 18093
cd /tmp/kairo-e2e-pet && ./kairo &                    # 18092
# 关键：KAIRO_RUN_DIR 必须指向实例运行目录（31-pet.js 用它定位 data/pet.json，
# 不设会检查默认 /tmp/kairo-review-2026-08/run → 旧残留导致 S3A-2 误报）
KAIRO_RUN_DIR=/tmp/kairo-e2e-pet BASE_URL=http://127.0.0.1:18092 \
  node tests/e2e/index.js --grep 宠物
# 结果：S3A 3/3 ✅ S3B 9/9 ✅ S7 3/4（1 跳过）✅ —— 0 失败
```
