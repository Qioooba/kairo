# 数据库工作台 V2 全功能审核报告（2026-09-05）

> 后续未提交代码审查见 [本轮审查记录](UNCOMMITTED-CODE-REVIEW-2026-09-05.md)。下方真实 Oracle 验收为此前记录，本轮没有重新执行；本轮修复与验证范围以链接文档为准。

## V4 实施与最终验收（当前结论）

本轮已把此前列为“必须补齐”的数据库工作台能力落地；下方 V3/V2 内容作为历史审计基线保留，不再代表当前缺口。

- 数据源：环境标识、只读/DDL 门禁、生产 DML 二次确认、TLS/mTLS、SSH 隧道及独立凭据、连接池与事务超时清理。
- SQL 工作台：命名/位置绑定参数、Oracle PL/SQL `/` 结束符脚本执行、查找替换、结构化格式化、表/字段联想、错误行跳转、文件打开/保存、查询历史、收藏深链接和新窗口直达。
- 数据：页签事务状态统一、CSV/XLSX 预览映射与导入、安全网格新增/修改/删除、主键或 Oracle ROWID 定位、原值乐观并发检查。
- 对象工作室：表、视图、索引、约束、序列的 typed preview/apply 和反向结构回填；Function 源码读取、编辑、编译、错误列表及行列跳转；对象依赖和无效对象查询。
- 性能：结果虚拟化、输入/高亮单一 owner、低配设备自动降级、减少动效和重绘。2560×1440 最终实测页面无横向溢出，初始 DOM 516 个节点，进入可用约 1.486 秒，约 50K SQL 粘贴同步处理约 282 ms。

最终验证：

- `go test -mod=vendor ./...`：全仓通过。
- 数据库前端 JS 语法检查、`tests/database-features-unit.js`、`tests/workbench-unit-tests.js`：通过。
- 项目既有浏览器套件 `node tests/e2e/index.js --grep 数据库工作台`：11/11 通过。
- 独立真实 Oracle + 2560×1440 验收：17 个场景全部通过，包含真实建表/改表、索引/CHECK、默认值反查、绑定参数、网格增删改、CSV 导入、事务回滚、五类对象设计器、新窗口深链接、Function 错误行跳转和 50K SQL 输入。
- 验收临时对象 `KAIRO_E2E_%` 已全部清理，最终查询为 0 行。

环境边界：本机 `KAIRO_RO` 只有 `CREATE SESSION`、`CREATE TABLE` 权限，因此视图/序列使用真实 Oracle typed preview 验证；Function 编译已真实到达 Oracle 并返回结构化 `ORA-01031` 权限错误，编译错误列表与行跳转使用同一真实页面的受控编译响应验证。具备相应 Oracle 权限时无需前端或接口改造即可执行。

## 修复后最终复验（V3）

本节是最新结论；下方“高优先级问题”等内容保留为修复前基线，便于追溯。此前列出的状态文案、只读网格绕过、成功消息误用错误色、快捷键、对象栏生命周期和页签状态隔离问题均已修复。

- 左侧对象区只保留导航树，不再重复展示字段/索引/约束/DDL；表、视图、函数等分类统一为小文件夹外观。单击对象在右侧新增详情页签，双击仍生成 SQL。
- 查询结果按类型使用约定列宽：标识/数字 116px、日期时间 176px、布尔 92px、普通字符 180px、LOB/长文本 260px；用户拖动范围限制为 72–420px。对象详情的字段、索引、约束表也使用固定列宽和溢出省略。
- DML 改为真正的页签级事务：执行后保持未提交，页面“提交”或显式 `COMMIT` 才落库；“回滚”或 `ROLLBACK` 撤销。不同页签使用不同事务，SELECT 可读取本页签自己的未提交内容，其他页签不可见。网格批量修改先暂存到同一事务，提交响应失败时重试不会重复执行 UPDATE。
- Oracle DDL 无法由客户端改变其隐式提交语义；界面在执行前警告，执行后明确显示“DDL 已执行（数据库隐式提交）”。
- 关闭含未提交事务的页签、切换数据源和关闭管理器时均走回滚保护；连接失效和服务退出也会回滚并释放事务。
- 结构按钮图标统一为 SVG；提交、回滚、错误、成功和 DDL 警告使用独立视觉语义。
- 修复对象详情作为 CSS Grid 子项时被内容撑宽的问题：1080p 下详情面板从越界的 1465px 收敛为可用区内的 1309px，查询、INSERT 模板、复制 DDL、刷新、对象设计操作全部可见；标题过长会省略，操作区可换行。
- 专业增强层现已取得真实的当前查询页签 `session_id/source_id`，脚本、网格批处理和导入不会再发送空事务标识；数据源管理新增的环境、只读、允许 DDL、TLS 与 SSH 隧道字段也纳入基础表单保存序列化，不会出现“界面填写但保存丢失”。
- 只审核桌面目标：1920×1080 与 2560×1440；项目不按手机端布局验收。

