# Kairo 近一周代码审核与 AI 修复任务书

## 审核范围与结论

仓库：`Qioooba/kairo`，分支：`main`。

日期窗口：2026-09-10 至 2026-09-17。基线提交：`dc75701583fe78ec8b8bcfa706d77c68a7ba6011`；审核终点：`6791488014271e892d19b9aba2b1a9929a6958b1`。该比较区间包含 21 个提交，实际提交集中在 9 月 14—16 日。

本次核对了提交范围，重点追踪多 Tab 生命周期、SSH/上传资源归属、SFTP 文件名编解码、数据库 UPDATE 导出与事务终态、代码生成发布，并阅读了升级恢复、同步和测试入口相关实现。不是对全部变更文件、全部执行路径的无遗漏审计。

发现 9 项具有明确源码依据的问题：6 项 P1、3 项 P2。P1 表示优先修复的连接/任务中断、错误数据定位、错误事务状态或文件覆盖风险；P2 表示在明确操作条件下发生的功能或工作区数据保护缺陷。条件性风险不代表每次操作都会触发。

本次没有修改仓库，也没有运行完整的 `go test ./...`、浏览器 E2E、Windows 真机、真实 Oracle/MySQL/SSH 测试。已用摘取的关键函数/分支和替身依赖运行 5 个隔离反例，复现日志及脚本在同一证据包中。这些反例验证控制流或标准库行为，不等于运行了完整产品。

| 编号 | 优先级 | 问题 | 主要位置 |
|---|---|---|---|
| R1 | P1 | SSH/上传等资源注册到错误 Tab，关闭别的 Tab 会清理它们 | `web/tabs.js`、`web/core.js`、`web/pages/ssh.js` |
| R2 | P1 | 用户取消离开页面，连接和任务却已被清理 | `web/app.js` |
| R3 | P1 | 计算表达式伪装成主键，UPDATE 导出可能更新另一行 | `internal/dbconsole/export_plan.go`、`export.go` |
| R4 | P1 | 并发重复提交可把已提交终态改写成已回滚 | `internal/dbconsole/transactions.go` |
| R5 | P1 | UTF-8/GBK 同名文件失去身份，可能读错、删错或重命名错对象 | `internal/sftpclient/names.go`、`sftpclient.go` |
| R6 | P2 | GBK 目录可浏览，但上传、保存和重命名目的路径没有正确解析 | `internal/sftpclient/sftpclient.go` |
| R7 | P1 | 禁止覆盖被硬链接失败后的普通写入绕过 | `internal/wscodegen/generate.go` |
| R8 | P2 | 关闭便笺 Tab 会丢弃行内草稿，且残留未保存标记 | `web/pages/notes.js`、`web/notes.js`、`web/app.js` |
| R9 | P2 | 便笺与提醒的子路由没有和保活页面同步 | `web/tabs.js`、`web/pages/notes.js`、`web/notes.js` |

## R1：Tab 资源所有权取错时机

### 源码与调用链

`TabManager.openRoute()` 先 `renderTabPane(tab)`，后 `activate(tab.id)`。前者执行具体页面的 render 时，活动 Tab 仍是上一个页面。

`core._tabIdOf()` 在没有显式 owner 时读取 `Kairo.tabs.getActiveId()`。SSH 页同步执行 `setActiveShells(controller)`、`setActiveUploads(controller)`，两处均未传 Tab ID。

首次从首页进入 SSH 的顺序因此为：

```text
activeId = home
renderSSH → setActiveShells(...) → 注册在 home 桶内
activate(ssh)
openRoute 自动 destroyTab(home)
releaseTabResources(home) → 调用刚注册的 SSH closeAllTabs
```

从其他页面打开 SSH，也会把 SSH 控制器挂到旧页面；关闭旧页面会误清理 SSH。销毁时 `setActiveShells(null)` 又按“此刻活动 Tab”注销，可能进一步影响邻居页面的注册表。

### 修复要求

给每个 pane/scope 提供创建后不变的 owner（必要时再加实例代号）。所有 SSE、WS、XHR、上传控制器的注册和注销都使用 owner，不得在异步回调或清理函数里重新读取当前活动 Tab。注销应校验资源身份，避免旧实例清理掉新实例。

