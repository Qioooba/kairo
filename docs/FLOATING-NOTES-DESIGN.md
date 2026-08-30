# 悬浮便笺功能设计（便笺中心 + Kairo 内悬浮 + Windows 10/11 桌面置顶）

> 状态：设计提案，尚未实现。
>
> 核心结论：**不要新增一个孤立的“悬浮便笺”菜单，也不要把便笺字段硬塞进 Reminder。**
> 应把现有「便笺提醒」升级成「便笺中心」：便笺负责随手记录和持续可见，提醒负责未来触发；
> 二者是独立数据模型，通过“从便笺创建提醒”弱关联。悬浮只是便笺的一种展示方式。

## 0. 平台与工程决策

本功能的原生桌面部分明确以 **Windows 10/11 主线**为目标，不为 Win7 增加兼容分支、API fallback 或降级 UI。

理由不是便笺本身无法在 Win7 实现，而是当前主线已经升级到 `go 1.24`，`go.mod` 也明确注明 Win7 留在 legacy 分支维护。
为了一个新功能重新引入 Go 1.20、旧 Win32 API 和双套构建约束，会让主线架构继续被历史平台牵制。

工程原则：

1. 原生桌面便笺支持 Windows 10/11；浏览器内悬浮仍可在其他开发平台工作；
2. 不向 Win7 legacy 分支回移本功能，也不在主线保留 `if win7` 路径；
3. 不复制现有桌面宠物的 Win32 封装来“快速上线”，而是抽取正式的桌面 UI 基础层；
4. 不继续在 `reminders.js` 内内联 modal、状态和新便笺逻辑，而是拆分页面、领域控制器和全局服务；
5. 不用多个 route alias、DOM 特判或全局变量粘合新旧页面；路由兼容在统一 router 中声明；
6. 重构必须保持日志、SSH、下载等核心功能可独立工作，便笺初始化失败不能拖垮整个工具箱。

目标系统建议定为 Windows 10 22H2 / Windows 11。原生窗口可以直接采用 Per-Monitor DPI、RichEdit、现代多显示器和
UTF-16/IME 路径，不为已经退出主线的平台保留过渡实现。

---

## 1. 为什么这个功能适合 Kairo

Kairo 的核心使用场景不是长文写作，而是排障期间在多个工具之间来回切换：

- 从日志里临时记一段异常、时间点、主机名、文件路径；
- 从 HTTP / WebService 响应里摘一个 request id、订单号或待验证参数；
- 在 SSH、文件下载、代码比对之间保留“下一步要做什么”；
- 临时记住一段命令，但还不值得沉淀进「常用命令」；
- 当前就要看见，不一定需要未来某个时间弹通知。

现有模块各自解决了相邻问题，但没有覆盖“快速记录 + 跨页面持续可见”：

| 现有能力 | 解决的问题 | 为什么不能直接替代便笺 |
|---|---|---|
| 便笺提醒 `/reminders` | 到点触发动作 | 创建时必须先选择时间；内容上限 200 字；不适合随手写和持续编辑 |
| 定时任务 `/tasks` | cron 执行本地命令 | 是自动化执行器，不是信息记录工具 |
| 常用命令 `/commands` | 稳定知识速查 | 内容是长期、结构化、可复用的，不适合一次排障的临时上下文 |
| Toast / 右上角通知栈 | 短暂反馈 | 会消失、不可编辑、不是用户数据 |
| 桌面宠物 | 常驻桌面交互 | 证明了 Win32 悬浮窗口可行，但其数据与交互模型完全不同 |

因此，便笺是现有工具链之间的“短期工作记忆层”，和 Kairo 的运维工作流是互补关系。

---

## 2. 产品边界

### 2.1 便笺是什么

- 纯文本快速记录；保留换行和等宽内容；
- 在 Kairo 页面内可跨路由悬浮；
- Windows 10/11 上可选择“置顶到桌面”，浏览器最小化后仍可见；
- 可置顶、换颜色、归档、搜索；
- 可从一条便笺创建提醒，提醒内容取当时快照；
- 数据只落本机，不同步到外部服务。

### 2.2 首版不做什么

- 不做富文本、Markdown 预览、图片、附件；
- 不做多人协作、云同步、账号体系；
- 不自动读取剪贴板，不自动抓取密码框或终端输入；
- 不把便笺当成任务看板，不引入完成率、子任务、负责人；
- 不让提醒和便笺共用同一条记录，避免调度字段与窗口字段互相污染；
- 不直接执行便笺中的命令，避免把记录工具变成第二个任务执行器。

