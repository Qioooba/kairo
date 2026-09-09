# Luna 串行测试交接（2026-09-09）

## 最新指令：单个模型，节省 token

用户准备在**当前窗口切换 Luna 极高（xhigh）**继续。最新要求覆盖旧分工：**不再创建或唤醒任何子 agent，不并行测试**。由当前一个 Luna 串行执行测试、必要修复及复测。原子 agent `luna_real_tests` 已被中断，不应继续调用它。

已有持续目标仍未完成，不要重建目标或标记完成。保留完整范围，按以下台账接着做，不从头重测。

**硬约束：只允许 Codex 内置浏览器交互。禁止系统鼠标、系统键盘、原生窗口控制、sky/computer-use、Chrome/Edge 扩展控制；不操作 UU 远程及其进程。** 用户使用 UU 远程，曾反馈鼠标被控制/消失。Shell 读写代码、构建、创建隔离夹具和核验文件仍可用。不要再次清理控制助手进程。

## 范围和权威文件

- 仓库：`D:/kairo`，PowerShell；HEAD `4b848da232bd0715d61174b0047c70427dda56cb`。
- 最近一周审核基线：`c1d0df941ae4b609d71e1bd6c0c3911e0b2ac24c` 至 HEAD，19 个提交、15 个非数据库页面模块。
- **排除数据库工作台**。仓库存在大量用户/其他工作的未提交修改，不回滚、不覆盖、不顺手整理。
- 原始 77 组测试要求：`docs/qa/2026-09-08-weekly-review-and-browser-test-plan.md`。
- 逐组进度：`docs/qa/2026-09-09-test-ledger.md`（验收依据）。
- 本线程修复说明：`docs/qa/2026-09-09-fixes-and-retest.md`。
- 真实证据：`tmp/qa-20260909/evidence/real-20260909-b0/`。目录名一直沿用 b0，里面实际包含 B1–B6 和工作区前端的结果，不能统称同一固定版本。
- **新发现的另一路工作记录**：`docs/qa/2026-09-09-security-review-followup.md`。它记录了额外的权限、路径、打包、配置等修复，应读完后确定新增复测范围；不要把这些修改归为本线程完成。

## 当前进度

最后实读台账：**17 PASS / 56 NOT RUN / 1 FAIL / 3 BLOCKED，共 77 组**。NOT RUN 含部分子项已完成，不等于完全没碰过；只在原组所有要求有证据时改 PASS。

已通过：CMP-01～08、CMP-11；WSC-01、04；NOTE-01；TASK-02、03；HTTP-01；AUX-01、02。

- CMP-09 部分：来源变更清除旧结果、选择和覆盖入口已过；取消、重开、离页及同步断连仍需完成。
- CMP-10 保留 FAIL：原覆盖漏洞已修复且定向复测通过，但 8 MiB 单长行编辑遇浏览器控制超时，整组未验收。
- CMP-12 正在执行，**尚未通过**，见下一节。
- NOTE-06、REM-03、CFG-03 为原生功能限制。继续其可在浏览器完成的子项，不启用原生控制。
- WAS-01～12 尚未实际完成。其他未通过组必须按原计划继续。

## 最后现场与优先下一步

1. **先处理构建与源码不一致。** 主服务仍是 B6（01:18 构建），当前 `internal/comparefs/local.go` 最后修改时间为 01:50，另一路安全修复明显晚于 B6。前端是热读工作区，后端仍是旧二进制。先读安全修复记录、检查相关差异与测试，再构建新的隔离 QA 版本，在无活动测试任务时只替换主 QA 服务。原发布/认证/升级实例也各是旧版本，按其验收需要更新；不能用旧实例结果证明最新代码全部通过。
2. **完成 CMP-12。** 最新真实动作：左路径 `D:/kairo/tmp/qa-outside-20260909` 扫描明确拒绝“路径不在 compare_allowed_roots 白名单内”；随后改为 `D:/kairo/tmp/qa-20260909/outside-link`（真实 Junction 指向前者），旧 B6 UI 却显示扫描完成。此结果需要核对请求路径、旧 B6 与新源码差异后重新验证，不能直接宣布最新代码有/无漏洞。此后已填 `type-left` / `type-right` 并点击扫描，**最后结果尚未读取**。
3. CMP-12 类型夹具：`type-left/clash/keep.txt` 内容 `KEEP DIR`，右侧 `type-right/clash` 是文件，内容 `KEEP FILE`。预期明确类型冲突、拒绝危险覆盖。外部 `tmp/qa-outside-20260909/keep.txt` SHA256 为 `1517A92427271FCC97B1C04797B849E2325246C93E63ECB04EB883A6B719F4E1`；右侧 clash SHA256 为 `1C7F8937BC59CF761BCB878E05DBEBE2631C641BDC26A08223908DB981AABC62`。还没有做任何越界写入/删除。
4. 完成 CMP-09 和 CMP-10 剩余子项，随后按页面集中做 WAS，再做文件/日志/SSH、WSC/WS 剩余、便笺/提醒/任务、配置/公共/升级/发布。不要为追求快速 PASS 缩减要求。

