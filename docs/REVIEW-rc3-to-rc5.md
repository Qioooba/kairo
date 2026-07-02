# Code Review: v0.12-rc3 → v0.12-rc5

> 审查范围：自上次编译 v0.12-rc3 (15:46) 之后修改的 Go 文件
> 焦点模块：logquery / handlers_logs_search / handlers_logs_search_multi / handlers_autostart / autostart_windows
> 审查方式：纯代码 review，未跑测试，未在真 AIX/老 Unix 上验证
> 严重程度：**严重**（数据/业务错乱）/ **中**（行为偏离预期或限制）/ **低**（防御性 / 体验）

---

## 严重（3 条）

### 1. filename 含 `:` 时解析错位（多文件）

**位置**:
- `internal/logquery/logquery.go:1031` —— `idx1 := strings.IndexAny(line, ":-")`
- `internal/httpserver/handlers_logs_search.go:236-240` —— `idx1 := strings.Index(line, ":")`

**现象**: grep -H 输出格式 `filename:lineno:content`，当前解析器找**第一个** `:`（或 `:`/`-` 任一）作为 filename/lineno 边界。当远端文件名本身含 `:`（Linux/AIX 都允许；AIX 老系统尤其常见），idx1 会指向 filename 内部的 `:`，导致 file 字段被截断，lineno 和 content 跟着错位。

**复现**: AIX 上日志命名 `SystemOut:20260619.log`，grep 输出 `SystemOut:20260619.log:123:error msg`，被解析成：
- file = `SystemOut`
- lineno = `20260619.log` → Atoi 失败 → 整行被 `continue` 丢弃

实际 line 1031 的 `IndexAny(line, ":-")` 更激进 —— 找 `:` 或 `-` 任一。filename 里的 `-` 也会触发同样的错位（GNU/Linux 允许 `-` 在文件名里）。

**影响**: 单条命中丢失（因为解析失败 skip）；不会污染别的命中（白名单兜底），但**用户看不到应该看到的命中**。

**建议**:
- 改用"最后一个 `:`"作为边界（grep -H 实际行为就是 N 个 `:`，filename 含 `:` 时也无歧义，因为 lineno 是纯数字）
- 或改成 `IndexByte(line, ':')` 但同时验证 lineno 是纯数字
- 最稳：用 awk 在远端就把 filename 编码成 basename + 顺序号 / hash，但改协议成本高
- 短期：在 byName 白名单校验里多加一道 —— file 解析失败/为空时，整行 skip 并打 warn

**测试**: 没在真 AIX 上跑过，纯推理。

---

### 2. selected_files 注释与代码不符（大小写不匹配静默 fallback）

**位置**:
- `internal/httpserver/handlers_logs_search_multi.go:515` 注释："不区分大小写"
- `internal/httpserver/handlers_logs_search_multi.go:520` 实际：`if it.Server == target.Server && it.Dir == target.Dir`（区分大小写）

**现象**: 注释承诺"不区分大小写"，代码用 `==` 比较。如果前端传 `"Server01"` 而配置里 server name 是 `"server01"`，per-target 过滤失败 → fallback 到通用 `SelectedFiles`（line 277）→ 再 fallback 到 `latest` 模式（line 252-261）。整条请求的 scope 模式悄悄从用户期望的 `selected` 变成 `latest`，返回与预期不符的文件范围。

**复现**: 前端带 `selected_file_targets: [{server: "Server01", dir: "log", file: "sysout.log"}]`，配置里 server 是 `server01`。结果：返回最近 N 个 log 文件（latest 模式），而不是用户选的 `sysout.log`。

**影响**: 用户选了具体文件但搜不到 / 搜到错的文件，没有错误提示。

**建议**:
- 二选一：把注释改成"区分大小写"，或者把代码改成 `strings.EqualFold`
- 我倾向 EqualFold —— Windows 文件系统本身就不区分大小写，前端/用户也不该被强制记配置的大小写

---

### 3. cleanFile 静默删除单引号（应为报错）

**位置**: `internal/logquery/logquery.go:802, 848, 894, 965`

**现象**:
```go
cleanFile := strings.ReplaceAll(file, "'", "")
```
把文件名里的 `'` 静默删掉。前面 file 已经校验不能含 `'`（line 603 / 784 / 835 / 881 / 931 等多处 `strings.ContainsAny(file, "'...")`），所以正常路径不会触发。