首版坚持纯文本，既适合日志、命令、JSON 片段，也能把 XSS、格式兼容和编辑器体积控制在最低水平。

---

## 3. 信息架构：融入现有「便笺提醒」

### 3.1 菜单与路由

推荐将侧栏「便笺提醒」改名为「便笺」，首页卡片也改为「便笺」。进入后是一个“便笺中心”：

```text
便笺中心
┌───────────────┬───────────────┐
│ 便笺          │ 提醒          │
└───────────────┴───────────────┘
```

- 主入口使用 `#/notes`，子状态使用 `#/notes/list` 与 `#/notes/reminders`；
- 旧 `#/reminders` 在 router 的 alias 表中一次性声明为 `#/notes/reminders`，不保留第二套 renderer；
- 现有提醒 CRUD、暂停今日、Cron、动作类型全部保留，不做功能降级；
- 首页描述改为“随手记录、跨页悬浮，可选定时提醒”。

这比新增第二个「悬浮便笺」菜单更好：用户不会被迫理解“便笺”和“便笺提醒”为什么是两个模块。

### 3.2 全局入口放在顶部栏

在主题切换按钮左侧增加便笺按钮：

```text
┌ 顶部栏 ───────────────────────────────┐
│ 当前页面                    [便笺] [主题] │
└──────────────────────────────────────┘
```

按钮行为：

1. 没有活动便笺：创建一条空便笺并展开；
2. 已有活动便笺：展开 / 收起当前便笺；
3. 有选中文本：按钮菜单提供“把选中内容加入便笺”；
4. 长操作页面中也能使用，不需要先离开当前页面。

不建议放在右下角：当前右下角已经被 Toast、下载通知栈和 Windows 桌面宠物占用，继续叠加会出现遮挡和点击竞争。

### 3.3 悬浮层必须挂在 `#view` 外

`web/app.js` 每次路由切换都会执行 `view.innerHTML = ''`。因此悬浮便笺不能由某个页面 renderer 创建，
也不能放在 `web/pages/reminders.js` 的 DOM 子树中。

推荐在 `web/index.html` 增加与 `#toast` 同级的全局挂载点：

```html
<div id="kairo-note-layer"></div>
```

由新的 `web/features/notes/controller.js` 初始化全局 `NoteController`。页面路由只订阅状态并渲染列表，
悬浮层在整个应用生命周期内存在，不由任何 page renderer 拥有。

当前 router 只有 `route -> render(view)`，没有标准卸载钩子，`navigate()` 还直接清空 `#view`。实现本功能时应正式重构为：

```js
router.register('notes', {
  mount(view, routeState) {},
  unmount() {}
});

router.alias('reminders', { route: 'notes', state: { tab: 'reminders' } });
```

所有页面计时器、SSE、WebSocket 和事件监听都应由 `unmount()` 回收。便笺全局控制器注册为 application service，
不参与页面 mount/unmount。这样也能顺手解决当前部分页面切走后 interval 仍工作的历史问题。

---

## 4. 交互设计

### 4.1 Kairo 内悬浮便笺

浏览器内首版只同时展开一张便笺，避免在日志、SSH 等高密度页面上形成窗口海洋。可以从标题下拉切换其他便笺。

```text
┌─ 生产问题排查 ───────────── [—] [×] ┐
│ 10.24.8.16                            │
│ requestId: 9f2a...                    │
│ 下一步：对比两台机器的 app.properties │
│                                      │
├──────────────────────────────────────┤
│ 已保存  [设提醒] [置顶到桌面] [更多]   │
└──────────────────────────────────────┘
```

行为约定：

- 拖动标题栏移动；右下角缩放；位置和尺寸按浏览器本地偏好记忆；
- 正文自动保存，输入停止 500ms 后提交；失焦时立即补一次保存；
- 保存中显示“保存中…”，成功显示“已保存”，失败保留本地草稿并显示重试；
- `Esc` 收起，标题栏双击折叠正文；关闭只取消悬浮，不删除便笺；
- 删除只能在“更多”或便笺中心完成，必须二次确认；
- 颜色只作为轻量识别，不改变正文语义；高对比主题仍需保证可读性；
- 正文按纯文本展示，保留换行，长行横向滚动或软换行由用户偏好决定。

