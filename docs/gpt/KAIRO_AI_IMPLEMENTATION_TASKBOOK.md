# Kairo：交给实施 AI 的任务书

## 1. 直接复制的开工指令

```text
你正在维护 Qioooba/kairo（本地 Go 运维工具箱），不是 kairo-ide。
先读取 KAIRO_AUDIT_AND_DESIGN_2026-09-14.md 和本任务书。
审查基线是 dc75701583fe78ec8b8bcfa706d77c68a7ba6011。
先报告当前 HEAD、工作区状态与基线差异，不回退/覆盖用户改动。

严格按 G0→G1→G2→G3→G4 执行。每次只完成一个任务或明确绑定的子任务组。
先做 QA-01，确认测试入口；随后优先 DB-01/DB-05，阻断错误脚本和错误成品。
不能把全部任务混在一个大提交。database.js/export.go/handlers_database.go 不允许多个实施者并行改。

保持 Go + 原生 JavaScript + 内嵌静态资源，优先扩展已有 web/workbench 模块。
不得引入前端框架、微服务、必需外部缓存、云同步；不得把此项目改成 IDE。
不得以禁用 LOB/导出/写入、减少主题、删工具页面作为修复。
不得取消权限/路径/TLS/生产确认检查。不能无条件给任意 SQL 加 ORDER BY 或 COUNT。

每项先核对实际调用链，再写失败测试/复现，再最小修改，再跑该项和相邻模块回归。
文档写“建议新增”的文件是设计建议，不要冒充仓库已有文件；先找可复用实现。
已被后续提交修复的缺陷，不重复改代码，补回归并给证据。
待实测风险必须先测试，不允许凭猜测宣称线上已存在或已解决。

每项交付必须含：任务 ID、根因、改动文件、兼容性、测试命令及实际结果、
使用真实还是模拟环境、skipped 项及原因、剩余风险、可回退提交。
不准删除断言、吞错误、盲目重试写操作、扩大限制、标 skip 来让测试变绿。
没有环境时明确未验证，不能写“全部测试通过”。

产品 E2E 必须以页面点击覆盖实际用户流程；后台 API 只能用于测试准备/故障注入/核验，
不能用直接调 API 代替点击页面完成流程。产物内容也要校验，不只是按钮能点。
```

## 2. 现有协议的最小增量约定

以下为新增设计，不是声称当前全部字段已存在。先完成兼容性最小修复，再逐步增加 result 快照或成品任务能力。

### 2.1 会话身份

- 浏览器只保存并提交 raw `session_id`，如 `dbtab-...`。
- handler 校验格式并调用既有 scopedDatabaseSessionID **一次**。
- 内部函数/签名 Token 使用 scoped ID；不能再经过同一个边界二次转换。
- 不相信客户端说“已 scoped”；字符串出现 `u:` 也不意味着它可信。
- 只有原执行真正使用持久事务时，结果/LOB 引用才包含事务身份；普通 SELECT 不因有 Tab ID 而拥有事务。
- 当前会话已经结束：409 + 明确失效码，禁止自动新建事务/换连接重试旧 LOB。

可在入口与内部之间增加两个不同 string 类型或命名明确的结构，防止 raw/scoped 混用；不要求一次重构所有历史接口。

### 2.2 导出请求（沿用现有入口）

```json
{
  "source_id": "source-1",
  "session_id": "dbtab-raw-client-id",
  "sql": "SELECT ... WHERE ID=:id",
  "parameters": [],
  "page": 1,
  "page_size": 20,
  "format": "csv",
  "export_scope": "requery_page"
}
```

`parameters` 必须使用仓库现有 BindParameter 结构，不创建另一套不兼容参数格式。新增 export_scope 空值兼容旧分页重查语义，但不能继续丢弃旧请求中的参数和会话。

当前结果快照能力后续使用 `result_id + result_version`。服务端应从有界、短期的受信结果上下文找来源/列映射；不能让浏览器任意声称 primary_key/ROWID 后获得安全 SQL 导出或 LOB Token。纯显示数据复制可以在前端做，但不能当作服务端认可的数据来源证明。

本轮先解决当前页数据导出；全量导出要另过预算/一致性门禁，不允许自动遍历 100 万页拼接成“全量一致快照”。

### 2.3 错误响应保持兼容

