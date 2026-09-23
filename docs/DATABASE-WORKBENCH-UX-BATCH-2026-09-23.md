# 数据库工作台 16 项问题处理记录（2026-09-23 第二轮）

- 来源：用户 2026-09-23 报告的 16 条问题（编号沿用用户原编号）
- 前置事实：用户当时访问的 `127.0.0.1:18092` 由 `D:\kairo\kairo.exe`（**2026-09-22 23:00 构建**）提供，
  不含 09-23 的 30 个提交与本次修复；下列结论均以当前工作树（本轮修复后）为准
- 本轮已修复并验证 12 项；3 项属"读取链路根因"（见 §2，已修但需真实 Oracle 端到端确认）；1 项待继续（#2 仍有单帧闪动）

---

## 1. 逐项状态

| # | 用户描述 | 状态 | 说明 / 证据 |
| --- | --- | --- | --- |
| 1 | LOB 单元格查看报 `目标表未定义主键且无受信任的 ROWID` | **根因已修（待真实 Oracle 确认）** | 见 §2：隐藏 ROWID 探测依赖的 `isOracleHeapTable` 等 7 条 Oracle 查询绑定数不对，go-ora 下 ORA-01008 ⇒ 探测失败 ⇒ 单元格没有 rowid |
| 2 | 从别的页签回到工作台时左右布局会闪一下 | **部分修复** | 已加"同步布局提示 + 首帧遮罩 + 权威值未到时按提示维持布局"三层；探针仍能测到 1 帧 `可见+上下布局`(~10ms)，根因未最终定位 |
| 3 | 左右布局下 SQL 编辑器没铺满、下方有点不动区域 | **当前构建未复现** | 实测：`shell.h = ta.h = 477`、`deadBelowTa = 0`（上下布局拖动后再切也一致）。下方 43–47px 是"事务说明"帮助条，不是编辑区 |
| 4 | 表名联想不连续 / 函数也要联想 | **已修复** | 表名：`FROM ` 空槽给全量列表、1 字符即筛选、持续过滤、Ctrl+Space 强制（上一轮）；本轮新增**函数名**池（`category=functions`，与表名同权重，标签"函数"），实测 `SELECT getC` → `GETCUSTOMERID函数` |
| 5 | Ctrl+滚轮时纵向也跟着滚 | **已修复** | 结果区 Ctrl+滚轮统一 `preventDefault`（挂在整块结果区，含工具栏）；并修掉"结果网格 + 表格"双 handler 叠加导致一次滚动走两倍距离的 bug（实测 5×120 由 1200px → 600px） |
| 6 | NULL 直接显示为空（网格 + 单行记录） | **已修复** | `fmtCell`/`cellText` 不再输出 `NULL`；SQL 导出/INSERT 仍走 `sqlValueLiteral` 保留 NULL 字面量 |
| 7 | 右键单元格增加"查看内容"多行弹窗（可缩放） | **未做** | 需在 `openResultMenu` 增加菜单项 + 可缩放弹窗（`Kairo.overlays.modal` + `resize` 容器），下一轮 |
| 8 | 点函数/存储过程不应展示字段/索引/约束，只留 DDL | **已修复** | 按对象类型分配页签：TABLE→字段/索引/约束/DDL；VIEW→字段/DDL；FUNCTION/PROCEDURE/PACKAGE/TRIGGER/SEQUENCE/SYNONYM→仅"源码/DDL"；并隐藏"查询数据/INSERT 模板"、函数优先展示 `source_text`。实测函数对象：页签 `["源码 / DDL"]`、动作 `["复制 DDL","刷新","编辑源码"]`、含源码正文 |
| 9 | 点表名报 `读取失败 ORA-01008 … not all variables bound` | **根因已修（待真实 Oracle 确认）** | 见 §2：`markPrimaryKeys` 的 `:1` 写两次只传一个值；OCI 按出现次数计绑定、go-ora 按位置只发 len(args) 个 ⇒ ORA-01008 |
| 10 | 网格编辑报 `update/delete 必须提供主键列或 Oracle ROWID` | **根因已修（待真实 Oracle 确认）** | 同一根因：无主键表要靠隐藏 ROWID，而 ROWID 探测/字段元数据都因 ORA-01008 失败 ⇒ 前端拿不到 `identity_policy=oracle_rowid` 与隐藏列，退化到 legacy 路径报此错 |
| 11 | `无法在受限预算内读取目标基表XX的元数据` | **根因已修（待真实 Oracle 确认）** | 该文案是 `prepareGridQuery` 对**任何** Fields/Indexes 失败的兜底措辞，真实错误被丢弃（`gridCachedFetch` 吞错）；真实原因仍是 §2 的 ORA-01008 |
| 12 | 结果每列加浅色竖线 | **已修复** | `.db-table th/td` 增加 `border-right: 1px solid color-mix(--line 55%)`，末列与 spacer 不画；实测 `1px solid`（55% 线色） |
| 13 | 数据源管理页面特别卡 | **已定位现象，未修** | 实测打开动作 124ms 尚可，但打开后 500ms 只出 **10 帧**（~20fps）、`longtask` 0：疑似 features 层 MutationObserver 在管理器打开时反复 install/rebuild。待专项分析（下一轮） |
| 14 | "更多"面板点别处应自动收起 | **已修复** | 新增 `bindToolbarMoreAutoClose()`（capture 阶段 document click，面板内点击不干预原生 toggle）；实测 打开→点外部→收起 |
| 15 | 复制链接/新窗口打开后没有这些 SQL | **已修复** | 根因：SPA 页签管理器在激活路由时把 hash 规范化成 `#/database`（丢掉 `?sql=`），`applyDeepLink` 读不到参数。改为在模块求值期快照初始 hash + 会话恢复覆盖时补写一次；实测新窗口编辑器内容 = 链接里的 SQL |
| 16 | 工具栏重排（导出SQL/执行计划/查找/打开→更多；保存→回滚右侧） | **未做** | 动 4 处 JS 产出点 + 5 套 CSS 栅格变体（含对象树展开/≤1100px/≥1500px/窄列），并影响 `tests/e2e/click-database-oracle.js:129` 等；下一轮按方案实施 |

