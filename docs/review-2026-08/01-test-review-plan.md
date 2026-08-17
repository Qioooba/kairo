# 方案 01 · 测试审查方案（单测质量 + 覆盖 + 缺口 + API 冒烟）

> 执行者：agent-1。只读审查 + 跑测试，**不改业务代码**。
> 产出：`test-results/review-2026-08/01-test-review-report.md` + 覆盖率文件。
> 范围：批次 A（未提交 v0.16）+ 批次 B（07-12~07-13 v0.14），清单见 [README](./README.md)。

## 目标

1. 两批代码合入后**全量测试真实通过**（含 race），建立基线证据。
2. 新增/大改的测试**质量审查**：断言强度、边界覆盖、flaky 风险、是否测了该测的语义。
3. **缺口矩阵**：功能 × 单测 × API 测试 × e2e，标出裸奔代码。
4. 安全敏感修复（脱敏/SSRF/命令执行/竞态）有**针对性回归测试且语义正确**。

## Phase 0 · 基线（先于一切，结果写进报告 §基线）

```bash
go build . ./internal/... ./cmd/...                      # 编译（勿用 ./...，sdk/go120 非模块）
go vet . ./internal/... ./cmd/...
go test . ./internal/... ./cmd/... 2>&1 | tee test-results/review-2026-08/p0-test-all.log
# 并发敏感包加 -race -count=2：
go test -race -count=2 ./internal/pet/... ./internal/schedtask/... ./internal/cronx/... \
  ./internal/audit/... ./internal/reminder/... ./internal/endpointclient/... \
  ./internal/sponsor/... ./internal/sshclient/... ./internal/webservice/... ./internal/httpserver/...
# 覆盖率（模块全量 + 新包单列）
go test -coverprofile=test-results/review-2026-08/cover.out . ./internal/... ./cmd/...
go tool cover -func=test-results/review-2026-08/cover.out | grep -E "pet|schedtask|cronx|ssrf|profilestore|handlers_(pet|tasks|http_curl|http_ws)"
```

- 基线要求：build/vet 0 错误；全量 test 0 FAIL；race 0 DATA RACE。任何一项不过 → 报告标 P0 并继续后续阶段（除非编译失败则中止）。
- 已知基线（2026-08-16 本机实测）：build ✓、vet ✓（`go build ./...` 会因 sdk/go120 报错，属预期，须在报告中注明避免误报）。

## Phase 1 · 新增/大改测试文件逐个审查

对下表每个文件：(a) 通读；(b) 按 §Phase1 检查单打分；(c) 记录问题（文件:行 + 严重度）。