真实 Oracle 21c XE 复验：先恢复本机已挂载但未打开的 `XEPDB1` 为 `READ WRITE`，再创建测试表 `KAIRO_TX_TEST_0905`。未提交 INSERT 在第二页签查询为 0 行；回滚后仍为 0 行；点击页面提交后第二页签查询为 1 行；显式 `COMMIT` 后更新值对其他页签可见。按用户授权，测试表和最终数据保留。

最终自动化结果：

- `go test -mod=vendor -count=1 ./internal/dbconsole ./internal/httpserver`：通过。
- `node --check web/pages/database.js`：通过。
- `node web/app.test.js`：23/23 通过；`node tests/workbench-unit-tests.js`：全部通过。
- `node tests/e2e/index.js --grep 数据库工作台`：11/11 通过，包括 20,000 行虚拟滚动、双页签并发、网格修改隔离、权限/事务/快捷键/输入法保护，以及增强层页签事务契约与安全连接字段。
- 逐步真实点击截图：`output/playwright/database-workbench-v3-2026-09-05/`，共 51 张；其中 08、11、13、16、18、29、31 分别记录未提交、跨页签不可见、回滚、页面提交、提交后可见、最终待提交、显式 COMMIT 状态同步；44、46、47、48、49、50、51 是最终构建的 1080p 对象操作、固定列宽、2K 查询/对象页、括号匹配、数据源安全表单和最终工作台。

最终布局实测：1920×1080 结果列宽为 `54 / 116 / 180 / 176 / 180 / 260px`，表格宽 966px，结果容器 1254px；对象字段表为 `44 / 180 / 170 / 84 / 100 / 180 / 320px`，表宽 1078px。2560×1440 下结果容器 1894px。两种分辨率均为 `document.scrollWidth === document.clientWidth`，无整页横向溢出。

与 Navicat / PL/SQL Developer 的定位仍应实事求是：当前已经是可靠的可写数据库工作台，覆盖查询、多页签、事务、轻量网格编辑、对象详情、执行计划、导出、历史/收藏；尚未覆盖 PL/SQL 调试器、Profiler、对象可视化设计器、ER 模型、数据/结构同步、备份恢复等专业 IDE 套件能力。

## 结论

本报告已按 **V2（允许编辑与执行写语句）** 重新审核；此前依据 V1 文档得出的“写入本身是阻断缺陷”结论作废。

V2 的核心功能在本机真实 Oracle 环境中已经可用：连接测试、查询、DDL、DML、结果表格编辑、批量提交、回滚未提交网格修改、对象字段/索引/约束/DDL、对象双击生成 SQL、真实执行计划、慢查询取消均已通过。1920×1080 与 2560×1440 均无页面横向溢出，20,000 行前端虚拟滚动也有效。

当前不属于“不能用”，但还不建议把它宣传成与 Navicat / PL/SQL Developer 等价的完整数据库 IDE。发布前最需要解决的是：**页面仍宣称“只读会话”、网格只读模式可被双击/F2绕过、SQL 编辑器与网格的事务语义不一致、成功结果使用错误色、快捷键和 E2E 状态隔离缺陷**。

本轮未修改业务代码；只补充审计脚本、测试截图和本报告。

## 真实环境与方法

- 浏览器：真实 Chromium 页面，实际点击、输入、左键、右键、双击、快捷键；关键动作后截图。
- 分辨率：1920×1080、2560×1440，另复用项目 1366×900 E2E。
- 数据库：本机 Oracle Database 21c XE 21.3，`XEPDB1` 为 READ WRITE；监听器与服务正常，端口 1521。
- 账号：项目自带的 `KAIRO_RO` 实验账号，拥有其测试 schema 的建表权限，可查询 `KAIRO_LAB.EMPLOYEES`（2,500 行）。凭据仅保存到本机 Kairo 凭据存储，未写入报告或截图。
- 隔离：创建专用表 `KAIRO_V2_AUDIT_0905` 验证 V2，结束后已删除；当前可从 Oracle recycle bin 恢复，直到被 purge。
- 压力：真实 Oracle 验证了取消与会话释放；20,000 行和 8 页签为浏览器侧受控压力，用于测虚拟化与交互，不等同于数据库长稳压测。