不要仅把 `activate()` 移到前面：这只能缓解同步注册，不能解决后台异步注册和销毁后的迟到回调。

### 必须补的真实回归测试

1. 首页进入 SSH，控制器属于 SSH，自动关闭首页不调用 SSH closeAll。
2. 文件页打开 SSH，关闭文件 Tab，不中断 SSH。
3. A 页发起异步请求，切到 B 后请求完成，新增资源仍归 A。
4. 关闭 A、重开 A，旧 A 的迟到 cleanup 不删除新 A 的资源。
5. 关闭 SSH 后重新打开，只存在一组控制器与监听器。

**本次验证：**已运行注册/释放关键分支的隔离反例，观察到资源注册在 home，释放 home 会关闭该 SSH 控制器。没有运行真实 SSH 会话。

## R2：beforeunload 在用户决定前销毁资源

### 源码与影响

`web/app.js` 的 beforeunload 处理器在调用 `preventDefault()`、设置 `returnValue` 后，无条件执行 `releaseAllForUnload()`。

该函数关闭下载/tail EventSource、发出取消 beacon，释放所有 Tab 资源，并取消数据库查询。浏览器稍后才询问用户是否离开，因此“取消离开”不能撤销已经发生的关闭和取消请求。

这是本周关闭守卫与资源清理仍然冲突的问题。基线 app.js 中也存在 beforeunload 清理，因此不把其历史起点全部归因于本周；本周多 Tab 和关闭守卫改造没有解决这一冲突，影响范围变成多个保活页面。

### 修复要求

beforeunload 仅检查并提示，不执行破坏性动作。实际页面离开、应用显式关闭 Tab、后台任务生命周期应分别处理。页面真正离开时的清理可结合 pagehide，但必须明确 bfcache 的 `persisted` 语义，不能把普通隐藏/失焦误判为关闭。

后端无法保证收到浏览器最后一条请求，仍需保留 TTL、断连回收等兜底。不要把修复写成“删除所有清理”或“取消页面隐藏时的全部后台任务”。

### 验收

已有 SSH、上传、tail、下载和查询时触发离开提示并取消：本地连接仍有效，上传未 abort，未发出任务取消请求，用户输入保留。确认离开或显式关闭 Tab：按约定清理且最多一次。

**本次验证：**处理器关键控制流隔离反例显示：已请求确认，但 release 已调用。没有运行原生浏览器确认框测试。

## R3：UPDATE 导出没有证明结果主键来自真实主键列

### 源码与触发条件

`BuildExportTargetPlan()` 根据字典主键名称匹配 `columns[i].Name`，没有证明结果列是基表原始主键，还是计算表达式、常量或其他列的别名。

`ValidateSingleTableQuery()` 拒绝部分 JOIN/UNION/GROUP/HAVING，但不能阻止以下查询：

```sql
SELECT ID + 1 AS ID, BALANCE
FROM T
WHERE ID = 1;
```

假设原始行 ID=1、BALANCE=100，结果是 ID=2、BALANCE=100。列名仍与字典主键 ID 匹配，导出代码会按结果 ID 构造：

```sql
UPDATE "T" SET "BALANCE" = 100 WHERE "ID" = 2;
```

这不是“可能导出失败”，而是在目标 ID=2 存在时，生成可更新另一行的脚本。`handleDatabaseExport()` 确实把当前 SQL、结果列及字典主键交给该计划，并调用 `WriteUPDATEWithPlan()`；没有后续投影来源校验挡住这一情况。

### 修复要求

UPDATE 导出必须验证目标基表、结果列和主键的来源。无法证明时拒绝，不通过增加几个危险关键字正则来猜测安全。

可先支持一个保守子集：能严格证明的直接单表列投影、明确映射的别名和完整主键；表达式、重复别名、常量伪装主键、子查询或不明来源先拒绝。SET 列也需要来源映射，不能把任意显示别名当作物理列名。

不得退回“首列作为 WHERE”，也不得接受前端宣称 ROWID 可信。

### 验收

