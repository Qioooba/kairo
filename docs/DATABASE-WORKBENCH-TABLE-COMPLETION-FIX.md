# 数据库工作台：表名 / 列名联想修复与优化（2026-09-23）

- 现象：在 SQL 编辑器里写 `SELECT ... FROM ...` 时看不到表名联想（只有关键字偶有提示）
- 结论：**不是今天（2026-09-23）30 个提交引入的回归**，而是 2026-09-02 / 09-09 就存在的三处历史缺陷叠加
- 本次同时补齐了原先偏弱的 `别名.列名` 联想（第 3.6 节）
- 状态：已修复并通过真实浏览器验收（含"元数据预热失败仍可自愈"与"列名联想"用例）

---

## 1. 复现与证据（真实浏览器 + mock 元数据）

诊断脚本：`tmp/db-completion-probe.js`、`tmp/db-completion-retry-check.js`（隔离实例 18099，运行时用发布的 `dist/kairo-v0.20/Kairo_win10.exe -web-dir web`）。

修复**前**的实测（对象池里有 4 张表）：

| 输入 | 结果 |
| --- | --- |
| `SELECT * FROM `（刚敲完 FROM + 空格） | 弹层不出现，0 条 |
| `SELECT * FROM u`（1 个字符） | 弹层不出现，0 条 |
| `SELECT * FROM us`（2 个字符） | 出现 `USER_LOGS / USERS / USING` |
| `Ctrl+Space` | 弹层不出现（快捷键完全失效） |

即：**表名联想机制本身是好的，但触发条件把最常见的使用姿势全排除了。**

## 2. 三个根因

### 根因 1：空前缀直接返回空（`e1d3709`，2026-09-02 引入）
`completionPrefix()` 只在光标前有"标识符字符"时才返回前缀，`suggestSQL()` 遇到空前缀立刻返回空数组。
→ 刚敲完 `FROM `（还没输表名首字母）时**永远不会**弹出表名列表。这是最贴近"看不到表名"的姿势，
主流客户端（Navicat / PL/SQL Developer / DBeaver）在 `FROM ` 后就会给全量表名。

### 根因 2：所有位置统一要求 ≥2 个字符（`e1d3709`）
`suggestSQL()` 开头 `if (!ctx || ctx.prefix.length < 2) return 空`。
→ `FROM u`、`FROM T` 这种"敲了 1 个字母看看有什么"的姿势什么也没有。
表名位置与 `SELECT` 列位置用同一个阈值，等于把表名首字母筛选这条最常用的捷径关掉了。

### 根因 3：Ctrl+Space 是死命令（`20122fe`，2026-09-09 引入）
`database-features.js` 把补全所有权让给 `database.js` 时写了：

```js
if (id('db-sql-ac')) { hideCompletion(); return; }              // maybeContextCompletion 首行
commands.register({ name: 'db.complete', spec: 'Ctrl+Space', when: function () { return !id('db-sql-ac'); }, ... });
```

`#db-sql-ac` 是 SQL 工作区模板里**常驻**的补全容器（只是 `hidden`），所以这个条件在 SQL 工作区恒为
false：命令永不触发，`maybeContextCompletion` 还会顺手把已有的补全收起。
→ 用户最后一次逃生通道（"我按 Ctrl+Space 总该出来吧"）也是黑的。

### 叠加因素：预热失败被完全静默
`warmupSchemaTables()` 捕获异常后不做任何记录/重试；`loadSchemas` 失败时 `#db-schema` 是占位值，
而 `completionExtras()` 会回退到"数据源用户名"这个**从未预热过的键**。
→ 元数据一失败（网络抖动、ORA 权限错误、schema 列表加载失败），对象池永远为空，
   用户只会看到关键字（`FROM us` 只提示 `USING`），且没有任何解释。

## 3. 修复

`web/pages/database.js`：

1. **空前缀在"空表名槽位"给出完整列表**：新增 `sqlEmptyTableSlot()`——光标前最后一个有效 token
   恰好是 `FROM / JOIN / INTO / UPDATE / TABLE / ,` 时才在零输入下弹全量候选（对象优先）。
   特意不做成"只要表名位置就弹"，否则 `FROM T_ORDER ` 之后会再弹一次全表列表，干扰连续输入。
2. **表名位置放宽到 1 个字符**：`suggestSQL()` 改为"表名位置 1 字符、其它位置仍 2 字符"，
   保留列名/关键字的防噪音阈值。
3. **Ctrl+Space 恢复**：页面层新增 `db.complete` 命令（`openCompletion()`），空前缀也强制给全量列表，
   已打开时再按一次收起；同时在 `onWorkbenchKey` 加兜底分支（features 分发器未就绪时也生效）。
   命令优先级高于 features 层那条 `when` 恒 false 的同名命令。
