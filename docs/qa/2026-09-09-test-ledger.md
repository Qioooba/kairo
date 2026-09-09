# 2026-09-09 内置浏览器真实测试台账

基线：`4b848da232bd0715d61174b0047c70427dda56cb`（专用 QA 构建）  
实例：`http://127.0.0.1:18109`  
配置/数据/日志：`D:\\kairo\\tmp\\qa-20260909`  
运行编号：`real-20260909-b0`  
浏览器：Codex In-app Browser（iab，标签页已打开；子代理环境无法将 iab 设为前台可见）  
证据目录：`D:\\kairo\\tmp\\qa-20260909\\evidence\\real-20260909-b0`

状态只使用 `PASS / FAIL / BLOCKED / NOT RUN`。当前台账先记录全量 77 组，按实际点击结果逐项更新；数据库工作台场景不纳入本轮。

| ID | 状态 | 证据路径 | 阻塞/最短记录 |
|---|---|---|---|
| CMP-01 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-compare.md` | 文本比较、差异定位/视图、单行双向合并与重新比较归零均通过 |
| CMP-02 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-compare.md` | 单行与整块向左/向右合并均真实执行，重新比较归零，目标内容和未差异行核验正确 |
| CMP-03 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-encoding.md` | BOM/UTF-8/GBK 文件真实编辑保存，CRLF/LF、BOM 与原文件备份哈希均核验 |
| CMP-04 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-encoding.md` | 外部修改后保存明确拒绝，dirty 草稿保留，外部文件 SHA256 不变 |
| CMP-05 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-compare.md` | 本地、SFTP、FTP 来源测试与浅扫/展开/不限深度均通过；同大小同时间不同内容实测识别，空目录验证完成，无完整性 alert |
| CMP-06 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-compare.md` | 空目录、嵌套空目录、目录计数、单文件复制/备份、含文件整目录、目标独有文件保留均通过；源/目标实际哈希已核验 |
| CMP-07 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-compare.md` | SFTP→FTP 批量同步 1 文件成功，覆盖备份可读，目标独有文件哈希保留，自动重扫结果一致2/不同0/仅右1 |
| CMP-08 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-compare.md` | 50,001 截断保护、恰好50,000完成、深度限制、忽略后缀和第五层深扫均通过；批量入口按未完成状态禁用，夹具已恢复 |
| CMP-09 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-compare.md` | R14 路径变更清除旧结果/覆盖入口、旧 job 取消、序列响应回归已通过；B7 真实取消大目录任务后显示“已取消”，再次开始得到独立截断结果；离页、同步中受控断连仍未完成 |
| CMP-10 | FAIL | `tmp/qa-20260909/evidence/real-20260909-b0/ui-encoding.md` | R15 原失败已由定向复测确认修复（超限/二进制填充后保存 disabled，8MiB 边界读取准确，20,001 行可编辑）；8MiB 单行编辑遇 iab/CDP deadline 未保存，完整大文本/二进制/大量行验收未完成，保留原真实失败待完整复测 |
| CMP-11 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-encoding.md` | 两真实页面并发保存，先保存成功，旧版本保存拒绝并保留 dirty 草稿，当前文件与备份哈希核验 |
| CMP-12 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-compare.md` | B7 真实浏览器拒绝 Junction 越界访问；同名目录/文件类型冲突明确显示且禁用选择与复制，沙箱外与目标冲突文件哈希保持不变 |
| WAS-01 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md` | 应用模式预检/生成通过，Java/Class/JSP 与内部类配对；tar、ZIP、list、脚本路径和 SHA256 已核验 |
| WAS-02 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md` | 批量模式真实预检通过，Java/Class/XML/shell 相对路径和部署根配置正确 |
| WAS-03 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md` | 空清单、缺失文件、越界路径和重复行均明确阻止；重复行前端校验已修复并复测 |
| WAS-04 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md` | 空输出目录真实生成 tar+ZIP，解包内容、list、脚本和 SHA256 交叉核验一致 |
| WAS-05 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md` | 默认严格策略拒绝非空目录，旧用户文件哈希保持不变 |
| WAS-06 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md` | 清理 Kairo 已知产物成功，用户文件原样保留且哈希不变 |
| WAS-07 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md` | replace 取消零修改；确认后实际目标被清空并生成当前产物 |
| WAS-08 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md` | WAR 抽取、修改 war、打包修改内容及输入变化使阶段凭据失效均通过 |
| WAS-09 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md` | 同名 ZIP 占用时真实失败且 tar 保留、失败历史可见；完整“部分成功”构建注入仍未完成 |
| WAS-10 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md` | 历史详情字段/哈希正确，删除记录不删产物已真实核验 |
| WAS-11 | BLOCKED | `tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md` | 不同输出目录的比较按钮可用；iab 当前 window.open 交接未出现可操作新标签，需支持弹窗的 iab 环境复测 |
| WAS-12 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md` | 仅完成受控失败/覆盖取消；同目录并发发布和危险路径沙箱映射未完成 |
| WSC-01 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-wscodegen.md` | UI 导入本地隔离 URL WSDL、保存 `qa-wsdl-real`，解析 1 operation/关键字段，点击生成 Java 跳转带 project 参数 |
| WSC-02 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-wscodegen.md` | B7 URL 来源真实预览成功；本地文件/粘贴/已导入项目四来源一致性尚未全部执行 |
| WSC-03 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-wscodegen.md` | B7 URL 纯 JDK 预览并点击下载，页面报告 8 文件；iab 下载事件未暴露 ZIP 文件，六引擎 ZIP 解包核验未完成 |
| WSC-04 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-wscodegen.md` | B4 六套生成源码均用真实 JDK6 与隔离 `java/lib` 编译通过；纯 JDK、JAX-WS、CXF 与 B5 XFire 均真实运行并调用 18111 SOAP，返回 `SuLianLoanHengLiResponse`。XFire 运行前补齐 XmlSchema/JDOM/commons-httpclient/commons-codec 依赖，过程日志保留 |
| WSC-05 | NOT RUN | — | 等待多 JDK 夹具 |
| WSC-06 | NOT RUN | — | 等待多 JDK 夹具 |
| WSC-07 | NOT RUN | — | 等待 WSDL/XSD 夹具 |
| WSC-08 | NOT RUN | — | 需非 admin 隔离账号 |
| WSC-09 | NOT RUN | — | 等待错误 WSDL 夹具 |
| WSC-10 | NOT RUN | — | 等待 WSDL/XSD 夹具 |
| WS-01 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-webservice.md` | B7 真实导入 WSDL、选择 operation、生成 SOAP 报文并发送到 18111，收到 200/returnCode 0000，历史计数增加 |
| WS-02 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-webservice.md` | B7 真实创建 Mock、测试访问 200、查看请求记录；停用后访问 404，路由停止生效 |
| FILE-01 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-files.md` | B7 真实连接 QA-SSH-A、浏览 /logs、列表与远端一致，预览 SystemOut.log 内容/字节数正确 |
| FILE-02 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-files.md` | 新建文件入口触发原生 prompt，iab 无 prompt 接管；真实新建/目录/刷新未完成 |
| FILE-03 | NOT RUN | — | 等待专用 SSH/SFTP 夹具 |
| FILE-04 | NOT RUN | — | 等待专用 SSH/SFTP 夹具 |
| FILE-05 | NOT RUN | — | 等待专用 SSH/SFTP 夹具 |
| FILE-06 | NOT RUN | — | 等待专用 SSH/SFTP 夹具 |
| LOG-01 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-logs.md` | B7 真实勾选 A/B/FTP/离线服务器，测试连接显示 A/B 独立成功、错误目标明确失败 |
| LOG-02 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-logs.md` | B7 多目标搜索 needle 返回 A/B 命中及 FTP handshake/OFFLINE refused，2/4 成功且行号正确 |
| LOG-03 | NOT RUN | — | 等待日志夹具 |
| LOG-04 | NOT RUN | — | 等待 Tail 夹具 |
| SSH-01 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-ssh.md` | B7 真实 SSH-A shell 执行命令成功；同主机双击产生独立第二标签 |
| SSH-02 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-ssh.md` | 会话关闭/重连/独立窗口完整子项尚未完成 |
| NOTE-01 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-notes.md` | 新建/蓝色/保存、行内取消、归档→已归档筛选→恢复、更多→删除确认、刷新后空列表均通过 |
| NOTE-02 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-notes.md` | B7 已真实归档/恢复及更多菜单；提醒、滚动和完整筛选子项尚未完成 |
| NOTE-03 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-notes.md` | B7 已过页面悬浮编辑、跨页刷新持久化；断连重连尚未完成 |
| NOTE-04 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-notes.md` | B1 已验证冲突提示和“使用最新版本”；原计划还需分别执行“保留我的版本”和“复制两份” |
| NOTE-05 | NOT RUN | — | 本批次 |
| NOTE-06 | BLOCKED | — | 桌面便笺/原生窗口需可见原生控制；浏览器部分另记 |
| REM-01 | NOT RUN | — | 需从便笺来源创建快照提醒并修改原便笺 |
| REM-02 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-notes.md` | 已过单次提醒创建；周/月/Cron、编辑、禁用、实际触发尚未完成 |
| REM-03 | BLOCKED | — | 原生文件选择框需原生控制 |
| TASK-01 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-tasks.md` | B7 已新增并真实执行成功任务；删除触发 iab 无法接管的 JavaScript confirm，删除与重启持久化仍未完成，任务暂保留 |
| TASK-02 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-tasks.md` | `task-fail.cmd` UI 显示失败/退出码 7；`task-slow.cmd` UI 显示 5s 超时/exit -1；执行中按钮变为禁用“运行中…”且删除禁用，日志仅有 QA_STARTED 无 QA_FINISHED |
| TASK-03 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-tasks.md` | 保存失败告警开关与本地 Webhook；测试通知 UI 显示已发送；留空保存仍显示已配置；勾选显式清除后显示未配置；桌面通知已关闭避免原生外发 |
| TASK-04 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-tasks.md` | R8 业务失败一次端点已真实投递两次并按夹具规则从 45009 转 0；尚未完成关闭告警后的零投递复测、HTTP 500 重试及原生桌面通知 |
| CFG-01 | NOT RUN | — | 本轮先做只读导航，避免配置写入 |
| CFG-02 | NOT RUN | — | 本轮先做只读导航，避免配置写入 |
| CFG-03 | BLOCKED | — | 原生文件选择框需原生控制 |
| HTTP-01 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-http.md` | UI 选择 POST，发送至隔离 `127.0.0.1:18110/hook`；断言 200 通过，响应显示方法 POST、路径 /hook 和 JSON 正文；用例保存为 qa-real/real-http-20260909 |
| HTTP-02 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-http.md` | B7 真实请求 18110 leaderboard 返回 200/JSON，保存 qa-http-b7 后离页再进仍可见 |
| AUX-01 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-public-navigation.md` | iab 真实点击验证首页、日志、文件、打包、SSH、HTTP、WebService、WSC、配置、下载、formatter、timestamp、cron、JSONPath、compare、commands、notes、diagnostics、about；未访问数据库 |
| AUX-02 | PASS | `tmp/qa-20260909/evidence/real-20260909-b0/ui-public-navigation.md` | B1 诊断页显示 4 台 server 中 3 台 TCP 可达（18221/18222/18223），并明确报告隔离的 QA-OFFLINE 18224 拒绝；下载历史显示 0 个文件/0 B；formatter/timestamp/cron/JSONPath/commands 有确定输出 |
| AUX-03 | NOT RUN | — | 赞助服务需专用接收器 |
| PUB-01 | NOT RUN | — | B1 真启动后继续 Esc/关闭/Tab/窄屏 |
| PUB-02 | NOT RUN | — | 需视口批次 |
| PUB-03 | NOT RUN | — | 需认证账号夹具 |
| PUB-04 | NOT RUN | — | 需故障注入/重启批次 |
| UPG-01 | NOT RUN | `tmp/qa-20260909/evidence/real-20260909-b0/ui-upgrade.md` | 18114 夹具错误已排除；18116 正确旧数组副本已加载 WS 项目、真实调用 SOAP、保存模板并刷新后保留。配置/便笺/提醒/任务/凭据/HTTP 的完整读写与重启持久化尚未全部完成 |
| UPG-02 | NOT RUN | — | 需损坏/未来版本副本 |
| UPG-03 | NOT RUN | — | 需多模式副本 |
| REL-01 | NOT RUN | — | 需隔离构建/发布包 |

