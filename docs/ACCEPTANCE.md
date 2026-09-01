# 第一阶段验收清单（ACCEPTANCE）

> 目标：把"首页 + WebSphere 日志助手 + 报文格式化"这版工具跑稳。
> 验收前请先按顺序完成下列步骤，全部通过再开始日常使用。

---

## 0. 准备

| 项 | 说明 |
| --- | --- |
| 工具包 | 仅 `Kairo.exe`；首次启动在用户配置目录创建 `config.yaml`、`downloads/`、`logs/`、`data/` |
| Windows 版本 | 主线支持 Win10/11；Win7 仅在 legacy 分支独立验收 |
| 启动方式 | **先用 `cmd` 启动**（不要直接双击），看完整日志 |
| 测试服务器 | 至少准备 1 台 Linux 机器，能 SSH 上、有 WebSphere 日志目录 |

`config.yaml` 默认有"信贷生产 / 信贷测试"两组示例，按需改 IP / 用户名 / 路径 / 文件规则。

---

## 1. 启动验收

| # | 操作 | 期望结果 |
| --- | --- | --- |
| 1.1 | 把交付目录放到 `D:\kairo\` | 目录结构完整 |
| 1.2 | `Win+R` → `cmd` → `cd /d D:\kairo` → `Kairo.exe` | 控制台输出 `[Kairo] 工具箱已启动: http://127.0.0.1:18080` 等日志 |
| 1.3 | 默认浏览器自动打开 `http://127.0.0.1:18080` | 进入首页，看到 6 个功能卡片 |
| 1.4 | 左侧菜单点击"WebSphere 日志" | 进入日志助手页面，能看到系统/服务器下拉 |
| 1.5 | 左侧菜单点击"报文格式化" | 进入格式化页面 |
| 1.6 | 左侧菜单点击"系统配置" | 看到 config.yaml 中所有 systems/servers/log_dirs |
| 1.7 | 关闭控制台 | 服务退出（`[Kairo] 服务已停止，再见。`） |

如果浏览器没自动打开：手动访问 `http://127.0.0.1:18080`。

如果端口被占用：改 `app.port` 后重启。

---

## 2. WebSphere 日志助手验收

**重要：必须用真实 Linux 测试服务器 + 真实 SSH 账号**。用示例 IP 测不出来问题。

### 2.1 SSH 测试连接

| # | 操作 | 期望结果 |
| --- | --- | --- |
| 2.1.1 | 选系统 → 选服务器 → 选日志目录 → 填 SSH 用户名 + 密码 → 点 **测试连接** | 提示"连接成功" |
| 2.1.2 | 故意填错密码 | 提示"SSH 连接 ... 失败"，**密码不会出现在错误信息里** |
| 2.1.3 | 不填密码点测试 | 提示"缺少用户名或密码" |
| 2.1.4 | 查看 `logs/audit.log` | 出现 `op=ssh.test result=ok` 或 `result=fail` 记录，**不包含密码** |

### 2.2 列出日志文件

| # | 操作 | 期望结果 |
| --- | --- | --- |
| 2.2.1 | 点 **列出文件** | 表格展示该目录下匹配 patterns 的文件，按修改时间倒序 |
| 2.2.2 | 文件数 | 默认最多 100 条 |
| 2.2.3 | 字段 | 文件名 / 大小 / 修改时间 / 路径 都正确 |
| 2.2.4 | 故意选一个空目录 | 表格为空，不报错 |
| 2.2.5 | 目录被改成 `/` 之类的非法路径 | 应该被 config 校验或目录白名单拒绝，**不会**真去 `ls /` |

### 2.3 下载最新日志

| # | 操作 | 期望结果 |
| --- | --- | --- |
| 2.3.1 | 点 **下载最新日志** | 出现"下载完成"卡片，文件名形如 `<server>_<time>_<name>.log` |
| 2.3.2 | 点击下载链接 | 文件下载到本地，体积与远程一致 |
| 2.3.3 | 用文本编辑器打开 | 中文不乱码（如果日志是 UTF-8）或按 GBK 解码后正常（如果日志是 GBK） |
| 2.3.4 | 重复点下载 | 不覆盖已存在的文件（文件名含秒级时间戳） |