| 文件 | 被测对象 | 必须覆盖的语义（缺一项记 P1） |
|---|---|---|
| `internal/pet/pet_test.go` | 状态/落盘/HMAC | enable 幂等；**未解锁零文件副作用**；HMAC 篡改 → 回退 .bak → 再失败重置并写 `pet.corrupt` 审计；原子写(tmp+rename) |
| `internal/pet/engine_test.go` | 经验引擎 | op→exp 映射（未列出 op=0 分）；同 op+target 60min 冷却去重；每日上限截断；会话时长每 10min+1 且单会话封顶+5；no-op gate（未解锁回调零副作用）；防抖落盘 |
| `internal/pet/rules_test.go` | 规则默认值+config 覆盖 | 默认表完整；config 部分覆盖合并；非法值兜底 |
| `internal/pet/stats_test.go` | 升级曲线 | `100*level^1.5` 边界；阶段区间 egg/hatchling/grown/mythic 跨档 |
| `internal/pet/sync_test.go` | 服务器同步 | 协议序列化字段齐全；失败保留 dirty；旧分单调性本地预检；未配置端点**不发请求**优雅降级（X5） |
| `internal/pet/ledger`（含于 pet_test） | 环形流水 | ≤2000 条溢出淘汰最旧；只含 op/ts/exp（**无 host/IP**，隐私） |
| `internal/audit/subscribe_test.go` | Subscribe | 回调 panic 被 recover 不影响 Write；unsubscribe 生效；锁外回调（回调里再 Write 不死锁） |
| `internal/schedtask/manager_test.go` + `schedtask_test.go` | 调度/存储 | cron 到点触发；toggle 启停；**并发 run 防重入**；命令 30s 超时杀进程；store 原子写+损坏恢复；runs 截断 20 条；删除任务停止未来调度 |
| `internal/cronx/cronx_test.go` | cron 解析 | `*/n`、范围、列表、非法表达式报错、next 计算跨日/跨月 |
| `internal/httpserver/handlers_pet_test.go` | pet API | 未解锁 `/api/pet/state` 返回 `{enabled:false}` 且其余 404；enable/name(≤16 去控制字符)/pos/skin(范围校验) 参数校验；nil 引擎 404 |
| `internal/httpserver/handlers_http_curl_test.go` | curl 解析 | 合法 curl→method/url/header/body 填充；非法输入 400；`-k`/compressed 等常见 flag；**拒绝非 http(s) scheme** |
| `internal/httpserver/handlers_tasks.go`（无直接测试→缺口） | tasks API | 见 Phase 3 缺口 |
| `internal/httpserver/handlers_http_ws.go`（无直接测试→缺口） | WS 端点 | 见 Phase 3 缺口 |
| `internal/sshclient/profilestore_test.go` | profile 存储 | 增删改查往返；损坏文件恢复；并发写；凭据不明文落盘 |
| `internal/webservice/compat_test.go` / `multiip_test.go` / `hengli_upload_verify_test.go` / `wsdl_test.go`(+434) | WSDL/SSRF | 多 IP 解析全放行或全拒绝语义；Dial 期 TOCTOU 复检；link-local/组播/unspecified 拦截、私网+loopback 放行（X1 最终语义） |
| `internal/httpserver/handlers_webservice_ssrf_test.go`（重写） | SSRF HTTP 层 | 已确认 `TestWSDLImportURL_NoIPRestriction` 被 `..._BlocksDangerousTargets` 取代（169.254.169.254/0.0.0.0/fe80::1 → 400） |
| `internal/httpserver/handlers_config_yaml_test.go`(+42) | 导出脱敏 | kairo token/endpoint auth/auth.tokens 均脱敏且 `enabled` 保留（已有，验证其通过） |
| `internal/httpserver/handlers_files_test.go`(+98) / `httpssh_test.go`(+21) / `httpserver_test.go`(+22) | 文件/接线 | 读懂新增用例意图并核对有效性 |
| `internal/reminder/reminder_test.go`(+277) | 动作扩展 | popup/url/command 三分发；command 30s 超时；字段合并加固（B 批 833d397）回归；漏传字段不被空串清空 |
| `internal/logquery/windowsearch_test.go` | 日志窗口搜索 | 窗口边界/重叠/乱序 |
| B 批：`internal/endpointclient/client_test.go`、`internal/sponsor/*_test.go`、`internal/license/server_test.go` | 已推送 | 主备 failover/超时/auth；updated_at string 透传真实 DATETIME；5min 缓存命中；防重；竞态修复 |

**每文件检查单**（逐项 ✓/✗，✗ 记问题）：
1. 断言是否**验证语义**而非仅 `err == nil`（如校验错误内容、状态字段、文件真实落盘）。
2. 边界：空/nil/超长/非法字符/并发/时间边界（跨天、月初）。
3. 无 `time.Sleep` 依赖的 flaky 等待（schedtask/pet 防抖类允许受控睡眠，需 ≤ 短时长且留 margin）；无依赖真实网络/真实 home 目录（应 t.TempDir()）。
4. 表驱动、子测试命名清晰；`t.Parallel` 使用正确（共享状态包慎加）。
5. 测试不会在生产用户目录留痕迹（隔离 DataDir/HOME）。
6. 与环境无关：Win7 Go 1.20 可编译（不用 1.21+ 标准库 API）。

## Phase 2 · 专项深审（不看测试也要看实现，测试缺失处直接记缺口）

| # | 专项 | 审查动作 | 通过标准 |
|---|---|---|---|
| S1 | sanitizeAppConfig 回归 | 起实例 `GET /api/config` 抓响应 | 响应 JSON 无 `credential_key`/`kairo_internal_token` 值；存在针对此的单测（若无 → P1 缺口） |
| S2 | SSRF 最终语义（X1） | 读 `ssrf.go` + 重写后的 ssrf 测试 + `handlers_webservice.go` 接线 | SOAP 发送与 WSDL URL 导入**都**走校验；Dial 期复检真实生效（Transport.DialContext 被替换）；放行私网/loopback（本工具自带 /mock/ 在 127.0.0.1） |
| S3 | 命令执行面 | 读 `schedtask/runner.go` + reminder command 动作 | shell 调用方式明确（sh -c / 直接 exec）；超时杀整进程组；stdout/stderr 截断；审计记录完整（谁/何时/何命令/结果） |
| S4 | audit.Subscribe 主流程安全 | 读 `audit.go` diff + pet 回调首行 | 回调在锁外；panic recover；未解锁 gate 为原子读；Write 延迟回归证据（设计文档 §14 要求回调 <1μs，可写 micro-benchmark 或注明人工评估） |
| S5 | pet sync 未配置降级（X5） | 无 pet_leaderboard 配置起实例，`POST /api/pet/sync` | 不请求硬编码地址；返回降级标识；前端文案「仅本地展示」（与 02 方案 S3 场景联动） |
| S6 | wsdl.go +396 重写 | 读 diff + wsdl_test | 重写未丢旧行为（用 git diff 逐 hunk 对照）；新测试覆盖新增分支 |
| S7 | endpointclient / sponsor（B 批） | 跑其测试 + 读 failover 逻辑 | primary 挂→secondary；双挂→错误不泄露内部地址（d5c541c 语义）；缓存 5min；防重提交窗口 |
| S8 | 版本一致性（X3） | grep 六处版本号 | 记录当前不一致状态（v0.14 vs 注释 v0.16），报告标注由人决策 |