但万一上游重构把校验去掉、或新加入口绕过校验，`O'Brien.log` 会变成 `OBrien.log`，**读到错的文件而没报错**。silent corruption 比 silent skip 更危险（用户以为搜对了文件）。

**影响**: 当前不会触发（前置校验在）；属于纵深防御 bug。

**建议**: 改成 `if strings.Contains(file, "'") { return "", fmt.Errorf(...) }`，前置校验失败时直接报错。

---

## 中（3 条）

### 4. README 文档未同步改名（多个 Kairo.exe 引用）

**位置**:
- `README.md:43` —— `**双击 \`Kairo.exe\`**`
- `README.md:513` —— 构建示例 `-o Kairo.exe .`
- `README.md:541` —— `Kairo.exe # 主程序（Win10/11）`

**现象**: 上次脚本改成输出 `Kairo_win10.exe` / `Kairo_win7.exe`，但 README 没同步。

**影响**: 新同事看 README 找不到 `Kairo.exe`，以为产物丢了；或以为系统托盘文件名是 `Kairo.exe`，出错时找错文件。

**建议**: 全局替换 `Kairo.exe` → `Kairo_win10.exe`（Win10/11 部分），Win7 那段改成 `Kairo_win7.exe`。构建命令同步更新。

---

### 5. posix_ls pattern 字符白名单丢失中文/Unicode 后缀

**位置**: `internal/logquery/logquery.go:386-388`

**现象**:
```go
if r == '*' || r == '?' || r == '[' || r == ']' || r == '.' || r == '-' || r == '_' ||
    (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
```
只保留 glob 元字符 + ASCII 字母数字 + `.-_`。中文 / Unicode 文件扩展名（如 `日志.2026.gz`、`Système.1.log`）被丢弃。

**影响**: AIX / 老 Unix 上中文命名的日志 pattern 静默失配。最新代码引入 LogQuery 模块时要意识到这点 —— 之前业务上没遇到不代表没有。

**建议**:
- 把白名单改成"排除法"：只去掉真正危险的 shell 元字符（`'` `` ` `` `$` `\` `;` `&` `|` `<` `>` `(` `)` 等），其他字符全保留
- 或者 glob 字符 + 任意非 ASCII 字符都允许

---

### 6. enrichHitsWithContext 失败静默回退（无审计/无提示）

**位置**:
- `internal/httpserver/handlers_logs_search.go:159` —— `if enriched, err := ...; err == nil { hits = enriched }`
- `internal/httpserver/handlers_logs_search_multi.go:501` —— 同上

**现象**: enrich 失败时，前端拿到的是没上下文的 hits，没有错误提示，没有审计日志。

**复现**: awk 命令因 awk 脚本太大被 shell 拒绝 / sed 超时 / charset 转换失败 → err != nil → hits 仍是原版本 → 前端"上下文"按钮像没生效。

**影响**: 用户操作失败但 UI 无感知，调试困难。

**建议**:
- err != nil 时写一条 audit：`"logs.search", "result", "context_fail", "err", err.Error()`
- 仍然把 hits 返回（不阻断主流程），但前端可以读 audit 看到错误

---

## 低（4 条）

### 7. dir 校验字符集不全（缺 `&|()<>*?~!` 等）

**位置**:
- `internal/logquery/logquery.go:341, 599, 781, 832, 878, 928` —— `strings.ContainsAny(dir, "'`$\\;")`

**现象**: dir 只拒绝 `'` `` ` `` `$` `\` `;`，不拒绝 `&` `|` `<` `>` `(` `)` `*` `?` `~` `!`。

**影响**: dir 来自 yaml 配置（用户控制）+ 白名单（运维控制），不是终端用户输入；ssh cmd 经过 shellQuote，注入被外层防御；属于纵深防御的缺失，不构成实际漏洞。

**建议**: 把所有 dir 校验统一提到一个 helper：`validateDirShellSafe(dir string) error`，字符集和 file 校验对齐（line 603）。

---

### 8. handleAdminAutoStartPut 忽略 IsAutoStartEnabled 的 err

**位置**: `internal/httpserver/handlers_autostart.go:251` —— `newActual, _ := ops.IsAutoStartEnabled()`

**现象**: 注册表实际值读失败时，actual 字段是零值 `false`，前端可能误以为"没启用"。