`ID+1 AS ID`、常量 `AS ID`、其他列 `AS ID`、重复主键别名均被拒绝，响应不能携带可执行 UPDATE 脚本。普通单表原始列、完整联合主键、正确引号标识符仍能导出；对构造数据实际运行导出脚本，不得更新非来源行。

**本次验证：**运行了原验证函数及其主键名称匹配分支的隔离反例，查询被接受且键值指向 2。没有实际执行 SQL 或运行完整 HTTP 导出接口。

## R4：并发重复事务控制覆盖真实终态

### 源码与时序

`ControlSessionTransaction()` 先调用 `transactionFor(..., false)` 获取 entry，之后才等待 `entry.mu`。两个并发请求可以提前取得同一个 entry。

```text
请求 A、B 先后取到相同 entry
A 获锁 → Commit 成功 → 记录 committed → 移除注册表 entry
B 随后获锁 → 继续操作已结束的相同 sql.Tx
第二次 Commit 返回 sql.ErrTxDone
commitEntryLocked 将它走到“提交失败已回滚”，覆盖 terminal record
```

顺序发送、第二个请求在第一次完全结束后才开始时，已有的 terminal 查询分支可能正确处理。因此这里的触发条件是“两个请求提前取得相同 entry”，不是所有重复提交必现。

### 修复要求

将“取得 entry、终止状态、终态写入”纳入同一生命周期约束。获得 entry 锁后应判断该 entry 是否已结束/已被替换；已结束就返回该次事务的确定结果，不能再调用驱动并改写终态。终态应绑定事务实例，防止同一 session 开启下一次事务后被旧请求覆盖。

`sql.ErrTxDone` 不等于“已回滚”。没有可信终态时也不能宣称“肯定回滚”。同样检查 COMMIT/ROLLBACK/显式断开/超时清理之间的竞争。

### 验收

使用可控制 barrier 的 fake driver，让两个真实 ControlSessionTransaction 请求在第一次提交完成前都获得 entry。成功提交后，重复 COMMIT、并发 ROLLBACK 不得把 committed 改为 rolled_back；未知结果仍保留 outcome_unknown，不给用户可盲重试的错误安全感。

**本次验证：**使用真实 database/sql + 无外部连接的 fake driver，复现同一已提交 Tx 的第二次 Commit 返回 ErrTxDone，并按当前分支把 committed 改为 rolled_back。并发 HTTP 链路仅作源码时序分析，未运行完整 Manager 集成测试。

## R5：GBK 显示名不能作为文件身份

### 源码与反例

`wrapDecoded()/decodedFileInfo` 把原始 `Name()` 替换成显示名，没有同时携带原始文件名/路径身份。`EncodePathCandidates()` 又把用户传回的显示字符串按 UTF-8、GB18030 两种整路径尝试。

在同一个目录内，UTF-8 字节的 `中文.txt` 与 GBK 字节的 `中文.txt` 可以是两个不同文件。两条记录显示名相同。后端 Open/Remove 等总先尝试 UTF-8 路径，不能判断用户选择的是哪条原始记录。

Remove 对任意失败继续尝试另一编码；Rename 也在源路径上试候选。这把名称猜测带到了破坏性操作中，存在读错/删错/重命名错文件风险。风险条件是编码混用、同名碰撞或相关失败重试，不代表单一编码目录每次都会错。

### 修复要求

展示文本与文件身份分离：返回受约束的 opaque handle/原始路径编码标识，或在连接上下文中建立可验证的显示项到原始字节映射。删除、覆盖、重命名必须唯一定位对象，不能凭显示字符串轮流试操作。

路径标识仍须绑定服务器/连接和允许操作边界；不能把原始路径句柄做成绕过既有权限校验的后门。读取回退也应区分 NotExist、PermissionDenied、连接错误，保留真实错误。

### 验收

构造同目录同名但不同编码的两个文件，分别读取/删除/重命名，非目标文件的字节、名称和内容均不变；权限错误或断连不导致尝试另一个文件。补混合编码父子目录测试。

**本次验证：**源码调用链和文件名身份反例；未连接真实混合编码 SFTP 服务器。