浏览器几何信息属于浏览器实例，推荐放在：

```text
localStorage['kairo:notes:layout']
```

内容、颜色、置顶、归档等用户数据必须走后端存储，不能只放 localStorage。

### 4.2 便笺中心

“便笺”页签提供：

- 搜索标题和正文；
- 筛选：全部 / 悬浮中 / 已置顶 / 已归档；
- 卡片或紧凑列表两种视图首版只选一种，推荐紧凑列表；
- 新增、编辑、复制、悬浮、置顶到桌面、设提醒、归档、删除；
- 默认排序：置顶优先，其余按 `updated_at` 倒序；
- 空状态直接提供“新建第一条便笺”。

“提醒”页签复用现有提醒列表与编辑器。当前按 once / weekly / monthly / cron 的筛选可以作为二级筛选保留。

### 4.3 从便笺创建提醒

点击“设提醒”打开现有提醒编辑器：

- `content` 预填便笺标题或正文前 200 字；
- 用户仍需选择 once / weekly / monthly / cron；
- 保存后 Reminder 增加可选 `source_note_id`；
- 提醒保存的是内容快照，之后修改便笺不自动改提醒；
- 删除便笺不级联删除提醒；提醒列表显示“来自便笺”并可跳回仍存在的便笺。

这是弱关联，能够避免两个编辑入口互相覆盖用户内容。

### 4.4 从现有工具捕获内容

第一阶段只做显式捕获：

- 顶部便笺按钮读取当前页面选中的文本；
- 在日志上下文、HTTP 响应、WebService 响应、代码比对结果旁逐步补“加入便笺”；
- 所有模块统一调用 `Kairo.notes.capture({ text, source })`，不要各自实现存储逻辑；
- `source` 只记录模块类型和可安全展示的标题，不记录 token、密码或完整请求头。

推荐快捷键 `Alt+Shift+N`：无选中文本时打开便笺，有选中文本时弹出“追加到当前 / 新建便笺”。

### 4.5 Windows 10/11 桌面置顶

“置顶到桌面”是真正的系统级悬浮，不应伪装成浏览器 popup：浏览器无法可靠保证 always-on-top，
也无法在浏览器最小化后继续显示。

Windows 10/11 形态：

- 独立 Win32 工具窗口，`WS_EX_TOOLWINDOW | WS_EX_TOPMOST`；
- 可激活、可输入，因此编辑态不能使用桌面宠物的 `WS_EX_NOACTIVATE`；
- 正文控件使用系统 `RichEdit50W`，保留纯文本语义，但直接获得中文 IME、撤销、选择、滚动和大文本编辑能力；
- 进程启动时统一设置 Per-Monitor DPI awareness，窗口按各自显示器 DPI 计算边框和最小尺寸；
- 标题栏拖动，边缘缩放；关闭表示隐藏，删除仍需在菜单中确认；
- 位置、尺寸、折叠状态、显示器信息持久化；显示器拔出后自动 clamp 回主工作区；
- 首版最多同时显示 6 张桌面便笺，超出时提示先收起一张；
- 托盘增加“新建便笺”和“显示 / 隐藏全部便笺”。

macOS / Linux 开发环境继续支持 Kairo 内悬浮，但“置顶到桌面”按钮置灰并明确提示“桌面置顶仅支持 Windows 10/11”。
Win7 不显示降级按钮，也不提供浏览器小窗口冒充桌面置顶的 fallback。

---

## 5. 数据模型：Note 与 Reminder 分离

### 5.1 Note

建议新增 `internal/note` 包和 `data/notes.json`：

```go
type Note struct {
    ID        string    `json:"id"`
    Title     string    `json:"title"`       // <= 80 rune
    Body      string    `json:"body"`        // <= 20,000 rune
    Color     string    `json:"color"`       // yellow|blue|green|pink|gray
    Pinned    bool      `json:"pinned"`       // 列表置顶
    Floating  bool      `json:"floating"`     // Kairo 内当前悬浮候选
    Archived  bool      `json:"archived"`
    Revision  uint64    `json:"revision"`     // 乐观并发控制
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`

    Desktop *DesktopLayout `json:"desktop,omitempty"`
}