## 本线程修复与验证摘要

| 编号 | 修改 | 当前证据 |
|---|---|---|
| R1 | WSC 非静态引擎列表操作要求 admin | Go 回归；非 admin UI 仍需测 |
| R2 | SFTP 新文件排他创建，已有文件不截断 | Go 回归；真实 SFTP 重名流程待测 |
| R3 | 便笺 dirty 草稿不被 SSE 推进 revision | 已测“使用最新”；保留我的/复制两份待测 |
| R4 | 同步包含空目录 | CMP-06 真实文件/嵌套目录/哈希通过 |
| R5 | WSC 全量预检、暂存、排他发布、回滚 | 回归通过；完整 UI 失败发布流程待测 |
| R6 | 偏好读取不吞错误 | 回归通过；页面故障待测 |
| R7 | 显式无效 JDK 不静默回退 | 回归通过；工具页面待测 |
| R8 B2 | Webhook 非零 errcode 判失败并重试 | 业务失败一次端点真实重试成功；HTTP500及关闭告警未完 |
| R9 B3 | WS 失败缓存失效、写前检查格式版本 | Go 回归；升级和损坏数据 UI 未完 |
| R10 B4 | JAXWS/CXF/XFire 超时方法及 CXF 调用代码 | 六引擎真实 JDK6 编译通过 |
| R11 B5 | XFire WSDL 基准 URI 保留、相对 XSD 可解析 | 实际 SOAP 调用通过 |
| R12 B6 | 同步结果显示目录数量 | 真实空目录/文件数量与落盘通过 |
| R13 前端 | 已验证空目录清除 pending，深扫保留后端确认状态 | 本地/SFTP/FTP 深扫及逐层展开通过 |
| R14 前端 | 来源变化清除旧结果、取消旧请求、防止旧展开响应写回 | 路径与历史来源切换已过；最后补充“迟到 job_id 也取消”，仅语法检查，待取消场景复测 |
| R15 前端 P1 | 保存要求成功完整读取的 version，禁止失败读取后输入覆盖 | 超限与二进制输入后普通/菜单保存均 disabled，文件哈希不变；10 项 Node 回归通过 |

前端主要在 `web/pages/compare.js`；R15 测试 `tests/compare-folder-regression.js`。运行 `E:/Tools/nodejs/node.exe tests/compare-folder-regression.js`，当前 10 项通过。不要因为前端没改又反复运行相同测试。

R15 曾真实破坏**专用副本** `encoding/oversize-copy.txt`：由 8 MiB+1 B 写成 23 B，备份保留原文。保护复测使用 `oversize-protected.txt` 和 `binary-protected.bin`，均保持原哈希。`boundary-8m.txt` 是 8,388,608 个 A，完整加载已通过；大值 fill 和单长行按键测试超时，**没有成功保存**。另一页 20,001 行/208,900 字符输入及末尾追加 QA-END 后 20,002 行已通过。不要将控制超时推断为整个应用崩溃，也不要将未编辑成功当作 PASS。

## 已运行的隔离环境

以下进程在交接时实查存在；后续仍应按命令行核实，不凭 PID 盲目停止。