## R6：GBK 写入链路缺失已存在父目录解析

### 源码与影响

ReadDir/Stat/Open 有候选路径解析，但以下路径直接传给 backend：

- UploadFile → WriteFile；UploadStream → backend.UploadStream。
- MkdirAll、CreateExclusive、Chtimes。
- Rename 只尝试 oldPath，newPath 始终使用显示字符串。

例如磁盘实际目录是 GBK 字节的 `/tmp/中文目录`，浏览可以成功；上传使用 UTF-8 展示路径 `/tmp/中文目录/a.txt` 时，父目录不存在，连 ASCII 文件名都写不进去。目录内重命名也会因目的父目录未解析失败。

在 ASCII 父目录下，保存一个原本以 GBK 字节命名的文件，如将显示路径直接交给 UploadFile，可能创建 UTF-8 同名副本而不是更新原文件。将“创建新名字不应盲目猜编码”扩展成“整个目标路径不解析”并不正确。

### 修复要求

区分已存在对象、已存在父目录及待创建 basename。先用稳定身份解析父目录和既有目标；只对新 basename 使用明确的命名编码策略。复用 R5 的路径身份机制，禁止把 UploadStream 改为失败后直接对另一条路径重试写入。

### 验收

GBK 父目录内上传 ASCII/中文文件、新建、重命名、保存已有文件都操作正确对象；保存后原文件更新且目录没有多出 UTF-8 同名副本。UTF-8 目录行为不回退。失败后不留不明 partial 文件。

**本次验证：**读写路径源码对照，未运行完整上传或远程编辑流程。

## R7：硬链接失败的 fallback 破坏不覆盖保证

### 源码与反例

`writeFilesLocked()` 发布阶段的逻辑是：

```go
if err := os.Link(srcFile, dest); err != nil {
    data, rerr := os.ReadFile(srcFile)
    // ...
    if werr := os.WriteFile(dest, data, 0o644); werr != nil {
        return rollback(werr)
    }
}
```

该分支不区分 EEXIST 和“不支持硬链接”。即使 overwrite=false，目标文件在预检后被 IDE、另一进程或另一路径写入者创建，Link 返回 EEXIST 后，WriteFile 会将新出现的用户文件截断覆盖。目录锁只保护本进程按同一个锁键进入的代码，不能保护外部写入。

fallback 写入部分失败时，`installed` 尚未设为 true，回滚也可能漏清理本次创建的半成品，应一并处理。

### 修复要求

EEXIST 一律按冲突失败，不得切换为覆盖写。只对明确支持降级的错误处理，使用不覆盖的创建语义（O_CREATE|O_EXCL），并把创建后半写失败纳入回滚；能使用同目录临时文件和具有合适语义的原子发布时优先使用。不能仅用发布前再 Stat 一次替代互斥创建。

### 验收

在预检和发布之间注入创建 dest，overwrite=false 必须失败且保持原字节。覆盖模式发生冲突/部分写错误要保留可恢复备份；模拟硬链接不支持、磁盘写入失败，不能留下半生成文件或删掉非本次创建的文件。

**本次验证：**Linux 临时目录中的标准库隔离反例，确定性模拟预检后出现目标；观察到 Link 返回 EEXIST 后用户文件被覆盖。没有运行完整生成器或 Windows 文件系统测试。

## R8：便笺行内草稿未接入 Tab 关闭守卫

### 源码与操作

`beginInlineEdit()` 将草稿记录为 `Kairo.notes.setInlineDraft(id,true)`；用户尚未点保存时，新文字仍在输入框 DOM 中。

`web/app.js` 注册的 Tab 关闭守卫覆盖配置、数据库、上传和 SSH，没有便笺。`renderNotes()` 不注册 scope.canLeave，返回的 cleanup 仅取消订阅。

在便笺中编辑未保存内容，打开另一个 Tab 后关闭便笺 Tab，页面 DOM 被销毁，草稿未保存且无确认。对应 inlineDrafts 标记还可能留在应用级状态，使后续浏览器离开提示继续声称有未保存内容。

### 修复要求与验收

