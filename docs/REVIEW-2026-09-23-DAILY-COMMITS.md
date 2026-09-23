# 2026-09-23 当日提交审查报告（30 个提交 / 97 文件 / +22229 −1991）

- 审查范围：`git diff d70eb31^..99d80a0`（当日全部 30 个提交，覆盖 Go 后端、web 前端、测试、脚本、文档）
- 审查方式：三路**只读**并行深度审查（Go 切片 / web 切片 / tests+scripts 切片）+ 协调者门禁复核
- 结论：**已修复 6 类发布阻塞与回归缺陷 + 4 项本轮自身缺陷**；剩余遗留项按 BLOCKER/HIGH/MEDIUM/LOW 分级列出，**无构建/发布阻塞项**
- 发布产物：`dist/kairo-v0.20/Kairo_win10.exe` + `dist/kairo-v0.20-windows.zip`（含本报告的全部修复）

---

## 1. 门禁结果（修复后重跑）

| 门禁 | 结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `go test ./...` | 全部包 ok，exit 0 |
| `npm test`（21 个单测文件） | Passed=21 / Failed=0 |
| `web/app.test.js` | 29 pass / 0 fail |
| 左右分栏浏览器定向验收（`tmp/split-layout-verify.js`） | 48/48 |
| 本轮修复专项验收（`tmp/split-layout-fix-verify.js`） | 11/11 |
| 5 主题巡检 / Redis 隔离回归 | 5/5 / 6/6 |
| e2e `--grep 左右分栏布局` | 1/1 |

---

## 2. 已修复（含失败优先证据）

### 2.1 SSH 文件浏览器下载在默认配置下 100% 403 —— CONFIRMED，已修复
- **位置**：`internal/httpserver/handlers_ssh_sftp.go`（`handleSshSftpDownload`，当日提交 `f666fa6` 引入）
- **问题**：该 handler 对每个下载路径新增 `App.FreeFileRootsEnabled(display)` 校验。但 `/api/ssh/sftp/*` 命名空间的契约明确写着**不校验** `free_file_roots`（本文件头、"httpserver.go` 路由注释、`handlers_ssh_sftp_upload.go` 三处），且同命名空间的 list/preview（同样返回文件字节）/mkdir/rename/upload 都没有该校验。`FreeFileRootsEnabled` 对空列表 fail-closed，而出厂 `config.yaml` 正是 `free_file_roots: []` ⇒ 默认安装下 SSH 文件浏览器下载全部 403。CI 全绿只是因为所有测试/QA 配置都写了 `["*"]`，且没有用例覆盖该路由。
- **修复**：删除该 4 行校验，并留注释说明为什么这里不能加白名单。
- **验证**：`go build`/`go vet`/`go test ./internal/httpserver/` 通过；代码与三处契约注释重新一致。

### 2.2 无 SFTP 子系统主机（老 AIX）无法上传新文件/新建目录 —— CONFIRMED，已修复
- **位置**：`internal/sftpclient/sftpclient.go`（`shellBackend.Stat/ReadDir`）+ `internal/sftpclient/resolve.go`（写路径解析）
- **问题**：`resolveDir` / `resolveWritePathCtx` / `resolveDirectoryForCreate` 统一用 `errors.Is(err, os.ErrNotExist)` 区分"确定不存在＝允许新建"与"权限/超时/协议错误＝必须终止"。但 shell 兜底后端（`NewAuto` 在无 SFTP 子系统时降级，正是 OTH 系列要兼容的老 AIX）对缺省路径返回的是裸 `fmt.Errorf("ls -ld 退出码 2: …")`，永远不匹配 `os.ErrNotExist`。旧实现在 ReadDir 列表里推导存在性，因此问题未被暴露；改写后**上传新文件、新建目录、改名为新名、CreateExclusive 全部失败**。
- **修复**：新增 `shellNotFound(code, raw...)` 分类器（识别 GNU/BusyBox/AIX 的缺省文案；**显式排除 Permission denied**，退出码 126/127 不算），`Stat`/`ReadDir` 命中时返回带路径的 `*os.PathError{Err: os.ErrNotExist}`；非缺省错误仍原样保留 stderr 诊断。
- **失败优先证据**：临时把分类器改成 `return false` 后，新增用例给出与审查描述一致的失败：
  `写入 "/tmp/newfile.txt" 失败: ls -ld 退出码 2: ls: cannot access '/tmp/newfile.txt': No such file or directory`，
  `解析目录 "/tmp/new/deep" 的段 "new" 失败: …`；恢复实现后 6 个新用例全绿。