type DesktopLayout struct {
    Visible     bool    `json:"visible"`
    XRatio      float64 `json:"x_ratio"`      // 相对工作区，0..1
    YRatio      float64 `json:"y_ratio"`
    Width       int     `json:"width"`
    Height      int     `json:"height"`
    Collapsed   bool    `json:"collapsed"`
    MonitorHint string  `json:"monitor_hint,omitempty"`
}
```

约束：

- 最多 200 条未删除便笺；归档仍计入，防止数据文件无限增长；
- 标题为空时由正文第一行生成展示标题，但不强行回写；
- 正文和标题 trim 规则要保守：正文保留用户前后换行，只有“全空白”才拒绝；
- 更新时客户端携带 `base_revision`，版本不一致返回 409 和服务器当前值；
- 审计只写 `id / body_len / result`，绝不写正文。

### 5.2 Reminder 的最小扩展

现有 `reminder.Reminder` 只增加：

```go
SourceNoteID string `json:"source_note_id,omitempty"`
```

它不改变调度、触发和 Action 语义。`source_note_id` 不做强外键，不阻止删除便笺。

### 5.3 存储策略

沿用 `internal/reminder/store.go` 已验证的模式：

- `{ "version": 1, "notes": [...] }`；
- `CreateTemp -> Write -> Close -> Chmod(0600) -> Rename` 原子替换；
- JSON 损坏先备份 `.bak`，再返回可诊断错误；
- 保存失败必须回滚内存状态，不能出现 UI 显示成功但磁盘未落盘；
- 不使用 localStorage 作为正文权威存储；
- 不写到 `preferences.json`，便笺是业务数据，不是偏好。

---

## 6. API 与同步

推荐 API：

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/notes` | 列表，支持 `q / archived / floating` |
| POST | `/api/notes` | 新建 |
| GET | `/api/notes/{id}` | 读取单条 |
| PATCH | `/api/notes/{id}` | 局部更新，携带 `base_revision` |
| DELETE | `/api/notes/{id}` | 删除 |
| POST | `/api/notes/{id}/archive` | 归档 / 恢复 |
| POST | `/api/notes/{id}/float` | Kairo 内悬浮 / 收起 |
| POST | `/api/notes/{id}/desktop` | Windows 桌面显示状态与布局 |
| GET | `/api/notes/events` | SSE：created / updated / deleted / visibility |

这里适合用 PATCH：便笺会高频自动保存正文和布局，PUT 全量替换容易把另一端刚更新的字段覆盖掉。

同步路径：

```text
浏览器编辑 ── PATCH /api/notes/{id} ──► note.Manager ──► notes.json
                                             │
                                             ├─► SSE /api/notes/events ──► 其他浏览器页
                                             └─► 内存订阅 ──► Windows desknote

Windows 编辑 ──► note.Manager.Update ─────────┘
```

原生桌面便笺和后端在同一进程，不需要每秒轮询本机 HTTP。`note.Manager` 提供订阅接口，
桌面窗口直接订阅；HTTP SSE 只是把同一事件流转给浏览器。

SSE 断线后浏览器重新 GET 列表校准，不依赖无限事件回放。全局控制器在 `#view` 外，路由切换时不关闭 SSE；
窗口卸载时统一关闭。

---

## 7. 后端与原生模块放置

这部分不采用“新增一个 desknote 包，复制一份 deskpet Win32 声明”的接法。应先建立共享桌面宿主，
再让宠物和便笺分别成为宿主上的 feature。

推荐目录：

```text
internal/note/
├── note.go             # 模型、校验、局部更新契约
├── manager.go          # 线程安全 CRUD、revision、订阅
├── store.go            # notes.json 原子持久化
├── note_test.go
└── store_test.go

internal/winui/
├── host_windows.go     # 单一 UI 线程、消息循环、Post/Invoke
├── window_windows.go   # class 注册、窗口创建、生命周期
├── controls_windows.go # RichEdit、按钮、菜单、字体
├── dpi_windows.go      # Per-Monitor DPI 与尺寸换算
├── monitor_windows.go  # 显示器识别、工作区、窗口回收
├── theme_windows.go    # 5 套主题到原生色板的映射
└── host_other.go       # 非 Windows 明确返回 unsupported

internal/desknote/
├── controller.go       # note.Manager 订阅、窗口注册表
├── window_windows.go   # 便笺窗口行为，不直接声明 user32/gdi32
├── layout.go           # 位置、尺寸、折叠与多屏恢复
└── desknote_other.go   # 返回 ErrUnsupportedPlatform

internal/httpserver/
├── handlers_notes.go
└── handlers_notes_test.go

web/
├── app/
│   ├── router.js       # mount/unmount、route state、alias
│   ├── overlays.js     # modal/toast/notify/note 的统一层级
│   └── services.js     # application-scope service 生命周期
├── features/
│   ├── notes/
│   │   ├── controller.js
│   │   ├── floating-note.js
│   │   ├── note-list.js
│   │   └── note-editor.js
│   └── reminders/
│       ├── reminder-list.js
│       └── reminder-editor.js
└── pages/
    └── notes.js        # 只组合“便笺 / 提醒”，不承载领域实现
```