将便笺草稿纳入 Tab 关闭决策，并区分当前便笺页面的行内草稿与应用级悬浮便笺。取消关闭时保留 DOM/输入；明确丢弃时只清理本页面草稿标记；保存失败不能直接放行。后台自动保存与冲突编辑器也要有明确归属。

测试前台和后台关闭便笺、取消关闭、保存失败、确认丢弃、关闭后重开、仍有悬浮便笺未保存等场景。

**本次验证：**完整页面 render/cleanup 与关闭守卫的静态调用链核查；未运行 DOM E2E。

## R9：保活页面没有接收 notes 子路由更新

### 源码与操作

`ensureTab()` 仅替换 `tab.routeState`，已存在 pane 不重新 render；`renderNotes()` 只在第一次渲染时读取 routeState，局部变量 tab 不会随后更新。页面没有对应 route-state 更新监听。

`Kairo.notes.openReminder()` 设置 pendingReminder 并将 hash 改为 `#/notes/reminders`。如果便笺列表早已保活，hash 和 Tab 记录会更新，但 renderTab 不执行，仍显示列表，提醒编辑器不打开。

反向也不一致：页面内部 switchTab 使用 history.replaceState 更新 hash，却不更新 TabManager 的 routeState；切走再回来，syncChrome 可用旧状态把 hash 改回去。

### 修复要求与验收

建立明确的子路由更新协议，例如 TabManager 提供 route-state 更新接口/事件，页面接受新状态；页内切换也走同一入口。不可通过重建整个 pane 修复，以免重新丢失搜索、滚动和草稿。

首次打开 reminders、已打开列表后“设提醒”、后台 notes 的深链接、页内切换后切走返回、刷新恢复均要求 hash、routeState、实际选中内容一致；pendingReminder 只消费一次。

**本次验证：**TabManager、notes 页面及 openReminder 调用链静态核查。

## 给实施 AI 的统一约束

请以本报告所列 HEAD 为审查依据。若仓库已有新提交，先逐项检查是否已经修复，保留用户的新改动，不硬回退到旧 HEAD。

1. 先为每个问题增加能在当前实现失败的真实回归用例，再改实现。隔离反例是定位证据，不能替代仓库测试。
2. 不以源码 includes/关键字存在、注释写着“已修复”、函数被调用次数的空泛检查代替行为断言。断言实际文件字节、事务终态、资源 owner、DOM 输入和网络取消动作。
3. 保持 Go + 原生 JavaScript、现有 API 的合理兼容与 Windows 支持；不要迁移前端框架，不重写整个工程。
4. 不通过删除关闭守卫、放宽文件权限、关闭主键校验或对写操作增加盲重试使测试变绿。
5. 分模块提交：Tab/便笺生命周期、数据库导出、事务、SFTP 路径身份、代码生成发布。R5/R6 的接口方案一起设计，避免两次互相冲突的修改。
6. 运行现有测试入口和受影响 Go 包测试，执行真实浏览器生命周期回归；Windows、Oracle/MySQL、GBK SSH/SFTP 有环境才声明通过。缺失环境明确列为未验证，不能写“全部测试通过”。

每项交付请记录：编号、修改文件和函数、根因、行为变化、测试命令、修复前失败证据、修复后结果、剩余未验证环境。优先修复 R1/R2 的日常操作中断，同时停止把 R3/R4/R5/R7 涉及的危险路径视为已安全验收。

## 隔离反例结果

执行环境：Node.js v22.16.0；Go go1.23.2；Linux。

```text
TAB_OWNER: opening SSH from home registers under home; destroying home closes SSH controller
BEFOREUNLOAD: confirmation requested AND resources already released before any user decision
EXPORT_ALIAS: validation accepted; PK column index=0; WHERE ID=2 although source row ID=1
DUPLICATE_COMMIT: successful commit recorded committed; stale second request changes record to rolled_back
NO_OVERWRITE: Link returned EEXIST; WriteFile fallback replaced USER_WORK with GENERATED
```

上述输出表明反例存在，不表示修复已实施，也不表示完整产品测试通过。脚本里的外部依赖采用替身，仅使用临时目录，不连接用户数据库或远端服务器。