- **验证**：新增 `shell_missing_target_regression_test.go`（写路径可新建 / 权限错误仍致命 / MkdirAll 多级创建）+ `shell_backend_test.go` 分类用例；`go test ./internal/sftpclient/` 通过。

### 2.3 无主键 Oracle 表显式投影下插入能力被静默关闭 —— CONFIRMED（当日旗舰场景回归），已修复
- **位置**：`internal/dbconsole/grid_plan.go` `gridProjectionAllowsInsert`
- **问题**：`plan.Columns` 末尾会追加"行尾隐藏定位列"（`oracle_rowid` 的 ROWID，`Writable=false`）。旧实现把该定位列也计入投影数并与 `len(parsed.Projections)` 比较，于是 `SELECT id, name FROM t`（显式投影、无 PK/唯一键的堆表）恒得 `CanInsert=false`，与仓库自己的意图 fixture（`CanInsert: true`）矛盾。测试没抓到是因为 `gridCPlanFor` 一直传 `hiddenRowIDIdx=-1`。
- **修复**：只比较前 `len(parsed.Projections)` 个绑定，并加 `len(bindings) < limit` 的保守拒绝；通配符与表达式拒绝语义不变。
- **验证**：新增 `grid_insert_projection_regression_test.go`（隐藏定位列不阻塞插入 / 表达式列与绑定不足仍拒绝 / nil 与通配符边界）；`go test ./internal/dbconsole/` 通过。

### 2.4 孤儿视图里"＋ 页签"得到不可用页签 —— CONFIRMED，已修复
- **位置**：`web/pages/database.js` `switchSession`（当日 `DBUI-03` 提交给孤儿视图加了"＋ 页签"）
- **问题**：孤儿视图只有 `#db-sql`，没有 `#db-main`/`#db-run`/`#db-results-section`。新建页签已绑定存活的 `state.source`，因此走了 `restoreSessionChrome()` 分支——SQL 被写进孤儿 textarea，页签"已激活"却没有任何执行入口，面板还继续显示"原数据源已删除"。
- **修复**：在 `switchSession` 落到 `restoreSessionChrome` 之前判断"当前 DOM 仍是孤儿布局"（`!q('db-main') && q('db-workspace')`）→ `renderWorkspace(true)` 重建完整工作区（无数据源时正确落到空状态页）。
- **浏览器验证**（真实界面删除数据源 → 孤儿视图 → 点"＋ 页签"）：进入孤儿视图 ✔、点击后重建出带执行按钮的完整工作区 ✔、结果网格存在 ✔。

### 2.5 本轮左右分栏自身的 3 个缺陷 —— CONFIRMED，已修复
| # | 位置 | 问题 | 修复 | 验证 |
| --- | --- | --- | --- | --- |
| a | `database.js` `applyColumnsHeight` | 用 `viewport − (rect.top + scrollY) − 24` 计算高度，把滚动偏移多扣了一次（滚动后工作区偏矮）；滚过顶部时还会越算越高 | 改用视口坐标 `viewport − max(0, rect.top) − 24`，并压在 `[560, viewport−24]` | 浏览器断言高度与公式一致（832/764 两轮一致） |
| b | `database.js` `applyLayoutMode` | 每次容器宽度变化都把夹紧后的值写回 `persisted.editor_col_width`，临时把窗口拖窄就把用户拖出来的列宽永久改小 | 容器变化只做显示层夹紧，`persisted` 仅由用户手势（拖动/双击/键盘）写入 | 拖到 834 → 窗口变窄夹到 634 → 窗口拉回恢复 834 ✔ |
| c | `database.js` `editorColEdges` | 夹紧上限没扣 `.db-main` 的 2×8px 列间距，边界处三列总宽超出容器约 16px（结果列被裁切） | 新增 `LAYOUT_TRACKS_EXTRA = 6 + 16` 参与夹紧 | 边界值由 850 修正为 834（= main−22−420）✔ |
| d | `database.js` `bindPaneSplitter` | 双击/键盘后遗留的 700ms 提示自动隐藏计时器会在下一轮拖动中途触发，宽度提示闪断 | `showHint` 取消未决计时器，新增 `autoHideHint()`；e2e 拖动期间提示稳定可见 | e2e 用例由失败转为通过 ✔ |

### 2.6 测试卫生（本轮引入）—— 已修复
| # | 位置 | 问题 | 修复 |
| --- | --- | --- | --- |
| a | `tests/e2e/tests/14-database-workbench.js` | 新用例以 `if (!(await page.$('#db-sql'))) return;` 开头，工作台未渲染时整条用例被判 PASS（runner 里 return 等于通过），恰好跳过全部断言 | 改用 `helpers.requireElement()`（当日 QA-02 新增的防"零断言通过"helper）显式失败 |
| b | 同上 | 用例改视口后不还原，runner 共用同一个 page，后续用例与截图/trace/run-metadata 的 `1366x900` 标签失真 | `finally` 中还原 `page.viewportSize()` |