4. **预热失败自愈**：`updateComplete()` 发现"表名位置 + 对象池为空"时调用 `retryTableWarmup()`
   （15s 冷却）补拉一次并重算候选；`warmupSchemaTables(schema, force)` 支持忽略"缓存里是空数组"的失败残留，
   并把失败原因记到 `state.tableWarmupError` 便于诊断。
5. **预热键与查询键对齐**：`loadSchemas` / `db-schema.onchange` 改用 `currentSchema()`（含占位值回退）
   作为预热键，修掉"预热了 A、查询查 B"的错位。
6. **`别名.列名` 联想（本轮追加优化）**：新增 `qualifierCompletion()` 识别 `别名.` / `别名.前缀`
   （也支持 `schema.表.`），并新增 `resolveQualifierTable()` 把别名解析成真实表名
   （`FROM t_order t ... WHERE t.` → `t_order`；直接写表名限定也命中）。
   字段池按 `source|schema|object` 缓存（5 分钟），正在查看的对象（`state.inspect`）零延迟复用；
   首次遇到某张表时异步取一次 `/api/database/metadata/fields` 并在返回后重算候选。
   小写表名会按 `schemaTableCache` 规范化为元数据里的真实拼写（Oracle 常全大写）。
   列名位置**只给字段**，不混入表名与关键字；数字字面量（`1.5`）与字符串内的点号不会误触发。

## 4. 验收（修复后实测）

| 输入 | 修复前 | 修复后 |
| --- | --- | --- |
| `FROM ` + 0 字符 | 0 条 | **12 条，表名排最前**（`T_ORDER / T_ORDER_ITEM / USER_LOGS / USERS ...`） |
| `FROM u`（1 字符） | 0 条 | **5 条**（`USER_LOGS / USERS / UNION / UPDATE`，对象在前） |
| `FROM us`（2 字符） | 3 条 | 3 条（不回退） |
| `FROM t_` | 2 条 | 2 条（不回退） |
| `FROM t_order `（表名后空格） | 无弹层 | 无弹层（不误弹全表列表） |
| `FROM a, `（逗号后） | 0 条 | **有表名**（新增能力） |
| `Ctrl+Space`（弹层已收起） | 不出现 | **出现 12 条**；再按一次收起 |
| `SELECT ` + `Ctrl+Space`（非表名位置） | 不出现 | **强制出现全量候选** |
| 预热首次失败（HTTP 500） | 永远无表名 | 输入表名首字母时**自动补拉成功**并弹出表名，无需刷新 |
| `WHERE t.`（别名 + 点号） | 无候选 | **5 个字段**（`AMOUNT / CREATED_AT / ID / ORDER_NO / STATUS`，只给字段不混关键字） |
| `WHERE t.o` | 无候选（列名要 2 字符） | **只筛出 ORDER_NO** |
| `WHERE t_order.`（表名限定） | 无候选 | **5 个字段** |
| 小写 `from t_order o` + `o.` | 无候选 | 按元数据真实拼写（大写）取到字段 |
| 同一张表再次 `t.` | — | 命中按表缓存，**不再请求** fields 接口 |
| 对象树里查看过该表后再 `t.` | — | 复用 `state.inspect` 字段，**零延迟零请求** |

自动化回归：`web/app.test.js` 新增 16 条断言（表名 8 条 + 列名 8 条：空前缀/1 字符/表名后空格不误弹/逗号后/非表名位置仍 2 字符/
Ctrl+Space 强制/`db.complete` 注册/自愈函数/别名点号只给字段/插入起点不含限定符/表名限定/数字字面量不误触发/字符串内不触发/缓存与解析函数存在），
`web/app.test.js` 29/29 通过；`npm test` 21/21；左右分栏 48/48；Redis 6/6；5 主题 5/5；e2e 1/1；
浏览器列名联想验收 8/8（`tmp/db-column-completion-check.js`），截图
`tmp/split-layout-e2e/shots/11-popup-tables.png`、`12-popup-columns.png`。

## 5. 仍然存在的限制（本次未做）

1. **字段候选只显示列名，不显示数据类型**：`t.` 弹层里的 `small` 仍是"字段"，
   数据源已经能拿到 `data_type`/`primary_key`（对象详情页在展示），要显示类型提示需要把
   `buildSuggestions` 的 item 结构扩成带 `detail` 并同步 `renderComplete`。
2. **表名/字段联想都依赖元数据接口**：失败时会自愈补拉一次并缓存 5 分钟，但连续失败仍是空的，
   没有把"元数据没加载出来"直接显示给用户（目前只有对象树会显示加载失败）。
3. **多处同名/子查询别名不做语义校验**：`resolveQualifierTable` 取 `FROM/JOIN/UPDATE/INTO` 后的
   第一个匹配（别名优先、表名次之），CTE 与子查询别名不解析；解析不到就不出候选。
4. **Redis 工作区不适用**（无 SQL 编辑器）；MySQL 的库名/schema 语义与 Oracle 的 owner 语义不同，
   预热键取 `currentSchema()`（MySQL 取 `database`），如需跨库联想需另做。
