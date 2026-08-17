# 方案 02 · 真实页面点击测试方案（Playwright E2E）

> 执行者：agent-2。**允许**新增 e2e 测试文件（tests/e2e/tests/30~35），不改业务代码。
> 产出：`test-results/review-2026-08/02-e2e-report.md` + 截图/网络/console 日志 + 新增 e2e 脚本。
> 范围：批次 A+B 涉及的全部页面交互。共享上下文见 [README](./README.md)。

## 1. 目标与原则

- 真实 Chromium 点击，验证 v0.14 已推送功能**无回归**、v0.16 新功能**交互闭环**。
- 每条场景三段式：**前置 → 操作步骤（真实 click/type）→ 断言（DOM + 网络 + console）**。
- 全局断言贯穿所有场景（任一失败即该场景 FAIL）：
  - `console` 无 error（helpers.setupConsoleCapture 已收集）；
  - 无非预期 5xx / 无对**外部真实地址**的请求（network 日志逐条过，sponsor/pet 端点只允许指向 mock）；
  - 关键操作后 UI 有可见反馈（toast/列表刷新/状态文本）。

## 2. 环境准备

```bash
# 隔离实例（README §6）：HOME=/tmp/kairo-review-2026-08，端口 18092
# mock 依赖：
python3 scripts/mock_sshd.py &                       # 2225，SSH 场景用
go run ./cmd/mock-sponsor-server &                   # sponsor 榜数据源（端口见其 main.go，配进测试实例 config.yaml）
# WS 场景：起一个本地 ws echo（node 一行 ws 库，或 python websockets），地址配到场景 S6
# 跑法：
cd tests/e2e && BASE_URL=http://127.0.0.1:18092 node index.js --grep 定时任务
```

- 实施第 0 步：**先读源码确认选择器**——`web/pages/tasks.js`、`pet.js`、`sponsor.js`、`http.js`、`ssh.js`、`reminders.js` 的 class/id/文案以下表「建议定位」为准但**以源码为准**，源码变更时同步修正。已知可用：`.tool-card`（aria-label）、`.nav-item[data-route]`、`.modal-overlay/.modal-card`、toast 组件（Kairo.core.toast）。
- 测试实例 config：license 走测试 token 或接受激活弹窗由 `helpers.clearTransientUI` 清理；`pet_leaderboard` 端点**故意不配置**（验证 X5 降级），再单跑一轮配置 mock 的（验证正常链路）。

## 3. 新增 e2e 文件（沿用 tests/e2e 框架：TestRunner + helpers + page-data）

| 文件 | 场景 |
|---|---|
| `tests/e2e/tests/30-tasks.js` | S2 定时任务全流程 |
| `tests/e2e/tests/31-pet.js` | S3 宠物彩蛋全流程 |
| `tests/e2e/tests/32-http-curl-ws.js` | S6 curl 解析 + WebSocket |
| `tests/e2e/tests/33-ssh-profiles.js` | S5 SSH profile |
| `tests/e2e/tests/34-reminders-actions.js` | S4 提醒三动作 |
| `tests/e2e/tests/35-sponsor-pet-board.js` | S7 sponsor + 宠物榜 tab |
| 回归复跑 | `01-menu-navigation`、`03-button-scan`、`09-misc-pages`、`12-http-deep`、`04-websphere`（SSRF 语义改动后必跑） |

## 4. 场景详单

### S1 首页与导航（v0.16 新增入口）
| 步 | 操作 | 断言 |
|---|---|---|
| 1 | 打开 `#/` | 「定时任务」卡片存在于工具 grid（icon=timer，tag「新」）；侧栏有 `定时任务` nav-item，icon 渲染为 SVG 非方框 |
| 2 | 点击卡片 | hash 跳转 `#/tasks`，页面标题/空态正确 |
| 3 | 逐一点击侧栏全部 nav-item | 每页无 console error（回归 01-menu-navigation 已有则跳过） |