---

## 2. 关键根因：7 条 Oracle 查询的绑定占位符重复（ORA-01008）

`(owner = :1 OR UPPER(owner) = UPPER(:1))` 这种写法把同一个占位符写两次、只传一个值：

- OCI/ODPI-C 对 **SQL 语句**按"出现次数"计绑定变量（厂商源码注释明确），
  而纯 Go 的 **go-ora 按位置只发送 `len(args)` 个绑定**（只有 `sql.Named` 才展开重复名）
  ⇒ Oracle 回 **ORA-01008 not all variables bound**（`error occur at position:` 后缀是 go-ora 独有）。
- 便携版（`CGO_ENABLED=0`）用的正是 go-ora —— 也就是发到 Z 盘、你现在运行的那条 Oracle 路径。

受影响的 7 处（全部为 09-21 `0ad4405` 引入，早于 09-23 的 30 个提交，故"以前不报"）：

| 文件 | 函数 | 影响 |
| --- | --- | --- |
| `internal/dbconsole/inspect.go` `markPrimaryKeys` | 主键标记（`Fields` 内调用） | **点击对象即"读取失败 ORA-01008"** |
| `internal/dbconsole/inspect.go` Indexes / Constraints | 索引 / 约束页签 | 错误被吞 ⇒ 页签静默为空 |
| `internal/dbconsole/grid_plan.go` `isOracleHeapTable` | 堆表探测 | 隐藏 ROWID 注入被跳过 ⇒ 网格编辑无身份 |
| `internal/dbconsole/lob_locator.go` ×3 | 表 owner / 唯一键列定位 | LOB 定位失败 ⇒ #1 的 400 |

**修复**：每个出现位置改用独立占位符（如 `:1/:2/:3/:4` 各传一个值），语义完全不变、与驱动无关。
**防回归**：新增 `internal/dbconsole/sql_bind_placeholder_test.go`（静态扫描：非 PL/SQL 的 Oracle SQL
字面量里同一 `:N` 不得出现两次；实测能抓住旧写法，70 段字面量全部通过）。

> 待你在真实 Oracle 上确认（3 分钟）：
> 1. 展开对象树点任意表 → 应显示字段/索引/约束（PK 徽标正常），不再报 ORA-01008；
> 2. 无主键表 `SELECT * FROM t` → 开启网格编辑改一格并提交 → 应生成 `UPDATE "T" … WHERE ROWID = :kairo_pN` 且影响 1 行；
> 3. 大字段（CLOB）点"待加载"预览 → 不再 400。

---

## 3. 本轮验证

| 项 | 结果 |
| --- | --- |
| `go build ./...` / `go vet ./...` / `go test ./...` | 全部 exit 0（含新增绑定唯一性测试） |
| `npm test`（21 个前端套件） | 21/21（回退了与既有契约冲突的 `autorun` 改动） |
| 浏览器修复专项（`tmp/db-fixes-verify.js`） | 13/14（唯一失败项 = #2 残留单帧） |
| 左右分栏回归（`tmp/split-layout-verify.js`） | 48/48 |
| 表名/列名联想（`tmp/db-column-completion-check.js`） | 8/8 |

截图：`tmp/split-layout-e2e/shots/`（`14-no-flash`、`15-function-ac`、`16-null-and-lines`、`17-function-object`）。

## 4. 下一轮待办（按建议顺序）

1. **#16 工具栏重排**（方案已定：导出SQL/执行计划/查找/打开→更多；保存→回滚右侧；同步 5 套 CSS 栅格 + 2 个 e2e 断言）
2. **#7 单元格右键"查看内容"**（可缩放多行弹窗）
3. **#13 数据源管理性能**（先定位 MutationObserver/重渲染抖动，再优化）
4. **#2 首帧闪动残留**（需要在 `renderSQL`/`applyLayoutMode` 内部加一次性探针，确认 `persisted.layout` 在第一帧为何仍为 stacked）
5. **#10 加固**（可选）：`prepareGridQuery` 把真实错误带进 `prep.Reason` 而不是统一"受限预算"文案；
   前端在没有 plan/`result_id` 时不要 `planAllows = true` 兜底