### 7.1 Win32 基础层重构

当前 `internal/deskpet` 自己维护窗口类、消息循环、DPI 初始化、GDI 资源和 user32/gdi32 声明。
如果 desknote 再复制一套，会立刻产生两个 UI 线程模型、两套 DPI 状态和两套退出顺序。

本次应把通用能力抽进 `internal/winui`：

- 一个锁定 OS thread 的 Host 和消息泵；
- `Post` / `Invoke`，确保窗口操作只发生在 UI 线程；
- 窗口类注册、句柄生命周期、退出 drain；
- DPI、显示器工作区、字体、主题色、定时器、公共控件；
- 进程级 DPI awareness 只初始化一次。

然后改造 `deskpet.Run` 接受 `*winui.Host`，把它现有的 pet / panel / bubble 注册到同一宿主；
`desknote.Controller` 也使用这个 Host，但保留自己的窗口状态机。共享的是基础设施，不共享业务状态。

这样是一次有边界的重构：不重写宠物动画和渲染，只移走通用 Win32 与线程调度；便笺不再成为第二套桌面框架。

### 7.2 应用装配重构

当前 `httpserver.Server` 通过 `SetReminders / SetTasks / SetPet` 逐个晚注入依赖。新增 notes 时不应继续添加 `SetNotes`。
推荐把构造函数改成显式依赖对象：

```go
type Dependencies struct {
    Reminders *reminder.Manager
    Notes     *note.Manager
    Tasks     *schedtask.Manager
    Pet       *pet.Engine
}

func New(cfg *config.Manager, audit *audit.Logger, web fs.FS,
    tails *tailmgr.Manager, shells *sshshell.Manager, deps Dependencies) *Server
```

`main.go` 先构造全部 manager 和 `winui.Host`，再一次性装配 HTTP server、桌面 feature 和 tray。
退出顺序也统一为：停止接收写请求 → flush note/reminder/pet → 关闭桌面窗口 → 停止 UI Host → 关闭 HTTP。

### 7.3 前端组件重构

现有 `web/pages/reminders.js` 同时包含 modal 实现、API、列表、筛选、编辑器和字段校验，不能在文件末尾继续追加便笺。

本次拆分后：

- `reminder-editor.js` 只负责提醒表单，既能被提醒页调用，也能被“从便笺设提醒”调用；
- `NoteController` 是唯一 API/SSE/草稿状态入口；浮窗和列表订阅同一 controller；
- `pages/notes.js` 只负责 tabs 和页面布局；
- modal、focus trap、Escape、层级由 `app/overlays.js` 统一提供，删除 reminders 内联 modal；
- 页面不直接改 `window.Kairo.state.routes`，统一通过 router 注册。

不要在迁移期间长期维护两套页面。拆分完成后删除原 `web/pages/reminders.js`，由新 reminders feature 接管全部现有功能和测试。

---

## 8. 前端层级与冲突处理

当前项目层级并不统一：配置底栏约 50、Toast 60、通知栈 9999、部分弹层 10000、登录和提醒 modal 99999。
这次不只给便笺挑一个“能显示”的 z-index，而是把层级收口为 CSS token 和 OverlayManager：

```css
:root {
  --z-sticky: 100;
  --z-floating: 800;
  --z-notice: 1200;
  --z-modal: 10000;
  --z-gate: 99999;
}
```

| 层 | 建议 z-index | 示例 |
|---|---:|---|
| 页面内容 | 0–99 | 卡片、配置 sticky footer |
| 悬浮便笺 | 800 | 日常跨页显示 |
| Toast / 通知 | 1000–1999 | 短暂反馈，应压在便笺之上 |
| 普通 Modal | 10000 | 编辑器、确认框 |
| Auth / License | 99999 | 必须阻断所有交互 |