---

## 3. 遗留问题（按严重度分级，均已定位到行；本轮未修）

> 说明：以下均经审查者追踪确认（CONFIRMED），但**不影响构建/发布**；标注了是否为当日改动引入的回归、是否"潜伏"（当前前端/调用方未走到的契约缺口）。

### MEDIUM（建议下一轮优先）
| 位置 | 问题 | 修复方向 |
| --- | --- | --- |
| `internal/httpserver/handlers_ssh_sftp.go:243-249` | `sftpCreateTarget` 只要 `parent_path_id` **或** `name` 非空就走身份分支，只给 `name` 时合法绝对路径回退不可达（400） | 仅按 `hasParent` 分支（潜伏：现前端总是两者都发） |
| `internal/dbconsole/grid_value.go:343` | 索引协议门槛是 `len(Changes)>0`，删除请求没有列变更 ⇒ `RowColumns` 被静默丢弃 | 门槛改为 `len(Changes)>0 \|\| len(RowColumns)>0`（潜伏） |
| `internal/dbconsole/grid_value.go:602` | 索引分支把隐藏定位列存成 key `"ROWID"`，而 WHERE 只找 `__KAIRO_EDIT_RID__` ⇒ 客户端按 `hidden_rowid_index` 交付仍报"缺少 ROWID" | 索引分支同时置 `intent.rowID`（潜伏：前端另发顶层 `rowid`） |
| `internal/dbconsole/lob_locator.go:271-285, 236` | 会话事务分支执行 SQL **未走 `m.acquire`**（其它同类路径都走）；2s 预算又在不可取消的 `entry.mu.Lock()` 之前创建，竞争时可"出生即过期" | 加 acquire/release；预算在取锁之后再建 |
| `web/pages/compare.js:1732`（及单侧源对话框 ~646） | 切换文档只判断 `dirty`，不计新引入的内联草稿 `draft` ⇒ 编辑中的单元格被静默丢弃（其它守卫都算了） | 加入 `hasPendingDraft('left'/'right')` |
| `web/pages/compare.js:1329-1331` | 保存飞行中另一侧换了文档时提前 `return false`：文件其实已写入，却无提示、`dirty` 不退、版本不推进；再次 Ctrl+S 会发旧版本号得到 409，被告知"磁盘文件已被外部修改" | 只按被保存侧的文档身份判定，或在另一侧变化时仍推进版本并提示 |
| `web/workbench/sql-format-service.js:843-853, 976` + `database.js` 格式化入口 | 保真扫描失败时静默返回原文，UI 提示"SQL 无需格式化"（实际是放弃格式化）；`lastFormatDiagnostic()` 导出但无人消费 | 把诊断接到 toast |
| `tests/e2e/utils/test-runner.js:287` | `_startTrace()` 在 `try` 之外，抛错时绕过 catch，用例被记成 `skipped` 且不计入 `executed` ⇒ 真实失败不可见 | 移入 try 或按失败计数 |

