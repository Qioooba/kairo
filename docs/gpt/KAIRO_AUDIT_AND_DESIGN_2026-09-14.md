# Kairo 全栈代码审查与开发设计文档

**审查日期：2026-09-14**  
**仓库：Qioooba/kairo；固定代码基线：`dc75701583fe78ec8b8bcfa706d77c68a7ba6011`**  
**适用对象：实施 AI、项目负责人、开发、测试、发布人员。**  
**交付类型：审查结论 + 增量设计 + 可执行任务定义。没有修改用户仓库。**

---

## 0. 先读结论与使用规则

### 0.1 总体判断

项目不是需要推倒重来的原型。数据库页签事务、资源绑定、参数化查询、文件比对任务、WAS 输出保护、WSDL 生成和升级协调已经构成一套有实际工程投入的本地运维工作台。

但不能根据“功能已实现”“历史报告通过”认定没有问题。当前主要风险是：**单个功能内部已有防护，跨功能的数据身份、执行上下文、失败结果和生命周期尚未完全一致**。数据库导出和 LOB 链路尤其明显；另有路由异步清理、XSD 身份及外部工具输出的问题。

下一版本建议定位为“现有能力可靠性收敛”，而不是扩大产品范围。先修可能产生错误数据或错误脚本的问题，再修高频功能闭环，然后补 UI 一致性、兼容性和真实验收。

### 0.2 审查边界，不得改写成更强的结论

本次读取了仓库信息、近期 35 条提交检索结果、最新提交详情、关键目录树、路线文档及下列关键代码。不同模块审查深度不同：数据库链路较深；比对、打包、生成、升级读取了关键分支；其他页面主要做信息架构和验收设计。

**未在本环境完成整仓编译、整仓 Go/Node 测试、真实浏览器点击验收、Windows 本地 UI、真实 Oracle 11g/MySQL/Redis/FTP/SFTP 集成测试。** 通过 GitHub 连接器可以读代码，但工作容器没有取得可运行整仓；没有生产凭据和真实数据库。历史文档中的测试通过记录，不是本次执行结果。

附带的 `audit_contract_probes.cjs` 运行了 7 条“源代码逻辑隔离反例”。它们只演示明确的控制流/身份规则问题，不是从当前仓库直接运行的测试，也不证明线上表现、平台兼容性或性能。实施时必须把这些反例移植成真正的仓库回归测试。

页面建议不是“本次肉眼见到了溢出”的截图审计结论。未实际打开的页面不得标记为视觉验收通过。

### 0.3 证据类型与优先级

| 标记 | 含义 | AI 应怎样处理 |
|---|---|---|
| C：代码确认 | 在已读代码中有直接证据，触发条件可描述 | 先写回归测试，再最小修改 |
| R：待实测风险 | 结构或控制流存在风险，但实际发生依赖平台、驱动、并发或故障 | 先复现/故障注入；未复现不冒充已修复 |
| D：设计优化 | 产品/架构建议，不代表原功能必定错误 | 按任务阶段增量实施，不自行扩张 |

P0：发布前阻断会静默产生错误数据、危险脚本或误定位对象的路径。P1：高频功能不可用、状态不可信或关键恢复缺口。P2：体验、可维护性、性能与兼容性完善。优先级表示处理顺序，不代表已发现远程可利用安全漏洞。

### 0.4 开工前必须检查

执行 AI 先记录当前分支、HEAD、`git status --short`。若 HEAD 不等于本报告基线，逐项对照对应函数：问题仍存在才修改，已修复只补测试。**禁止 reset --hard、清理用户未提交代码、把代码回退到本报告版本。**

不得把整个报告一次性交给 AI “自由发挥重构”。按后文阶段，每次只实现一个任务或一个紧密相关的任务组；每次提交都要能单独解释和回退。

---

## 1. 产品路线锁定

### 1.1 本项目到底是什么

这是 **Kairo 本地优先运维工具箱**，不是 `Qioooba/kairo-ide`，不是 Theia/Monaco IDE，也不是云端多租户平台。主要服务开发、运维、DBA 的内网工作：SSH、日志、文件、Oracle/MySQL/Redis、比对、WAS 打包、HTTP/WebService、WSDL Java 生成、便笺和定时任务。

必须保留现有 Go 后端、原生 JavaScript 前端、内嵌静态资源、本地数据目录和已有模块。沿用 `web/workbench/` 的共享能力思路，优先扩展已存在的模块，不能重新建立一套同名状态系统。

### 1.2 不允许实施 AI 做的事情

- 不引入 React/Vue/Electron/Theia，不改微服务，不增加 Redis/MQ 作为 Kairo 的必需运行依赖，不把本地配置迁到云端。
- 不通过禁用 LOB、禁止所有复杂查询、删除写入能力、削减主题/工具页面来“修复”。无法安全执行的特定请求可以明确拒绝，但要保留其他正确路径和恢复办法。
- 不给任意 SQL 自动加主键排序、`COUNT(*)` 或全量扫描；不把元数据请求塞回每次首屏的串行热路径。
- 不以放宽认证、Token 校验、来源校验、路径白名单、TLS 校验、生产确认、行定位条件来绕过报错。
- 不把主线 Go 工具链降到旧版本来兼容 Win7。Win7 属于单独 legacy 路线；主线不能继承另一项目的 IE/JDK 约束。
- 不默认启用遥测，不上传 SQL、结果数据、日志、连接信息。诊断导出须脱敏并由用户发起。

### 1.3 已有能力，禁止重复发明

已有页签事务、会话隔离、网格修改、完整文件重新解析导入、生产写入确认、对象工作台、连接恢复边界、比对任务上限、WAS 暂存与回滚、WSDL 内置生成暂存、升级快照。后文是在这些实现上补契约，不是宣称它们不存在。

尤其注意：当前 HTTP `/api/database/batch` 调用的是 `ExecuteSessionBatch`，它限定 DML。不能拿 `query.go` 里的旧 `ExecuteBatch` 直接断言当前批量接口可以被 COMMIT 穿透。旧辅助函数的风险另做调用关系清理，不混淆线上可达路径。

---

## 2. 近期实现评价与风险分布

2026-09-09 的近期大提交集中在数据库、比对、打包、WSDL、通知、前端生命周期和 Go/Oracle 驱动；最新提交继续优化分页首屏和 LOB 元数据缓存。方向与原路线一致：提升工作台效率，并补生产场景防护。

值得保留的成果包括：首批少量行尽早返回、分页多取一行判断下一页、不强制给无排序 SQL 注入全局排序、会话内 DML/DDL 边界、资源删除后不静默绑定新资源、导入不只执行预览行、比对任务数量和保留期限制、输出只清理已知归属产物。

需要收紧的是“优化后所有消费者是否仍理解相同的数据”。例如，首屏 LOB 变成 lazy 对象后，下载、导出、SQL 生成不能继续当普通字段；查询携带参数与事务后，导出不能仍调用老的无状态入口。近期改动面较大，应按功能契约分提交，避免一个提交同时修改查询执行、UI 外观、生成器和打包发布。