| 用途 | 地址/端口 | 进程/参数 |
|---|---|---|
| 主 QA B6 | 18109 | PID 38092，`tmp/qa-20260909/kairo-qa-b6.exe --config D:/kairo/tmp/qa-20260909/config.yaml --web-dir D:/kairo/web` |
| 认证 QA B1 | 18112 | PID 68396，`auth/config.yaml`，独立 auth/data |
| 发布包 B1 | 18113 | PID 32436，`release/Kairo_win10.exe`，无 web-dir，release-config.yaml |
| 正确旧版升级 B3 | 18116 | PID 35816，`upgrade-legacy-corrected/config.yaml` |
| SSH/SFTP/FTP/HTTP 夹具 | 18221/18222/18223/18110 | PID 49736，`services.py` |
| SOAP | 18111 | PID 81860，`soap-service.py` |
| 故障 Webhook | 18115 | PID 39636，`fault-http.py` |

- **不要访问或停止用户实例 18092/18095**。不要停止、清理 UU 或 computer-use 等系统助手进程。
- 仅重启自己核实的 QA 应用进程，后台启动用 `Start-Process -WindowStyle Hidden`。
- 主配置 compare_allowed_roots 仅 `D:/kairo/tmp/qa-20260909`；free_file_roots 为 `*`，开发隔离配置，不代表发布默认设置。
- SSH A：127.0.0.1:18221 `qa / qa-test-only`，根 ssh-a；B：18222 `qa / qa-other-only`，根 ssh-b。
- FTP：18223 `qa / qa-ftp-only`，根 ftp。18224 故意离线。
- 认证本地 token：`qa-user-20260909-only`、`qa-admin-20260909-only`（仅 QA 夹具）。
- HTTP `/hook`、`/leaderboard`、`/leaderboard-backup` 在 18110，本地投递日志 http-received.jsonl。
- SOAP 18111 `/soap`；WSDL `/SuLianLoanHengLiService.wsdl`，两个 XSD 同目录；记录 soap-received.jsonl。
- 18115 `/fail-once/<新名称>` 首次500再200；`/fail-always/<新名称>` 一直500；`/business-once/<新名称>` 首次errcode45009再0；`/business-always/<新名称>`一直业务失败。日志只记录请求/状态/次数，响应errcode需按夹具代码规则解释。
- 18114 旧升级夹具曾制错（信封只去版本，而真实旧格式是裸数组），不能当产品失败。正确实例为18116，已真实读取WS项目、SOAP成功、保存模板刷新保留；其他模块/重启未完。

工具路径：Go `E:/Tools/Go/bin/go.exe`，gofmt 同目录；Node `E:/Tools/nodejs/node.exe`；Python `C:/Users/Qi/AppData/Local/Programs/Python/Python38/python.exe`；Bash `E:/Tools/Git/bin/bash.exe`。Python 网络用无代理 urllib opener（系统代理不可靠）。不要重跑 `prepare.py` / `more-fixtures.py` 覆盖已有数据。

## 夹具与已发生的修改

- compare-left/right：编码、二进制、单侧、空目录；不同文件已有真实同步和备份。`qa-deep-proof.txt` 左ABCD/右WXYZ，4B、同时间；已检出差异。
- SFTP/FTP `/compare`：empty/nested、same.txt、different.txt。FTP different.txt 已被同步为AAAA，原CCCC在备份；target-only.txt 保留且哈希已核验。
- compare-large：**已恢复为50,001项**；f50000.txt 曾暂移以测试精确50,000，已移回，不要再当夹具丢失。
- depth-left/right：a/b/c/d/deep.txt 和 ignored.log，两项差异；三层截断、深扫和忽略log/清空过滤均已通过。
- encoding：三个编码文件已改为“中文已修改\n第二行\n”，均有正确备份；conflict.txt保留EXTERNAL KEEP；dual.txt保留FIRST PAGE WINS及DUAL BASE备份。
- was-app/was-batch 已有真实 JDK6 编译 Demo.class、Demo$1.class、Job.class；qa-input.war、invalid.xml、wsdl、java/lib 可用。还需按源码和夹具实际生成 WAS 清单，不能猜 tar 路径。
- JDK6/JDK8 在 `tmp/qa-20260909/java/`。六生成引擎实际 javac 成功；纯JDK/JAXWS/CXF/XFire SOAP实际调用成功。Axis1/2的原要求是编译，勿自动扩大到额外运行检查浪费 token。

## 内置浏览器与省 token 操作

