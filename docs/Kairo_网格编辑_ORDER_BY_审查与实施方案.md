# Kairo 网格编辑：ORDER BY 限制审查与分阶段实施方案

## 0. 结论、基线与验证范围

**推荐取消 `ORDER BY` 对网格更新的硬性限制，同时把编辑资格改为：可信的查询来源、可靠的原始行身份、可精确比较的原值，以及明确的事务状态。不要只删除提示，也不要自动给任意 SQL 拼排序。**

上一份 AI 解释正确地区分了分页次序和行定位，但把“有主键或 ROWID”说成“绝对安全”，低估了当前项目的来源绑定、ROWID 接入和并发校验问题。`ORDER BY 1` 只能通过当前检查，不是可靠的修复方法。

本报告基于 [Qioooba/kairo 的提交 38df3aa604817955f470eaaaa0eb4c29124972c4](https://github.com/Qioooba/kairo/commit/38df3aa604817955f470eaaaa0eb4c29124972c4)，提交时间为 2026-09-21 10:48:39 UTC。已读取远端提交与 Git tree，并将关键代码与远端 Git blob SHA 对照。实施时应重新核对最新 HEAD；本文行号以这个固定提交为准。

已完成：

- 前端查询、单元格编辑、分页、数据源切换、增删队列、更新提交链路审查。
- Go 查询、分页、元数据、网格 SQL 构造、事务和 HTTP 接口审查。
- 两组仓库 JavaScript 回归测试及前端语法检查。
- 提取当前源码函数进行定向模拟，验证跨数据源请求组装、编辑入口不一致、分页取消状态、Schema 来源及 ROWID 解析问题。
- 核验 Oracle、MySQL 及 PL/SQL Developer 的相关官方资料。

**验证边界：定向模拟的 DOM 和 API 均为替身，没有连接真实业务数据库，也没有实际写入数据。本环境没有 Go 工具链，因此没有执行 Go 测试、Oracle 11g 集成测试或完整浏览器端到端测试。没有修改产品代码或提交仓库。** 模拟证明的是当前代码能形成问题状态/请求；数据库是否真正误写，还取决于目标库存在相应数据、账号权限和原值条件是否匹配。

## 1. 对上一份 AI 方案的逐项判断

| 原主张 | 判断 | 应采用的表述 |
| --- | --- | --- |
| 行定位与分页排序应分开 | 正确 | 排序处理浏览次序，定位器决定回写哪条记录。 |
| 单表结果有主键或 ROWID 就足够安全 | 不完整 | 还必须验证真实表/列来源、原始定位值、可更新列、并发比较和事务结果。 |
| 当前底层已有绝对安全的主键防护 | 不成立 | 当前有参数化、原值条件和行数检查，但主键列表由客户端提供，原值可缺省，真实来源未由查询上下文约束。 |
| PL/SQL Developer 任何单行 SELECT 都可改 | 过度概括 | 原厂手册同样要求结果可更新；包含 ROWID 或使用 FOR UPDATE 是其提供的方式。 |
| ROWID 是永久、绝对可靠的身份 | 不成立 | ROWID 可变化或被重用，必须带表上下文和原值/版本条件。 |
| 加 ORDER BY 1 就解决问题 | 不成立 | 第一列未必唯一；即使按唯一键排序，也不能防跨次分页期间数据变化造成偏移。 |
| 删除硬限制，改为提示即可完成 | 方向对，实施不足 | 删除限制必须与来源绑定、统一能力检查、原值和状态回归一起交付。 |

MySQL 官方说明，相同排序值的记录相对次序仍可变化，应使用额外唯一列形成确定次序。[MySQL：LIMIT Query Optimization](https://dev.mysql.com/doc/refman/8.0/en/limit-optimization.html)

Oracle 11.2 明确说明 ROWID 不应作为表主键，删除后可能被分配给后来插入的记录。[Oracle 11.2：ROWID Pseudocolumn](https://docs.oracle.com/cd/E11882_01/server.112/e41084/pseudocolumns008.htm)

PL/SQL Developer 14 原厂手册的 7.6 节说明 ROWID、FOR UPDATE 与可编辑结果的关系；7.1 节描述分批续取和刷新。其行为可以参考，但不能据此推断闭源客户端所有版本、所有场景的内部游标实现。[原厂用户手册，7.1 / 7.6](https://origin2.cdn.componentsource.com/sites/default/files/resources/allround-automations/718836/plsql-developer-manual-14.pdf)

## 2. 当前代码的关键发现

### F01：ORDER BY 是前端入口条件，而且不同入口执行不一致

`web/pages/database.js`：

- 2603–2605：从关闭切换到开启时，遇到 `summary.ordered === false` 直接返回。
- 1557–1561：`getGridContext().editable` 把 `isOrdered` 算入资格。
- 1455–1465：`startCellEdit()` 检查账号、编辑开关等，但没有统一使用上述能力判断，也没有阻止查询在途。
- 1577：`commitPendingEdits()` 只要求 context 存在，没有检查 `context.editable`。
- 3919–3941：重跑查询清空结果并递增序号，但不重置编辑开关。

定向模拟已验证：当编辑开关保留为开启，`ordered=false` 且查询 controller 存在时，单元格仍能编辑，并能组装网格提交请求，即使 `getGridContext().editable` 已经是 false。

这说明排序门槛目前连统一的交互规则都不是。修复时应让按钮、双击、设 NULL、单行面板、新增/删除、应用和提交统一使用同一个能力判断。

[源码：编辑与提交](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L1455-L1638)；[源码：排序门禁](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L2600-L2611)

### F02：切换数据源后保留旧结果，提交却使用新数据源

`database.js:1940–1967` 切源时处理部分未提交状态，然后重绑当前页签数据源并调用 `renderWorkspace(true)`。已有结果可能保留。`effectiveSource()` 读取当前页签绑定的数据源；`getGridContext()` 和 `commitPendingEdits()` 都调用它。

已模拟出的流程：

1. 从 A 数据源查出 `APP.T`，结果包含 `ID=1, NAME='from-a'`。
2. 此时没有草稿或待提交事务，切到 B 数据源。
3. 页面仍保存 A 的结果，但页签 sourceId 已经是 B。
4. 修改旧结果中的 NAME 并提交。
5. 请求变成 `source_id=B`，原值仍是 A 的 `ID=1, NAME='from-a'`。

**如果 B 中也存在同表、同键且参与比较的旧值相同的记录，当前后端的一行影响检查可能允许对 B 的修改。** 这是业务可靠性问题，不应描述成未授权用户绕过权限：接口已有管理员与数据源权限检查。

修复：结果必须保存不可变来源，提交使用该来源；切源撤销旧结果的编辑资格，终止/作废旧请求，不能让保留的旧结果获得新数据源身份。同一个 sourceId 的连接配置被改成另一数据库时，也须通过项目已有的来源指纹检测失效。

[源码：切源](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L1940-L1967)；[源码：当前源与提交](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L1545-L1605)

### F03：Schema 下拉框会被当作实际查询对象的 Schema

`runQuery()` 在 3922 行记录 `resultSchema = currentSchema()`。但 3983 行查询请求并不携带该 Schema；后端 `databaseQueryRequest` 也没有把这个浏览器下拉框转换成查询会话的 CURRENT_SCHEMA。

示例：连接实际查询 `APP.T`，元数据浏览下拉框却选了 `OTHER`，用户执行未限定对象名的 `SELECT ID, NAME FROM T`。前端随后可能把回写目标解析为 `OTHER.T`。定向模拟确认了该 context 生成行为，真实数据库结果解析仍需 Oracle 集成验证。

修复：由执行查询的后端解析实际 owner/catalog/table。未限定名要按执行连接的真实环境解析；同义词、视图和跨库对象只有能证明底层来源时才开放。不允许用“Schema 下拉选了什么”猜写入目标，也不能无条件假设 CURRENT_SCHEMA 永远等于登录用户名。

[源码：查询准备与请求](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L3919-L3989)；[源码：查询请求结构](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database.go#L408-L421)

### F04：ROWID 有 SQL 构造支持，常用更新链路没有完整接入

`grid.go` 支持 `UseRowID / RowID`。但单元格更新在 `database.js:1596` 仅发送 `original/key/primary_key`，没有构造对应 ROWID 字段。

增删模块虽然在发送处有 `rowid/use_rowid` 字段，当前普通行弹窗入队时也未完整提供该身份。定向测试还发现，示例 `SELECT t.*, t.rowid FROM emp t` 被当前前端 `resolveGridTarget()` 拒绝。

LOB 重写里确实存在隐藏 ROWID，但它受专门的 SQL、对象类型与 Fast 模式路径限制；无 LOB 表直接返回不重写。因此不能当成所有查询都已有隐藏行身份。

修复：把网格身份元数据与 LOB 预览拆开管理。Oracle 的真实 ROWID 属于行元数据，不是普通可编辑列，不写进 SET，也不当作用户列名原样参与 original 条件。普通键缺失时可提供隐藏 ROWID，但必须在实际返回这批结果的同一次查询中取得。

[源码：更新请求](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L1583-L1600)；[源码：前端单表识别与增删](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/workbench/database-features.js#L1313-L1468)；[源码：LOB 重写边界](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_projection.go#L233-L245)

### F05：后端接受客户端声明的主键，原值比较可以缺省

已有值得保留的机制：参数化值、标识符校验、管理员权限、数据源只读限制、用户与页签事务隔离、逐条行数检查，以及发生异常时回滚。

缺口是：

- `PrimaryKey/Key/RowID/Original` 均可由请求给出。
- `ApplyGridMutations()` 不重新确认这些列确实构成目标表的完整有效主键。
- `buildGridWhere()` 只检查请求有键列与键值；还接受所谓主键值为 NULL。
- `Original` 缺省时明确保留 key-only 更新语义。

因此，一行影响检查只能说明该 SQL 匹配了一个目标，不能证明它就是原结果中的那条记录，也不能保证他人修改未被覆盖。当前前端解析器已经拒绝普通列别名，不能将 `other_id AS id` 冒认主键写成当前 UI 已实测可利用；它应作为后端和未来扩展解析的负例测试。

修复：服务端编辑上下文决定真实对象、键和可写列；网格 UPDATE 必须按规定发送并验证原值/版本。把“用户手工输入任意 SQL”的能力与“结果网格保证正确定位”的契约分开。

[源码：网格请求与 SQL](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/grid.go#L16-L37)；[源码：定位与可选原值](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/grid.go#L207-L295)；[源码：接口已有权限](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database_v2.go#L118-L152)

### F06：页码、草稿和增删队列存在不同生命周期

`installDatabasePagination()` 先更改页码，再进入 `runQuery()` 的放弃草稿确认。定向模拟发现：用户取消确认后，页码已从 1 变为 2，实际行和 summary 仍是第 1 页。

单元格更新使用页签 `dirtyCells`；增删模块另外维护 `state.gridMutations`。后者的 contextKey 包含 source/session/schema/table/sql，却不包含查询执行实例或参数值。相同 SQL 带不同参数、重新执行、分页以及页签关闭，都需要统一的草稿处理，不能只清其中一套。

定向模拟还确认：只有待删除队列时，主页面的 `hasPendingWork()` 返回没有待处理内容；主提交/回滚不处理该队列；关闭页签没有相应确认。同 SQL/session 重新执行后，旧删除仍会被视为当前待变更。旧删除携带的仍是原 ID，不能把这个问题误称为按新行号自动删除另一行；问题在于操作的归属与当前结果、按钮语义已经分离。

修复：任何替换结果操作，先形成候选目标，处理全部待提交变更，再修改已生效 page/SQL/source 状态。初版允许本地排序和过滤；同一数据源内翻页，可采用“应用到当前事务后继续、放弃本地草稿后继续、取消”，暂不实现跨页草稿。应用成功后仍保持未提交事务，不能顺手 COMMIT 此页签先前的其他 SQL。切数据源则必须明确处理旧数据源上的完整事务：由用户选择提交、回滚或取消切换，不能把事务转移到新源。增删改可以保留各自 UI，但必须共用页签变更协调器。

同一批结果内，本地排序当前只改变索引视图，没有重排底层 rows；该行为已验证，应保留。不能笼统宣称当前一排序就会错行。

[源码：分页](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L4073-L4140)；[源码：增删上下文与队列](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/workbench/database-features.js#L1347-L1357)

### F07：精确值往返和 MySQL 零行语义需同时补齐

查询 `normalizeValue()` 将 int64 保留为 JSON number，浏览器可能在解析时丢失精度。实际 Node 验证：JSON 中的 `9007199254740993` 被解析成 `9007199254740992`。输入端 Go `UseNumber()` 不能恢复早已丢失的位数。

日期时间被格式化为字符串，也不能不分数据库类型就拿展示字符串作为 Oracle 原值绑定。RAW、DECIMAL、NUMBER、NULL、空字符串、时间精度均需定义无损编码。

MySQL 当前连接没有配置 `ClientFoundRows`。驱动默认返回 UPDATE 实际改变行数，匹配一行但数据库认为值没变时，可能返回 0；当前统一当并发冲突并回滚。

修复：身份和 original 与展示值分离；按数据库类型编码与绑定。MySQL 可在现有工作台连接配置中开启 `ClientFoundRows=true`，并回归普通 DML 行数展示。必须使用既有页签事务所用连接，不能临时换另一连接执行网格写入。不要将所有 0 行当成功。

[源码：值归一化](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/query.go#L937-L955)；[源码：MySQL 连接配置](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/manager.go#L323-L341)；[Go MySQL Driver：clientFoundRows](https://github.com/go-sql-driver/mysql#clientfoundrows)

### F08：当前失败回滚整个页签事务，返回与 UI 必须如实表达

`ApplyGridMutations()` 任一项异常会回滚整个页签事务，可能包括此前已执行但尚未提交的手工 SQL。回滚后，前面条目的 `succeeded` 和累计行数没有完整改写，后面尚未执行条目也可能没有明确状态。

此外，网格请求的 `commit=true` 路径没有像单独事务接口那样完整返回结构化 `OUTCOME_UNKNOWN`。前端单元格更新主要走先应用、再事务提交，其他入口和 API 调用仍应统一。

修复：保留当前手动页签事务语义；先验证整批输入，再执行。回滚后区分 `rolled_back / conflict / skipped`，标注 `rollback_scope=session`，UI 同步说明该页签此前未提交 SQL 也被撤销。若将来要只撤销当前批次，应真正实现 savepoint，而不是只修改文案。COMMIT 确认丢失必须沿用项目已有未知结果机制，禁止自动重发写入。

[源码：事务应用与回滚](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/grid.go#L298-L378)；[源码：事务终态](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/transactions.go#L210-L270)

## 3. 推荐产品行为

| 查询/操作 | 推荐行为 | 必要条件 |
| --- | --- | --- |
| `SELECT * FROM EMP WHERE EMPNO=7369`，EMPNO 为真实主键 | 可直接编辑，不要求排序 | 后端确认实际目标、完整原始键、可写列与原值。 |
| 多行单表查询包含完整主键、没有排序 | 可编辑已加载记录 | 有跨页时给轻量提示；不按页码或行号回写。 |
| 按 STATUS 等非唯一列排序 | 可编辑，但不宣称分页完全稳定 | 排序能力与编辑能力分别描述。 |
| 只返回一行，但没有可信身份 | UPDATE/DELETE 保持只读，或获取隐藏身份后重新显示结果 | “现在只有一行”不能证明条件唯一。 |
| 联合主键只投影一部分 | 获取缺失身份后显示新结果，或暂为只读 | 不得把缺失键忽略。 |
| Oracle 无主键普通堆表 | 后续支持隐藏 ROWID | 对象支持、原始 ROWID、原值条件齐全。 |
| MySQL 无主键但有受支持唯一键 | 后续可编辑 | 完整、有效、非空且可无损比较的唯一键。 |
| 无主键且没有可用唯一定位 | UPDATE/DELETE 只读 | 不退化成“整行值匹配再取第一条”。 |
| JOIN、聚合、DISTINCT、UNION、无法确认来源的视图等 | 第一阶段只读 | 以后需要独立的列来源和更新语义设计。 |
| 新增行 | 按目标表插入能力判断 | INSERT 不需要已有行身份；仍校验列、类型、默认值、权限。 |
| 查询正在执行、结果有错误或已失效 | 禁止基于该结果新增编辑/提交草稿 | 元数据和身份能力尚未就绪。 |

推荐提示：

- 无序且存在跨页：`当前查询未指定排序，跨页浏览可能出现重复或遗漏；已加载记录按原始身份提交。`
- 缺定位能力：`当前结果缺少可用于定位的完整主键或受支持的行标识。`
- 结果来源改变：`数据源已改变，请在当前数据源重新查询后编辑。`
- 原值冲突：`该记录已变化或不存在，本次未提交。请刷新对比后决定是否重新修改。`

只有一页结果时，不必反复弹无排序告警。不要强迫用户猜应补哪句 ORDER BY。

## 4. 推荐架构：轻量编辑上下文与原始行身份

### 4.1 后端统一给出能力，不让每个 UI 各猜一次

建议新增受限的 `GridEditPlan / ResultEditContext`。命名可按仓库习惯调整，语义必须稳定。

```text
ResultEditContext
  resultId              每次查询执行独立，不能只用 SQL 文本哈希
  principal/sessionId   复用现有用户和页签隔离
  sourceId/fingerprint  固定本次查询的真实数据源配置
  executedSql/params    绑定本次实际 SQL 与参数，而非当前编辑器内容
  actualObject          后端确认的 catalog/schema/base table/object identity
  columnBindings        结果列 -> 真实基表列；可写/只读；类型与精度
  identityPolicy        pk / unique / oracle_rowid / none
  capabilities          canInsert / canUpdate / canDelete 及原因
  comparisonPolicy      哪些原值参与比较，是否使用可靠版本列
  pagination            是否显式排序、是否证明全序、是否固定快照
  lifecycle             loading / ready / stale / closed

RowIdentity
  rowRef                属于本 resultId 的稳定引用
  originalLocator       查询时原始完整键或 ROWID
  originalValues        查询时无损值；与展示字符串分离
```

推荐先用有上限、有失效时间的轻量上下文/原始身份记录，实现于 Manager 的结果登记层。不要为了登记可编辑结果就调用 `transactionFor(..., true)` 打开数据库事务。查询 cursor 结束后仍应释放；上下文只保存必要元数据和已加载页的原值，不缓存整张表或完整 LOB。

初版只在受支持、准备编辑的结果上维护必要数据，设置每用户/页签容量和总字节上限；失效就要求重新查询。项目已有来源指纹、事务终态和 LOB 签名基础可借鉴，但不可把 LOB 业务 token 直接当网格写权限。若后续使用签名 rowRef 减少缓存，签名必须绑定用户、源指纹、执行实例、对象和原始值摘要。

这套上下文是正确性约束，不是数据库权限的替代；每次写入仍执行既有服务端权限检查。

### 4.2 提交不重新推断目标

建议请求形状如下，字段名是实施建议：

```json
{
  "result_id": "result-instance-id",
  "session_id": "existing-tab-session",
  "mutations": [
    {
      "action": "update",
      "row_ref": "row-ref-from-that-result",
      "values": {
        "STATUS": { "kind": "string", "value": "1" }
      }
    }
  ],
  "confirm": false
}
```

服务端用 context 得到真正目标、键、类型和原值。兼容保留 `source_id/schema/table/primary_key` 时，也必须与 context 一致，不允许这些字段覆盖计划。并非一定要一次删除旧 API 字段，但网格入口的安全语义要统一，不能让新旧入口分别绕过。

对同一 rowRef 的多次单元格编辑合并为一条 mutation；显示行号只用来渲染，不承担身份。原始键不能被新值覆盖。初版可禁改定位键；后续如支持改主键，应当 `WHERE 原主键`、`SET 新主键`，提交后重新取得身份。

应用/提交后，应按原身份回读真实新值或刷新结果并重新建立身份。触发器、数据库归一化、生成列以及查询过滤条件变化，都可能使数据库中的最终值不同于前端 `newVal`。不要只覆盖显示数组便把旧快照当新快照继续编辑。对未知提交结果，要完成针对目标的核对或明确的恢复流程；普通无关 SELECT 成功不能自动证明先前写入已成功或已回滚。

### 4.3 查询解析明确支持范围

一期至少覆盖以下组合：

```sql
SELECT * FROM EMP;
SELECT * FROM EMP WHERE EMPNO = :empno;
SELECT EMPNO, ENAME FROM SCOTT.EMP WHERE EMPNO = 7369;
SELECT t.EMPNO, t.ENAME FROM SCOTT.EMP t WHERE t.EMPNO = 7369;
SELECT t.* FROM SCOTT.EMP t WHERE t.DEPTNO = 20 ORDER BY t.EMPNO;
```

基于完整分词/受限语法分析，确认一个真实基础表与直接列投影；字符串、注释、括号和 Oracle q 引号不能扰乱识别。WHERE 中无法支持的复杂结构可只读，不应通过字符串替换猜测安全性。

**不要直接声称复用现有 `parseSafeSingleTableQuery()` 就完成。** 该函数当前仅支持很窄的 `SELECT [alias.]* FROM [owner.]table [alias]`，实际 WHERE/ORDER BY 查询会被拒绝。建议独立提取网格解析，避免修改 LOB 路径时造成联动回归。[源码：现有窄解析器](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_projection.go#L22-L147)

别名映射、表达式、窗口函数、同名重复列、视图、同义词、数据库链接和 IOT 等，只有明确实现对应能力才逐步开放。第一阶段宁可说明具体只读原因，也不要将 `AS ID` 当真实主键。

### 4.4 原值冲突检测

示例 SQL 仅用于说明；实现必须按真实列类型绑定：

```sql
UPDATE "SCOTT"."EMP"
SET "SAL" = :new_sal
WHERE "EMPNO" = :original_empno
  AND "SAL" = :original_sal;
```

原值为 NULL 时使用 `IS NULL`，区分“没有提交原值”与“原值确实为 NULL”。原值 100 被另一用户改为 150 后，旧条件不再匹配，不能去掉原值条件重试。

默认保留并规范现有“比较已读取且可精确比较的真实列”策略，至少覆盖所有被修改字段。明确展示并发比较范围：若只投影了部分列、LOB 未参与比较，就不能声称整行所有变化都能发现。DELETE 应有明确快照范围或可靠版本规则，不允许静默省略保护。

如业务表已有由所有写入方可靠维护的版本列，可配置使用；不要按字段名猜测 UPDATE_TIME 一定是版本，也不要为此次修复擅自更改用户业务表。不要默认 `ORA_ROWSCN` 是所有 Oracle 11g 表均精确的行版本。

原值条件与 UPDATE/DELETE 必须在同一条语句中判断，或在同一事务内加锁校验后立即写入；不能独立 SELECT 检查通过、释放连接后再写。

[Oracle：并发与丢失更新](https://docs.oracle.com/en/database/oracle/oracle-database/19/cncpt/data-concurrency-and-consistency.html)；[Oracle：OWA_OPT_LOCK 的 Web 乐观锁机制](https://docs.oracle.com/en/database/oracle/oracle-database/21/arpls/OWA_OPT_LOCK.html)

### 4.5 精确类型

| 类型/用途 | 建议表示与处理 |
| --- | --- |
| 主键、唯一键 | 保存原始数据库类型和值；不从格式化单元格恢复。 |
| 大整数、NUMBER、DECIMAL | 十进制字符串与类型信息；绑定前按元数据无损解析，不能中转 JS Number/Go float64。 |
| 普通字符串 | 保留原值，不随意 trim；是否大小写等价服从数据库和选定策略。 |
| Oracle DATE / TIMESTAMP | 明确时分秒、小数秒和时区语义，由驱动按类型绑定，不依赖 NLS 隐式解析。 |
| NULL 与空字符串 | 明确传输 null；Oracle 空字符串语义按数据库处理并回读确认。 |
| RAW / 二进制键 | 明确 base64/hex 编码并在服务端还原二进制，不能比较预览文字。 |
| LOB / 截断预览 | 原型网格维持只读或专门编辑器；不能把预览当完整值提交。 |

初版未覆盖的类型逐列标只读；不因复杂字段存在就无必要地禁止其他能可靠定位和比较的普通列。但如果该类型属于必需定位键，整行 UPDATE/DELETE 必须暂时只读。

## 5. Oracle ROWID 如何补齐，才不会产生新问题

优先顺序建议：完整有效主键 → 已验证的完整非空唯一键 → 受支持 Oracle 基础表的 ROWID。第一版可以先仅支持主键，再扩展后两种。

对于没有业务键的普通 Oracle 表，服务端确认简单单表 SQL 后，在实际查询投影里增加专用隐藏定位字段，例如：

```sql
SELECT t.*, ROWIDTOCHAR(t.ROWID) AS "__KAIRO_EDIT_RID__"
FROM APP.LOG_TABLE t
WHERE t.SERIALNO = :serialno;
```

这是受限语法下的示意，不是给任意 SQL 末尾拼文字。要正确处理查询过滤、绑定变量、分页包装和隐藏列名碰撞，不新增业务排序，不让隐藏字段污染导出、SET 或列映射。

**如果旧结果没有取得可靠身份，不允许再查一次 ROWID 列表，然后按旧行号对齐。** 无序、并发情况下两次查询的行顺序可能不同。正确流程是：重新执行获得身份的查询，生成新的 resultId，完整替换屏幕结果，再让用户编辑这份新结果；已有草稿先处理。

ROWID/UROWID 不应硬编码成固定 18 字符假设。IOT 优先使用其主键；尚未实现对应逻辑行标识能力时明确只读。ROWID 也不能解决删除后重用且内容相同的 ABA 场景，不应给出绝对不可能错行的承诺。[Oracle 11.2 ROWID](https://docs.oracle.com/cd/E11882_01/server.112/e41084/pseudocolumns008.htm)；[Oracle OCI UROWID](https://docs.oracle.com/en/database/oracle/oracle-database/19/lnoci/data-types.html)

不要把 LOB 读取中“按标量特征找回 ROWID”的策略直接复制成写入定位规则，尤其不能遇到多条匹配时取第一条。

## 6. 分页应如何处理

### 自由 SQL 工作台

用户未写排序时，保持其查询语义与当前按需读取方式。具备可靠身份就允许编辑；跨页提示可能重复/遗漏。不要默认对两亿行等大表追加无必要排序，也不能假设排序永远走索引、没有成本。

### 表数据浏览器

工具自己生成 SELECT 时，可提供基于真实完整主键的确定排序；已有用户排序时，在可证明安全的简单查询中补唯一键作为最后的决胜列，明确展示采用的排序。此能力是浏览增强，不是网格编辑前提。

### 深分页与并发

后续可以给受控表浏览增加 keyset/seek，改善深页性能与位置偏移。不要把它称为固定快照。

例如按 ID 唯一排序，第一页 `[1,2,3]`；随后有人插入 0，第二页 `OFFSET 3` 仍会出现 3。这个例子证明即使 `ORDER BY ID` 也不能让独立分页查询自动共享同一数据快照。

当前 Oracle/MySQL 分页是在每个请求重建查询，不能把 `summary.ordered=true` 理解为“跨页完全不重不漏”。Oracle 同一次查询的语句级一致性与多次分页请求的快照是不同问题。[源码：分页计划](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/pagination.go#L43-L114)；[Oracle：Read Consistency](https://docs.oracle.com/en/database/oracle/oracle-database/19/cncpt/data-concurrency-and-consistency.html)

不把长时间保留 OCI 游标、FOR UPDATE 锁定所有浏览行、固定 SCN 或跨页草稿引擎作为本次修复的前置条件。它们各有独立的连接、锁、资源和产品语义成本。

## 7. 分阶段实施

### Phase 0：先让现有主键编辑路径可靠，再取消排序硬限制

目标：常见单表、完整主键查询，无 ORDER BY 也能编辑，同时修复已发现的错源、入口和状态问题。

1. 固定查询执行实例与真实来源，加入 source fingerprint 和实际对象信息；不能继续用 UI Schema 猜对象。
2. 建立最小服务端编辑计划，覆盖真实单表列投影、普通 WHERE、可选 ORDER BY；主键来自服务端真实有效元数据。
3. 原始行身份与展示值分离；主键、参与比较的普通数值/字符串/日期实现必要的无损往返，未支持类型明确限制。
4. 统一 `canInsert/canUpdate/canDelete` 与所有入口的状态检查：查询在途、来源失效、transactionBusy、gridEditsStaged、outcomeUnknown 均按定义处理。
5. 切源、改配置、重查、翻页、切每页数量、关闭页签使用统一草稿处理；旧异步响应不能覆盖新上下文。取消操作不得先改页码。
6. 增删队列与更新草稿共用页签和执行实例生命周期，至少统一计数、保护、失效和回滚；无需为了统一而把全部 UI 文件重写。
7. 强制网格原值保护，拒绝键缺失/NULL/伪造列来源；保留参数化、一条一行检查、现有权限和生产确认。
8. 处理 MySQL foundRows/no-op、整页签回滚返回状态，以及网格 COMMIT 的未知结果响应。
9. 上述能力就绪后移除两处 `ordered` 否决；无排序仅作为分页提示。`summary.ordered` 仍可保留兼容，用于描述用户 SQL 是否显式排序。

**验收门槛：完整主键的无序 UPDATE 成功，跨源/跨 Schema 不误写，重复点击/在途状态不形成错误提交，并发覆盖被检测，类型无损，取消分页不改当前结果状态。不能只完成第 9 项就宣布完成。**

### Phase 1：补齐 Oracle 无主键表与其他定位能力

1. Oracle 受支持基础表自动取得隐藏 ROWID，与 LOB 预览解耦，统一接入更新和删除。
2. 缺失主键投影时可补隐藏键；有必要重新查询时完整替换结果，绝不按旧位置拼身份。
3. 支持经过验证的唯一键：完整、有有效唯一约束、各定位值非空、类型可比。表达式索引、可空唯一性等先不泛化。
4. 按需支持真实列别名映射、更多数值时间/RAW 类型与主键变更。每类能力独立测试，不放开全部复杂查询。
5. IOT、同义词、视图和数据库链接等给出明确支持范围。MySQL 只有具备事务保证的受支持表引擎才能承诺批次回滚。

### Phase 2：分页和大表体验优化

1. 表浏览器可选完整键排序，保留用户主排序语义。
2. 对受限查询支持 keyset/seek；自由 SQL 保持兼容。
3. 性能测试冷/热元数据、首包延迟、开启编辑耗时、内存预算与失效回收。
4. 需要跨页编辑时再独立设计基于 rowRef 的草稿管理、冲突提示和资源上限；不是 Phase 0 的默认交付内容。

### 文件级任务映射

| 文件/模块 | 实施内容 |
| --- | --- |
| `web/pages/database.js` | 统一编辑资格；结果来源与 generation；移除排序门禁；冻结原始身份；切源/重查/翻页原子状态；提交/回滚/未知结果 UI。 |
| `web/workbench/database-features.js` | 消费后端计划；增删改共享生命周期；真正接入 rowRef；原值类型信息不在入队时丢失；避免仅以 SQL 文本识别结果。 |
| `internal/dbconsole/query.go` | 生成执行实例与实际来源；返回必要能力和 typed identity；不让展示值承担定位。 |
| 建议新增 `internal/dbconsole/grid_plan.go` | 受限单表分析、真实对象/键元数据、编辑计划与失效规则。 |
| 建议新增 `internal/dbconsole/grid_value.go` | 类型编解码、原值比较输入、精度与 NULL 规则。可按实际规模合并到现有模块。 |
| `internal/dbconsole/grid.go` | 校验计划；只按计划绑定目标和原始键；规定原值；整批预校验；准确逐项终态。 |
| `internal/dbconsole/inspect.go` | 真实完整有效主键/唯一键解析；精确对象和列身份，避免大小写模糊命中多个对象。 |
| `internal/dbconsole/manager.go` | MySQL foundRows 配置及既有同池调用兼容。 |
| `internal/dbconsole/transactions.go` | 复用来源指纹、页签事务、终态和未知结果机制；不为浏览新增长事务。 |
| `internal/httpserver/handlers_database.go` | 查询能力传输、类型和现有事务错误协议衔接。 |
| `internal/httpserver/handlers_database_v2.go` | 网格请求绑定编辑计划；结构化冲突/回滚/未知结果。 |
| `internal/dbconsole/lob_projection.go` | 只在明确需要处共享底层工具；避免网格新解析改变 LOB 既有边界。 |

## 8. 必须执行的验收用例

以下为待实现版本的验收要求，不代表当前全部通过。

| 编号 | 场景 | 期望 |
| --- | --- | --- |
| A01 | 单表完整 PK，WHERE 命中一行，无 ORDER BY | 能开启编辑并只改目标行；无排序弹窗不阻塞。 |
| A02 | 单表多行无排序 | 可编辑当前行；跨页有轻量提示。 |
| A03 | 非唯一 ORDER BY | 编辑可用；不宣称全序或固定快照。 |
| A04 | 结果一行，无 PK/唯一身份 | 不因行数等于 1 就放行 UPDATE/DELETE。 |
| A05 | 完整联合主键 / 缺一个分量 / NULL 键 | 完整通过，缺失或 NULL 拒绝。 |
| A06 | 普通字段伪称 PK；常量/表达式 AS ID、AS ROWID | 后端拒绝错误来源。 |
| A07 | 有序查询开编辑，再执行无序查询 | 新结果按能力重新判断；所有入口一致。 |
| A08 | controller 在途，元数据未就绪，查询中途错误 | 不形成基于半成品结果的提交。 |
| A09 | A/B 均有同表同 PK 同旧值，从 A 查后切 B | 保留结果只读或清空；绝不提交给 B。 |
| A10 | sourceId 不变但连接地址/库配置改变 | 指纹失效，旧身份不能写新库。 |
| A11 | 元数据 Schema 下拉 OTHER，实际查询 APP.T | 回写仍绑定实际对象或明确拒绝；不猜 OTHER.T。 |
| A12 | 修改编辑器文字但尚未执行 | 当前结果仍绑定已执行 SQL，不能按新文本回写。 |
| A13 | 同 SQL、不同绑定参数、重新执行 | resultId 不同，旧增删队列不得重新挂到新结果。 |
| A14 | 本地排序/过滤后改值 | 身份不变；提交更新正确记录。 |
| A15 | 有更新/新增/删除草稿，翻页/换页大小/关页签 | 三类草稿都受到保护；取消不更改生效状态。 |
| A16 | 两会话读同一值，B 先改，A 后交 | A 得到冲突；不覆盖 B，不自动去掉原值重试。 |
| A17 | NULL 原值 vs 未提供 original | NULL 可正确比较；缺少必要 original 拒绝。 |
| A18 | MySQL 等值、类型归一等值、真实变化、已删、原值已改 | no-op 与冲突明确区分，不将所有零行成功化。 |
| A19 | `9007199254740993`、超 int64 NUMBER、高精度金额 | 查、显、改、定位和比较无精度丢失。 |
| A20 | Oracle DATE/TIMESTAMP、NULL/空串、RAW | 不依赖 NLS 隐式转换；受支持类型往返正确。 |
| A21 | LOB/截断文本在结果中 | 预览不被当完整值写回或作不可靠身份。 |
| A22 | 无 PK Oracle 表，补隐藏 ROWID | UPDATE/DELETE 统一支持；身份与结果来自同次查询。 |
| A23 | 原 ROWID 失效/原值改变 | 冲突，不根据旧页码或当前第一行重找。 |
| A24 | 第二条 mutation 冲突，前面有页签 DML | DB终态、rolled_back/skipped、回滚作用域和 UI 一致。 |
| A25 | COMMIT 后确认丢失；重复点击提交 | 明确未知结果或忙状态，不自动重复写入。 |
| A26 | 只读账号/数据源、生产确认、过期 context | 所有入口与后端均执行既有约束。 |
| A27 | INSERT 到无 PK 但支持插入的表 | 依据插入能力处理，不强加已有行定位要求。 |
| A28 | JOIN/聚合/视图/IOT/不支持类型 | 与支持矩阵一致，给出原因，不模糊承诺全支持。 |
| A29 | 大表无序读取、打开编辑、失效清理 | 没有无必要全表排序、全量COUNT、逐行元数据请求或游标泄漏。 |
| A30 | 已有手工 DML，应用网格草稿后翻页 | 保持同一页签未提交事务，不自动 COMMIT。 |
| A31 | 更新触发器改变值，或者更新后不再符合 WHERE | 回读真实值或刷新结果；下一次编辑使用新的身份/原值。 |

真实集成至少覆盖 Oracle 11.2.0.4 与项目实际 MySQL 版本；Oracle 对实际部署的 godror/go-ora 路径分别验证。不要把 SQL 字符串断言当数据库执行结果，也不要把 mock 通过当完整 E2E 通过。

本次已运行命令及结果：

```text
node tests/database-features-unit.js
  database-features-unit: ok
  grid target regression: ok

node tests/database-review-regression.js
  Database review regressions passed
  Bounded export regression passed
  DB-09 outcome_unknown regression passed

node --check web/pages/database.js
  exit 0

定向源码函数模拟
  已确认跨 source 的请求组装
  已确认编辑入口与 context.editable 不一致
  已确认取消翻页后页码与实际结果分离
  已确认 UI Schema 可进入错误的回写 context
  已确认示例 t.*, t.rowid 被当前解析器拒绝
  已确认本地排序保留底层 rows 身份（正确行为）
  已确认删除队列不计入主页面 pending 检查、主提交和主回滚
  已确认同 SQL/session 重查后旧删除队列仍匹配新结果上下文
  已确认只有删除队列的页签关闭时缺少未保存确认

JSON 数值精度实验
  输入 9007199254740993 -> JS解析 9007199254740992
```

## 9. 可以直接交给实现 AI 的任务指令

```text
请按《Kairo_网格编辑_ORDER_BY_审查与实施方案》实施 Phase 0，再实施 Phase 1。

目标：常见单表查询只要能证明目标对象、原始行身份和可比较原值，
即使没有 ORDER BY 也能进行网格编辑。ORDER BY 仅描述分页次序。

先重新核对最新 HEAD，定位本报告固定基线 38df3aa604817955f470eaaaa0eb4c29124972c4
对应代码是否已变化；逐项验证问题，不盲目按行号替换。

必须先设计并实现：
1. 服务端确认真实 source fingerprint、schema/table、列来源、完整有效键的编辑计划。
2. 每次实际查询独立 resultId；原始行身份和原值与 UI 展示值分离。
3. 更新/增删/应用/提交共用资格和草稿生命周期；结果切换、异步、取消状态正确。
4. 原值冲突条件、精确类型、MySQL no-op、页签事务回滚范围和 OUTCOME_UNKNOWN。
5. 最后删除当前前端两处 ordered 否决，用非阻塞分页提示替代。

禁止通过以下方式完成任务：
- 只删除 ORDER BY 提示或只改一个按钮；
- 给任意 SQL 自动加 ORDER BY 1、主键或 ROWID；
- 将只有一条结果当作唯一性证明；
- 只依据客户端传来的主键列名；
- 以当前数据源/Schema下拉值覆盖旧结果的真实来源；
- 按页码、数组下标或重新查询的行序号拼接原身份；
- 用显示文本/JS Number/截断 LOB 充当无损原值；
- 冲突时去掉 original 条件重试；
- 另开数据库连接绕开既有页签事务；
- 将未知提交结果自动当失败重试；
- 为此修改业务表结构或默认长期 FOR UPDATE 锁住浏览结果。

一期保持复杂查询只读，支持范围必须清楚。
Oracle 隐藏 ROWID 必须与结果在同次查询取得；旧结果没有身份时，
重新查询并完整展示新结果后才可编辑，不能按旧行号补身份。
完整实现报告中的验收用例，按阶段提交，说明改了什么、数据库实测结果和未覆盖边界。
遵守仓库 AGENTS.md 的真实模型版本提交信息要求。
```

**最终交付判断：用户无需为了编辑一条已可靠定位的数据再补 ORDER BY；同时，工具能证明提交的确是该查询结果中的那条记录，并按明确范围检测并发变化。**