| 模块 | 本次证据深度 | 当前判断 | 后续要求 |
|---|---|---|---|
| 数据库查询/分页/LOB/导出/事务 | 关键调用链代码审查 | 有明确缺陷，优先处理 | 单元 + HTTP 契约 + 真实 Oracle/MySQL |
| 数据库对象工作台/导入/Redis | handler 与历史文档，未遍历全部内部实现 | 已有较多能力，不能认定已全部验收 | 权限/事务/数据类型矩阵 |
| 比对 | FS 入口、扫描主循环、任务管理 | 已有资源保护；浅比较语义与失败结果需清晰 | 真实协议、变更冲突、取消 |
| WAS 打包 | 暂存发布/归属/回滚关键代码 | 已有防护，不应推倒；补断电与副作用结果 | 文件故障注入和原目录校验 |
| WSDL 代码生成 | 来源/本地 XSD/两种输出路径 | XSD 身份碰撞；工具模式输出隔离不足 | 相对依赖图、工具失败与编译验收 |
| 升级 | Run 主流程 | 普通错误回滚不等于崩溃恢复 | kill-point + 恢复日志 |
| 全局前端 | app/index/database 关键代码 | 异步清理明确缺口，导航需要任务策略 | 路由竞态与 UI 自动化 |
| 日志/SSH/HTTP/便笺/任务等 | 页面导航与现有路线，非完整实现审查 | 提供专项验收，不做无依据“无 bug”保证 | 逐项补足实际环境证据 |

---

## 3. 缺陷总表

| ID | 类型 | 优先级 | 问题 | 最小交付 |
|---|---|---|---|---|
| DB-01 | C | P0 | UPDATE 导出猜测 ID/ROWID，且复合主键不要求完整 | 可信且完整的行定位计划 |
| DB-02 | C | P1 | lazy LOB 使用原始页签 ID，后端却按已隔离 ID 校验；无事务仍绑定事务 | 统一客户端 ID → 服务端会话映射 |
| DB-03 | C/R | P0 | 无主键时把普通列当键；分块多次查询缺一致版本保证 | 可信定位 + 下载级一致性 |
| DB-04 | C | P1 | 导出丢失绑定参数和页签事务 | 共享查询执行上下文 |
| DB-05 | C | P0/P1 | SQL 导出把 LOB/截断对象当字面量；导出失败可生成貌似成功文件 | 数据完整性预检 + 成品发布 |
| DB-06 | C | P1 | 用保留名前缀判断辅助列，误删业务列 | 分页计划显式标记辅助列 |
| DB-07 | C | P1 | 引号标识符转大写，缓存混淆不同 Oracle 对象 | 精确保留对象身份 |
| DB-08 | C/D | P2 | 内层 ORDER BY 被算成分页有序 | 方言与层级感知的排序提示 |
| DB-09 | R/D | P1 | 提交确认丢失后缺明确“不确定”结果契约 | 事务终态记录与禁止盲重试 |
| UI-01 | C | P1 | 旧页面异步返回的清理函数被丢弃 | 一次性 disposer + 路由作用域 |
| UI-02 | C/D | P2 | 路由层统一取消操作，缺操作级离开策略 | 保留/取消/确认的显式契约 |
| GEN-01 | C | P1 | 不同子目录同名 XSD 被 basename 覆盖 | 规范 URI 依赖图 |
| GEN-02 | C/R | P1 | 官方工具直接写最终目录，与内置暂存行为不同 | 隔离工作区 + 验证后发布 |
| UPG-01 | C/R | P1 | 多文件升级缺持久提交阶段和崩溃恢复入口 | 恢复日志 + 启动恢复 |
| QA-01 | C | P1 | npm test 固定失败，headed 命令采用 Unix 写法 | 跨平台一致测试入口 |

DB-03 的重复定位逻辑可由代码确认；跨块混合版本的实际表现要故障/并发验证。UPG-01 不是宣称已经丢失用户文件，而是当前顺序替换结构不能直接承诺断电时跨文件原子性。

---

## 4. 数据库核心整改设计

### DB-01：UPDATE 导出必须使用可信、完整、同来源的键