- 当前 CUA 已读 API 文档。最后已确认的内置浏览器 ID 为 **3**；原 ID 1 失效后通过 `cua.getState()` 查到 3。不要硬编码长期有效。
- 现有标签：用户 commands 页不要关闭；QA tab3 是主要测试页（绑定 `qaOther`），刚发起 type-left/right 比较；QA tab7（绑定 `qaCurrent`）为8MiB单行现场，曾出现输入超时。旧 QA tab5/6 和后台tab8已关闭。
- 优先复用 tab3；需要加载新前端用 **`tab.reload()`**，同URL `goto`不能当作已经加载新脚本的证据。
- 首次进入工具/句柄失效按 CUA 要求获取实际标签；只选 type=iab，绝不回退到Chrome扩展或原生鼠标。
- **大文本页不要打印全页快照**，`getTab`可能自动输出超长AX内容。优先复用句柄，以只读 DOM evaluate 返回字符数/末字符/按钮状态。不得调用页面隐藏业务状态或直接接口替代真实UI操作。
- 可用：`tab.playwright.getByRole(...).click/fill/check/selectOption/press/type`；DOM `evaluate`只读；`tab.reload/close/markHandoff`。`inputValue()`不可用。键盘 `press`仅作用于内置浏览器元素，不能使用系统键盘工具。
- `fill('')`曾没有清空值，需真实 `Control+A` + `Backspace`，并读实际值确认。路径两次被额外追加字符，打开后要核对路径和文件内容；不把输入干扰记作代码缺陷。
- 窄窗口侧栏链接可能在DOM存在但实际菜单关闭：先点“切换菜单”再点链接。浮层“更多操作”打开时可能挡住合并按钮，关闭后再点。
- 当前无需新增标签。只在双页面明确场景时开第二页，完成后关闭自己创建的页；不要清理用户标签。
- 批量同页用例，单次操作后只取下一步所需DOM/状态；不要重复整页快照、重读长历史、重复跑已过测试。具体失败写证据文件，聊天只报新增问题/阶段进度。
- 浏览器下载可用 waitForEvent('download')；浏览器文件上传先按工具文档读取 file-uploads，使用文件选择器API，不能动系统文件对话框。仅原生可测的子项保留阻塞。

## 接手后的第一段工作

读本文件 → 台账和原计划相关组 → 新安全修复记录 → 核对并构建当前 QA 后端 → 复测 CMP-12 和安全修改影响项。继续单 Luna 串行推进，不唤醒被中断的子 agent。每完成一组就更新台账；所有允许范围完成之前保持目标 active。

## 2026-09-09 继续执行补充

- 当前已按单模型串行继续，未唤醒或新增 agent。当前隔离后端为 B7：`127.0.0.1:18117`，PID 19616，`tmp/qa-20260909/kairo-qa-b7.exe`；仅使用 Codex In-app Browser。
- WAS-01/02/03/04/05/06/07/08/10 已真实复测并更新为 PASS；WAS-09 保留 NOT RUN（仅 ZIP 占用失败已测，完整部分成功注入未完成）；WAS-11 标记 BLOCKED（iab 的 `window.open` 交接未出现可操作新标签）；WAS-12 保留 NOT RUN（并发/危险路径尚未完成）。证据：`tmp/qa-20260909/evidence/real-20260909-b0/ui-waspack.md`。
- `web/pages/waspack.js` 已修复重复投产清单静默去重：前端明确定位重复行并阻止预检/生成，校验失败状态文案也已修正。全量 `web/app.test.js` 仍只有数据库工作台既有 LOB 测试失败，按要求不修改数据库代码。
- B7 追加完成 WebService 导入/真实 SOAP 发送与 Mock 启停（WS-01/02 PASS）、HTTP 回显请求和用例持久化（HTTP-02 PASS）、文件下载 SSH-A 浏览/预览（FILE-01 PASS）、日志多目标搜索部分失败（LOG-01/02 PASS）、SSH-A 双标签命令执行（SSH-01 PASS）。证据分别在 `ui-webservice.md`、`ui-http.md`、`ui-files.md`、`ui-logs.md`、`ui-ssh.md`。
- 最新台账计数：PASS 34、FAIL 1、BLOCKED 4、NOT RUN 38。当前 iab 测试标签已标记 handoff；不要切换到 Chrome/Edge 或原生控制。