现有 `.modal-overlay`、`.kairo-dialog-overlay`、预览弹层和新便笺编辑器逐步改用同一 OverlayManager。
OverlayManager 负责 focus trap、Escape、body scroll lock、叠层计数和恢复焦点，不在各 feature 内复制一份。

便笺默认贴右侧中部，并避让：

- 顶部栏；
- 右上角通知栈；
- 配置页底部保存栏；
- 小屏时宽度降到 `min(360px, calc(100vw - 24px))`；
- 宽度小于 900px 时默认收起，不主动遮住主要表单。

主题上复用现有 5 套 CSS 变量，不硬编码“黄色便签 = 黑字”。颜色应表现为边框 / 标题栏色带，正文背景仍由主题控制，
这样 dark、hc、xianxia 都能保持对比度。

---

## 9. 安全、隐私与可靠性

便笺很容易被用户写入主机名、内部路径、SQL、token，必须比普通 UI 偏好更谨慎：

- 数据文件权限 `0600`；不上传、不遥测；
- 审计日志不记录正文、标题、选中文本，只记录长度和 ID；
- “加入便笺”只处理用户明确选中的文本，不扫描页面表单；
- password 输入、凭据弹窗和 SSH 键盘输入不提供捕获按钮；
- 纯文本渲染使用 `textContent`，禁止把正文送入 `unsafeHtml`；
- 自动保存失败时保留内存草稿，并在关闭 / 刷新前提示；
- 409 冲突不静默覆盖，提供“保留我的版本 / 使用最新版本 / 复制两份”；
- 桌面置顶窗口退出时先 flush 未保存编辑，再关闭 HTTP 服务；
- note.Manager 初始化失败不应阻断日志、SSH 等核心功能，只让便笺入口显示明确错误。

首版不建议给 notes.json 再做自定义加密：Kairo 已有文件权限和本机访问边界，自研加密会引入密钥备份问题。
如果以后明确要存敏感信息，应复用 credentials 的系统钥匙串能力，而不是在便笺层自造密钥方案。

---

## 10. 实现顺序与交付边界

下面是同一个完整版本内部的施工顺序，不是四套长期并存的临时方案。Windows 10/11 桌面置顶属于首个正式版本，
不能只上线一个浏览器浮层后就把功能标记为完成。

### 阶段 A：先完成基础重构

- 建立 `internal/winui.Host`，迁移 deskpet 的消息泵、DPI、显示器与公共 Win32 封装；
- 将 `httpserver.New` 改为显式 Dependencies，删除本轮涉及的晚注入 setter；
- 建立 router 的 mount/unmount/alias 契约；
- 建立 OverlayManager 和 z-index token；
- 拆分 `reminders.js`，保证拆分前后提醒功能和测试等价。

这一步不增加用户功能，但它是后续实现的前置条件，不允许用 TODO 或复制代码绕过。

### 阶段 B：便笺领域与 Kairo 内悬浮

- 新增 `internal/note`、notes API、revision、订阅、原子存储和单元测试；
- 新增 `NoteController`、顶部入口与 Kairo 内全局悬浮编辑器；
- 新增便笺中心，现有提醒成为第二页签；
- 从便笺创建提醒，旧 `/reminders` 只通过 router alias 兼容；
- 选中文本显式加入便笺；
- 5 套主题、键盘、窄屏和自动保存失败态验证。

### 阶段 C：Windows 10/11 真正桌面悬浮

- 新增 `internal/desknote`，使用共享 `winui.Host` 和 RichEdit50W；
- 托盘“新建便笺 / 显示全部便笺”；
- 多窗口上限、拖动、缩放、Per-Monitor DPI、多显示器恢复、中文 IME；
- note.Manager 内存事件同步和退出前 flush；
- Windows 10 22H2 / Windows 11 真机测试；
- 不实现 Win7 fallback，不回移 legacy 分支。

阶段 A-C 合在一个正式功能版本中验收。任一阶段未完成，都不应把“悬浮便笺”标为已完成。

### 阶段 D：场景融合