保留现有 `error` 字符串，新增可选 `code`、`operation_id`、`retryable`、`effect_status`，旧客户端不会因为 error 变成对象而崩溃。

```json
{
  "ok": false,
  "error": "缺少复合主键 ROW_ID，不能生成安全的 UPDATE 脚本",
  "code": "EXPORT_UNSAFE_KEY",
  "operation_id": "opaque-operation-id",
  "retryable": false,
  "effect_status": "not_applied"
}
```

建议错误码：EXPORT_UNSAFE_KEY、EXPORT_INCOMPLETE_CELL、AMBIGUOUS_ROW_LOCATOR、SOURCE_REVISION_CHANGED、TRANSACTION_EXPIRED、TRANSACTION_OUTCOME_UNKNOWN、EXPORT_FAILED、DEPENDENCY_AMBIGUOUS、RECOVERY_REQUIRED。

HTTP 分层：格式错误400；权限403；资源/事务版本冲突409；语义不可安全执行422；上游失败502/503。遵守仓库已有约定并保持兼容，不为统一状态码一次性改所有接口。已开始附件传输不能再假装能改成 JSON 4xx。

### 2.4 操作结果必须表达副作用

`effect_status = not_applied | applied | unknown` 与用户任务状态是两种不同信息。例如“文件已发布但清理旧备份失败”：操作结果 applied，任务 complete_with_warnings；“提交确认丢失”：unknown。不能统一转换为 failed 并提供无条件重试。

不要求本阶段做全产品通用事件平台；用已有 job manager 的适配视图即可。

## 3. 修改边界与文件级任务

### QA-01 · 建立跨平台测试入口和基线

阶段 G0；优先级 P1；证据 C；前置：无。

修改边界：`package.json`；`scripts/（复用已有测试启动脚本；缺失再新增）`。

必须新增/保留的测试：npm test 不再执行固定失败占位；Windows npm 默认 shell 下 e2e:headed 可启动；无集成环境时明确 skipped。

完成定义：记录基线通过/失败/跳过；单元和 E2E 分开；不改变业务行为。

### DB-01 · 修正 UPDATE 导出的可信行定位

阶段 G1；优先级 P0；证据 C；前置：QA-01。

修改边界：`internal/dbconsole/export.go`；`internal/httpserver/handlers_database.go`；`internal/dbconsole/export_plan.go（建议新增）`。

必须新增/保留的测试：无约束 ID 不得猜键；完整/缺失复合主键；伪 ROWID 别名；字典失败；JOIN/别名来源不明确。

完成定义：只有完整且可信的键能生成 UPDATE；拒绝发生在写附件前；正常主键导出仍可用。

### DB-05 · 导出数据完整性与失败成品控制

阶段 G1；优先级 P0；证据 C；前置：DB-01。

修改边界：`internal/dbconsole/export.go`；`internal/httpserver/handlers_database.go`；`web/pages/database.js`。

必须新增/保留的测试：lazy LOB 不进 SQL 字面量；binary 不被偷换 NULL；截断文本不冒充全文；无键不下载 200 空附件；CSV 中断不生成成功成品；重复列名不丢值。

完成定义：明确完整数据/预览；格式预检；先完成后发布；失败不显示下载成功。

### DB-06 · 分页辅助列使用显式计划

阶段 G1；优先级 P1；证据 C；前置：QA-01。

修改边界：`internal/dbconsole/pagination.go`；`internal/dbconsole/query.go`。

必须新增/保留的测试：Oracle 首/后页；MySQL 首/后页；保留名前缀业务列；别名冲突；LOB 投影列映射。

完成定义：无辅助列的计划不隐藏任何业务列；有辅助列只隐藏该计划注入的列。

### DB-07 · 修复大小写敏感元数据缓存身份

阶段 G1；优先级 P1；证据 C；前置：QA-01。

修改边界：`internal/dbconsole/lob_projection.go`；`internal/dbconsole/manager.go（仅复用失效机制）`。

必须新增/保留的测试：Foo/FOO 两个引号表；引号 schema；负缓存；源配置改变；DDL 后缓存失效。

完成定义：精确对象无碰撞；未引号对象行为保持；首屏不增加无关同步查询。

### DB-02 · LOB 统一会话映射与无事务路径

阶段 G1；优先级 P1；证据 C；前置：DB-07。