## Phase 3 · 缺口矩阵（报告核心产出）

按此模板填表（✓ 有且有效 / △ 有但弱 / ✗ 缺失）：

| 功能 | 单测 | API 级测试 | e2e（02 方案回填） | 风险 | 结论 |
|---|---|---|---|---|---|
| pet 解锁/引擎/落盘 | | | | P0 | |
| pet sync/排行榜 | | | | P1 | |
| schedtask CRUD/调度/执行 | | | | P0 | |
| cronx 解析 | | — | — | P1 | |
| /api/tasks handler 层 | 预期 ✗ | | | P0 | |
| /api/http/curl-parse | | | | P1 | |
| /api/http/ws/* | 预期 ✗ | | | P1 | |
| ssh profilestore | | — | | P1 | |
| webservice SSRF | | | | P0 | |
| reminder 三动作 | | | | P0 | |
| sanitizeAppConfig | 预期 ✗(仅导出有测) | | — | P0 | |
| audit Subscribe | | — | — | P0 | |
| endpointclient/sponsor/license（B 批） | | | | P1 | |
| about 懒渲染（B 批） | — | — | tests/perf-*.js | P2 | |

## Phase 4 · API 冒烟（非 UI，补 handler 层缺口的最小成本方式）

参照 `scripts/acceptance_run.py` 的模式（urllib 直连，无需浏览器），产出 `test-results/review-2026-08/01-api-smoke.log`。对 18092 实例执行：

```
POST /api/pet/enable            → 200，二次调用幂等
GET  /api/pet/state             → enabled:true, level/exp/stage 字段齐全
POST /api/pet/name {"name":"测试<script>宠物名超长长长长长长长长长长"}  → 截断≤16 且去控制字符
POST /api/pet/skin {"skin":99}  → 4xx 范围校验
POST /api/pet/sync（未配置端点） → 优雅降级响应，无对外请求（观察日志）
POST /api/tasks  非法 cron      → 400；合法 cron → 200 得 id
POST /api/tasks/{id}/run        → 异步执行；GET /api/tasks/{id}/runs → 有记录
POST /api/tasks/{id}/toggle ×2  → enabled 翻转；DELETE → 200
POST /api/http/curl-parse {"curl":"curl -X POST http://a.b/c -H 'A: 1' -d 'x=1'"} → 解析字段正确
POST /api/http/curl-parse {"curl":"not a curl"} → 400
POST /api/http/ws/connect {"url":"ws://127.0.0.1:1/x"} → 连接失败错误结构化（不 panic）
GET  /api/config                → 无 credential_key/kairo_internal_token
GET  /api/config/export         → kairo/endpoint auth 脱敏
POST /api/wsdl/import-url {"url":"http://169.254.169.254/"} → 400（X1 语义）
负路径：未解锁全新实例（另起 HOME）→ /api/pet/state 返回 enabled:false、/api/pet/name 404、磁盘无 pet.json
```

## 报告模板与 DoD

报告 `01-test-review-report.md` 结构：
1. 基线结果（build/vet/test/race/覆盖率表，附 log 路径）
2. Phase 1 逐文件评分表 + 问题清单
3. Phase 2 专项结论（S1–S8 各一段）
4. Phase 3 缺口矩阵
5. Phase 4 API 冒烟结果表
6. P0/P1/P2 问题汇总（文件:行、复现、建议修法）

**DoD**：6 节齐全；每个 P0 有复现证据；覆盖率附 `go tool cover -func` 输出；明确回答「这批代码能不能提交，阻塞项是什么」。
