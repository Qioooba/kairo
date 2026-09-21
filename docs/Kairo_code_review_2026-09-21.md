# Kairo 最近一周提交代码审查报告

审核日期：2026-09-21  
仓库：[Qioooba/kairo](https://github.com/Qioooba/kairo)  
固定审核版本：[38df3aa604817955f470eaaaa0eb4c29124972c4](https://github.com/Qioooba/kairo/tree/38df3aa604817955f470eaaaa0eb4c29124972c4)  
对比基线：[dc75701583fe78ec8b8bcfa706d77c68a7ba6011](https://github.com/Qioooba/kairo/tree/dc75701583fe78ec8b8bcfa706d77c68a7ba6011)  
整体差异：[BASE…HEAD](https://github.com/Qioooba/kairo/compare/dc75701583fe78ec8b8bcfa706d77c68a7ba6011...38df3aa604817955f470eaaaa0eb4c29124972c4)

## 1. 审核结论与阅读方法

**这些提交包含大量真实实现，但目前不能判定“提交说明中的功能都已完整实现”。** 主要问题集中在状态切换、异步任务身份、数据定位、文件身份和失败恢复。多个模块的正常路径可以工作，组合操作或故障分支仍会出现 SQL 草稿被覆盖、UPDATE 定位错行、LOB 读取错行、比对合并丢内容、保存写错文件、GBK 上传改变目标文件名字节，以及升级崩溃后恢复入口无法到达。

这是一次代码审查和定向验证，没有修改或提交仓库业务代码。下文把问题分成：

- **本周新增回归**：能定位到本轮修改引入的行为，例如最新的会话序列化副作用、备份失败后不重试、比对撤回与文件身份不一致。
- **本轮改造没有覆盖的旧路径**：例如前端右键单行 SQL 导出的危险 fallback、便笺内部切换丢草稿、上传附件按 basename 覆盖。它们在当前 HEAD 仍影响本轮功能是否完成，但不能全部算成新引入的 bug。
- **功能只完成了一部分**：已经有结构、后端能力或单一路径，却未接入真实入口，例如 path_id、可信 ROWID 导出、上传 WSDL 项目到官方生成器。
- **待真实环境验证的风险**：没有把静态推断、模拟夹具或 VM 测试包装成真实 Oracle、SFTP、浏览器或 Windows 实测。

优先阅读第 3 节的问题索引和第 5 节的 AI 修复任务。逐条根因、具体文件、行号、复现步骤、修复建议和验收条件放在后半部分模块详审。C05 与 TUI-03 是同一个问题；C10 与 QA-02 的 T060 项也是同一测试缺口，不能重复计数。

### 优先级定义

| 级别 | 本报告中的含义 |
| --- | --- |
| P1 | 可能丢失用户内容、操作错误数据/文件、泄露导入凭据，或阻断核心恢复流程；建议在依赖该功能处理真实数据前修复 |
| P2 | 功能不完整、错误提示或结果不可靠、特定条件下失败，或关键测试覆盖不足 |
| P3 | 开发流程、格式校验等较低影响问题 |

优先级是本次审查的处理建议，不是对事故发生率的统计。P1 均有具体触发条件，不代表每次正常使用都会失败。

## 2. 范围与实际验证

“最近几天”按最近 7 天处理：**2026-09-14 至 2026-09-21，main 分支 32 个提交、136 个变更路径**。区间首个提交时间为 2026-09-14 12:56:06 UTC，末个为 2026-09-21 10:48:39 UTC。已获取全部 32 个提交的详情和 patch，逐提交文件集合与整体差异的 136 个路径一致；审核时额外读取了相关调用链和依赖文件。完成前重新核对 main，仍为上述固定 HEAD。

审核判断以最终 HEAD 为准。中途提交中出现、后来已修好的问题不再当作当前缺陷；测试、文档、配置和构建变更也在范围内。完整提交清单和变更文件清单附在报告后面。

| 证据类型 | 本次实际做了什么 | 不能由此声称什么 |
| --- | --- | --- |
| 逐提交与最终源码审查 | 读取 32 个提交及最终调用链，核对前端入口、HTTP handler、服务层、持久化和测试 | 不能仅凭提交标题判定功能完成 |
| 仓库现有 Node 测试 | 8 个前端/Node 脚本最终均通过；版本一致性脚本也通过 | 通过不能覆盖下文未测试到的场景 |
| 原始 JavaScript 函数定向执行 | 在 Node VM 中执行固定 HEAD 实际函数，仅模拟 DOM、时间、网络等外围依赖；复现会话、备份、事务保护、空 LOB、历史、比对、资源归属和上传附件问题 | 不是完整真实浏览器 E2E |
| 后端规则夹具 | SQLite 数据夹具加窄范围决策镜像验证导出错行、LOB 歧义；Python 路径规则镜像使用真实 POSIX 文件名字节验证 SFTP 路径偏移 | 不是 Go 生产程序、Oracle 或 SFTP 服务端实测 |
| Shell 定向实验 | 实际执行提交 hook 与打包脚本；打包实验使用隔离临时目录和受控 git 输出 | 不代表当前最近 5 条提交已经满足打包失败条件 |
| 未执行的环境 | 当前没有 Go 工具链、真实 Oracle/MySQL、SFTP 联调服务和 Windows 执行环境；未运行真实浏览器 E2E | 没有声称 go test、真库事务/LOB、Windows 进程树清理通过 |

源码链接全部固定到同一个 HEAD，避免后续提交造成行号漂移。报告中的建议结构体、状态机和接口名若标为“建议”，是实现方案，不是声称当前已有这些代码。

## 3. 需要优先处理的问题索引

| 问题组 | 关键症状与影响 | 主要详审编号 |
| --- | --- | --- |
| 数据库会话保存与恢复 | 恢复时空 textarea 覆盖 SQL；关闭 A 时将 A 的 SQL/结果写入 B；备份 500 后同内容不再重试；撤回后旧计时器发送过期内容 | DBF-01、02、09 |
| 数据库事务生命周期 | 批量关闭吞掉回滚失败；失败 SELECT 清除提交未知保护；batch/COMMIT 交错覆盖已确定终态 | DBF-03、04；DB-B06 |
| SQL 导出数据正确性 | 派生表可把计算后的 ID 当真实主键；右键导出仍猜 WHERE；惰性 LOB 写为空；普通 Oracle 小写列被错误加引号 | DB-B01、07；DBF-07 |
| LOB 行、对象与事务身份 | 前六个非唯一特征加首行限制可能读另一条记录；owner 猜错 schema；旧 token 可绑定新源配置/新事务；token 暴露已脱敏配置 | DB-B02、03、04、05 |
| 比对编辑与保存 | 忽略空行后合并丢空白；连续合并用旧 diff 丢第一次修改；交换后撤回只还原文字，保存目标仍是另一文件；关闭无脏检查 | C01、02、03、05 / TUI-03 |
| 文件同步冲突保护 | 预览时不存在的目标，在执行前被他人创建仍可能覆盖；目录递归任务未携带预览时每个文件的版本 | C06 |
| 多 Tab 异步资源 | 后台 A 的结束回调清理活动 B 的流；旧页面迟到的启动响应覆盖重开页面的新资源；disposed scope 注册后不实际关闭流 | TUI-01、02 |
| SFTP 文件身份与写入 | path_id 前后端未闭环，父目录用显示名构造身份；父路径解析错误被吞；GBK replace 上传成功却改变原始文件名字节 | SFTP-01、02、03、04 |
| SSH 输入 | GBK 编码失败及真实 I/O 错误被当成功，命令后半段可能静默丢失 | SSH-01 |
| 升级恢复 | 进程被杀后残留锁阻止进入 journal 恢复；手工恢复成功却未结束 recovery_failed 状态 | UPG-01、02 |
| WSDL 凭据与依赖 | 跨源 import 发送原站认证头；同名附件上传前被覆盖；wsdl:import 交给 XSD 解码器；上传依赖未交给官方生成器 | WSDL-01、02、03、04 |
| 验收与构建 | 新 E2E 不在统一入口；空 SQL 可通过断言；崩溃测试手动删锁；版本检查未检查打包脚本 | QA-01、02；BUILD-01 |

这个索引按修复主题聚合，不是独立 bug 数量。后面的详审还包含空 BLOB 循环、历史来源错误、51 页签备份失败、导出缺行却成功、目录扫描开销、路由与移动布局、恢复空文件等 P2 项。

## 4. 功能究竟实现了多少

“已接通”表示在源码调用链上成立，或已有对应的有限验证；不自动等同于跨平台、真库和所有边界都验收通过。

| 本轮希望实现的能力 | 当前判断 | 代码层面的主要依据或缺口 |
| --- | --- | --- |
| SQL 输入防抖、空闲去重、本地恢复、远端备份 | **部分完成，含严重回归** | 防抖/去重真实接入，但序列化会改状态，远端成功确认不正确，恢复可能丢稿；DBF-01/02/09 |
| SQL 页签批量管理及事务安全关闭 | **部分完成** | 多选/关闭入口存在，批量关闭没有复用单页签事务保护；DBF-03 |
| 事务终态与 outcome_unknown 保护 | **部分完成** | 重复 control 主路径有终态保护，但新事务、batch 和前端解锁未统一；DBF-04、DB-B06 |
| 查询历史接通与状态修复 | **未完整接通** | getHistory 导出被覆盖，运行中记录变成功，完成事件取错当前源；DBF-06 |
| 页签数量放开与恢复 | **前后端契约冲突** | 前端不限制，后端备份最多 50；对象页签身份字段未完整持久化；DBF-08 |
| 导出携带参数、会话和页码 | **主路径已接通** | handler 传递到查询层，可结合原事务执行；DB 后端功能矩阵 |
| 严格 UPDATE 导出、完整复合主键 | **部分完成** | 直接计算列和不完整 PK 有拦截，但派生表和旧右键路径仍绕过；DB-B01、DBF-07 |
| 无 PK 但有可信 ROWID 的 UPDATE 导出 | **接口未接通** | plan 有结构，但 handler 提前拒绝无 PK，并固定传 hasTrustedROWID=false；DB 后端补项 |
| Oracle 元数据大小写隔离 | **未完整实现** | 模糊字典查询、缓存 key 和强制大写仍混合不同对象；DB-B07 |
| 无主键 LOB 查看与 owner 发现 | **有入口，正确性不成立于一般场景** | 特征猜测不是唯一身份，owner 排序不是同义词解析；DB-B02/03 |
| LOB 源配置与事务绑定 | **部分实现** | 存在未升级签发入口，session ID 可复用；DB-B04/05 |
| 超大 LOB 快速预览 | **部分实现** | 前端限制预览字节，但仍走完整 LOB 下载能力和服务端总长度上限；空 BLOB 会循环；DBF-05、DB 后端矩阵 |
| 分页辅助列剔除、JSON 重复列名拒绝 | **主路径已实现** | 后端使用显式投影计划/重复名称检查；DB 后端功能矩阵 |
| 索引/约束信息页、DDL/只读联动 | **主路径可见** | 新入口和数据读取存在；对象大小写/真库兼容性还需矩阵验收 |
| 多 Tab DOM 保活与基础路由调度 | **主体已实现** | TabManager 保留 pane，renderer 有迟到清理框架；业务页面未全部迁移；TUI 功能矩阵 |
| Tab 资源清理、关闭和 bfcache 保护 | **部分完成** | 无 owner 的异步清理、迟到注册、比较页漏守卫、旧 pagehide 绕过；TUI-01/02/03/07 |
| 日志助手子路由保活/分享/刷新 | **未完整接通** | query 状态被 hashFor 丢弃；TUI-04 |
| 便笺草稿保护 | **外层完成，内部仍缺** | 外层关闭可检查，内部切换仍重建并丢编辑 DOM；TUI-05，既有缺口 |
| 文本比对、单块覆盖与 Undo | **正常入口存在，数据完整性有缺陷** | 合并基于显示行，diff 过期可继续使用，Undo 未绑定文件身份；C01–04 |
| 比对保存后视角、搜索/方向键、Shift 多选 | **部分完成** | 保存重置视角、搜索索引和定位侧有缺口；选择模型/checkbox 不同步；C07/09 |
| 文件同步冲突检查与部分结果 | **部分完成** | 缺失目标/递归目录的基准不完整，准备阶段取消漏结果；C06/08 |
| SFTP GBK 显示与原始路径身份 | **底层能力存在，产品链路未闭环** | UI/API 不使用身份，嵌套身份构造错误；SFTP-01/02 |
| 中文路径创建、流式覆盖和权限错误提示 | **部分完成** | 父路径/replace 身份不稳定，权限错误被转成不存在；SFTP-03/04/06 |
| SSH 非 UTF-8 输入 | **不可靠** | 错误吞掉、gb18030 映射到 GBK；SSH-01 |
| 升级 journal 与崩溃恢复 | **正常错误恢复存在，真实崩溃链未完成** | 残留锁挡入口、恢复命令不结束失败 journal、空文件存在性混淆；UPG-01/02/03 |
| WSDL/XSD URI 图、同名依赖解析 | **本地 builtin 部分完成** | 本地目录用例已接入；上传路径丢失、canonical 查找和 namespace 上下文仍不足；WSDL-02/07 |
| 外部 WSDL 导入 | **未正确实现** | Definitions 被按 Schema 解码，外部 operation 未合并；WSDL-03 |
| 上传 WSDL 项目经官方工具生成 | **依赖链未接通** | 未持久化和物化附件内容；WSDL-04 |
| 官方生成器隔离、禁止覆盖、并发上限 | **主路径已实现** | 临时工作目录、产物校验、EEXIST/O_EXCL、同目录互斥和最多 4 工具并发已接通 |
| 官方工具进程树取消 | **源码已接入，跨平台实测待补** | Windows Job Object/进程组逻辑存在；当前测试不能充分证明孙进程都退出 |
| WAS 发布成功后的备份清理 warning | **本轮改动基本完整** | Build、Extract、PackageExtracted 三入口处理一致，未发现这部分新的确定缺陷 |
| 统一测试入口、真实验收矩阵 | **部分完成且存在假阳性** | 新模块未注册，部分断言检验过弱；QA-01/02 |
| 版本唯一来源与模型版本 hook | **部分完成** | build 与 package 来源不一致；hook 可接受无版本 Model；BUILD-01/02 |

对应的精确源码证据见各模块详审。上表明确承认已完成的工作，也区分“能力存在”“入口接通”“失败条件正确”和“真实环境验收”。

## 5. 可直接交给实现 AI 的修复任务

### 5.1 通用实施约束

以下是建议给实现 AI 的任务约束：

> 以本报告固定 HEAD 或包含它的新分支为起点。先核对当前代码是否已经修复对应问题，再修改。每批围绕一个共享状态/身份契约完成，保留兼容迁移。测试必须从真实入口验证最终内容、目标身份和失败结果，不能仅统计调用次数、匹配字符串或删除故障现场。每批交付：根因、修改文件、实现差异、复现修复前失败/修复后成功的测试结果、未具备环境的明确说明。不得将跳过、mock 通过或缺少工具链写成真环境通过。

无需一次性重写整个 database.js、compare.js 或 websphere.js；先抽出负责状态和身份的少量纯逻辑，修调用边界，再逐步整理 UI。

### 5.2 批次 A：数据库会话模型与可靠备份

**关联：DBF-01/02/08/09。主要文件：web/pages/database.js、internal/httpserver/handlers_database.go。**

1. 把 serializeDBSessions 改成纯函数，只读 session model；禁止调用 saveEditorSQL、访问 editor DOM 或更改 activeSessionId。
2. 给当前编辑器绑定明确的 sessionId。切换/关闭前以“预期旧 sessionId”保存；input/change 只更新该模型；恢复先校验数据并创建模型，再绑定 DOM，最后计算指纹。
3. 持久化记录区分 current、inFlight、acknowledged revision/fingerprint。请求成功且服务器确认后才更新 acknowledged；网络失败、500、超时保留 dirty。
4. 防抖调度前先取消旧任务；定时器读取当前最新快照或核对 revision，修复 A→B→A 发送旧 B。
5. 同一来源的备份写入串行，成功后如仍有更新则继续；多窗口/多客户端采用服务器 revision/CAS 或明确的冲突规则，不能让晚到请求覆盖新状态。
6. localStorage 写失败时保留未落盘状态；服务器使用临时文件+原子替换。统一页签数量/字节限制，明确超过限制的可见错误；持久化对象页签类型、schema、name。

建议状态示意：

```ts
type SessionSnapshot = {
  schemaVersion: number;
  revision: number;
  activeSessionId: string | null;
  sessions: PersistedSession[];
};
type BackupState = {
  currentRevision: number;
  acknowledgedRevision: number;
  inFlightRevision: number | null;
};
```

这只是建议契约，具体字段命名可随项目风格调整。关键是“编辑状态”和“已经成功存储的状态”不能共用一个变量。

**验收：** 恢复多个不同 SQL；关闭 A 后 B 的 SQL、rows、dirtyCells、dataSource 不变；断网/500 后自动重试；A→B→A 最终备份 A；请求乱序不回退；51 页签行为明确；对象页签刷新后身份正确。

### 5.3 批次 B：事务终态与关闭会话协议

**关联：DBF-03/04、DB-B06，并为后续 LOB token 提供 TransactionID。**

1. 为每次真实事务生成不可复用 transactionId/generation，避免把浏览器 sessionId 当事务身份。
2. 后端统一 finishTransactionLocked：再次验证 entry 未终结、generation 相同；终态一次写入，不允许 committed/outcome_unknown 被迟到 batch 改成 rolled_back。
3. ExecuteSessionBatch 拿锁后再次检查 done；删除 map entry 时验证仍为原实例，不能清掉已经替换的新事务。
4. outcome_unknown 必须保留到明确的核查/确认流程结束。普通 SELECT，无论成功还是失败，都不应隐式解除。服务器也检查该屏障，不能仅依赖前端按钮。
5. 单个关闭、关闭其他、批量关闭共用 prepareSessionClose。运行中/提交中先明确等待或拒绝；取消和回滚要等待结果；失败必须保留会话和可继续处置的状态。
6. 批量操作逐项记录结果；只移除成功关闭的会话，并无条件同步标签 UI 和备份模型。

**验收：** 人工栅栏控制 batch 与 COMMIT 的交错；已提交/未知终态不能被覆盖；失败 SELECT 不解锁写入；回滚错误不能导致页签消失；旧事务操作不能影响新事务。

### 5.4 批次 C：统一安全 SQL 导出与标识符解析

**关联：DB-B01/07/08、DBF-07。**

1. 所有 SQL 导出入口，包括右键单行操作，共用后端的完整值校验、标识符处理和明确目标列映射基础层；删除前端猜 WHERE 和使用 LOB preview 构造 SQL 的旁路。UPDATE 与 INSERT 使用分别定义的计划策略。
2. 仅对 UPDATE 先实现可证明的受限语法：顶层 FROM 只能是一个有名的基本对象。正确识别括号、字符串和注释；暂时拒绝派生表、CTE、table function、无法证明来源的复杂查询，而不是继续扩展零散正则。
3. UPDATE 计划保存真实 source、owner、table、physical columns、完整可信唯一键和每列值是否完整。输出别名不等于物理列，计算值不等于原始主键。INSERT 不需要源主键；在目标表、列映射和完整值均明确的前提下，可以导出 JOIN 或计算查询的结果，不能把 UPDATE 的来源/定位限制直接套到 INSERT。
4. Oracle 标识符保留 quoted 语义；未加引号名字依数据库规则解析，最终用字典返回的精确名字输出。不能把用户写的小写 token 原样包双引号。
5. 惰性/截断 LOB 必须取到可信完整值，或明确拒绝相应导出；不能导出空串、预览或占位对象。
6. 对 query.Truncated 在发送成功附件前做完整性检查，区分“按用户选择只导当前页”与“当前页被预算截断”。后者默认失败，若产品允许部分导出则必须显式声明范围和不完整原因。
7. ROWID 路径只有来自同次查询的可信身份才可开放，不得允许用户自报 ROWID 绕过校验。

**验收核心 SQL：**

```sql
-- 表中原始数据：ID=1,BALANCE=100；ID=2,BALANCE=200。
SELECT * FROM (
  SELECT ID + 1 AS ID, BALANCE
  FROM T
  WHERE ID = 1
);
```

必须拒绝把该结果导出为更新 T.ID=2 的脚本；外层显式列版本同样要拒绝。另测 lowercase SQL、quoted 混合大小写对象、复合主键缺失、同名列、无唯一键、右键入口、lazy LOB 和预算截断。

### 5.5 批次 D：LOB 的可信身份和读取契约

**关联：DB-B02/03/04/05、DBF-05。依赖批次 B 的 transactionId。**

1. 在查询时生成可信 RowIdentity，绑定 source revision、resolved object、完整 PK 或真实 ROWID、transactionId。点击 LOB 时不再靠最多六个显示值重建行身份。
2. 如果暂时保留特征 fallback，应使用原事务、完整可用条件并查询至少两条；0 条和多条均拒绝。该措施只能过渡，不能声称解决了跨时间身份问题。
3. 对象解析遵守 Oracle explicit owner/current_schema/private synonym/public synonym 语义，处理循环；暂不支持的 DB link 或对象类型明确拒绝。禁止在 all_tables 中按 owner 排序猜一个。
4. 所有 token 签发路径统一；旧的空 fingerprint token 在安全迁移期内受控处理，之后拒绝。事务开始/结束和源配置修改应使不再匹配的 token 失效。
5. SourceFingerprint 使用不可逆摘要或不透明 revision；不能将整份 Source JSON 塞进可解码 token。
6. 前端用 previewState=idle/loading/loaded/error，byteLength=0 也是 loaded；原位更新预览，关闭取消请求，不再以空 hex/base64 判断“未加载”。
7. 如果目标是任意大字段的少量预览，应提供真正的范围/前缀读取接口与明确预算，不能继续调用先验证完整字段总长的下载流程来声称“秒开”。

**验收：** 重复日志、前六列相同第七列不同、NULL 特征、未提交行、同名跨 schema/同义词、修改源连接、旧事务 token、新事务复用页签、空 BLOB、超过全量下载上限的前缀预览。

### 5.6 批次 E：比对文档模型、Undo 与同步计划

**关联：C01–09、TUI-03。**

1. 完整原文 Document 是唯一可写数据源，展示层 alignedRows 仅用于渲染。“忽略空行”不得改变保存内容。DiffHunk 使用原始文档坐标和稳定插入锚点。
2. 每次输入、交换、选项改变递增 document/options revision；diff 结果携带两边 revision。旧结果丢弃；merge 必须在 revision 匹配时应用，不能只禁用 Compare 按钮。
3. Undo 命令绑定 documentId 和 source identity。交换是交换两个 slot 的文档引用；打开新文档形成历史边界。不可只还原字符串而让保存目标停留在交换后的路径。
4. 保存版本需要跟随真实文件状态，不可因 Undo 机械恢复过期版本。应用/撤回 patch 后仍保留一致的 baseline 与 dirty 判定。
5. 统一焦点所属的快捷键分发；textarea 内部处理后防止冒泡重复处理，隐藏 Tab 不拦截其他页输入。
6. 比较文档的关闭、刷新、替换源纳入统一脏检查；正常保活切换继续保留内容。
7. 文件同步创建不可变 SyncPlan，包含每个文件的 expected source/target version，以及 missing/existing/unknown 三态。递归目录展开也在预览时生成计划；执行前再次验证，不允许把“原本不存在”降级成“随便写”。
8. 所有同步项从 pending 初始化，准备/复制/取消阶段均有终态；取消保留成功、失败、未执行和取消结果。早取消、晚返回 jobId 要补发 stop。
9. 统一 ViewState 保存光标、选择、滚动、活动搜索命中；selection Set 与所有 checkbox.checked 每次同步。

**验收：** 忽略空行合并保留目标空白和末尾换行；diff 尚未返回时连续两次合并；swap→undo→save 检查实际两个文件；Ctrl+Z 一次一步且只影响当前编辑器；目录目标在预览后被创建/修改时拒绝；Shift 选择显示和执行范围一致；取消后所有项目均可解释。

### 5.7 批次 F：Tab 资源作用域与历史/路由状态

**关联：TUI-01/02/04/05/06/07、DBF-06。可与数据库后端修复并行。**

1. 每次 pane 挂载创建唯一 instanceId；不要复用 routeId 充当实例身份。
2. 所有异步回调捕获 owner、instanceId、resourceId。清理要提供 expected instance；注册同样检查 scope 是否仍存活，不能只防旧清理删除新记录。
3. await 启动请求后先判 scope。若后端任务已创建但原 scope 已关闭，立即补偿 stop；disposed scope 接到资源时实际 close/abort。
4. 把 websphere/files 的匿名全局监听、流、计时器及停止逻辑迁入 scope；集中处理 pagehide.persisted，消除旧监听器绕过。
5. 统一路由 parse/serialize 与 setRouteState；query 状态必须 round-trip。页面内部不再直接覆盖 hash 丢状态。
6. notes 草稿按 noteId 存模型，内部 tab 切换保留或触发确认；不能只给外层 TabManager 增加一个布尔量。
7. 桌面折叠 CSS 限制在桌面 media query，移动抽屉用独立状态；验证折叠桌面→缩窄→打开移动菜单。
8. 查询历史改成单一 HistoryStore，记录 runId/sourceId/sessionId/sqlExecuted；在查询开始捕获来源，完成时按 runId 更新。running 不得在打开面板时变 success；重启后的中断记录用 interrupted/unknown。统一清理/迁移入口并保持最终导出对象包含 API。

**验收：** A 后台完成时 B 的流仍在；关闭再重开 A，旧响应不能覆盖新资源；disposed 后注册资源立即关闭；保活/刷新/分享子路由一致；真实输入便笺内容后内部切换；两个数据源并行查询历史归属正确。

### 5.8 批次 G：SFTP 原始路径与 SSH 编码

**关联：SFTP-01–06、SSH-01。**

1. 使用 RemotePathRef={displayPath,pathId}；显示文本只用于展示。所有目录项包括 UTF-8 项都提供身份，UI 选择集合、下载/上传/删除/改名请求都带身份。
2. 在 ReadDir 取得真实父路径时生成完整 raw absolute path 的身份；不能将显示父目录与 raw basename 拼接。身份不可通过字符串 dirname/join 推导。
3. API 先解析 pathId 得到原始绝对路径，再做允许路径/权限校验；不要因为 token 不以 / 开头就在入口拒绝，也不能跳过授权。
4. 写操作固定 raw parent/raw final path。MkdirAll 按段解析并保留已经确认的父路径；歧义、权限、网络错误必须返回，不得当成不存在并另建 UTF-8 路径。
5. replace 上传开始即固定目标身份；partial、backup、commit、rollback 全程使用固定原始路径。目标重命名为备份后不能再按显示名解析“现在不存在的目标”。
6. partial 文件带 uploadId，回滚失败据实报告；无覆盖模式使用最终提交阶段的原子排他语义，不能仅上传前 Stat。
7. 同批任务可共享目录身份索引，避免逐文件全量扫描；写操作后失效缓存。先测 ReadDir 次数，再做真 SFTP 性能测试。
8. SSH writer 遵守 io.Writer n/err 语义，传播短写与编码错误。明确严格/替换策略并避免静默截断整条命令；gb18030 使用正确编码器。

**验收：** GBK 父目录+GBK 文件，UTF-8/GBK 同显示名并存，多层不存在子目录，replace 后原始 filename bytes 不变；permission denied 不变成 not found；同目录批量 N 个文件不产生 N 次整目录扫描；中文+emoji+命令结尾换行、分片 UTF-8、短写及连接中断均有正确结果。

### 5.9 批次 H：升级恢复的统一状态机

**关联：UPG-01–04。**

1. Run/Recover/RestoreSnapshot 共用锁协议，优先使用进程死亡自动释放的 OS advisory lock（Linux flock/Windows LockFileEx 或项目已选跨平台封装）。锁文件可残留，锁所有权必须由内核判断。
2. 禁止仅按文件年龄删除锁；仍存活的升级进程必须保持互斥。
3. 恢复成功后验证资产，再原子迁移 journal 到可继续启动的终态或归档；不能恢复完数据却保留 recovery_failed 挡入口，也不能先删失败证据再验证。
4. 明确记录文件 exists 与字节内容；空文件存在、文件缺失、manifest 无效是三种不同状态。
5. 自动恢复复用人工恢复的路径/白名单/backup/哈希检查，绑定 journal 与 snapshot 每个条目；不信任独立修改后的 snapshot.json。

**验收：** 正式子进程写 journal 后强杀，保留所有现场文件，直接用正式入口重启恢复；删除测试中的手工删锁步骤；并发活进程不能被接管；recovery_failed→官方恢复命令→正常启动；空文件/缺失文件/损坏 snapshot 逐个故障注入。

### 5.10 批次 I：WSDL 文档图与代码生成输入

**关联：WSDL-01–08。**

1. 每次请求按 origin 计算凭据策略，不能只在 redirect 时剥离。跨源直接 import 同样不继承敏感 Header；自定义密钥头也纳入策略。
2. 上传附件保留完整相对路径，建立明确的 memory bundle 根；遇到重复身份拒绝而非覆盖。后端按 baseURI+ref 得到的 canonicalURI 精确查找；basename fallback 只能在全局唯一时使用。
3. 建立有类型的文档图：WSDL Definitions 和 XSD Schema 分开解析，合并时按 QName 处理消息、portType、binding、service 与 schema。
4. 缓存文档字节按 URI，展开语义按 (URI,effectiveNamespace)；修复 chameleon include。
5. 持久化完整 ResolvedDocumentBundle 内容/URI/hash；builtin 和官方生成器共用同一份输入。官方工具在隔离目录物化全部依赖并安全改写引用，不能只写主 WSDL。
6. PathToFileURI 用 url.URL 构造，正确处理 #/?/%、空格、Windows 盘符。
7. context 从 HTTP 请求一直传到 graph resolver、fetcher、并发等待与外部工具。
8. 区分经授权的本地导入与 remote/memory 模式；后两者默认不得跨到服务器 file://。本地模式有明确根目录、软链接与文件类型校验。

**验收：** a/common.xsd 与 b/common.xsd 同时上传后仍各自存在；嵌套同名 root/child 引用选对；跨源认证头未发送；wsdl:import 外部 operation 可见；上传→保存→重开→官方工具可生成；共享 chameleon 两个 namespace；取消依赖请求及时结束；合法本地导入可用、远程诱导读服务器文件被拒绝。

### 5.11 批次 J：真实验收门禁与发布脚本

**关联：QA-01/02、BUILD-01/02。贯穿前述每批，最后统一验收。**

1. 把新增数据库 E2E 注册到统一入口；所需模块加载失败必须计失败并非零退出，未具备环境明确 SKIP。
2. 备份测试断言响应成功+服务端回读+刷新恢复内容；SQL 测试先断言非空，再验证目标表、唯一键和完整值。
3. 修复 DOM harness，至少断言没有“页面渲染失败”；草稿、焦点、滚动、导航使用真实浏览器覆盖。
4. 崩溃恢复不得手工修复现场；同步截断必须断言目标不变；XLSX 的 Close/Rename 故障要有真实注入点与文件产物断言。
5. 验收矩阵分别记录 unit/integration/browser/live 状态、命令、环境、原始日志；性能声明必须有真实采样，进程树取消必须验证后代 PID。
6. build/package 共用版本读取逻辑：显式参数优先，其次 VERSION，不读取提交说明猜版本；测试同时覆盖两脚本。
7. hook 明确模型版本格式及 Human 例外，并提供安装/CI 入口；格式校验不冒充真实性证明。

**推荐顺序：** A 与 B 优先；C 可独立推进；D 依赖 B 的事务身份；E 与 F 先对齐 Tab guard/scope 接口再并行；G、H、I 各自独立；J 在每批实施时补测试，最后运行全量验收。不要等所有功能改完才补验证。

## 6. 实施时的共同设计要点

反复出现的根因是“展示值被当成身份”和“中间状态被当成成功”：

| 当前易混淆的概念 | 建议明确区分 |
| --- | --- |
| DOM 上显示的 SQL / 会话中存储的 SQL | 编辑器绑定的 sessionId 与纯模型序列化 |
| 请求已经发出 / 备份已经成功 | current / inFlight / acknowledged revision |
| 浏览器页签 / 具体一次事务 | sessionId / transactionId |
| 查询显示列 / 数据库物理列 | output alias / resolved physical column |
| 可见字段值 / 一行数据的身份 | preview values / trusted RowIdentity |
| 比对过滤后的行 / 文件完整内容 | display rows / canonical document |
| 左右位置 / 实际文件身份 | view slot / documentId + source |
| 页面 route / 一次挂载实例 | routeId / instanceId |
| 中文显示路径 / SFTP 原始路径 | displayPath / raw path identity |
| 解析到了依赖名 / 生成器拥有依赖内容 | graph metadata / persisted document bundle |
| 回滚动作返回 / 系统已恢复可启动 | restored assets / verified journal terminal state |
| 测试脚本存在 / 功能真实验收 | registered case / semantic assertions / environment evidence |

这些是根据多个模块源码归纳出的设计建议，不是说必须引入大型新框架。修复应集中在上述边界，避免继续增加各入口互不一致的特殊判断。

## 7. 没有作为缺陷计入的事项

- config.yaml 的开发 token 不能直接推出正式发行版默认绕过 license：正常启动调用 BootstrapDistributionConfig 清除内部 token/endpoints。
- compare_allowed_roots 为空时放行是后续提交明确调整的产品策略，不能仅因偏好不同就报告成安全 bug；非空 roots 和管理员写权限仍需继续验证。
- 环境没有 Go 不是项目错误；本次首次测试缺少已存在的 webservice.js，是审核快照依赖未取齐，补齐后通过，不是仓库缺文件。
- 未被生产路径调用的 RollbackTransaction 未单列为用户可达故障；修统一终态时可一并清理。
- 本轮 WAS 备份清理 warning 的三处处理、生成器禁止覆盖修复等已有真实接入，不因其它模块有问题而否定。

上述判断的源码位置及测试证据见 QA 和对应模块详审。

## 8. 逐提交覆盖清单

以下清单由仓库提交元数据生成，按时间从新到旧排列。变更文件数为每个提交的文件数，同一文件在不同提交中重复计数；最后的 136 路径清单才是去重后的整体范围。

| UTC 时间 | 提交 | 变更文件数 | 提交标题 |
| --- | --- | ---: | --- |
| 2026-09-21 10:48:39 | [38df3aa6](https://github.com/Qioooba/kairo/commit/38df3aa604817955f470eaaaa0eb4c29124972c4) | 4 | fix(database): 优化会话备份频次与脏检查，消除空闲重复发包 |
| 2026-09-21 10:07:47 | [8d033426](https://github.com/Qioooba/kairo/commit/8d0334266dea1f3f6c23df814b209c4950b9910e) | 7 | fix(database): 修复无主键表 LOB 定位与 Schema 占位符污染，实现在线即时流式预览 |
| 2026-09-21 04:41:48 | [aa07c9a5](https://github.com/Qioooba/kairo/commit/aa07c9a510cda6c06265c30fefb8fca825a472bf) | 5 | feat(database,compare): 优化查询历史、页签右键操作与文件比对自适应滑动 |
| 2026-09-21 03:56:17 | [0ad4405a](https://github.com/Qioooba/kairo/commit/0ad4405a6228514a74028ee3347383a0301d750d) | 5 | fix(database): 修复对象详情页索引与约束不展示的问题 |
| 2026-09-21 03:21:39 | [695b8c2b](https://github.com/Qioooba/kairo/commit/695b8c2b1efcd84af1c0d001d88f33242ef43dec) | 8 | ﻿fix(database): 修复网格编辑与元数据查询中 schema 占位符污染导致非法字符错误的问题 |
| 2026-09-20 17:38:42 | [027f0aab](https://github.com/Qioooba/kairo/commit/027f0aab9962ad48fd4d516f5a59363afc8e28a8) | 2 | test(database): 增加真实浏览器与真实数据库端到端深度测试脚本 |
| 2026-09-20 17:15:20 | [50d32ddf](https://github.com/Qioooba/kairo/commit/50d32ddfa1fca96f1c5925344673db4ebeee3195) | 9 | feat(layout): 支持桌面端左侧菜单收起与全屏展开（方案 A）及工作台精修 |
| 2026-09-17 12:05:23 | [08cc737c](https://github.com/Qioooba/kairo/commit/08cc737ce37adc9d040171ea06cac1ba233715d2) | 4 | feat(compare): 优化比对横向滑动、光标选中、保存退回、视角保持与勾选对齐行覆盖 |
| 2026-09-17 02:44:58 | [5eca1d89](https://github.com/Qioooba/kairo/commit/5eca1d899133a2283099d433e29e0f5584c27745) | 5 | fix(sftp): resolve path identity and GBK parent directory (R5, R6) |
| 2026-09-17 02:29:27 | [fdd9627b](https://github.com/Qioooba/kairo/commit/fdd9627b257f211f6cbb12f1f62ff2357293d844) | 9 | fix(dbconsole,wscodegen): fix UPDATE projection validation, transaction terminal overwrite & codegen overwrite protection (R3, R4, R7) |
| 2026-09-17 02:07:17 | [3bbd007a](https://github.com/Qioooba/kairo/commit/3bbd007aa2c8c9169a65e2c37e8512400af7bb67) | 13 | fix(tabs): fix tab resource ownership, beforeunload, notes drafts & subroute sync (R1, R2, R8, R9) |
| 2026-09-16 15:47:34 | [67914880](https://github.com/Qioooba/kairo/commit/6791488014271e892d19b9aba2b1a9929a6958b1) | 19 | feat(tabs+gbk): 多Tab保活重构与GBK中文路径自适应 |
| 2026-09-16 05:02:17 | [f96fba8a](https://github.com/Qioooba/kairo/commit/f96fba8a76153d1e228cbc3f65b18804734afac9) | 19 | feat(compare): 白名单默认无校验+文本比对体验优化 |
| 2026-09-15 16:00:04 | [356312db](https://github.com/Qioooba/kairo/commit/356312dbd897fd69e6b042ae7843ddec8983a66f) | 2 | fix(sftp): 上传无权限报错带路径/用户/排查命令，去三层堆叠 |
| 2026-09-15 03:27:52 | [1f91be82](https://github.com/Qioooba/kairo/commit/1f91be82fedaeb802cae332b75f7a5ff0130519b) | 1 | docs(qa): finalize full acceptance test matrix mappings from T001 to T075 |
| 2026-09-15 02:51:49 | [24d1358a](https://github.com/Qioooba/kairo/commit/24d1358a9d7ea960da966b4c08417a7ce31c7bcf) | 2 | test(sec): verify RBAC isolation, CORS/WebSocket check, and error sanitization (SEC-01) |
| 2026-09-15 02:47:11 | [bfc0102b](https://github.com/Qioooba/kairo/commit/bfc0102b28ee652e5de10009f274f1c065e875f8) | 3 | perf(core): establish latency, allocations, cancellation, and task retention baselines (PERF-01) |
| 2026-09-15 02:42:24 | [f1530d2f](https://github.com/Qioooba/kairo/commit/f1530d2fd3b7a373bd2ef1b6b2c3cab1337dd6d9) | 11 | feat(compat): release compatibility matrix, environment diagnosis, and text codecs (COMPAT-01) |
| 2026-09-15 02:25:10 | [b5f64e94](https://github.com/Qioooba/kairo/commit/b5f64e94b4e5a63e37bc73e0dfa5e5019096e953) | 8 | feat(ui): operation-level navigation policy and canLeave guards (UI-02) |
| 2026-09-15 02:15:12 | [f3444a1c](https://github.com/Qioooba/kairo/commit/f3444a1c59f6a513a48c0c2bbf13408209f5e9b6) | 7 | fix(ops): real failure handling for compare sync and waspack publishing |
| 2026-09-14 20:34:52 | [40f113ea](https://github.com/Qioooba/kairo/commit/40f113ea8f18b7c8734c09bbeddcc246cb25eb0d) | 4 | fix(upgrade): add crash recovery journal and pre-store startup recovery |
| 2026-09-14 20:26:24 | [b6445d50](https://github.com/Qioooba/kairo/commit/b6445d503aa6e795ce41b12f56961a23aa27e9e2) | 7 | fix(wscodegen): isolate external tool workspace and unify safe atomic publish |
| 2026-09-14 20:18:01 | [c5e1df59](https://github.com/Qioooba/kairo/commit/c5e1df5941a3359680f6f79930d2c2f7cbd7623b) | 8 | fix(wsdl): manage XML schema dependencies by canonical URI graph |
| 2026-09-14 20:01:46 | [3134687a](https://github.com/Qioooba/kairo/commit/3134687a365b7e02e4b5a5a5b13356549cd384fc) | 4 | fix(web): add route scope and safe async unmount cleanup |
| 2026-09-14 19:50:58 | [43a35605](https://github.com/Qioooba/kairo/commit/43a35605a3bf89009ec65d9e65c6a027012ac683) | 9 | fix(dbconsole): track transaction terminal state and handle commit uncertainty |
| 2026-09-14 19:16:58 | [3251a246](https://github.com/Qioooba/kairo/commit/3251a246ce15cde5cd63a3849d8321e897d0ce60) | 4 | fix(dbconsole): pass full query context including parameters and session to export |
| 2026-09-14 19:02:47 | [274ab967](https://github.com/Qioooba/kairo/commit/274ab96789042beb5a7630ae43607ee062653d5a) | 8 | fix(dbconsole): unified LOB session mapping and trusted locator without version mixing |
| 2026-09-14 18:20:32 | [d0d3ab3d](https://github.com/Qioooba/kairo/commit/d0d3ab3db1d843c7ea5e06954f6a90d9e93a6baa) | 6 | fix(dbconsole): correct pagination helper column, metadata cache identity and order by detection |
| 2026-09-14 17:46:26 | [fa9cdca1](https://github.com/Qioooba/kairo/commit/fa9cdca1caef42dc69e5177cdbae64ee627f4f04) | 6 | fix(database): 修正 UPDATE 导出可信行定位与成品数据完整性 (DB-01, DB-05) |
| 2026-09-14 14:16:51 | [6ddaab25](https://github.com/Qioooba/kairo/commit/6ddaab255da648edd310f98bc1627c796a83a386) | 2 | test(qa): 建立跨平台统一测试入口与分级调度 (QA-01) |
| 2026-09-14 13:07:28 | [aeb09863](https://github.com/Qioooba/kairo/commit/aeb09863df88ccb5fff02ab67f152044a3d6baf6) | 4 | chore(ci): 增加提交模型版本约束规则与 commit-msg 校验钩子 |
| 2026-09-14 12:56:06 | [6b2068ca](https://github.com/Qioooba/kairo/commit/6b2068cada6d04f80190d26e855b11dd549a8739) | 36 | fix(workbench): 修复runQuery作用域与DDL守卫并优化比对体验与安全基线 |

## 9. 模块详审

以下保留稳定问题编号。每个模块给出具体根因、触发条件、源码证据、修复步骤和验收；实现 AI 可按第 5 节批次选取相关编号。报告内实验名称是审查记录标识，下方第 10 节摘录结果，重建用例所需的输入和验收条件已写在各条问题中。

### 9.A. 数据库前端、会话、备份与历史

审核基准：`38df3aa604817955f470eaaaa0eb4c29124972c4`；比较基线：`dc75701583fe78ec8b8bcfa706d77c68a7ba6011`。本报告只描述 HEAD 中仍存在的问题。行号均指 HEAD。源码来自 GitHub connector；下载后与基线逐文件生成完整 unified diff，未修改仓库代码。

#### 核心结论

本周数据库 UI 改动确实接通了整列选择、排序按钮、上下横向滚动条、首条 SQL 命名、批量关闭、结构化查询完成回调和自动 LOB 预览。但会话防丢改造引入直接覆盖其他页签 SQL/结果的回归；备份失败也被当成成功。当前不能认为数据库工作台已达到“防丢、事务安全、历史真实、LOB 任意大小快速预览”的完整验收状态。

最应优先交给实现 AI 修复：DBF-01（SQL/结果串写）、DBF-02（备份失败永久不重试）、DBF-03（批量关闭吞回滚失败）、DBF-04（未知提交被失败 SELECT 解锁）、DBF-05（空 BLOB 预览无限循环）。

#### 验证证据与边界

已在 Node v24.19.0 中运行仓库自带：

```text
node tests/database-review-regression.js
Database review regressions passed
Bounded export regression passed
DB-09 outcome_unknown regression passed

node tests/database-features-unit.js
database-features-unit: ok
grid target regression: ok
```

另编写审查用 `notes/db_frontend_repro.cjs`，直接读取完整 HEAD `database.js` 与 `database-features.js`，只在 IIFE 末尾添加测试访问口；用最小 DOM、可控计时器和可控 HTTP 响应执行原函数。不是字符串/正则断言，也未改仓库源码。实际运行结果保存在 `notes/db_frontend_repro.txt`：

```text
CONFIRMED restore destroys active SQL: ["","SELECT second_draft FROM T"]
CONFIRMED close overwrites surviving tab SQL and rows: SELECT A FROM A [["A-result"]]
CONFIRMED network backup failure is considered synced; repeated idle checks request count=1
CONFIRMED HTTP 500 backup failure is considered synced; repeated idle checks request count=1
CONFIRMED bulk close removes tab despite failed rollback; server transaction unresolved
CONFIRMED getHistory bridge missing after module load; opening history marks still-running query success
CONFIRMED edit A→B→A leaves B pending; next remote backup contains stale B
CONFIRMED EMPTY_BLOB preview loops automatically: 6 requests / 6 modal opens before synthetic stop
CONFIRMED failed SELECT clears outcomeUnknown; failure status=执行失败
CONFIRMED right-click row export guesses nonunique WHERE: UPDATE PEOPLE SET NAME = 'Alice' WHERE NAME = 'Alice';
CONFIRMED right-click row export converts lazy CLOB to empty string: INSERT INTO DOCS (ID, BODY) VALUES (1, '');
CONFIRMED background query from source-A recorded under source-B after tab/source switch
```

没有可用的真实 Oracle/MySQL 连接环境，所以不能宣称真实数据库端到端通过。以上前端状态机、序列化、导出文本和空流循环问题在不依赖真库的真实 JS 函数执行中已经确定；Oracle 实际提交是否落地、真实 LOB 吞吐等需按后续验收用例连接真库验证。

#### 功能落实矩阵

| 改动目标 | HEAD 实际实现 | 判定/余缺 | 代码 |
|---|---|---|---|
| 3 秒防抖、30 秒巡检、空闲零请求 | 编辑事件先 800ms debounce，再 3000ms；巡检 30000ms；内容相同跳过请求 | 请求频次路径已实现；实际输入后约 3.8 秒，备份可靠性未完成，见 DBF-01/02/09 | database.js 1733-1786、2637-2648 |
| 本地快速备份、刷新恢复 SQL | localStorage + 服务端备份/恢复入口 | 恢复 active SQL 会被空 textarea 覆盖；关闭页签会覆盖幸存页签，不能验收 | database.js 1712-1845、433-436 |
| 服务端相同内容不覆写磁盘 | 比较 activeId/tabSeq/sourceId/高度/折叠状态/sessions | 已实现串行相同 payload 的去重；未实现按版本防乱序和原子持久化 | handlers_database.go 1448-1463 |
| 首条 SQL 命名 | 移除注释、取首个分号前 22 字符 | 基本实现；SQL 字符串中的 `--`、`/*...*/`、分号未按词法区分，属命名边界 | database.js 180-200 |
| 页签右键关闭左/右/其他/全部 | 菜单与批量函数已接通 | 事务/忙状态保护被绕过，失败也删除；active 不变时不重绘；见 DBF-03 | database.js 231-331 |
| 双击空白新建页签 | host 和 parent 都监听 dblclick | 同一空白事件可冒泡触发两次 addSession，需合并监听或 stopPropagation | database.js 359-376 |
| 移除页签数量限制 | addSession、object tab 移除前端限制 | 服务端最多 50；第 51 页签以后远端备份返回 400，而前端不显示失败，见 DBF-08 | database.js 378-383；handlers_database.go 1428-1429 |
| 历史不再永久显示执行中 | 主页面开始/成功/失败回调已接通 | loadHistory 无条件将 running 改 success；getHistory bridge 被覆盖；多源结果写错来源，见 DBF-06 | database-features.js 544-648；database.js 730-744、5302 |
| 列名选整列 | 独立按钮和 th 选中，虚拟行也输出 col-selected | 已实现代码路径；浏览器实际宽表 UI 还需 E2E | database.js 4370-4410、4657-4663 |
| 排序角标与选列分离 | 独立排序按钮，stopPropagation，升序/降序/取消 | 已实现；仅排序已取回的当前结果集，非跨所有数据库结果的全量排序 | database.js 4380-4394 |
| 表头上方滚动条双向同步 | topScroll/scroll 两侧同步，跟随总列宽 | 已实现代码；桌面 resize/虚拟宽表需截图验证 | database.js 4412-4498 |
| schema 占位符过滤 | 主页面 defaultSchema/currentSchema 与相关请求过滤，designer 也拦截 | 主要入口已实现；过滤器分散，应共用，避免不同模块识别集合漂移 | database.js 111-127；database-object-designer.js 26-30 |
| 对象详情索引/约束展示 | inspect 请求加 refresh、读取并渲染索引/约束 | 前端路径已实现；真实性取决于数据库字典返回，见后端报告 | database.js 508-676 |
| LOB 点击自动在线预览 | /lob/token → /lob；CLOB 前 256KiB，BLOB 前 64KiB，取消 reader | 非空受限字段路径已接通；空 BLOB 循环；后端仍先限制整字段 64MiB；“任意超大毫秒级”未完成 | database.js 1096-1300；lob_stream.go 169-175 |
| LOB 可信定位 | token 优先，lazy 请求含当前 scalar keys/session | 不能判完全完成：后端动态 ROWID 只取若干特征并任取第一行、同义词 owner 等问题由后端报告覆盖 | database.js 1305-1388 |
| 提交结果未知后阻断重复写 | outcomeUnknown 显示与写 guard | 首次 SELECT 即清除，失败也清，见 DBF-04 | database.js 1618-1629、3903-3931 |
| 安全 UPDATE/成品数据完整性 | 服务端 /export 做新校验；前端右键仍独立拼 SQL | 未统一完成；右键猜 WHERE、LOB 预览当全文，见 DBF-07 | database.js 881-1039 |
| 防 MutationObserver 自触发循环 | features/designer safeInstall 中 disconnect → install → takeRecords → observe | 代码目标已实现，未发现此次改动的确定新问题 | database-features.js 1713-1755；database-object-designer.js 393-433 |

#### DBF-01 — P1：序列化读取 DOM 的副作用导致恢复/关闭页签覆盖 SQL 和查询结果

引入：[`38df3aa6`](https://github.com/Qioooba/kairo/commit/38df3aa604817955f470eaaaa0eb4c29124972c4)。确定，真实函数已复现。

关键代码：[restoreLocalDBSessions 1810-1813](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L1810-L1813)、[serializeDBSessions 1712-1714](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L1712-L1714)、[saveEditorSQL 167-177](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L167-L177)、[closeSession 433-436](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L433-L436)。

调用链 A：`renderSQL` 先创建空 textarea（2559），随后 `restoreDBSessions`（2651）→ `restoreLocalDBSessions` 装载保存 SQL → 新增的“计算已同步指纹”调用 `serializeDBSessions` → `saveEditorSQL` 以 `ta.value` 覆盖刚恢复的 active session.sql。非 active 草稿仍保留，active 草稿直接变空。随后又把错误快照登记为已同步。

调用链 B：关闭 A 时 `state.sessions.splice` → `state.activeId = B.id` → 新增 `backupDBSessions` → `serializeDBSessions` → `saveEditorSQL`，此时 DOM 与 state.rows 仍代表 A，sess() 已代表 B；B.sql、rows、columns、summary、lastSQL、dirtyCells、controller 等被 A 覆盖，然后 `restoreSessionChrome(B)` 显示已经串写的数据。即使关闭的是后台页签，函数也强行切 active，仍可能串写。

最小复现：建 A=`SELECT A FROM A`、B=`SELECT B FROM B`，两页签分别查询不同结果，激活 A 后点 A 的 ×；幸存 B 变为 A 的 SQL 和结果。刷新带两个保存草稿的会话，active 草稿变空。

实施方案：把 `serializeDBSessions` 改成只读取传入模型、没有 DOM 或 state 写入的纯函数。`saveEditorSQL(expectedSessionId)` 只在切换/关闭旧 active 前调用，校验 DOM 所属 sessionId；输入监听直接更新该编辑器绑定的 session。关闭流程按“保存旧 active → 处理事务 → 删除 → 设置 next → bind/render next → pure backup”执行。恢复过程先完整构造并验证所有 session、activeId，再绑定新 DOM，最后计算纯模型指纹；不要把从本地恢复的快照自动认为服务端已成功保存。

验收：刷新分别恢复 active/非 active SQL；关闭 active/非 active/object tab；不同 source 的 A/B；带 rows/dirtyCells/controller；每个 surviving session 均保持自身 SQL/结果/事务 ID；本地与服务端备份内容一致。

#### DBF-02 — P1：备份在 HTTP 成功之前就记成“已同步”，一次失败后永久停止重试

引入：`38df3aa6`。确定，网络异常与 HTTP 500 均已执行复现。

关键代码：[1775-1786](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L1775-L1786)。`lastRemoteBackupFingerprint = fingerprint` 在 fetch 之前；既不看 `resp.ok` 也不读响应；catch 为空。之后 30 秒巡检、beforeunload 和人工触发同内容都在 1749/1775 返回，不会重试。localStorage 1740 也在 setItem 成功前更新 local 指纹；存储错误恢复后同内容仍不重写。

最小复现：输入 SQL，首个 /backup 返回 500 或断网；恢复网络保持内容不动，等待多个巡检周期。服务端仍为旧备份，页面无提示且不再请求。已有“空闲零请求”E2E恰好会把这种失败当作成功。

实施方案：分离 `currentFingerprint`、`acknowledgedFingerprint`、`inFlightFingerprint`；成功解析 HTTP 2xx `{ok:true}` 后才更新 acknowledged；失败保留 dirty 并以有上限退避/online/visibilitychange 重试，显示可见保存状态。请求串行只保留最新待发 snapshot，避免新旧保存乱序。local 指纹只在 setItem 成功后更新。关闭页面可使用受限大小 `fetch keepalive`/sendBeacon，但服务端未确认的状态必须继续在本地保留为待同步，不能等同确认成功。

服务端需配套用户级写锁、递增 revision/乐观锁，拒绝旧 revision，并复用 atomic file helper 写临时文件+rename，避免当前 `os.WriteFile`（handlers_database.go 1463）并发覆盖或写入中断。后端直接写文件的基本风险早于本次提交，这次去重没有解决它。

验收：依次注入 network error、401/500、写盘失败、QuotaExceededError；恢复后相同内容自动重试成功；严格断言服务端 restore 返回最终 SQL，而非仅断言 request 次数；A 请求慢于 B 时最终仍保留 B。

#### DBF-03 — P1：批量关闭绕过单页签事务保护，回滚失败照样删除；部分路径不更新 UI

引入：[`aa07c9a5`](https://github.com/Qioooba/kairo/commit/aa07c9a510cda6c06265c30fefb8fca825a472bf)。确定，回滚拒绝后页签被删已执行复现。

关键代码：[closeSessionsList 275-315](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L275-L315)，与单页签 [417-428](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L417-L428) 对比。

批量函数没有 transactionBusy 守卫；有事务时 await ROLLBACK，但 catch 什么也不做；之后无条件从 state.sessions 删除。SQL 正在执行的页签只有 abort，没有等待结束，没有结束后检查新产生的 transactionPending。用户可能失去提交结果、回滚失败信息及后续控制入口，后端事务还占连接/锁直到清理。

同一实现还有明确 UI/数据迁移缺口：关闭其他页签并保留当前 active 时，302-303 调 `switchSession(activeId)`，388 的 active 相同提前返回，因此没有 renderTabs/backup。UI仍显示已删除页签。若 active 被删除，`switchSession` 的 `saveEditorSQL` 会在 sess() fallback 到 survivor 时再写入旧 DOM，引发类似 DBF-01 的串写。全部关闭从对象页签返回 fresh 查询时也未统一执行 hideObjectSession。

实施方案：不要保留第二套事务关闭逻辑。抽出 `prepareSessionClose(session)`，供单关/批关共用：忙状态等待或拒绝；运行中先取消并 await completion；重新获取事务状态；回滚失败保留原页签并明确汇总。批量仅删除成功关闭的 ID；关闭顺序和最后 active 选择单独计算，统一更新 source、bind、render、backup。禁止 await 后用旧 state 索引，按 session identity/runSeq 再验证。

验收：批量关闭包含 clean/dirty/running/committing/rollback-failed 五种页签；必须保留失败项、无遗失锁、菜单 DOM和state一致；active保留和被关闭两条路径；断网回滚失败、处理中切换数据源等场景。

#### DBF-04 — P1：提交结果未知后执行一条失败 SELECT 就解除写入保护

引入：[`43a35605`](https://github.com/Qioooba/kairo/commit/43a35605a3bf89009ec65d9e65c6a027012ac683)。确定，真实 runQuery 输入失败 SELECT 复现。后端事务新建行为已通过对应调用链交叉确认。

关键代码：[3903-3904](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L3903-L3904)、[3927-3932](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L3927-L3932)、[transactions.go 137-140](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/transactions.go#L137-L140)。

`runQuery`允许 SELECT 以核对未知提交，但在发送查询之前 `s.outcomeUnknown = false`。随后 SELECT 因断网或 ORA-00942 失败，catch 不恢复。下次 DML 前端不拦截；后端 transactionForContext(create=true)可创建新事务并删除相同 session 的 terminalRecords，未兜住未知提交状态。用户实际上没有核对任何数据，仍可重复业务写入。

实施方案：将未知提交状态从普通查询状态中分离，任何 SELECT开始/成功/失败都不得自动清除。新增显式“已核对实际结果，创建新事务”操作，记录人工确认并换新事务 generation/session ID。旧 unknown terminal 保持可查询且阻止同 generation 创建新的写事务。清除本地 dirtyCells 与承认原事务结果是两种动作，UI应说清楚。

验收：CommitUncertainError 后执行失败/取消/空结果/无关 SELECT，写保护保持；只有显式确认流程换新事务；同 session 下重新 DML API不能直接擦除旧 unknown 记录；不自动重放原 SQL。

#### DBF-05 — P2：空 BLOB 的自动预览无限重复加载并重建弹窗

引入：[`8d033426`](https://github.com/Qioooba/kairo/commit/8d0334266dea1f3f6c23df814b209c4950b9910e) 的自动调用；此前手动流程也不正确识别空内容，但没有此次自动循环。确定，已复现 6 连续请求/6次 modal，测试人为用 500 停止后续循环。

关键代码：[1180](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L1180)、[1275-1300](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L1275-L1300)、[lob_stream.go 166-167](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_stream.go#L166-L167)。

空 BLOB 是合法数据，后端合法返回零字节。前端生成 `val.hex=''` 和 `val.preview_base64=''` 后关闭并重开查看器；下一次 `Boolean(val.hex || val.preview_base64)`仍false，自动再次fetch，再关再开。持续消耗请求与数据库连接，并干扰用户关闭/切换操作。

实施方案：独立 `previewState: idle/loading/loaded/error` 或检查属性存在性，不能用内容 truthy 判断加载完成。优先在同一个modal内更新内容而非close/reopen。空BLOB显示“0字节”；一个modal仅允许一个in-flight请求；关闭使用AbortController取消token/read请求。客户端限长应对应服务端preview length参数，使前256KiB请求不先被整LOB64MiB限制拒绝。

验收：NULL、EMPTY_BLOB、1字节、64KiB整边界、64KiB+1、超64MiB BLOB；每次打开最多一次有效加载；重复点击/关窗/切tab不复开、不跨页写UI；EMPTY_CLOB也展示合法空文本。

#### DBF-06 — P2：查询历史“打通”与状态修复不完整，存在假成功、错来源、旧记录不迁移

引入：`aa07c9a5`。确定，桥接被覆盖、running变success、跨source结果归属三个场景均已执行复现。

1. [database.js 730-733](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L730-L733)先设置 `Kairo.database.getHistory`，但文件尾 [5302-5336](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L5302-L5336)重建 `Kairo.database = {...}`，未保留getHistory。features的552-573迁移桥永远不可用。新query通过pushHistory额外addHistory接入，不能证明旧历史已打通。
2. [features 575-579](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/workbench/database-features.js#L575-L579)每次loadHistory无条件把running改success，没有检查请求是否还活跃、执行结果或持久化中断。真实慢查询尚未完成时打开历史即可显示成功；崩溃中断记录也被伪造成功。
3. [features 619-647](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/workbench/database-features.js#L619-L647)只保留一个全局pendingRun；addHistory在594/603根据当前source下拉框loadHistory和填来源。A源查询进行中切换B源，A完成写入B历史；返回值回调没有sourceId/sessionId。成功事件 [database.js 4174、4205](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L4172-L4209)还用`s.sql || s.lastSQL`，期间修改编辑器或只执行选中SQL时可能记录非实际执行文本。
4. 工具栏清空只清persisted.history（2717-2720），结构化历史清空只清localStorage对应source（features1640）。两个入口没有统一删除语义。

实施方案：统一HistoryStore与事件数据结构`{runId, sourceId, sessionId, sqlExecuted, startedAt, endedAt, status, rows, error}`；查询开始捕获完整上下文，结果回调以runId更新原记录，不读当前DOM。`pendingRuns`改Map，多query可并行；running只能被同runId真实done/error/cancel改终态；启动后无法确认的running标interrupted/unknown。getHistory加到最终对象导出或使用Object.assign；历史删除/清空统一到store，迁移只做一次且保持来源未知，不把旧记录伪造为当前数据源。

验收：A/B源并发；只执行选中语句；执行中编辑文本；成功/失败/取消/异常重启；从基线升级带旧prefs历史；两历史入口清空/删除后刷新不复活；查询还活跃时状态仍running。

#### DBF-07 — P1：安全导出覆盖不全，右键单行 SQL 仍猜测 WHERE 并把 LOB 预览当全文

性质：核心不安全fallback在基线已有；是本周“可信行定位/成品数据完整性”改造遗留缺口，不应误称由最新提交首次引入。本周 [`50d32ddf`](https://github.com/Qioooba/kairo/commit/50d32ddfa1fca96f1c5925344673db4ebeee3195)继续修改该路径，把quoted identifier改cleanIdent，增加保留字/大小写/特殊字段兼容缺陷。确定，已真实执行拼SQL函数复现。

关键代码：[getTablePrimaryKeys 920-937](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L920-L937)、[buildWhereClause 940-974](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L940-L974)、[sqlValueLiteral 881-887](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L881-L887)、[exportRowAsUpdateFile 1009-1024](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L1009-L1024)。

右键复制/导出UPDATE没有走服务端BuildExportTargetPlan。字典失败直接返回[]，随后猜ID列、ROWID列名或所有可见列作为WHERE；重复行、计算PK、伪造ROWID别名都没有可靠验证。示例无PK PEOPLE两行NAME='Alice'，选择一行生成`UPDATE PEOPLE SET NAME='Alice' WHERE NAME='Alice';`，脚本可能同时改多行。导出文件还自动追加COMMIT。

LOB方面，lazy CLOB没有text时会被当成空串；有truncated preview时直接取该片段，BLOB同理。例`{kind:'clob',lazy:true}`生成`INSERT INTO DOCS (ID, BODY) VALUES (1, '');`。这与“成品完整性”目标相冲突，并非仅格式问题。

实施方案：所有UPDATE生成统一由一个经过审查的服务端export plan完成，右键入口传source/session/queryContext/selected row identity，不在浏览器猜主键。无可信定位拒绝UPDATE；大字段未完整取得时禁止完整导出或明确选择受控预览，不得静默空串/截断。标识符保留schema和引用语义，仅符合数据库普通未引用标识符规则的名字可以美化去引号；跨schema不能默认去前缀。不要默认追加COMMIT。

验收：两个相同无PK行、字典403/500、计算ID别名、隐藏PK、跨schema同名表、带空格/保留字/大小写列名、lazy/truncated LOB；错误场景不产出可执行危险UPDATE；全文导出内容哈希一致。

#### DBF-08 — P2：前端“无限页签”与备份服务器50页签上限冲突

引入：`aa07c9a5`移除前端TAB_LIMIT；`38df3aa6`失败标已同步进一步使问题静默化。确定代码契约冲突。

关键代码：[database.js 378-383](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L378-L383)、[handlers_database.go 1428-1429](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database.go#L1428-L1429)。前端允许第51个页签，备份map包含全部页签，服务端直接400；前端忽略状态。对象页签也被serialize成query字段，但type/objectName/schema/objectType不保存，恢复后不能复原对象页。

实施方案：协商统一容量策略。保留服务器保护时，界面明确最大50并限制创建；若目标确需大量页签，应服务端按session独立保存/版本化，限制总字节而非整包失败，并清楚呈现不可保存状态。对象页签制定单独schema或明确不列入SQL草稿恢复，勿转换成空query。对双击监听只保留单点或stopPropagation，避免一次操作建两页签。

验收：49/50/51页签，包含对象页；大SQL使payload达到body limit；响应失败可见且本地可恢复；一次双击只增加1个页签。

#### DBF-09 — P2：编辑 A→B→A 时未清除旧防抖任务，实际备份变回 B

引入：`38df3aa6`。确定，真实函数配可控计时器已复现。

关键代码：[1749-1765](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L1749-L1765)。备份A已成功，编辑B创建计时器捕获B；3秒内撤销回A，fingerprint等于lastRemote而提前return，没有取消旧B计时器。随后计时器发B。document.hidden又会跳过巡检，错误远端状态可能保持很久。

实施方案：每次新状态输入都先处理已有timer/pendingSnapshot；若最新状态等于acknowledged，取消旧timer并清空pendingSnapshot。定时器触发应读取最新模型快照，或检查捕获revision仍为最新，绝不盲发旧闭包data。与DBF-02一起实现串行、有ack的保存队列。

验收：已保存A→B→A；A→B→C→B；输入后迅速切tab/撤销/关页；只允许最后模型状态成为服务端有效备份；不依赖30秒巡检才能最终纠正。

#### 建议实施批次

1. **会话模型与保存队列**：DBF-01、02、09一起修；纯序列化、DOM/session归属、明确ack/version；先加上本报告已复现的状态机回归用例，再改实现，确保每个用例从失败转成功。
2. **事务与关闭**：DBF-03、04；单关批关统一prepare流程，unknown独立持久状态，后端拒绝旧generation重写。
3. **LOB与导出**：DBF-05、07；加载状态与内容分离，统一可信导出plan，preview endpoint预算与后端大小限制一致；结合后端ROWID/owner修复。
4. **历史与容量**：DBF-06、08；HistoryStore捕获run上下文，统一清理入口，页签容量前后端契约及双击事件。
5. **真正的浏览器/数据库验收**：已有 `test-database-backup-real.js`主要计算请求次数，不刷新恢复、不读备份返回值、不注入失败；`test-database-deep.js`主要验证列选择/排序/横滑与剪贴板格式，不验证提交恢复/重复行定位/LOB完整性。应补上前述负向用例，连接Oracle 11g和可用MySQL，保存截图与SQL/服务端状态断言；不能把脚本存在等同该真实环境已经测试通过。

### 9.B. 数据库后端、导出、事务与 LOB

范围：BASE dc75701583fe78ec8b8bcfa706d77c68a7ba6011 至 HEAD，重点 internal/dbconsole 全部变更以及 handlers_database.go / handlers_database_lob.go。已阅读比较 diff、关键完整实现及现有测试。未修改仓库代码。当前容器无 Go（`go version` 返回 command not found），无 Oracle/MySQL 实例，以下“确定”指源码控制流/SQL 构造可确定，不宣称已完成真实数据库或 Go 测试。

#### 核心结论与优先级

P1：可以生成错行 UPDATE、返回别的记录/数据库/事务的 LOB、覆写真实事务终态。P2：导出无效/不完整、信息泄漏、功能没有接通。不能依据提交中的“修复”“可信”“全链路”措辞直接判断完成。

| 编号 | 优先级 | 结论 | 相关提交 |
|---|---|---|---|
| DB-B01 | P1 | UPDATE 导出仍可被合法派生表绕过投影来源校验，执行成品可能更新错行 | fa9cdca1ca + fdd9627b25，后者只修直接表达式 |
| DB-B02 | P1 | 无主键 LOB 用前 6 个特征加 ROWNUM<=1 猜 ROWID，重复数据会返回另一行；定位还脱离事务连接 | 8d0334266d |
| DB-B03 | P1 | FindTableOwner 按字典排序猜 owner，不解析同义词，可把查询结果映射到另一 schema 的同名表 | 8d0334266d |
| DB-B04 | P1 | LOB token 的源/事务版本契约未完成：正常扫描签发漏指纹，事务仅绑定可重用的页签 ID | 274ab96789（新增约束遗漏调用链） |
| DB-B05 | P2 | token 的所谓 SourceFingerprint 是整份 Source JSON，可被只读用户解出应已脱敏的网络/配置数据 | 274ab96789 |
| DB-B06 | P1 | 并发 batch 与 COMMIT 仍能把 committed / outcome_unknown 覆写成 rolled_back | 43a35605a3 引入；fdd9627b25 未覆盖 batch |
| DB-B07 | P2 | Oracle 普通小写列名被按原始拼写生成带引号标识符；大小写不同对象的元数据又被混合 | fdd9627b25 / 0ad4405a62 / 8d0334266d |
| DB-B08 | P2 | 达结果字节上限时导出仍返回 200 成功附件；没有把整页缺行作为失败或显式部分导出 | 本轮导出完整性修复未覆盖既有问题 |

次要功能缺口：真实 ROWID 导出分支在 HTTP 入口永远不启用；第二页导出 ORDER BY 检测漏传数据库方言；OCI 降级状态按 source ID 永久黏住。这三项见矩阵和补充节，不宜与上列 P1 混为同一严重程度。

#### DB-B01：派生表绕过 UPDATE 来源校验

**证据**

- [export_plan.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/export_plan.go#L32)：32–59 的 ValidateSingleTableQuery 只用 JOIN/UNION/GROUP BY/HAVING 正则和 FROM 后逗号判断，不排除 FROM 子查询。
- 同文件 583–593：外层投影仅为 `*` / `alias.*` 时，直接用结果列名作为目标物理列名；完全没有证明外层 FROM 指向物理基表。
- [export.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/export.go#L88)：88–99 的 InferExportTable 正则会跳过 `FROM (` 并命中内层 `FROM T`。
- [handlers_database.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database.go#L815)：815–845 查 T 的真实主键后调用 BuildExportTargetPlan；这个字典校验不能证明查询中的 ID 是 T.ID。

**正常用户即可触发**

```sql
-- T 有两行：(ID=1,BALANCE=100), (ID=2,BALANCE=200)，主键 ID
SELECT * FROM (SELECT ID + 1 AS ID, BALANCE FROM T WHERE ID = 1)
```

结果列是 ID/BALANCE，结果值是 2/100。接口自动推断目标 T，接受完整主键，输出：

```sql
UPDATE "T" SET "BALANCE" = 100 WHERE "ID" = 2;
COMMIT;
```

源值来自第一行，脚本改第二行。外层显式 `SELECT ID, BALANCE FROM (...)` 也可通过；不能只补 `*` 分支。fdd 新增的直接 `ID+1 AS ID`、常量与重复 PK 投影测试均没有覆盖派生表。

**具体方案**

第一阶段明确能力边界：支持 `SELECT <直接物理列及别名>|* FROM [schema.]table [alias] [WHERE...] [ORDER BY...]`，FROM 目标必须是一个可证明的命名基表；遇到派生表、WITH、集合操作、连接、表函数、SELECT 投影子查询、无法解析的引用，一律返回 EXPORT_UNSAFE_SOURCE，并保留 CSV/JSON 等展示导出。用已有 SQL token 流跟踪括号/引号/注释深度，提取顶层 FROM，不能用第一处全文 FROM 正则。查询选定的源表与目标表的关系必须明确；默认安全导出要求一致。无需一开始就实现完整 SQL 解析器。

生成一个受限 `SingleTableExportPlan`，含已解析的 QualifiedName、直接列映射、投影位置、PK；通配符只能展开已验证基表的字典列。Writer 只接收该计划。

**验收**：上述派生表、外层显式列、带注释/换行的派生表、嵌套二层全部 422、无附件；普通单表 WHERE/ORDER BY 与直接列别名成功；结果别名与物理列不混用；错误脚本绝不落到下载响应。

#### DB-B02：无主键 LOB 定位猜到别的行；未使用原事务

**证据**

- [lob_locator.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_locator.go#L139)：139–201 跳过 NULL/空串/LOB/超长文本并按列名优先级排序；203–205 只取前 6 个条件；233 构造 `... AND ROWNUM <= 1`；239–240 通过 `m.withSQL(...db.QueryRowContext...)` 独立连接查询。
- [handlers_database_lob.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database_lob.go#L261)：261–271 唯一键找不到即采用上述 ROWID，并设置 UseRowID=true 签发。
- [database.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js#L1318)：1318–1366，fast lazy 单元格将当前行所有标量值发送到 `/lob/token`。所以不是需要伪造请求才触发。

**场景**：日志表没有主键，两行前六个被选择的特征值完全相同，第七个标量/正文 CLOB 不同。点第二行返回第一行 ROWID，进而展示第一行 CLOB。即使不超过六列，只要可见标量重复、差异仅在 LOB，问题仍存在。ROWID 是唯一标识，并不意味着“取得这个 ROWID 的过程”正确。

同页签刚 INSERT 一行尚未 COMMIT，SELECT 能看见该行；token handler 确认事务 active，但 ResolveRowIDByKeys 另开连接看不见它，导致无法预览。更坏的情况是别的已提交行恰好匹配这些特征，签到别行，再在事务内读取别行。

**修复**：默认单表浏览就由服务端投影真实 ROWID（可以仅作为隐藏元数据，不需要读取 LOB 内容），与结果行一并发给客户端；不要等用户点击时以普通值猜位置。已有 `compileLOBProjectedQuery` 可复用 ROWID 投影思路，把行身份提取与重 LOB 字典/内容获取解耦。对于无法安全改写的任意查询，仅接受可证明完整 PK/唯一键，缺失时给出明确提示。

若为兼容旧结果保留回查：必须在原 `transactionEntry` 锁及同一 queryer 下进行；字段和值需逐一按字典检查，禁止静默丢条件；至少读 2 个 ROWID，0 行报失效，2 行报歧义，仅恰好 1 行可用。此方案仍只能保证“点击时唯一”，无法恢复已变化结果的身份，所以应作为有限兼容模式。

**验收**：重复日志、差异只在 CLOB、超过 6 列、NULL 条件、未提交 INSERT/UPDATE、单连接池、记录被删后重插，均不得返回另一行；无唯一定位时必须显式失败。

#### DB-B03：真实 owner 发现不是 Oracle 对象解析

**证据**：[lob_locator.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_locator.go#L14) 14–45 遍历 all_tables/all_views，以当前用户优先、然后 owner 字母序选第一行；没有使用原查询当前 schema 或 all_synonyms。handler 239–243 在 Fields 失败/空时静默更换 Owner。

**场景**：APP 用户有私有同义词 LOG 指向 B.LOG，且能访问 A.LOG 与 B.LOG；APP 下无真实 LOG。`SELECT * FROM LOG` 返回 B.LOG，token 的 Fields(APP,LOG) 为空后，FindTableOwner 选 A.LOG。如果 A.LOG 同样有 ID=1，最终显示 A.LOG 的正文。提供了错误 owner 时也不能安全地改成任意同名对象。

**修复**：复用统一对象身份解析器，在原查询会话下按 Oracle 名称解析规则处理：明确 schema 的名字保持精确；未限定的名称基于 CURRENT_SCHEMA，再处理当前 schema 私有同义词与 PUBLIC 同义词，并检测循环/DB link/多跳。无法支持远程同义词时明确拒绝。记录 resolved owner + object + object identifier 至查询行元数据；不要用 `all_tables ORDER BY owner` 推断。

**验收**：APP 私有同义词→B.LOG（同时存在 A.LOG）、PUBLIC 同义词、同名 VIEW/TABLE、明确 owner、不存在对象，均解析到查询实际来源或明确失败。Oracle 真库验收应实际建立同义词，不能用任意缓存 Fields 覆盖这个过程。

#### DB-B04：LOB token 仍可能跨数据源配置和事务实例读取

**分支 A：正常查询签发漏配置信息。** [lob_projection.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_projection.go#L507) 507–519 仍调用旧 GenerateSignedLOBToken；[lob_token.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_token.go#L97) 97–99 传空指纹。handler 80–82 只在指纹非空时检查。查询 `fast:false` 取得现成 token，修改同 source ID 的 Host/Service 但保持数据库 Username 后，旧 token 可查询新数据库同名表/主键。

**分支 B：session ID 不能代表事务版本。** [lob_execution.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_execution.go#L50) 50–61 仅按 source+session 找当前 entry；LOBRef/token 没有事务 generation。事务 A 取得 LOB token→提交 A→同页签建立事务 B 并修改相同记录→点 A 的旧 token，会读取 B。注释所说“原查询事务结束必须失败”没有成立。

**三个独立修复要求**

1. 所有生产签发路径（scanner、lazy token endpoint、未来 preview）必须通过一个函数，强制带版本化 source 配置摘要；删除/限制可签空指纹的兼容入口。下载端拒绝缺失摘要的生产 token。
2. `transactionEntry` 每次 BeginTx 成功后生成不可重用的 TransactionID/epoch，token 含该字段；读取取得 entry.mu 后比较 token 与 entry 的源摘要、TransactionID、done 状态，任一变化报 409，不能按页签 ID 自动绑定新事务。
3. 摘要必须是单向摘要或不可枚举的配置版本号；当前 SourceFingerprint 明文问题见 DB-B05。不要只“把更多配置塞进 token”完成绑定。

**验收**：fast/nonfast 两条签发路径都测试；改源配置后旧 token 失效；A 结束后 B 重用同页签，A token 必须拒绝；A 仍 active 时读取本事务未提交内容成功；用错误用户 session 前缀仍拒绝。当前已有 fingerprint 测试只证明显式新签发函数和 handler 某一路，缺 scanner 全链路覆盖。

#### DB-B05：SourceFingerprint 明文泄漏已脱敏配置

**证据**：[manager.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/manager.go#L216) 216–223 的 SourceFingerprint 最终直接返回 `string(json.Marshal(source))`；[lob_token.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_token.go#L67) 67–94 将其放进 sfp 后 Base64 URL 编码并 HMAC 签名。签名仅防篡改，不隐藏数据。

与 [handlers_database.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database.go#L1311) 1311–1336 的 `databaseSourceView` 对普通用户清空 Host/Port/Username/Database/TLS 文件路径/SSH tunnel/AllowedUsers 等形成直接冲突。LOB 接口刻意允许被授权只读用户，所以他们可用浏览器解码 token 第一段拿到整份连接元数据。Source 不包含数据库密码，**不得表述成密码泄漏**。

**修复**：引入对外专用 `SourceRevisionDigest`，对稳定的安全敏感配置作 SHA-256/HMAC；内部连接池即便仍以序列化配置比较，也不要把内部 key 暴露给 token。完成 DB-B04 统一签发后，断言 token 解码 JSON 不含 host/tunnel/TLS 路径/AllowedUsers，摘要长度及格式固定。普通用户 token HTTP 回归需调用真实签发路径。

#### DB-B06：batch 与 COMMIT 交错仍覆写终态

**证据**：[transactions.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/transactions.go#L407) 407 获取 entry、411 等待 entry.mu、417 执行；419 调 rollbackEntryLocked 后，420 无条件记录 rolled_back。rollbackEntryLocked 的 214–215 虽检查 entry.done，调用方仍会覆写记录。

真实调用链 `/api/database/batch`→[handlers_database.go 653](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database.go#L653)→ExecuteSessionBatch；前端 workbench/database-features.js 930 也会请求它。不是未使用的辅助函数。

**可发生的时序**

1. 已有事务 entry E；COMMIT 请求持有 E.mu 并等待驱动返回。
2. batch 请求的 transactionForContext 返回 E，随后等待 E.mu。
3. COMMIT 完成，设置 E.done=true、E.outcome=committed（或网络异常 outcome_unknown），记录终态并从 map 删除 E。
4. batch 取得 E.mu，未检查 done，已结束的 `sql.Tx.ExecContext` 返回 sql.ErrTxDone。
5. rollbackEntryLocked 因 done 不再回滚，但 batch 下一行无条件将终态写成 rolled_back。

此时数据库可能已经成功提交，前端状态会告诉用户已回滚，诱发再次执行。fdd 的测试只覆盖 COMMIT/COMMIT 和 COMMIT/ROLLBACK control 交错，不覆盖 batch。

**修复**：所有事务操作获得 entry.mu 后统一检查 done、entry generation 及 registry ownership；陈旧请求按已确定终态拒绝，不能执行/回滚/另起事务。终态写入集中在一个 `finishTransactionLocked`（命名可调整），携带 expected entry/TransactionID，比较交换地写入并移除 map。删除 batch 的第二次无条件 recordTerminalState；回滚错误也不能宣称已确认回滚。GetTransactionStatus 在拿到 entry 锁后重新检查 done，避免刚提交后还返回 active。

**验收**：用已有 mutationDriver.onCommit 设置 barrier，确保 batch 已拿旧 entry 再释放 commit；分别模拟 commit 成功、EOF/timeout未知、rollback、超时清扫；终态不可逆地保持真实已知/未知结果，无第二次驱动写入。测试应有确定同步点，不仅依赖 sleep。

补充：RollbackTransaction 当前全库只有定义，没有生产调用点，因此不单独作为用户 Bug。它和 janitor 同样需要复用统一终态函数，以免未来接入再次引入问题。

#### DB-B07：标识符大小写处理不一致

##### A. 新 UPDATE 物理列映射把常见 Oracle 小写 SQL 写成无效脚本

[export_plan.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/export_plan.go#L530) 530–538 将 sourcePart 去引号后原样存 physName；652 用 EqualFold 匹配 PK，667 保存原拼写；[export.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/export.go#L407) 407–427 将其双引号引用。

目标 T 的真实列为 ID/BALANCE，查询 `SELECT id, balance AS amount FROM T`，结果列 ID/AMOUNT，实际导出是 `UPDATE "T" SET "balance"=100 WHERE "id"=1`。Oracle 未引用的 id 原本应解析为 ID，增加小写双引号后已变成另一个标识符，导致 ORA-00904；若恰好存在小写引用列，也可能定位/更新错列。

**修复**：解析后的标识符保留 `Value` 与 `Quoted`，Oracle 未引用 token 规范为大写，已引用 token 保持原样；最终物理名称以字典对象身份确认。不能对所有名字直接 ToUpper/EqualFold，因为 `"Id"` 与 `"ID"` 可以是不同列。

##### B. 元数据再次把大小写不同对象合并；LOB 下载还强制大写

[inspect.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/inspect.go#L62) 62、151 的 Indexes/Constraints cache key 都 ToUpper；74–82、164–178 查询条件额外加入 UPPER/LOWER 相等。MySQL case-sensitive 环境下 Orders/orders 是两个表，先读任一表再读另一表会串缓存；即使 refresh 清掉缓存，LOWER 条件也把两个表的索引/约束一起拿出。Oracle quoted Foo/FOO 同理。Fields cache 虽已改保持原大小写（metadata.go333），Oracle Fields 查询仍 ToUpper(schema/object)（350）。

[handlers_database_lob.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database_lob.go#L84) 84–87 连已签名的 owner/table/column 也强制大写；因此非fast签出的 `"Foo"."Text"` token 到下载时仍失真。

**修复**：统一 `ResolvedObjectIdentity`，保留字典返回的精确名称；cache key 包含 source revision、精确 schema/object。别用大小写模糊查询混合不同对象；若需要用户输入容错，先精确匹配，再单独进行唯一候选解析，多候选拒绝。前端 token 不改写服务端已解析身份。

**验收**：Oracle 未引用小写 SQL、双引号大小写列/表、两者同时存在；MySQL Linux lower_case_table_names=0 的 Orders/orders 不同 PK/索引，分别刷新和缓存读，都不混淆。

#### DB-B08：结果字节预算导致缺行，却仍按完整导出成功

[query.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/query.go#L544) 548–550 达 MaxResultBytes 时仅设 Truncated=true 后 break，最后正常返回 summary,nil；[handlers_database.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database.go#L788) 788–858、892–910 没有在 summary.Truncated 时拒绝，仍 200/Content-Length/附件，只有审计日志记录 truncated。

场景：请求导出一页 1000 行，每行文本较大达到 source.MaxResultBytes，返回仅前一部分行；由于本次已把 CSV 改成缓冲后发送，本来有机会发现并拒绝，但仍没有完整性门禁。严格 SQL 字面量检查只能发现已经读到的截断单元格，发现不了整行被提前丢弃。

**修复**：为“当前页导出”明确页面结果预算；预算超限统一 `EXPORT_LIMIT_EXCEEDED`，在任何响应内容发送之前失败。若产品需要“部分导出”，必须用户显式选择，文件/响应元数据标注 requested_rows、actual_rows、reason，不当作正常完整导出。CSV/JSON/XLSX/INSERT/UPDATE 使用同一完整性检查。普通表格查询仍可保留 Truncated 提示。

**验收**：将 MaxResultBytes 设为低值，第三行越界；五种导出都不能返回正常成功附件，审计为 fail/partial 明确值；page_size 上限与 HasNext 导致仅当前页属于预期，不要误当截断。

#### 功能落实矩阵（仅按代码能证明的部分）

| 本轮目标 | 状态 | 判断依据/边界 |
|---|---|---|
| 导出带绑定参数、页码、Fast、session | 已接通主要路径 | handler 770–788/892 将上下文传入统一 query；不能据此保证旧页面结果与后来再查询的值完全一致 |
| UPDATE 必须完整复合主键、拒绝 NULL、拒绝直接伪主键表达式 | 部分完成 | 主键完整和直接投影检查存在；派生表绕过 DB-B01，Oracle case DB-B07 |
| 真实 ROWID 支持无主键 UPDATE 导出 | 未接通 HTTP 路径 | handler764先拒绝无PK，840传 hasTrustedROWID=false；计划结构中的 UseRowID 不能说明产品已支持 |
| INSERT/UPDATE 拒绝未加载/截断/二进制占位内容 | 已实现这类输入校验 | exportSQLLiteralStrict / ValidateRowValues；整页丢行还缺 DB-B08 |
| JSON 重复列名防丢数据 | 已实现 | WriteJSON 先检测重复名并返回错误 |
| CSV 失败不留下半份网络附件 | 主要路径实现 | 已改先写 csvBuf 后发响应；预算提前停止被视为成功的问题尚存 |
| Oracle 分页辅助列误删修复 | 已实现主要逻辑 | PaginationPlan 明确 HasHelperColumn/HelperColumnName，只删当前生成的确切辅助列，首屏/MySQL不删 |
| 顶层 ORDER BY 识别 | 函数已实现，调用未全统一 | query summary 用 QueryHasTopLevelOrderBy(kind)；export第二页门禁仍QueryHasOrderBy无kind，MySQL #注释可被误判 |
| 元数据 cache 区分 quoted case | 部分完成后又出现不一致 | Fields/LOB projection key保留case；Indexes/Constraints仍ToUpper，查询又模糊合并 |
| 主键/唯一键/ROWID LOB 定位 | 不可验收 | DB-B02/03；唯一约束探测存在但无唯一表被退化成猜行 |
| LOB 绑定当前用户 session、事务内预览 | 主要路径已接通，版本约束未完成 | raw session scope映射存在；DB-B02同连接与DB-B04事务代次缺失 |
| LOB token 配置变化即失效 | 部分路径实现 | lazy签发有指纹，scanner无；指纹还是明文 DB-B05 |
| LOB 流中途失败不能误报完整下载 | 已有控制 | handler字节已发时panic(http.ErrAbortHandler)，内容长度/上下文限制存在 |
| 任意超大 LOB 首屏预览 | 未实现独立预览契约 | 前端仍调用完整流下载；go-ora lob_stream.go169–175先拒绝估计超过64MB，CLOB按字符*4估计；只能说有预算范围内预览 |
| 事务 committed/rolled_back/unknown/expired 状态查询与TTL | 主要结构已完成 | 终态记录、TTL、重复control处理存在；batch竞态 DB-B06；状态getter拿锁后没有done复核 |
| 提交未知防止盲目重试 | 部分完成 | Control重复COMMIT会拒绝未知；transactionForContext(create=true)无unknown阻止条件，新事务会删除旧终态；前端审查发现失败SELECT可清掉unknown保护，应合并验收 |
| 新建/草稿连通性测试使用自定义密码且不污染池 | 已有独立连接路径 | TestWithCredentials/buildSQLDB；正常连接和Oracle自动降级须真库/真客户端验证 |
| OCI 自动检测/降级/恢复 | 有检测和降级；恢复不完整 | dpiFailedSources仅Store/Load，Invalidate不Delete，换好Client后同source ID仍降级直至进程重启；需按配置版本+过期/显式重试重置 |
| DDL能力与未提交事务守卫 | 主要逻辑已完成 | DDLAllowed仍AllowDDL&&!ReadOnly，query/script检查当前未提交事务；真实Oracle隐式提交场景须真库测试 |

#### 次要但明确的补项

1. **ROWID UPDATE**：若产品承诺支持，应让查询创建服务端证明的 ROWID 元数据，然后 handler 使用同一可信计划决定定位方式。不能把当前 `false` 改成“只要有列名ROWID就true”；派生列/别名会再次误定位。若短期不支持，则UI明确只支持真实PK。
2. **分页方言**：handlers_database.go729 将 `QueryHasOrderBy(req.SQL)` 改为 `QueryHasTopLevelOrderBy(source.Kind, req.SQL)`。MySQL `SELECT ID FROM T # ORDER BY ID\n` 实际没有排序，现无方言检测会看到注释中的 ORDER BY；补HTTP级拒绝测试。含 ORDER BY 也不自动保证唯一排序和跨请求快照稳定，页面提示应保留边界。
3. **OCI黏住降级**：m.dpiFailedSources 以 source ID 而非配置指纹作key，未见任何清理。给失败条目记录 revision、错误类别、失败时间；Invalidate/修改Client目录清理对应状态，明确重试按钮可以重探测。不要每条SQL都重复危险探测。
4. **LOB chunk一致性需真库证明**：withLOBQuery 先尝试只读事务，但失败后允许普通事务，甚至两次Begin失败后直接用conn；不能仅因“同连接”就宣称多条DBMS_LOB.SUBSTR查询一定属于同一快照。应在Oracle11g go-ora/OCI分别用并发写入中断分块验证；需要显式固定快照或一个服务端游标/定位器保证整次读取版本一致。此项列验证风险，未在本环境完成驱动语义验证，不计确定线上Bug。

#### 给实现 AI 的实施顺序

1. 先修 DB-B01（限制安全导出语法范围）、DB-B02/03（结果行身份与对象身份）与 DB-B06（终态唯一写入点），避免错误数据和误报事务。
2. 再统一 SourceRevision + TransactionID 的 token 签发/读取契约，同时解决 DB-B04/05；与前端审查发现的 unknown被查询清空问题一起验收。
3. 统一 Identifier/ResolvedObjectIdentity 类型解决DB-B07，然后补所有导出格式的完整性门禁DB-B08。
4. 最后补ROWID产品路径、方言检测、OCI恢复策略和性能验证。

建议新增回归名：TestExportRejectsDerivedPhysicalKey；TestExportOracleUnquotedLowercaseUsesDictionaryCase；TestLazyLOBRejectsAmbiguousRow；TestLazyLOBUsesOriginTransaction；TestLobSynonymResolvesExactOwner；TestScannerTokenRequiresSourceRevision；TestOldLobTokenRejectedInNewTransactionSameTab；TestBatchAfterConcurrentCommitPreservesTerminalOutcome；TestExportByteCapDoesNotReturnSuccessAttachment；TestMetadataCaseDistinctObjectsDoNotShareCache。它们目前是建议补测，未在本次环境运行。

#### 本次实际执行的有限验证

执行了 `dbtemp/repro_backend_sql_mirror.py`，退出码 0。脚本使用内存 SQLite 数据夹具以及对 Go 源码中少量判定规则的窄范围镜像，**不等于 Go 程序运行或 Oracle 集成测试**。

- DB-B01：上述外层 `SELECT *` 派生查询通过镜像的 ValidateSingleTableQuery 规则，InferExportTable 返回 T；SQLite实际查询值为(2,100)。按当前 writer 构造的 UPDATE 将第二行 BALANCE 从200改为100，第一行保持100，证明错行后果。
- DB-B02：点击的第二行拥有第七个唯一可区分标量 DISCRIMINATOR=second，排序截断到C1…C6后实际有两条匹配记录；使用与ROWNUM<=1等价的SQLite LIMIT1返回rowid=1，读到“body of row 1”。这个验证不假装Oracle总是按相同物理顺序取行；它证明算法在多行匹配时没有办法保证返回被点击的那一行。

事务竞态、Oracle标识符语义、同义词/驱动/真实LOB流的验收仍需按上节用Go与真实数据库执行。

### 9.C. 文件比对、编辑、保存与同步

审核 HEAD：`38df3aa604817955f470eaaaa0eb4c29124972c4`。比较基线：`dc75701583fe78ec8b8bcfa706d77c68a7ba6011`。

覆盖 `web/pages/compare.js`、`compare-workbench.css`、`web/style.css` 相关差异、`internal/comparefs/local.go`、`handlers_compare.go`、`handlers_compare_workbench.go`、`compare_jobs.go` 及本周比较测试。阅读完整 HEAD 对应文件，并沿调用链检查 `handlers_diff.go`、`internal/diff/diff.go`、TabManager。

关键变更：

| 提交 | 意图 |
|---|---|
| `6b2068cada` | 临时文本来源、文件夹滚动恢复、Shift 锚点、白名单错误提示 |
| `f3444a1c59` | 浅/深比较区分、逐项同步结果、取消保留部分结果、截断与目标冲突 |
| `f96fba8a76` | roots 默认开放、Tab 缩进、按内容判脏、保存后重比、搜索状态 |
| `08cc737ce3` | 全局对齐行重建覆盖、光标、保存退回、保持视角 |
| `aa07c9a510` | 比对 Undo、联动横滑、按侧搜索、文件夹双击、紧凑界面 |
| `bfc0102b28` | compare job 并发、保留数量、取消响应测试 |

#### 复核方法与边界

已执行 `node kairo-review/comparetemp/reproduce_compare.cjs`，直接抽取 HEAD 函数到 Node VM 中运行。六组用例验证了相关函数的数据输出或处理器调用，记录在 `comparetemp/reproduce_compare_results.json`。这属于函数级验证，键盘/选择只构造必要 DOM 存根；下表明确区分实跑范围与静态推导，未冒充真实浏览器或服务端端到端测试。

| 项目 | 实际执行内容 | 未执行、依据源码判断的部分 |
|---|---|---|
| C01 | 原版 buildHunks / buildAlignedRows / reconstructTargetLines，构造 ignore_blank 后的响应行 | 未真正启动 Go handler；响应行与 handler.prepareLines 过滤规则一致；未写磁盘 |
| C02 | 原版 reconstructTargetLines 连续两次处理同一旧 rows，证实第二次输出不含第一次结果 | 未运行真实 Ajax 延迟/点击流程；旧 rows 可重复调用的窗口由 apply* 和异步更新调用链确认 |
| C03 | 原版 pushUndoSnapshot / swapSides / performUndo，配编辑器和UI存根 | 未调用 saveSide/WriteAtomic；写错目标后果由 sourceAtSave / target 请求的静态路径判断 |
| C04 | 原版 history、textarea/panel handler 按冒泡次序调用；原版 document handler 面对外部 contentEditable 焦点 | 未由真实浏览器派发按键、未测试系统原生Undo；后台拦截实际表现需E2E补验 |
| C07 | 原版 handleRowSelect / updateSelectionUI / updateActiveHighlights，配现有行/checkbox对象存根 | 未渲染真实checkbox；显示不一致由 checked 未被更新及模型结果确认 |
| C05 / C06 / C08 / C09 / C10 | HEAD 调用链与测试源码审查；C06 另读 BASE 验证归因 | 未现场关闭Tab、取消远端任务、写本地文件；不能表述为已运行端到端复现 |

Playwright 库可加载，但环境没有 Chromium 可执行文件，真实浏览器布局/滚动测试未执行。横滑三项以“待浏览器验证风险”列出，不计作已运行证实的问题。没有修改仓库源代码。

#### C01 — P1：启用“忽略空行”后，覆盖一个差异会删除目标文件所有被忽略的空白行

**归属：`08cc737ce3` 新增回归；已函数级复现。**

证据：

- [compare.js L1378-L1387，展示行来自差异结果](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1378-L1387)
- [compare.js L1391-L1424，遍历 allRows 重建整个目标](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1391-L1424)
- [handlers_diff.go L148-L156，ignore_blank 移除空白行但保留真实行号](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_diff.go#L148-L156)
- 三个入口都调用该重建：`applyHunk` L1244-L1258、`applyLine` L1262-L1270、`applyBatch` L1273-L1288。

最小输入：左 `header\n\nnew\n\nfooter\n`，右 `header\n\nold\n\nfooter\n`。启用 ignore_blank，只覆盖 `old → new`。预期右侧是 `header\n\nnew\n\nfooter\n`；实际 `header\nnew\nfooter`，中间空行和末尾换行全部丢失。对于用空白行表达段落的文档、脚本或配置，保存后就是非选中范围的数据修改。

触发前提：文本已成功加载并完成比较，启用“忽略空行”，目标存在被该选项省略的行，用户点击行/块/批量覆盖。首先改变内存；必须再成功保存才会改变磁盘，不能说点击覆盖就直接删除了磁盘内容。

原因：服务端 diff 中“省略展示的行”与“原文件中不存在的行”被当成同一概念。`reconstructTargetLines` 不接收原始目标数组，仅从过滤后的 alignedRows 收集行。

实现方案：

1. 独立保存完整文档行模型，不以展示 rows 作为写回数据源。
2. 从选中差异生成 `{start, deleteCount, insertLines}` 补丁，坐标使用真实目标行号；只在选中区间应用补丁，按目标起始行倒序处理。
3. 对源侧独有的插入行，使用前/后已对齐的目标行作为插入锚点；不能简单使用另一侧行号。
4. 保留目标原始数组中未参与差异的空白行与末尾空元素；ignore_blank 只影响等价判断和显示。

验收：上述输入、仅空格/Tab 行、文件头尾空行、仅一侧有空白行、多选不连续行、仅差异视图、左右双向覆盖，都必须保证未选中目标区域逐字不变。

#### C02 — P1：连续覆盖使用旧 diff，第二次操作会静默撤销第一次修改

**归属：`08cc737ce3` 改成整文件重建后引入，`aa07c9a510` 改立即请求重比也未补版本门禁；重建函数的数据行为已执行，Ajax时序可达性由源码确认。**

证据：[applyLine / applyBatch L1262-L1288](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1262-L1288)。`state.alignedRows` 只在 [renderResult L1191-L1192](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1191-L1192) 更新。`markDirty` 会设 `diffStale`（L921），但合并操作不检查它，`compareNow` 仅禁用“比较”按钮（L1119），合并按钮仍能点击。

复现：左 `A\nseparator\nB`，右 `a\nseparator\nb`。先把第一处覆盖到右边，右变 `A\nseparator\nb`。在重比 API 返回前再覆盖第二处，实际右变 `a\nseparator\nB`，第一处 A 被旧 rows 内的 a 覆盖。慢网络、大文件、自动比较失败后继续操作，都可扩大窗口。

触发前提：同一已渲染diff内有两个可操作差异；第一次覆盖之后，第二次覆盖发生在新diff被安装之前（或新diff请求失败后）。不是每次正常等待重比完成后再操作都会出错；当前复现没有实际发HTTP请求，也没有实际保存文件。

同类窗口：原始 textarea 编辑后，600ms 防抖期间仍可点击旧差异箭头；旧请求在 textarea 已改变但新请求尚未发出时返回，`requestNo` 仍有效，响应会被当成最新结果。当前只检查请求序号，没检查输入的 editSeq/内容指纹是否仍一致。

实现方案：

1. 每个 diff 绑定不可变 `{leftDocumentId,rightDocumentId,leftRevision,rightRevision,optionsRevision}`。
2. 所有 apply* 操作入口强制校验当前版本与 diff 版本；不匹配时阻止操作并请求重比，所有行/块/批量按钮显示等待状态。
3. 每次原文变化立即增加 revision；API 返回时除 requestNo 外还检查 revision，旧结果不得安装。
4. 第一轮安全修复可以“写内存 → 禁用合并 → 最新重比成功 → 恢复合并”；若要支持排队，必须把第二个用户意图按新文档重定位，不能复用旧 rows。

验收：人为延迟 diff API 2 秒后连续合并两处；自动比较返回乱序；返回前编辑源/目标；API 失败后再合并。结果必须累计用户操作或明确拒绝，不能撤销其它已完成编辑。

#### C03 — P1：Undo 只恢复文本，不恢复文件身份；撤回交换后保存会写错文件

**归属：`aa07c9a510` 新增 Undo；已执行 HEAD 的 pushUndoSnapshot / swapSides / performUndo 复现。**

证据：[快照/撤回 L670-L691](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L670-L691)，[交换 L1301-L1304](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1301-L1304)，[loadPair L1329-L1338](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1329-L1338)。

复现：左文件 `/left.txt` 内容 `L`、右文件 `/right.txt` 内容 `R`。交换后左侧 state 指向 `/right.txt`、右侧指向 `/left.txt`。点“撤回”，文本恢复 L/R，但 source、version、baseline 等状态仍是交换后的左右归属。再保存左侧会将 L 写进 `/right.txt`；保存两侧相当于把磁盘两个文件内容对调。

触发前提：两侧是不同路径、不同内容的已加载可写文件，使用工具栏“交换”后点击工具栏“撤回”，然后执行保存，期间文件未被外部改写导致版本冲突。无需依赖C04的键盘路由问题。脚本确认的是恢复后的路径/文本错配；实际写盘后果未现场执行。

另一个入口：Undo 栈在重新打开文件或 `loadPair` 时从不清空，上一对文件的历史可以恢复到新一对文件的编辑器中，然后按新路径写回。

实现方案：把“文档身份”与“左右槽位”分离。文档有稳定 id、source、baseline、version、codec、text；交换只变更槽位中的文档引用。Undo 使用类型化命令：文本编辑绑定 documentId，交换命令撤销引用顺序。加载新文档时建立历史边界或清空工作台历史。不要简单恢复旧快照中的服务器 version/baseline，否则跨保存 Undo 又会使用过期版本；保存后的版本归文档状态管理。

验收：交换→撤回→保存；两侧不同编码；交换前一侧已脏；打开新文件→Undo；保存后 Undo 再保存。检查实际请求 target.path/content/expected 的归属与磁盘内容。

#### C04 — P2：Ctrl+Z 重复分发，同时会影响隐藏的比较 Tab

**归属：重复编辑撤回来自 `08cc737ce3`；document 全局拦截来自 `aa07c9a510`；均已函数级复现。**

证据：[textarea 键盘处理 L427-L446](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L427-L446)、[panel Ctrl+Z L855-L860](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L855-L860)、[document 处理器 L699-L709](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L699-L709)。

- textarea 自己 undo 后只有 preventDefault，没有 stopPropagation；事件冒泡至 panel，再执行一次 editors[side].undo。历史 `'' → A → B` 按一次 Ctrl+Z，实际回到空字符串；预期 A。
- document listener 只排除 textarea / input[type=text]，没有检查当前 Tab、panel.contains、contentEditable 或 defaultPrevented。Compare Tab 隐藏但未关闭时仍注册着；在其它页面的 contentEditable 或普通按钮焦点下按 Ctrl+Z，会撤回后台比较历史并阻止本来页面的快捷键。
- 在比较结果 contentEditable 内，panel 会避开原生编辑，但 document handler 又拦截，形成第三套 Undo 语义。

实现方案：统一 Undo 路由。textarea/contentEditable 拥有其输入级历史，处理后停止冒泡；工作台命令 Undo 绑定本 panel，不用 document 全局监听，且先检查 event.defaultPrevented / active Tab / event.target 属于当前面板。输入框中的 Ctrl+Z（搜索、文件路径）不能触发文件编辑撤回。

验收：一次 Ctrl+Z 只撤回一个步骤；Ctrl+Shift+Z 同样只一次；Ctrl+Z 在搜索输入框、本侧 textarea、行内编辑、工具栏焦点、另一 Tab、隐藏 Compare 各自作用于正确历史，另一 Tab 内容不改变。

#### C05 — P1：比较页的未保存编辑没有加入 Tab 关闭/离开保护

**归属：`6791488014` 新增多 Tab 关闭路径与近期 Compare 编辑功能的集成遗漏；HEAD 源码可确定。未声称此前已有比较关闭守卫被删除。已与 UI 调用链交叉确认，不重复报。**

`renderCompare(view)`（compare.js L482）不接收 scope；返回的清理器 L566 只销毁编辑器；dirty 仅控制按钮，没有向全局保护器暴露。整份 compare.js 无 canLeave/setCanLeave/close guard/beforeunload 注册。

[tabs.js L354-L370](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/tabs.js#L354-L370) 只汇总注册过的守卫；[requestClose L407-L440](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/tabs.js#L407-L440) 无 blocker 就销毁。app.js 全局 close guards 只有 config/database/uploads/SSH/notes，不含 compare。

确定的源码触发路径：至少打开两个应用 Tab，比较页对任一侧做覆盖而不保存，且没有其它全局阻止项；关闭比较 Tab 走无blocker销毁路径，重新打开时此前只存在内存的修改消失。浏览器退出同样不会因为比较页自身脏状态而提示。此处没有在真实浏览器中执行关闭操作，不排除其它页面有未保存内容时触发其自己的提示。

实现方案：给比较工作台提供 `pendingWork()`/`isDirty()`，注册 Tab owner 对应关闭守卫并在 cleanup 解除；覆盖、文本输入、行内未完成输入都计入；保存成功/退回后清除。界面提供“保存后关闭 / 放弃 / 取消”，异步保存失败时保持 Tab；真正 beforeunload 仅依赖同步 pendingWork 判定，不能尝试异步保存。普通 Tab 切换保活可允许，关闭和打开替换新来源需保护。

#### C06 — P1：同步冲突保护没有覆盖“预览时目标不存在”和“整目录递归同步”

**原有残留，不冒充本周新引入；但 `f3444a1c59` 声称目标变更保护与 T060 验收，实际仅既有单文件路径闭环。**

归因再次核验：[BASE L550-L615](https://github.com/Qioooba/kairo/blob/dc75701583fe78ec8b8bcfa706d77c68a7ba6011/internal/httpserver/handlers_compare_workbench.go#L550-L615) 已存在相同的目录子文件重新snapshot及单文件Expected空时采用执行快照逻辑。不得把C06计入“f344新引入的回归数”。

证据：[前端预览请求 L3167-L3169](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L3167-L3169)，目标不存在发 `expected:null`；[后端 L613-L623](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_compare_workbench.go#L613-L623)，执行开始重新 snapshotCompareTarget。结构 compareSyncItem L82-L85 没有预览 ExpectedMissing 字段。

复现：扫描时右侧无 `new.txt`，预览标“新增”。在点“开始覆盖”前，另一个程序创建右侧 `new.txt`。服务端现在把这个新出现文件采成 expected，后续校验通过，覆盖其内容。ExpectedMissing 只防“服务端采样之后”出现文件，不能防扫描/预览之后出现。

完整前提：同步源/目标为不同且可访问可写的允许目录，源文件存在；首次扫描/预览目标缺失，外部在提交同步前创建目标，且从同步开始快照到写入期间目标不再变化。上述结论是前端请求→服务端快照→版本校验的静态调用链推导，并未现场执行该同步。

目录路径更明显：L572-L611 展开目录时根本不检查 item.Expected，子文件 expected 都在 L600 重新采集；预览之后外部修改目录中的目标文件，也可能被新快照接受并覆盖。

实现方案：请求中的预期目标必须三态表示 `unknown / missing / existing(version)`，禁止用 null 同时代表“预览不存在”与“不提供条件”。前端传 expected_missing=true；后端在初次 Stat 就要求仍不存在，并把该条件传到 WriteAtomic。目录同步先生成具体文件级预览计划及版本，回传不透明 planId 或带身份绑定的计划，执行不得用新状态悄悄重置原预期。

验收：预览后外部新增、删除、改写目标；目录子文件改写；源文件变化；执行期间出现目标；所有场景应返回逐项 conflict，原目标字节不变。已有单文件版本变更拒绝行为已实现，可保留。

#### C07 — P2：批量选择的模型与复选框显示不同步，可能造成选错覆盖范围

**本周新增文字 diff 批量选择；已抽取函数复现。**

证据：[handleRowSelect / clearSelection L1800-L1830](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1800-L1830)、[updateActiveHighlights L1908-L1916](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1908-L1916)。更新 Set 后只刷新 class；checkbox.checked 仅在 renderWindow L1873 创建时赋值。

勾第 1 行、Shift 勾第 3 行，model={0,1,2}，可见 checkbox={true,false,true}；中间行实际会被覆盖但框没勾。“选择全部差异行”同样只更新背景；“取消选择”清空 Set，但现有勾号还留着，用户再点有勾的框会触发取消而非选中。

修复：updateActiveHighlights 根据每个 rowIdx 把对应 `input[type=checkbox].checked = selectedRowIndices.has(rowIdx)` 同步；不必重建整个虚拟列表。覆盖按钮与数量统一从 Set 算，屏幕显示必须一致。验收 Shift 连选、Shift 反选、全选、取消、滚动换窗口、覆盖前逐项核对。

#### C08 — P2：部分同步结果只做了复制阶段，准备阶段漏项；普通“取消任务”又直接丢弃结果 UI

**准备阶段漏记为 `f3444a1c59` 新增结果合同未完成；关闭弹窗丢结果/创建期间取消漏洞是原有流程残留。**

后端：

- [L542-L548](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_compare_workbench.go#L542-L548) 在规划第 idx 项时取消，只记录 idx..最后的请求项；此前已加入 plans 但还没复制的文件全部漏掉，Failed 也没统一结算。
- L552、569、574、580、585、602、615 的准备失败只写 Failures，不写 Items；L562-L564 重复覆盖条目直接 continue，新增 Skipped 字段从未递增。
- 例如 3 项，第一项已规划、第二次 loop 头观察到取消，结果只含后两项 cancelled，第一项实际未复制却从明细消失。

前端：取消按钮 L3224 始终调用 close，modal.onClose L3228-L3232 设置 closed、清除 poll、发 DELETE 然后不再取任务结果，所以本次新增部分结果展示分支通常不会在用户主动取消时出现。更危险的现存窗口：POST `/sync/start` 尚未返回时关闭，jobID 为空不会发 DELETE；L3248-L3250 收到 jobID 后 poll 因 closed 返回，后台复制任务继续跑。

修复：

1. 统一 outcome reducer，创建计划时建立每项 pending；所有 return 前 finalize，将未执行项归入 cancelled/skipped，所有失败路径也生成明细并统一统计。
2. 分清顶层请求项和展开后的文件项；计数单位写清楚，目录被覆盖而跳过的子请求标 skipped，避免重复与漏项。
3. 取消进入 cancelRequested 状态，保留弹窗到后端终态，展示 success/conflict/failed/cancelled/skipped 明细；关闭与取消是两个动作。
4. start 响应晚到时若已请求取消，立即 DELETE 新 jobId，然后继续拉取终态；必须保证取消意图不因 jobID 尚未取得而丢失。

验收：开始请求尚未返回时取消、连接/扫描/规划期间取消、复制第 N 项期间取消、正常完成同时点取消；所有实际写成功项可查，未执行项无漏项，任务不孤儿化。

#### C09 — P2：宣称“保存后保持视角”和“方向键准确定位”仍有确定缺口

保存路径 [L1043-L1057](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1043-L1057) 只保存 wasShowing 布尔值，随后 loadSide→invalidateComparison 把 currentVirtualDiff=null、showingResult=false（[L888-L900](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L888-L900)）。自动重比时已经拿不到原滚动/活动行。普通覆盖的 renderResult(showingResult) 有恢复，但保存后没有。修复：保存前保存 viewState，在重新载入完成后的 compare/render 显式传回，必要时用原始目标行号锚定。

方向键：[navigateLine L1961-L1966](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1961-L1966) 永远先选择右侧 code，并把 caretAtEnd=true。用户在左侧中间列按↓会被送到下一行右侧行尾。修复：导航参数带 side 与目标列号，选同侧行，以 DOM Range 映射字符列；保留 preferredColumn，短行截断后到长行恢复。

搜索 query/case/side/open 能恢复，但 restoreSearch L1687-L1689 每次把 activeMatchIndex 重置 0，尚未保存当前命中序号；有第三处命中时修改/重比后计数回 1，再“下一处”跳第二处。增加稳定的 `{side,originalLine,column}` 命中锚点。

这三项功能部分已实现，不应整项打“已完成”。建议合并为一个视图状态模型修复任务，而不是用多个局部 scrollTop 补丁。

#### C10 — P2：T060 的“截断目录不得部分同步”半个测试是空验收

**归属 `f3444a1c59`；源码确定。**

[compare_backend_p0_test.go L468-L478](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/compare_backend_p0_test.go#L468-L478) 用 Kind="synthetic" 调 performCompareSync，但 openCompareFS（handlers_compare_workbench.go L174-L219）仅支持 local/sftp/ftp，会直接返回不支持。测试没有 assert err、res.Truncated、零复制或目标不变，仅 `_ = truncatedRes`。因此即使完全删除截断保护，这段也会通过。

T061 第二次 runCompareSync 复用已经 cancel 的 ctx，能证明“立即取消返回非空结果”，不能证明复制了一部分后 job 的 SyncResult 保留了成功记录；准备阶段取消也没覆盖。

实现：注入 compareFS factory 或在独立 copy engine 层传 FS，真正喂超限 synthetic FS；断言返回 truncated、所有目标字节/目录状态不变；取消测试通过第一次复制后受控 hook 取消同一个 runCompareSync 的 ctx，再检查最终 job 的部分成功项。以上 C01-C09 用例也应补入回归入口。现有 3 个 reconstructTargetLines 示例全部假设 rows 覆盖完整原文，不能覆盖 ignore_blank 与异步交错。

#### 需真实浏览器核验的风险，未冒充已运行证实

1. **横向滚动后的虚拟行丢偏移。** `makeDiffCell` L2211-L2214 在 codeWrap 尚未挂入 DOM 时写 scrollLeft；renderWindow L1839 重建 rows 后没有统一挂载后恢复横滑，只有手动横滑/restoreViewState 才再次写。应测试向右滚 400px 后上下滚动 100 行，新渲染行是否回 x=0。修复可在全部节点挂载后统一 scrollLeft，且需要 dispose 旧视图计时器。
2. **maxCodeWidth 使用 maxChars*8.2 的估计，不含实际字体/中文/Tab 展开。** compare.js L1465-L1491，CSS code 行保持 white-space:pre，中文长行或连续 Tab 的实际宽度可能超过虚拟滚动条范围。需 1000 汉字、Tab、混合宽字符、缩放测试，末尾必须可见。应从实际 scrollWidth 或按当前 font 的宽度计算出范围，并响应字体/窗口尺寸变化。
3. **程序设置 wrap.scrollLeft 的 scroll 事件反馈。** syncHScroll 用同步布尔锁，而 canvas capture scroll 事件会将任意行的实际 scrollLeft 写回左轨。行长不同发生 clamp 后可能把全局偏移拉回较短行位置。需浏览器混合长短行测试；统一滚动容器/主动来源令牌比瞬时布尔锁可靠。

#### 功能落实矩阵

| 需求/宣称 | HEAD 状态 | 依据与剩余项 |
|---|---|---|
| 使用全局对齐行修复勾选插入/删除行错位 | 部分 | 简单完整 rows 场景成立；C01 空白行丢失、C02 旧快照覆盖 |
| 文字行/块/批量覆盖仅改内存，保存才写文件 | 基本实现 | apply* 改 editor；save 调 /write；但写入文本正确性被 C01-C04 破坏 |
| 按内容判脏、撤回到基线自动消脏 | 已有正确实现 | markDirty 比较 current 与 baseline；Undo 路由存在 C04 |
| 保存后自动重比 | 已实现 | saveSide 成功 reload 后 compareNow；视角未保存见 C09 |
| 一键退回未保存修改 | 已实现主要路径 | 恢复 baseline；baseline 是最近成功加载（包含保存后 reload），并非永远第一次打开 |
| 全局比对操作 Undo / Ctrl+Z | 未正确完成 | C03 文档身份错配；C04 多重/跨 Tab 拦截 |
| 未保存操作关闭保护 | 缺失 | C05 |
| 独立左右搜索、大小写开关、防抖 | 已实现主要路径 | 保存 query/case/side/open；当前命中锚点未保留 C09 |
| 横滑自适应与左右联动 | 有代码，待浏览器验证 | 三项横滑风险，不能据静态就给体验验收通过 |
| 行内光标方向键导航 | 部分 | 点击可启用编辑；上下键固定跳右侧行末 C09 |
| Shift/全选/取消复选框对齐 | 未正确完成 | C07 |
| 浅比较与深比较区分 | 已实现 | same&&!hashed 显示“元数据相同（未核验内容）”；same&&hashed 显示“内容一致”；T059 有实断言 |
| 既有单文件目标版本变更拒绝 | 已实现 | Expected 从 scan 传入，copy 前与 WriteAtomic 校验；T060 前半有断言 |
| 新增文件/目录同步的预览版本保护 | 未闭环（原有残留） | C06 |
| 截断目录拒绝部分覆盖 | 代码有保护、测试未完成 | plans 全准备后 result.Truncated 就 return，尚未写；C10 假覆盖需替换 |
| 复制中取消保留部分结果 | 后端主要路径实现 | C08 规划漏项、用户取消 UI 丢结果、晚到 jobId 漏取消 |
| compare job 并发8/保留16/每分钟16 | 已有实现并新增测试 | tryCreate 数量限制、终态淘汰；ctx取消测试只验证本地 channel 响应，不等于远端阻塞 I/O <50ms |
| roots 为空默认开放 | 按明确意图实现 | f96fba8 明确要 fail-open；AppConfig 与 Local 同步；非空配置仍有词法与 symlink 保护。不应单独以“开放”就判BUG，网络部署与RBAC另审 |

#### 建议交给实现 AI 的顺序

1. C01+C02：完整文档模型与 diff revision，首先保证不会改掉未选中内容。
2. C03+C04+C05：文档身份绑定历史、快捷键单一入口、未保存关闭保护。
3. C06+C08：不可变预览计划、逐项终态和可靠取消。
4. C07+C09 与横滑真实浏览器测试。
5. C10：修复假覆盖；每轮修复补真正能在旧版本失败的行为测试，才更新验收矩阵。

### 9.D. 多 Tab、资源生命周期、导航与布局

审核固定版本：`38df3aa604817955f470eaaaa0eb4c29124972c4`，比较基线：`dc75701583fe78ec8b8bcfa706d77c68a7ba6011`。源码在 `repo/web`；下列行号均为 HEAD。全量读过 app、tabs、core、route-scope、overlays、notes 两层模块、files、websphere、ssh 的资源接入、commands、home、diagnostics 与相关 CSS。DB 和 compare 业务由其他审核分工覆盖。

#### 结论

多 Tab 单实例保活主体已实现：切换时保留 pane 与控制器，关闭时才 dispose；晚到达的 render cleanup 会被回收；beforeunload 主处理器已改为只查询阻止原因。**但调用方迁移没有完成，不能认定“全部页面资源隔离与安全关闭”已经实现。** 当前还有能够复现的跨页面清理、旧页面异步响应覆盖新页面资源、比对草稿无关闭保护、非 notes 子路由丢失等问题。

##### 已执行验证

- `node tests/tab-lifecycle-review-regression.test.js`：4 组现有测试全部通过。
- `node ../notes/ui_runtime_probes.js`：以仓库原有 VM DOM doubles 加载 HEAD 的 core/tabs/app，抽取并执行 HEAD 真正的 `doDownloadSelected`、`doTailStart`，4 个缺陷逻辑均重现。测试脚本位于 `notes/ui_runtime_probes.js`，没有修改源文件。
- 这些是实际 JavaScript 函数运行验证，**不是完整浏览器 E2E，也没有真实 SSH 服务参与**。CSS 与 notes 内部子页数据丢失目前为静态确定证据；未声称截图验证。
- 额外核实了现有测试环境：其 document 缺失 addEventListener，真实 renderNotes 会抛错并由 tabs 显示“页面渲染失败 / document.addEventListener is not a function”。Fake DOM 的 innerHTML 赋值不移除 children，因此 R8/R9 还能找到旧按钮而 PASS。复用该 harness 渲染并遍历文本已确认。应补“页面无渲染错误卡”的断言并使用真实 DOM / 浏览器，不能用这两个测试的 PASS 宣称便笺页面成功渲染。

#### TUI-01 / P1：后台日志下载完成会关掉另一个 Tab 的下载 SSE

**定位：** `web/pages/websphere.js:1235,1300,1499`；`web/core.js:779-793,837-843`。

`doDownloadSelected` 和 `doDownloadLatest` 注册 SSE 已传 `tabOwner`（1163、1400），每组 `finish` 也传了 owner 与任务 id（1174、1411），但整个异步任务的尾部仍调用无参数 `Kairo.core.clearActiveDL()`。`core._tabIdOf()` 对无参数调用查询执行当时的活动 Tab。

**复现：** 日志助手中开始下载 A → 切到文件传输并开始较慢下载 B → A 完成。A 的整个循环退出至 1235 时，活动页是 files，因此关闭 B 的 EventSource 并删除 files 的 DL 注册。用户看到 B 的进度卡住；失去 SSE 不等于 B 的后端下载已取消，不要把该结果误写成“远端任务必定被取消”。

**运行证据：** 真正 `doDownloadSelected` 完成后，B 的 `close()` 调用数为 1，`getActiveDL('files') === null`。

**实现方案：** 所有异步资源注册/释放必须使用创建时捕获的 owner 和资源实例，消除页面异步代码里的无参 `clearActive*()`。尾部若没有仍属本任务的资源可直接不再清理；如需清理，传 `(tabOwner, currentResource)`。建议从兼容 API 中拆出 `registerResource(scope, kind, resource)`，不允许业务层在异步回调中隐式推导 owner。不要只改其中一个调用点。

**验收：** files 与 websphere 同时各跑下载；分别在 A 先完成、A 出错、A 用户取消三种情况下，B 的 SSE、注册记录、进度继续有效；反方向相同。检查背景页面完成提示不会错误改变另一任务状态。

证据：[websphere.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/websphere.js#L1235)、[core.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/core.js#L837)。

#### TUI-02 / P1：Tab 关闭后迟到的启动响应仍创建流，并覆盖重开的 Tab

**定位：** `web/pages/websphere.js:2855-2869`；同模式 `web/pages/files.js:1283-1293,1959-1969`；`web/core.js:820-831` 与 setActiveTail；`web/workbench/route-scope.js:117-159`。

`doTailStart()` 等在 await POST 后不检查 `scope.isCurrent()`、任务 generation 或 pane 身份，直接创建 EventSource 并按固定路由 id 注册。Tab 关闭时注册表还没有流，所以 `releaseTabResources` 没有可释放对象。更严重的路径是：关闭旧 websphere → 重开同名路由 → 新 tail 先注册 → 旧 POST 返回，用同一个 `'websphere'` key 覆盖新 tail；关闭新 Tab 就只关闭了旧流，真正的新流继续活着。R1 增加的 expectedInstance 只防止旧清理删除新记录，**不防止旧注册覆盖新记录**。

RouteScope 的 registerDL/registerTail/registerShells/registerUploads 也不构成兜底：先 setActive，再 `scope.add(() => setActive(null,...))`。disposed 后 add 虽立即调用 disposer，却只是注销条目；没有 close/cancel 新资源。当前运行探针确认 `scope.dispose(); scope.registerTail(stream)` 后 `stream.close` 为 0。

**运行证据：** 真正 `doTailStart()` 挂起 → close/reopen → 新 tail 注册 → 旧响应到达，注册 id 变成 `old-late-tail`；再次关闭后新流 close 次数为 0。

**实现方案：**

1. 每次挂载产生唯一 `instanceId`；scope 与资源注册保存该 id，路由 id 仅用于 UI 查找，不能独自代表实例。
2. await 后先判断当前 scope；对已创建后端任务但 scope 已失效的响应，应调用任务 stop/cancel，再返回，禁止建立 SSE。
3. 创建操作的 fetch 接入 scope 的 AbortController；仅 abort fetch 不足以保证服务端任务没有创建，所以仍需对迟到成功结果做补偿取消。
4. RouteScope register 系列在 disposed 时立即实际 `close`/`cancelAll`，且不能覆盖活跃实例。释放动作与注销动作分开命名，不要把注销当作释放。
5. websphere 当前除取 tabOwner 外没有用 scope，也没有 return cleanup（函数终点 3457）；将 pagehide、keydown、定时器、编辑器 SSE、启动请求纳入 scope。files 的 2068-2072 仅移除了 controller，2025-2034 的全局事件仍常驻，需一并迁移。

**验收：** 人工挂住 start 响应，执行 A→关闭 A→重开 A→新响应先于旧响应；老实例不新增 listener/SSE，不覆盖新记录，已建后端任务被补偿 stop。重开关闭 30 次，document/window listener 与 SSE 数量回到基线。

证据：[tail 启动](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/websphere.js#L2855)、[files 启动](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/files.js#L1283)、[scope 注册](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/workbench/route-scope.js#L117)。

#### TUI-03 / P1：比对编辑没有接入关闭 / 浏览器退出守卫（与 compare 报告同一项）

**定位：** `web/app.js:47-112` 关闭注册表；`web/tabs.js:354-371,407-440`；`web/pages/compare.js:482` 与左右侧 dirty 状态。

app 只注册 config/database/uploads/ssh/notes。compare 的 `renderCompare(view)` 不接 scope，整文件没有 canLeave/setCanLeave/beforeunload/close guard 注册。dirty 只影响保存与撤回 UI。因此：任意比对输入/覆盖造成未保存修改 → 打开另一外层 Tab → 关比对 Tab，`closeBlockers('compare')` 返回空，pane 与编辑状态直接删除；刷新/退出的统一守卫同样不知道这些修改。这是新增“关闭才保护”协议接入缺失，不是已有保存按钮即可覆盖。

**实现方案：** 把 dirty 查询作为页面实例提供的 blocker 注册到 scope，包含左右侧与当前文档 id，并提供明确的 discard 回调；beforeunload 使用同一只读 blocker。为临时文本与本地/远端文件分别验证；保存失败后不得把 dirty 清掉。避免把 compare 全局单例状态暴露给 app 去猜。

**验收：** 左/右任一脏侧，外层 Tab close 与浏览器刷新均提示；取消保留编辑、滚动与 undo；保存成功后解除；切换 Tab 本身仍不提示。具体比对操作的代码细节由 compare 报告提供，汇总时不要重复计数。

证据：[guard 列表](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/app.js#L47)、[无 blocker 直接关闭](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/tabs.js#L407)、[compare 入口](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L482)。

#### TUI-04 / P2：日志助手子路由在保活重用时失效，刷新/分享状态也被抹掉

**定位：** `web/app.js:38-42`；`web/tabs.js:43-47,311-314`；`web/pages/websphere.js:3347-3357,3413-3420`。

routeFromHash 丢掉 query，除 notes 外都传 `{}`；hashFor 也只支持 notes 子状态，其他路由强制写回 `#/websphere`。websphere 仅初次渲染读取 location.hash query，内部 switchTab 自己写 history，却没有将状态交回 TabManager，也没有监听 route-state。

**后果：** 首次深链进入 `?tab=tail` 能在初次 render 读到 tail，但 activate 紧接着删除 query，刷新后回 files；一个已保活的页面在 tail 时再点击侧栏 `?tab=files`，不重新 render，也没有有效事件，仍留 tail，与入口“总落 files”的注释目标冲突。

**运行证据：** 在已经打开的 websphere 上设置 hash `#/websphere?tab=tail`，最终变为 `#/websphere`，pane 收到的 route-state 为 `{}`。

**实现方案：** 统一 parse/serialize routeState，白名单支持 websphere 的 `files/search/tail`；websphere 从传入 routeState 初始化，监听 `kairo:route-state`，内部切换调用 `tabs.setRouteState`，禁止自身直接改 history。setRouteState 后也持久化。明确“点击外层已开 Tab保留子状态”与“点显式侧栏 URL采用指定子状态”两种语义。

**验收：** 3 个深链首开/刷新/返回/前进；页面保活时相同路由新 query；离开再点外层 Tab仍保留 tail，点击指定 files 的侧栏回 files；notes 子路由不回归。

证据：[route parse](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/app.js#L38)、[hash serialize](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/tabs.js#L43)、[日志内部切换](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/websphere.js#L3347)。

#### TUI-05 / P2：便笺草稿关闭保护只覆盖外层，内部切换仍丢内容

**定位：** `web/pages/notes.js:14-19,42-57,264-309`；`web/notes.js:40-48`。

inlineDrafts 只存布尔标志，真正的 title/body/color 值只存在行内 DOM。点击“提醒”或重复点击当前“便笺”按钮会运行 renderTab，57 行 `content.innerHTML = ''` 直接移除编辑 DOM，不检查 dirty，也不保存 draft 值。回便笺重建列表使用服务端已保存 n.title/n.body，刚输入的内容已丢；inlineDrafts 标志却残留，之后关闭还提示“未保存草稿”。新 R8 外层关闭测试只手动 setInlineDraft，并未输入或切换真实内部子页，测不到这一点。

已对照 BASE 的 notes.js:27-39：该内部切换清空行为在基线即存在，是**既有缺口，本轮草稿保护未闭环**，不是本轮新引入。新 route-state 同样调用这个有缺口的 renderTab。

**实现方案：** draft 按 noteId 保存完整字段与 baseline；保活 list/reminder 两个子容器，或 render 前保存/恢复 draft；切换到相同 tab 直接 return。若选择拦截内部切换，则需统一处置确认，确认丢弃后才删除实际 draft 与标记。

**验收：** 输入新标题正文颜色 → 切提醒 → 返回仍保留；重复点当前便笺不重建；关闭取消仍保留全部字段；保存后 pending 标志与实际内容一致。

证据：[内部切换清 DOM](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/notes.js#L42)、[草稿仅持有 bool](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/notes.js#L40)。

#### TUI-06 / P2：桌面收起侧栏后缩窄窗口，移动菜单打不开

**定位：** `web/style.css:320-332` 与 `8103-8135`；`web/app.js:200-220`。

全局 `:root[data-sidebar="collapsed"] .sidebar` 设置 width:0、visibility:hidden、opacity:0、pointer-events:none，均为 !important；移动 CSS 用低优先级 `.sidebar` 和 `.sidebar > *` 尝试以 !important恢复。两者都 important 时，前者的选择器权重胜出。桌面折叠后 resize 到≤768，root 属性未清；移动按钮只切 body.sidebar-open，结果打开遮罩，侧栏仍不可见且不可点。

**实现方案：** 把所有桌面折叠 CSS 放进 `@media (min-width: 769px)`，移动抽屉独立于 desktop collapsed 状态；用 matchMedia change 更新按钮 aria-expanded/title，不覆盖保存的桌面偏好。避免以更多 !important 层层覆盖。

**验收：** 宽1920折叠→窄767打开抽屉→回宽1920保留折叠；宽侧栏展开→窄侧栏默认关闭；横竖屏多次切换，遮罩和按钮状态正确。Ctrl+B 在输入/contenteditable 中不会被抢占，这部分源码已有判断。

证据：[桌面折叠](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/style.css#L320)、[移动规则](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/style.css#L8103)。

#### TUI-07 / P2：pagehide 的 bfcache 保护在旧页面监听器中仍被绕过

**定位：** `web/app.js:178-181` 与 `web/pages/websphere.js:3024-3028`。

app 对 persisted=true 会正确 return，但 websphere 仍单独注册匿名 pagehide，完全不看 persisted，tailId 非空就发 stop beacon。页面如进入 bfcache，前端状态保留而后台 tail 已停止，返回后原页面无法继续原会话。该监听也未在关闭 Tab 时移除。此项由调用链静态确认，当前未执行真实 bfcache 浏览器验证。

**实现方案：** 生命周期出口集中在 app，去掉页面匿名 pagehide；需要特定动作时注册到 scope 并由统一事件带 persisted/context 调用。对于正常 Tab close，websphere 应主动 stop 后端 tail；目前只关 SSE，后端 tailmgr 无订阅者 grace 60s 清理（每轮 GC 判断），不应宣传为关闭立即释放 SSH。

**验收：** persisted=true 不发送 stop，不销毁会话；persisted=false只取消一次；正常关闭所属 Tab立即 stop，别的 Tab不受影响。Safari/Chromium以实际支持 bfcache 的条件单独跑。

证据：[页面旧监听](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/websphere.js#L3024)、[全局新监听](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/app.js#L178)、[后台 grace](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/tailmgr/tailmgr.go#L333)。

#### 其他已核验边界（不必全部占主报告问题数）

- **弹层归属只迁移了一部分。** overlays.modal 根据调用时 active Tab 推导 owner（overlays.js:37），后台 await 完成后的调用会归错页；prompt 没把 opts.ownerTab/tabId 透传到 modal（137附近）。core.confirmDialog 自己创建 `.kairo-dialog-overlay`（core.js:249-296），files/websphere/ssh 还各自 append body 的 overlay，不在 closeTabOverlays/setActiveTab 栈里。将统一 overlay API 注入页面 scope，异步调用显式带实例 owner。
- **隐藏 modal 仍处理按键。** overlays.js:79 的 onKey 只比较全栈最后项，不判断当前 active Tab或 display；setActiveTab仅改display（196-202）。在A弹层隐藏、B工作时按Escape会关A的未提交弹层，焦点恢复也可能指向隐藏页面。应以“当前可见栈顶”处理焦点陷阱/Escape并维护 has-open-overlay。
- **日志助手全局 `/` 监听无活动页判断。** websphere.js:3200-3208 即使 pane 隐藏或已关闭仍preventDefault并试图focus旧输入；commands与ssh这次已经做了路由判定/关闭时remove，websphere漏迁移。纳入 TUI-02 全局监听清理验收即可。
- **scope.canLeave目前没有真实页面接入。** API和 mock test存在，但业务 guard主要仍硬编码在 app；若未来既返回silent blocker又有确认型 blocker，tabs.js:415仅在没有confirms时阻止，确认其他原因会越过silent blocker。当前没有实际页面用这一组合，作为API契约建议而非确定用户路径的独立P1。

#### 功能实现矩阵

| 目标 | 当前实现判定 | 证据/限制 |
| --- | --- | --- |
| 每路由最多1个Tab，切换保留DOM和输入 | 已实现主体 | tabs Map键为route；activate只hide/show；原测试通过 |
| 首页落地后自动收掉、最后一个Tab不可关 | 已实现 | openRoute/requestClose + 原测试覆盖 |
| 按Tab登记上传、SSH、tail、download | 部分实现 | core Map存在，若干最终clear漏owner，迟到注册无实例检查 |
| 关闭某Tab不影响另一Tab任务 | 未闭环 | TUI-01真实函数复现 |
| 关闭后晚回调/晚注册不泄漏 | 未闭环 | renderer cleanup处理正确，但TUI-02真实页面API任务仍失败 |
| beforeunload取消时不先杀资源 | 全局处理器已实现 | 现有R2测试通过；不等于所有页面卸载处理已迁移 |
| bfcache保留前后台状态 | 部分实现 | websphere旧pagehide仍发stop |
| DB/配置/上传/SSH关闭保护 | 注册机制存在 | DB具体事务处置以db_frontend报告为准；SSH真实已连会话有提示 |
| compare未保存内容保护 | 未实现 | 无关闭/卸载guard，dirty仅影响按钮 |
| notes草稿保护 | 部分实现 | 外层close可确认，内部renderTab仍丢值 |
| notes list/reminders外部子路由同步 | 已实现基本切换 | pane route-state监听已接，现有R9 test通过 |
| websphere files/search/tail子路由同步 | 未实现完整协议 | query被parse/serialize丢弃；无route-state监听 |
| 桌面折叠/Ctrl+B/偏好持久化 | 已实现常规桌面流程 | 输入判断已存在；跨768断点失败 |
| 弹层跨Tab归属/清理 | 部分实现 | managed modal可hide/close；旧弹层与异步owner不完整 |

#### 建议交给实现AI的修改顺序

1. 先修 TUI-01 三个漏owner释放点，并补双下载交错完成的行为测试。
2. 实现 generation-aware Scope 资源协议，迁移 websphere/files 的请求、流、全局事件，覆盖TUI-02与TUI-07；资源dispose务必真实关闭，不能只delete Map。
3. 给 compare 和 notes 内部子页补真实draft查询/保护；使用相同生命周期query给Tab close与beforeunload。
4. 统一路由parse/serialize与每页route-state消费，修websphere深链；最后修CSS媒体范围与弹层可见栈。
5. VM逻辑测试之外增加真实浏览器针对用户输入/Tab切换/焦点与响应式尺寸的E2E。现有回归套件能证明基础框架行为，不能证明所有业务页面已迁移完成。

### 9.E. SFTP、SSH 与中文编码

基线：`dc75701583fe78ec8b8bcfa706d77c68a7ba6011`；审核 HEAD：`38df3aa604817955f470eaaaa0eb4c29124972c4`。

本专项覆盖 compare 中 `internal/sftpclient/*`、`internal/sshclient/shell*`、`internal/textcodec/*`、`handlers_files.go`、`handlers_ssh_sftp.go`、`handlers_ssh_sftp_upload.go`、`handlers_ssh_shell_test.go` 的全部变更，并追踪 `web/pages/{sftp-common,ssh,files}.js`、递归下载、远程编辑与目录同步调用链。

相关提交：`356312dbd897` 上传权限错误；`679148801427` GBK 中文路径与终端双向编码；`5eca1d899133` R5/R6 身份与父目录修复；textcodec 显式 GBK 编码拆分。

#### 总体判断

不是空实现：普通 UTF-8/GBK 文件名展示、非冲突路径回读、单个既有 GBK 父目录下写入、显式 GBK 文本编码、终端常见汉字输入，均有真实代码和针对性测试。

但“唯一身份贯穿前后端”“同名不同编码精确选中”“混合编码层级下完整读写”“覆盖后保留原物理名”“无额外元数据性能损耗”没有完整实现。现有 R5/R6 测试只覆盖 Client/mock 层的理想场景，漏过实际 UI/API/upload handler 链路。

本环境没有 Go 工具链，因此未声称执行 Go 测试，也未连接用户远端 SSH/AIX。已执行 `notes/repro_sftp_paths.py`：它是忠实对应关键控制流的 Python 源码模型，在临时 POSIX 字节文件名目录上复现；不替代 Go 或真 SFTP 集成测试。结果见下。

#### SFTP-01 — P1：path_id 没有接通 UI/API，双编码同名文件仍无法独立操作

##### 确定证据

- 新后端响应有 `path_id`，但只给非 UTF-8 条目：`internal/httpserver/handlers_ssh_sftp.go:245-257`、`handlers_files.go:420-432`。
- `web/pages/ssh.js:1592` 仍用 `joinPath(tab.sftpCwd, entry.name)`；`1594,1617-1621` 按 `entry.name` 存选择状态，`1513` 全选也是 `new Set(entries.map(e => e.name))`；`2280-2281` 批量下载再次按展示名拼路径。
- `web/pages/files.js:1060` 同样拼 `entry.name`；`1067-1072` 同名行共享选择键。
- 上述 3 个前端文件没有任何 `path_id` 或 `raw_name` 使用。
- 即便前端简单改成传 `entry.path_id`，SSH list `handlers_ssh_sftp.go:169-171`、preview `359-361`，files list `handlers_files.go:225-227`，upload target `handlers_ssh_sftp_upload.go:611-614` 都要求路径以 `/` 开头，会拒绝 `kairo-raw:...`。

##### 触发与影响

`/data` 下同时有 UTF-8 `中文.txt` 与 GBK `中文.txt`，二者内容不同。列表出现两行同名，但选择集合合并成一个键；点击任意一行均发送 `/data/中文.txt`。Client 按设计返回 `ErrAmbiguousPath`，所以预览、下载、重命名、编辑无法选择具体那一个。父目录同名时也无法继续下钻。

R5 单测直接调用 `PathIDOf` 再调用 `c.Remove(id)`，绕开整个 HTTP/UI 链路；这不能证明用户端已完成。

##### 实现方案

1. 定义统一 `RemotePathRef`：`display_path` 用于地址栏，`path_id` 用于操作，不把 token 当作普通 path 拼接。保留旧 `path` 字段兼容手输路径；请求含 `path_id` 时优先且唯一使用它。
2. **所有条目都发唯一 ID**，包括 UTF-8 碰撞兄弟、ASCII 子项；前端 Set、DOM key、任务 key 使用 `server + path_id`，展示名只用于展示。
3. 列目录、预览、下载、编辑、rename、mkdir、上传目标父目录、比对目录树统一接收身份。解码后再校验绝对路径/控制字符/授权，不能因为是 token 跳过账号或目录权限。
4. 后端返回 `parent_path_id` 与显示父路径，前端后退使用它，不从 token 字符串 `dirname`。
5. `raw_name` 若需要对外暴露，必须改成 `raw_name_hex`/base64；非法 UTF-8 原字节塞 JSON string 会被 U+FFFD 替换。

##### 验收

真 SFTP 建立双编码同名的文件与目录。两行可同时勾选、分别预览/下载/编辑/重命名；只作用选中的原始字节路径，兄弟的内容 SHA-256、mtime、名称字节均不变。通过 HTTP 走至少两级目录，不能只直调 Client。

代码链接：[前端 SSH 路径与选择](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/ssh.js#L1584-L1634)、[files 选择](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/files.js#L1058-L1072)、[API 拒绝 token](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_ssh_sftp.go#L165-L175)。

#### SFTP-02 — P1：PathIDOf 用显示父目录拼 raw basename，生成的是错误身份

主位置 `internal/sftpclient/names.go:94-100`；调用方 `handlers_ssh_sftp.go:251` / `handlers_files.go:426`。

`Client.ReadDir(displayParent)` 内部已解析到真实父目录，但 `wrapDecoded` 只保留 raw basename；handler 调 `PathIDOf` 时传回的仍是原请求显示父路径。例如真实目录为 `/tmp/<GBK中文>/<GBK子.txt>`，请求 `/tmp/中文`，返回 token 却编码成 `/tmp/<UTF8中文>/<GBK子.txt>`。`DecodePathIdentity` 成功后 `resolveExistingPath:783-784` 直接信任 raw path，绕过自适应，文件不存在。若父目录入参本身是 token，直接 `path.Join(token, rawName)` 会生成不以 `/` 开头的被编码内容，下一次 DecodePathIdentity 直接失败。

此问题目前被 SFTP-01 的 UI 未接通遮住；只修前端后会立即暴露。

修复：在 `ReadDir/ListLimited` 得到 `resolvedParent` 时构建每个 entry 的绝对 raw path，并绑定在返回 Entry/decodedFileInfo；ID 从已解析 raw absolute path 编码，任何 handler 都不能用显示路径重建。所有 Join/Dir/Base 都应以 `RawPath` 字节对象或经解码 raw string 操作，输出时另行生成显示文本。

验收：UTF8→GBK→UTF8、GBK→UTF8→GBK 两种三级路径；逐级 list 后返回的 ID 解码字节必须与真实磁盘路径逐字节相同，使用该 ID 的 Stat/Open/Write/Remove 访问正确条目。

模型复现结果：真实子文件存在=true；所发 token 路径存在=false；两者字节相等=false；将父 token 继续拼接后新 token 可解码=false。

代码链接：[PathIDOf](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/names.go#L78-L101)、[ReadDir 丢失 resolved parent](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/sftpclient.go#L569-L581)。

#### SFTP-03 — P1：写路径忽略父目录歧义，递归创建还会另建 UTF-8 同名目录

主位置 `internal/sftpclient/sftpclient.go:962-970`、`715-727`；相关 `866-940`、`943-1016`、`Chtimes:643-647`。

##### 场景 A：歧义错误被吞后写错位置

`/tmp/<UTF8中文>` 和 `/tmp/<GBK中文>` 都存在且各有 `config.txt`。`resolveExistingDir('/tmp/中文')` 本来返回 ErrAmbiguousPath；`resolveWritePath` 却捕获**所有**错误，再遍历整段候选并选第一条（UTF-8），随后覆盖该目录里的 config.txt。明确写出的“阻止歧义非幂等操作”没有贯彻到父目录。

`resolveExistingPath` 的父错误也在 `798-837` 丢失后做整段候选；`MkdirAll` 和 `Chtimes` 在解析返回错误时退回原字符串执行。应审计这些统一错误路径。

##### 场景 B：GBK 现有目录下新建两级目录偏离目标

已有 `/tmp/<GBK中文>`，要 `MkdirAll('/tmp/中文/new/deep')`。代码只解析直接父 `/tmp/中文/new`，因为 new 不存在而失败；整段候选均不存在后保留 UTF-8 输入，再交给 backend.MkdirAll，结果创建 `/tmp/<UTF8中文>/new/deep`，原 GBK 中文目录没有 new/deep。目录同步调用 `comparefs/sftp.go:98-102` 会进入此路径。

##### 修复方案

- 对 `ErrAmbiguousPath`、permission、取消、网络错误立即返回；只有经 `errors.Is(err, fs.ErrNotExist)` 确认目标不存在才进入创建流程。
- 将解析器拆成 `ResolveExisting(ref)` 与 `ResolveForCreate(parentRef, name, encoding)`，返回已解析 raw parent、raw leaf、是否存在。写阶段不再猜测现有父目录。
- MkdirAll 从根/已验证 root 按段遍历：现存段精确解析；遇到首个不存在段后根据明确 filename encoding 逐段创建，沿用已解析的 raw prefix；不得丢掉已完成的前缀解析。
- 删除 `MkdirAll`/`Chtimes` 的 `err → original path` 兜底。
- 不使用整段 UTF8/GBK候选成功一次就证明多层路径唯一；混合编码目录应逐段识别冲突。

验收：上述两个场景；父目录无 read 权限但有 x/write；完全无权限；父目录解析途中断连；各失败场景 backend.Write/Rename/Mkdir 调用次数为 0，所有兄弟目录快照保持一致。

模型结果：双编码父目录静默选 UTF8=true；递归新建 UTF8 同名 sibling=true；原 GBK 父目录下创建目标=false。

代码链接：[写解析吞错](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/sftpclient.go#L958-L975)、[MkdirAll 吞错](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/sftpclient.go#L714-L727)。

#### SFTP-04 — P1：流式 replace 覆盖后把原 GBK 文件名变成 UTF-8

主位置 `handlers_ssh_sftp_upload.go:466-500`，组合 `sftpclient.go:740` 和 `990-1016`。

##### 真实调用序列

ASCII 父目录 `/data` 中只有 GBK 字节名 `中文.txt`。用户上传同名文件并选 replace：

1. `Stat('/data/中文.txt')` 找到 GBK 原文件，exists=true。
2. 上传 `/data/.中文.txt.partial`；ASCII 父目录且临时名不存在，创建 UTF-8 临时名。
3. `Rename(finalPath, backupPath)` 把原 GBK 文件移走；新 backup 名在 ASCII 父目录，创建 UTF-8 backup 名。
4. `Rename(partialPath, finalPath)` 时**原目标已经被移走**，resolveWritePath 查不到原 GBK 名；依照 ASCII 父目录创建 UTF-8 `中文.txt`。
5. 删除 backup，返回 200。

结果：内容上传成功，但原 GBK 路径消失，GBK locale 程序按原字节名称打开失败。既有 R6 测试只测直接 UploadFile 覆盖仍存在的原目标，没有测这段备份-替换事务。

##### 修复

在上传落盘前只解析一次最终目标，保存准确 `rawFinalPath`、`rawParentPath`、`rawBaseName`。partial 和 backup 从同一个 raw parent 构造唯一随机名称，commit/rollback 全程操作这些 raw 路径或其不可变 ID，禁止临时移走目标后再次根据显示路径重新解析。replace 目标身份必须在整个操作中固定。

验收：ASCII 父目录/GBK 父目录 × UTF8/GBK文件名的四种组合；成功前后 finalPath 原字节完全一致；注入第二次 Rename 失败时 rollback 也恢复到**原字节**路径。加 handler+Client+fake SFTP 的集成测试，而非仅 mock Client。

模型结果：replace 后原 GBK 路径存在=false，UTF8新路径存在=true，新内容=new。

代码链接：[流式 replace](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_ssh_sftp_upload.go#L463-L506)、[目标不存在时编码推断](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/sftpclient.go#L990-L1016)。

#### SSH-01 — P1/P2：GBK stdin 吞掉编码错误及真实 I/O 错误，命令后半段静默丢失

主位置 `internal/sshclient/shell.go:91-113`。

`newGBKStdinEncoder` 直接使用 `simplifiedchinese.GBK.NewEncoder()`；其默认行为并不是注释声称的“emoji 自动替换为 ?”。本仓库固定 `golang.org/x/text v0.21.0`：官方 `encoding/simplifiedchinese/gbk.go:227-228` 对不可表示 rune 返回 `internal.ErrASCIIReplacement`；`transform/transform.go:244-291` 会先向底层写已转换前缀，再返回该错误及消费数量。

但 wrapper 无论错误类型都返回 `(len(p), nil)`；n != len(p) 也谎报全部消费。一次 Write `echo 你好🌟END\r` 只发送 emoji 之前的 `echo 你好`，`🌟END\r` 整段被丢弃，上层认为成功。底层已断连/写失败时同样被吞。gb18030 参数还被 isGBKEncoding 归到严格 GBK encoder/decoder，四字节字符实际不支持。

修复：

1. 首先停止无条件 `return len(p),nil`，真实底层错误必须原样返回正确 n/err；上层明确显示终端写失败。
2. 对不可编码字符定义可见策略：严格拒绝并提示字符/当前编码，或实现明确且可测试的替代策略并继续处理后续字节；不能只忽略 error 丢整个尾部。若严格拒绝，先对当前完整输入块编码成功后再发给远端，避免先输出半段。
3. 保留跨 Write 的 UTF8 partial 缓冲；分别选择 GBK 与 GB18030 对应 encoder/decoder。
4. 不直接套用“替换 unsupported”为任意控制字节的 helper；替代字符必须清楚定义，终端命令输入不应被无声修改。

验收：跨 1/2/3 字节边界的汉字；emoji/罕见字前后 ASCII 与回车；GB18030 四字节字符往返；writer 返回 `io.ErrClosedPipe`、短写、`n>0+err`；并发写与关闭。既有 shell_gbk_stdin_test 只覆盖普通中文/ASCII/split，HTTP fake SSH 只覆盖你好。

代码链接：[吞掉所有写错](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sshclient/shell.go#L91-L113)、[官方 GBK encoder 失败分支](https://github.com/golang/text/blob/v0.21.0/encoding/simplifiedchinese/gbk.go#L208-L228)、[官方 transform.Writer 返回语义](https://github.com/golang/text/blob/v0.21.0/transform/transform.go#L234-L291)。官方文件已经由 GitHub connector 精确获取并保存 `meta/xtext_*`；web open 被环境 DisabledError 拒绝，不影响已读源码证据。

#### SFTP-05 — P2：逐文件读写反复全量 ReadDir(parent)，普通 ASCII 目录也产生平方级元数据扫描

主位置 `sftpclient.go:798-811`；Stat `620`、Open `547`、Download `480`、Write `974`。

之前 Stat/Open 直接访问目标；现在每个叶子操作先解析 parent，再完整 ReadDir(parent) 搜名字。批量下载 N 个同目录文件，至少列同一个 N 项父目录 N 次，元数据条目扫描约 N²；普通 ASCII 路径也一样。预览 `handlers_ssh_sftp.go:455,463` 连续 Stat+Open，再扫同一 parent 两次。`ListLimited` 也在调用有上限 backend 前走无上限父目录扫描。names.go 注释“一次即中/零额外开销”与实际不符。

修复：列表已知身份的操作直接走 raw ID；纯 ASCII 完整路径可直接调用底层避免歧义枚举；手输非ASCII路径按段解析一次，批量任务内复用目录字节索引并在写入/rename/remove后更新或失效。不要跨连接永久缓存来掩盖并发变化。解析也需 context/取消策略，不让新加 ReadDir 阶段绕过取消。

验收：instrumented backend 对 100、1000、10000 个同目录文件操作，断言父目录枚举次数与目录数相关，不随文件数逐个重复；报告 ReadDir次数、传输字节、p95、取消延迟。不要只测最终内容正确。

源码模型对100个文件执行100次resolveExisting：ReadDir=100，entries_examined=10000。不是性能实测数值，不据此估算真实耗时。

代码链接：[无条件父目录枚举](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/sftpclient.go#L794-L818)。

#### SFTP-06 — P2：权限失败被解析器变成不存在，新权限提示未覆盖 shell fallback

新增解析器 `sftpclient.go:837-842` 对 ASCII候选的 Stat错误不保留，任何错误均构造 os.ErrNotExist；`819-832` 在目标候选 Stat失败后也返回不存在。权限拒绝或断连会被错误显示为不存在，破坏上层精确分类。应记录原错误，仅 os.ErrNotExist 允许候选回退；ambiguity、permission、network、context全部保留类型。

另外，本次上传“权限精准提示”对 realSftpBackend 基本有效（errors.Is(os.ErrPermission) +403），但 shellBackend.UploadStream `1157-1229` 没有捕获 sess.Stderr，cat 重定向的 permission denied 只剩 ExitError/写入断开；shellBackend.Rename/MkdirAll 还把 run 返回 stderr丢弃。handler 的 IsPermissionDenied字符串兜底无从命中。此为受影响功能的**既有未覆盖分支**，不应称本周新引入。建议有上限的 stderr收集、结构化远端阶段错误，再按类型转友好说明。

验收：mock底层ReadDir/Stat分别返回 os.ErrPermission、连接断开、os.ErrNotExist；Client必须保持errors.Is语义。fake SSH关闭SFTP后上传到无写权限目录，返回403及用户/目录/原错误，同时不要把quota/full/no such file误分类成permission。

代码链接：[错误被替换为NotExist](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/sftpclient.go#L819-L842)、[shell上传未捕获stderr](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/sftpclient.go#L1157-L1229)。

#### 受影响上传链路另有既有缺陷，可作为后续补项

不归类本周新增回归，但若要兑现“不会丢原文件”的描述需要处理：

- `handlers_ssh_sftp_upload.go:484` rollback Rename错误被丢弃，`490/492` 却明确告诉用户“原文件已恢复”。应记录restoreErr，只在恢复真正成功时如此回复，否则保留backup并返回恢复路径与不确定状态。
- `395-396` 将任何Stat错误都当exists=false；仅ErrNotExist应视为不存在。
- `426` 临时名不带upload ID，两个并发同名上传共享partial，`430-431`会互相删/截断。使用随机ID+exclusive create。
- reject只在传输之前Stat一次，而 `realSftpBackend.Rename:360` 优先PosixRename允许覆盖，传输期间目标新出现仍可被覆盖。reject提交必须使用不会替换现有目标的原子操作，或明确在不支持的backend拒绝该语义。

#### 功能落实矩阵

| 目标 | 代码结论 | 证据/缺口 |
|---|---|---|
| 非UTF8文件名不会在显示层直接乱码 | 部分落实 | wrapDecoded返回中文；非法GB18030仍可能被replacement且误标gb18030；raw_name JSON不能保字节 |
| 普通UTF8/GBK非冲突路径列目录/Stat/Open/下载 | 主要落实 | Client统一解析；names_test覆盖浅层ASCII父目录和GBK目录 |
| 同目录双编码同名安全拒绝显示路径操作 | Client叶子层落实 | R5测试与ErrAmbiguousPath；父层错误传播不完整 |
| 精准选择双编码同名项 | 未完成 | UI不用id、UTF8兄弟无id、API拒绝token |
| 混合编码多级路径身份不丢失 | 未完成 | ID父路径错误；整段fast path也不证明所有中间段唯一 |
| 已有GBK父目录下新建一个文件 | 主要落实 | R6 UploadFile/UploadStream/CreateExclusive测试 |
| 既有GBK文件直接UploadFile覆盖不另建副本 | 已有代码与测试 | R6 UpdateExistingGBKFileInASCIIDir |
| 实际流式replace保持原GBK名字 | 未完成 | 备份移走后重猜编码，原始路径变化 |
| GBK父目录下递归创建多级新目录 | 未完成 | 缺失中间父目录后丢失GBK前缀 |
| UTF8→GBK终端常见中文输入 | 已有真实代码与WS测试 | 命令中的普通汉字和跨Write缓冲具备实现 |
| 终端不可编码字符不截断、I/O错正确处理 | 未完成 | 吞错误并谎报len(p) |
| GB18030完整双向终端 | 未完成 | gb18030被等价当GBK编码器/解码器 |
| 显式GBK内容保存拒绝emoji、CRLF/BOM保留 | 本次变更落实 | textcodec分开GBK/GB18030，新增T068；auto仍将非UTF8默认为GB18030，不能自动保证原文件是严格GBK |
| 上传权限错误给用户与目录建议 | SFTP分支主要落实 | shell fallback缺stderr；解析层又把部分权限改成不存在 |
| 大目录操作保持基线性能 | 有回归 | 同一父目录重复全量枚举；测试没有计数断言 |
| 上传取消页面卸载beacon | 现有链路成立，未另报缺陷 | ssh.js cancelAllTabsUploads→cancelTabUploads:2048-2057实际sendBeacon+abort；/data也合并HTTP断开ctx |

#### 推荐开发顺序

1. 先修RawPath/PathRef与错误传播（SFTP-02/03），不允许歧义写入。
2. 接通所有前后端身份与选中键（SFTP-01），同时递归下载fullRemote改用raw身份，显示zipName另算。
3. 固定上传事务的raw target/partial/backup身份（SFTP-04），加入真实handler用例。
4. 修终端编码器错误语义及gb18030分流（SSH-01）。
5. 用已知身份访问消除重复ReadDir，补权限/性能验收（SFTP-05/06）。

合并门槛：至少在 Linux临时字节文件名SFTP服务跑API闭环；目标旧AIX/GBK服务器额外跑终端locale、shell fallback和权限矩阵。只通过Client mock单测不构成这些功能完成。

### 9.F. 升级恢复、WSDL、代码生成与 WAS

审核 HEAD：`38df3aa604817955f470eaaaa0eb4c29124972c4`；BASE：`dc75701583fe78ec8b8bcfa706d77c68a7ba6011`。已读上述范围全部 compare diff、新增测试和相关 HEAD 调用链。环境没有 Go 编译器，Go 问题为源码控制流确认；未宣称实际跑过 Go 测试。另实际执行了 HEAD 原始 JavaScript 上传 handler 的 mock transport 回归，证实同名 XSD 在前端已被覆盖。`repo/` 与独立只读下载目录 `read_upgrade/` 文件均来自相同 HEAD，行号指 GitHub 原文件。

归因说明：UPG-01/02 是新增恢复 journal 与旧锁/旧手动恢复入口的集成缺口；UPG-03、WSDL-01/03、URI helper、file:// 边界来自新增路径。WSDL-02 的后端路径规则是本次新增问题，前端 basename 覆盖属于旧路径未随本次“同名 XSD”修复完成。WSDL-04 的上传项目→官方工具缺 bundle 也是现有功能仍未闭环，不应描述成这几天首次引入的回归。

#### 最值得优先修复的问题

##### UPG-01 / P1：真实崩溃后的残留锁使自动恢复入口不可达，新增测试手动删锁掩盖问题

证据：

- `internal/upgrade/upgrade.go:114–120`：`Run` 必须先成功 `acquireLock`，才调用 `recoverIncompleteUpgrade`。
- `internal/upgrade/files.go:133–140`：锁是 `os.O_CREATE|os.O_EXCL` 创建的普通文件；已有锁直接报错，明确不会接管。
- `internal/upgrade/files.go:147–153`：释放锁只在返回的 defer 清理函数中发生。
- `internal/upgrade/journal_test.go:603–604`：真实 kill 子进程测试在父进程重启前手工 `os.Remove(filepath.Join(dataDir, lockFileName))`。
- `internal/upgrade/journal.go:338–344`：公开 `Recover` 同样先获取这个锁。

真实调用链：启动 → Run → 写 `applying` journal → A 文件提交 → kill / 断电 → 普通锁文件残留 → 下次启动在 acquireLock 返回 `another upgrade may be running` → 完全没有执行恢复。不是最终混合数据被正常打开，而是用户永远无法重新启动，除非了解内部细节手动删锁。

实现方案：把进程生命周期互斥改成 OS advisory lock（Unix `flock` / Windows `LockFileEx` 或等效内核锁），句柄持有至整个 Run/Recover/RestoreSnapshot 结束；进程死亡由 OS 释放。锁文件里的 pid/token 可作诊断，但不能作唯一持锁判据。不要按时间或只按 pid 探活删除锁，避免 PID 重用与并发接管。三个入口共用同一个锁实现。

验收：原 `TestSubprocessCrashAndRecovery` 删除手工删锁这一步；在 prepared、A 写完、manifest 写完三个位置硬杀子进程并直接调用正式入口；必须恢复成功且活着的另一个进程仍被拒绝。保留同时启动竞争测试。

固定来源：[Run 顺序](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/upgrade/upgrade.go#L114-L120)、[锁实现](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/upgrade/files.go#L125-L153)、[测试绕过](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/upgrade/journal_test.go#L603-L604)。

##### UPG-02 / P1：执行官方恢复命令成功后，失败 journal 仍会永久拦住下一次启动

证据：`journal.go:238–243` 遇到 `PhaseRecoveryFailed` 无条件直接报错。`restore.go:19–101` 的 `RestoreSnapshot` 只恢复 snapshot 资产，根本没有处理 journal；journal 也不在 `DefaultAssets` 中。`main.go:129–135` 提供 `--restore-upgrade`，恢复成功后告诉用户重新启动。

触发：一次恢复由于外部修改或坏快照失败 → 状态落成 recovery_failed → 用户选择有效快照运行 `--restore-upgrade` → 文件已恢复且命令显示成功 → 正常启动还是立即读到旧 failure，永远失败。即使手工把业务数据修回旧摘要，该 phase 也不会重试。

实现方案：把手工 RestoreSnapshot 纳入统一恢复状态机。获得同一锁后保存原 journal 作为审计备份；成功恢复且验证文件摘要后，原子提交 `restored/complete`（或新恢复事务 ID 并明确关联原事务）。恢复失败必须保留原失败状态与 safety snapshot。不能一开始直接删除 journal。为 `RecoveryFailed` 增加受控的“验证后重试 / 指定快照恢复”入口。

验收：构造外部修改触发 recovery_failed → `RestoreSnapshot` 有效快照 → 不删任何内部文件直接 Run；应成功。故意使恢复失败时 journal 不得被标成 complete。

固定来源：[失败状态闸门](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/upgrade/journal.go#L238-L243)、[手动恢复实现](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/upgrade/restore.go#L86-L101)、[用户入口](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/main.go#L129-L135)。

##### UPG-03 / P2：空文件被 journal 当作“不存在”，恢复后又因文件存在判失败

证据：`upgrade.go:264–275` 通过 `len(item.oldRaw)>0` 推导 OldSHA 和 Existed；但 `upgrade.go:464–466` 的 `ValidateJSON` 明确允许空文件，`assets.go:72–75` 也将空 JSON 文件视作旧版无数据。`snapshot.go:59–81` 则用真实 stat 记录 Existed=true，备份空文件。恢复完成后 `journal.go:219–221` 根据错误的 Existed=false 要求文件不存在。

触发：至少一个合法空的 JSON 资产，加上另一个需要迁移的非空资产；进入 applying 后中断。journal 将空文件记成不存在，snapshot 实际保存了它。snapshot 正确恢复空文件后，verifyAssetsMatchOld 报 `should not exist after rollback`，落入 recovery_failed。零字节不是罕见非法输入，代码现有校验契约明确支持。

相关同类问题：`upgrade.go:128–130` 为了重建损坏 manifest 把解析语义 `exists` 改为 false；`290` 又直接用它表示磁盘是否存在。旧损坏 manifest 实际仍在磁盘，prepared/applying 崩溃恢复可能被误判成“不该存在的外部新文件”。

实现方案：`preparedAsset` 增加 `existed bool`，仅由 lstat/readManagedFile 的 ErrNotExist 决定；只要 existed 就计算 SHA256，空内容也有合法 hash；分离 manifest 的 `fileExisted` 和 `validManifest` 两个字段。journal 与 snapshot 从同一份稳定的 prepared snapshot 元数据生成，避免双重推导不一致。

验收：0 字节、空白、缺失三个状态分别做 prepared/applying 中断与恢复；空文件应仍为空，缺失应仍缺失。损坏 manifest 的接管流程同样注入中断验证。

固定来源：[错误存在性](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/upgrade/upgrade.go#L262-L295)、[恢复验证](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/upgrade/journal.go#L209-L225)。

##### WSDL-01 / P1：跨源直接 import 仍会把 WSDL 凭据发送给另一台服务

证据：`handlers_webservice.go:190–194` 将导入 WSDL 时的全部 `Headers` 传入 `SchemaResolverConfig.AuthHeaders`；`schema_resolver.go:498–507` 为每一个依赖 URL 无条件设置这些头。仅 `makeSafeHTTPClient` 的 CheckRedirect 删除 Authorization / Cookie（214–233 一带），对“没有 302、直接引用另一个 origin”的 import 完全不起作用。

最小场景：用户带 `Authorization: Bearer TOKEN` 导入 A 的 WSDL；该 WSDL 或它导入的 XSD 中存在 `<xsd:import schemaLocation="http://B/schema.xsd" .../>`；B 收到 TOKEN。现有 T051 只测 A→302→B，未测 A 的文档直接引用 B。AuthHeaders 接口也允许 API key 等自定义秘密头；目前重定向只删三个固定名称，也不能履行“全部认证头仅用于授权来源”的承诺。

实现方案：将凭据绑定明确的授权 origin（规范化 scheme、host、effective port），构造每一次网络请求时先判断目标是否在该 credential scope 内；跨源默认不复制 AuthHeaders，同源继承；需要跨源鉴权用按 origin 配置映射。redirect policy 必须对当前目标重新施加同一策略，不能只比较上一跳。根 WSDL 和 XSD 共用此策略。来源 URL 不等于凭据可授权给它的全部依赖。

验收：同源直接 import 保留头，A→B 直接 import 不带头，A→B→B 和 A→B→A redirect、HTTP↔HTTPS、端口变化、自定义认证头均覆盖；检查接收端实际头。将最小复现接到 `handleWSDLImportURL`，不要仅测底层 client。

固定来源：[实际入口传递凭据](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_webservice.go#L190-L194)、[逐个 import 无条件发头](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/webservice/schema_resolver.go#L498-L507)。

##### WSDL-02 / P1：附件路径并没有真正按 canonical URI 找，嵌套同名文件会静默选错

前端还有更早的断点：`web/pages/webservice.js:987–995` 使用 `fileContents[f.name]=text`，`1011–1022` 同样按 basename 构造附件表，从不保存 `webkitRelativePath`。因此 a/common.xsd 与 b/common.xsd 一起进入 FileList 时，后者在发请求以前就覆盖前者，后端再正确也救不回来。实际执行原函数的复现见 `notes/repro_wsdl_upload.cjs`：传入带不同 webkitRelativePath 的两个 common.xsd，捕获到只发送一个 key=`common.xsd`、值为 urn:B。断言预期附件数 2，实际 1，脚本退出码 1。这是源码函数执行测试，未通过浏览器文件选择器。

证据：`lookupAttachment(canonicalURI,parentURI,rawRef)` 接收 canonicalURI 却没有使用；`schema_resolver.go:541–543` 首先拿 rawRef 直接从根附件表查找，之后 547–552 才尝试父目录。`handlers_webservice.go:227–231` 实际上传入口调用 ParseWSDL 时仅传 Attachments，未提供 memory base URI；下级 parentURI 可能是无 scheme 的 `a/schema.xsd`，使第二步整个跳过。

明确复现数据：附件里同时有 `common.xsd`（内容含 wrongField）、`a/common.xsd`（内容含 rightField）、`a/schema.xsd`（include `common.xsd`）；两个 common 可声明同一个 targetNamespace，均为合法 schema。主 WSDL 导入 `a/schema.xsd`。处理子引用时先命中根 `common.xsd`，根本不会访问 `a/common.xsd`，并将错误内容登记在计算出的 a/common canonical URI 下。依赖状态仍为 loaded，但生成的参数树是错的。即使传了 `memory:///service.wsdl`，查找优先级仍会选错。

实现方案：前端支持目录/ZIP 上传并保存完整 relativePath；普通多文件选择无法提供完整路径时，至少检测重复 basename 并显式拒绝，禁止覆盖。每次上传建 `memory:///bundle/<id>/service.wsdl` 根，所有附件预先规范化为相对于 bundle 根的 canonical URI；只按解析后 URI 精确查内容。保留 basename 回退也只能在不存在 canonical match 且全 bundle 唯一时应用，且标注兼容回退；不能 rawRef 根匹配抢在 parent-relative 解析前。

验收：上述 3 附件通过实际 HTTP 导入 + Generate builtin 全链路检查 rightField 存在、wrongField 不存在，新增无根 common 但两个子目录同名 common 的情况；在 memory / file / HTTP 三类基准下测试 a→../b。

固定来源：[前端先覆盖同名文件](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/webservice.js#L987-L1022)、[附件匹配代码](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/webservice/schema_resolver.go#L537-L567)、[上传入口](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_webservice.go#L227-L239)。

##### WSDL-03 / P2：新增 wsdl:import 被错误送给 XSD 解码器，导入的业务 operation 不能加载

证据：`wsdl.go:491–494` 把 `defs.Imports` 转为 `xsdImport`；resolver 统一调用 `parseExternalXSD`（378）；该函数解码到根名必须为 `schema` 的 `xsdSchema`（wsdl.go:55–56、990–998）。标准的被导入 WSDL 根是 `definitions`，因此不能解析，更没有把它的 message / portType / binding 合并到解析索引。

触发：主 service.wsdl 放 service/binding 或只有 import，具体 message、portType 在 contract.wsdl；此次代码看似开始支持 wsdl:import，实际会得到 XSD XML 解析失败，业务操作消失。builtin 在没有 operation 时仅警告并生成通用 raw 调用（builtin.go:55–61），仍可回 `OK=true`，不代表合同客户端功能实现。

实现方案：依赖图节点增加 WSDL/XSD 类型，边区分 wsdl:import / xsd:import / include；WSDL 文档解析为 Definitions，把 messages/portTypes/bindings/services 按 QName 索引，读取其内联 schema，再递归遍历它的 WSDL/XSD 依赖。不能简单把多个 WSDL 的 local-name map 拼在一起。

验收：外置 contract WSDL 的 operation 可展开；多 WSDL 同 local name 不冲突；WSDL import 循环终止；缺失必需 contract 时显式结果 incomplete 或阻止“完整客户端生成”，不能仅显示成功。

固定来源：[把 WSDL 当 XSD](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/webservice/wsdl.go#L489-L499)、[schema 根约束](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/webservice/wsdl.go#L55-L63)。

##### WSDL-04 / P2：上传项目的 XSD 图没有接到官方生成器输入，解析成功不代表 wsimport / XFire 能生成

证据：`types.go` 的 WSDLProject 只新存 Dependencies 的 URI / namespace / status / size，没有依赖正文或可物化的 bundle；`Generate/ResolveWSDL` 选已导入项目只得到 `Project`, `RawWSDL`, `SourceURL`（generate.go:26–37）。官方 `wsdlArgForTool`（tool.go:133–160）无 file/URL 时仅把 RawWSDL 写到一个随机临时 .wsdl；从未写入上传的 XSD，也没有改写 import location。

触发：在 WebService 页上传 service.wsdl + common.xsd，解析参数正常并保存项目；在代码生成选该已导入项目、官方模式。临时目录里只有 WSDL，工具相对引用 common.xsd 查找失败。新的 dependency 清单不能替代依赖文件。URL 项目有鉴权时官方工具也只收到裸 URL，没有此解析器的 credential scope / 内容快照。

实现方案：让 resolver 产出可持久化 `ResolvedDocumentBundle`：节点内容（或受控 blob ID）、内容哈希、document kind、canonical URI、原始引用；WSDLProject 引用 bundle。为工具在独立输入目录完整物化图，重写 import/include location 为本地相对地址并保持 QName/namespace；生成器只消费该输入快照，输出另设 staging 子目录。敏感 headers 不直接写入 project JSON，凭据使用服务端引用。builtin、tool 和 preview 的 dependency 状态来自同一份解析结果。

验收：实际上传并保存后重启 Store，再走官方模式生成，输入目录必须有全部依赖；关闭外网仍可根据上传 bundle 生成；缺文件时准确指出 URI。现有 `TestGenerate_MultiDirSameNameXSD_PreservesBothBeans` 只测本地文件 + builtin，不覆盖这一条业务流程。

固定来源：[项目解析](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/wscodegen/generate.go#L24-L37)、[官方只写主 WSDL](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/wscodegen/tool.go#L139-L169)、[依赖存储结构](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/webservice/types.go#L65-L82)。

#### 其它已确认的边界缺口（建议放详细报告）

##### WSDL-05 / P2：本地文件 URI 直接字符串拼接，路径里 # / ? / % 会破坏依赖解析

`schema_resolver.go:39–62` 的 PathToFileURI 将路径直接拼成 file://，不做 URL 编码；73 一带 FileURIToPath 使用 url.Parse。目录 `/projects/release#1/service.wsdl` 会把 `#1/service.wsdl` 当 fragment，后续文件路径丢失。旧 wscodegen `fileURI`（generate.go:255–272）已经用 `url.URL{Scheme,Path}.String()` 正确编码，但 ResolveWSDL 的新流程用的是另一个不安全实现（48）。统一用结构化 URL 构造与 ResolveReference，覆盖中文、空格、#、?、%、Windows 盘符和 UNC 的 round trip。

##### WSDL-06 / P2：代码生成取消没有传到新 XSD 解析器，生成开始之前仍可长时间后台取依赖

`GenerateContext` 在 112 调用无 ctx 的 ResolveWSDL；本地与 URL+text 分支构造 resolver config（49–52、74）均不设 Context，于是 NewSchemaResolver 默认 Background（schema_resolver.go:172–175）。正式 `handlers_wscodegen.go:104` 虽传 r.Context，也只能到之后 runTool。上传导入 handler 也没传请求 ctx。修复为 ResolveWSDLContext/带 context 参数并打通每个 config；directory lock acquire（generate.go:372–390）也应可响应 ctx，否则同目录等待不可取消。验收使用阻塞 httptest XSD 请求，取消 handler 后远端 r.Context 必须被取消、解析迅速返回、不启动官方工具、不写目标目录。

##### WSDL-07 / P2：chameleon include 缺少上下文身份，根内联 schema 与共享 include 场景仍不正确

`wsdl.go:497–500` 收集内联 schema 的 import/include 时丢弃其 targetNamespace；resolver 根队列 parentNS 写死空（246–250），所以内联 schema include 的无 targetNamespace 文件不能继承父 namespace。另 `visited[canonicalURI]`（322、405）未加 effective namespace，同一个无 namespace common.xsd 被 urn:A 与 urn:B 两个 schema include，只注册第一次命名空间。应区分 URI 文档缓存和 `(URI,effectiveNamespace)` 展开身份，边保存 parent namespace 和 include/import 种类。验收两个命名空间共用一个 chameleon schema，各自类型均生成且 QName 不串。

##### UPG-04 / P2：自动恢复的 snapshot 元数据没有与 journal/白名单绑定，弱于已有手动恢复

`journal.go:268` 只校验 journal.Assets 的 Path；279 后单独从 j.SnapshotDir 读 snapshot.json；`snapshot.go:124–155` 不验证 snapshot dir 在受管目录、不验证 entry.Name/Path 与允许资产一致、不拒绝 backup 路径穿越、不要求 SHA 非空；158–179 直接写或删除 snapshot entry.Path。这不是普通原始备份数据损坏（已有 SHA 测试），而是快照元数据被误改/替换时，白名单无法约束最终修改路径。比如在 snapshot.json 加一个 Existed=false 的陌生路径，restore 会 os.Remove 它，journal 校验毫无感知。已有 `RestoreSnapshot` 在 restore.go:31–75 做了更严格的根目录、Name/Path、basename 和必有 SHA 检查，新自动恢复没有复用。

修复统一 `loadValidatedSnapshot(j, allowed)`：只读受管根、不跟随符号链接、asset name/path/existed/old SHA 与 journal 一一对应（拒绝重复/缺项/新增）；backup basename 和 mandatory hash 校验后再允许任何修改。验收篡改 Path、增加删除项、backup=../、空 SHA、snapshot 根逃逸全部先拒绝且所有原文件保持不变。

##### WSDL-08 / P2：远程文档可触发未授权本机文件依赖读取

这里不是禁止内网 HTTP。`handlers_webservice.go:12–15` 的原有安全契约明确写“文件导入只接受请求体里的 WSDL 文本，不直接读服务器磁盘任意路径”。新 resolver 的 `file://` 分支在 schema_resolver.go:460–471 只有 AllowedRootDir 非空才检查；HTTP 根无法自动生成 allowedRoot，于是来自 URL 导入的 `<xsd:import schemaLocation="file:///..."/>` 可直接走 473/481 的本机 stat/read。实际 URL handler 只传 BaseURI/Context/AuthHeaders，不设置允许本机读取的授权信息。上传+附件触发 resolver 也可如此。范围不能夸大为“任意文件内容全部返回”：非 XML 只会解析报错；合法 XSD/某些 XML 内容会被读取解析，错误还可暴露路径存在性；特殊文件可能阻塞读取，因为这里也没有 IsRegular 检查。

若产品明确想允许管理员导入文件系统 WSDL，权限应显式来自用户选的本地路径/授权 root：remote/memory document 默认不能转到 file；file document 要 `EvalSymlinks` + root 约束并拒绝非普通文件。当前 CheckPathWithinRoot（135–145）只做词法 Rel，对 root 内符号链接跳到外层无效。验收 HTTP→file 与 memory→file 默认拒绝，显式本地 root 中相对 XSD 可用，symlink 外跳拒绝。

#### 功能落实矩阵

| 本次提交承诺 | HEAD 实现判断 | 依据 / 尚缺内容 |
| --- | --- | --- |
| journal prepared/applying/manifest_committed/complete | 状态与文件存在 | Run 已接入 store 之前；真实硬崩溃因普通文件锁未闭环（UPG-01） |
| 外部修改不被自动回滚覆盖 | 部分完成 | 恢复入口会比较 old/new SHA 并拒绝；手动恢复无法收尾 journal（UPG-02），空文件身份错误（UPG-03） |
| 坏备份检测与保留现场 | 部分完成 | 正文 SHA 检测有；snapshot 元数据路径/清单绑定不足（UPG-04） |
| 本地 a/common.xsd 与 b/common.xsd | 正常指定路径基本接通 builtin | 原始同目录 basename 覆盖被修；Generate 测试走到了实际 bean 输出 |
| 上传附件多层路径/同名文件 | 未完成 | 前端先按 f.name 覆盖；后端 rawRef 根匹配优先 + 上传入口缺 memory base（WSDL-02） |
| XSD 递归、循环、字节与深度限制 | 基本实现，尚有边界 | 限制与 warnings 已加；失败节点不计 visited/maxSchemas，chameleon 身份和 context 有缺口 |
| WSDL import | 未实现成可用功能 | 节点被当成 XSD 解码，无 Definitions 合并（WSDL-03） |
| 跨源凭据剥离 | 部分完成 | 现有测试仅检查 302；直接 import 漏凭据（WSDL-01） |
| 带 Headers 的 URL 导入 | 仅 API 有新增能力 | WebService 页 openImportURLDialog（898–904）只收集 URL/name，未提供 Headers 输入；API 功能尚未接到用户界面 |
| 依赖清单接入生成结果 | builtin 已接入；tool 输入未闭环 | 已导入上传项目到官方工具缺 XSD bundle（WSDL-04） |
| 官方工具失败不污染目标 | 正常失败路径已实现 | 先临时工作目录生成，校验后共用 writeFilesLocked 发布 |
| 输出校验与无覆盖发布 | 大部分实现 | symlink/regular file/1000 文件/10MB 单文件/50MB 总量检查；EEXIST + O_EXCL 修复有效；批量发布不是崩溃恢复事务 |
| 同目录互斥与最多 4 工具并发 | 已实现到单进程 | 精确目录锁 + semaphore；锁等待无 ctx，父子输出目录不共享锁，属于后续边界 |
| Windows 工具进程树取消 | 源码机制已接入，未做本机验证 | suspended create → Job Object → resume，timeout/cancel 调 KillTree；无 Windows/Go runtime，不声称实测 |
| Unix 工具进程树取消 | 普通同组后代已实现 | Setpgid + kill(-pid)；主动 setsid 逃逸并不属于同组，不能宣称任何子孙绝对可清理 |
| WAS 产物成功而备份清理失败 | 此次修改已完整接入三入口 | Build / Extract / PackageExtracted 都识别 BackupCleanupWarning 并返回 OK+warnings；未发现此次改动本身新的确定缺陷 |
| WAS 发布失败回滚、保留用户文件 | 正常错误路径已有实现与新故障注入用例 | 查看了 staged publish、named publish、rollback 路径；没有实际执行 Go 故障注入测试 |

#### 测试质量补充

- T056 hard kill 子进程测试删锁后才验证重启，是 UPG-01 的关键漏测。
- T051 只测 Authorization 且只测单跳跨源 redirect，不能证明 import credential scope。
- T048 / codegen 同名 XSD 测试用本地文件且两个类名为 ItemA/ItemB，未覆盖上传相对路径、同名元素跨 namespace 或官方工具输入。
- T053 ProcessTreeTermination 只等主 proc.Wait，没有记录并验证孙进程 PID；可以证明主进程快速退出，不能独立证明没有孤儿。应让子孙记录 PID 并在取消后逐个确认退出。
- T052 在 Windows 分支把 batch 文本写入 `wsimport.exe`/`java.exe` 名称，进程启动可能直接因无效 exe 失败；测试只要求任意 err 非 nil，可能根本没执行“先写一个文件再失败”的阶段。需要编译小型测试 helper 或显式 cmd.exe /c 脚本，并断言 staging 中的 sentinel 确实写过再退出。

以上测试质量点是证据强度限制，不把缺少测试本身当运行时 bug。

### 9.G. 测试可信度与发布链

审核固定 HEAD：38df3aa604817955f470eaaaa0eb4c29124972c4；范围 2026-09-14 至 2026-09-21，main 共 32 commits，累计涉及136个路径，累计变化与逐提交路径并集一致。对各commit详情/patch已逐条获取至meta/commits；不以旧审核文档作为发现证据。

#### QA-01 / P2：新增真实数据库测试未接入统一入口，且部分通过条件可误报
- package.json:10-15，scripts/run-tests.js:22-31,105-106：unit白名单8脚本；E2E始终运行tests/e2e/index.js。
- tests/e2e/index.js:120-147固定模块列表不包含 tests/e2e/test-database-deep.js 或 test-database-backup-real.js；文件存在不等于 npm test:all 会执行。
- test-database-backup-real.js:29-34只计request，不判response.ok，更不GET回读；即每次请求都是HTTP500，计数仍符合“空闲0/编辑1”，不能证明保存成功。
- test-database-deep.js:218-255 等待UPDATE最多8秒但未断言非空；空SQL可通过所有后续引号/正则检查并打印成功。
- index.js:149-171模块加载失败只console.error，之后继续跑，不增加failure计数。是原有残留，当前统一入口没有修正。
- 建议：新增两个脚本改export register加入统一suite；必需suite缺依赖/模块加载失败必须退出非0；明确SKIP；备份断网/500/乱序、响应后回读、刷新恢复内容断言；SQL非空+目标表+完整唯一键+LOB全值校验。
- 验收：统一命令列出具体case；让备份500/剪贴板空/模块不存在应失败。网络请求数正确但保存失败不得PASS。

#### QA-02 / P2：验收矩阵把窄范围测试标为完整场景通过
- docs/gpt/KAIRO_ACCEPTANCE_TEST_MATRIX.md:38 T028声明Close/Rename故障注入通过，映射 TestWriteXLSXIsZipWithSheet；internal/dbconsole/export_test.go:272起只检查bytes.Buffer产生zip条目，没注入Close/Rename。
- internal/httpserver/compare_backend_p0_test.go:468-478截断目录测试用不存在synthetic FS；忽略err/truncatedRes，不验证目标不变。
- internal/upgrade/journal_test.go:603-604手动删除被杀进程留下的锁，绕开生产重启问题。
- 性能T074（矩阵84行）将模拟行/缓存benchmark和简单ctx取消、多goroutine测试标为固定机器真实UI/数据库性能验收，没有相应原始采样。
- 建议结果字段拆 unit_verified/integration_verified/browser_verified/live_verified；测试有明确输入、故障注入点、产物断言、原始日志，严格不将mock覆盖替代live。
- 本次真实执行：原有8个Node脚本最终均通过（首轮webservice缺审核快照依赖，补全依赖后单独重跑通过；此缺文件是取证环境问题，不是仓库缺陷）。check:version通过。本次审查针对HEAD实际函数做定向VM复现，与生产浏览器/Go真库实验明确区分。Go工具链/现场库未提供，未声称go test、Oracle、SFTP、Windows真运行通过。
- GitHub read-only API：actions/runs返回0，HEAD check-runs返回0；不能替代开发者本机测试，也不声称开发者从未测试。

#### BUILD-01 / P2：构建与打包仍使用不同版本来源
- scripts/build_windows_amd64.sh:19-29已只读参数或VERSION；scripts/package_windows.sh:19-28仍扫描最近5条commit message，发现v0.19-dev就强改VER。
- scripts/check-version.js:43-48只检查build脚本，因此check:version通过仍没覆盖package脚本。
- 触发条件：VERSION=v0.20且最近5条任意message包含v0.19-dev；构建dist/kairo-v0.20，打包却找dist/kairo-v0.19-dev-<sha>，失败或打旧残留目录。当前最近5条无该字符串，属于条件性剩余缺陷，不说当前所有构建必失败。
- 修改：两脚本共用read-version脚本/函数，显式版本最高、否则VERSION；门禁扫描两个脚本，用临时目录+stub git验证不得受message影响。
- 相关commit f96fba8a76153d1e228cbc3f65b18804734afac9（功能声称唯一真相源），package旧分支后续仍在HEAD。

#### BUILD-02 / P3：commit-msg 并未强制存在模型版本

- .githooks/commit-msg:48-73只是泛称黑名单，不校验版本；真实运行输入 Model: Gemini Flash（无版本）返回0。
- 建议明确接受的模型版本格式及Human例外，并在CI执行同一校验。本地hook安装状态不能通过把文件提交入库自动保证；应有明确安装入口。校验格式不能证明模型自报版本真实，报告不作这种承诺。
- 打包分支也已做受控shell复现：VERSION=v0.20、有dist/kairo-v0.20、stub git log含v0.19-dev，真实package脚本退出1并去找dist/kairo-v0.19-dev-abc1234。仅复现该条件，不表示当前5条提交满足条件。
- 新E2E的UPDATE校验块已直接抽取至Node VM运行：updateSQL空串没有异常，仍打印校验通过。

#### 已排除的误报
- config.yaml加入开发token，但 main.go:89 经 BootstrapDistributionConfig；internal/config/document.go:225-232清KairoInternalToken和InternalEndpoints。不能声称正常首次启动发行版因此直接绕过license。
- 空compare_allowed_roots放行是f96fba8明确的产品策略变更；不能仅用安全偏好当BUG。管理员写权限/非空roots路径约束继续存在。
- Go未安装不属于项目缺陷；初次本地快照缺webservice.js不属于项目缺文件。

#### 测试与发布链源码索引

[package.json](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/package.json#L10)；[scripts/run-tests.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/scripts/run-tests.js#L22)；[tests/e2e/index.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/tests/e2e/index.js#L120)；[test-database-backup-real.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/tests/e2e/test-database-backup-real.js#L29)；[test-database-deep.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/tests/e2e/test-database-deep.js#L218)；[验收矩阵 T028/T074](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/docs/gpt/KAIRO_ACCEPTANCE_TEST_MATRIX.md#L38)；[XLSX 测试](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/export_test.go#L272)；[T060 截断目录测试](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/compare_backend_p0_test.go#L468)；[强杀后手动删除锁](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/upgrade/journal_test.go#L603)；[构建版本来源](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/scripts/build_windows_amd64.sh#L19)；[打包版本来源](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/scripts/package_windows.sh#L19)；[版本检查](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/scripts/check-version.js#L43)；[commit hook](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/.githooks/commit-msg#L48)；[发行配置清理](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/config/document.go#L225)。

## 10. 本次定向实验结果摘录

这些结果用于补强具体控制流判断。VM 运行实际 JavaScript 函数但替换外围依赖；规则镜像只验证对应决策的后果。请勿把本节当成完整服务、真实数据库或浏览器的运行证明。Compare 的内存变化只有在之后成功保存时才落到磁盘，实验没有执行真实保存 API。

### 10.1 数据库前端原函数执行

```text
CONFIRMED restore destroys active SQL: ["","SELECT second_draft FROM T"]
CONFIRMED close overwrites surviving tab SQL and rows: SELECT A FROM A [["A-result"]]
CONFIRMED network backup failure is considered synced; repeated idle checks request count=1
CONFIRMED HTTP 500 backup failure is considered synced; repeated idle checks request count=1
CONFIRMED bulk close removes tab despite failed rollback; server transaction unresolved
CONFIRMED getHistory bridge missing after module load; opening history marks still-running query success
CONFIRMED edit A→B→A leaves B pending; next remote backup contains stale B
CONFIRMED EMPTY_BLOB preview loops automatically: 6 requests / 6 modal opens before synthetic stop
CONFIRMED failed SELECT clears outcomeUnknown; failure status=执行失败
CONFIRMED right-click row export guesses nonunique WHERE: UPDATE PEOPLE SET NAME = 'Alice' WHERE NAME = 'Alice';
CONFIRMED right-click row export converts lazy CLOB to empty string: INSERT INTO DOCS (ID, BODY) VALUES (1, '');
CONFIRMED background query from source-A recorded under source-B after tab/source switch
```

上述 12 个输出逐一对应 DBF 详审中的输入、调用链和验收步骤。回滚失败保留状态、未知提交保持屏障、备份回读内容等应作为修复后的明确断言。

### 10.2 Tab 原函数执行

```json
[
  {
    "case": "websphere completion clears active files download",
    "otherStreamClosed": 1,
    "filesRegistry": null
  },
  {
    "case": "late response from closed instance overwrites reopened tab registry",
    "storedId": "old-late-tail",
    "newStreamCloseCalls": 0
  },
  {
    "case": "registerTail after scope disposal unregisters but never closes new stream",
    "streamCloseCalls": 0
  },
  {
    "case": "websphere subroute lost on reuse",
    "hash": "#/websphere",
    "routeState": {}
  }
]
```

### 10.3 Compare 原函数与构造输入

```json
{
  "head": "38df3aa604817955f470eaaaa0eb4c29124972c4",
  "method": "exact HEAD functions extracted into Node VM; DOM/network not simulated beyond noted stubs",
  "results": [
    {
      "case": "ignore_blank_merge_deletes_unselected_blanks",
      "expected": "header\n\nnew\n\nfooter\n",
      "actual": "header\nnew\nfooter"
    },
    {
      "case": "second_merge_on_stale_rows_reverts_first_merge",
      "afterFirst": "A\nseparator\nb",
      "expectedAfterSecond": "A\nseparator\nB",
      "actualAfterSecond": "a\nseparator\nB"
    },
    {
      "case": "undo_swap_keeps_swapped_write_destinations",
      "left": {
        "path": "/right.txt",
        "value": "L"
      },
      "right": {
        "path": "/left.txt",
        "value": "R"
      },
      "impact": "saving both files exchanges their contents"
    },
    {
      "case": "one_ctrl_z_undoes_two_editor_history_entries",
      "before": "B",
      "afterTextarea": "A",
      "afterBubbling": ""
    },
    {
      "case": "background_compare_intercepts_other_contenteditable_ctrl_z",
      "globalUndoCount": 1
    },
    {
      "case": "shift_select_model_and_checkbox_disagree",
      "model": [
        0,
        1,
        2
      ],
      "renderedCheckboxes": [
        true,
        false,
        true
      ]
    }
  ]
}
```

### 10.4 WSDL 上传原函数执行

输入文件：service.wsdl、a/common.xsd（urn:A）、b/common.xsd（urn:B），后两项具有不同 webkitRelativePath。原 handleUploadFile 发送的附件对象只剩一个 common.xsd，内容为 urn:B。断言“应发送 2 个附件”失败，说明缺陷已重现，而非产品测试通过。未运行真实浏览器文件选择器。

### 10.5 后端规则夹具的边界

- SQLite 夹具加受限规则镜像：派生表结果把 ID=2 的余额从 200 改为 100。LOB 用例点击第二条记录，前六个特征相同而第七个不同；丢弃第七特征后匹配两条，首行限制返回第一条正文。这不是 Go/Oracle 执行。
- Python 路径控制流镜像使用临时 POSIX 原始名字节文件：验证显示父目录生成错误 path_id、递归创建偏到 UTF-8 同名目录、replace 后原 GBK 名字消失而 UTF-8 名字出现。这不是 SFTP 协议实测。
- 100 个同目录文件导致 100 次目录扫描、合计处理 10,000 个条目是调用次数模型；不能直接换算成真实延迟或吞吐量。

### 10.6 发布与弱断言的实际执行

```json
{
  "commit_hook_versionless_model": {
    "model": "Gemini Flash",
    "actual_exit_code": 0,
    "expected_exit_code": 1
  },
  "packaging_version_with_controlled_git_output": {
    "version_file": "v0.20",
    "built_directory": "dist/kairo-v0.20",
    "actual_exit_code": 1,
    "stderr": "错误：找不到 dist/kairo-v0.19-dev-abc1234\n请先跑 ./scripts/build_windows_amd64.sh v0.19-dev-abc1234"
  }
}
```

```json
{
  "case": "actual_new_e2e_UPDATE_assertion_block",
  "input": "",
  "threw": false,
  "log": [
    "UPDATE 语句校验通过: 表名无库名前缀，表名及字段名均无双引号！"
  ]
}
```

本次既有 Node 脚本最终均通过；新增定向用例仍发现上述问题，说明现有测试缺少相应语义断言，不能以原测试 PASS 推翻已确认的缺陷。

## 11. 全部变更路径清单

下列 136 项来自固定 BASE…HEAD 的去重累计差异，链接均指向固定版本；删除项指向基线版本。数量已与 32 个提交文件并集核对。

| 路径 | 状态 | 新增 / 删除行 |
| --- | --- | ---: |
| [.gitattributes](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/.gitattributes) | modified | +3 / -0 |
| [.githooks/commit-msg](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/.githooks/commit-msg) | added | +65 / -0 |
| [AGENTS.md](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/AGENTS.md) | added | +27 / -0 |
| [GEMINI.md](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/GEMINI.md) | added | +27 / -0 |
| [README.md](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/README.md) | modified | +48 / -23 |
| [VERSION](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/VERSION) | modified | +1 / -1 |
| [config.yaml](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/config.yaml) | modified | +5 / -2 |
| [docs/KAIRO_WEEKLY_REVIEW_2026-09-17.md](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/docs/KAIRO_WEEKLY_REVIEW_2026-09-17.md) | added | +286 / -0 |
| [docs/gpt/KAIRO_ACCEPTANCE_TEST_MATRIX.md](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/docs/gpt/KAIRO_ACCEPTANCE_TEST_MATRIX.md) | added | +91 / -0 |
| [docs/gpt/KAIRO_AI_IMPLEMENTATION_TASKBOOK.md](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/docs/gpt/KAIRO_AI_IMPLEMENTATION_TASKBOOK.md) | added | +311 / -0 |
| [docs/gpt/KAIRO_AUDIT_AND_DESIGN_2026-09-14.md](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/docs/gpt/KAIRO_AUDIT_AND_DESIGN_2026-09-14.md) | added | +611 / -0 |
| [internal/comparefs/local.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/comparefs/local.go) | modified | +11 / -0 |
| [internal/config/config.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/config/config.go) | modified | +15 / -9 |
| [internal/config/config_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/config/config_test.go) | modified | +6 / -6 |
| [internal/dbconsole/connection.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/connection.go) | modified | +12 / -3 |
| [internal/dbconsole/export.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/export.go) | modified | +125 / -54 |
| [internal/dbconsole/export_plan.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/export_plan.go) | added | +754 / -0 |
| [internal/dbconsole/export_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/export_test.go) | modified | +212 / -1 |
| [internal/dbconsole/grid.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/grid.go) | modified | +3 / -1 |
| [internal/dbconsole/grid_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/grid_test.go) | modified | +15 / -0 |
| [internal/dbconsole/inspect.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/inspect.go) | modified | +85 / -42 |
| [internal/dbconsole/inspect_metadata_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/inspect_metadata_test.go) | added | +115 / -0 |
| [internal/dbconsole/lob_execution.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_execution.go) | modified | +13 / -0 |
| [internal/dbconsole/lob_locator.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_locator.go) | added | +246 / -0 |
| [internal/dbconsole/lob_projection.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_projection.go) | modified | +4 / -1 |
| [internal/dbconsole/lob_projection_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_projection_test.go) | modified | +67 / -0 |
| [internal/dbconsole/lob_stream.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_stream.go) | modified | +1 / -1 |
| [internal/dbconsole/lob_stream_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_stream_test.go) | modified | +1 / -1 |
| [internal/dbconsole/lob_token.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_token.go) | modified | +35 / -28 |
| [internal/dbconsole/lob_token_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/lob_token_test.go) | modified | +21 / -0 |
| [internal/dbconsole/manager.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/manager.go) | modified | +230 / -41 |
| [internal/dbconsole/metadata.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/metadata.go) | modified | +25 / -1 |
| [internal/dbconsole/metadata_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/metadata_test.go) | modified | +19 / -0 |
| [internal/dbconsole/mutation_regression_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/mutation_regression_test.go) | modified | +8 / -2 |
| [internal/dbconsole/oracle_backend.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/oracle_backend.go) | modified | +21 / -0 |
| [internal/dbconsole/oracle_detect.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/oracle_detect.go) | modified | +16 / -1 |
| [internal/dbconsole/oracle_detect_other.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/oracle_detect_other.go) | modified | +5 / -0 |
| [internal/dbconsole/oracle_detect_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/oracle_detect_test.go) | modified | +87 / -0 |
| [internal/dbconsole/oracle_detect_windows.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/oracle_detect_windows.go) | modified | +33 / -0 |
| [internal/dbconsole/oracle_godror_stub.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/oracle_godror_stub.go) | modified | +11 / -3 |
| [internal/dbconsole/pagination.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/pagination.go) | modified | +191 / -22 |
| [internal/dbconsole/pagination_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/pagination_test.go) | modified | +62 / -0 |
| [internal/dbconsole/perf_benchmark_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/perf_benchmark_test.go) | added | +199 / -0 |
| [internal/dbconsole/policy.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/policy.go) | modified | +94 / -8 |
| [internal/dbconsole/policy_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/policy_test.go) | modified | +25 / -3 |
| [internal/dbconsole/query.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/query.go) | modified | +95 / -49 |
| [internal/dbconsole/query_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/query_test.go) | modified | +19 / -0 |
| [internal/dbconsole/script.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/script.go) | modified | +3 / -1 |
| [internal/dbconsole/transaction_lifecycle.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/transaction_lifecycle.go) | modified | +137 / -11 |
| [internal/dbconsole/transaction_lifecycle_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/transaction_lifecycle_test.go) | modified | +288 / -0 |
| [internal/dbconsole/transactions.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/transactions.go) | modified | +169 / -15 |
| [internal/dbconsole/types.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/dbconsole/types.go) | modified | +1 / -1 |
| [internal/diagnostics/diagnostics.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/diagnostics/diagnostics.go) | modified | +30 / -0 |
| [internal/diagnostics/diagnostics_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/diagnostics/diagnostics_test.go) | modified | +36 / -0 |
| [internal/httpserver/compare_backend_p0_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/compare_backend_p0_test.go) | modified | +194 / -0 |
| [internal/httpserver/compare_jobs_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/compare_jobs_test.go) | added | +117 / -0 |
| [internal/httpserver/handlers_compare.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_compare.go) | modified | +8 / -1 |
| [internal/httpserver/handlers_compare_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_compare_test.go) | modified | +8 / -8 |
| [internal/httpserver/handlers_compare_workbench.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_compare_workbench.go) | modified | +83 / -17 |
| [internal/httpserver/handlers_database.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database.go) | modified | +234 / -61 |
| [internal/httpserver/handlers_database_lob.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database_lob.go) | modified | +127 / -29 |
| [internal/httpserver/handlers_database_lob_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database_lob_test.go) | modified | +211 / -4 |
| [internal/httpserver/handlers_database_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_database_test.go) | modified | +264 / -0 |
| [internal/httpserver/handlers_diagnostics.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_diagnostics.go) | modified | +28 / -0 |
| [internal/httpserver/handlers_files.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_files.go) | modified | +28 / -16 |
| [internal/httpserver/handlers_misc_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_misc_test.go) | modified | +29 / -0 |
| [internal/httpserver/handlers_ssh_sftp.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_ssh_sftp.go) | modified | +22 / -10 |
| [internal/httpserver/handlers_ssh_sftp_upload.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_ssh_sftp_upload.go) | modified | +19 / -5 |
| [internal/httpserver/handlers_ssh_shell_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_ssh_shell_test.go) | modified | +75 / -0 |
| [internal/httpserver/handlers_webservice.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_webservice.go) | modified | +28 / -6 |
| [internal/httpserver/helpers.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/helpers.go) | modified | +15 / -3 |
| [internal/httpserver/httpserver_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/httpserver_test.go) | modified | +12 / -2 |
| [internal/httpserver/response.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/response.go) | modified | +27 / -0 |
| [internal/httpserver/security_acceptance_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/security_acceptance_test.go) | added | +168 / -0 |
| [internal/schedtask/process.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/schedtask/process.go) | modified | +10 / -5 |
| [internal/sftpclient/names.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/names.go) | added | +202 / -0 |
| [internal/sftpclient/names_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/names_test.go) | added | +151 / -0 |
| [internal/sftpclient/sftp_identity_review_regression_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/sftp_identity_review_regression_test.go) | added | +276 / -0 |
| [internal/sftpclient/sftpclient.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/sftpclient.go) | modified | +410 / -39 |
| [internal/sshclient/shell.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sshclient/shell.go) | modified | +100 / -6 |
| [internal/sshclient/shell_gbk_stdin_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sshclient/shell_gbk_stdin_test.go) | added | +134 / -0 |
| [internal/sysutil/process.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sysutil/process.go) | added | +9 / -0 |
| [internal/sysutil/process_unix.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sysutil/process_unix.go) | renamed | +3 / -2 |
| [internal/sysutil/process_windows.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sysutil/process_windows.go) | renamed | +6 / -10 |
| [internal/textcodec/textcodec.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/textcodec/textcodec.go) | modified | +8 / -1 |
| [internal/textcodec/textcodec_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/textcodec/textcodec_test.go) | modified | +43 / -0 |
| [internal/upgrade/journal.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/upgrade/journal.go) | added | +346 / -0 |
| [internal/upgrade/journal_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/upgrade/journal_test.go) | added | +635 / -0 |
| [internal/upgrade/snapshot.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/upgrade/snapshot.go) | modified | +38 / -0 |
| [internal/upgrade/upgrade.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/upgrade/upgrade.go) | modified | +168 / -36 |
| [internal/waspack/build.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/waspack/build.go) | modified | +23 / -4 |
| [internal/waspack/stages.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/waspack/stages.go) | modified | +23 / -3 |
| [internal/waspack/waspack_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/waspack/waspack_test.go) | modified | +199 / -0 |
| [internal/webservice/schema_resolver.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/webservice/schema_resolver.go) | added | +581 / -0 |
| [internal/webservice/schema_resolver_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/webservice/schema_resolver_test.go) | added | +473 / -0 |
| [internal/webservice/types.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/webservice/types.go) | modified | +14 / -3 |
| [internal/webservice/wsdl.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/webservice/wsdl.go) | modified | +51 / -58 |
| [internal/wscodegen/generate.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/wscodegen/generate.go) | modified | +245 / -69 |
| [internal/wscodegen/generate_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/wscodegen/generate_test.go) | modified | +149 / -0 |
| [internal/wscodegen/tool.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/wscodegen/tool.go) | modified | +27 / -5 |
| [internal/wscodegen/tool_isolation_test.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/wscodegen/tool_isolation_test.go) | added | +229 / -0 |
| [internal/wscodegen/types.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/wscodegen/types.go) | modified | +5 / -4 |
| [main.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/main.go) | modified | +1 / -0 |
| [package-lock.json](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/package-lock.json) | modified | +2 / -2 |
| [package.json](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/package.json) | modified | +6 / -3 |
| [scripts/build_windows_amd64.sh](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/scripts/build_windows_amd64.sh) | modified | +18 / -0 |
| [scripts/check-version.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/scripts/check-version.js) | modified | +7 / -0 |
| [scripts/package_windows.sh](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/scripts/package_windows.sh) | modified | +6 / -0 |
| [scripts/run-tests.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/scripts/run-tests.js) | added | +135 / -0 |
| [tests/compare-folder-regression.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/tests/compare-folder-regression.js) | modified | +42 / -2 |
| [tests/database-review-regression.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/tests/database-review-regression.js) | modified | +47 / -0 |
| [tests/e2e/test-database-backup-real.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/tests/e2e/test-database-backup-real.js) | added | +108 / -0 |
| [tests/e2e/test-database-deep.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/tests/e2e/test-database-deep.js) | added | +268 / -0 |
| [tests/tab-lifecycle-review-regression.test.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/tests/tab-lifecycle-review-regression.test.js) | added | +419 / -0 |
| [web/app.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/app.js) | modified | +202 / -149 |
| [web/app.test.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/app.test.js) | modified | +562 / -3 |
| [web/core.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/core.js) | modified | +253 / -64 |
| [web/index.html](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/index.html) | modified | +16 / -5 |
| [web/notes.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/notes.js) | modified | +4 / -1 |
| [web/overlays.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/overlays.js) | modified | +41 / -4 |
| [web/pages/about.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/about.js) | modified | +14 / -14 |
| [web/pages/commands.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/commands.js) | modified | +10 / -4 |
| [web/pages/compare-workbench.css](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare-workbench.css) | modified | +262 / -13 |
| [web/pages/compare.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js) | modified | +1005 / -111 |
| [web/pages/database.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/database.js) | modified | +0 / -0 |
| [web/pages/diagnostics.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/diagnostics.js) | modified | +0 / -0 |
| [web/pages/files.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/files.js) | modified | +0 / -0 |
| [web/pages/home.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/home.js) | modified | +0 / -0 |
| [web/pages/notes.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/notes.js) | modified | +0 / -0 |
| [web/pages/ssh.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/ssh.js) | modified | +0 / -0 |
| [web/pages/websphere.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/websphere.js) | modified | +0 / -0 |
| [web/style.css](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/style.css) | modified | +0 / -0 |
| [web/tabs.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/tabs.js) | added | +0 / -0 |
| [web/workbench/database-features.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/workbench/database-features.js) | modified | +0 / -0 |
| [web/workbench/database-object-designer.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/workbench/database-object-designer.js) | modified | +0 / -0 |
| [web/workbench/route-scope.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/workbench/route-scope.js) | added | +0 / -0 |

## 12. 交付与验收结论

本报告已覆盖约定的一周提交，问题均针对固定 HEAD 下仍存在的行为。修复优先级首先考虑用户内容、数据定位和失败恢复；功能矩阵区分正常路径接通与完整验收。建议将第 5 节各批次分别交给实现 AI，要求附修复前失败、修复后成功的语义测试；真实 Oracle/SFTP/Windows 和浏览器场景仍需在具备环境时验收。
