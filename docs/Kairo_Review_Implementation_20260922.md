# Kairo 代码审查与优化实施文档

审查对象为 Qioooba/kairo，交付对象为项目维护者及后续执行修复的 AI 开发者。本文围绕当前代码中的真实缺陷给出复现、原因、代码位置、修改方案和验收方法；重点是查询正确性、写入目标、草稿保存、跨页签行为、GBK 路径和升级恢复。

审查基线：`2b14d6774394e048bacb03b2f7044c2a380c48de`。该提交时间为北京时间 2026 年 9 月 22 日 22 时 49 分 43 秒。审查窗口为 2026 年 9 月 15 日至 9 月 22 日，共 40 个提交；比较起点为 `6ddaab255da648edd310f98bc1627c796a83a386`。排除 vendor 和 docs 后，涉及 141 个文件，新增 19385 行、删除 1625 行。这是改动规模统计，不代表逐行穷尽审核的覆盖率。[审查提交](https://github.com/Qioooba/kairo/commit/2b14d6774394e048bacb03b2f7044c2a380c48de)

## 一 审查结论与证据边界

本次整理出 **29 组可行动问题，其中 P1 为 16 组，P2 为 13 组**。P1 表示应在相关功能继续承担真实写入或重要操作前完成修复；P2 表示明确的功能、交互、性能或测试覆盖缺口。没有把未实际观察到的事故标成已发生，也没有把全部缺陷都归为本周新引入。

建议先完成数据库查询规划、网格契约和比较保存一致性的修复，再处理其他交互与性能项。尤其不能只把 `canUpdate` 改成 `can_update` 就发布：这会恢复编辑入口，同时暴露已经确认的别名映射错误。SQL 格式化改变语义、文件取消切换后仍改保存目标、过期锁回收失去互斥，都应以失败回归转绿作为合入条件。

### 已取得的证据

| 验证方式 | 本次结果 | 结论适用范围 |
|---|---|---|
| 当前源码构建 | Linux CGO 构建成功；Windows amd64 CGO关闭的交叉编译成功；go vet 通过 | 可编译性；未运行 Windows GUI，也未覆盖 OCI 构建 |
| 前端现有测试 | npm test 的 12 个测试脚本均通过 | 现有脚本断言通过；其中有 fixture 与真实协议不一致的问题 |
| 当前二进制隔离 HTTP 测试 | 111 项正常断言通过，另确认 1 项 CT01 风险；112 次请求为 1 首页、60 静态资源、51 次 API，涉及 31 个不同 API 路径 | 真实 HTTP 与临时文件行为；不是 111 个独立业务流程，也不是浏览器验收 |
| 数据库前端新增复现 | 8 个失败场景，归并为 6 组问题 | 执行真实 JavaScript 函数和事件处理器，DOM/API 用替身 |
| 比较工作台新增复现 | 7 个行为问题均复现 | 执行真实源码函数，覆盖取消、保存、撤回、隐藏与关闭、虚拟全选 |
| 数据库后端新增回归 | 7 个 TestAudit 保护断言在当前版本失败 | 真实 Go 函数和 database/sql 测试驱动；含交叉别名，未连真实数据库 |
| SFTP WSDL 升级新增探针 | 6 个 SFTP/WSDL 场景，以及升级锁的并发与独立进程验证 | 本地内存文件系统、httptest、临时目录、真实独立子进程 |
| 格式化语义交叉核验 | 两个只读样本在 SQLite 内存库前后结果不同 | 证明格式化不保语义；不代表 Oracle 集成测试 |
| 已有 Go 全量测试 | 未完成完整认证；保留已执行日志并做定向复核 | 不能写成全量通过；环境错误与产品错误在附录区分 |

Go 新增回归的非零退出码是有意保留的缺陷证据：它们断言修复后的正确保护，当前代码无法满足。两个 Node 复现脚本采用相反口径，即断言当前错误行为确实发生，所以当前运行成功；实施时应把它们改写为正确行为断言，不能直接用“复现脚本返回 0”宣称修复完成。

测试环境为 Linux x86_64、Go 1.24.0、Node 24.19.0、npm 11.9.0，Go 依赖使用仓库 vendor，未更新依赖。版本一致性检查通过。源码工作区最终无修改，临时测试通过 overlay 或仓库外脚本引入。

已有 Go 日志在中断前记录顶层 1017 通过、21 失败、5 跳过，子例 259 通过、9 失败；这些都是局部结果。三条数据库 HTTP 诊断失败经对照确认来自缺少系统 keyring 服务：仅给测试注入临时 file 凭据后端后，相同三条全部通过，所以未列为产品数据库缺陷。logquery 的 17 个顶层失败则稳定对应真实 shell 兼容问题；仅在 overlay 把十六进制转义改成八进制后，选定的 23 个顶层运行测试和 9 个子例全部通过，排除的一条旧格式结构断言已在测试记录明确说明。

### 未完成的验证

当前浏览器环境拒绝访问用户提供的 `http://127.0.0.1:18092`，无法连接用户电脑的本地页面；本次没有取得新页面截图，也没有完成真实浏览器点击、像素对齐、鼠标拖拽、焦点或帧率测量。已在隔离环境运行项目二进制和 HTTP 测试。本文的 UI 问题来源于真实事件处理器与 DOM 模型执行；后文的页面步骤与截图名称是后续验收要求。

未连接用户的 Oracle 11g、MySQL、Redis、WAS 或远端 SFTP，未验证 Windows 托盘、原生便笺、原生宠物及 Windows 下的真实进程锁。Oracle 的两个 SQL 改写问题已结合官方标识符与聚合语义核对；错误 SQL 确已生成，具体驱动报错及数据库效果仍需独立测试库补测。

全量 Go 测试曾触发对仓库中固定外部服务器的请求，自动审批因目标和发送内容未经确认而拒绝。此后仅执行已确认使用本地模拟服务、临时目录和受控测试驱动的定向验证。完整历史记录见交付证据包，不将被中止任务的局部结果计为全量通过。

### 真实服务确认的文件保存风险

CT01 已有“源码事件处理器 + 真实 HTTP + 磁盘字节”的交叉证据。隔离目录内 A.txt 为 `AAA`，B.txt 为 `BBB`，两者大小与 mtime 完全一致。读取 A 取得 version 后，按前端错误组合发出 `target=B.txt, content=A edited, expected=A.version`，当前接口返回 HTTP 200。A 仍为 `AAA`，B 已变成 `A edited`，B 的备份仍为 `BBB`。这证明版本校验不能阻止此类前端来源错配；本次被改写的都是新建测试文件。[保存接口](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/httpserver/handlers_compare_workbench.go#L324)

## 二 问题总表

同一根因引发的多个表象按一组计数；网格能力字段与结果列映射以 DBUI-01 的完整接口契约作为一个实施项。其余独立失败条件保留独立编号，便于提交与验收映射。

| 编号 | 优先级 | 问题与直接影响 |
|---|---|---|
| DB-01 | P1 | Oracle ROWID 改写把未引用的小写表名和别名变为区分大小写名称 |
| DB-02 | P1 | COUNT 与 MAX 等聚合 SELECT 被附加逐行 ROWID，合法查询被破坏 |
| DB-03 | P1 | 持有查询连接时再次取元数据连接，单连接冷缓存查询等待自身到超时 |
| DB-04 | P1 | VARCHAR2 日期样式文本被绑定成 time.Time，文本类型和精确值语义丢失 |
| DB-05 | P1 | PK 定位时省略其他唯一键字段的原值，漏掉并发修改冲突 |
| DB-06 | P2 | LOB 非 Fast 分支的行数组缺少编辑计划宣称的隐藏 ROWID |
| DB-07 | P1 | 无主键 LOB 用部分特征取首行，可能显示另一条记录的内容 |
| DBUI-01 | P1 | 网格能力命名断链；结果列与物理列映射缺失，交叉别名可生成指向另一行的 UPDATE |
| DBUI-02 | P1 | SQL 格式化改变注释边界和字符串内容，执行语义改变 |
| DBUI-03 | P1 | 失效数据源页签自动绑定剩余数据源，旧 SQL 和结果仍保留 |
| DBUI-04 | P2 | 大文本阈值清空执行所需绑定参数，短查询也受整个文档长度影响 |
| DBUI-05 | P2 | Ctrl Shift F 被查找快捷键截获，新增格式化快捷键冲突 |
| DBUI-06 | P2 | 查询历史被标到错误数据源并伪标成功，清空后又导回 |
| CT01 | P1 | 取消打开新文件后保存目标已更换，旧内容可写到另一文件 |
| CT06 | P1 | 差异区行内编辑直接 Ctrl S，写请求读取旧正文，最新草稿未提交 |
| CT02 | P2 | 保存期间继续编辑，成功响应没有推进版本，后续保存反复冲突 |
| CT03 | P2 | 后台比较页仍处理全局 Ctrl Z，撤回隐藏页的内容 |
| CT04 | P2 | 左右交换后撤回被自己的文档身份检查拒绝 |
| CT05 | P2 | 关闭比较页后文件夹扫描仍轮询，资源未归属页签 |
| CT07 | P2 | 全选虚拟结果只选当前渲染行，复制内容不完整 |
| OTH-01 | P1 | 升级旧锁回收可删除其他进程的新锁，两个进程同时持锁 |
| OTH-02 | P1 | WSDL 跨源重定向泄露自定义认证头，多跳可重新带回 Authorization |
| OTH-03 | P1 | SFTP 父目录歧义被忽略，写操作擅自选择同显示名目录 |
| OTH-04 | P2 | GBK 目录中新建多层路径丢失原始前缀，创建另一套 UTF8 分支 |
| OTH-05 | P2 | path_id 没有贯通前端与 HTTP，嵌套 GBK 路径的身份本身也不正确 |
| OTH-06 | P2 | ASCII 文件 Stat 与 Open 重复列全父目录，大目录出现平方级枚举 |
| OPS-01 | P1 | 日志搜索依赖 Bash 的十六进制 printf 扩展，dash 上匹配错误 |
| QA-01 | P1 | 普通 Go 测试读取开发机数据源并强行开启 DDL，测试库隔离缺失 |
| QA-02 | P2 | 新测试含硬编码机器路径、错误协议 fixture 和未接入统一入口的脚本 |

各问题的详细章节给出证据级别和提交归属。CT01、CT02、DBUI-03、OPS-01 等属于当前仍存在的历史缺陷；本次复核发现，不将其全部归因于最后一次提交。

## 三 数据库执行与行定位

### DB-01：ROWID 改写破坏 Oracle 未加引号的小写表名与别名

**定位**：[internal/dbconsole/grid_plan.go:195-211](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/dbconsole/grid_plan.go#L195-L211) 丢失标识符是否加引号的信息；`:294-312` 用 `quoteGridIdentifier` 直接包双引号；[internal/dbconsole/query.go:418-420](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/dbconsole/query.go#L418-L420) 将改写用于正常 Oracle SELECT。[internal/dbconsole/oracle_rowid_grid_test.go:22-38](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/dbconsole/oracle_rowid_grid_test.go#L22-L38) 的既有期望字符串已经包含同样错误。

实际函数结果：

```sql
-- 输入
select * from emp
-- 实际改写
SELECT "emp".*, ROWIDTOCHAR("emp".ROWID) AS "__KAIRO_EDIT_RID__" from emp

-- 输入
SELECT t.* FROM SCOTT.EMP t
-- 实际改写
SELECT t.*, ROWIDTOCHAR("t".ROWID) AS "__KAIRO_EDIT_RID__" FROM SCOTT.EMP t
```

Oracle 会把未加引号的 `emp`、`t` 解释成 `EMP`、`T`。后来注入的 `"emp"`、`"t"` 是区分大小写的不同名字，预期会报非法标识符；连只想执行查询的用户也受影响。[Oracle 官方标识符规则](https://docs.oracle.com/en/database/oracle/oracle-database/19/sqlrf/Database-Object-Names-and-Qualifiers.html)

**实施方案**：

1. `ParsedGridQuery` 的 owner/table/alias 改成保留原始 token、解码后的名字以及 quoted 标记的结构；使用相同规则解析原始查询和改写后的引用。
2. 未加引号的 Oracle 标识符统一转大写再引用；明确加双引号的名称保留精确大小写。不要无条件对所有名称 `ToUpper`。
3. 查询目标的 `plan.Schema/plan.Table` 同步使用规范化结果，否则即使查询修好，后端写入仍可能发往 `"emp"`。
4. 修正既有测试的错误期望，不要用原错误字符串继续证明“测试通过”。

**验收**：大写/小写混合的未引号表名、`t`/`T` 别名、`"t"` 真正带引号的别名、带 schema、显式列与 `*`、第 1/2 页分别验证；在 Oracle 11g 中对比原始 SQL 和改写 SQL 的业务列和值。

### DB-02：无 GROUP BY 的聚合 SELECT 被错误增加 ROWID

**定位**：[internal/dbconsole/grid_plan.go:94](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/dbconsole/grid_plan.go#L94) 仅检查 GROUP/JOIN 等关键字；`:136-155` 提取投影后没有验证能否增加逐行标识；`:304-312` 对非通配符投影也直接拼接 ROWID；`query.go:418-420` 没有额外条件。

实测：

```sql
-- 合法原查询
SELECT COUNT(*) FROM EMP
-- 当前返回的改写结果，err == nil
SELECT COUNT(*), ROWIDTOCHAR("EMP".ROWID) AS "__KAIRO_EDIT_RID__" FROM EMP
```

`MAX(SAL)` 同样失败。聚合结果与非聚合的 ROWID 表达式混用且没有 GROUP BY，Oracle 官方定义此情况为 ORA-00937。[Oracle 官方 ORA-00937](https://docs.oracle.com/en/error-help/db/ora-00937/)

**实施方案**：

1. 给 ROWID 改写建立严格的允许语法：单一真实表、直接物理列投影或可确认的 `alias.*`，且不含聚合、DISTINCT/UNIQUE、集合、层次查询、派生来源和不支持的子查询。无法证明允许时保留原 SQL，结果只读。
2. 不要仅追加 COUNT/MAX 黑名单；标量子查询的 FROM、字符串中的 FROM/关键字、Oracle 引号/注释都可能误导目前正则解析。共享一个识别 token、嵌套层级和方言的查询来源解析器。
3. Oracle 普通堆表检查应在决定改写之前完成。当前 `isOracleHeapTable` 在查询执行后分析编辑性时才调用，无法保护原查询；其 `isHeap := true` 且忽略查询错误也应改成未知/false。
4. 保持“支持读取”与“支持编辑”独立。不能为了网格定位让原本合法的 SQL 无法查询。

**验收**：COUNT、SUM、MAX、MIN、AVG、DISTINCT、只读视图、IOT、含 scalar subquery 的 SELECT 均能按原语义查询；不符合编辑条件时只关闭修改能力。纯函数测试之外须用真 Oracle 验证改写 SQL。

### DB-03：查询在持有结果游标时同步获取元数据，连接池可自等待

**定位**：[internal/dbconsole/query.go:351-357](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/dbconsole/query.go#L351-L357) 获取全局并发槽；`:375-387` 开事务；`:435-442` 获取且保留 rows；`:491-498` 在发 meta/rows 前同步分析编辑能力。分析调用 `grid_plan.go:381` 的 `Fields` 和 `:400` 的 `Indexes`；`metadata.go:344-356`、`manager.go:542-551` 又通过池取连接，并再次获取全局槽。

**触发条件**：配置允许的 `MaxOpenConnections=1`，当前目标表字段尚未缓存，有 session_id 的单表 SELECT。当前查询事务占住唯一连接，元数据调用只能等待当前查询结束；但当前查询正在等元数据结束。

实测输出：

```text
elapsed=1.001112787s
error=context deadline exceeded
MaxOpenConnections:1
WaitCount:1
WaitDuration:1.000744043s
```

测试用 1 秒上限，以便快速复现。实际超时时间取决于数据源配置。默认多连接池在并发首次查询占满时也存在同类结构性等待；此扩展情形源代码可推导，未把它表述为已实际压测。

**实施方案**：

1. 在占用流式查询连接之前完成受限元数据规划，或将已缓存的元数据传入纯分析函数，禁止游标打开后再发起嵌套池请求。
2. 元数据规划使用独立的短预算，无法获得时返回只读能力并继续读查询，不把辅助能力分析耗时算成查询首包的无限前置条件。
3. 共享已经获得的全局并发令牌，避免同一请求在元数据链路中重复申请；不要用提高连接数或简单加大超时来掩盖依赖循环。
4. `AnalyzeGridQuery` 拆为纯 `AnalyzeGridQueryWithMetadata` 与外层元数据获取，便于离线验证，并按 source fingerprint/schema/table 缓存与合并并发刷新。

**验收**：冷/热缓存、池大小 1/2/4、同表与不同表并发查询，用户取消与元数据权限不足；首包仍应返回，且池 InUse 最终归零。新增单连接测试通过，再增基于屏障同步的并发回归。

### DB-04：VARCHAR2 文本被“日期识别”擅自变更类型

**定位**：[internal/dbconsole/grid_value.go:14-34](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/dbconsole/grid_value.go#L14-L34) 的 `normalizeTypedParam` 只检查字符串外形；`:73-82`、`:169-178` 对 SET、INSERT、主键和原值统一使用此逻辑。虽有 `GridColumnBinding.DataType`，转换函数没有接收它。

实测：向声明为 `VARCHAR2` 的 `MESSAGE` 写入字面文本 `2026-09-22 12:34:56`，得到 `sql.NamedArg.Value` 的类型为 `time.Time`。输入中的 `/` 还会被替换为 `-` 后解析。

**影响**：文本字段的输入不再保持精确类型和值，Oracle 驱动可能按日期传输，再受数据库隐式转换/NLS 设置影响而改变展示、失去部分格式或报错。日期样式文本如果用作 key/original，也会导致查找或并发检查与原文本语义不同。已证实的是参数类型发生变化，未在真库声称某个固定错误码或落库文本。

**实施方案**：

1. 参数转换使用明确的物理字段类型，例如 `normalizeTypedParam(kind, fieldMeta, value)`；CHAR/VARCHAR/TEXT 保持字符串原样。
2. DATE/TIMESTAMP 才做格式解析，并明确“无时区时间”和带 offset 时间的规则；不要用机器默认时区暗改用户输入。
3. INSERT 也要按服务端字段元数据绑定，不能因为新行没有结果绑定就走字符串外形推断。
4. NUMBER/DECIMAL、大整数、二进制、NULL 分开处理；对于 `json.Number` 保持十进制精度，不经 float64 中转。

**验收**：同一日期样式文本分别写入 VARCHAR2、DATE、TIMESTAMP；文本包括 `2026/09/22 12:34:56`、RFC3339、不同秒小数精度，并验证写入、查询、original 比较、导出恢复均保持语义。

### DB-05：未用于定位的唯一键列被错误省略原值检查

**定位**：[internal/dbconsole/grid_value.go:252-253](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/dbconsole/grid_value.go#L252-L253) 更新时无条件跳过 `PrimaryKeys` 或 `UniqueKeys` 中的列；`:270-271` 删除同样处理。实际用于 WHERE 的定位集合则由 `:187-237` 的 `IdentityPolicy` 决定。

**条件**：计划为 `identity_policy=pk`、真实主键 `id`、另有非空唯一列 `email`，选中的 `UniqueKeys` 包含 email。`AnalyzeGridQuery` 无论最终采用什么定位策略都会读取唯一索引并保存 `UniqueKeys`（`grid_plan.go:398-416`），因此这一计划实际可产生。尤其唯一索引名在 PK 对应索引名前排序时容易选中不同列。

实测请求值 `email=new@example.test`，原值 `email=old@example.test`，key.id=1，生成：

```sql
UPDATE `app`.`accounts` SET `email` = ? WHERE `id` = ?
-- args: [new@example.test 1]
```

预期还应有 `AND email = old@example.test` 的绑定条件。两个客户端读到相同旧值，其中一个先修改 email，另一个仍可以凭 id 覆盖新值，affected=1 无法识别冲突。

**实施方案**：

1. 构造 WHERE 时维护 `locatorColumns`，仅记录本次真正添加到 WHERE 的物理定位列。只有该集合里的列可以省略重复 original 条件。
2. 若采用 ROWID，`locatorColumns` 不包含任何业务列，因此所有被修改的业务列仍必须比较原值。
3. 对 key 中的定位原值与 original 对应原值进行一致性检查，防止同一请求使用两套不一致快照。
4. 在入口统一规范化 action 后，WHERE 构造只读规范化结果，避免大写/带空格 action 通过前半段却跳过后半段原值检查。

**验收**：PK 定位更新普通列/唯一列、unique 定位更新普通列/其他 unique 列、ROWID 定位更新部分 PK 列；两会话并发改同一字段，后提交者必须 conflict，且不会覆盖另一会话的数据。delete 同样比较用户提供的快照。

### DB-06：LOB scanner 没有返回编辑计划所声明的隐藏 ROWID

**定位**：[internal/dbconsole/query.go:448-463](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/dbconsole/query.go#L448-L463) 的 LOB rewrite 分支将 `hiddenRowIDIdx=len(columns)` 交给编辑计划；`lob_projection.go:435-445` 已读取真实 rowID，但 `:456-457` 仅创建业务列长度的 outRow，`:541` 原样返回。普通 rowScanner 在 `query.go:807-824` 会追加 ROWID，两条分支协议不一致。

**条件与表现**：无主键/未投影完整定位键的 Oracle LOB 表执行 `SELECT *`，非 Fast 模式。默认分页接口 `QueryOptions{Fast: page <= 1}`（`query.go:61`），翻到第 2 页可切到 LOB rewrite。后端宣称 `oracle_rowid` 可编辑，但行数组中没有定位值；前端按 `hidden_rowid_index` 或业务列长度尝试取值均拿不到，提交后报缺少有效 ROWID。

实测 scanner：`business_columns=2; advertised_hidden_index=2; actual_row_length=2`。这是独立于前端编辑开关错误的后端数据契约缺失。

**实施方案**：

1. 短期让 LOB scanner 与普通 scanner 返回完全相同协议，在行尾追加真实 rowID，保证摘要下标等于最终序列化后的下标。
2. 更稳妥的协议是将身份元数据独立成 `row_refs` 或按行绑定的服务端令牌，使业务列数组长度恒等于业务列数。若选择升级协议，需要原子更新两条 scanner、分页、前端 grid/LOB 消费端和 export 消费端。
3. 只有确实写入当前行身份载荷时才声明可编辑；不要仅因为改写计划里存在 rowID 就认定前端已经收到。

**验收**：无 PK 表分别含 0/1/2 个 LOB，第一页 Fast 和第二页 normal、显式 normal；检查列名保持纯业务列、每行 ref 有效、更新/删除准确作用于选中行、导出不泄露辅助字段。

### DB-07：无主键 LOB 的特征回查会把相似日志当成同一行

**定位**：[internal/dbconsole/lob_locator.go:157-190](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/dbconsole/lob_locator.go#L157-L190) 跳过 NULL/空值等信息；`:203-205` 只留前 6 个候选字段；`:233` 强制 `ROWNUM <= 1`；`:238-240` 使用 QueryRow，完全不检查候选匹配是否唯一。`handlers_database_lob.go:266-275` 在无 PK/UK 时直接采用此结果签发 token。

前端链路也仍会走到这里：[web/pages/database.js:1511-1519](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/database.js#L1511-L1519) 从业务列收集标量 keys；`:1555-1557` 只读取 LOB 对象的 `rowid`。Fast 模式 `query.go:726-748` 创建 lazy LOB 对象时未设置 rowid，普通 rowScanner 只把隐藏 ID 放在行末，前端 LOB 请求没有读取这个位置。因此即使查询实际上已经带回隐藏 ROWID，按需 LOB 加载仍可能走特征回查。

**最小数据场景**：无主键表 `HTTP_REQ_LOG(TAG VARCHAR2(...), BODY CLOB)` 有两行，TAG 都是 `same`，BODY 分别是 `first payload`、`second payload`。用户点击第二行 BODY，请求 keys 仅有 TAG=same。回查 `WHERE TAG=:value AND ROWNUM<=1` 得到某一首行，于是可能展示/下载 first payload。也可以由第 7 个标量字段不同但前 6 个字段相同触发。

**验证级别**：这条由实际完整源码调用链与 SQL 语义确认；没有声称在真实 Oracle 页面点击过第二行。应作为真库验收的重点。

**实施方案**：

1. 优先把原查询的真实 row ref 传入 lazy LOB token 请求；服务端绑定 source fingerprint、owner/table/column、事务会话和过期时间，避免前端重新猜行身份。
2. 如果确需旧客户端 fallback，最多查 2 行并显式区分 0/1/多行；多行返回“无法唯一定位，请重新查询”，不得静默取第一行。
3. 不把“非 NULL 的前六列”当成唯一键。使用元数据确认的完整 PK/UK，或完整受控的可比较快照；NULL 用 IS NULL 参与比较。
4. 如果查询属于未提交事务，定位与 LOB 读取要使用同一事务快照；当前 ResolveRowIDByKeys 的独立池连接看不到本会话新插入的未提交日志。

**验收**：重复标量、只有 LOB 值不同、仅第 7 列不同、含 NULL、并发更改、未提交新增日志六组场景；两行内容绝不能互换。可唯一定位失败时允许明确报错，不能伪成功。


## 四 数据库界面与协议

### DBUI-01：真实网格编辑能力协议断链，需连同列映射一起修复

#### 触发与直接证据

使用允许写入的数据源，对普通带主键表执行 `SELECT ID, NAME FROM USERS`。服务端 `GridEditPlanSummary` 序列化的是：

```json
{
  "result_id": "res-wire-real",
  "schema": "APP",
  "table": "JOBS",
  "identity_policy": "pk",
  "can_update": true,
  "can_insert": true,
  "can_delete": true,
  "primary_keys": ["ID"]
}
```

`consumeQueryEvent()` 直接把 `e.edit_plan` / `e.summary.edit_plan` 放入页签，没有字段归一化。点击“网格编辑”时却判断 `current.editPlan.canUpdate`、`canInsert`，两个字段都不存在，于是即使服务端允许更新，仍显示“当前查询结果不支持网格编辑”。

复现 `GRID-WIRE-CONTRACT` 的实际输出：

```json
{
  "editModeAfterClickHandler": false,
  "toast": {"message":"当前查询结果不支持网格编辑","type":"warn"},
  "editableType": "undefined"
}
```

#### 精确位置与原因

- [internal/dbconsole/grid_plan.go:52-67](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/dbconsole/grid_plan.go#L52-L67)：Go JSON 契约使用 `can_update` / `can_insert` / `can_delete`。
- [web/pages/database.js:4644-4648](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/database.js#L4644-L4648)、`4694-4696`：直接接收后端 `edit_plan`。
- [web/pages/database.js:2914-2916](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/database.js#L2914-L2916)：切换编辑模式使用 camelCase，拒绝开启。
- [web/pages/database.js:1768-1769](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/database.js#L1768-L1769)：`getGridContext()` 同样误读字段，`editable` 可能为 `undefined`，与后续严格判断 `=== false` 又不一致。
- [tests/grid-orderby-phase0-regression.test.js:61-67](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/tests/grid-orderby-phase0-regression.test.js#L61-L67)：测试对象同时塞入 `can_update: true` 和 `canUpdate: true`。该 fixture 不符合真实 Go 响应，掩盖了主路径失效。

#### 同一协议中必须一并修复的列别名问题

这部分是独立提交分支复现，**当前正常页面会先被上面的编辑入口缺陷拦住**。修复入口后如果不处理列映射，会立即暴露。

`SELECT ID, NAME AS LABEL FROM APP.JOBS` 的结果列名为 `ID`、`LABEL`。修改 LABEL 后，实际前端提交：

```json
{
  "mutations": [{
    "action": "update",
    "values": {"LABEL": "new"},
    "original": {"ID": 42, "LABEL": "old"},
    "key": {"ID": 42, "LABEL": "old"},
    "primary_key": ["ID"]
  }]
}
```

[web/pages/database.js:1814-1816](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/database.js#L1814-L1816) 以结果列名组织原值，`:1835` 以结果列名组织新值。后端 [internal/dbconsole/grid_value.go:130-149](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/dbconsole/grid_value.go#L130-L149) 只以 `PhysicalName` 建映射，因而找不到 LABEL。主键使用别名时，定位键也会缺失。Go `ResultEditContext.Columns` 虽然已有结果名与物理名映射，但 `ToSummary()` 不传列绑定，客户端完全没有可靠映射可用。

更危险的后续场景是两个真实列名互换别名，例如 `SELECT SALARY AS ID, ID AS SALARY FROM ACCOUNTS`。结果名正好也存在于物理表时，后端“物理列存在”校验不等于“映射正确”，可能把值和定位键解释为别的列。新增的后端交叉别名回归已确认：实际查询 `SELECT salary AS id, id AS salary FROM accounts WHERE id=1` 得到正确的反向 binding，但收到当前前端负载后，写入构造函数生成以下 SQL。

```sql
UPDATE `app`.`accounts`
SET `salary` = ?
WHERE `id` = ? AND `salary` = ?
-- args: [2, 100, 1]
```

原始查询行是 id=1，生成 UPDATE 却指向 id=100。如果另一行恰好为 id=100、salary=1，影响行数仍为 1，不能靠 RowsAffected 检查发现错行。证据为 `database-alias-test-output.txt`。这证明 SQL 构造错误，没有对真实数据库执行该 UPDATE。必须连同编辑入口一起修复映射。

#### 代码级实施

1. 在 `consumeQueryEvent()` 统一归一化编辑计划，或全前端统一使用 snake_case。只允许真正的布尔 `true` 开放能力；`undefined` 必须归为 `false`。添加 `canInsert`、`canUpdate`、`canDelete` 的独立动作判断，不能用“允许插入”推导“允许更新”。
2. Go 的公开响应增加按结果索引关联的只读列绑定摘要，例如 `columns: [{index,result_name,physical_name,writable,read_only_reason}]`。是否可写及物理列映射仍由服务器最终校验。
3. 前端提交采用结果列索引加值，服务端凭 `result_id` 的不可变 `ResultEditContext.Columns` 解析物理列。若保留名称协议，必须在服务端把 `values`、`original`、`key` 三者一起按结果名映射；检测重复别名、大小写歧义、计算列和交叉别名。
4. `startCellEdit()` 按列绑定决定能否编辑，显示不可编辑原因；不要让用户改完才在提交阶段发现计算列、LOB 或表达式列无法写入。
5. 删除测试里的双字段 fixture。优先保存 Go `json.Marshal(plan.ToSummary())` 的真实输出作为前端契约 fixture，并对“存在字段”和“字段类型”断言。

#### 验收

- 使用 Go 实际生成的 snake_case JSON，普通主键、非空唯一键、隐藏 ROWID 三类查询均能按能力开启网格操作。
- `can_insert=true, can_update=false` 时只允许新增，双击更新、更新提交与删除都正确限制。
- `SELECT ID AS K, NAME AS LABEL` 编辑后，目标物理列和主键定位正确；原值并发检查也使用正确物理列。
- `SELECT SALARY AS ID, ID AS SALARY` 绝不能更新另外一行；重复别名明确只读。
- 表达式列、聚合列、缺失定位键不会被 UI 放开。
- `editable` 无论什么响应都必须严格是 boolean。

### DBUI-02：格式化会改变可执行 SQL 的结果

#### 反例 A：数字紧邻单行注释

原 SQL：

```sql
select 1--comment
+2 from dual;
```

真实格式化输出：

```sql
SELECT
  1--c omment + 2
FROM DUAL;
```

原来的 `+2` 被移到 `--` 注释同行。SQLite 内存库执行原语句结果为 **3**，格式化结果为 **1**。该复现不是“样式偏好”，而是有效表达式被注释掉。

原因：[web/workbench/sql-format-service.js:255-261](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/workbench/sql-format-service.js#L255-L261) 用 `[0-9A-Fa-f_xX.eE+-]` 连续扫描数字，把 `1--c` 吞成一个 number token，单行注释分支根本看不到 `--`。失去注释边界后，格式化器合法地按它错误的 token 流排版。

#### 反例 B：Oracle 普通字符串中的反斜杠

原 SQL：

```sql
select '\' || 'select' as txt from dual;
```

真实格式化输出：

```sql
SELECT
  '\' || '
SELECT
  ' as txt from dual;
```

第二个字符串的 `select` 被当关键字转成大写，并在字面量内部新增换行与空格。SQLite 交叉核验中，字符串结果从 `\\select` 变为 `\\\nSELECT\n  `。

原因：`:191-205` 对普通引号无条件套用反斜杠跳过下个字符的规则。`formatSQL()` 在 `:673` 调用 `tokenizeSQL(text)`，没有把 `options.dialect` 传入词法分析，所以“Oracle 方言”不会改变引号语义。Oracle 普通单引号文本不能按 MySQL 的反斜杠模式解析。

#### 影响范围

影响工具栏“格式化”、更多中的“格式化全文”、调用该服务的 DDL 格式化和编辑器入口。编辑器还会选中格式化后的片段，随后执行的可能就是已经改变语义的文本。`try/catch` 不能发现这类错误，因为整个过程没有抛出异常。仅检验幂等性也不够：错误结果可以稳定地保持幂等。

#### 代码级实施

1. 将词法接口改为 `tokenizeSQL(text, {dialect, sqlMode})`，`formatSQL`、`formatRange`、`formatInEditor` 全链路传递。Oracle 普通字符串只处理成对单引号；MySQL 根据 SQL mode 处理反斜杠；N 字符串、Q 引号、注释、引用标识符独立分支。
2. 重写数字扫描为有限状态过程：整数、小数、指数各有明确边界，正负号仅能紧邻指数标记；十六进制等按方言单独处理。遇到 `--`、`/*`、`..` 必须结束数字 token，不能被宽泛字符集合吞掉。
3. 格式化后增加保真校验：比较原文与输出的字符串、引用标识符、参数、注释 token 内容及顺序；可执行 token 顺序保持等价。校验失败原样返回并说明原因。这个检查是补充，不能依赖同一个有缺陷 lexer 证明自身正确。
4. 在 `tests/sql-format-service.test.js` 新增两个反例，并补 `1/*c*/+2`、`1--c\r\n+2`、`1e-2`、`1..10`、`1+column`、Oracle 路径字面量、MySQL 反斜杠模式与 NO_BACKSLASH_ESCAPES。
5. 对只读表达式建立 Oracle/MySQL 真实驱动测试：原语句与格式化语句分别执行，比较列数、列名、值和类型。格式化前后的可执行 token 保真与数据库结果比较均通过才恢复“安全格式化”的承诺。

#### 验收

两个样本的字符串内容、注释结束位置和执行结果必须不变；原样字面量保留与幂等性同时通过。一次 Ctrl+Z 可以撤销格式化，选择范围外内容保持逐字一致。

### DBUI-03：失效页签自动改绑其他数据源，旧 SQL 与结果未隔离

#### 触发与证据

预置 A、B 两个数据源，A 页签中保存自定义 SQL 及查询结果。删除 A 后 `deleteSource()` 调用 `reconcileSessionsWithSources({deletedId:'A'})`。VM 执行实际函数的结果：

```json
{
  "sourceId": "B",
  "sql": "UPDATE APP.JOBS SET STATUS=1 WHERE ID=42",
  "retainedResultRows": [[42,"testA"]],
  "sourceRef": {"id":"B","name":"Production B","kind":"oracle"}
}
```

能够确定的是**绑定变更且原 SQL、结果未清除**。复现没有执行任何 UPDATE，不将可能的业务写错描述成已经发生的真实数据损坏。即使有生产确认弹窗，SQL 归属从 A 变成 B 的隐式迁移仍然存在；B 若没有标为生产则不会出现该生产提示。

#### 位置与根因

- [web/pages/database.js:2468-2477](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/database.js#L2468-L2477)：删除后重整会话。
- `:2176-2207`：把“从未绑定的新空页签”和“原绑定的数据源已删除”合为一类，自动绑定剩余数据源；`deletedId` 未参与保护。
- `:2731-2740`：`renderWorkspace()` 再次按同样逻辑 fallback。
- `:2084-2087`、`:2092-2093`：恢复页签时，来源不存在也自动绑定当前源；对象恢复是近期新增。
- `:2765`：代码已有 `renderOrphanWorkspace()`，但当前没有调用点。

#### 实施

将页签来源状态分为 `unbound`、`bound`、`orphaned`。只有新建且无历史内容的 `unbound` 页签可以自动使用当前源；曾经拥有非空 sourceId 的失效页签必须保留原 ID 与名称快照，进入 `orphaned`。恢复备份、删除源、刷新源列表和工作区渲染都遵循同一规则。

复用现有孤儿页签视图，保留 SQL 可复制，展示“原数据源已删除”。重新绑定必须是用户在页签上的明确操作；一旦绑定新源，清空结果行、计划、result_id、元数据缓存、未提交网格队列，生成新 transactionId。若旧源仍有事务或执行中请求，应等待取消/回滚结果并保存失败状态，不能悄悄转移事务上下文。

#### 验收

A/B 两个测试源，A 页签包含自定义 SQL：删除 A、刷新页面、恢复本地备份、恢复远端备份后都仍指向失效 A，不得自动出现 B。点击执行、导出、元数据、运行脚本不能发往 B。用户明确选择 B 后，SQL 可以保留，结果和写入上下文必须重置。

### DBUI-04：大文本阈值错误地禁用执行所需的绑定参数

#### 触发与证据

先在 `SELECT :id FROM dual` 中绑定 `id=123`，随后给整个编辑器添加一段长注释：

```javascript
'SELECT :id FROM dual;\n/*' + 'x'.repeat(220000) + '*/'
```

总长度 220026，真正待执行的首条 SQL 很短。实际 `findParameters()` 返回空数组，`getBoundParameters()` 返回空数组，而且原 `state.bindings.id` 被删除。前端“绑定参数”入口据此显示“当前 SQL 没有发现绑定参数”，请求体不再包含该参数。

#### 位置与根因

- [web/workbench/database-features.js:479-481](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/workbench/database-features.js#L479-L481)：HEAD 新增 `text.length > MAX_SQL_HIGHLIGHT` 则返回 `[]`。
- `:511-516`：`pruneBindings()` 把空扫描结果解释为参数已删除。
- `:897-899`：UI 把扫描被跳过解释为 SQL 无参数。
- [web/pages/database.js:4470-4472](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/database.js#L4470-L4472)：执行直接使用被清空的绑定列表。
- [tests/database-sql-editor-performance.test.js:102-105](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/tests/database-sql-editor-performance.test.js#L102-L105)：把“超长 SQL 返回空参数”写成正确预期，实际上在固化功能回退。

#### 实施

高亮可以降级，执行参数的语义扫描不应降级成“无参数”。把参数解析与高亮阈值分开：先用 `statementModel` 确定当前执行范围，再只扫描所执行的 SQL；全文脚本使用 Worker 或分块扫描。接口至少返回 `{status:'ready'|'pending'|'error', parameters}`，扫描未完成时保留原绑定，不做 destructive prune。

绑定缓存按页签和 SQL 修订号保存；只有一份确认完成且对应当前修订的解析结果，才能删除确实不再存在的参数。超过高亮阈值仍必须支持普通 `:name`、`:1`、`?`、`{{name}}` 的编辑与执行。

#### 验收

相同短查询在 1KB、120KB、220KB、500KB 编辑器中，参数绑定结果一致。粘贴长注释不会删除已填参数；长参数脚本不冻结 UI；解析中禁止提交或等待解析完成，绝不静默发送空参数。

### DBUI-05：新增格式化快捷键与查找快捷键冲突

#### 触发与证据

焦点处于 SQL textarea，按 Ctrl+Shift+F。`database-features.js` 的 capture 监听器只检查 ctrl、alt、meta 和 `key='f'`，没有排除 shift；它把该组合当成查找，并调用 `stopImmediatePropagation()`。

VM 调用当前真实键监听器，输出：

```json
{"ctrlShiftFInterceptedByFeatureCapture":true,"prevented":true,"stoppedBeforeWorkbenchFormatter":true}
```

当前 feature 启动绑定通常早于异步数据库工作区完成渲染，因此会先拦截；即使监听注册顺序相反，两个监听器都没有协调已处理事件，仍可能同时触发格式化和查找。这里已确认键识别与拦截冲突，没有声称截图中实际出现了查找框。

#### 位置与实施

- [web/workbench/database-features.js:1737-1744](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/workbench/database-features.js#L1737-L1744)：Ctrl+F/H/S/O 都忽略 shift。
- [web/pages/database.js:3423-3427](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/database.js#L3423-L3427)、`:3465-3468`：另一套 capture 监听处理格式化。

短期把 Ctrl+F/H/S/O 的条件补上 `!event.shiftKey` 并遵守 `event.defaultPrevented`。长期把所有数据库命令注册到一个快捷键分发器，统一平台 Ctrl/Cmd、修饰键精确匹配、IME、弹窗焦点和输入控件作用域。执行一个命令后停止后续命令，而不是各模块独立抢占。

#### 验收

编辑器内 Ctrl+F 只打开查找；Ctrl+Shift+F 只格式化，保持关闭查找框；选择文本时仅格式化选区；全文格式化按钮仍有效。事件测试按两个可能的模块加载顺序运行。补 Windows/Linux 的 Ctrl 和 macOS 的 Cmd；IME 输入中、参数弹窗打开时不会误触格式化或运行查询。

### DBUI-06：查询历史被复制到错误数据源，清空后又自动恢复

#### 触发与证据

在 A 执行过 `SELECT A_ONLY FROM TEST_A`，全局字符串历史中会保存该语句。切 B 并加载结构化历史，`loadHistory('source-B')` 将全局 SQL 塞入 B，伪造当前数据源名称、当前时间、零耗时和 `success` 状态。

实际输出包含：

```json
{
  "sourceId":"source-B",
  "sourceName":"Source B",
  "status":"success",
  "elapsedMs":0,
  "sql":"SELECT A_ONLY FROM TEST_A"
}
```

清空 B 的结构化历史、保存后再次调用同一真实加载函数，该 SQL 又被重新导入。删除单条同理。由于新窗口深链接使用此条记录的 sourceId，错误归属还会影响后续执行目标。

#### 位置与根因

- [web/workbench/database-features.js:551-580](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/workbench/database-features.js#L551-L580)：每次加载把全局 `K.database.getHistory()` 合入当前 source 的历史，不是一次性迁移。
- `:566-575`：原始记录没有来源、时间和结果信息，却填当前源与 `success`。
- `:1686-1709`：结构化历史删除/清空只保存 v2 历史。
- [web/pages/database.js:783-793](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/database.js#L783-L793)：旧字符串历史继续更新；形成两个可相互复活的存储源。

#### 实施

统一一个结构化历史仓库，所有入口读取同一数据。无来源的旧历史仅一次迁移到“旧版历史／来源未知”，状态为 `unknown`，不得编造执行时间和成功结果。迁移完成写入版本标记，避免每次打开重复导入。

新增、删除、清空、旧版下拉、结构化弹窗必须调用同一 history store；若需要兼容旧键，只能单向同步派生显示，不允许再次反向合并。历史重放必须保留原 sourceId；来源未知或已删除时要求用户明确选择，不能产生带错误 sourceId 的自动运行链接。

#### 验收

A 的历史不会显示为 B 的成功查询；失败和取消的执行状态不被刷新成成功。删除或清空后，关闭弹窗、刷新页面、切源再回来都不会复活。旧版数据只迁移一次，标明来源与结果未知。历史重放请求使用用户确认的真实来源。


## 五 比较工作台与页签生命周期

### CT01 — 取消更换文件仍修改保存目标，可能把 A 的修改写入 B

**代码位置**

- [web/pages/compare.js:3566-3575](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L3566-L3575)：`compareFile` 先执行 `state.left.source = left` / `state.right.source = right`，然后才调用 `loadPair`。
- [web/pages/compare.js:1502-1517](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L1502-L1517)：`loadPair` 在检测未保存修改后弹确认；取消返回 `false`，没有回滚先前来源修改。
- [web/pages/compare.js:1166-1177](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L1166-L1177)：`saveSide` 从已污染的 `item.source` 取目标，与旧 `item.version` 和编辑器旧文本组成写请求。
- `internal/comparefs/comparefs.go:45-48,59,100-108`：版本仅为 `size` 与 `mtime`，未绑定读取路径；两文件大小及修改时间相同时不能阻止写错目标。

**用户触发**

1. 文件夹比较中双击 A 文件对，编辑其中一侧但不保存。
2. 回到“文件夹比较”，双击 B 文件对。
3. “当前文件比对有未保存的修改”弹窗选择取消。
4. 用户仍看到 A 的旧内容/旧标题时保存。保存请求的目标实际已经是 B。

**执行结果**

CT01 使用真实 `compareFile`、真实 `loadPair` 与真实 `saveSide` 函数，确认函数返回 `false`。最终请求为：

```json
{
  "target": { "kind": "local", "path": "/left/B.txt", "label": "B.txt" },
  "content": "A edited",
  "expected": { "size": 3, "mtime": "2026-09-22T00:00:00Z" }
}
```

来源已经变成 `/left/B.txt`，内容仍是 A 的编辑稿。若 A/B 版本不同，表现为本不该出现的冲突；若同大小同修改时间，后台版本检查允许写 B。同一时刻产生且尺寸一致的文件、复制保留时间的文件对满足这一条件；即使有自动备份，写错目标本身仍需优先修复。

**真实 HTTP 补证**：本次隔离二进制也接受了上述错配负载，返回 200，临时 B.txt 确实从 BBB 变成 A edited，A.txt 保持 AAA，B 备份为 BBB。普通旧版本写 B 则正确返回 409。详情和原始响应见 `logs/api-smoke.json`。

**实施方案**

1. 删除 `compareFile` 对 `state.left/right.source` 的直接赋值，把 `loadPair` 作为唯一的“切换文档”入口。
2. `loadPair` 在确认之前只构造候选来源；确认取消时不得修改来源、版本、编码、baseline、内容、历史、当前视图。
3. 确认同意后再提交两侧来源并开始读取；成功后 `compareFile` 才切到文本结果，或明确展示加载状态。读取失败时用独立错误状态，避免旧文本与新路径并存。
4. 为每侧引入 `documentId` / source identity（协议+服务器+规范路径）及 `loadSeq`，读取响应、保存快照、撤销命令都绑定文档身份。
5. 后端可追加与来源绑定的版本 token，但不能只依赖后端补丁；前端取消语义必须修正。`expected` 不应该被重新解释成任何同版本文件都可写。

**验收**

- 在 A 脏状态下尝试打开 B 并取消，完整比较状态与取消前相同；继续保存必须仍发往 A。
- A/B 设成相同字节数和完全相同 mtime，重复上述流程；B 内容和时间都不变。
- 左脏/右脏/两侧脏、文本来源与文件来源混合、目录双击与“载入并比对”入口分别覆盖。
- 读取 B 失败或快速两次打开不同文件时，不能产生新 source+旧 version 的组合。

### CT06 — 行内编辑按 Ctrl+S 未先提交当前行，保存旧内容

**代码位置**

- [web/pages/compare.js:2353-2390](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L2353-L2390)：行内 `contenteditable` 的内容只在 `finishEdit()` 中提交给 `onEdit`。
- [web/pages/compare.js:2401-2445](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L2401-L2445)：只对方向键、Ctrl+Enter、blur 等调用 `finishEdit`，Ctrl+S 没有提交逻辑。
- [web/pages/compare.js:938-953](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L938-L953)：面板收到 Ctrl+S 直接执行 `saveSide`。
- `web/pages/compare.js:1169,1176,1180`：保存读取隐藏的 `editors[side].getValue()`，成功后 `loadSide()` 重建内容。

**触发与影响**

在左右差异结果区直接点入一行、输入新文本、不离开焦点即按 Ctrl+S。此时可见的新内容只存在 `contenteditable` DOM，主编辑器仍是上次提交的值：

- 如果此前没有已提交的修改，代码判断“文件内容未修改，无需保存”，此次 Ctrl+S 没有保存。
- 如果此前已有脏修改，则保存旧的已提交值；保存后重新读取和清空结果区的路径会移除当前行内草稿，存在丢失最新输入的风险。

**执行结果**

CT06 执行真实 `makeDiffCell` 的 keydown handler、真实面板快捷键 handler 与真实 `saveSide`。可见草稿 `A inline latest`，底层已提交文本 `A edited`，Ctrl+S 后结果：

```json
{"visibleDraft":"A inline latest","written":"A edited","cellCommits":0}
```

这已确认写请求内容错误。DOM harness 没有模拟浏览器真实布局与删除焦点节点，因此“删除焦点节点后的 blur 顺序”仍应在浏览器回归中验证；修复不得依赖该顺序。

**实施方案**

1. 在 `createVirtualDiff` 中注册当前编辑单元格，暴露 `flushActiveEdit({ recompare: false })` / `cancelActiveEdit()` / `hasDraft()`。`finishEdit` 保持幂等。
2. 所有保存入口统一先 flush 当前行内草稿，再捕获正文、文档 identity、editSeq、version。按钮保存、Ctrl+S、双侧保存都走同一 command。
3. flush 阶段可以更新主模型和 dirty，但不要立即启动会删除当前编辑节点的重比；保存捕获快照后再统一调度。
4. 行内草稿在 input 时登记到文档 draft 状态，让关闭保护、虚拟窗口重建、模式切换均能看到尚未 blur 的编辑，避免把 DOM 当唯一数据源。
5. 虚拟行重建、字体/行距变更、搜索重渲染前，要提交或保留编辑 draft。不要只依赖 blur。

**验收**

- 原文件干净时，行内编辑后直接 Ctrl+S，磁盘必须包含最新字符，不出现“无需保存”。
- 文件已有脏修改时再行内输入新内容，Ctrl+S 必须保存最新行内内容；保存后仍可见，编码/EOL不改变。
- 左右两侧、粘贴多行、Ctrl+Enter后保存、按钮保存、输入中滚动/改字号/切换视图/关闭 Tab 均验证。

### CT02 — 保存期间继续编辑，写成功后版本不前进

**代码位置**

- [web/pages/compare.js:1166-1177](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L1166-L1177)：捕获 `editNo` 后异步写入；响应到达时如果 `editSeq` 改变直接返回 false。
- [web/pages/compare.js:1178-1180](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L1178-L1180)：清 dirty 与更新内容版本都依赖随后的 `loadSide`，提前返回跳过它。
- [internal/httpserver/handlers_compare_workbench.go:380-381](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/httpserver/handlers_compare_workbench.go#L380-L381)：写接口只返回 `{ok:true}`，没有返回新版本。

**触发与影响**

读取版本 V1 → 编辑为 C1 → 保存（慢磁盘、SFTP/FTP最易遇到）→ 请求未完成时继续编辑 C2 → C1已写入产生V2 → 前端因为editSeq变化提前返回，保留V1。下一次保存C2仍携带V1，触发409。点击“重新比较”只对内存文本diff，不会更新文件版本，因此错误文案所说的“重新比较后再保存”也不能解决这个状态。强制重新载入会丢掉C2，必须先手动另存/复制才能安全恢复。

**执行结果**

CT02 使用可延迟 Promise 执行真实 `saveSide`。写成功后 `loadSide` 调用次数0、dirty仍true、版本仍旧；第二次请求正文是新C2而expected仍为V1。用后端冲突响应模拟第二次返回409，错误文案为“目标文件已变化，请重新比较后再保存”。

**实施方案**

1. 写接口在同一写操作完成后返回新版本、规范化路径和写入正文的摘要/身份，避免通过“先清编辑器再重新读取”取得版本。
2. 前端保存快照至少包含 `{documentId, source, expectedVersion, content, editSeq}`。
3. 成功后，只要当前还是同一文档，即使编辑序号已变，也把baseline推进到**本次成功写入的content**，把version推进到返回的新版本；保留当前C2，重新计算dirty = current !== savedSnapshot。
4. 只有文档已更换或关闭时丢弃UI更新，不要把“编辑继续发生”当成写成功结果完全过期。
5. 每文档串行化保存；`if (item.saving)` 返回/合并下一次保存请求，Ctrl+S不能绕过按钮disabled重复发写。
6. 真正的外部409进入“磁盘新版本与本地草稿”的明确冲突处理，禁止自动reload覆盖本地草稿。

**验收**

- 保存C1延迟响应时编辑C2：C1成功后C2仍在且dirty；再保存C2成功；无需重载或人工复制。
- 连按Ctrl+S、保存期间换源/关闭Tab、外部第三方改文件分别验证，不能把外部修改当自身保存覆盖。
- 新版本来自同一次成功写操作，编码、BOM、EOL与正文写入保持一致。

### CT03 — 隐藏的比较 Tab 仍处理 Ctrl+Z

**代码位置**

- [web/pages/compare.js:742-755](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L742-L755)：监听注册在 `document`；判断 `Kairo.tabs.activeRoute()` 是否为compare。
- [web/tabs.js:485-499](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/tabs.js#L485-L499)：实际公开的是 `getActiveId`、`getActive`、`isActive`，没有 `activeRoute`。

因此条件 `typeof Kairo.tabs.activeRoute === 'function'` 永远不成立，保护分支被整体略过。

**触发与执行结果**

比较中做一次合并或覆盖，切到文件/配置等其他Tab，焦点位于按钮/正文上按Ctrl+Z。CT03把实际Tab接口设为当前`files`，执行真实handler，`performUndo`仍调用一次并 `preventDefault=true`。后台比较文本会在用户看不到时被撤回，且可能阻断前台快捷键。

**实施**

- 最小修复使用已存在API：`if (Kairo.tabs && !Kairo.tabs.isActive('compare')) return;`。
- 更稳妥是把比较快捷键绑定到本Pane/工作台root，并按 `event.target` 归属路由；必须绑定document的快捷键由Tab级统一分发。
- 事件处理保留 `defaultPrevented`、输入控件和组合输入保护；关闭时通过scope自动注销。

**验收**

比较前台/后台各按Ctrl+Z：只有前台比较执行撤回；编辑textarea或contenteditable时不双重撤回；关闭并重开比较20次时一次按键仅执行一次。

### CT04 — 左右交换之后无法撤回

**代码位置**

- [web/pages/compare.js:691-708](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L691-L708)：快照记录交换前的有序 `pairKey`。
- [web/pages/compare.js:714-718](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L714-L718)：撤回只接受当前pairKey等于快照pairKey。
- [web/pages/compare.js:1461-1464](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L1461-L1464)：交换先入栈，再交换左右完整state。

**触发与执行结果**

两侧使用不同文件/来源，点“交换”，再点“撤回”。交换后pair顺序必然变化，CT04真实函数结果为“撤回快照与当前比较文件不匹配，已阻止”，并已pop掉快照。最普通的交换动作不能撤回。

**实施**

不要直接删除身份保护。把undo记录设计成命令，区分 `edit/merge/swap`：普通内容修改要求同一文档pair；swap命令记录交换后的身份作为撤回前置条件，再原子交换回两侧完整文档状态。若仍用快照，必须包含 `source/version/codec/baseline/loadState` 等完整关联字段，不能只恢复source和codec。检查通过后才pop；若真的不匹配，要清楚反馈但保留可检查记录。

**验收**

不同路径/编码两文件交换→撤回，路径、正文、版本、baseline、dirty、编码全回原状；保存必须到原目标。交换→编辑→撤回编辑→撤回交换、交换两次分别验证。重新载入第三个文件后旧快照仍应拒绝跨文档恢复。

### CT05 — 关闭比较 Tab 后文件夹扫描继续轮询

**代码位置**

- [web/pages/compare.js:22-23](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L22-L23)：`activeScanJob` / `scanPollTimer` 是模块级变量。
- [web/pages/compare.js:577-580](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L577-L580)：renderCompare cleanup只执行textCleanup。
- [web/pages/compare.js:3089-3136](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L3089-L3136)：pollScan只判断私有scanSequence，正常running响应后继续setTimeout 350ms。
- [web/pages/compare.js:3611](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L3611)：folderWorkbench只返回startScan，没有dispose。
- [web/tabs.js:379-407](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/tabs.js#L379-L407)、[web/core.js:1010-1026](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/core.js#L1010-L1026)：Tab关闭会释放注册资源与scope，但比较扫描没有注册进去。

**触发与执行结果**

开始大目录/远端目录扫描，关闭比较Tab。CT05执行真实render cleanup之后再执行已有poll回调，仍发`GET /api/compare/jobs/job-old`，并安排350ms后下一轮，jobID仍保留。

请求完成前关Tab并重开时，旧实例和新实例共享模块级jobID，有进一步的串扰风险：旧回调可能轮询新job并清掉全局jobID，结果却渲染到旧的已卸载DOM。该串扰链目前为源码推演，未冒充端到端实测；基础的关闭后继续轮询已实证。

**实施**

1. `buildFolderWorkbench` 返回 `{startScan, dispose}`，将jobID/pollTimer/filterRenderTimer/scanSequence全部收进实例。
2. renderCompare cleanup同时调用两个workbench的dispose；真实隐藏Tab继续保活，真实关闭才取消资源。
3. dispose设置disposed、递增generation、清timer、捕获并清空jobID，向后端DELETE该job；正在返回的POST/GET在await后再次验证实例generation及jobID。
4. `pollScan` 使用本次任务固定jobID，而不是每轮读全局可变化的变量。
5. 若产品希望关闭Tab后仍运行扫描，需转入可查看/取消的全局任务管理器，而非保留脱离DOM的页面闭包。

**验收**

Tab切换保持扫描；Tab关闭时仅发一次cancel且不再GET。关闭发生在POST响应前、GET响应前、timer已安排后均覆盖。旧Tab关闭后立即重开并启动新job，旧回调不能修改新job状态。后端cancel失败不能恢复前端poll。

### CT07 — “全选内容”只覆盖虚拟窗口，复制结果截断

**位置与证据**

- `web/pages/compare.js:644,678-687,858-863`：“更多操作→全选内容”用Range选择`.cmp-vdiff-canvas`已有DOM，并提示“已全选比对内容，可直接Ctrl+C复制”。
- [web/pages/compare.js:2062-2070](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/compare.js#L2062-L2070)：虚拟化仅创建当前窗口及overscan内的行。
- CT07输入1,000行、viewport=400px、行高25px，执行真实renderWindow+selectAllDiff。canvas只有46行，被选内容没有第1,000行，提示仍宣称全选。

**排除误报边界**

“复制左侧全部”和“复制右侧全部”是另外两个正常入口：`copySideAll`（664-675）直接从editor.getValue读取全文。本问题仅针对“全选内容”及它绑定的结果区Ctrl+A，不是声称所有复制能力都截断。原文textarea的原生全选也不在本问题内。

**实施**

可保留原生可见文本选择，但将“全选”建模成数据级选择；copy事件检测该状态并从完整alignedRows/左右原文序列化用户选择的格式。若产品不需要复制双栏格式，直接把该入口改为明确的“复制左侧全部 / 复制右侧全部 / 复制Diff”，删除声称能选择完整虚拟列表的按钮。只改文案为“选中当前可见文本”也能避免误导，但不能满足原本全选全文的意图。

**验收**

1,000行、50,000行、中间滚动位置全选后复制，首尾标记都存在；只差异模式按定义复制全体差异行；普通鼠标局部选区仍按选区复制。现有左右全文复制不退化。


## 六 SFTP 请求凭据与升级恢复

### OTH-01｜升级旧锁回收破坏互斥

#### 源码定位

- [internal/upgrade/files.go:149-157](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/upgrade/files.go#L149-L157)：获取失败后读取锁内 PID，发现进程死亡就 `os.Remove(path)`，紧接着重新 `O_CREATE|O_EXCL` 创建。
- 同文件 `170–175` 的 token 校验只保护 release，无法保护回收旧锁的删除动作。
- 相关提交：`13252c0`，2026-09-22，升级崩溃死锁自动回收。
- 源码：[files.go](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/upgrade/files.go#L149)。

#### 触发与结果

崩溃后留有死 PID 锁，两个 Kairo 进程接近同时启动或执行恢复：

1. A、B 都读取旧锁并判断旧 PID 已死。
2. A 删除旧锁，重新创建文件，获得锁并进入升级。
3. B 仍沿用此前读到的旧 PID 判断，删除路径当前对应的 **A 的新锁**。
4. B 再次独占创建成功。此时 A、B 都认为自己获得锁，可同时进入 journal/快照/文件替换流程。

仅重新 `O_EXCL` 创建无法修复，因为竞争发生在对旧判断执行删除时。再加一次“读 token 然后删除”依然有读后删除的竞争窗口。

#### 实测

`TestAuditConcurrentStaleLockReclamation`：64 个 goroutine 同时争用死 PID 锁，在第 9 轮发现 **2 个同时成功持锁者**。任何 release 均等到所有尝试结束后才执行。

为排除只在同一进程内部成立的假设，又运行 `TestAuditIndependentProcessStaleLock`：16 个独立测试子进程、各自真实 PID、统一 start 文件放行、成功进程持锁等待 release 文件。第 7 轮发现 **PID 843 与 846 同时成功持锁**，在统计时二者均尚未释放。这是单独一次运行观测，轮次和 PID 会随环境变化。

#### 实施方案

新增跨平台 `sysutil.AcquireFileLock(path) (release func(), err error)`，优先使用内核锁：

- Unix 对稳定存在的文件描述符使用非阻塞排他 `flock`，持有 fd 到升级/恢复结束。
- Windows 对稳定文件句柄使用 `LockFileEx` 排他、立即失败模式，保持句柄到事务结束。
- 现有 `golang.org/x/sys` 已在依赖中，适合做平台封装；具体平台构建标签保持与 `process_unix.go/process_windows.go` 一致。
- PID、token、时间只保留作诊断信息，互斥正确性不再取决于 PID 是否复用、读写元数据是否完整。
- 不在 release 时 unlink 一个仍被当作内核锁对象的文件；锁文件路径保持稳定，解锁并关闭句柄即可，避免新 inode 绕过旧 inode 的锁。
- `Run`、`Recover`、`RestoreSnapshot` 共用同一个锁实现，所有路径必须走同一把锁。

#### 验收

- 16 个独立进程同时启动，连续多轮，任意时刻成功持锁者不超过 1。
- 杀死持锁子进程后，另一个进程能获得锁并进入已有恢复流程，无需手删文件。
- 在刚创建文件、元数据写入中途、迁移资产写入后分别杀死进程，都不遗留永久死锁。
- 保留已有“正在运行进程不被抢锁”“释放旧 owner 不删新 owner”语义；Linux 和 Windows 都实跑。
- 对升级文件注入两个竞争启动流程，确认只出现一次完整 journal/manifest 提交，不以“两个都最终返回成功”作为验收标准。

### OTH-02｜WSDL 重定向凭据隔离仍有两条缺口

#### 源码定位

- [internal/webservice/schema_resolver.go:201-227](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/webservice/schema_resolver.go#L201-L227)：`makeSafeHTTPClient(authHeaders)` 没有使用 `authHeaders` 参数；只在相对上一跳跨源时删除三个固定头。
- [internal/httpserver/handlers_webservice.go:150-165](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/httpserver/handlers_webservice.go#L150-L165)：根 WSDL URL 下载器复制了同一策略，存在相同源码缺口。
- `schema_resolver.go:532–537` 已实现跨源直接 import 不附带认证头，此处不能再报成“直接 import 漏凭据”；当前问题是重定向。
- 源码：[schema_resolver.go](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/webservice/schema_resolver.go#L201)。

#### 已复现的两种情况

**自定义认证头：** A 的同源 schema 请求携带用户配置的 `X-API-Key: audit-secret`，A 302 到 B。B 确实收到 `audit-secret`，因为 Kairo 仅删除 Authorization、Proxy-Authorization、Cookie，未删除用户提供的其他认证头。

**Authorization 多跳恢复：** A、B 使用同一主机、不同端口。A 重定向到 B `/one`，B 再重定向到自己的 `/two`。第一跳 Kairo 删除 Authorization；第二跳 B→B 被视为同源，Kairo不删头，而 Go 已从初始请求重新复制了 `Authorization: Bearer audit-secret`。最终 B `/two` 确实收到该值。

Go 1.24 标准库源码链已在当前测试工具链核对：

- `src/net/http/client.go:678–687`：每次重定向先从初始请求复制 Headers。
- `803–813`：初始 Headers 的复制循环包含敏感头；只有 `stripSensitiveHeaders` 为 true 才移除。
- `990–1006`：是否自动剥离敏感头按 hostname/subdomain 判断，不以端口定义 origin；同主机不同端口时不会自动替 Kairo 完成严格同源隔离。
- `694`：Header 复制完成后才执行 Kairo 的 `CheckRedirect`，所以必须每一跳对可信源重新判定。

这属于用户主动携带凭据导入 WSDL/XSD 时的出站凭据泄露；不能描述为“互联网可直接攻击默认本机接口”。

#### 实施方案

抽出 WSDL 根文档和 schema 共用的重定向策略，例如 `newSchemaHTTPClient(trustedOrigin, headers)`：

1. 以首次携带这些凭据的可信 origin 为基准，而不是 `via[len(via)-1]`。
2. 每一跳使用已有 `isSameOrigin` 的协议、标准化主机、有效端口判断；不同源时删除所有用户传入头名及固定敏感头。可显式保留 User-Agent、Accept 等程序自己设置的非敏感头，不把任意用户自定义头默认当安全。
3. 保留 URL/DNS 检查与最多 10 次跳转限制。
4. 如跨源后又返回原可信源，可明确选择只在原可信源恢复凭据；无论选择哪种策略，跨源 B→B 的后续跳转都不得恢复 A 的凭据。
5. 避免 handler 与 resolver 维护两个稍有差异的副本。

#### 验收

- 同源直接 import、同源重定向仍携带凭据。
- 跨源直接 import 不携带凭据，保持现有修复。
- A→B、A→B→B、A→子域→子域、同主机换端口等组合均校验 Authorization/Cookie/Proxy-Authorization/X-API-Key/任意自定义头。
- 根 WSDL 拉取和外部 XSD 拉取各跑同一组用例。
- 断言接收服务器真实收到的 Header，不仅检查 `CheckRedirect` 被调用。

### OTH-03｜SFTP 父目录歧义未能阻止写入

#### 源码定位

- [internal/sftpclient/sftpclient.go:961-971](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/sftpclient/sftpclient.go#L961-L971)：`resolveExistingDir` 返回 `ErrAmbiguousPath` 后，`resolveWritePath` 忽略错误，顺序 `Stat` 并选择首个目录。
- `987–1004` 只检查被选中父目录内的目标，无法恢复已经丢失的父目录歧义。
- 关联入口：UploadFile、UploadStream、Rename 目标、CreateExclusive；`MkdirAll:723–727` 还会再次吞掉解析错误。
- 提交：`5eca1d8` 的父目录解析修复。
- 源码：[sftpclient.go](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/sftpclient/sftpclient.go#L961)。

#### 触发与实测

同一个 `/tmp` 下有两个物理目录：一个名字是 UTF-8 `中文`，另一个名字是 GBK `中文`；界面均显示 `/tmp/中文`。两个目录分别含 `protected.txt`。

向展示路径 `/tmp/中文/protected.txt` 上传 `new`，预期报歧义并保持两份文件。实测返回 `nil`，UTF-8 文件变为 `new`，GBK 文件保留 `gbk-old`。这证明当前实现在没有明确文件身份时擅自选定了一份文件。

#### 实施方案

- 将目录解析结果区分为“唯一存在”“确定不存在”“歧义”“权限/网络错误”，不要将所有错误降级为默认路径。
- `ErrAmbiguousPath`、permission、超时、协议错误必须透传并在写操作前终止。
- 如允许不存在的父目录，由专用目录创建解析处理；不能在一般文件写路径函数里直接把失败改为首个候选。
- 写入已有文件必须先得到完整、唯一的原始父路径及原始文件名，再交给 backend。
- UI/API 通过 OTH-05 的身份字段消除用户已明确点击某条记录时的歧义。

#### 验收

对 UploadFile、UploadStream、CreateExclusive、Rename 目标、MkdirAll 逐个覆盖：父目录同显示名不同编码，必须返回 `ErrAmbiguousPath`，且两边文件/目录快照完全不变；传入明确身份时只能作用于指定物理条目。加入“父目录没有 ReadDir 权限但 Stat 可用”的测试，不能借 fallback 任意选择两个存在候选之一。

### OTH-04｜GBK 已有父目录下新建多级目录会形成错误分支

#### 源码定位

- [internal/sftpclient/sftpclient.go:715-727](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/sftpclient/sftpclient.go#L715-L727) 把 MkdirAll 当作单目标文件的 `resolveWritePath` 处理。
- `resolveWritePath:962–971` 要求整个 parent 已存在；parent 中有新建层级时失败并恢复未经解析的显示路径。
- [internal/comparefs/sftp.go:98-104](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/comparefs/sftp.go#L98-L104) 将目录同步的 MkdirAll 交给这个实现。

#### 实测

只存在原始 GBK 目录 `/tmp/\xd6\xd0\xce\xc4`，它显示为 `/tmp/中文`。调用：

```go
client.MkdirAll("/tmp/中文/new/deep")
```

正确目标是保留 GBK 前缀，再新增 ASCII `new/deep`。当前传给后端的却是 UTF-8 `/tmp/中文/new/deep`；真实远端 `mkdir -p`/MkdirAll 会沿这个不同字节的路径创建另一套目录。探针直接记录了被创建的参数路径，未连接真实远端。

这是普通“GBK 现有目录 + 多层新目录”即可触发，不要求用户本来就有同显示名重复目录。

#### 实施方案

新增针对目录创建的 `resolveDirectoryForCreate`：逐级解析已存在前缀；对第一个确认为不存在的段，基于已经确定的原始父路径编码新段，并继续向下。任何歧义或非 NotExist 错误立即终止。不要对一个混合 UTF-8/GBK 的完整绝对路径整体转码，也不要丢弃已经成功解析的前缀。

完成 OTH-03 错误分类后共享底层逐段解析；MkdirAll 避免吞错。目录同步无需依赖父目录计划恰好先执行，因为 MkdirAll 本身就应支持多层不存在。

#### 验收

- GBK 父目录 + 两层、三层新增目录；GBK/UTF-8 混合层级；纯 ASCII 父目录；中文新增子目录。
- 真实 GBK SFTP fixture 按原始文件名字节检查只新增预期分支，不能只看界面显示文字相同。
- 目录同步选择带多级空目录和文件的整棵树，确认创建落在原有物理目录，重复同步不增加 UTF-8 同名分支。

### OTH-05｜path_id 身份没有贯通页面、HTTP 校验及嵌套目录

#### 三层断点

**前端没有使用：** 在整个 `web` 中搜索 `path_id`、`raw_name`、`kairo-raw` 没有命中。[web/pages/files.js:1060-1072](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/files.js#L1060-L1072) 和 [web/pages/ssh.js:1592-1622](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/web/pages/ssh.js#L1592-L1622) 仍以 `entry.name` 拼远程路径，以 `entry.name` 作为选中集合键。两条同显示名记录无法独立选中与操作。

**HTTP 拒绝身份：** `handlers_ssh_sftp.go:169/359/504/740/790/841` 的 list、preview、download、create、save、rename 路径检查仍要求字符串以 `/` 开头。因此就算前端直接把后端返回的 `kairo-raw:...` 放进现有 path 字段，也会先被 HTTP 层 400 拒绝。`handlers_files.go` 同样存在绝对路径检查，不能只改 SSH 页面。

**返回身份本身不总正确：** [internal/sftpclient/names.go:94-100](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/sftpclient/names.go#L94-L100) 的 `PathIDOf(parentDir, fi)` 用传入父目录拼原始文件名；`handlers_files.go:420–432`、`handlers_ssh_sftp.go:245–257` 传入的是用户请求的展示父路径，并非 `Client.ReadDir` 内部解析后的原始父路径。GBK 父目录下的中文文件由此获得“UTF-8 父目录 + GBK 文件名”的错误混合路径 ID。

此外只对非 UTF-8 条目发 path_id，使同显示名的 UTF-8 兄弟条目缺少可区别于展示路径的明确身份；comparefs 的 List/ListLimited 也继续用展示名生成 `Entry.Path`，需一起设计。

#### 实测

`TestAuditPathIDMustIncludeResolvedParent` 先用展示路径成功 ReadDir GBK `/tmp/中文`，再按 HTTP handler 同样的方法生成子文件 path_id，最后 `Client.Open(path_id)` 返回不存在。该测试独立于前端，证实不能仅加 `entry.path_id || fullPath` 就宣称完成。

#### 实施方案

1. 在 SFTP 客户端列目录结果中保留完整原始绝对路径，推荐用明确条目结构或扩展 FileInfo 暴露原始路径；由**已解析的原始父路径**生成每条记录的 path_id，包含 ASCII、UTF-8、GBK 所有条目。
2. HTTP 契约区分 `display_path` 和 `path_id`；旧 `path` 可保留兼容，但身份解析、路径合法性、已有根目录限制应集中在一个函数，先正确解码身份再按原始绝对路径做验证，不放宽非法路径校验。
3. 前端选中键、右键、预览、编辑、删除、重命名、下载均使用身份。地址栏和面包屑展示人类可读路径，显示名不能再做资源唯一键。
4. 新建/重命名使用 `parent_path_id + new_name` 组合在服务端处理；不能在 opaque token 后直接拼 `/name` 或 `.partial`。
5. 比对适配器保留展示相对路径用于对齐，保留独立源文件身份用于 Stat/Open；发现多个相同展示相对路径时明确报告歧义，不能在 map 中静默覆盖。
6. `raw_name` 不是可安全往返的 JSON 身份：非法 UTF-8 序列会在 JSON 中替换。依赖编码后的 path_id 或显式 bytes 表达。

#### 验收

在真实 GBK fixture 中创建同显示名 UTF-8/GBK 两个文件、两个目录，以及嵌套 GBK 目录；通过**真实页面点击**分别选中、预览、下载、编辑、重命名。记录每个 HTTP 请求的 identity，按原始磁盘字节核对只有选中目标变化。再验证刷新、返回上级、打开第二个服务器相同路径不会复用错误身份；只跑 sftpclient 单测不足以通过此项。

### OTH-06｜ASCII 文件也反复拉取整个父目录

#### 源码定位

- [internal/sftpclient/sftpclient.go:797-808](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/sftpclient/sftpclient.go#L797-L808) 每个 resolveExistingPath 都 `c.b.ReadDir(resolvedParent)` 再遍历匹配，没有 ASCII 快路径或复用刚列出的身份。
- `Client.Stat:620`、Open:547 都调用此解析器。实际 SFTP backend `165–166` 直接调协议客户端完整 ReadDir。
- 比较同步 `handlers_compare_workbench.go:727–773` 为版本校验多次 Stat/Open，在目录含大量文件时进一步放大次数；不能删掉这些版本校验来绕开性能问题。

#### 实测与影响解释

计数后端构建 `/data` 下 1000 个 ASCII 文件，对这 1000 个文件各调用一次 Client.Stat：

```text
1000 ASCII Stat calls caused 1000 complete directory reads and 1000000 returned entries
```

这是 100 万条目录项被重复返回和遍历，不是 100 万次网络请求。对于 N 个文件都在同一个 N 项目录，解析所需目录枚举达到 O(N²)。实际延迟还取决于远端 RTT、协议实现和单目录分布，当前没有把这些因素换算成未测量的秒数。

#### 实施方案

- 父目录已唯一解析且 basename 是 ASCII 时，直接 Stat 该原始父目录下的同一 ASCII 名称；ASCII 文件名不需要寻找 UTF-8/GBK 两种编码。
- 已从列表取得的所有记录直接携带 OTH-05 的原始路径身份，随后的 Open/Stat 不再按显示名列目录反推身份。
- 中文路径必须解析时，在一次扫描/同步任务内按连接和原始目录建立有界索引，缓存展示名→原始名集合，保留歧义标记；写操作后失效受影响目录。不能把长期缓存当成防覆盖的唯一正确性依据，最终版本/存在性检查仍需访问远端。
- Context 取消/超时需要贯穿昂贵的解析阶段；这是性能实现的一部分，但当前缺陷优先以消除每文件完整枚举为验收点。

#### 验收

- 1000 个 ASCII 文件各一次 Stat 的计数测试不产生 1000 次完整 ReadDir；目录规模扩大后枚举数应与扫描目录数近似线性。
- 1 万文件目录比较/同步实测记录 ReadDir 次数、远程操作次数、耗时和峰值内存；与修改前同一服务器对照，不能只看本地 mock 用时。
- 中文/混合编码、重复显示名与目标并发修改保护仍通过，不能通过取消路径身份或并发校验换取速度。


## 七 日志兼容与测试工程问题

### OPS-01 日志搜索在 dash 中不能还原十六进制关键词

**优先级 P1，既存兼容问题。** [internal/logquery/logquery.go:190-207](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/logquery/logquery.go#L190-L207) 的 `ToEncodingEscaped()` 把关键词目标编码字节写成 `\xHH`；`:696`、`:703` 用远端 shell 的 `printf %b` 还原，然后交给 awk。该逻辑依赖 Bash 支持的十六进制扩展。本次 `/bin/sh` 实际是 dash，直接执行结果如下。

| Shell | 输入给 printf %b 的转义 | 实际输出字节 |
|---|---|---|
| dash | `\x41` | `5c 78 34 31`，即字面量反斜杠 x41 |
| Bash | `\x41` | `41`，即 A |
| dash | `\0101` | `41`，即 A |
| Bash | `\0101` | `41`，即 A |

已有 logquery 测试在本环境稳定失败，涉及普通词、布尔条件、中文 UTF8、GBK、窗口搜索等；纯否定条件还可能错误保留本应排除的行。这里只确认 dash 与 Bash 的实际差异，AIX ksh 等环境留作兼容验收。核心调用的版本追踪到 2026 年 8 月，不能称作本周新增回归。

**实施方案。** 将字节转义统一为 POSIX `printf %b` 可解释的 `\0ooo` 格式，例如 Go 中 ``fmt.Fprintf(&sb, `\0%03o`, b)``；保持每个原始字节固定三位八进制，不把原始用户文本拼进 shell。关键词中的引号、反引号、美元符号和换行仍通过安全编码传输。修改现有测试 fixture 的预期，同时增加明确指定 `/bin/dash`、`/bin/bash` 的命令执行测试；若系统未装某 shell，输出有原因的 skip。将远端 shell 能力写进诊断信息，但不能依靠要求用户更换默认 shell 才保证基本查询正确。

**验收。** 同一批固定日志在 dash/Bash 下返回相同文件、行号与命中内容；覆盖中文、GBK、AND、OR、NOT、跨行窗口、空结果、重复词和 shell 元字符。继续保留字面量注入测试，不能通过放弃编码获得通过。再在真实目标 AIX/Linux 测试机核验。

### QA-01 默认测试读取开发机数据源并执行删表

**优先级 P1，测试隔离缺陷。** [internal/dbconsole/oracle_workbench_test.go:10-25](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/dbconsole/oracle_workbench_test.go#L10-L25) 从固定 `d:\kairo\data` 读取数据源，挑第一个 Oracle，复制后强制设置 `ReadOnly=false`、`AllowDDL=true`；`:40-41` 在没有测试库授权门禁的情况下发出 `DROP TABLE kairo_wb_test CASCADE CONSTRAINTS`，随后建表、插入、更新、再次删表。当前隔离 Linux 没有相应数据源，测试跳过；这不能证明在用户 Windows 开发机执行 `go test ./...` 安全。

**实施方案。** 把所有真实数据库测试移至显式集成入口，例如独立 build tag `integration` 和 `KAIRO_INTEGRATION_ORACLE=1` 双条件。测试数据源只能来自专用测试 DSN，使用新建临时目录，不读取默认配置目录、用户 AppData 或固定开发目录。独立测试 schema 中使用带本次 run_id 的表名，启动时检查 schema 和显式授权；清理范围仅限本次创建且记录在 manifest 内的对象。将当前无条件 DROP 改为按所有权记录清理。测试请求应有 context deadline，连接失败与没有配置给出可区分的 skip/fail，不静默把授权错误当完成。

本次意外外发的直接原因也已查清：[internal/httpserver/handlers_pet_test.go:310](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/httpserver/handlers_pet_test.go#L310) 只覆盖 mock primary，把 secondary 传空；[internal/pet/sync.go:64-73](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/internal/pet/sync.go#L64-L73) 对空参数的约定是保留默认地址，因此 mock 返回失败后仍尝试真实备用上游。后续测试还会继承未完整恢复的全局配置。该外发被自动审批拒绝。单元测试须在构造客户端时注入 transport/baseURL，每例显式设置两个本地 mock 并在清理时恢复配置，默认没有任何外部网络；真实外联集成测试使用独立配置显式启动。

**验收。** 在 Windows 与 Linux 各运行默认单测，即使机器上有真实用户配置，也不会读取它、建立业务数据库连接或发出外部请求。集成模式缺任一门禁即不执行 DDL；实际执行只影响本次测试 schema 中的临时对象。用本地记录 transport、文件访问替身和固定 schema 检查验证边界。

### QA-02 测试输入偏离真实接口且入口不可移植

**优先级 P2，跨层测试缺口。** 本次的三个具体例子足以解释“测试全绿但核心流程仍坏”。

1. [tests/grid-orderby-phase0-regression.test.js:61-67](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/tests/grid-orderby-phase0-regression.test.js#L61-L67) 同时设置 `can_update` 与 `canUpdate`，真实 Go 响应只有前者，主流程的契约错误因此被遮住。
2. [tests/database-sql-editor-performance.test.js:102-105](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/tests/database-sql-editor-performance.test.js#L102-L105) 将超长 SQL 返回空绑定参数当成预期，从性能优化测试固化了执行功能回退。
3. [tests/e2e/test-sql-editor-large-text.js:1](https://github.com/Qioooba/kairo/blob/2b14d6774394e048bacb03b2f7044c2a380c48de/tests/e2e/test-sql-editor-large-text.js#L1) 从 `d:/kairo/node_modules/playwright` 加载模块，`:14` 固定服务地址；脚本位于统一 runner 自动发现目录之外，没有出现在 npm test 的 12 个脚本或 e2e/tests 中。本次没有运行这个浏览器脚本，路径不可移植和入口断链由源码确定。

**实施方案。** Go 生成真实序列化契约 fixture，前端测试直接消费；禁止用“新旧字段都填上”的 mock 兼容真实系统不存在的行为。性能测试同时验证结果正确性，语义扫描与高亮降级各自独立验收。所有 E2E 用 `require('playwright')`、`BASE_URL`、`path.resolve` 和单一 runner 注册机制；每个模块记录 discovered/executed/passed/failed/skipped，选择器没有匹配用例时不能输出伪成功。重点交互测试应经真实点击、键盘、拖拽触发，程序化直接改 value/style 仅作为低层补充。截图与 trace 绑定 SHA、测试名、运行时间和 viewport。

**验收。** 在两个不同绝对目录以及 Windows/Linux checkout 中都能发现同一用例集合；真实契约 fixture 必须让旧命名缺陷变红；220KB 参数样本不能因性能测试而丢参数。CI 区分未运行和通过，报告不得把静态资源 200、脚本文件数量或子断言数等同于已覆盖业务流程数。

## 八 按依赖实施的开发计划

建议按以下批次形成独立可审查提交。每批包含对应失败回归及运行记录，完成相应验收后再合入。批次表达的是依赖与边界，不是未经评估的工时承诺。

| 批次 | 对应问题 | 修改边界与交付标准 |
|---|---|---|
| A 测试隔离 | QA-01 QA-02 | 隔离默认单测与真实环境，接入真实契约 fixture；后续修复有可重复且不会连接业务资源的验证入口 |
| B 查询及格式化 | DB-01 DB-02 DB-03 DBUI-02 DBUI-04 | 拆分读取与编辑能力规划，保留标识符语义，消除嵌套连接等待；词法按方言保真，绑定扫描不随高亮被禁用 |
| C 网格及 LOB | DBUI-01 DB-04 DB-05 DB-06 DB-07 | 同批统一能力、列绑定和行身份；随后验证类型与乐观并发；没有可验证身份时明确只读 |
| D 比较保存 | CT01 CT06 CT02 CT04 | 文档身份、草稿提交、保存快照、版本推进及交换撤回共用一致状态规则 |
| E 路由及历史 | DBUI-03 DBUI-05 DBUI-06 CT03 CT05 CT07 | 孤儿页签不自动改源，快捷键只归当前工作台，关闭释放资源，历史单一存储，复制依据完整数据 |
| F SFTP | OTH-03 OTH-04 OTH-05 OTH-06 | 逐段原始路径解析、身份全链路传递、写入歧义终止；消除每文件完整目录枚举 |
| G 升级与请求 | OTH-01 OTH-02 | 跨进程内核锁替换 PID 删锁策略，根 WSDL 与 schema 共享可信 origin 认证头策略 |
| H 远端 shell | OPS-01 | 用可移植转义恢复关键词字节，完成 dash Bash 与目标服务器兼容验证 |

### 数据库协议的统一约束

查询结果必须携带不可变的来源身份，至少包括 `source_id`、`source_fingerprint`、`session_id`、`result_id`。服务端保存结果列索引到物理字段的绑定，页面显示名不参与物理目标推断。写入动作依据真正的 `can_insert`、`can_update`、`can_delete` 分别判定；每行定位使用本次查询产生的 row reference，并在服务器侧验证它属于相同来源、查询与会话。

推荐请求形状如下，字段名称可在实现时统一，但语义不能省略。

```json
{
  "result_id": "server-issued-result-id",
  "session_id": "original-session-id",
  "mutations": [{
    "action": "update",
    "row_ref": "server-issued-row-reference",
    "changes": [{"column_index": 1, "value": "new", "original": "old"}]
  }]
}
```

`row_ref` 应由服务端保存或签名，绑定源 fingerprint、物理表、定位值和有效期，不是未经验证的客户端 ROWID。表结构变化、切源、恢复备份和事务终结使旧编辑上下文失效时，要返回明确的过期错误并保留用户草稿。导出与 LOB 查询复用同一来源身份，不能各自重新猜表和行。

### 比较保存的统一约束

每侧维护 `{documentId, source, content, baseline, version, editSeq, loadSeq, draft}`。文档切换确认通过前不变更这些字段；用户取消意味着旧状态保持完整。保存先提交行内草稿，再捕获不可变快照；响应成功后推进该快照的 baseline 和 version，保留用户在等待期间继续输入的内容。如果 `documentId` 已变化，则不把旧响应写回新文档。

```text
保存命令
  提交当前行内草稿到文档模型
  捕获 documentId content version editSeq
  串行发送条件写入
  成功后取服务端新 version
  同一文档时推进 baseline 到本次保存的 content
  保留当前 content 并重新计算 dirty
  外部版本冲突时展示对照并保留草稿
```

后端版本须绑定源文件身份；备份与条件写入继续保留。不要通过在冲突后自动重读覆盖编辑器、把 expected 置空、或忽略 409 来获得“保存成功”。

### 交给 AI 的执行约束

后续 AI 应先核对当前仓库 SHA；如果用户继续提交了修改，重新检查对应函数后再应用方案，不能盲按旧行号改写。先把本文反例转成会失败的正确行为测试，再修改实现。每批反馈问题编号、变更文件、关键设计、测试命令及实际结果。新增 mock 必须来自真实协议；SQL 输出、文件字节、行定位、请求头和锁持有者数量是验收依据，不能仅检查源码字符串。

未完成真实 Oracle 11g、Windows 或浏览器验证的项目保留未验证状态。修复结果不能因为有截图就忽略后端契约，也不能因为单测通过就声称已经做过页面操作。用户原配置与数据不得作为默认测试夹具。

## 九 浏览器与真实环境补测矩阵

下面所有浏览器条目均为**待执行**。当前没有新页面截图。建议固定 Windows 10 或 11 与用户实际浏览器，在独立测试数据上运行；分辨率至少 1366×768 和 1920×1080，并补系统缩放 125%/150%。五套主题逐项检查本次涉及的编辑器、弹窗、按钮焦点和禁用态。表中的截图名是建议输出名，不是已存在文件。

| 场景 | 操作与应有行为 | 建议证据 |
|---|---|---|
| SQL 拉伸与大文本 | 输入 40 行并拖拽到 520px，再输入 2500 行与 220KB 长注释；正文、光标、滚动与高亮层对齐，参数不丢 | ui_sql_resize.png 与前后尺寸记录 |
| 格式化语义 | 粘贴本文两个反例，点击格式化并撤销；只读真库比较原文与格式化结果 | ui_sql_format.png 与数据库结果 |
| 绑定参数 | 首条 SELECT 含 id 参数，后接长注释；点击绑定参数并执行首句 | ui_sql_bindings.png 与实际请求体 |
| 网格主键及别名 | 使用真实响应打开编辑，验证 K LABEL 与交叉别名，检查磁盘数据库行 | ui_grid_alias.png 与提交前后表快照 |
| LOB 与翻页 | 无 PK 且标量重复的两行，不同 LOB；查看第 2 行并翻第 2 页后更新 | ui_lob_rowref.png 与返回内容校验 |
| 源删除与恢复 | A 页签保留 SQL，删除 A，再刷新与恢复备份；保持孤儿状态 | ui_orphan_source.png 与零 B 请求断言 |
| 比较取消切换 | A 修改后打开 B 并取消，再保存；A 更新而 B 不变 | ui_compare_cancel.png 与磁盘对照 |
| 行内快捷保存 | 差异单元格保持焦点，输入后 Ctrl S；文件含最新输入 | ui_compare_inline_save.png 与字节校验 |
| 慢保存继续编辑 | 延迟一次保存响应，在等待期继续输入；第二次保存成功，草稿未丢 | ui_compare_pending_save.png 与请求版本 |
| 交换与撤回 | 两个不同路径和编码的文件交换后撤回；身份版本全恢复 | ui_compare_swap_undo.png |
| 多页签资源 | 比较转后台后 Ctrl Z 不处理；关闭扫描页后停止请求 | ui_tabs_cleanup.png 与网络 trace |
| 虚拟全选 | 1000 行和 50000 行，滚动到中部全选复制，首尾标记完整 | ui_compare_select_all.png 与剪贴板文本 |
| 历史归属 | A/B 查询，删除和清空后刷新；不串源、不复活、状态正确 | ui_history_source.png |
| GBK 文件身份 | UTF8/GBK 同显示名文件及嵌套目录，分别点击编辑和重命名 | ui_sftp_identity.png 与原始文件名字节 |
| 主题及布局 | 五套主题、两种分辨率及缩放下检查截断、重叠、焦点与滚动容器 | ui_theme_layout.png 与尺寸环境 |

Oracle 11g 集成应覆盖堆表、IOT、视图、真实 quoted 名称、无主键 LOB、完整及部分投影的 PK/UK、两会话并发和未提交事务。SFTP 需真实构造不同编码的原始文件名字节；只用 UTF8 中文目录不等于 GBK 验证。升级锁在 Windows 真实进程上补杀进程及恢复测试。性能项记录实际的请求数量、连接等待、首包时间、输入到绘制耗时和峰值内存，本文未给出未经实测的性能改善倍数。

## 十 交付证据与复跑方法

Markdown 与 Word 包含同一份实施内容。证据压缩包收录本次新增复现代码、Go overlay 准备脚本、原始结果和隔离 HTTP 测试记录；不含用户数据、业务凭据或运行时二进制。

在审核的提交或要验证的修复分支执行：

```bash
node compare-tabs-repros.js /absolute/path/to/kairo
node db-frontend-repro.js /absolute/path/to/kairo
python3 prepare-overlays.py /absolute/path/to/kairo
```

准备脚本会输出只执行 TestAudit 的 Go 命令，使用生成的绝对路径 overlay，不向产品源码目录写文件。源码复现与未来修复回归的断言方向不同，详见证据包 README。不要直接在用户已有配置上运行 API 测试脚本；真实 HTTP 测试需先新建独立运行目录、空服务配置与专用 fixtures，包中保存本次执行记录以供复核。

推荐最终验收顺序为：测试隔离门禁 → 精确失败回归转绿 → 现有离线测试 → 独立真实数据库及 SFTP 集成 → 浏览器点击与截图 → Windows 原生专项。任何一步缺环境都保留未验证项，不以其他层级的成功替代。