### 2.4 关键词搜索

测试用例（输入框 + 文件范围默认 3）：

| # | 表达式 | 期望 |
| --- | --- | --- |
| 2.4.1 | `Exception` | 命中含 Exception 的行 |
| 2.4.2 | `Exception && userinfo` | 同一行同时含两个词（AND） |
| 2.4.3 | `Exception \|\| Timeout` | 任一关键词（OR） |
| 2.4.4 | `Exception && !DEBUG` | 含 Exception 但不含 DEBUG |
| 2.4.5 | `!DEBUG` | 排除含 DEBUG 的所有行 |
| 2.4.6 | `Exception; cat /etc/passwd` | **被拒绝**，返回"关键词含非法字符" |

每个用例：
- 命中行有高亮（橙色背景）
- 点 **查看上下文** 弹出前后 30 行
- 命中行在上下文里再次高亮
- 不下载整个文件

### 2.5 上下文查看

| # | 操作 | 期望 |
| --- | --- | --- |
| 2.5.1 | 点搜索结果"查看上下文" | 上下文卡片出现，行号连贯 |
| 2.5.2 | 命中行 | 在上下文里高亮 |
| 2.5.3 | 上下文区 | 可以上下滚动；不下载整个日志 |
| 2.5.4 | 行号 1 的命中 | 上下文从 1 开始（不报错） |
| 2.5.5 | 文件不存在 / 改了 | 提示"远程命令退出码非 0" |

### 2.6 远程命令兼容性

| # | 命令 | 期望 |
| --- | --- | --- |
| 2.6.1 | `which find grep sed ssh sftp` | 都存在 |
| 2.6.2 | `which timeout` | **不需要存在**（已改用 Go 侧 ctx 控制） |
| 2.6.3 | `echo $LANG` | 中文系统一般是 `zh_CN.UTF-8`，GBK 机器可能是 `zh_CN.GBK` 或 `zh_CN.GB18030` |

如果远程是 GBK：在 `config.yaml` 的 `log_dirs` 里加 `encoding: gbk`（已支持 GBK → UTF-8 自动转换）。

### 2.7 服务器压力控制

| # | 操作 | 期望 |
| --- | --- | --- |
| 2.7.1 | 搜索范围选 10 | 仍能限制 max 200 条 |
| 2.7.2 | 搜索超时（30s 默认） | 客户端 ctx 到时自动 SIGTERM → 1s → SIGKILL |
| 2.7.3 | 重复点搜索/下载 | 不会出现并发风暴（HTTP handler 是顺序的） |

---

## 3. 报文格式化验收

| # | 输入 | 操作 | 期望 |
| --- | --- | --- | --- |
| 3.1 | `{"a":1,"b":[1,2,3]}` | 格式化 JSON | 多行带缩进 |
| 3.2 | 上面格式化结果 | 压缩 JSON | 单行 |
| 3.3 | `not json` | 校验 JSON | 错误提示"JSON 非法" |
| 3.4 | `<a><b>1</b><b>2</b></a>` | 格式化 XML | 多行带缩进 |
| 3.5 | 格式化结果 | 压缩 XML | 单行 |
| 3.6 | 任何 | 复制结果 | 剪贴板有内容 |
| 3.7 | 任何 | 清空 | 输入输出都清空 |

---

## 4. 审计日志验收

`logs/audit.log` 应该出现的操作类型（按时间顺序）：

```
op=ssh.test
op=logs.list
op=logs.download
op=logs.search
op=logs.context
```

每条至少含：
- `ts=` 时间戳
- `op=` 操作类型
- `system=` `server=` `dir=` （ssh/list/download/search/context）
- `query=` （search）
- `result=ok` 或 `result=fail`
- `err=` （失败原因）

**绝对不能**出现：
- 任何包含 `password` 字段的内容
- 任何日志正文片段

---

## 5. 安全性验收