## 真实 Oracle V2 验收结果

| 场景 | 结果 | 判定 |
| --- | --- | --- |
| 测试连接 | Oracle 21c XE，约 1ms | 通过 |
| `SELECT SYSDATE FROM DUAL` | 返回 1 行，约 6ms | 通过 |
| 创建专用测试表 | `CREATE` 成功 | 通过 |
| 插入测试数据 | `INSERT` 影响 1 行 | 通过 |
| 网格双击编辑 + 提交 | `Alice` 改为 `Alice V2 Edited`，重新查询一致 | 通过 |
| 网格编辑 + 回滚 | `NOTE` 临时改值后恢复为原值 | 通过 |
| 对象树 | 可刷新并看到测试表与 `PLAN_TABLE` | 通过 |
| 对象单击 | 字段、主键、索引、约束、DDL 均能读取 | 通过 |
| 对象双击 | 生成带 schema/table 引号的 `SELECT *` | 通过 |
| 执行计划 | 返回 12 行 `DBMS_XPLAN`，可见 `TABLE ACCESS FULL` | 通过 |
| 慢查询取消 | 进入“查询中”后取消，约 55ms 显示“已取消” | 通过 |
| 取消后会话 | `KAIRO_RO` 活跃非空闲会话为 0 | 通过 |
| 2560×1440 | `scrollWidth = clientWidth = 2560` | 通过 |

说明：一次用 `CONNECT BY LEVEL <= 100000000` 构造压力的尝试被 Oracle 以 ORA-30009 很快拒绝，这是测试 SQL 不合适，不是产品缺陷；随后改用三表 CROSS JOIN 完成了可取消慢查询验证。

## 高优先级问题

| 等级 | 问题 | 证据与影响 | 建议 |
| --- | --- | --- | --- |
| P1 | V2 页面仍显示“只读会话”，空 SQL 提示也写“请输入只读查询”；编辑按钮只控制网格却叫“只读/编辑中”。 | 真实 DDL/DML 与网格编辑都成功，但顶部仍显示“只读会话”；`web/pages/database.js:1168,1410,2508,2616`。用户无法判断是会话只读、SQL 只读还是仅网格锁定。 | 顶部改为真实权限/事务状态；按钮改为“网格编辑关闭/开启”；删除 V1 残留文案并补 V2 设计说明。 |
| P1 | 网格“只读”状态下，双击单元格或按 F2 仍进入编辑。 | 自动指标：`readOnlyBeforeDblClick=true, cellInput=true`；`startCellEdit` 及双击/F2入口未检查 `state.isEditMode`（`database.js:842,1918,2951,2975,3105`）。 | 入口与函数内部双重校验；只读双击打开单行记录，编辑模式才允许进入输入框。 |
| P1 | SQL 编辑器与网格编辑的事务语义不清。 | SQL DML/DDL走 `db.ExecContext`，在 Oracle 驱动下为独立语句提交；工具栏“提交/回滚”只处理网格 pending changes。分类器又接受 `COMMIT/ROLLBACK`，但 HTTP 请求没有固定到同一数据库会话。 | 二选一：明确标注编辑器“自动提交”，拒绝无意义的独立 `COMMIT/ROLLBACK`；或实现每页签固定连接/事务、明确 dirty 状态与提交/回滚。 |
| P1 | DML/DDL 成功面板使用红色错误样式与感叹号。 | 真实 `UPDATE` 成功截图中标题为“执行成功”，但整个结果卡为红色，容易被误判为失败。 | 为 `ok/warn/error` 建立独立语义色与图标，并做视觉回归测试。 |
| P1 | `Alt+O` tooltip 声称可收起/展开对象栏，但实际无效果。 | 1080p 可见浏览器与自动操作都复现；DOM 有标题（`database.js:1383-1384`），全局快捷键未实现该分支。 | 实现并补 E2E，或删除快捷键提示。 |
| P1 | 默认 `Ctrl+Shift+E` 与 Windows 中文输入法冲突。 | 手测中执行计划被触发，同时“谷歌”被插入 SQL。 | 增加 composition 防护，避免在 `isComposing` 时处理；考虑更换默认组合并覆盖中文 IME。 |
| P1 | 数据库 E2E 9 项仅 7 项通过。 | 一项受服务端持久化“对象栏折叠”污染，一项因只读双击进入编辑失败。 | 每例重置工作台偏好/会话；把 V2 的编辑模式、自动提交和只读网格语义写成明确断言。 |