修改边界：`internal/httpserver/handlers_database_lob.go`；`internal/httpserver/handlers_database.go`；`internal/dbconsole/lob_execution.go`；`web/pages/database.js`。

必须新增/保留的测试：登录与本地模式；普通 SELECT；待提交事务；过期事务；跨用户；点击后换 Tab/结果。

完成定义：客户端 raw ID 由后端映射一次；无事务不绑定虚构事务；旧事务不换连接。

### DB-03 · LOB 可信定位和单次下载一致版本

阶段 G1；优先级 P0；证据 C/R；前置：DB-02。

修改边界：`internal/httpserver/handlers_database_lob.go`；`internal/dbconsole/lob_stream.go`；`internal/dbconsole/lob_execution.go`；`internal/dbconsole/lob_token.go`；`相关 OracleBackend 实现（先确认调用）`。

必须新增/保留的测试：相同 scalar 不同 LOB 的两行；完整主键；只在安全基表使用 ROWID；并发更新 LOB；下载取消/超限；Oracle11g ORA-01466 回归。

完成定义：模糊定位拒绝；成功成品不混合版本；事务与 OCI cursor 生命周期不被破坏。

### DB-04 · 导出透传完整查询上下文

阶段 G2；优先级 P1；证据 C；前置：DB-05, DB-02。

修改边界：`internal/httpserver/handlers_database.go`；`internal/dbconsole/export.go`；`internal/dbconsole/query.go`；`web/pages/database.js`。

必须新增/保留的测试：各格式参数化查询；未提交数据导出；源删除/修改；当前页范围与参数快照。

完成定义：参数绑定/源/事务一致，旧 max_rows/page 行为兼容，UI 明示是否重查。

### DB-09 · 事务结果未知与终态记录

阶段 G2；优先级 P1；证据 R/D；前置：DB-04。

修改边界：`internal/dbconsole/transactions.go`；`internal/httpserver/handlers_database.go`；`internal/httpserver/handlers_database_v2.go`；`web/pages/database.js`。

必须新增/保留的测试：提交成功响应丢失；确认未提交；重复控制请求；终态 TTL；禁止自动重放写入。

完成定义：未知不算回滚/成功，连接仍释放，用户有核对与恢复指引。

### UI-01 · 异步页面卸载和过期回调

阶段 G2；优先级 P1；证据 C；前置：QA-01。

修改边界：`web/app.js`；`web/app.test.js`；`web/workbench/现有生命周期模块（无则新增 route-scope.js）`。

必须新增/保留的测试：A 晚返回 cleanup；A-B-A；失败后资源创建；dispose 两次；旧回调不得写新页面。

完成定义：cleanup 仅执行一次；作用域结束后新资源立即释放；无过期 DOM 写入。

### GEN-01 · XSD 规范 URI 依赖解析

阶段 G2；优先级 P1；证据 C；前置：QA-01。

修改边界：`internal/wscodegen/generate.go`；`internal/webservice/现有 WSDL import resolver（先定位）`。

必须新增/保留的测试：a/common 与 b/common；深层相对依赖；循环；缺文件；编码错误；总字节上限；离线附件。

完成定义：不按 basename 覆盖；依赖完整性可解释；保持已有成功 WSDL 用例。

### GEN-02 · 外部生成工具隔离和安全发布

阶段 G3；优先级 P1；证据 C/R；前置：GEN-01。

修改边界：`internal/wscodegen/generate.go`；`internal/wscodegen/tool.go`；`internal/wscodegen/现有发布辅助函数`。

必须新增/保留的测试：工具生成一文件后失败；超时子进程；同目标并发；覆盖旧文件失败恢复；不同目录并行。

完成定义：失败前后的旧产物摘要一致；取消收尾可见；输出校验后发布。

### UPG-01 · 跨文件升级崩溃恢复

阶段 G3；优先级 P1；证据 C/R；前置：QA-01。

修改边界：`internal/upgrade/upgrade.go`；`internal/upgrade/snapshot.go`；`internal/upgrade/restore.go`；`internal/upgrade/journal.go（建议新增）`。

必须新增/保留的测试：每个持久阶段强杀；重复恢复；外部修改；损坏快照；manifest 前后；旧二进制拒绝不匹配数据。