| # | 攻击向量 | 期望 |
| --- | --- | --- |
| 5.1 | 把 `app.host` 改成 `0.0.0.0` | 启动时被 config 校验拒绝 |
| 5.2 | 搜索框输入 `; cat /etc/passwd` | 返回"关键词含非法字符" |
| 5.3 | 搜索框输入 `` `whoami` `` | 同上 |
| 5.4 | 搜索框输入 `$(id)` | 同上 |
| 5.5 | 选一个不在 config 里的目录 | 返回"目录不在白名单中" |
| 5.6 | 故意构造不合法文件规则 | config 启动时被拒 |
| 5.7 | 故意构造 `app.host=8.8.8.8` | 启动失败 |
| 5.8 | 翻看 `logs/audit.log` | 看不到任何密码、SSH 私钥、日志正文 |
| 5.9 | 浏览器地址栏 `http://127.0.0.1:18080/downloads/x%0D%0AX-Evil-Header: hacked` | 404，绝不出现注入的响应头 |
| 5.10 | 浏览器下载中文文件名 `日志.zip` | `Content-Disposition` 同时含 `filename=` 和 `filename*=UTF-8''<percent-encoded>`（RFC 5987）|
| 5.11 | 浏览器打开 `/api/config` | 返回 JSON 含 `paths.download_dir / log_dir / data_dir` 三个绝对路径字段 |
| 5.12 | 看 sidebar footer | 应显示"已启动 · <app名> · 保存到 <绝对路径>" |

---

## 5b. v0.3 文件浏览器（任意路径下载）补充验收

| # | 操作 | 期望 |
| --- | --- | --- |
| 5b.1 | 进入「文件下载」菜单 | 顶部连接区可选手系统/服务器 + 用户名 + 密码 |
| 5b.2 | 选服务器 → 输入密码 → 默认进入 `/` | 列出该 SSH 账号能看到的目录条目（dir / size / mode / mtime） |
| 5b.3 | 面包屑点「上级」回到上一级目录 | 路径栏同步更新 |
| 5b.4 | 路径框输入 `/opt` 回车 | 跳到 /opt 列目录 |
| 5b.5 | 勾选 2 个文件 → 「打包 zip」勾选 → 「下载选中」 | 进度条滚动，完成后「下载历史」里出现 .zip 文件 |
| 5b.6 | 在「下载历史」点下载链接 | 浏览器下载文件，文件名原始名（中文也正常） |
| 5b.7 | 在「下载历史」点删除 | 该文件从列表消失，但其他文件不受影响 |
| 5b.8 | 故意选 101 个文件 | 400 错误 "单次最多下载 100 个文件" |
| 5b.9 | 路径框输入 `opt/a.log`（相对路径） | 400 "path 必须是绝对路径（以 / 开头）" |
| 5b.10 | 故意路径含换行 `\n` | 400 "path 含非法字符" |
| 5b.11 | 下载中点「停止」 | SSE 事件流关闭，dlmanager 标记 cancelled，session 仍存在供前端拉 done |
| 5b.12 | 切到其他 tab 再回来 | 旧 EventSource 已 close，新订阅正常 |

---

## 5c. tail 会话管理（v0.2 起）

| # | 操作 | 期望 |
| --- | --- | --- |
| 5c.1 | WebSphere 日志页选文件 → 「实时跟踪」 | 返回 `tail_id`，SSE 立即开始推新行 |
| 5c.2 | 跟踪中关闭浏览器 tab | 服务端 ctx cancel，SSH session 关闭，无 goroutine 泄漏 |
| 5c.3 | 跟踪超过 5 分钟（默认 idleAfter） | 兜底：killSSH，session 结束 |
| 5c.4 | 中文日志行 | SSE 收到的 JSON line 字段是 UTF-8（不是字节截断的乱码） |

---

## 5d. 测试与回归（保证改动不破坏既有功能）

| # | 操作 | 期望 |
| --- | --- | --- |
| 5d.1 | 开发者本地跑 `go test ./...` | 12 个包全过；dlmanager 90%+，tailmgr 90%+，httpserver 70%+ |
| 5d.2 | 跑 `go test -mod=vendor ./...` | vendor 模式下全过；构建不依赖外网 |
| 5d.3 | 跑 `node web/app.test.js` | 7 个前端 pure 函数全过（escapeHtml / formatBytes / formatTime / trimMiddle / cssEscape / pctText / validate） |
| 5d.4 | 跑 `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -mod=vendor` | macOS / Linux 上能交叉编译出 Windows exe，不联网 |
| 5d.5 | 跑 `./scripts/build_windows_amd64.sh v0.3.0` | 脚本自动检测 vendor/ 并加 `-mod=vendor`，产物在 `dist/kairo-v0.3.0/Kairo.exe`，`file` 命令验证是 PE32+ x86-64 |
| 5d.6 | 跑 `./scripts/package_windows.sh v0.3.0` | `dist/kairo-v0.3.0-windows.zip` 包含 exe + config + README + 空 downloads/logs/data |
| 5d.7 | 跑 `gofmt -l .` | 0 行（包含 `vendor/` 因为已 commit） |