- 日志上下文、HTTP 响应、WebService、代码比对增加“加入便笺”；
- 便笺来源跳转（只存安全定位信息）；
- 提醒触发后可选择“显示对应桌面便笺”；
- 导入 / 导出纯文本或 JSON；
- 根据真实使用反馈决定是否支持多张 Kairo 内悬浮便笺。

---

## 11. 验收标准

### 11.1 便笺中心与 Kairo 内悬浮验收

1. 在任意页面点击顶部便笺按钮，500ms 内可开始输入；
2. 从日志页切到 SSH、HTTP、配置页，便笺内容和展开状态不丢；
3. 刷新浏览器、重启 Kairo 后正文恢复；
4. 断开 API 或制造磁盘写失败时，不显示假“已保存”；
5. 两个浏览器页同时编辑同一便笺时，旧 revision 收到 409，不静默覆盖；
6. 从便笺创建提醒后，修改或删除便笺不影响提醒按原快照触发；
7. 搜索能匹配中文、路径、命令和多行正文；
8. 5 套主题下正文、焦点、按钮和颜色标识可读；
9. auth/license 弹层出现时，便笺不能盖住或绕过阻断层；
10. audit.log 中不出现便笺正文。

### 11.2 Windows 10/11 原生验收

1. 浏览器最小化后桌面便笺仍保持置顶；
2. 中文 IME、复制粘贴、撤销、滚动可用；
3. 拖动 / 缩放 / 折叠后重启位置恢复；
4. 从双屏切回单屏后窗口不会留在不可见区域；
5. 浏览器和桌面同时编辑时走 revision 冲突，不丢数据；
6. 托盘隐藏便笺不删除内容，退出 Kairo 前完成最后一次保存；
7. 桌面宠物、系统提醒和桌面便笺可同时工作，退出无残留窗口。
8. 进程只初始化一次 DPI awareness，deskpet 与 desknote 共用同一 UI Host；
9. 代码中不存在第二套原生消息循环、独立 UI 线程或 Win7 条件分支；各功能可保留贴合自身绘制模型的薄 Win32 调用层；
10. Windows 10 22H2 与 Windows 11 分别完成一次中文输入、多屏和休眠唤醒冒烟。

---

## 12. 不推荐的方案

### 方案 A：直接给 Reminder 加 `x/y/color/floating`

不推荐。Reminder 的不变量是“能计算下一次触发时间”，Note 的不变量是“能持续编辑和展示”。
强行共表会导致无时间的 reminder、无意义的 enabled、触发后自动停用等语义冲突。

### 方案 B：只用 localStorage 做一张便笺

只能算临时草稿。换浏览器、清缓存、隐身模式都会丢，无法和原生桌面窗口共享，也不符合项目已有本地数据落盘模式。

### 方案 C：只做一个新的独立菜单页

解决了记录，却没有解决“跨页面持续可见”。悬浮入口必须是全局能力，管理页只是归档和整理入口。

### 方案 D：用 `window.open` 冒充桌面便笺

浏览器不能可靠置顶，窗口会被拦截、带浏览器边框，并在浏览器退出时消失。可以做调试工具，不能作为桌面悬浮承诺。

### 方案 E：把便笺塞进桌面宠物面板

入口隐蔽且依赖彩蛋解锁；宠物是可选娱乐功能，便笺是生产工作流。两者可以同时存在，但不能互相作为前置条件。

### 方案 F：复制 deskpet 的 Win32 封装后各跑各的

不推荐。短期看少改文件，长期会形成两个消息泵、两次 DPI 初始化、两套窗口回收和重复的 user32/gdi32 声明。
本功能明确先抽取 `winui.Host`，再迁移宠物和接入便笺；不会以“后续再重构”为由保留重复基础设施。

---

## 13. 最终推荐

产品上把现有「便笺提醒」升级为「便笺中心」，技术上新增独立 `note` 领域：

```text
便笺内容（独立数据）
├─ Kairo 内悬浮：跨路由快速记录
├─ Windows 10/11 桌面置顶：浏览器最小化后仍可见
└─ 创建提醒：把当前内容快照交给现有 reminder.Manager
```

本功能以一次正式重构交付：共享 `winui.Host`、结构化 router、统一 OverlayManager、拆分 reminders feature、独立 note domain。
它利用项目已有的提醒、托盘、Win32 悬浮和本地原子存储经验，但不沿用当前偶然形成的重复实现和全局拼接方式，
也避免把记录、提醒、任务执行三个概念混成一个难以维护的模型。