## 环境探测

## 定向回归

- R13/R15 compare 回归：`node tests/compare-folder-regression.js` 输出 `compare-folder-regression: 10 cases passed`，`node --check` 通过。覆盖未加载空目录保持 `pending`、已加载空目录变 `same`、嵌套空目录向上汇总 `same`、子差异保持 `different`、类型冲突/错误不被改写，以及无 version/读取失败/文本源/并发 saving 不可写和正常 versioned save 保护；CMP-05 的真实远程页面复测已在 `ui-compare.md` 记录。

- 专用服务：初次 PID 71840 因缺少 `systems` 退出；父代理已补齐隔离配置并重启，B1 当前监听 `127.0.0.1:18109`（PID 38532，以父代理确认值为准）。`18110` 赞助夹具已备妥，服务日志：`tmp/qa-20260909/logs/kairo.log`。
- 前端：QA 服务使用 `--web-dir D:/kairo/web` 热读取工作区前端；因此本轮页面结果记录为“修复前后端 + 工作区前端”的 B0 状态，不能当作固定发布包结果。
- iab：可创建并读取页面；Codex 子代理中 `visible:true` 被平台拒绝，已保留后台 iab 标签并将此原生可见性限制记为环境事实。核心页面动作仍由 iab 用户界面完成，不使用直接接口替代。
- 现有 18092/18095 实例属于用户/其他审查实例，本轮不访问。
- 本轮不启动、不连接数据库服务。