### S2 定时任务页（A2，P0）
| 步 | 操作 | 断言 |
|---|---|---|
| 1 | `#/tasks` 空态 | 空态文案 + 「新增」按钮可见；无 running 任务时**无 2s 轮询**（network 静默 5s 验证） |
| 2 | 新增：cron 填 `not-a-cron` 提交 | 行内/toast 报错，不创建 |
| 3 | 新增：cron `*/1 * * * *`，命令 `echo hello > /tmp/kairo-e2e-task.out` | 列表出现该行，next_run_at 有值 |
| 4 | 点「立即执行」 | 行状态变 running；页面开始 2s 轮询；完成后 last_status 成功；`GET /api/tasks/{id}/runs` 有记录；磁盘文件落盘证明真执行 |
| 5 | 点「启停」两次 | enabled 图标翻转且刷新后保持 |
| 6 | 编辑改命令 → 保存 | 列表内容更新；enabled 不被编辑重置（后端语义：更新忽略 enabled 字段） |
| 7 | 删除 → 确认弹窗 → 确定 | 列表移除；再次轮询无该 id 的 next_run |
| 8 | 刷新页面 | 持久化正确（sched_tasks.json） |

### S3 宠物彩蛋（A1，P0；分两实例跑）
**实例 A（未解锁，全新 HOME）——零痕迹验证：**
| 步 | 操作 | 断言 |
|---|---|---|
| 1 | 开任意页 | 无浮动宠物 DOM；`#/` 关于卡片正常；sponsor 页**无宠物榜 tab** |
| 2 | `GET /api/pet/state`（fetch） | `{enabled:false}`；data 目录**无 pet.json/pet.json.bak** |
| 3 | 10s 内点「关于」卡片 **5** 次，等 11s，再点 1 次 | 不解锁（滑窗清零），无 toast |

**实例 B（解锁流程）：**
| 步 | 操作 | 断言 |
|---|---|---|
| 1 | 10s 内点「关于」6 次 | toast「已开启宠物功能…」；右下角出现浮动宠物（浮现动画），带等级徽章 Lv1 |
| 2 | 每次点击关于页照常打开 | 彩蛋不干扰正常跳转 |
| 3 | 拖动宠物到屏幕左上 → 刷新 | 位置持久化（pos 百分比恢复） |
| 4 | 窗口 resize 缩小 | 宠物 clamp 回可视区 |
| 5 | 按住快速来回晃动（方向反转≥4 次/1.5s） | 换肤动画 + 气泡说话；皮肤索引 +1（2s 冷却内二次晃动不触发） |
| 6 | 单击宠物 | 迷你面板：等级/经验进度/皮肤数/改名入口 |
| 7 | 改名 `小K2号` → 保存 | 宠物复述新名气泡；刷新后名字保持 |
| 8 | 去 `#/sponsor` | 出现 `[武林排行榜][宠物排行榜]` tab；切宠物榜 → 未配置端点时显示「未配置服务器，仅本地展示」+ 本地等级经验（X5）；sessionStorage 记住选择 |
| 9 | 执行若干操作（如跑一次 HTTP 请求）后重开宠物面板 | 经验值增长（audit→exp 链路 e2e 证据） |
| 10 | 删除 data/pet.json 重启实例 | 彩蛋关闭，恢复实例 A 状态 |

**实例 C（config 强制开关）：** config.yaml 加 `pet.enabled: true` → 启动即解锁（开发者开关与用户开关 OR 语义）。

### S4 提醒三动作（A6+B 批 833d397，P0）
| 步 | 操作 | 断言 |
|---|---|---|
| 1 | `#/reminders` 新增，动作=弹窗 | 编辑器只显示 popup 相关字段 |
| 2 | 动作=打开网址，填 `https://example.com` | 保存成功，列表显示动作类型标识 |
| 3 | 动作=执行命令，填 `echo hi`，时间=1 分钟后 | 列表正常；到点后（或立即触发入口若有）审计日志有 `reminder.fire.*` 且命令执行结果落审计 |
| 4 | 编辑已有提醒：**只改内容不改时间/动作** → 保存 | 时间/动作字段不被清空（833d397 回归） |
| 5 | 非法输入（空内容/非法 url） | 校验提示，不崩溃 |