## 布局、文本框、下拉框、图标与交互

### 已通过

- 1080p、2K 均无页面级横向溢出，主编辑器、结果区、对象栏可用。
- 数据源下拉、连接测试、数据源管理、设置入口层级清晰；真实连接状态反馈准确。
- SQL 编辑器可输入长 SQL，格式化、补全、模板、多页签、历史、收藏、导出入口存在。
- 结果区网格/单行视图、过滤、分页、列控制、复制和右键菜单可工作。
- 对象单击加载详情、双击生成 SQL 的区分合理；对象详情四个页签均有真实数据。
- 慢查询时“取消”按钮状态正确，取消后没有残留活跃会话。

### 需要改进

1. 数据源管理以内联长面板展开，1080p 下页面高度约从 1080 增至 1739，SQL 与结果被推离首屏。建议改右侧抽屉或模态页。
2. 结果工具栏约 12px 字号、28px 高，SQL 约 13px；高密度桌面勉强可用，但长期使用偏小。建议控件统一到 32–36px，并提供密度/字号设置。
3. 2K 下仍保留两行工具栏与固定约 280px 编辑器，没有充分利用宽度。可在宽屏断点合并工具栏并记忆分隔条高度。
4. “每页 2000”实际上接近“查询返回上限”，又与结果分页 20/50/100/200 并列，命名易误导。应改为“查询上限”和“页面行数”。
5. 对象详情底部面板高度较小，索引/约束/DDL 阅读拥挤；建议支持独立放大、双击展开或记忆高度。
6. SQL 文本框、结果过滤框主要依赖 placeholder；部分图标按钮和下拉框缺正式 label/`aria-label`。首屏 38 个可见控件中自动识别 10 个无正式可访问名称。
7. 对象栏使用 `📁/📑/💾/⤢` 等字符图标，主工具栏使用 SVG，跨系统基线与尺寸不一致。应统一图标系统、tooltip 与 `aria-label`。
8. DDL/危险 DML 会直接执行，没有二次确认、环境徽标或受保护 schema 策略。V2 可允许写，但生产数据源至少应提供可配置的 `DROP/TRUNCATE/无 WHERE UPDATE/DELETE` 警告。
9. 恢复会话时观察到页签标题与当前 SQL 正文可能不同步，应在 session 恢复和编辑后统一派生标题。

## 鼠标与快捷键矩阵

| 操作 | 结果 |
| --- | --- |
| 左键选择数据源、按钮、结果行 | 通过 |
| 对象单击 | 通过：加载字段/索引/约束/DDL |
| 对象双击 | 通过：生成对象查询 SQL |
| 结果单元格双击 | 编辑模式通过；“只读”模式失败，仍可编辑 |
| 结果单元格右键 | 菜单可打开；只读时“编辑”仍应禁用 |
| `Ctrl+Enter` | 通过：执行 SQL |
| `Escape` / 取消按钮 | 通过：真实慢查询终止且会话释放 |
| `Alt+1` / `Alt+2` | 通过：网格/单行视图切换 |
| `Ctrl+Shift+E` | 功能能触发，但中文输入法污染 SQL，失败 |
| `Alt+O` | 失败：提示存在但未实现 |
| `F2` | 编辑模式可用；只读模式缺少防线 |
| 补全方向键/Enter/Tab/Escape | 基础路径通过；模板 Space 展开通过 |

未发现产品对 `Ctrl+S`、查找/替换、运行当前选中 SQL、运行当前语句、注释切换等 IDE 常用组合提供完整实现或快捷键帮助页。若目标是完整 SQL IDE，应建立一张可配置快捷键表并自动化逐项验证，不能只在 tooltip 中分散声明。

## 性能与压力结果