---

## 6. 第一阶段明确不做的事

以下功能**暂时不做**，避免范围蔓延；如确需后续再做：

- [x] 密码本地保存（DPAPI / keychain）— 已完成
- [x] 多文件 zip 打包下载 — 已完成
- [x] 实时 tail（不下载看增量）— 已完成
- [ ] 任意命令执行（被设计为禁止）
- [x] 多服务器并发搜索（第一版顺序）— 已完成
- [ ] 常用命令模块
- [ ] 用户权限 / 登录
- [x] 操作历史页面 — 已完成
- [ ] HTTPS / 鉴权（只本地监听，不暴露公网）

> 日期 / 日历、文本处理两个模块已从产品中移除，不规划。

---

## 7. 验收通过标准

- [ ] 第 1、2、3、4、5 节全部用例通过
- [ ] `go test ./...` 全过
- [ ] `macOS` 上 `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build` 成功
- [ ] `go.mod` 顶部为 `go 1.24`，使用 Go 1.24+ 工具链构建主线
- [ ] 没有外部 CDN / React / Vue / Electron 依赖

满足以上条件即视为第一阶段验收通过，可以进入日常使用和第二阶段规划。

---

## 5f. v0.5 用户体验 20 条需求验收（v0.5-A / B / C / D / E / F 全量）

背景：用户在 v0.4 用了一段时间，提了 20 条问题（v0.5-PRE 文档里的 5 大类）。v0.5 用了 6 个 commit 全部修完（A-F）。下面每条对应一个具体场景验收。

### 5f.1 重大 bug（5 条 P0）

| # | 用户原话 | 验收方式 | 状态 |
|---|---------|---------|------|
| P0-01 | 重启服务运行 exe，功能全部丢失；切 tab 内容也没了 | 系统配置页改某 server host → 切到「文件下载」→ 切回 → 内容还在；点「保存」→ 重启进程 → GET /api/config 内容一致 | ✅ v0.5-A 23f7e8f + 23f7e8f |
| P0-02 | 编码选 GBK 后保存还是 utf-8 | 选 GBK → 保存 → GET /api/config dir.encoding === "gbk" | ✅ v0.5-A 23f7e8f + 5e |
| P0-03 | 多服务器点击「列出文件」只展示一台 | 选 2 台 → 「列出文件」 → 看到 2 段分组（`server-group` 各自 ok） | ✅ v0.5-D 18ce504 + 5e |
| P0-04 | 日志搜索没反应；没指定/模糊匹配文件名的搜索 | 选 server + dir → 输入 "Exception" + file_pattern "SystemOut*.log" → 命中展示 | ✅ v0.5-D + 5e file_patterns |
| P0-05 | 实时 tail 点了浏览器就死 | 点"新窗口 Tail" → 独立页 `tail.html` → 1 分钟连续刷不卡；主页内嵌 tail 改 buffer + rAF 批处理（5000 行上限）| ✅ v0.5-D 18ce504 + 5e |

### 5f.2 核心体验（6 条 P1）