### S5 SSH 配置档案（A4，P1）
| 步 | 操作 | 断言 |
|---|---|---|
| 1 | `#/ssh` 填 mock 连接（127.0.0.1:2225 test/ops）→ 保存为 profile `mock-1` | profile 出现在选择器/列表 |
| 2 | 重开页面选 `mock-1` | 表单自动回填；连接成功进终端 |
| 3 | 编辑/删除 profile | UI 与持久化一致；凭据不明文出现在任何网络响应与页面 DOM |
| 4 | profile 名称冲突/超长 | 校验提示 |

### S6 HTTP 页新功能（A3，P1）
| 步 | 操作 | 断言 |
|---|---|---|
| 1 | `#/http` 粘贴 curl：`curl -X POST http://127.0.0.1:18092/api/config -H 'A: 1' -d 'x=1'` → 解析 | method/url/header/body 正确填充表单 |
| 2 | 粘贴垃圾文本 → 解析 | 结构化报错，表单不被污染 |
| 3 | 「美化」按钮（B 批 715424b）对 body JSON | 格式化正确，按钮 SVG 图标非 emoji |
| 4 | WS 面板：连本地 ws echo → 发 `hello` → poll | 收到回显；消息列表追加；关闭后连接状态复位 |
| 5 | WS 连不可达地址 | 错误提示结构化，页面不死 |
| 6 | 发请求触发 audit 后看宠物（S3 联动可合并） | http.request +1 exp |

### S7 sponsor 页（A1+B 批交织，X2）
| 步 | 操作 | 断言 |
|---|---|---|
| 1 | 正常链路（mock sponsor server 配置好） | 三栏（库迪/瑞幸/随机奶茶）榜单渲染，真实姓名+花名前缀；5min 缓存（二次进页无重复请求） |
| 2 | 断 mock 后刷新 | 异步加载失败 → **随机搞笑提示**，页面/DOM/网络日志均**无内部地址泄露**（d5c541c 语义） |
| 3 | 快速双击投喂/提交按钮 | 防重提交（de0275f）：只发一次请求 |
| 4 | 未解锁宠物时 | 无宠物榜 tab；解锁后出现（与 S3 互证） |
| 5 | updated_at 展示 | Java DATETIME 字符串透传不解析崩溃（b5f88e9） |

### S8 WebService 页（A5+B 批，P0）
| 步 | 操作 | 断言 |
|---|---|---|
| 1 | WSDL URL 导入 `http://169.254.169.254/x` | 400 类错误提示「安全限制」语义（X1 最终态） |
| 2 | 导入本机 mock WSDL（/mock/ 或 httptest 等价物） | 成功（loopback 放行） |
| 3 | SOAP 发送占位符优化（B 批 033c642） | 模板占位符渲染正确 |
| 4 | 回归 `04-websphere` 全套 | 全绿 |

### S9 about 页（B 批 3a8da9d，P2）
| 步 | 操作 | 断言 |
|---|---|---|
| 1 | 打开 `#/about` | 首屏 12 section 懒渲染：初始 DOM 节点少，滚动逐 section 出现 |
| 2 | 滚到底 | 12 个 section 全部最终渲染，无空白 |
| 3 | 版本号 | 页面/footer 一致（当前均 v0.14；X3 记录） |

### S10 全量回归
`node tests/e2e/index.js`（不带 --grep）全跑，统计通过率；失败项逐条归类（环境/真实回归）。

## 5. 报告模板与 DoD

`02-e2e-report.md`：场景结果表（S1–S10 PASS/FAIL + 证据截图路径）→ console/network 异常清单 → 问题清单（P0/P1/P2 + 复现步骤 + 截图）→ 新增 e2e 文件清单与纳入 CI 的建议。

**DoD**：S1–S10 全执行有证据；新增 6 个 e2e 文件可重复运行（--grep 可单跑）；每个 FAIL 场景有截图+网络日志；明确回答「哪些交互闭环坏了」。
