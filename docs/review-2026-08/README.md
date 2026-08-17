# 审查方案总览 — v0.14 已推送 + v0.16 未推送代码（2026-08-16）

> 本目录是给执行 agent 的三套审查方案：**01 测试审查**、**02 真实页面点击测试**、**03 视觉 UI 审查**。
> 本文档是共享上下文：代码范围、风险矩阵、环境准备、产出物规范。三个方案各自独立可读。

## 0. 范围界定（重要假设）

- 今天是 2026-08-16，git 远程（`origin/main` = `10f2980`）与本地 main 一致，**没有未推送的 commit**。
- 「本地未推送的代码」= **工作区未提交改动**：48 个修改文件（+4355/-722）+ 20 个未跟踪文件/目录，即开发中的 **v0.16**。
- 「最近 2 天推送的代码」= 最后两个活跃推送日 **2026-07-12 ~ 2026-07-13** 的 18 个 commit（**v0.14**），见 §2。
- 两批代码有**跨批次衔接点**（SSRF 语义反复、sponsor 页被 v0.16 再次修改、版本号未升），是审查重点，见 §3。

## 1. 批次 A：未提交代码清单（v0.16 开发中）

| # | 模块 | 关键文件（新增 ✦） | 规模 | 核心行为 |
|---|------|------|------|---------|
| A1 | 宠物彩蛋 | ✦`internal/pet/`(engine/rules/ledger/sync/stats/pet/skill/battle + 5 测试)、✦`handlers_pet.go`(374)、✦`web/pages/pet.js`(545)、`home.js`(6 击解锁)、`sponsor.js`(+367，宠物榜 tab)、✦`web/img/pet/`、✦`docs/PET-FEATURE-DESIGN.md` | ~3700 行 | 关于卡片 10s 内点 6 次解锁；audit 订阅涨经验（未解锁零记录）；pet.json HMAC 落盘；浮动宠物可拖/晃动换肤/说话/改名；武林榜页宠物榜 tab + sync |
| A2 | 定时任务 | ✦`internal/schedtask/`(manager/runner/store/task + 2 测试)、✦`internal/cronx/`(+测试)、✦`handlers_tasks.go`(253)、✦`web/pages/tasks.js`(512)、`index.html`(导航) | ~2100 行 | cron 调度执行**本地 shell 命令**；time.AfterFunc 事件驱动；data/sched_tasks.json + 运行历史(≤20 条/任务) |
| A3 | HTTP 增强 | ✦`handlers_http_curl.go`(444+测试)、✦`handlers_http_ws.go`(357)、`http.js`(+392) | ~1200 行 | `/api/http/curl-parse` 粘贴 curl 填充表单；`/api/http/ws/{connect,send,poll,close}` WebSocket 调试 |
| A4 | SSH 配置档案 | ✦`internal/sshclient/profilestore.go`(169+测试)、`ssh.js`(+432) | ~600 行 | 连接 profile 持久化 + 终端页 UI |
| A5 | WebService SSRF 回归 | ✦`internal/webservice/ssrf.go`(137)、`wsdl.go`(+396 重写)、`soap.go`、`client.go`、`handlers_webservice_ssrf_test.go`(重写) | ~700 行 | **重新引入** SSRF 防护（拦 link-local/组播/unspecified，放行私网+回环，拨号期防 DNS rebinding）；WSDL 解析大改 |
| A6 | 提醒动作扩展 | `reminder.go`(+258)、`manager.go`、`handlers_reminder.go`、`reminders.js`(+193)、`reminder_test.go`(+277) | ~900 行 | 触发动作从仅 popup 扩展为 popup/url/**command(本地命令 30s 超时)** 三种 |
| A7 | 安全修复 | `httpserver.go`(sanitizeAppConfig + CORS 拒绝日志)、`handlers_config_yaml.go`、`handlers_config_yaml_test.go`(+42) | ~120 行 | `/api/config` 不再下发 `credential_key`/`kairo_internal_token`；导出脱敏 kairo token 与 endpoint auth（已有测试） |
| A8 | 其他改动 | `logquery.go`(+203)、`handlers_files.go`(+182)、`handlers_format.go`(重构)、`audit.go`(+133，Subscribe)、`helpers.go`、`handlers_logs_search*.go`、`main.go`(接线)、`config.go`(pet/internal_endpoints 块) | ~900 行 | audit.Subscribe 订阅机制；日志窗口搜索；文件/格式化 handler 调整 |
| A9 | 新增测试文件 | ✦`subscribe_test`、`handlers_http_curl_test`、`handlers_pet_test`、`windowsearch_test`、`profilestore_test`、`compat_test`、`hengli_upload_verify_test`、`multiip_test`、`cronx_test`、`engine_test`、`pet_test`、`rules_test`、`stats_test`、`sync_test`、`manager_test`、`schedtask_test` | ~2600 行 | 见 01 方案逐个审查 |

## 2. 批次 B：07-12 ~ 07-13 已推送清单（v0.14，18 commits）

| Commit | 内容 | 审查关注点 |
|---|---|---|
| `3a8da9d` | about 页 12 section IO 懒渲染，首屏节点 -95% | 滚动加载不漏 section；5 主题下 content-visibility 无闪烁 |
| `979ae99` | endpointclient 共享 HTTP 调用器 + License 审计增强 | 主备切换/超时/Basic Auth；竞态修复有效 |
| `32a20ee`/`b2cd572` | sponsor 排行榜 Go 后端 + Mock 服务器 + Web UI | 榜单渲染、失败降级 |
| `d5c541c` | sponsor 榜异步加载，失败改随机搞笑提示 | 失败不泄露内部地址 |
| `11903d2`/`235b1d9` | README v0.14 重写 | 无需测代码，抽查事实 |
| `033c642` | 投喂导航分组 + SOAP 占位符优化 + 构建精简 | webservice 页回归 |
| `016f130` | 审查修复：凭据外置/数据竞争/错误泄露/XML 注入 | 修复真实有效、无回归 |
| `04e1821` | revert：激活/排行榜地址恢复硬编码 | 与 `d813690` config 块并存语义 |
| `b5f88e9` | sponsor updated_at 改 string 透传 | Java DATETIME 兼容 |
| `de0275f` | **SSRF 防护移除** + 榜 5min 缓存 + 防重提交 | ⚠ 与批次 A5 直接矛盾，见 §3 |
| `715424b` | Win7 emoji 兼容 + http 美化 + about 渐变 + 7 SVG 图标 | 新 UI 不得引入裸 emoji（视觉方案检查） |
| `833d397` | reminder Update 字段合并加固 | 前端漏传不被清空（A6 又改了同文件，一起测） |
| `d813690`/`10f2980` | config internal_endpoints 块 + 构建剥离 + gitignore | build 脚本剥离后二进制无内网地址 |

## 3. 跨批次衔接点（必须审）

| # | 问题 | 现状 | 要求 |
|---|------|------|------|
| X1 | **SSRF 语义反复**：07-13 `de0275f` 推送「移除 SSRF 防护」（测试断言无限制），未提交代码又新增 `ssrf.go` 精细防护（拦 link-local/组播/unspecified，放行私网+loopback）并重写测试 | 工作区为最终态 | 确认最终语义是「精细拦截」，旧测试 `TestWSDLImportURL_NoIPRestriction` 已被 `TestWSDLImportURL_BlocksDangerousTargets` 取代；行为与设计注释一致 |
| X2 | **sponsor.js 二次修改**：v0.14 推送的 sponsor 页被 A1 加了宠物榜 tab（+367 行） | 两功能同文件交织 | 点击/视觉测试需同时覆盖武林榜 + 宠物榜 + 未解锁无 tab |
| X3 | **版本号未升**：代码注释已写 v0.16，但 VERSION/about.js/index.html/README 徽章仍 v0.14 | 六处版本号一致性规则（httpserver.go 注释） | 审查中标记，由人决定是否随提交升版 |
| X4 | reminder 同文件双改：B 批 `833d397` 字段合并加固 + A6 动作扩展 | 同一 `manager.go`/`reminder.go` | 回归测试需同时覆盖两个语义 |
| X5 | pet sync 端点走 `internal_endpoints.pet_leaderboard`（复用 endpointclient 模式），未配置时优雅降级 | config.yaml 当前**无 pet 块**（默认编译进二进制） | 验证未配置时 `/api/pet/sync` 不请求硬编码地址、前端显示「仅本地展示」 |

## 4. 风险矩阵（决定三方案优先级）

| 级别 | 项 | 为什么 |
|---|---|---|
| P0 | A2 schedtask / A6 reminder-command 执行**本地命令** | 命令注入面、超时、并发、审计完整性 |
| P0 | A7 配置脱敏回归 | 密钥泄露面，已修需回归证据 |
| P0 | X1 SSRF 最终语义 + A5 wsdl.go 重写 | 安全语义反复 + 大改 |
| P0 | A1 pet 引擎挂在 audit 主流程 | 回调 panic/延迟影响全部审计写盘 |
| P1 | A3 WS 连接生命周期 / A4 profilestore 凭据 / A1 HMAC 与 ledger | 资源泄露、落盘正确性 |
| P1 | B 批 endpointclient 主备、sponsor 缓存/防重、about 懒渲染 | 已推送功能的回归验证 |
| P2 | 各页 UI 交互与 5 主题视觉 | 体验面 |

## 5. 三方案与执行顺序

| 方案 | 文件 | 类型 | 依赖 |
|---|---|---|---|
| 01 测试审查 | [01-test-review-plan.md](./01-test-review-plan.md) | 静态/单测/API，离线可跑 | 无，先行 |
| 02 页面点击测试 | [02-e2e-click-test-plan.md](./02-e2e-click-test-plan.md) | Playwright 真实浏览器 | 需运行实例；建议 01 严重问题修复后执行 |
| 03 视觉 UI 审查 | [03-visual-ui-review-plan.md](./03-visual-ui-review-plan.md) | 截图矩阵 + 走查 | 依赖 02 功能稳定；可与 02 共用实例 |

建议顺序：**01 → (修 P0) → 02 → 03**。三个 agent 分工：agent-1 跑 01，agent-2 跑 02，agent-3 跑 03；02/03 可并行启动但 03 结论以 02 修复后版本为准。

## 6. 公共环境准备（三个方案共用，只做一次）

```bash
# 构建（注意：sdk/go120 是 Win7 构建用的 vendored SDK，不属于模块，勿用 go build ./...）
go build . ./internal/... ./cmd/...

# 隔离运行实例（数据目录 = $HOME/.kairo，用独立 HOME 避免污染真实数据）
export KAIRO_TEST_HOME=/tmp/kairo-review-2026-08
mkdir -p $KAIRO_TEST_HOME
cp config.yaml $KAIRO_TEST_HOME/.kairo/config.yaml   # 首次启动后按需要改
HOME=$KAIRO_TEST_HOME go run . &                      # 默认端口见启动日志（acceptance 用 18090，e2e 约定 18092）

# e2e 依赖（已装）：node + playwright@1.61
cd tests/e2e && BASE_URL=http://127.0.0.1:18092 node index.js --grep <场景>
```

约定：测试实例统一 **18092** 端口（与 tests/e2e 默认 BASE_URL 对齐）；pet 解锁可 UI 六击或直接 `POST /api/pet/enable`；mock SSH 用 `scripts/mock_sshd.py`（端口 2225）；sponsor 后端用 `cmd/mock-sponsor-server`。

## 7. 产出物规范（三个 agent 统一）

- 所有报告写到 `test-results/review-2026-08/`（已建目录，git 忽略 test-results 则无需提交，报告正文同时回贴给用户）。
- 报告统一结构：`结论摘要（PASS/FAIL 计数）→ 问题清单（严重度 P0/P1/P2 + 文件:行 + 复现步骤 + 建议）→ 附录（原始日志/截图路径）`。
- 截图命名：`{方案}-{页面}-{主题}-{状态}-{视口}.png`，存 `test-results/review-2026-08/screenshots/`。
- **只审查不改代码**：发现问题记录在报告，修复留给后续轮次；除非方案中明确允许（如 02 新增 e2e 测试文件）。