**影响**: UI 上能看到 KeyName/Platform/Supported 这些字段兜底，但 audit 日志里的 `actual: false` 容易误导排查。

**建议**: err != nil 时 audit 记录 `actual_err`，前端展示"读注册表失败：xxx"。

---

### 9. parseSearchOutput / ParseContextEnrichedOutput 没硬上限

**位置**:
- `internal/httpserver/handlers_logs_search.go:230` —— `for _, line := range strings.Split(out, "\n")`
- `internal/logquery/logquery.go:1023` —— 同上

**现象**: 解析器逐行加 hits，没硬上限。当前 SearchCommand 里有 `head -n max`（max 默认 200），所以实际命中数受限。但解析器没自带 limit，万一上游 SearchCommand 去掉 head 或 max 配置被改大，会爆内存。

**影响**: 当前不会爆（上游有 head）；属于"耦合假设"。

**建议**: 解析器内部加一道 `if len(hits) >= MaxParseHits { break }`，兜底保护。

---

### 10. README 还在说 `kairo` 命令，但其他文件用 `Kairo`（一致性）

**位置**: 多个 README 文件 + .gitignore 里的 `/Kairo`

**现象**: `Kairo.exe` 大写 K，但 build script 输出也是 `Kairo_win10.exe`。README / 文档 / 帮助文案里偶尔混用 `kairo` 小写和 `Kairo` 大写。KairoActivateAction.java 类名是 `KairoActivateAction`，但 git config / 部署目录可能是小写。

**影响**: 文档不一致，新人找文件时容易混乱。

**建议**: 跑一遍全文检索 `kairo` vs `Kairo`，统一大小写。Windows 文件系统不区分大小写，但 Unix / 部署脚本区分。

---

## 已通过的部分

下面这些点过了一遍，没发现问题：

- `autoStartPutCore` 的 yaml 失败回滚逻辑（line 256-294）—— 处理得相当好，三种结果（ok / rollback_ok / rollback_fail）都有 audit
- `runOneServerSearchWithScope` 的并发控制（信号量 + per-server 独立 ctx）—— 干净
- `FilterHitsByTimeWindow` 的 fallback 行为（files=nil / window=IsZero 时保留所有 hit）—— 注释充分
- `ParseQuery` 状态机的拒收形态（开头/结尾/连续操作符）—— 严谨
- `shellQuote` 双层嵌套（外层给 sh -c，内层给 cd）—— POSIX 标准做法，正确
- 注册表 API 的 cbData 用 `RegSzCbData` 算真实 UTF-16 长度（line 91-92）—— 避免中文路径的越界读
- `defer cli.Close()` 在每个 handler 里都有，没有泄漏
- 凭据解析统一走 `resolveCreds` —— 没有从 query string 取密码
- 输入限制：所有 handler 都用 `io.LimitReader(r.Body, ...)` 防大 body
- 审计日志：每个 handler 都有 `s.audit.Write(...)`，失败/成功都记

---

## 建议修复顺序

1. 先修**严重 #2**（selected_files 大小写）—— 注释与代码不符最该消歧，5 分钟改完
2. 修**严重 #3**（cleanFile 静默删）—— 改几行，影响面小
3. 修**严重 #1**（filename 含 `:` 错位）—— 需要决定改 awk 还是改解析器，影响面较大，建议先在测试环境复现再改
4. **中 #4**（README 同步）—— 一次性全局替换
5. **中 #5**（posix_ls 中文 pattern）—— 改白名单逻辑
6. **中 #6**（enrich 失败审计）—— 加一行
7. **低**按需修，不阻断发版

---

## 没审查到的部分

这次没看（即使 mtime > rc3）：
- `internal/license/*` 全套 —— 上次 v0.11-rc1 改过很多，但本次 rc3→rc5 没新增文件，只是 license_test.go 的 mtime 飘过；没动业务逻辑
- `internal/httpserver/handlers_logs_list.go` / `open_dir.go` —— mtime 改动但非搜索相关
- `internal/config/config.go` —— 配置 schema 改动，rc5 编译通过说明 schema 没破坏性变更
- `web/*` JS 改动 —— 不在 Go 审查范围
- 新增的 `_tmp-*.js` / `bugfix-0701-shots/` —— 调试产物，不入仓库

如果要全量审查（包括 license、JS），告诉我，单独跑一次。