| # | 用户原话 | 验收方式 | 状态 |
|---|---------|---------|------|
| P1-06 | 一台 server 应能多选日志目录 | 选 1 server → 勾 2 个 dir → "列出文件" 或 "搜索" 看到 2 个 target 分组 | ✅ v0.5-D 18ce504 + 5e |
| P1-07 | 多 server 并行搜索没勾选 server / 目录 | 搜索卡片走 targets 数组；前端 getSelectedTargets() 多对多 | ✅ v0.5-D 18ce504 |
| P1-08 | 不支持单文件/多文件/glob 文件名搜索 | filePatternInp 接受逗号/空格分隔的 glob；后端 file_patterns 字段覆盖配置 patterns | ✅ v0.5-E 0b98581 + 5f UI 改进 |
| P1-09 | 文件下载页没常用目录 | 「常用目录」按钮栏（点即跳转）+「⭐ 收藏当前路径」+「⚙ 管理」（弹 modal 改别名/路径/删/上下移）| ✅ v0.5-E 0b98581 |
| P1-10 | 文件列表没模糊搜索 | filter input 接受子串 / `*.log` / `SystemOut*` / `log?` 通配 + "过滤后 X / Y" 计数 | ✅ v0.5-E 0b98581 |
| P1-11 | 点击文件名新窗口预览 | 后端 `/api/files/preview`（前 1MB / 上限 10MB / utf-8 或 gbk / 二进制检测）；前端 modal 默认弹 + Shift+点击 → `/preview.html` 独立新窗口 | ✅ v0.5-E 0b98581 |
| P1-12 | 下载完成不知道文件在哪 | Item 加 `abs_path` 字段；`/api/local/reveal-file` + `/api/local/open-folder` + `/api/downloads/{name}/open-dir`（B3）；下载完成 → `Kairo.core.notify` 右上角通知（含 title/path/操作按钮「📂 打开 / 📋 复制 / 📜 查看下载历史」） | ✅ v0.5-F a53840f + 5f |

### 5f.3 页面可用性（9 条 P2）

| # | 用户原话 | 验收方式 | 状态 |
|---|---------|---------|------|
| P2-13 | 业务系统/服务器/日志目录框不明确、复制说明不清、目录别名/文件名规则重复 | 「业务系统 N / 服务器 N / 日志目录 N」编号 badge（紫色/绿/黄边）；复制按钮改名「📋 复制服务器（含日志目录）」+ 自动 -copy 后缀 + tooltip；目录别名/远端路径/编码/文件名规则都有字段说明卡 | ✅ v0.5-F a53840f + 5f |
| P2-14 | 保存按钮位置不合理 | `.cfg-save-footer-bar` 改 `position: fixed; left: 240px; bottom: 0; z-index: 50;` + 视口 padding-bottom 80px 防遮挡；导航到 config 时 view 加 class `has-sticky-footer` | ✅ v0.5-F a53840f |
| P2-15 | 复制服务器按钮加说明 | 同 P2-13 合并 | ✅ v0.5-F |
| P2-16 | 系统紫色图标"系统"两字横排 | `grid-template-columns: auto 1.4fr 2fr auto`（不再固定 36px 挤成竖排）；新增 tier-badge "业务系统 N" badge | ✅ v0.5-C 4d323a3 + 5f |
| P2-17 | 记住密码默认勾上 | `files.js` `rememberChk.checked = true`；`websphere.js` 同 | ✅ v0.4-final + v0.5 |
| P2-18 | 目标服务器默认勾上 + 记忆上次操作 | `Kairo.core.lastGet/lastSet('websphere', 'sel', {system, servers, dirs, username})`；进入页面 `lastForSys` 恢复 | ✅ v0.4-final + v0.5 |
| P2-19 | 背景色可切换 | `web/theme.js` + localStorage 持久化 + index.html inline script 防 FOUC + 顶栏 ☀️/🌙 按钮 | ✅ v0.5-C 4d323a3 |
| P2-20 | FTP 下载到指定目录 | `dlTargetDirInp` + 后端 `validateTargetDir`（绝对路径/mkdir/写探针）+ zip 路径也跟 target_dir 走 | ✅ v0.5-D 18ce504 |

### 5f.4 总览

- v0.5 6 个 commit：A / B(无独立 commit 合并到 E) / C / D / E / F
- 累计修改：约 3000+ 行（含 50+ 新测试）
- go test ./... 13 包全 pass（含 8 个新 preview 测试 + 2 个 new local 测试 + 4 个多 server 列文件测试）
- node web/app.test.js 13/13 pass
- go.mod x/text 升级（v0.5-D 引用 simplifiedchinese.GBK decoder）
- 没有破坏 v0.4 接口（向后兼容 targets > servers > server 三模式）
