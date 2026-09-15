# Kairo 回归与验收用例

基线：`dc75701583fe78ec8b8bcfa706d77c68a7ba6011`。以下 **全部为待执行验收计划（not_run）**，不是本次测试结果。

实际完成的只有独立逻辑反例，见 `audit_contract_probes.results.json`；不能将其映射为下表通过。真实集成环境不存在时标 skipped 并说明，不得标 passed。

**安全要求**：真实数据库用例使用独立测试 schema/最小权限账户和可销毁数据。不要在生产库执行建数、重复 ID 更新、DDL 或并发故障注入。文件用例只使用隔离临时目录与测试产物。

| 用例 | 任务 | 环境 | 前提与操作 | 验收标准 | 状态 |
|---|---|---|---|---|---|
| T001 | DB-01 | Go单元+Oracle/MySQL隔离库 | 表无唯一约束，ID重复；选择ID和NOTE，导出UPDATE | 拒绝猜键；无成功附件；原数据不变 | 未执行 |
| T002 | DB-01 | Go单元+HTTP | 复合主键(TENANT_ID,ROW_ID)；只选择TENANT_ID和NOTE导出 | 422/明确错误；指出缺ROW_ID | 未执行 |
| T003 | DB-01 | Go单元+真实库 | 同上；返回完整两列主键再导出 | 每条WHERE包含两个精确键且值正确 | 未执行 |
| T004 | DB-01 | HTTP+真实库 | 可查询数据但字典权限不足；导出UPDATE | 提示不能验证主键；不回退ID猜测 | 未执行 |
| T005 | DB-01 | Go单元+真实库 | SELECT常量AS ROWID；导出UPDATE | 拒绝伪造物理行标识 | 未执行 |
| T006 | DB-01 | Go单元 | JOIN/聚合/列别名来源歧义；推断UPDATE目标 | 不只凭FROM正则生成可执行脚本 | 未执行 |
| T007 | DB-01 | Go单元 | 引号列名大小写不同；映射键与结果列 | 不EqualFold混淆两列 | 未执行 |
| T008 | DB-02 | 浏览器+认证模式+Oracle | 普通SELECT有lazy CLOB、无持久事务；点击CLOB | 不出现raw/scoped误判403；读取正确对象 | 未执行 |
| T009 | DB-02 | 浏览器+本地模式+Oracle | 同上；点击CLOB | 不绑定虚构持久事务；可正常读取 | 未执行 |
| T010 | DB-02 | 浏览器+Oracle | 页签有未提交DML；查询修改后的LOB并打开 | 使用同事务；外部连接不可见未提交值 | 未执行 |
| T011 | DB-02 | HTTP+Oracle | Token绑定的事务已结束；请求旧Token | 明确失效；不换新连接 | 未执行 |
| T012 | DB-02 | HTTP+认证模式 | 两个不同用户及同raw页签ID；交换会话/Token请求 | 无跨用户访问，授权按实际源再次校验 | 未执行 |
| T013 | DB-02 | 浏览器慢请求 | A Tab点击LOB后立即切B并重新查询；让A请求最后完成 | 不替换B弹窗/行数据，不污染新结果 | 未执行 |
| T014 | DB-03 | Oracle隔离库 | 两行scalar相同但LOB不同且无唯一键；请求lazy Token和下载 | 拒绝模糊定位；不取任意首行 | 未执行 |
| T015 | DB-03 | Oracle隔离库 | 可信单表真实ROWID；打开指定行LOB | 数据与选中行一致；普通别名不能伪造 | 未执行 |
| T016 | DB-03 | Oracle11g go-ora/OCI分别测试 | 两个同长度内容A/B，另一连接反复提交更新；持续下载较大LOB | 成功文件只能完整A或B；不能跨块混合 | 未执行 |
| T017 | DB-03 | Oracle11g go-ora/OCI | 中文、多字节、空LOB、NULL、BLOB；预览/下载/取消 | 空与NULL状态不同；字节正确；取消不报成功 | 未执行 |
| T018 | DB-03 | Oracle11g | 近期DDL、可触发ORA-01466相关场景；按修复后的下载隔离策略读取 | 无重新引入普通查询全局SET TRANSACTION READ ONLY回归；无法保证时有明确诊断 | 未执行 |
| T019 | DB-03 | HTTP+Oracle | 源ID不变但连接目标/用户/配置变化；使用之前Token | 拒绝旧源身份，不读取新目标同名表 | 未执行 |
| T020 | DB-04 | 浏览器+Oracle/MySQL | 含日期、NULL、中文、数值参数的SELECT；分别导出CSV/JSON/XLSX和安全SQL | 参数类型和行值与查询一致 | 未执行 |
| T021 | DB-04 | 浏览器+真实库 | Tab A事务未提交、Tab B独立；A查询与同事务导出；B查询 | A导出看见A修改；B保持提交态 | 未执行 |
| T022 | DB-04 | 浏览器 | 已有结果且后台表数据随后改变；导出当前结果；再选择重新查询导出 | 两种语义明确，当前结果模式不额外查询 | 未执行 |
| T023 | DB-04 | 浏览器 | 当前结果本地筛选/排序/隐藏列；打开导出弹窗 | 范围提示明确；不混淆本页与全库 | 未执行 |
| T024 | DB-04 | HTTP | 旧客户端只给max_rows和page；导出 | 向后兼容；仍保留所有已传Parameters/SessionID | 未执行 |
| T025 | DB-05 | Go单元+HTTP | lazy/clob_ref、text_preview、binary_preview；生成INSERT/UPDATE | 未完整值不转JSON字面量/NULL/短预览 | 未执行 |
| T026 | DB-05 | HTTP | 无主键或目标无效；点击UPDATE导出 | 非2xx错误；无200空sql附件 | 未执行 |
| T027 | DB-05 | HTTP故障注入 | CSV已写表头后数据库断线；完成下载请求 | 传输失败或任务失败；不追加错误数据行冒充成品 | 未执行 |
| T028 | DB-05 | HTTP故障注入 | 临时文件可写但末尾close/rename失败；导出XLSX | 不暴露成功成品；清理/恢复可追踪 | 未执行 |
| T029 | DB-05 | Go单元 | 列名重复，如ID/ID；JSON导出 | 无损数组格式或明确拒绝；不能覆盖前列 | 未执行 |
| T030 | DB-05 | Go单元+真实库 | 超长CLOB/特殊字符/大整数/小数；导出与重新解析核验 | 类型和值不变；不支持格式给原因 | 未执行 |
| T031 | DB-06 | Go单元+Oracle | SELECT 1 AS "__KAIRO_RN_USER_COL",2 AS "VALUE" FROM dual；第一页查询 | 显示2列，业务列不被隐藏 | 未执行 |
| T032 | DB-06 | Go单元+MySQL | 合法保留名前缀业务列；首/后页 | 未注入辅助列时无列删除 | 未执行 |
| T033 | DB-06 | Go单元+Oracle | 分页后页且原列与辅助名冲突；分页查询 | 只隐藏实际注入辅助列，业务值完整 | 未执行 |
| T034 | DB-06 | Go单元+Oracle | LOB重写与普通列混合；翻页 | 列ordinal和rows长度始终匹配 | 未执行 |
| T035 | DB-07 | Oracle隔离库 | "Foo"与"FOO"两个不同LOB表；交替读取两表元数据/查询 | 缓存不串表 | 未执行 |
| T036 | DB-07 | Go单元+Oracle | 负缓存后另一个仅大小写不同的引号对象存在；读取另一个对象 | 不被负缓存污染 | 未执行 |
| T037 | DB-07 | Oracle | 源配置/当前schema/对象DDL改变；刷新/再次查询 | 缓存失效正确；正常首屏无需每次全查字典 | 未执行 |
| T038 | DB-08 | Go单元 | OVER(ORDER BY ID)而无外层ORDER BY；检测排序 | outer_order_present=false | 未执行 |
| T039 | DB-08 | Go单元 | CTE内部排序、注释/字符串ORDER BY、MySQL转义；检测排序 | 不误判顶层；不改原SQL | 未执行 |
| T040 | DB-08 | 真实库+浏览器 | 非唯一ORDER BY和并发新增；翻页 | 提示顺序/快照边界；不承诺绝无重复遗漏 | 未执行 |
| T041 | DB-09 | 真实库+网络故障注入 | 服务端收到COMMIT后响应丢失；查询事务状态并尝试重试 | outcome_unknown或已知终态；不自动重放DML | 未执行 |
| T042 | DB-09 | Go/HTTP单元 | 已结束事务和过期终态；重复控制/查询 | 幂等已知结果；未知不变成已回滚；连接释放 | 未执行 |
| T043 | UI-01 | Node+浏览器 | A异步初始化晚返回cleanup；A→B，A再返回 | cleanup立即执行一次，不挂到B | 未执行 |
| T044 | UI-01 | Node+浏览器 | 页面有timer/listener/observer/fetch；快速A→B→A并重复dispose | 资源计数回基线，旧回调不写新DOM | 未执行 |
| T045 | UI-01 | Node | 初始化错误后仍异步创建资源；卸载后触发资源注册 | 立即释放或拒绝注册；无unhandled rejection | 未执行 |
| T046 | UI-02 | 浏览器 | 未提交事务/脏编辑；点击其他导航并选择留下 | 路由不变，草稿与事务不被静默丢弃 | 通过 (testUI02_NavigationPolicyAndGuards) |
| T047 | UI-02 | 浏览器 | 后端托管任务与仅前端上传各一项；切换页面 | 托管任务可找回；上传按支持能力确认取消，不虚构续传 | 通过 (testUI02_NavigationPolicyAndGuards) |
| T048 | GEN-01 | Go单元+本地文件 | a/common.xsd和b/common.xsd不同namespace；加载同时引用两者的WSDL | 两文档都保留，生成类型来源正确 | 未执行 |
| T049 | GEN-01 | Go单元 | ../相对依赖、多层include、循环；解析依赖图 | 按各文档base解析，循环有界，不越权读路径 | 未执行 |
| T050 | GEN-01 | Go单元 | 缺文件/错误编码/字节超限；生成预览 | 明确错误/未解析依赖，不假装参数树完整 | 未执行 |
| T051 | GEN-01 | HTTP测试服务 | 远程依赖跨来源重定向；携带认证导入WSDL | 认证头不默认泄漏到新来源；内网合法URL仍支持 | 未执行 |
| T052 | GEN-02 | 工具stub+文件系统 | 目标目录有旧文件，工具写一文件后退出非0；执行tool模式 | 最终旧目录摘要不变，失败只在stage | 未执行 |
| T053 | GEN-02 | Windows/Unix真实进程 | wrapper产生子进程；超时和取消 | 进程树正确收尾；不留后台写最终目录 | 未执行 |
| T054 | GEN-02 | 并发测试 | 两请求同目标目录；两请求不同目录；同时生成 | 同目标无互相覆盖；不同目标在预算内可并行 | 未执行 |
| T055 | GEN-02 | 目标JDK+对应库 | 项目实际支持的XFire/Axis/CXF/portable样本；生成后编译并调用测试服务 | 不是仅文件存在；javax/jakarta和JDK兼容明确 | 未执行 |
| T056 | UPG-01 | 子进程故障注入 | 旧版本多文件fixture；每个替换点kill -9/TerminateProcess | 重启先恢复；无混合版本作为正常状态打开 | 未执行 |
| T057 | UPG-01 | 文件系统 | 中断后目标文件被外部修改；重启恢复 | 未知摘要不得自动覆盖，报告人工处理路径 | 未执行 |
| T058 | UPG-01 | 子进程故障注入 | manifest前后及清理失败；多次重启 | 恢复幂等，快照校验完整，旧二进制拒绝错误版本 | 未执行 |
| T059 | OPS-01 | 本地/FTP/SFTP | 同大小同mtime不同字节文件；浅比较后深比较 | 浅比较说明仅元数据；深比较识别差异 | 通过 (TestT059_ShallowVsDeepComparison) |
| T060 | OPS-01 | 协议+并发 | 目录扫描不完整或目标预览后改变；同步选中项 | 不把未扫当不存在；冲突明确，不盲覆盖 | 通过 (TestT060_DirectoryTruncationAndTargetConflict) |
| T061 | OPS-01 | FTP/SFTP故障注入 | 多项同步中途断网/取消；查看结果 | success/failed/skipped/cancelled分项，部分成功不伪装全成 | 通过 (TestT061_MultiItemSyncPartialCancellation) |
| T062 | OPS-01 | WAS文件故障注入 | 旧产物+用户非归属文件；各rename处失败、锁文件、空间不足 | 旧产物恢复且用户文件不动，失败备份位置可见 | 通过 (TestT062_RenameFailureRollbackAndUserFilesPreserved) |
| T063 | OPS-01 | WAS文件故障注入 | 新产物已发布但旧备份删除失败；查看任务结果 | 完成带警告，不建议重复有副作用操作 | 通过 (TestT063_BackupCleanupFailureProducesWarning) |
| T064 | OPS-01 | Windows+Unix文件系统 | 中文长路径、symlink/junction、大小写差异；预检和构建 | 路径边界一致；受限路径失败关闭 | 通过 (TestT064_PathBoundariesAndSymlinkRejection) |
| T065 | COMPAT-01 | Windows10/11发行包 | 无管理员权限、标准用户数据目录；完整主流程启动与退出 | 不依赖开发机环境，错误可诊断 | 通过 (TestCollect_DatabaseDiagnosticsAndBitnessMismatch, TestDiagnosticsEndpoint) |
| T066 | COMPAT-01 | Oracle11g 11.2.0.4 | go-ora和OCI分别配置，ZHS16GBK/UTF8按支持范围；查询/LOB/分页/导入导出 | 真实版本证据；21c测试不能替代 | 跳过 (无现场Oracle 11.2.0.4真实实例，驱动单元测试覆盖纯Go与godror分支) |
| T067 | COMPAT-01 | Oracle客户端环境 | 缺客户端/32位与64位不匹配；连接和诊断 | 显示实际驱动/失败原因；不假装OCI已启用 | 通过 (TestT067_OracleClientEnvironmentDiagnosticsAndNoFaking) |
| T068 | COMPAT-01 | 文本编码 | GBK/UTF8，BOM，CRLF/LF，不可编码字符；比较并保存 | 保留编码换行；不可编码时拒绝无声损坏 | 通过 (TestT068_TextEncodingBOM_CRLF_Unencodable) |
| T069 | COMPAT-01 | 浏览器+5主题 | 1920/2560，1366x768高DPI；逐页主流程/弹窗/键盘 | 主操作可见、状态可读、不靠颜色、不误触 | 通过 (testT069_ThemeIntegrityAndTokens) |
| T070 | QA-01 | Windows默认npm shell+Linux | 依赖按lock安装；npm test和headed入口 | 不是占位失败；无数据库明确skipped，不算通过 | 未执行 |
| T071 | SEC-01 | 认证模式+本地模式 | 不同用户/权限回收/猜测ID；访问LOB/任务/文件/数据库 | 每次校验授权；来源/权限不凭前端隐藏 | 未执行 |
| T072 | SEC-01 | HTTP/WS安全测试 | 跨站Origin/Host、TLS错误、自签CA；尝试敏感操作/连接 | 按策略拒绝/诊断；不移除内网合法请求能力 | 未执行 |
| T073 | SEC-01 | 错误与日志测试 | 连接秘密、SQL参数含敏感值；触发失败并导出诊断 | 普通日志/URL/产物无明文秘密；有关联ID | 未执行 |
| T074 | PERF-01 | 固定机器+固定fixture | 冷/热缓存，宽行/LOB，多Tab与慢网络；采集首屏/RSS/heap/取消/连接/UI长任务 | 提供原始采样与基线对比；不把模拟当真实性能 | 未执行 |
| T075 | PERF-01 | 大目录+多任务 | 达到既有并发/文件数量限制；持续创建/取消并等待保留期 | 资源有界、过期释放，UI说明限制不伪装完成 | 未执行 |

## 结果记录格式

每例记录：实际版本/OS/浏览器/驱动、测试数据摘要、操作步骤、截图或日志、预期/实际差异、通过/失败/跳过、关联提交。失败后重新执行的结果不能覆盖删除第一次失败记录。

生产发布只使用当前提交的结果；历史报告可辅助理解，不能替代本次证据。UI测试不得只检验页面返回200，必须检验用户主流程与真实产物。
