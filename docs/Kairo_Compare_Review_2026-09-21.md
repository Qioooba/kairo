# Kairo 文件与文件夹比对：代码审查与适度优化方案

审查日期：2026-09-21  
仓库：[Qioooba/kairo](https://github.com/Qioooba/kairo)  
固定基线：[38df3aa604817955f470eaaaa0eb4c29124972c4](https://github.com/Qioooba/kairo/commit/38df3aa604817955f470eaaaa0eb4c29124972c4)（main 在本次取证时的提交）  
目标：让文件、目录比较结果可靠，操作直观、连续，并控制改造范围。

## 1. 总体判断

**有明显优化空间，现有页面结构可以保留。优先修复比较对象、内容合并、撤销和覆盖范围的正确性，再精简入口与操作反馈。**

当前已具备两种比较标签、左右来源、树形结果、虚拟滚动、差异导航、查找、行/块/批量合并、显式保存、覆盖预览、备份、进度及取消等基础。继续增加高级功能的收益，低于把这些已有能力衔接正确的收益。

建议分三阶段：

1. **Phase 1：结果可信、内容不丢、写入范围正确。**
2. **Phase 2：搜索、导航、撤销、选择、返回目录连续可用。**
3. **Phase 3：精简工具栏、统一文案和状态、核对常用屏幕布局。**

本轮为审查与实施方案，未修改或提交产品代码。

## 2. 证据范围与验证边界

### 2.1 已读取的主要实现

| 范围 | 文件 | 主要职责 |
|---|---|---|
| 页面主实现 | [web/pages/compare.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js) | 来源、编辑器、差异结果、目录树、复制/覆盖预览 |
| 页面样式 | [compare-workbench.css](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare-workbench.css)、[style.css](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/style.css) | 控件、布局、响应式和状态展示 |
| 目录/读写后端 | [handlers_compare_workbench.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_compare_workbench.go) | 扫描、按需展开、内容验证、复制、批量同步 |
| 异步任务 | [compare_jobs.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/compare_jobs.go) | 任务状态、并发限制和取消 |
| 文件系统适配 | [internal/comparefs](https://github.com/Qioooba/kairo/tree/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/comparefs) | Local、SFTP、FTP/FTPS、版本检查和原子写入 |
| 文本比较协议 | [handlers_diff.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_diff.go) | 忽略规则、原始行号、请求限制 |
| 差异算法 | [internal/diff/diff.go](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/diff/diff.go) | 行差异与 Unified Diff 输出 |
| 关闭保护 | [app.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/app.js)、[tabs.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/tabs.js) | 页面保活、关闭守卫、beforeunload |

### 2.2 实际执行结果

| 验证 | 实际结果 | 能证明的范围 |
|---|---|---|
| 原 `tests/compare-folder-regression.js` | 退出码 0，脚本报告 13 cases passed | Node VM 中的页面纯函数 |
| 原 `internal/diff` 单元测试 | 14 个顶层测试通过，含 500 组随机输入重建 | 差异算法已有测试 |
| 原 Local FS 单元测试 | 5 个顶层测试通过 | 本地原子写入、版本冲突、备份、时间戳、路径 |
| 后端原函数隔离复现 | 10 个案例：8 个期望行为断言失败、2 个正向保护通过 | 捕获当前实现缺陷；不等于原项目测试有 8 个失败 |
| 忽略空行后的合并 | 已复现未选空行及末尾换行丢失 | 原前端函数与真实协议形状，无浏览器 |
| 导出补丁与 Git 兼容性 | 间隔 7/8 个相同行的双差异块被 `git apply --check` 拒绝；9/10 行通过 | 原算法的特定多块补丁输出 |

后端隔离复现直接提取原 `performCompareScan`、`performCompareSync`、walker、比较/复制函数，并使用原 Local FS；仅替换本地文件系统工厂、审计输出与错误清洗依赖。它能复现函数行为，不能代替完整 HTTP 服务、鉴权或真实远程联机测试。

**本轮没有完成浏览器实点、截图和 Windows/SFTP/FTP 端到端验收。** 浏览器安全策略拒绝打开本地测试页面，未绕过该限制。页面问题以下分别标明源码确认或设计建议；不把 CSS 推导写成已经目测的像素缺陷。

优先级：P0 表示可能意外改写或丢失内容，应先修；P1 表示错误比较、范围或核心交互问题；P2 表示次要兼容和体验改进。这些是本次修复排序，不是已有线上事故记录。

## 3. 应优先修复的功能问题

### F01 / P0：忽略空行后合并一处，会删除未选中的空行

**状态：已执行原前端函数最小复现。**

输入用转义表示：

```text
左侧：alpha\n\nold\ntail\n
右侧：alpha\n\nnew\ntail\n
```

开启“忽略空行”，仅将 old/new 这一行从左合并到右：

```text
期望：alpha\n\nold\ntail\n
实际：alpha\nold\ntail
```

中间空行和末尾换行一起丢失。问题不仅影响显示，合并后的缓冲区若保存，文件就会产生额外修改。

原因：后端 `prepareLines` 过滤空行但保留原始行号；前端 `buildAlignedRows` 只遍历返回行；`reconstructTargetLines` 再从这个不完整列表重建目标全文。单行、整块和批量合并都调用该路径。

**最小实现方案：**比较数据只用于定位差异。编辑时从完整目标文本出发，按照原始行号和范围执行局部替换；未选范围原样保留。保留原文件的末尾换行信息，不能把过滤后的显示行当成保存数据源。批量范围按原始位置从后向前应用，防止前面的替换使后面位置偏移。

验收：忽略空行、行尾空格、大小写分别与单行/整块/批量合并组合测试；断言完整结果，尤其是未选范围，而非仅检查所选行是否改变。

证据：[handlers_diff.go：prepareLines](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_diff.go#L128-L160)、[compare.js：合并入口](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1244-L1289)、[行模型与全文重建](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1378-L1419)。

### F02 / P0：过期差异仍可合并，可能覆盖刚输入的内容

**状态：源码确认，尚未浏览器实点。**

编辑后 `markDirty` 增加 `editSeq` 并设置 `diffStale`；但 `applyHunk/Line/Batch` 不检查该状态，仍用旧的 `alignedRows` 重建目标文本。自动比较存在防抖窗口；大文本暂停自动比较时，旧结果可操作的时间会更长。

还存在响应时序问题：比较请求返回时只检查 `requestNo === compareSeq`；用户编辑并未立即递增 compareSeq。旧响应可在下一次防抖请求发出前返回，并重新将 `diffStale` 置为 false。因此只在合并入口加 `if (diffStale)` 不够。

**最小实现方案：**发出请求时记录左右 `editSeq` 和来源身份；接收响应时核对当前版本；所有合并入口再次核对。旧结果可暂时保留供查看，但合并按钮失效并提示“内容已修改，请重新比较”。无需增加服务端编辑会话。

验收：请求在途时输入、快速连续编辑、超过自动比较阈值后三种场景，旧响应不能重新授权合并，未选中的新输入不能丢失。

证据：[markDirty](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L907-L923)、[接收比较响应](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1094-L1175)、[合并入口](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1244-L1289)。

### F03 / P0：换文件后撤回，可能把上一对文件内容写入新文件

**状态：源码确认，尚未浏览器实点。**

工作台 `undoStack` 只保存左右文本和动作名，没有来源/会话身份。换源时仅重置各编辑器自己的 history，未清空工作台撤回栈。

可达流程：A/B 做一次合并 → 载入 C/D → 点“撤回” → A/B 快照进入 C/D 编辑器，但 source 和 version 仍属于 C/D → 保存可能把 A/B 内容写到 C/D。

交换左右也把来源一起交换，而撤回仅恢复文本，存在身份与内容错配的同类风险。

**最小实现方案：**更换任一来源即清空合并撤回栈并禁用撤回；快照加一个轻量的比较会话标识，应用前检查。交换动作若可撤回，必须同时恢复来源、版本、baseline 与文本；也可以先不把“交换左右”放入普通文本撤回栈，保持明确行为。

验收：A/B 合并后载入 C/D，撤回不得恢复 A/B；交换左右及撤销后，路径、内容、版本始终对应。

证据：[快照与撤回](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L670-L709)、[loadSide](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L935-L990)、[交换及 loadPair](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L1301-L1339)。

### F04 / P0：更换来源、查看下一对文件、关闭页面缺少未保存保护

**状态：源码确认，尚未浏览器实点。**

“打开”“载入并比对”和文件夹双击文件，都能直接进入 `loadSide`，清空文本、历史和 dirty。已有 TabManager 关闭守卫没有接入 compare 的脏状态。

**最小实现方案：**复用已有关闭守卫和模态框，在真正替换有修改的缓冲区或关闭应用内标签时提示“保存后继续 / 放弃修改 / 取消”。普通切换内部两个标签无需提示，现有保活继续保留。任一保存失败都不得继续换源；不能在用户决定之前清空原编辑内容。关闭/刷新浏览器时接入 `beforeunload` 原生离开确认；不能承诺自定义三按钮，也不能依赖卸载阶段异步保存完成。

验收：单侧/双侧 dirty；换源成功/失败和应用内关闭的三个选择分支；浏览器关闭/刷新有原生离开确认，取消离开后原内容仍在。

证据：[打开入口](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L589-L605)、[从目录查看文件](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L3321-L3331)、[既有关闭守卫](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/app.js#L47-L115)。

### F05 / P0：覆盖预览与实际执行范围不一致

**状态：忽略规则丢失已用原 Go 函数复现；预览取消子项的界面路径为源码确认。**

前端预览允许父目录与子文件分别取消勾选；后台只要收到父目录，就再次完整遍历该目录。遍历使用空的 `compareScanReq{}`，没有保留原扫描的自定义忽略扩展名/目录规则。后台还会跳过已被父目录覆盖的显式子文件项。

结果：

1. 扫描忽略 `.log` 后选择父目录覆盖，原函数实测 `.log` 仍被复制，`copied=2`。
2. 保留父目录、取消某子文件，执行时父目录展开仍可把子文件纳入。
3. 预览显示“目录，文件数待展开”，却同时使用“完整预览”提示，用户无法核实实际写入量。

**最小实现方案：**让已有预览真正生成最终文件清单。父目录负责组织和批选；执行请求只包含用户最终选择的具体文件，以及确需创建的空目录。请求通过简单的 `operation` 或 `kind` 区分复制文件和仅创建目录；空目录项只创建目录，不能再走现有递归展开分支。后端按明确清单执行，不再次悄悄扩大目录范围。超出扫描上限则明确要求缩小范围；不需要引入双向同步规则引擎或持久任务平台。

若第一阶段尚无法生成完整清单，可暂时禁用尚未展开的整目录覆盖；已核验的具体文件仍可按原能力处理。

验收：忽略 `.log`、取消子文件、选择重叠父子项、空目录、目标独有文件；预览文件数与实际处理数一致，被排除文件逐字不变。

证据：[前端预览及执行请求](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L3178-L3254)、[后台父目录展开](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_compare_workbench.go#L552-L612)。

### F06 / P0：父目录覆盖丢失原比较版本；原本缺失的目标也可能被覆盖

**状态：原 Go 函数已复现。**

两个场景：

- 请求同时有父目录和携带旧 `expected` 的子文件；后台父目录重新展开后，使用执行时获取的新快照，子文件旧版本约束被跳过。目标已被外部修改仍被覆盖，实测 `conflicts=0`。
- 扫描时目标不存在，用户确认前有人创建了该目标；前端传 `expected:null`，后台执行时重新采集并把新文件作为可覆盖目标，实测仍覆盖而无冲突。

已有 `ExpectedMissing` 和版本保护值得保留。问题在于“缺失”和“版本”的基准被拖到执行阶段，而非用户看到的比较/预览阶段。

**最小实现方案：**每个最终清单项携带扫描/预览时的明确目标状态：`expected` 或 `expected_missing:true`，不能把 null 同时解释为“已知不存在”和“不检查”。当前 `compareSyncItem` 尚无 `expected_missing` 字段，应贯穿前端请求、请求结构与已有 `WriteOptions.ExpectedMissing`。后端不以新快照覆盖用户确认的版本。来源版本也应与预览文件身份对应；已有复制过程的源变更检查继续保留。

验收：目标执行前新建、已存在目标被修改、父子项重叠、复制期间改变源文件；逐项显示冲突并保留外部修改。

证据：[版本数据结构](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/comparefs/comparefs.go#L47-L78)、[同步计划版本生成](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_compare_workbench.go#L599-L627)、[前端 expected](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L3160-L3175)。

### F07 / P1：两目录各仅一个异名文件，会被强行配对

**状态：原 Go 函数已复现。**

左目录只有 `a.txt`，右目录只有 `b.txt`，内容相同。期望是“仅左 a.txt、仅右 b.txt”；实际得到一条 `same`、`Hashed:true`。

`performCompareScan` 仅判断左右映射各一条、该条目不是目录、名字不同，就用左名字作为共同 key。它没有确认用户选择的根对象本身是两个文件。

**最小实现方案：**只有两侧根对象 Stat 都明确是文件，才允许异名文件互比；目录比较严格使用相对路径配对。不要顺手增加自动重命名识别。

验收：异名同内容、异名异内容、同名不同内容、文件根对文件根、目录过滤后各剩一个文件。

证据：[错误配对分支](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_compare_workbench.go#L1084-L1103)。

### F08 / P1：永久忽略 target/dist/build/vendor 等目录，不适合通用文件夹比对

**状态：源码确认。**

`ignoreCompareEntry` 内置 `.git`、`node_modules`、`__pycache__`、`target`、`dist`、`build`、`.idea`、`.vscode`、`.svn`、`vendor`、`.next`、`.nuxt`，请求只能追加，不能取消。

对源码浏览，排除依赖/缓存可能有用；对发布包、编译产物或备份核验，`target/dist/build/vendor` 可能就是要核验的内容。当前页面没有把这些排除作为可见、可取消的有效规则展示。

**最小实现方案：**默认比较用户选定范围内的内容；常见缓存/依赖目录排除做成扫描设置里的简单可选项，并显示正在生效的排除规则。`target/dist/build/vendor` 不应永久硬排除。无需通用 DSL 或复杂规则管理器。

验收：两个目录只有 `dist/app.js` 不同，也必须能够被用户比较出来；排除打开/关闭结果与说明一致。

证据：[内置忽略列表](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_compare_workbench.go#L1495-L1523)。

### F09 / P1：历史路径已恢复，内容未加载，却可以点“开始比较”

**状态：源码确认，尚未浏览器实点。**

恢复偏好时 source 有文件路径、`loadState` 却直接为 ready；新编辑器为空，没有自动执行文件读取。主“开始比较”读 textarea 内容，只有另一“载入并比对”按钮才读文件。

可出现路径看上去正确，但实际比较两个空字符串的流程。这同时是错误状态与重复入口问题。

**最小实现方案：**一个主“开始比较”入口。非文本来源初始化为 `unloaded`；比较前 `ensureLoaded`，有未保存编辑时使用当前缓冲区并先遵守 F04。区分正在编辑的已加载来源和输入栏里待应用的新路径。读取失败不能用空内容生成一致结论。

同组问题：“载入并比对”无条件把来源改为 local。应只更新当前来源 path，保留 SFTP/FTP 的协议和端点；若用户确要改本地，通过明确动作完成。空路径也应同步清空旧快捷栏，防止残留身份。

证据：[初始状态](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L494-L499)、[两个不同动作入口](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L589-L607)、[无条件本地来源](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L792-L818)。

### F10 / P1：错误、未扫描、无匹配和一致必须分开表达

**状态：页面分支为源码确认；读取错误被误报差异已用原 Go 函数复现。**

页面：

- 导航徽章只数当前过滤可见差异，零项就写“两侧一致”。真实有差异时，搜索不存在文件也会得到该徽章。
- 列表空态有 `different` 且无搜索词的条件，搜索非空时已经会说无匹配；但该空态未检查 integrity，仍可能和未完成/截断告警并列出现“完全一致”。
- 元数据模式只能说明大小和时间在阈值内相符，不能表达全文内容一致。文件行的 `folderStatusText` 已实现“元数据相同（未核验内容）”，应保留；需要修的是顶部徽章、空态和总体结论的统一，而不是重新增加一个已有标签。

后端：流式比较某一侧读取报错、双方实际读取长度不同时，可先走“不相同”分支而返回 `err=nil`。原函数注入 reader 错误已复现。应先检查非 EOF 读取错误，再决定内容是否不同。

**最小实现方案：**状态判断统一生成：未比较、正在比较、待验证、比较失败、当前筛选无匹配、元数据相符、已核验范围无差异。只有内容验证完整且无错误时才能显示内容一致。筛选数量不能替代扫描结论。

证据：[页面空态与状态](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L3010-L3100)、[已存在的元数据标签](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L3596-L3604)、[流式比较](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_compare_workbench.go#L1650-L1677)。

## 4. 页面与操作：应做的小幅调整

### 4.1 保留并完善现有能力

| 已有能力 | 建议 |
|---|---|
| 文本/文件、文件夹两个标签 | 保留，名称可简化为“文件/文本”“文件夹” |
| 左右来源与历史来源 | 保留，始终显示协议、主机和路径的归属 |
| 默认只看目录差异 | 保留，同时让未验证范围可见 |
| 文本与目录虚拟滚动 | 保留，修复选择与复制对完整数据模型的访问 |
| 差异导航、查找、同步滚动 | 保留，结果无效时正确禁用写入动作 |
| 覆盖预览与备份 | 保留，统一单文件入口并保证清单等于执行范围 |
| 进度、取消、失败清单 | 保留，完善取消后/失败后的可继续状态 |

依据：[页面构建与标签](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L482-L566)、[覆盖预览](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L3178-L3317)。

### 4.2 文件/文本页推荐布局

这里是布局建议，未声称已经制作或验收新页面。

| 顺序 | 保留的内容 | 交互要求 |
|---|---|---|
| 来源行 | 左侧来源/路径、右侧来源/路径、打开/浏览 | 一处选择来源；远程身份不能静默变化 |
| 主操作行 | 开始/重新比较、上一处、差异计数、下一处、查找、撤销 | 高频操作集中，状态一致 |
| 次要操作 | 视图、比较选项、更多 | 全文替换、交换、复制/导出放更多 |
| 主内容 | 左右差异区 | 普通点击定位/选择；明确进入编辑；展示差异块边界 |
| 修改状态 | 左侧/右侧已修改与对应保存/放弃修改 | 写入对象明确；临时文本显示另存为 |

当前工具栏常驻约 18 个控件，原文展开后每侧又有打开、保存、撤销、重做、退回、语法等，重复入口会挤占阅读区。应调整动作出现的位置和时机，避免继续往顶栏追加按钮。

具体命名：

- “退回”改“放弃修改”，与撤销一步区分。
- 文本箭头叫“合并到右侧/左侧”，说明尚未保存。
- 目录箭头叫“复制到右侧/左侧”，预览中明确“新增”或“覆盖”。
- 全文动作写“用左侧全文替换右侧”，不要把有无选择隐式切换为两种不同范围。
- 初始编辑器可见时，不应出现与实际动作相反的“展开原文件编辑”。让标签及 `aria-expanded` 由同一可见状态决定。

依据：[工具栏与来源按钮](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L589-L632)、[常驻工具栏](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L751-L832)。

### 4.3 文件夹页推荐布局

保留两侧来源卡片、扫描行、筛选行和结果树。选中后出现的批量操作条已经实现，继续沿用。

主要调整：

1. 让“查看差异”成为可见动作，保留双击快捷方式。当前中央操作偏向写入箭头，查看操作主要依靠双击提示。
2. 单文件覆盖复用现有预览，避免一处箭头改缓冲区，另一处相似箭头直接改磁盘。
3. 有过滤词时，从全部已扫描 items 搜索，临时显示命中的祖先路径；清除搜索恢复原折叠状态。
4. 从目录打开文件时显示“返回目录比较”和当前相对路径。现有内部标签切换已经保留列表，不需要另建标签系统。
5. 文件保存后把目录里的对应行标为“已修改，待重新验证”，或仅重新核验该项；保留滚动、展开、筛选，不必每次从头扫描整个目录。
6. 根扫描与按需展开使用同一份扫描设置快照。修改方式、容差、忽略项后显示“重新比较后生效”，不得混用标准。
7. 对具体已验证文件按所选范围校验，避免无关 pending 目录阻塞全部操作；未展开目录覆盖继续受 F05/F06 的完整清单约束。

依据：[搜索只进入展开树](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L2984-L3007)、[按需展开读取当前控件](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L2940-L2958)、[单文件直接复制](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L3333-L3367)。

### 4.4 快捷键和虚拟列表的具体修复

| 问题 | 源码依据 | 最小修复 |
|---|---|---|
| textarea Ctrl+Z 后继续冒泡，panel 再撤销一次；document 还处理合并撤回 | compare.js 427–434、699–709、855–859 | 焦点作用域只选一个处理者，消费后停止传播；检查 defaultPrevented；隐藏页面不拦截 |
| Unified 5000 行内走 Diff2Html 后直接 return，导航仍指向旧 currentVirtualDiff；阈值两侧行为不同 | 1187–1188、1240–1245、1473 | 优先保留并排/仅差异；Unified 先归到补丁预览/导出。若保留独立视图，统一能力接口并禁用不支持动作 |
| “全选内容”只选当前虚拟 DOM，无法代表完整文本 | 658–667、1833 起 | 使用已有复制左侧全部/右侧全部/复制 Diff，从模型生成；删除含糊全选或改名当前显示内容 |
| 目录空格勾选后 render 因窗口未变提前退出，复选框与计数不一致 | 3424、3564–3568 | 独立 syncSelectionDOM 更新可见行、aria-selected、表头全选/半选 |
| 临时文本的保存按钮永久禁用，Ctrl+S 却有另存逻辑 | 2355–2358、1000–1026、838–853 | 临时文本显示“另存为”，已有文件显示“保存”，使用同一能力判断 |

这些都是局部状态和事件处理调整，不需要新增命令平台或换编辑器框架。证据文件：[compare.js](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js)。

### 4.5 视觉验收重点

CSS 已有响应式规则，不能笼统认定页面不适配。应在真实浏览器重点验证：

- 1366×768、1920×1080，以及 125% 缩放下，主按钮和修改状态可见。
- 内容区使用剩余可用高度，避免固定最小高度与多行工具栏叠加，把差异区挤出首屏。
- 长路径截断后能查看完整内容，协议和左右方向不能同时被截断到不可辨。
- 窄屏目录表头与行内容横向滚动保持对齐；当前表头不在 body 的 viewport 内，需要实际核对。
- 查找输入、范围、计数与按钮允许适当换行，不把文本输入挤成很窄。
- 普通点击用于定位与复制；双击/F2 明确进入行编辑，Esc 能退出。若保留单击编辑，必须让编辑态可辨。

建议先完善桌面浏览器常用尺寸，不另做一套移动端比对产品。依据：[compare-workbench.css](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare-workbench.css)、[目录表头及 viewport](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L3369-L3388)。

## 5. 其它已发现问题与适用范围

| 项目 | 证据等级与影响 | 最小处理 |
|---|---|---|
| 保存既有 Unix 文件时权限可能变为 0644 | 原 Local 写入复现：0700 → 0644。文本保存未传 Mode，WriteAtomic 使用默认值；复制文件已有传源 Mode 的路径，不能泛称所有复制都丢权限 | 已有目标且未指定 Mode 时保留原权限；新建文件再用默认值 |
| 首尾含空格的合法文件名被 trim | 原 helper 复现 `" report.txt "` → `"report.txt"`；存在相对路径配对或目标命名失真风险 | 校验路径分隔和特殊目录名，但保留合法名称原字面值 |
| 0 秒时间容差变成 2 秒 | 原函数复现；当前页面没有 0 秒选项，属低优先接口语义 | 区分未传值与显式 0，或明确定义支持范围 |
| 多块导出补丁与 Git 不兼容 | 原 diff.go 生成两个间隔 7/8 相同行的块，上下文重叠，Git 拒绝；GNU patch 仍可用 | 上下文相交时合并 hunk；否则保证范围不重叠；加真实 git apply 回归 |
| SFTP shell fallback 可能把 ls 失败当空目录 | 源码路径及同形 shell 命令验证，未远程联机；`ls ... 2>/dev/null \| head ...` 会以 head 成功掩盖 ls 失败 | 保留 ls 的错误与退出码，明确回传目录不可访问 |
| FTP 根路径 / 的 Stat 探测有遗漏 | 源码风险，未连接真实 FTP；SIZE失败后列父目录找自身，根目录自身项又会被过滤 | 单独正确探测根目录，不依赖父目录列表中出现 / |
| 远程阻塞 IO 的取消可能不及时 | 源码风险，未做断网联机复现；多个操作只在 IO 前检查 ctx | 对该比较任务独占连接处理取消/读写超时，避免影响其它连接 |

证据：[Local WriteAtomic](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/comparefs/local.go#L193-L247)、[名称校验](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/handlers_compare_workbench.go#L1483-L1494)、[Diff 上下文](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/diff/diff.go#L320-L423)、[SFTP fallback](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/sftpclient/sftpclient.go#L1493-L1516)、[FTP Stat](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/comparefs/ftp.go#L99-L119)、[SFTP 适配器](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/comparefs/sftp.go#L40-L95)。

## 6. 性能建议：保留现有基础，测实际瓶颈

现有实现已经有文本/目录虚拟滚动、输入防抖、大文本暂停自动比较、限深扫描、流式复制、内容比较并发等保护。内容头尾采样相同之后仍检查中间内容，原函数正向复现也发现了中段差异；不应误报为仅采样就判全文相同。

实际执行现有 `BenchmarkCompareLargeMostlyEqual` 一次：100,000 行、每 5,000 行一处变化，纯 Go 算法约 92.85 ms/op，分配约 13.74 MB。环境为 Xeon Platinum 8370C 容器，`-benchtime=1x`；这不是稳定性能基准，也不能推导用户电脑、页面渲染、远程网络或目录扫描耗时。

这次证据更支持保留现有算法，先修上述正确性与交互问题。性能改进范围控制为：

1. 保留当前虚拟渲染，修复全量复制/选择对数据模型的访问。
2. 搜索只检索已扫描结果，不在每次输入时深扫远程文件系统。
3. 保存/复制后优先刷新受影响文件或目录，不自动重扫所有目录。
4. 所有来源、输入和选项变化使旧结果正确失效，避免无用重算和旧响应重绘。
5. 在本地 1 千/1 万文件、远程高延迟目录、长行文本分别记录扫描、读取、计算、渲染耗时，再确定瓶颈。用实际数据决定是否需要进一步优化。

不建议现阶段引入新编辑器框架、三方合并、自动双向同步、重命名识别、复杂规则引擎、同步调度平台或 AI 差异解释。它们都会扩大维护面，当前问题已有更小的解决路径。

证据：[文本性能阈值](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/web/pages/compare.js#L8-L15)、[本地并发能力](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/comparefs/local.go#L62-L65)、[现有算法基准](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/diff/diff_test.go)。

## 7. 给实现 AI 的分阶段任务

### Phase 1：修复正确性与内容保护

按以下顺序实施，每完成一项补相应结果断言：

1. F01：局部范围编辑，保留未选内容、空行和末尾换行。
2. F02/F03：比较结果绑定来源及左右文本版本；换源重置操作历史；合并、撤回只作用于当前有效上下文。
3. F04/F09：统一来源与载入状态；主比较先加载正确来源；复用关闭守卫保护 dirty。
4. F05/F06：预览生成明确叶子清单，带 expected/expected_missing，后台不得扩大清单或重置版本约束。
5. F07/F08/F10：目录按相对路径配对；取消永久隐形排除；读取失败及未完成结果不能呈现为一致。
6. 文本保存已有 Unix 文件时保留权限。

**完成标准：**所有定向复现的期望行为成立；现有 Node/Go 测试保持通过；真实 HTTP 与本地 UI 端到端验证相同场景。

### Phase 2：修复日常操作与任务连续性

1. 从全部已扫描项搜索，并展示命中祖先。
2. Ctrl+Z 统一焦点分发；当前目录复选框与表头状态同步。
3. 固定一次扫描的有效设置；变更显示待重新比较。
4. 单文件覆盖复用已有预览；文字区合并与磁盘写入用词清晰。
5. 完善临时文本另存为；目录查看文件后可明确返回，并更新受影响行。
6. 修复或收敛 Unified 视图；修复实际多块补丁导出。
7. 远程功能使用频率高时，补真实 FTP 根目录、SFTP 权限错误与取消测试，再修对应适配器。

**完成标准：**一条完整用户路径“选目录 → 查找 → 查看差异 → 合并 → 保存 → 返回”不丢内容、状态和位置；写入范围可核对。

### Phase 3：页面精简和布局验收

1. 一个主比较入口；高频工具集中，全文替换/交换/导出收进更多。
2. 保存按左右归属展示；已修改提示与写入按钮相邻。
3. 统一“放弃修改、合并、复制、新增、覆盖、待验证、无匹配”等文案。
4. 1366×768、1920×1080、125% 缩放、长路径和长行截图验收。
5. 修复证据显示的溢出、表头对齐、焦点或滚动问题。

**完成标准：**用户能直接回答：正在比较哪两份内容、差异在哪、还有哪些没比较、点击此按钮会改哪里、当前修改是否已经保存。

### 代码组织约束

先在现有文件内归拢少量纯函数：来源身份与载入检查、差异版本校验、局部编辑、扫描状态文案、最终覆盖清单。测试通过后再决定是否拆出小模块。不要把此次修复扩展成全站架构重写或新增一套状态管理/事件平台。

## 8. 验收矩阵

下表是实现后的验收要求，不能理解为当前版本已全部通过。

| 编号 | 场景 | 必须断言的结果 |
|---|---|---|
| A01 | 恢复两个历史文件路径，直接点开始比较 | 比较前成功读入对应文件；不把空缓冲区当成文件内容 |
| A02 | 远程文件载入后再次比较/修改路径 | 协议、主机和路径保持对应，不静默改 local |
| A03 | 忽略空行后仅合并一行/一块/多选 | 未选空行、末尾换行和其它内容原样保留 |
| A04 | 比较请求在途时继续输入 | 旧响应不能覆盖新版本的有效状态；旧合并被阻止 |
| A05 | 大文本暂停自动比较后编辑并点击旧箭头 | 新输入不丢，提示重新比较 |
| A06 | A/B 合并后切换 C/D，再撤回 | 不能将 A/B 文本注入 C/D；保存目标身份始终正确 |
| A07 | 交换左右，再撤销 | 路径、版本、baseline、文本恢复为一致状态 |
| A08 | 未保存时换源/打开另一个文件/关闭 | 换源和应用内关闭的保存后继续、放弃、取消符合选择；保存失败不继续；浏览器关闭使用原生离开确认 |
| A09 | 两目录各一个不同名文件 | 两条仅单侧结果，不能强配为同一文件 |
| A10 | 只有 dist/target 下存在差异 | 可比较并找到差异；主动排除时范围说明正确 |
| A11 | 忽略扩展名后勾选父目录 | 被忽略文件不复制、不修改 |
| A12 | 父目录保留、子项取消 | 取消子项绝不被后台重新纳入 |
| A13 | 预览后外部修改目标 | 报冲突，不覆盖外部改动 |
| A14 | 扫描时缺失、执行前目标被新建 | 报冲突，不能当作原目标直接覆盖 |
| A15 | 元数据相同而内容不同 | 快速模式准确说明边界；内容模式查出差异 |
| A16 | 搜索不存在文件或只剩未验证目录 | 无匹配/待验证，不能显示两侧内容一致 |
| A17 | 已扫描子文件被折叠，再搜索 | 能命中并显示祖先；清除搜索恢复折叠 |
| A18 | 扫描后修改比较方式，再展开目录 | 使用原扫描配置或要求重新扫描，不混合标准 |
| A19 | Ctrl+Z、Shift+Ctrl+Z、行内编辑、隐藏页 | 一次只处理一个正确作用域动作 |
| A20 | 目录空格勾选、逐项勾选、全选 | 计数、复选框、表头半选/全选、aria-selected 一致 |
| A21 | 大结果复制全部/导出 | 使用完整模型，不仅复制当前虚拟窗口 |
| A22 | GBK/GB18030、CRLF、BOM 文件保存重读 | 内容及编码元信息正确，备份为旧内容；末尾换行按原约定保留 |
| A23 | 0700 已有脚本编辑保存 | 保存后仍保留相应权限 |
| A24 | 两差异块间隔 6/7/8/9 个相同行 | git apply --check 成功，应用后完整内容等于目标 |
| A25 | 覆盖中取消/部分失败/真实远程断开 | 成功、失败、冲突、取消数量准确；已完成项可核对，不宣称全部成功 |
| A26 | 1366×768、125% 缩放、长路径、长行 | 操作可见、表头对齐、左右身份明确、焦点与滚动稳定 |

## 9. 测试本身需要补强的地方

原有测试中有有价值的覆盖，应保留。但部分 E2E 只检查能点、能截图、出现“已修改”，没有检查实际结果。

- `tests/e2e/tests/11-compare-deep.js` 39–143：相同/不同/空文本/中文等案例缺少明确结果断言，按钮还可不存在而跳过。
- 146–165：清空后仍非空的分支没有失败断言。
- 168–183：双向合并没有断言完整目标内容。
- 221–286：目录覆盖流程缺少写入后磁盘内容核验。
- `run-real-compare-clicks.js` 173–209：少量文件存在性检查不能代表全部文件内容正确。
- `compare_backend_p0_test.go` 468–478：一处截断目录案例没有验证返回 err/res。
- `diff_test.go` 162–172：名为 MultipleHunks 的夹具实际只有一处变化，没有覆盖本次多块补丁问题。

应补进现有测试，不另建平台。必须让缺失按钮、错误差异数、未选内容被改变、过滤文件被写入、目标外部修改被覆盖等情况直接失败。截图用于布局核对，文件完整内容及写入结果用程序断言。

证据：[E2E 比较测试](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/tests/e2e/tests/11-compare-deep.js)、[真实点击脚本](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/tests/e2e/run-real-compare-clicks.js#L173-L209)、[截断测试](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/httpserver/compare_backend_p0_test.go#L468-L478)、[MultipleHunks 测试](https://github.com/Qioooba/kairo/blob/38df3aa604817955f470eaaaa0eb4c29124972c4/internal/diff/diff_test.go#L162-L172)。

## 10. 建议交给实现 AI 的任务说明

请以本文固定提交为审查基线，在实施时先核对当前代码是否已有后续修复。保留现有双标签、左右比对、树形目录、虚拟滚动和后端流式处理。优先完成 F01–F10 的正确性与内容保护，再做 Phase 2 常用操作，最后做 Phase 3 页面精简。每项改动都要用验收矩阵证明完整结果正确，尤其是未选择区域、目标外部修改及排除文件不变。复用已有预览、模态框、TabManager、测试入口，不引入新的大型编辑器、同步平台或复杂规则系统。真实浏览器及远程联机验证完成前，不把本次源码推导标成端到端通过。