完成定义：恢复发生在 Store 打开前；摘要不明不覆盖；日志和快照可追溯。

### OPS-01 · 比对与打包真实故障验收

阶段 G3；优先级 P1；证据 R/D；前置：QA-01。

修改边界：`internal/httpserver/handlers_compare_workbench.go`；`internal/httpserver/compare_jobs.go`；`internal/waspack/build.go`；`对应页面与现有测试`。

必须新增/保留的测试：浅比较语义；目标冲突；协议中断；部分同步失败；rename/空间/文件锁失败；产物成功但清理失败。

完成定义：不重写已有引擎；仅修实测缺陷；结果表达与实际副作用一致。

### UI-02 · 操作级导航策略和页面布局

阶段 G4；优先级 P2；证据 C/D；前置：UI-01, DB-09, OPS-01。

修改边界：`web/app.js`；`web/workbench/现有共享组件`；`web/pages/受影响页面`；`web/style.css（限定作用域）`。

必须新增/保留的测试：未提交事务离开；后台任务找回；仅前端上传确认取消；5主题；高DPI；停止按钮常驻。

完成定义：不新增框架；不虚构续传；不丢草稿；各页面按主文档验收。

### DB-08 · 排序提示与深分页能力表达

阶段 G4；优先级 P2；证据 C/D；前置：DB-06。

修改边界：`internal/dbconsole/pagination.go`；`internal/dbconsole/policy.go（复用 lexer）`；`web/pages/database.js`。

必须新增/保留的测试：OVER ORDER BY；CTE 内排序；顶层排序；注释/字符串 ORDER BY；MySQL 转义。

完成定义：只承诺可证明的顶层排序；不自动 COUNT 或排序全表。

### COMPAT-01 · 发行兼容矩阵与环境诊断

阶段 G4；优先级 P1；证据 R/D；前置：DB-03, GEN-02, UPG-01, UI-02。

修改边界：`实际发行脚本（开工先定位）`；`internal/dbconsole/oracle_backend.go`；`环境诊断页面/handler`；`README.md`。

必须新增/保留的测试：主线 Windows；实际支持 macOS/Linux；go-ora/OCI；Oracle11g/MySQL；离线发行包；JDK 引擎组合。

完成定义：区分支持/降级/不支持，真实驱动可见，未测项不能打勾。

### PERF-01 · 建立首屏/内存/取消性能基线

阶段 G4；优先级 P2；证据 R/D；前置：DB-03, DB-04, UI-01。

修改边界：`internal/dbconsole/现有 benchmark 文件`；`测试测量脚本`；`需要修正的热点函数（必须 profile 后确定）`。

必须新增/保留的测试：冷/热缓存；无LOB/多LOB；长宽行；慢网络；多Tab；取消；大目录。

完成定义：提供真实采样与硬件参数；无删除功能/放宽限制式优化；不编造加速比例。

### SEC-01 · 权限与本地服务安全专项验收

阶段 G4；优先级 P1；证据 R/D；前置：DB-03, DB-09, GEN-02。

修改边界：`实际 HTTP/WS 中间件（先定位）`；`各敏感端点`；`对应权限/路径测试`。

必须新增/保留的测试：跨用户资源ID；权限回收；Host/Origin/WS；日志脱敏；TLS/SSH host key；目录边界。

完成定义：不把本次局部审查当整体安全通过；修复不得移除内网合法能力。

## 4. 每次提交的交付模板

```text
任务：
当前 HEAD / 对照基线：
根因和真实调用链：
修改文件（不相关文件不得夹带）：
原行为 / 新行为：
正常功能如何保留：
失败/取消/部分成功/未知结果如何表达：
新增测试：
实际运行命令：
通过 / 失败 / 跳过：
真实数据库/系统/浏览器环境：
未验证内容：
性能影响与采样：
回退提交及数据恢复要求：
下一项任务：
```

## 5. 最终交付必须附带

修改清单、测试结果原文或机器可读结果、实际 UI 操作路径和截图、生成文件内容核验、故障恢复证明、平台矩阵、已知限制。若只是模拟测试则写模拟，不能用历史文档截图凑本次验收。

`audit_contract_probes.cjs` 仅是审查逻辑反例，可作为编写回归的依据；不得把该文件的“counterexample_demonstrated”结果写成你的修复已通过。