### LOW（可排期）
| 位置 | 问题 |
| --- | --- |
| `internal/sftpclient/resolve.go:388-391` | 父目录歧义未被透传：同显示名两目录且文件只在其中一个时，读/Remove/Rename 会静默选中歧义父目录 |
| `internal/sftpclient/resolve.go:110-112` | `"."` 被归一到 `"/"`，相对路径变成根绝对路径（包外调用者才会碰到） |
| `internal/sftpclient/resolve.go:225-231` | 索引未命中直接返回 NotExist 且不再访问远端，长连接下最长 5s 假阴性 |
| `internal/dbconsole/grid_plan.go:567-575` | `scott.emp.*` 这类限定通配符永远匹配不上，丢失 ROWID 编辑（fail-closed） |
| `internal/dbconsole/grid_plan.go:771-783` | singleflight 等待方在 `ctx.Done()` 分支不回读缓存，恰好与 leader 完成同时发生时会误降级为只读 |
| `internal/dbconsole/lob_projection.go:75-78` | 保留字守卫不再大写化，`… as\|order` 这类非法 SQL 被接受并改写执行 |
| `internal/dbconsole/lob_locator.go:200-204` | 空串键值绑成 `col = :p('')`，Oracle 下 `'' IS NULL` 永远匹配不到该行 |
| `internal/dbconsole/lob_locator.go:144-150` | 可比列过滤漏了 BFILE/SDO_GEOMETRY/ANYDATA 等非标量类型，DB-07 回退直接报错（fail-closed） |
| `internal/dbconsole/grid_value.go:419-424` | 插入绕过了逐列可写性判定，可能拼出 `INSERT INTO t ("ROWID")` 让数据库报原始 ORA 错 |
| `internal/dbconsole/grid_value.go:327-329` | 索引协议下"JSON null 且缺 has_value"被当成"未提供"而静默丢弃，多列更新仍成功 ⇒ 用户改的 NULL 没生效 |
| `internal/dbconsole/grid_plan.go:881, 641` | `PlanOracleRowIDRewrite` 零调用方；`AnalyzeGridQueryWithMetadata` 参数是未导出类型（外部不可调用） |
| `internal/httpserver/handlers_ssh_sftp.go:161-163, 227` | 远端路径/名字被 `TrimSpace`，首尾空格的合法 POSIX 名会被指到另一个文件 |
| `web/workbench/database-features.js:583-592` | `{{name}}` 的 `}}` 跨 64KB 分片时被静默丢弃，>64KB SQL 可能丢一个绑定参数 |
| `web/pages/files.js:823` | 预览弹窗"独立窗口打开"丢了 `path_id`，被拦弹窗时回退路径失去身份 |
| `web/pages/compare.js:3945`（`dispose` 3546-3556） | 关闭页签不清目录同步对话框的 `pollTimer`，后台 350ms GET 轮询持续到任务结束 |
| `tests/grid-edit-plan-wire.test.js:90/121/147` | 直接把共享 fixture 对象塞进 `session.editPlan`，无深拷贝（`grid-orderby-phase0-regression` 是拷贝的） |
| `tests/e2e/tests/43-sql-editor-large-text.js:36-45` | 布局偏好复位是"尽力而为"且带固定 sleep，未断言复位生效 |
| `scripts/verify-review-fixes.js:100-114, 189-190` | 失败重试一次即判 PASS（偶发失败被掩盖）；用"当前"测试清单跑历史 commit 的干净检出 |
| `scripts/release.sh:25-29` | 无 node 时 `check-version.js` 可选跳过 ⇒ 版本号不一致不拦发布 |
| `tests/compare-tabs-lifecycle.test.js:1128` | 防挂死 watchdog 被 `unref()`，循环空转时不会触发 |
| `tests/e2e/tests/32-http-curl-ws.js:321` | 固定 mock 端口 18094/18096 与 `/tmp/...` 路径（当日改动之外，历史遗留） |
| `tests/e2e-registration.test.js` | 只扫描 `tests/e2e/tests/*.js`，`run-real-*-clicks.js` 等仍写死 `D:\kairo-test-runtime\...` 绝对路径 |
| `docs/Kairo_Review_Implementation_20260922.md:1071-1073` | §10 复跑方法引用了仓库里不存在的脚本（`compare-tabs-repros.js` 等），需注明依赖外部证据包 |
| `docs/REVIEW-FIX-20260922-A-H.md:396` | `npm test` 写成 20/20（正确为 21/21） |
| `docs/DATABASE-WORKBENCH-SPLIT-LAYOUT-DESIGN.md:206` | 记 `database.js +384/-9`，实际 `+385/-9`（总数 722 正确）——**已修正** |

### 已确认但明确不改（记录以免误判）
- `internal/dbconsole/grid_plan.go:595/609` 前导注释被替换成空格后 `clean[6:fromIdx]` 会切错（`/* c */ SELECT a FROM t`）；与改动前**逐字相同**，非本轮引入。
- `internal/dbconsole/grid_value.go` 的 `fieldMap[strings.ToUpper(...)]` 让结果列到物理列绑定大小写不敏感（Oracle `"myCol"` vs `MYCOL` 可能撞名）；同为历史行。
- `/api/ssh/sftp/*` 命名空间整体不校验 `free_file_roots` 是**既定设计**（SSH 账号权限兜底），不是缺陷。

---

## 4. 结论

- 当日 30 个提交**没有构建/发布阻塞缺陷**；两个 HIGH（SSH 下载默认配置 403、无 SFTP 子系统主机无法新建）与一个当日旗舰回归（无主键表插入能力被关）已修复并补了回归测试，其中 sftpclient 的修复做了"失败优先"验证。
- 遗留项集中在 dbconsole 的编辑/LOB 契约边界、compare 的保存/草稿守卫、以及测试与发布脚本的证据强度上，均为 MEDIUM 及以下，已按上表分级归档，可下一轮按序清理。
- 本报告对应的二进制/压缩包由当前 HEAD 构建，见 Z 盘 `README-最新版本-2026-09-23.txt`。