代码证据：[S07 · `internal/httpserver/handlers_database.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/httpserver/handlers_database.go#L400-L850)；[S08 · `internal/dbconsole/export.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/export.go#L1-L355)。

**位置**：`internal/dbconsole/export.go::WriteUPDATE`；`handlers_database.go::handleDatabaseExport`。

**当前行为**：没有字典主键时，WriteUPDATE 仍依次寻找名为 ROWID、ID、表名_ID 的列作为 WHERE；已有复合主键时，只检查 `whereIndices` 非空，没有验证每一个主键列都存在。主键元数据获取错误在 handler 中没有作为导出失败向用户明确返回。

**危险不在“立即执行”**：导出本身只是生成脚本；但用户复制执行后，WHERE 可能匹配多行，从而产生错误更新。不能用“尚未自动执行”降低对生成脚本正确性的要求。

**复现 A**：测试表没有唯一约束，有两行相同 ID；导出 ID、NOTE 两列为 UPDATE。当前逻辑会猜 ID 为键。  
**复现 B**：主键为 `(TENANT_ID, ROW_ID)`，SELECT 只返回 TENANT_ID、NOTE。当前逻辑可仅用 TENANT_ID 生成 WHERE。  
**复现 C**：查询 `SELECT 123 AS ROWID, ...`。列名不能证明它是该基表真实 ROWID。

**实施步骤**：

1. 删除按列名猜键的分支。字典读取失败返回明确错误，禁止继续猜键。
2. 新增小型 `ExportTargetPlan`（名称可遵守仓库命名习惯），包含精确 owner/table、来源证明、键定义、每个键在结果中的列下标、数据源配置指纹。
3. UPDATE 导出首先验证结果确实对应目标基表。JOIN、聚合、表达式映射、同名列或不明确来源不得仅靠 FROM 正则推断为安全 UPDATE；提供明确原因及 CSV/JSON 替代路径。
4. 主键必须全部存在且能正确映射；缺一列即拒绝。键值 NULL、截断、lazy、不支持的二进制表示均不能作为定位值。
5. ROWID 仅允许后端确认的单基表、真实 ROWID 投影且目标一致；普通同名别名不算。跨库/跨环境持久脚本默认不用物理 ROWID；高级使用须明确“仅当前目标有效”。
6. 本阶段只要求主键和可信 ROWID，不顺带实现复杂可空唯一键推断。未来支持唯一键时必须验证非空与唯一约束。
7. 所有行先预检，再开始写下载响应或最终文件。当前上限内可用内存预检；不把全库结果无限装内存。

**验收**：无约束 ID 拒绝；缺复合键拒绝；完整复合键生成完整 WHERE；伪造 ROWID 拒绝；真实单表支持路径仍可用；字典权限不足显示具体原因；与用户列大小写、别名映射精确一致。

**禁止**：以全部可见列兜底、加 ROWNUM=1 掩盖多行、把 WHERE 缺失变成 `1=1`、直接信任客户端声明的 primary_key。

### DB-02：lazy LOB 的会话身份修复

代码证据：[S09 · `internal/httpserver/handlers_database_lob.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/httpserver/handlers_database_lob.go)；[S11 · `internal/dbconsole/lob_execution.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/lob_execution.go)；[S13 · `web/pages/database.js`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/web/pages/database.js)。

**位置**：`database.js` lazy 分支；`handlers_database_lob.go::handleDatabaseLobToken`；`handlers_database.go::scopedDatabaseSessionID`；`lob_execution.go::withLOBQuery`。

前端发送 `session_id: sess().transactionId`，是 `dbtab-...`。Token handler 却在登录模式下要求它已带服务端 `u:<hash>:` 前缀。正常客户端 ID 会被判为不属于当前用户。与此同时，前端普通 SELECT 也发送页签 ID，但请求级只读查询不会留下同 ID 的持久事务；后续下载会被 `withLOBQuery` 拒绝为事务已经结束。

**修复契约**：客户端永远只传原始页签 ID；服务端入口负责且仅负责一次 scope 映射，不能要求浏览器构造内部用户名散列。Token 中可使用内部受信 ID，不能混淆两种类型。

具体实现：

1. token endpoint 对原始 ID 执行与 query 相同的格式校验和用户隔离转换。
2. 检查实际是否存在该页签事务。原查询没有持久事务时，Token 的事务引用应为空；存在时绑定真实内部 ID，结束后失效。
3. “原查询是否在事务内”不能仅由点击时重新猜测。短期实现将 `transaction_pending` 和不可变查询执行身份放入结果上下文；生成 Token 时后端再次校验。
4. 不允许自动创建空事务来迎合 Token，也不允许对失效的事务 Token 静默改走新连接。
5. 同一来源/页签新的查询返回后，旧 lazy 回调不能写入新结果。捕获 sourceId、sessionId、runSeq、resultVersion、row/column identity；不继续读取变化的全局 state。
6. 普通无事务下载的 UI 文案应明确“按当前行重新读取”，不声称一定等于几分钟前的结果快照。

**验收**：登录/未登录 × 有/无待提交事务 × 第一页/后续页；A 用户不能借 B 的事务；普通 SELECT 的 lazy LOB 可以正常使用；事务结束报失效而不是切连接；点击后切 Tab/数据源不串行。

### DB-03：LOB 不允许模糊定位或混合版本下载

代码证据：[S09 · `internal/httpserver/handlers_database_lob.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/httpserver/handlers_database_lob.go)；[S10 · `internal/dbconsole/lob_stream.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/lob_stream.go#L1-L225)；[S11 · `internal/dbconsole/lob_execution.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/lob_execution.go)。

**代码证据**：token handler 未查到主键时把 `req.Keys` 的所有字段变成 `pkCols`；LOBRef 主要检查字段存在；chunked 下载用 QueryRowContext 查长度和每块内容。没有主键的两行可以拥有相同普通字段但不同 LOB。QueryRowContext 不会替应用验证唯一性，它取首行并忽略其余行。

**定位修复**：

1. 行定位方式只允许“完整主键”“后端验证的全非空唯一键”“受信单表 ROWID”。禁止把所有 scalar 值叫主键。
2. 对任意 SQL 的 LOB，不通过 `detectTableName()` 正则和当前 schema 下拉框拼出肯定正确的源。能证明来源才支持该定位方式；不能证明时返回能力说明，允许用户在安全单表浏览中继续查看。
3. 若需兼容无法直接证明唯一的旧请求，最多查询两条匹配记录并显式判歧义；不能把 `FETCH 1`/QueryRow 当唯一性检查。即使恰好一条，也不能把未受约束字段永久标记成主键。
4. Token 绑定数据源身份/配置指纹、数据库用户、精确对象、列、定位、查询版本、可选事务和过期时间。源配置改变必须失效，不能只比较数据库用户名。

**下载一致性修复**：固定同一连接不等于固定同一数据库快照。chunked 多次 SELECT 在默认 READ COMMITTED 下可能跨不同提交版本。

选择顺序固定为：a）能力允许时使用一次查询取得且生命周期受控的 LOB reader/locator；b）兼容回退使用本次下载独立只读/一致快照，必须在开始任何查询前建立且在结束清理；c）不能保证一致性的驱动/场景明确提示并失败，不拼接未经验证的多个版本。**已有待提交 DML 的页签不能为了下载而提交、回滚或随意重设事务隔离级别**；优先同事务的一次查询 LOB 读取，无法保证的后备路径单独验收。

Oracle 11g 与当前 ORA-01466 规避逻辑需要联合验证，不能重新给所有普通查询无条件加 `SET TRANSACTION READ ONLY`。只读快照应是独立、可检测能力，不影响当前首屏性能路径。

**验收**：两条普通列相同而 LOB 不同的记录必须拒绝模糊 Token；下载过程中另一连接反复提交 A/B 两个同长度大对象，成品只能完整 A 或完整 B，不能混合；读写事务不被下载动作改变；取消/超限/断线不返回成功成品。

### DB-04：查询、导出共享执行上下文

代码证据：[S07 · `internal/httpserver/handlers_database.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/httpserver/handlers_database.go#L400-L850)；[S08 · `internal/dbconsole/export.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/export.go#L1-L355)。

当前 query 传递 Parameters、scoped SessionID；export 对 CSV 调用 StreamQueryPage，对其他格式调用 CollectQueryPage，后者仍调 StreamQueryPage。这两条路径丢弃参数与事务。

**第一步最小修复**：增加参数化、会话感知的 Collect/Export 入口，复用既有核心执行器。不要直接对 SQL 做字符串替换。入口统一校验 raw session ID 并转换一次，保留 timeout、cancel、source fingerprint 和参数类型。

**第二步明确导出语义**：

- “导出当前结果”：不重新查询，导出当前结果快照，包含原顺序；本地过滤/排序/隐藏列是否生效必须由用户选择并在摘要展示。
- “重新查询并导出”：明确会重查，沿用 SQL、参数、源与有效事务，结果时间可能变化。
- “完整结果导出”：只在完成有界磁盘成品管道后开放；是一次受控流查询，不是把分页不断拼接冒充一致快照。

不能强制所有模式都持有长事务。当前结果快照可先限定为现有页大小/预算。完整导出属于同路线后续能力，不应阻塞当前参数与事务修复。

请求模型建议沿用现有 `databaseQueryRequest`，增加 `export_scope`、`result_id` 等向后兼容字段；空值保持旧的分页重查行为，但修正丢参数缺陷并明确提示。对带 session 的导出逐请求重新鉴权。源删除/配置变化时不得换源重查。

**验收**：带日期/空值/中文/数值参数在 CSV/JSON/XLSX/INSERT/UPDATE 的结果一致；未提交 UPDATE 后查询与同事务导出一致；另一会话不能看到未提交值；当前结果模式不发起额外 SQL。

### DB-05：区分真实数据、预览和完整成品

代码证据：[S04 · `internal/dbconsole/query.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/query.go#L1-L885)；[S07 · `internal/httpserver/handlers_database.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/httpserver/handlers_database.go#L400-L850)；[S08 · `internal/dbconsole/export.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/export.go#L1-L355)。

当前 Fast 模式输出 `{kind:'clob', lazy:true, ...}`，导出仍复用该查询；ExportSQLLiteral 对 map 调 ExportCellText 再加引号，可能把 LOB 描述 JSON 当列值。`kind:'binary'` 直接输出 NULL，截断文本也可能以预览代替全文。这些不是可靠的数据迁移脚本。

**数据契约**：新增或复用严格 Cell 状态：`null / scalar / text_preview / binary_preview / lob_ref`，另有 `complete`、`original_size`、`encoding`、`exportability`。兼容现有字段，不把所有单元格重构成复杂类。

SQL 导出规则：只序列化完整、类型明确、可以无损表示的值。LOB 需要完整加载且满足目标数据库字面量/脚本大小规则；否则导出计划明确报告不支持的列和原因。禁止把 placeholder、token、预览、二进制描述或 NULL 偷换成实际值。Oracle 大 CLOB 不能简单做一个超长 SQL 字符串；本阶段可以保留受控完整值路径，其他对象推荐独立文件导出，不假装一个 SQL 文件能无损覆盖所有类型。

普通 CSV/XLSX/JSON 可提供“预览导出”模式，但列旁/manifest 必须保留未完整标记；默认数据导出不能静默丢内容。Token、连接秘密不写入导出文件。

**错误成品问题**：非 CSV writer 出错后目前 handler 只 audit 然后 return；尚未写 body 的某些错误可留下 200 空附件。CSV 已开始输出后把“导出中断”写成一条数据行，浏览器仍可能得到正常附件。审计日志不能替代用户结果协议。

**实施路线**：

1. 参数、键、目标、数据完整性、格式支持先预检；未发送 body 前返回非 2xx 结构化错误，并去掉附件头。
2. 当前受限页导出可先用有界 buffer 或临时文件完成再下载。XLSX 等必须关闭 writer 成功后再发布。
3. 较大导出写 `*.part` 临时成品，完成后原子发布，返回 bytes、rows、checksum、warnings、status=complete；失败删除或保留诊断临时件但不挂成功下载入口。
4. 仍保留直传时，中途失败必须让传输失败（不能追加错误行伪装数据）；前端不能只看 HTTP 200 就 toast 成功。日志记录 requestId/jobId 供定位。
5. “文件完成但旧文件清理失败”应显示完成且有警告，不鼓励重做有副作用的操作。

**补充边界**：JSON writer 以列名为 map key，重复列名会覆盖。实现预检时对重复列名明确拒绝旧对象格式，或增加 `columns + rows[][]` 无损格式；不得静默改名/覆盖。带点的引号标识符不能再用 strings.Split 当 schema 分隔，统一复用标识符解析。

### DB-06：分页辅助列必须来自执行计划

代码证据：[S04 · `internal/dbconsole/query.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/query.go#L1-L885)；[S05 · `internal/dbconsole/pagination.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/pagination.go)。

现在扫描列名时使用 `HasPrefix("__KAIRO_RN_")`，即便 Oracle 第一页和 MySQL 查询没有添加辅助列，也会把业务列删掉。

只读复现：`SELECT 1 AS "__KAIRO_RN_USER_COL", 2 AS "VALUE" FROM dual`。第一页应显示两列。

修改 `serverPagedQuery` 的内部返回值为 `PaginationPlan{SQL, HasHelperColumn, HelperColumnName, HelperOrdinal}` 或等效结构。只有确实注入过的列才允许过滤。ordinal 可在拿到元数据后按已知投影结构验证；不能只凭名称猜。生成别名要避免与原投影冲突，无法安全包装的查询应明确诊断或使用受控游标路径，而不是删用户列。

现有 Oracle 11g ROWNUM、FIRST_ROWS、page_size+1、偏移上限都要保留。不要为修这个 bug 新增每次查字典。

验收：Oracle 页 1/2、MySQL 页 1/2、用户前缀列、同名碰撞、多列查询、LOB 重写路径；用户列个数和值必须不丢，辅助列不泄露。

### DB-07：缓存对象身份不能重复大小写归一化

代码证据：[S06 · `internal/dbconsole/lob_projection.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/lob_projection.go#L1-L245)。

parseSafeSingleTableQuery 已对非引号标识符转大写、对引号标识符保留原值；buildLOBProjectionOn 随后又把 owner/table 全部 ToUpper 做 cache key。`"Foo"` 与 `"FOO"` 因此碰撞；负缓存也可能影响另一个对象。

改为结构化键，直接使用解析后的精确 owner/table。对象身份规则集中到小型 helper；禁止各处自行 EqualFold 解决引号对象。缓存需绑定源配置指纹/实际 current_schema 和元数据版本；复用现有 InvalidateMetadata，不另建失效系统。单对象 DDL 优先精确失效，源配置变化整体失效；TTL 仅兜底。

验收：引号表/引号 schema 大小写不同；普通 foo 与 FOO 命中同逻辑对象；负缓存隔离；DDL 之后变化可见；普通第一页不增加同步字典查询次数。

### DB-08：排序提示与分页能力

代码证据：[S05 · `internal/dbconsole/pagination.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/pagination.go)。

queryHasOrderBy 只看任意相邻 ORDER BY token，忽略括号深度，并使用默认方言。窗口函数/子查询排序不代表最外层结果有序。

新增 `outer_order_present` 与可选 `order_stability`，未知就显示未知；沿用旧 ordered 字段时只按顶层语义赋值。词法分析应携带方言、括号层级、引号/注释状态。不要因此引入“解析所有 SQL”的大工程，不认识的形式保守提示，不擅改执行语义。

没有排序的提示是“翻页可能重复/遗漏，建议明确 ORDER BY”；有非唯一排序仍不能承诺稳定。键集分页只适合受控表浏览，不能套任意 SQL。当前保持 offset 兼容，极深页给成本提示和已有上限。

### DB-09：提交结果不确定不是已回滚

代码证据：[S12 · `internal/dbconsole/transactions.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/transactions.go#L1-L280)。

commitEntryLocked 无论 Commit 成功或错误都会释放 registry/context，这有资源清理价值，但服务端确认丢失后应用未必知道数据库最终是否提交。后续查不到事务不能等同“之前的提交失败且可以重做”。

保留资源清理，在事务之外保存短期终态/不确定状态记录：`committed / rolled_back / outcome_unknown / expired`；同时记录操作 ID、源指纹、用户 scope，禁止保存敏感 SQL 实参。网络/driver 错误分类时只有可证明的终态才能标成功。

前端不自动重放 DML/COMMIT；outcome_unknown 显示“结果未知，请核对目标数据”，保留本地修改摘要但标记不得直接重新提交。普通逻辑校验失败与提交确认丢失分开。操作 ID 防重复只能覆盖应用已知结果，不能凭空解决数据库提交与本地日志之间的原子性；这种不确定性要诚实保留。

---

## 5. 前端架构与 UI 实施

### UI-01：统一路由生命周期，但不引入新框架

代码证据：[S14 · `web/app.js`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/web/app.js)。

`web/app.js` 对异步 route 返回的 cleanup 仅在 renderToken 仍一致时赋给 currentUnmount；已切走的旧 route 返回 cleanup 被丢弃。需要在 stale 分支立即调用 cleanup，并确保幂等。第一提交只补这条分支与回归测试。

随后在现有 workbench/core 上实现 RouteScope：记录 AbortController、定时器、事件监听、ResizeObserver、WebSocket/SSE 和 disposer。每一项注册都返回取消函数；dispose 可以多次调用但实际只执行一次。异步初始化结束后，若 scope 已失效，不能注册新资源；创建了资源就立即释放。

示意逻辑（非可直接覆盖整个 app.js 的补丁）：

```js
Promise.resolve(routeResult).then(function (cleanup) {
  if (typeof cleanup !== 'function') return;
  const dispose = once(cleanup);
  if (isCurrentRouteScope(scope)) scope.add(dispose);
  else dispose();
});
```

仅修 cleanup 不等于修完异步 DOM 污染。每次异步回调写 UI 前还需校验 scope/runSeq，禁止旧页面 await 之后重新写入共享 #view。routeRenderToken 机制保留并扩展，不建立第二套互相不一致的 token。

测试 A→B→A、多次快速切换、A 晚返回成功/失败、初始化失败后才分配资源、卸载异常继续释放其他项。控制台无未处理异常；计时器/监听/请求计数回到基线。

### UI-02：离开页面应有操作策略，不是统一杀掉

代码证据：[S14 · `web/app.js`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/web/app.js)。

当前 navigate 集中取消下载、tail、shell、上传、数据库查询。资源释放方向正确，但产品上不同操作不该拥有相同离开含义。

本阶段不重写任务系统。为已有操作补 `navigationPolicy`：

| 操作 | 默认策略 | 用户看到什么 |
|---|---|---|
| 普通只读查询、临时 tail | 离开取消 | “离开将停止读取”，回到页面可重新开始 |
| 已由后端持有的下载/打包/比对任务 | 保留后端任务，卸载前端订阅 | 统一任务入口可找回；需确认现有 manager 支持 |
| 仅前端 XHR 的上传 | 离开前确认取消；不假装后台续传 | 已传/未传、取消结果明确 |
| 未提交事务、本地脏编辑 | canLeave guard | 留在页面 / 明确回滚后离开；不能隐式提交 |
| SSH 交互会话 | 按现有连接能力选择确认断开或保留 | 断开并不等于远端进程已停止，不能误导 |

仅当后端任务生命周期独立于页面时才提供“后台继续”。本阶段不能通过文案虚构断点续传。原生 beforeunload 只兜底，不能作为唯一恢复方案。

### 5.1 全局界面规则

以下是实施目标，不是本次截图实测数据。保留五主题及现有控件风格，逐模块统一语义：

- 顶部上下文固定显示“当前工具 / 当前资源 / 环境 / 状态”。生产环境颜色和文字同时标识，不只靠红绿颜色。
- 页面一级主操作最多一个突出按钮；执行/停止/提交/回滚保持常驻，低频操作放更多菜单。不能把停止藏起来。
- 工作区保留可拖动分栏与偏好。默认布局服务 1920/2560 桌面；1366×768、125%/150% 缩放是桌面兼容验证，不将手机端作为新产品目标。
- pending、running、complete、partial、cancelled、failed、outcome_unknown 分开；toast 仅提示，关键错误须在页面/任务详情常驻。
- 主体只保留一个主要纵向滚动容器；编辑器/结果网格内部滚动按任务需要保留，不嵌套三层无意义滚动条。
- 键盘可到达主操作，焦点可见；弹窗有标题、Esc 关闭规则、关闭后焦点回原按钮。提交确认、文本复制成功/失败不能仅靠颜色。
- 不调整用户现有主题偏好、不刷新清空未提交内容。缩小窗口只改变布局，不改变数据状态。

### 5.2 数据库工作台布局合同

```text
资源栏：数据源 / 环境 / 实际驱动 / 连接状态 / 只读或允许写入
页签栏：查询名 / 资源名 / 执行状态 / 待提交标记
编辑区：执行当前语句 | 停止 | 提交 | 回滚 | 格式化 | 更多
结果栏：网格/单记录 | 当前页 | 本页筛选 | 本页排序 | 导出范围
状态栏：显示行数 / 是否截断 / 是否有下一页 / 结果时间 / 事务状态
```

源选择不要与活动 Tab 绑定资源混淆。表格上的筛选和排序如果只作用于当前已加载页，必须标“本页”；不能让用户以为过滤了整个数据库。切换记录视图沿用相同原始行身份，不用排序后数组下标当数据库定位。

LOB badge 区分 NULL、空 LOB、预览、待加载、已失效、不可安全定位；不同状态都有真实可执行动作。主键/非空/数据类型等能力说明优先使用已有 inspect 元数据，不在每次单元格绘制发请求。

导出弹窗至少显示范围、是否重新查询、是否包含未提交事务、字段完整性、目标格式。SQL 导出预检失败直接说明哪一列或哪一个主键缺失。

### 5.3 页面级开发与验收清单

| 路由/页面 | 保留的主任务 | 本次增量设计与验收重点 |
|---|---|---|
| home 首页 | 找工具、回到最近任务 | 高频工具、最近资源、未完成任务、首次使用引导；不新增无数据仪表盘 |
| websphere 日志助手 | 选环境/服务/日志并读取 | tail/历史查询边界、GBK 解码、轮转/截断/重连提示，停止常驻 |
| files 文件下载/上传 | 浏览、传输、管理文件 | 当前路径与远端身份固定；队列进度和失败重试；命名可改“文件传输”，路由保持 |
| waspack 投产打包 | 验证清单并生成产物 | 输入→预检→生成→成品；默认不破坏旧输出，覆盖影响范围可见 |
| ssh SSH 终端 | 执行和交互 | 多会话身份、连接中/重连/断开；复制选区；浏览器剪贴板失败有提示 |
| database 数据库 | 查询/编辑/管理/导出 | 按本节布局，先解决数据契约再做外观 |
| http HTTP 测试 | 构造请求并查看响应 | 超时/取消、响应头/体、请求历史；危险重试不得自动执行写请求 |
| webservice WebService | 导入 WSDL 并调用 | 导入缺依赖与解析成功分开；SOAP Fault 即便 HTTP 200 也显示业务失败 |
| wscodegen 代码生成 | 选来源→扫描→选引擎→预览/生成 | 项目扫描推荐与人工选择同屏，依赖完整性、输出文件预览和失败恢复 |
| diagnostics 环境自检 | 解释为什么某能力不可用 | 实际驱动、位数、客户端路径、证书、目录权限；脱敏导出 |
| config 系统配置 | 管理源和行为 | 明确保存范围、保存状态、丢弃；测试连接不等于已保存；秘密不回填明文 |
| downloads 下载历史 | 找到已生成文件 | 文件存在/已删除/失败/部分结果分开；跳到原任务，不能给失效链接成功样式 |
| formatter 报文格式化 | 粘贴并转换 | 大输入取消、解析错误位置、输入保持；不自动修改原文件 |
| timestamp 时间戳 | 时间与数字转换 | 单位和时区显式；显示原值；夏令时/无效日期用例 |
| cron Cron 解析 | 理解表达式和运行时刻 | 明确采用的方言、秒字段和时区；下一次执行预览与任务引擎一致 |
| jsonpath JSONPath | 对 JSON 查询 | 语法错误/无匹配/NULL/空数组分开；结果可复制不自动变类型 |
| compare 文件与文本比较 | 对照、编辑、同步 | 双侧完整身份，文本脏状态；浅比较 vs 内容一致；同步清单与冲突 |
| commands 常用命令 | 保存/复制/执行模板 | 默认复制与立即执行明确分开；绑定目标可见；命令秘密不进摘要 |
| notes 便笺/提醒 | 快速记录与提醒 | 保存反馈、搜索、跨页编辑保护；提醒与便笺内容分离但可跳转 |
| tasks 定时任务 | 定时执行和检查结果 | 运行中/成功/失败/通知失败分开；暂停不会误杀不支持取消的远端动作 |
| sponsor 投喂作者 | 支持作者 | 不干扰工作流，不阻塞错误排查，保持现有入口 |
| about 关于 | 版本、能力、诊断入口 | VERSION 单一来源、构建标识、平台和限制清晰；不删除已有彩蛋 |

桌面便笺/桌宠属于 Windows 特定能力时，macOS/Linux 明确降级，不能为了界面一致而承诺同等原生实现。保留既有能力，不作为本轮扩展重点。

---

## 6. 比对、打包、WSDL 与升级

### 6.1 比对：保持现有引擎，补语义与恢复

已看到 compareReadLimit、最大文件数量、任务并发/超时/保留限制以及 incomplete/pending 状态，不能再建议无上限并行读取全目录。

浅比较里“大小相同且时间容差内”可标 same，这是元数据相同而非字节内容已验证。产品上应显示“元数据相同（未核验内容）”，深比较才显示“内容一致”。如果当前页面已有说明，复用即可，不重复改状态格式。

同步前固定左/右来源快照、方向、相对路径和目标版本；预览期间目录内容变化要重新校验。未完成扫描不能作为整目录删除/覆盖的依据。同步结果保留每项 success/conflict/failed/skipped/cancelled，部分失败不能只显示一个成功 toast。

**专项测试**：FTP 不可靠 mtime、同大小同时间但内容不同、大小写路径、中文/GBK 文件名、符号链接与 Windows junction、扫描中新增/删除、同步时目标被外部改写、网络中断、取消后仍有 worker 收尾。协议不支持可靠原子改名/强版本比较时要显示能力限制，不伪装强一致。

不把 compareJobManager 替换成通用分布式任务平台。只抽统一任务视图/事件格式，保留各域对取消与完成的不同语义。

### 6.2 WAS：有暂存和回滚，继续补可解释性

build.go 已有 sibling staging、旧产物备份、归属检查、回滚失败保留目录。必须保留。补充以下内容：

1. 预检摘要展示根目录、输出目录、包名、清单条目、自动配对条目、覆盖策略、将替换的产物与不会动的用户文件。
2. 将“构建失败无产物”“产物发布成功”“产物成功但旧备份清理失败”分开。最后一种不能提示用户盲目重新打包覆盖。
3. 故障注入覆盖每次 rename、权限变化、空间不足、文件锁、长路径、备份恢复失败及进程终止。普通错误回滚和断电恢复分开记录。
4. 发布目录下每个产物有摘要、大小和同一构建 ID；下载前可检查产物仍属于该构建。不能仅凭文件名认定当前成品。
5. batch 模式现有 chmod 默认 777 带兼容现网含义。保留旧配置行为；新配置推荐显式文件/目录权限模板并警告 777，禁止静默把现网脚本改成 755 导致生产失败。

### GEN-01：XSD 依赖按规范 URI 管理

代码证据：[S19 · `internal/wscodegen/generate.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/wscodegen/generate.go#L1-L430)。

`loadLocalXSDs` 将同目录及一层子目录 XSD 放入 `atts[filepath.Base(path)]`。`a/common.xsd` 和 `b/common.xsd` 会覆盖，两个 namespace 的依赖可能只剩一个。

实施要求：

- 从 WSDL 文档 URI 出发逐个解析 include/import 的 schemaLocation，键是规范化完整 URI，不是 basename；每个文档保留自身 base URI。
- 依赖图有 visited、循环检测、最大节点数/深度/单文件及总字节预算；读取使用 request context。兼容现有“附件导入”时维护相对路径，basename 回退只在能唯一匹配时允许，否则明确歧义错误。
- 不再无条件扫描整个一层目录当依赖，不给未引用 XSD 随机覆盖机会；对于无 schemaLocation 的 namespace 引用，显式走附件映射/用户选择。
- 不能静默忽略读取失败、错误编码、大小超限；生成结果列出已加载、未解析和冲突的依赖。
- 本地依赖只允许显式授权的根；远程 http(s) 的跳转、认证头和超时要受控。内网服务是合法目标，不能一刀切禁私有 IP；认证头也不能跨来源默认转发。

验收：同名不同目录、../ 相对导入、重复 namespace、循环 include、深层引用、带空格中文路径、缺文件、编码错误、跨源 URL、离线已有附件。

### GEN-02：官方工具与内置生成共用安全发布边界

代码证据：[S19 · `internal/wscodegen/generate.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/wscodegen/generate.go#L1-L430)。

内置 writeFiles 已加互斥、预检、stage、备份和普通错误回滚。工具模式则把最终 OutputDir 交给 planTool，prepareToolOutputDir 后直接 runTool。工具写了一部分后失败，最终目录可能已改变；该路径不能借用内置模式的“有回滚”承诺。

改为：请求预检→独立工作目录→准备完整依赖→运行工具→收集所有输出→校验路径/体积/文件数/符号链接→兼容性检查→调用现有安全发布机制。保持每个目标目录的互斥；不同目录允许受全局资源预算控制的并行，不用一个大锁长时间串行所有工具。

取消/超时需要检查子进程树退出，不只父进程；Windows 批处理启动 Java、Unix wrapper fork 都要真实测试。工具日志设置上限但保留最后错误摘要。仅创建文件成功不等于 Java 客户端可用：用选定目标 JDK 与相应依赖编译测试，不能自动把 javax 改成 jakarta，也不能混用 XFire/Axis/CXF 运行时。

覆盖只允许本次计划涉及的产物，不能清空整个用户工程目录。预览严格零写入。

### UPG-01：升级恢复日志，不伪称多文件整体原子

代码证据：[S20 · `internal/upgrade/upgrade.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/upgrade/upgrade.go#L1-L290)。

Run 先快照，再依次 writeAtomic 每个迁移文件，最后提交 manifest；普通函数返回错误时调用 rollback。进程崩溃/强杀不会执行这个 rollback，当前入口中没有持久 in-progress 阶段供下次启动判断部分提交。

最小补充是**升级日志**，不是换数据库：

```text
idle → prepared → applying → manifest_committed → complete
                     └→ recovering → restored / recovery_failed
```

prepared 日志记录升级 ID、旧/新版本、快照路径、目标白名单、各文件旧/新摘要和不存在状态。准备与日志持久化完成后才可替换首个文件。每个阶段变更持久写入；manifest 完成后写 committed，清理是独立可重试阶段。

启动必须在各 Store 打开前检查未完成日志。依据摘要判断哪些目标已是旧/新版本；**若文件既不匹配旧摘要也不匹配新摘要，视为外部修改，不自动覆盖**。恢复操作有路径白名单、备份摘要校验和可重复执行性。无法恢复时阻止会破坏数据的写入口，给出具体恢复目录，不自动 reset 用户配置。

故障注入：日志前、快照后、首个文件后、任意文件后、manifest 前后、清理前后强杀；重复启动两次；旧二进制读取部分新数据必须被拒绝。文件 fsync/目录持久性和 Windows rename 行为分别验收，不以 Linux 单进程异常测试代替。

---

## 7. 兼容性、安全与性能设计

### 7.1 兼容性不是一张“全部支持”的标签

| 维度 | 主线要求 | 验收/降级 |
|---|---|---|
| Windows | 主线 Win10/11，位数与发布包一致 | 文件锁/长路径/中文路径/无管理员权限/高 DPI；Win7 单独 legacy |
| macOS/Linux | 核心 Web 工作台可用 | 原生托盘/便笺/密钥存储能力按平台显示，不能静默失败 |
| 浏览器 | 现代 Chromium 系为主要验收；其他声称支持的浏览器实测 | 不让 IE 解析现代 JS 后白屏，保留已有不兼容提示；不做 IE5 兼容开发 |
| Oracle 11g | 真实 11.2.0.4 验收分页、LOB、长标识符限制、字符集 | 21c XE 通过不等于 11g 通过 |
| Oracle 驱动 | 分清 go-ora 与 godror/OCI 实际使用 | 环境自检显示实际后端、位数、客户端路径与降级原因 |
| OCI | 单独发布/能力选项 | 需要本机 Oracle Client；不能把增强包宣传成所有场景零依赖 |
| MySQL | 对项目实际声称支持的版本和 SQL mode 建矩阵 | 反引号、反斜线、utf8mb4、DECIMAL/大整数、时区 |
| Redis | 按已实现数据类型/命令/TTL 能力 | SCAN 不是稳定分页快照，游标变化/重启明确提示 |
| 文件编码 | UTF-8/GBK 与 BOM、CRLF/LF | 文本比较不改变原编码；不可编码字符写前报错不静默替换 |
| WSDL Java | 引擎×生成模式×目标 JDK×javax/jakarta | 以实际编译结果而非“生成成功”判兼容 |

Go/godror 支持范围依据官方资料核对，但“某 OS 名称被工具链支持”不等于 Kairo 所有原生能力可用。不要仅根据 go.mod 添加了 godror 就断言整个产品都必须 OCI；区分构建标签/发行包和运行时选择。

### 7.2 安全：保持内网工具能力，同时避免假安全

本次确认了数据源用户范围、管理员写入门禁、生产确认、会话 scope、LOB 签名及部分路径保护已存在。不能据此声称所有 HTTP/WS 入口都通过审计；完整中间件没有在本次全部检查。

下一阶段安全用例覆盖：本地模式与认证模式、非 loopback 监听默认行为、Host/Origin、跨站写入、WebSocket 来源、文件/任务/LOB ID 跨用户访问、关闭权限后现有任务、错误/日志脱敏。不能关闭校验让前端请求“变通成功”。

SQL 关键字过滤和只读 UI 锁不等于数据库级只读沙箱。SELECT 可能调用具有副作用的函数等；生产只读要求配合数据库最小权限账户、连接/事务能力和允许操作约束。不要承诺靠扩充字符串黑名单彻底解决数据库权限问题。

敏感数据不进 URL、下载文件名、浏览器长期 localStorage 或普通审计详情。审计保留行为、目标标识、摘要、结果和关联 ID；错误链在内部保存前做秘密遮盖。前端不得把 SQL/响应内容作为不受控 HTML 执行。

TLS/mTLS/SSH host key、FTPS 证书、HTTP 重定向都按实际协议测试。临时跳过证书校验须显式提示并避免默认永久保存；不要为内网自签证书简单移除验证，提供 CA 配置和诊断路径。

### 7.3 性能：先测，再在预算内优化

已看到源结果大小、单元格预览、流式批次、并发上限和缓存。不要再次用“增加限制”替代定位。Fast 模式 scanner 只是不再展示 LOB，不能据此证明底层驱动没有把完整 LOB 拉进内存；需要 go-ora 与 OCI 各自 profile。

先记录冷启动/热缓存两组、实际硬件/存储/网络 RTT、数据库版本/驱动、行宽/LOB 大小。采集：首 metadata、首批行、查询完成、导出完成、取消确认、RSS/heap/alloc、goroutine、DB 连接占用、UI 长任务和滚动帧时间。

建议设计目标（不是本次实测值，也不是所有网络下承诺）：在固定本地回归样本上，常用 UI 操作 p95 不出现 >100 ms 主线程阻塞；取消按钮 100 ms 内显示取消中；表格可见 DOM 不随所有结果行线性增加；新实现不得比固定基线的 p95 慢超过 10% 且无解释。网络任务取消真正完成的时限按驱动可中断能力单独测。

`fastRowBytes` 是估算，不是 JSON 实际长度或驱动内存上限。必须区分源数据字节、预览字节、编码后传输字节与内存高水位。停止条件不能先加载超大对象才发现超限；读取预算要下沉到支持的驱动/SQL 投影/LOB reader 层。

优化顺序：去掉重复请求和重复转换→复用字典缓存与 in-flight 请求→结果虚拟化/后台分批→必要的驱动优化。保持缓存有界；不为首屏缓存整表、不无限扩大连接池、不给每个单元格发元数据请求。源码中名为 benchmark 的 SQL 字符串断言不是吞吐基准，必须新增真实 Benchmark/测量脚本。

---

## 8. 开发拆分与版本交付

### 8.1 阶段顺序

| 阶段 | 任务 | 进入/退出条件 |
|---|---|---|
| G0 基线与门禁 | QA-01；保存现有通过/失败/skipped；建立关键反例 | 知道测试入口和环境差异，不改业务行为 |
| G1 数据正确性 | DB-01、DB-05、DB-06、DB-07；DB-02/03 紧接 | 危险导出、错误定位、误隐藏有回归测试，正常能力保留 |
| G2 工作流闭环 | DB-04、DB-09、UI-01、GEN-01 | 参数/事务/页签身份与清理一致 |
| G3 故障恢复 | GEN-02、UPG-01；WAS/比对故障矩阵 | 旧产物不因失败丢失，未知结果不报成功 |
| G4 UI 与性能 | UI-02、DB-08、各页面清单、兼容矩阵 | 视觉/交互实测、性能证据、无功能回退 |

DB-02 与 DB-03 会触及同一 LOB 契约，应由同一实施上下文顺序完成。DB-04 与 DB-05 共享导出契约，但分开提交便于验证。不要让多个 AI 同时修改 database.js、handlers_database.go、export.go。

### 8.2 每个任务的固定交付格式

任务 ID；基线 HEAD；根因；实际调用链；改动文件；兼容行为；新增/修改测试；命令与结果；真实/模拟/跳过环境；剩余风险；回退方式。禁止只写“已全面优化”“测试全通过”。

每个关键缺陷先提供失败测试或明确可执行复现，再提交修复；测试修复后通过，相关旧回归不减少。不能删除断言、提高等待时间、把错误吞掉或者改成 skip 来交差。

### 8.3 测试入口整改 QA-01

代码证据：[S21 · `package.json`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/package.json)。

package.json 的 `test` 当前是固定失败占位；`e2e:headed` 使用 `HEADLESS=false ...`，不适合默认 Windows npm shell。修成统一 Node 启动脚本：通过 process.env 设置 HEADLESS，再 spawn 当前 Node 执行现有 E2E；不为一个环境变量引入庞大依赖。

单元测试枚举由仓库实际测试文件决定，使用可跨平台的 Node 文件枚举与参数数组，不依赖 shell glob。明确区分 test:unit、test:integration、test:e2e、check:version。现有测试脚本入口保留别名。

建议命令目标：

```text
npm ci
npm test
npm run check:version
go test ./...
go vet ./...
# 在支持 race 的 OS/CGO 环境中：
go test -race ./internal/dbconsole ./internal/httpserver ./internal/upgrade ./internal/waspack ./internal/wscodegen
# 服务已启动、隔离数据与凭据就绪后：
npm run e2e
npm run e2e:headed
```

以上是实施后的目标，**不是声称当前 npm test 已能跑，也不是本次执行记录**。真实数据库集成测试的环境变量名称先读取实际 *_integration_test.go 再使用，不编造现有参数。无数据库则显示 skipped 且不计入验收完成。

发行工具链取经验证的补丁版本，固定构建清单；go.mod 最低版本与发行工具链版本可以不同。离线构建使用 vendor 和预装工具链，禁止自动下载失败后偷偷换旧工具链。依赖漏洞通过可达性分析/官方工具确认，不凭版本旧就宣称存在可利用漏洞。

### 8.4 合入与发布门禁

P0 未完成，不发布包含这些不安全路径的正式版；可以受控发内部验证包但明确范围。P1 未完成必须列清单，不用“全功能稳定”描述。

合入需：真实改动 diff 与任务匹配；新增协议向后兼容或有明确能力协商；错误路径有断言；无秘密输出；性能预算无无解释回退；用户数据目录可恢复。最终回归至少覆盖 Windows 主用发行包、便携 Oracle 与 OCI 两种实际支持路径、真实 Oracle11g/MySQL，以及5主题主要工作页。

UI E2E 验收从页面操作，禁止用后台 API 代替创建/点击/导出等用户流程；API 仍可用于测试环境建数、故障注入和结果核验，报告要区分。不能以页面按钮可点击代替真实产物内容正确。

---

## 9. 最终验收标准

真正的“可以交付”不是提交多、截图多或无异常日志，而是：

- 原有主流程仍可用，P0/P1 确认缺陷已按用例关闭，未验证项诚实列出。
- 同一资源、页签、结果和产物的身份可追溯，查询/预览/导出语义一致。
- 取消、失败、部分完成和结果未知在界面中可区分；不能提示成功却得到空文件/错误脚本。
- Windows/驱动/数据库/编码/协议的支持有真实证据，未支持的组合可解释降级。
- 每个任务有单独证据和可回退提交；不能通过大重构把缺陷埋掉。

本报告提供足够细的增量路线，但不声称已经穷尽整个仓库所有缺陷。实施过程中发现新的相关问题必须补证据和任务 ID，不能把未审模块默认为没有问题。

---

## 10. 代码和官方资料索引

以下全部代码链接固定到审查 SHA，避免后续 main 漂移；行号是原文件参考区间。部分文件只读了指定区间或定点检索，并非逐行审完全文。

- [S01 · `README.md`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/README.md)：产品定位、部署方式、现有功能、主线/legacy 边界。
- [S02 · `docs/WORKBENCH-REFACTOR-PLAN.md`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/docs/WORKBENCH-REFACTOR-PLAN.md#L1-L205)：原有路线：正确性优先、显式资源身份、原生 JS、共享工作台能力。
- [S03 · `docs/DATABASE-WORKBENCH-AUDIT-2026-09-05.md`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/docs/DATABASE-WORKBENCH-AUDIT-2026-09-05.md#L1-L240)：历史验收及修复记录；不能当成本次测试结果。
- [S04 · `internal/dbconsole/query.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/query.go#L1-L885)：查询、会话、Fast 模式、列隐藏与扫描器。
- [S05 · `internal/dbconsole/pagination.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/pagination.go)：分页改写和 ORDER BY 检测。
- [S06 · `internal/dbconsole/lob_projection.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/lob_projection.go#L1-L245)：安全单表解析、元数据缓存身份。
- [S07 · `internal/httpserver/handlers_database.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/httpserver/handlers_database.go#L400-L850)：参数化查询、用户会话隔离、导出处理。
- [S08 · `internal/dbconsole/export.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/export.go#L1-L355)：导出格式、主键推断、SQL 字面量和 JSON 行。
- [S09 · `internal/httpserver/handlers_database_lob.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/httpserver/handlers_database_lob.go)：LOB Token 签发、会话校验、下载。
- [S10 · `internal/dbconsole/lob_stream.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/lob_stream.go#L1-L225)：LOB 行定位、长度查询和逐块查询。
- [S11 · `internal/dbconsole/lob_execution.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/lob_execution.go)：LOB 连接/事务生命周期。
- [S12 · `internal/dbconsole/transactions.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/transactions.go#L1-L280)：事务注册、提交结果清理、实际 SessionBatch。
- [S13 · `web/pages/database.js`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/web/pages/database.js)：页签、结果渲染与 lazy LOB 请求；含定点检索。
- [S14 · `web/app.js`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/web/app.js)：路由、卸载、全局操作取消、启动顺序。
- [S15 · `web/index.html`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/web/index.html#L1-L160)：导航结构、主题、脚本装载、浏览器提示。
- [S16 · `internal/httpserver/handlers_compare_workbench.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/httpserver/handlers_compare_workbench.go#L1050-L1250)：比对数据结构、FS 入口、元数据/深度比较。
- [S17 · `internal/httpserver/compare_jobs.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/httpserver/compare_jobs.go)：任务并发、超时、结果保留和清理。
- [S18 · `internal/waspack/build.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/waspack/build.go#L1-L235)：输出所有权、暂存发布和回滚；batch chmod 默认。
- [S19 · `internal/wscodegen/generate.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/wscodegen/generate.go#L1-L430)：WSDL 来源、工具输出、XSD 加载、内置输出暂存。
- [S20 · `internal/upgrade/upgrade.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/upgrade/upgrade.go#L1-L290)：升级入口、快照、多文件提交、manifest 最后提交。
- [S21 · `package.json`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/package.json)：npm test 与 Windows 有头 E2E 命令。
- [S22 · `go.mod`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/go.mod)：Go 最低版本和 Oracle 双驱动依赖。
- [S23 · `internal/httpserver/handlers_database_v2.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/httpserver/handlers_database_v2.go)：脚本、网格、导入、事务状态已有实现。
- [S24 · `internal/dbconsole/policy.go`](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/dbconsole/policy.go#L1-L235)：SQL 分类与读写策略。

近期提交：[dc757015](https://github.com/Qioooba/kairo/commit/dc75701583fe78ec8b8bcfa706d77c68a7ba6011)；[20122feed](https://github.com/Qioooba/kairo/commit/20122feedb829e55a73d6550e52b49db5e2f59f8)。提交说明不能替代最终代码证据。

官方原理资料：

- [Oracle 11g 标识符大小写](https://docs.oracle.com/cd/E18283_01/server.112/e17118/sql_elements008.htm)：非引号归一化与引号保留大小写的差异。
- [Oracle 读一致性](https://docs.oracle.com/html/E10713_02/consist.htm)：READ COMMITTED 的语句级快照与只读/串行化事务的差异。
- [Go database/sql](https://pkg.go.dev/database/sql)：QueryRowContext 不验证唯一性；连接、事务和取消行为。
- [Go Windows 支持表](https://go.dev/wiki/Windows)：主线和旧 Windows 工具链支持边界。
- [Go 1.21 发布说明](https://go.dev/doc/go1.21)：Windows 最低版本与 go.mod/toolchain 规则。
- [godror 安装要求](https://godror.github.io/godror/doc/installation.html)：CGO 构建与 Oracle Client 运行条件。

这些资料只支持相关平台/数据库原理，不代表本项目已通过对应测试。