| 场景 | 结果 | 判断 |
| --- | ---: | --- |
| 真实慢查询取消 | UI约 55ms 显示取消；DB活跃会话回到 0 | 通过 |
| 20,000 行模拟流式结果加载 | 943ms | 前端可接受 |
| 20,000 行当前结果过滤 | 148ms | 可接受 |
| 虚拟网格 DOM 行数 | 56 行 | 通过，没有创建 20,000 个 DOM 行 |
| 网格滚动高度/可视高度 | 620,032px / 966px | 通过 |
| 8 页签模拟并发 | 1,291ms，峰值并发 5 | UI未冻结；非真实DB长稳结论 |
| `BenchmarkFastRowBytes` | 43.81–56.98ns/op，0 B/op，0 allocs/op | 通过 |

尚未完成：10分钟 8 并发长稳、浏览器内存峰值、16/64MiB 边界、弱网重连、连接池泄漏、Oracle 11g、MySQL 与 Redis 实机矩阵。本机是 Oracle 21c，不能替代 Oracle 11g 兼容性验收。

## 与 Navicat / PL/SQL Developer 对照

| 能力 | Kairo V2 | 结论 |
| --- | --- | --- |
| 连接、SQL 编辑、查询、多页签、历史/收藏 | 已有主体 | 基础可用，需补查找替换、参数、选区/当前语句运行和快捷键管理 |
| 数据网格编辑 | 已有提交/回滚 pending change | 可用，但只读门槛和事务文案必须修 |
| DML/DDL | 已实机通过 | 可用，但当前是编辑器独立语句执行，需明确自动提交与危险操作保护 |
| 对象树/字段/索引/约束/DDL | 已实机通过 | 基础可用；缺对象创建/修改设计器、依赖、权限、同义词等深度功能 |
| 执行计划 | 已有文本计划 | 基础可用；缺可视计划、统计、计划对比和性能诊断 |
| PL/SQL 编译、调试、单元测试、Profiler | 未见 | 若目标包含 PL/SQL Developer 等价能力，则是核心缺口 |
| 导入、导出、数据传输、结构/数据同步 | 有基础导出，其他未见 | 与 Navicat 完整能力仍有明显差距 |
| ER/数据模型、查询构建器 | 未见 | 完整数据库 IDE 的增强能力 |
| 备份恢复、调度、监控、报表 | 未见或由其他模块局部承担 | 不应宣称全量替代 Navicat/PLSQL Developer |

因此更准确的定位是：**V2 已经是可写的数据库工作台，不再是 V1 只读查看器；但它目前覆盖的是日常查询、轻量数据编辑和对象查看，不是完整 Oracle 开发调试或跨库管理套件。**

官方能力依据：[Navicat 产品功能](https://www.navicat.com/en/cn/component/content/article/69.html)、[Navicat 17 在线手册](https://www.navicat.com/manual/online_manual/en/navicat_17/linux_manual/)、[PL/SQL Developer SQL Window](https://www.allroundautomations.com/products/pl-sql-developer/features/sql-window/)、[PL/SQL Editor](https://www.allroundautomations.com/products/pl-sql-developer/features/pl-sql-editor/)、[Object Browser](https://www.allroundautomations.com/products/pl-sql-developer/features/object-browser/)、[完整功能](https://www.allroundautomations.com/products/pl-sql-developer/features/)。

## 自动化与构建

- `go test -mod=vendor ./internal/dbconsole ./internal/credentials ./internal/httpserver`：通过。
- `node web/app.test.js`：23/23 通过。
- `node tests/workbench-unit-tests.js`：全部通过。
- `node --check web/pages/database.js`：通过。
- `node tests/e2e/index.js --grep 数据库工作台`：7/9，通过 7，失败 2。
- `CGO_ENABLED=0 go build -mod=vendor .`：通过。
- Oracle 11g 集成测试：因没有 11g 环境而 SKIP，不能视为通过。

## 建议整改顺序

1. 统一 V2 模式与事务语义：顶部状态、网格编辑开关、SQL 自动提交/固定事务三者说清楚。
2. 修复只读网格双击/F2绕过，以及 DML/DDL 成功面板的红色错误样式。
3. 实现或移除 `Alt+O`，修复中文 IME 下 `Ctrl+Shift+E`。
4. 增加危险 SQL 警告、环境/账号/权限徽标和审计信息。
5. 重置 E2E 持久化状态，做到 9/9；补 V2 DDL/DML、提交/回滚、自动提交和取消后会话释放用例。
6. 优化管理面板、对象详情高度、控件尺寸、2K宽屏密度、标签与图标一致性。
7. 若目标是替代 Navicat / PL/SQL Developer，再单独规划 PL/SQL 编译调试、对象设计、导入同步、ER、可视计划等能力。

## 真实 Oracle 截图索引（19步）

目录：`output/database-workbench-oracle-v2-2026-09-05/screenshots/`

1. [V2 初始页](../output/database-workbench-oracle-v2-2026-09-05/screenshots/01-oracle-ready.png)
2. [Oracle 连接成功](../output/database-workbench-oracle-v2-2026-09-05/screenshots/02-connection-success.png)
3. [输入 SELECT](../output/database-workbench-oracle-v2-2026-09-05/screenshots/03-select-entered.png)
4. [真实查询结果](../output/database-workbench-oracle-v2-2026-09-05/screenshots/04-select-result.png)
5. [开启网格编辑](../output/database-workbench-oracle-v2-2026-09-05/screenshots/05-edit-mode.png)
6. [进入单元格编辑](../output/database-workbench-oracle-v2-2026-09-05/screenshots/06-cell-editing.png)
7. [保存待提交修改](../output/database-workbench-oracle-v2-2026-09-05/screenshots/07-cell-value-changed.png)
8. [第二次待提交修改](../output/database-workbench-oracle-v2-2026-09-05/screenshots/08-edit-pending.png)
9. [回滚网格修改](../output/database-workbench-oracle-v2-2026-09-05/screenshots/09-edit-rollback.png)
10. [输入 UPDATE](../output/database-workbench-oracle-v2-2026-09-05/screenshots/10-update-entered.png)
11. [UPDATE 成功但错误色](../output/database-workbench-oracle-v2-2026-09-05/screenshots/11-update-success.png)
12. [真实执行计划](../output/database-workbench-oracle-v2-2026-09-05/screenshots/12-explain-plan.png)
13. [对象字段](../output/database-workbench-oracle-v2-2026-09-05/screenshots/13-object-fields.png)
14. [对象索引](../output/database-workbench-oracle-v2-2026-09-05/screenshots/14-object-indexes.png)
15. [对象约束](../output/database-workbench-oracle-v2-2026-09-05/screenshots/15-object-constraints.png)
16. [对象 DDL](../output/database-workbench-oracle-v2-2026-09-05/screenshots/16-object-ddl.png)
17. [真实慢查询运行中](../output/database-workbench-oracle-v2-2026-09-05/screenshots/17-slow-query-running.png)
18. [慢查询已取消](../output/database-workbench-oracle-v2-2026-09-05/screenshots/18-slow-query-cancelled.png)
19. [2K 真实 Oracle 布局](../output/database-workbench-oracle-v2-2026-09-05/screenshots/19-2k-real-oracle.png)

真实运行原始结果：[results.json](../output/database-workbench-oracle-v2-2026-09-05/results.json)

## 补充前端与响应式截图

此前 22 步前端/响应式截图继续作为布局、右键、20,000 行和 8 页签证据使用，目录为 `output/database-workbench-audit-2026-09-05/screenshots/`。其中“凭据未保存”的截图只代表测试前状态，不再作为当前 Oracle 功能结论。

- [1080p 初始布局](../output/database-workbench-audit-2026-09-05/screenshots/01-1080p-initial.png)
- [只读状态双击仍编辑](../output/database-workbench-audit-2026-09-05/screenshots/18-readonly-double-click.png)
- [8 页签并发](../output/database-workbench-audit-2026-09-05/screenshots/19-eight-tab-concurrency.png)
- [2K 初始布局](../output/database-workbench-audit-2026-09-05/screenshots/20-2k-initial.png)
- [2K 数据源管理](../output/database-workbench-audit-2026-09-05/screenshots/21-2k-manager.png)
- [2K 浅色主题](../output/database-workbench-audit-2026-09-05/screenshots/22-2k-light-theme.png)
- [布局与性能 metrics.json](../output/database-workbench-audit-2026-09-05/metrics.json)

E2E 失败证据：

- [对象面板受持久化折叠状态影响](../test-results/screenshots/数据库工作台-已配置数据源时可查询_查看对象和执行计划-20260905-001349-988.png)
- [只读双击进入编辑](../test-results/screenshots/数据库工作台-结果网格虚拟滚动只渲染视口附近的行-20260905-001401-392.png)
