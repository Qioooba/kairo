# Kairo 工作台重构开发方案

版本：v0.1  
日期：2026-09-03  
状态：开发前方案，待评审后进入实现

## 1. 方案结论

本次不按页面逐项打补丁，而是先补齐一组跨页面基础能力，再对数据库、打包、比较、任务和 WSDL 生成页面做分域重构。

优先级如下：

1. **P0 正确性与安全边界**：修复存储过程误生成可执行 SQL、光标语句执行、数据源删除后的幽灵 Tab、断线重连误重试。
2. **P1 高频效率**：数据库分页、数据库工具栏、WAS 一键打包与输出策略、比较工作台的常驻编辑和语法高亮、WSDL 扫描推荐前置、任务失败告警。
3. **P2 能力扩展**：Redis 只读命令工作台、Redis 指标、受控 TTL 操作；邮件告警和更完整的 Redis 写操作另行开启。

核心原则：

- **执行语义前后一致**：前端生成的内容必须符合后端策略；后端拒绝时不能由 UI 主动制造必然失败的操作。
- **资源身份显式绑定**：Tab、任务、查询结果必须知道自己绑定的资源是否仍然有效，禁止静默降级到另一个数据源。
- **读写能力分层**：数据库工作台继续以只读为默认；Redis 的写操作必须通过单独能力、权限、确认和审计开启。
- **可观察、可恢复**：分页、重连、告警和输出目录覆盖都必须能在界面与日志中解释发生了什么。
- **保持当前技术栈**：当前前端是原生 JavaScript，不引入大型框架；共享能力以独立模块和明确的状态机实现。

## 2. 已核对的现状与证据

### 2.1 代码基线

- 数据库前端主要在 [`web/pages/database.js`](../web/pages/database.js)，后端在 `internal/dbconsole` 和 `internal/httpserver/handlers_database.go`。
- 数据库后端当前请求主要只有 `source_id`、`sql`、`max_rows`、`format`、`table`；分页不是现有查询协议的一部分。
- `internal/dbconsole/query.go` 已针对 Oracle 和 MySQL 做行数限制改写，但仍是“最多返回 X 行”语义。
- `internal/dbconsole/policy.go` 的 `ValidateReadOnlySQL` 仅允许只读查询首词，明确拒绝 `CALL`、`EXECUTE` 等执行型关键字，并拒绝单条 SQL 之后仍存在内容的分号。
- 数据库连接使用 `database/sql` 连接池，当前配置了连接最大生命周期和空闲生命周期，但真正执行查询时没有一次性的失效连接恢复流程。
- Redis 当前后端主要提供 `SCAN` 和 Key 详情预览，前端 `renderRedis` 也只有扫描、选择 Key 和查看详情。
- WAS 后端已经存在 `/api/waspack/build`，`Build` 内部可以完成抽取和打包；当前页面仍要求用户先点抽取、再点打包。
- WAS 的 `prepareOutputDir` 目前要求目录不存在或为空，非空时直接拒绝；这一规则没有“仅清理 Kairo 产物”或“明确覆盖”的分级策略。
- WSDL 后端已有 `ScanProject` 和引擎推荐，页面只是把工程扫描卡片放在引擎选择之后。
- 任务后端已有运行历史和失败状态，但 `internal/schedtask` 没有统一通知 sink，页面也没有告警配置入口。
- 比较页面使用原生编辑区和 `diff2html` 样式资源；没有现成的 Java/XML 通用高亮依赖。数据库页面已有 SQL 高亮 overlay，可抽象为共享的编辑器能力。

### 2.2 实际页面基线

在本地 `127.0.0.1:18092` 以约 1366×768 的视口检查了现有页面：

- `#/database` 的编辑器工具栏已出现换行，执行、分页相关输入、收藏夹、历史等操作平铺在同一层级。
- `#/waspack` 只显示“1. 一键抽取”和“2. 打包”，未暴露后端已有的直接 build 能力；页面提示输出目录必须不存在或为空。
- `#/compare` 顶部同时放置 Tab、11 个左右的操作、过滤选项和左右文件头部配置；编辑区虽可见，但上下文控制仍可压缩。
- `#/tasks` 空状态仅有“新增任务”，没有失败告警设置。

### 2.3 Figma 与浏览器工作流状态

- 已创建方案文件：[Kairo 工作台重构方案 v0.18](https://www.figma.com/design/za3IlIq9mqqq2oFOPldbEd)。
- 已接入并检查当前本地页面；当前 Figma Starter 账号在组件搜索和写入阶段触发 MCP 调用限额，文件暂未成功写入页面节点。实现阶段应以本方案的 token、布局和交互约束为基线；限额恢复后再补齐可交互设计稿。
- 当前项目没有发现 Code Connect 文件，不能假定 Figma 组件可以直接映射到现有原生 JS 代码。

## 3. 总体架构调整

### 3.1 前端共享模块

建议新增 `web/workbench/`，不要继续把跨页面状态和工具函数堆入各页面脚本：

```text
web/workbench/
  statement-model.js       # 光标位置、选区、注释/引号感知的 SQL 语句范围
  resource-session.js      # Tab 与数据源的显式身份、孤儿状态、恢复动作
  request-recovery.js      # 可重试错误分类、一次性重试、状态提示
  syntax-editor.js         # textarea + highlight overlay + 行号 + 滚动同步
  command-palette.js       # 次级操作、快捷键和“更多”菜单
  notice-bus.js             # 页面内通知、系统通知、Webhook 状态反馈
```

页面脚本只负责领域状态和 API 适配：

- `database.js`：查询、分页、Redis 面板、数据源对象树。
- `waspack.js`：预检、直接构建、输出目录策略。
- `compare.js`：两个文档模型、实时 diff 和文件操作。
- `tasks.js`：任务列表、运行状态、告警设置抽屉。
- `wscodegen.js`：扫描推荐、人工覆盖和生成。

在 `web/index.html` 中先加载共享模块，再加载页面模块；保持现有原生 JavaScript 结构，避免引入构建链和大体积运行时。

### 3.2 后端共享能力

建议抽出或补充以下服务，而不是在 handler 内各自实现：

- `internal/recovery`：失效连接错误分类、重连/重建、重试边界。
- `internal/notify`：系统通知、Webhook、未来邮件的统一事件模型和发送队列。
- `internal/audit`：Redis 受控写操作、输出目录覆盖、告警配置变更的审计记录。
- `internal/pathguard`：WAS 输出路径合法性、符号链接/危险路径和 Kairo 产物标记检查。

所有新增 API 必须保留旧字段的兼容默认值，旧客户端不发送新字段时行为不变。

## 4. 数据库工作台重构

### 4.1 查询协议改为分页语义

请求在保持 `max_rows` 兼容的同时增加：

```json
{
  "source_id": "prod-oracle",
  "sql": "SELECT ... FROM ... ORDER BY ID",
  "page": 1,
  "page_size": 100,
  "count_mode": "none"
}
```

建议规则：

- `page` 从 1 开始；`page_size` 可选 50/100/200/500/1000，服务端仍受数据源的最大返回行限制约束。
- 后端实际取 `page_size + 1` 行，裁掉多取的一行后返回 `has_next`；默认不执行 `COUNT(*)`，避免大表分页为了显示总数额外扫全表。
- `count_mode=exact` 只作为显式操作，且需要单独提示可能产生较高成本；可以后续作为 P2。
- 摘要增加 `page`、`page_size`、`offset`、`has_next`、`has_prev`、`total_rows`（可空）、`total_known`、`pagination_mode`、`retry_count`、`ordered`。
- 没有 `ORDER BY` 时返回 `ordered=false` 并在结果区提示“分页顺序不稳定，建议补充 ORDER BY”。不替用户擅自注入排序。
- `max_rows` 旧请求继续解释为第一页的 page size，旧 UI 不会因为协议升级失效。

分页改写应在 `internal/dbconsole/query.go` 统一实现，并先验证原始 SQL，再包裹分页层：

- Oracle 11g：采用双层 `ROWNUM` 包装，内层保留原查询和排序，外层过滤下界，避免直接在同层过滤 `ROWNUM > n` 的语义陷阱。
- MySQL：使用派生表加 `LIMIT page_size_plus_one OFFSET offset`，参数必须走驱动绑定，不能拼接用户输入。
- 生成分页别名必须使用内部保留名并处理原查询列名冲突；必要时由查询层生成列投影。
- 过大的 offset 要有限制并给出清晰错误，避免用户意外触发极深分页。

建议新增纯函数测试 `BuildPagedQuery(dialect, sql, page, pageSize)`，覆盖 Oracle 11g、MySQL、已有 `ORDER BY`、注释、尾部分号和非法页码。

### 4.2 按光标执行当前 SQL 语句

`statement-model.js` 负责把编辑区内容解析为语句范围：

1. 扫描单引号、双引号、Oracle q-quote、MySQL 反引号、行注释和块注释。
2. 只有处于普通代码状态的分号才是语句分隔符。
3. 有文本选区时，选区优先；无选区时，选择光标所在语句。
4. 光标在分号上时按前一条语句处理；光标位于语句间空白时选择下一条非空语句；全文只有一条语句时允许尾部分号。
5. 发往后端前裁剪首尾空白和尾部分号，但不改变字符串、注释和 SQL 内容。

后端仍必须拒绝一次请求中的多条 SQL。前端解析是交互改善，不是安全边界替代品。快捷键行为固定为：

- `Ctrl/Cmd+Enter`：执行选区或光标所在语句。
- `Shift+Ctrl/Cmd+Enter`：在确认后执行当前编辑器全部内容，仅在未来引入多语句执行协议后启用；当前版本保持禁用。

### 4.3 存储过程/Package 双击行为修正

只读数据库工作台不能再生成“看起来可以执行、实际上 100% 被拒绝”的 `BEGIN...END` 或 `CALL`：

- 双击过程：打开“源码/定义”视图，查询可读元数据或生成只读查看模板。
- 如必须把调用模板插入编辑器，插入为注释块，并明确标识“仅模板，当前只读策略不可执行”。
- 禁止在 `insertObjectSQL` 中直接把执行型语句当作普通 SQL 送入执行流程。
- 未来若支持过程执行，必须设计单独的 `execution_mode`、权限、二次确认、超时和审计接口，不能绕过 `ValidateReadOnlySQL`。

### 4.4 数据源会话与幽灵 Tab

将 Tab 中的 `sourceId` 升级为资源引用快照：

```js
sourceRef: {
  id: "prod-oracle",
  name: "生产 Oracle",
  kind: "oracle",
  version: 3
}
```

`resource-session.js` 只返回以下状态，不允许静默回退：

- `ready`：引用的数据源仍存在且版本匹配。
- `changed`：数据源仍存在但配置版本改变，要求重新确认或重新连接。
- `removed`：数据源已删除，保留 SQL 和结果快照，但禁用执行、导出重查和元数据操作。

切换到 `removed` Tab 时，标签保留历史名称，但显示“已删除”标记和“重新绑定数据源”按钮。删除数据源后应广播资源变更事件，所有相关 Tab 即时进入孤儿状态。`effectiveSource()` 不再有全局 `state.source` 兜底逻辑。

### 4.5 断线自动恢复

后端在 `internal/dbconsole/manager.go` / 查询执行层增加统一恢复流程：

```text
获取连接池
  ↓
执行 Ping/Query
  ├─ 成功：继续
  └─ 明确的连接失效错误：Invalidate → 重建连接池 → 原请求重试一次
```

规则：

- 只在尚未向客户端输出 meta/row 前重试；一旦流式结果已经输出，禁止重跑，避免重复数据。
- 只重试一次；连续失败直接返回原始错误和“已尝试重连”的状态。
- 错误分类至少覆盖 `sql.ErrBadConn`、MySQL 2006/2013，以及 Oracle 常见断线码（例如 ORA-03113、ORA-03114、ORA-01012、ORA-12537）；分类逻辑需要单元测试。
- 查询、执行计划、元数据读取、连接测试和导出重查共用同一策略。
- 返回 `retry_count` 和可读状态；日志记录 source、请求类型、耗时和结果，不记录密码与完整敏感 SQL。
- 前端仅展示一次“连接已失效，正在重新连接…”；不能对未知网络失败再盲目重复 POST。
- 复核 `SetConnMaxLifetime` 和 `SetConnMaxIdleTime` 的配置关系，避免连接池主动回收与防火墙 idle timeout 叠加造成误报。

### 4.6 Redis 从 Key 查看器升级为只读命令工作台

第一阶段只做安全的结构化只读命令，不提供任意 Redis CLI：

- String：`GET`、`TYPE`、`TTL`、`PTTL`、`STRLEN`。
- Hash：`HGET`、`HGETALL`、`HSCAN`。
- List：`LLEN`、`LRANGE`。
- Set：`SCARD`、`SSCAN`。
- ZSet：`ZCARD`、`ZRANGE ... WITHSCORES`。
- Stream：受限的 `XINFO`、`XRANGE` 预览。
- 运行指标：`INFO memory`、`INFO clients`、`INFO stats`、`INFO keyspace`、`DBSIZE`、`PING`。

建议增加以下 API：

```text
POST /api/database/redis/command
GET  /api/database/redis/info?section=memory,clients,stats,keyspace
GET  /api/database/redis/members?key_base64=...&type=hash&cursor=...&page_size=100
```

命令请求使用结构化字段 `{command, key, args}`，服务端按命令白名单、参数数量、分页大小和响应字节数校验；禁止将用户输入拼接成 shell 命令。`KEYS`、`EVAL`、`CONFIG`、`FLUSH*`、`DEL`、`UNLINK` 等高风险命令明确拒绝。

UI 改为三块：

1. 左侧 Key 浏览与 SCAN 游标。
2. 中间类型详情与分页成员表。
3. 右侧“只读命令/运行指标”抽屉，显示命令、耗时、截断状态和刷新时间。

“刷新缓存”在 UI 中拆成两个含义：普通刷新只是重新读取详情；真正删除或失效缓存属于写操作，不能使用同一个按钮语义。

第二阶段如业务确有需要，再增加独立的 `POST /api/database/redis/mutate`：默认关闭，通过数据源能力 `allow_redis_write`、用户权限、二次确认、TTL 上下文和审计开启。首批只建议 `EXPIRE`、`PEXPIRE`、`PERSIST`，不要把任意 CLI 或 `DEL` 作为默认能力。

### 4.7 数据库工具栏信息架构

将当前一行 15 个平铺控件改成两级操作：

- 主工具栏：执行、停止、页码、每页行数、当前页结果摘要。
- “更多”菜单：执行计划、格式化、导出、收藏、删除收藏、历史、清空历史、网格设置。

结果区域顶部或底部提供分页条，避免把翻页控件与编辑器操作混在一起。1366×768 和窗口未最大化时，主工具栏必须保持单行，低频操作不得挤压执行与分页。

键盘和无障碍要求：执行按钮有明确的 `aria-label`，分页输入支持 Enter 提交，正在查询时按钮显示取消状态；菜单打开后支持 Esc 关闭和 Tab 循环。

## 5. WebSphere 投产打包重构

### 5.1 一键直接打包

页面增加主按钮“预检并直接打包”，直接调用现有 `/api/waspack/build`，完成：

```text
清单解析 → 预检 → 抽取 → 生成 WAR/压缩包/脚本 → 返回产物
```

“一键抽取”和“2. 打包”保留在“高级流程”折叠区，供需要修改 `war` 目录的用户使用。主流程与高级流程的产物状态必须分开，避免一次抽取后的旧目录被误认为当前清单结果。

### 5.2 输出目录策略

在请求中增加 `output_policy`，建议值：

- `fail`：默认策略；检测到非空目录就停止。
- `clean_kairo_artifacts`：只清理带 Kairo manifest/marker 且由当前任务生成的产物。
- `replace`：明确确认后替换，适合用户确定目录全部由 Kairo 管理的场景。

实现要求：

- 先在同级 staging 目录生成完整结果，校验成功后再提交，避免中途失败留下半成品。
- 使用 Kairo 产物 marker 记录版本、清单摘要和生成时间。
- `clean_kairo_artifacts` 不得删除未知文件；发现人工文件时必须阻断并列出冲突。
- `replace` 必须由 UI 显示影响范围并二次确认，后端再次校验确认字段和路径。
- 复核路径不能是工作区根目录、系统目录、符号链接指向的目录或其他危险路径；所有删除/替换动作写入审计。
- 高级“打包已有 war”流程默认不覆盖用户人工修改的目录。

### 5.3 页面状态机

```text
idle → previewing → ready
ready → building → success
ready → extracting → extracted → packaging → success
任意进行中状态 → error / canceled
```

清单、输出目录或输出策略发生变化后，清除旧预检结果并要求重新预检。成功页展示产物清单、输出路径、策略、是否清理过旧产物和可打开文件夹入口。

## 6. 代码与文件比较工作台重构

### 6.1 数据模型与渲染模型分离

两个编辑器内容是唯一 source of truth；Diff 是派生结果，不再点击“比对”后把编辑器整体替换成只读 `diff2html` 画布。

```text
左文档 + 右文档
      ↓ debounce 解析
  diff model / hunks
      ├─ 左右编辑器高亮层
      ├─ 行号与变更标记
      └─ 统一 Diff 导出/复制视图
```

`diff2html` 可以继续用于复制、下载或统一 Diff 预览，但不能作为常驻编辑态的唯一渲染器。

### 6.2 实时语法高亮与就地编辑

抽象现有数据库 SQL overlay 为 `syntax-editor.js`，提供 textarea、代码层、行号层、滚动同步和焦点保持：

- 支持 Java、SQL、XML、JSON、普通文本；可按文件扩展名和手工选择切换。
- 解析器必须对字符串和注释安全，未支持或异常时回退纯文本；高亮层不能产生 HTML 注入。
- 输入后 150～250ms debounce 重新计算 diff；为每次计算携带递增 request id，旧结果不得覆盖新内容。
- 编辑时保持光标、选区和滚动位置；左右面板允许独立编辑、保存和撤销。
- 显示“未保存变更”状态；比对结果实时更新，不要求用户先返回编辑再重新点击比对。

### 6.3 视口与工具栏

- 顶部只保留 Tab、比对、布局、搜索上一处/下一处和交换等高频操作。
- 高级过滤项收纳在“过滤”Popover/Details 中，保留当前勾选状态。
- 左右文件路径 Header 压缩为单行，可将打开/保存放入文件菜单。
- 整体采用 `min-height: 0` 的 flex 布局，让 Diff 编辑区成为主要伸缩区域。
- 顶部控制区目标高度约 120～160px，代码区在标准笔记本窗口下应明显多于当前可视行数。

## 7. 定时任务失败告警

### 7.1 事件模型

任务执行完成且最终状态为 `failed` 或 `timeout` 时产生 `TaskRunFailed` 事件；事件至少包含：

```json
{
  "event": "task.run.failed",
  "task_id": "...",
  "task_name": "...",
  "run_id": "...",
  "status": "failed",
  "error": "...",
  "started_at": "...",
  "finished_at": "..."
}
```

取消和跳过默认不告警；启动恢复时发现上次运行异常中断，可作为可选策略。相同 `run_id` 只能发送一次失败告警。

### 7.2 通道与配置

第一阶段：

- Windows 系统托盘/气泡通知。
- 企业微信 Webhook。
- 钉钉 Webhook。

邮件作为 P2，先不把 SMTP 凭据和投递重试混入第一期核心路径。

新增任务级策略：`inherit`、`on`、`off`；全局设置包含启用通道、失败/超时筛选、静默窗口和测试发送。

建议 API：

```text
GET  /api/tasks/notifications
PUT  /api/tasks/notifications
POST /api/tasks/notifications/test
```

实现要求：

- `internal/notify` 使用有界异步队列，不能阻塞任务执行 goroutine。
- Webhook 失败使用有限次数重试和指数退避；超过上限在应用日志和页面通知中显示。
- URL、签名密钥、SMTP 密码不写普通运行日志；优先使用现有凭据存储能力。
- 页面增加“告警设置”抽屉和最近失败横幅，不能只依赖托盘通知。
- 测试发送必须标记为 test event，不得伪装成真实任务失败。

## 8. WSDL 代码生成流程调整

后端扫描推荐能力已经存在，本次重点是重排信息架构并强化置信度表达：

1. **工程依赖扫描**：选择工程目录，扫描 `WEB-INF/lib`、POM/构建文件。
2. **扫描结果**：列出识别到的 Axis、CXF、JAX-WS、XFire 等依赖、版本和证据。
3. **推荐引擎**：自动填入推荐引擎和置信度；无证据时显示“无法确定”，不伪造高置信推荐。
4. **人工覆盖**：用户可以明确选择其他引擎，并看到兼容性提醒。
5. **生成选项与输出**：只有在引擎决策后展示详细参数。

没有工程目录时可以默认 `Portable`，但必须说明这是无依赖证据下的安全默认值。扫描结果与手工覆盖都应进入生成摘要，便于排障。

## 9. API、兼容性与数据迁移

### 9.1 兼容策略

- 旧数据库请求只带 `max_rows` 时，服务端按第一页处理并保持原上限。
- 旧 Redis `/scan`、`/key` 接口继续保留，新的命令/成员/指标接口独立增加。
- `/api/waspack/extract`、`/api/waspack/package` 行为保持；`/api/waspack/build` 暴露为新主流程。
- 任务通知配置缺失时按关闭处理；旧任务默认继承全局关闭策略。
- Tab 持久化数据没有 `sourceRef` 时，启动时由旧 `sourceId` 补全快照；找不到资源则直接标记 `removed`，不能绑定当前全局数据源。

### 9.2 不做数据库迁移的优先方案

优先扩展现有 JSON/配置结构，新增字段均有默认值。任务通知配置可单独存放在现有数据目录，避免为本次重构引入数据库迁移。后续如果告警历史、审计量增长，再评估持久化到 SQLite。

## 10. 分阶段实施与提交边界

### Phase 0：契约和基础设施

- 确认分页、不精确总数、Redis 只读、WAS 默认拒绝覆盖、告警通道范围等决策。
- 建立 `statement-model`、`resource-session`、`request-recovery`、`notice-bus` 的接口和测试。
- 固化 API 错误格式、请求 ID、审计字段和 feature flag 命名。

### Phase 1：先修正确性硬伤

- 光标语句解析与 `Ctrl/Cmd+Enter`。
- 过程/Package 双击改为只读定义/注释模板。
- 数据源删除后的 Tab 孤儿状态与重新绑定。
- 后端断线一次性重连；前端展示恢复状态。

建议提交边界：`dbconsole` 合同与单测、数据库前端状态模型、数据库对象树和快捷键、连接恢复。

### Phase 2：数据库分页与 Redis 只读工作台

- 分页 SQL wrapper、摘要协议、结果区分页控件。
- 数据库工具栏重排。
- Redis 成员分页、类型面板、只读命令、INFO 指标和刷新状态。

Redis 受控写操作不阻塞只读版本，可通过 feature flag 单独开发和验收。

### Phase 3：WAS、Compare、WSDL

- WAS 直接 build、staging、marker 和输出策略。
- Compare 常驻编辑器、语法高亮、实时 diff、紧凑布局。
- WSDL 扫描卡片前置、推荐结果和人工覆盖。

### Phase 4：任务告警

- 统一通知事件、托盘通道、企业微信/钉钉 Webhook。
- 任务级继承/开启/关闭策略、测试发送、失败横幅。
- 投递失败重试、去重和审计。

### Phase 5：验收与灰度

- 先在本地和测试数据源打开新能力。
- Redis 写操作、WAS `replace`、精确总数和邮件保持关闭，待安全评审后逐项打开。
- 收集查询重连率、分页使用率、告警投递成功率和前端错误率。

## 11. 测试方案

### 11.1 单元测试

- SQL 语句范围：引号、反引号、q-quote、行/块注释、分号、光标在边界、空白和选区优先级。
- 分页 SQL：Oracle 11g ROWNUM 双层包装、MySQL LIMIT/OFFSET、参数绑定、列别名冲突和非法页码。
- 连接恢复：错误分类、最多重试一次、已输出结果后不重试、重连失败原错误保留。
- 资源会话：ready/changed/removed、删除广播、禁止静默 fallback。
- Redis：命令白名单、参数上限、成员分页、响应字节限制和危险命令拒绝。
- WAS：路径保护、marker、未知文件检测、staging 失败回滚和三种 output policy。
- 通知：失败/超时事件、run_id 去重、队列满、Webhook 重试和敏感信息脱敏。

### 11.2 Handler/API 测试

- 数据库分页请求和旧 `max_rows` 请求都能返回合法 NDJSON。
- Redis 命令、成员、INFO 及错误响应包含 request id 和截断信息。
- WAS build 的预检失败不会创建不完整输出；覆盖必须有明确策略。
- 任务通知配置读取、更新、测试发送和权限边界完整。

### 11.3 浏览器验收场景

- 1366×768 下数据库主工具栏不换行；分页可从第 1 页跳到第 11 页查看第 1001～1100 行，并能上一页/下一页。
- 一个编辑区放三条查询，光标分别位于三条语句时，`Ctrl+Enter` 只执行当前语句。
- 双击 Oracle/MySQL Procedure 或 Package 不再生成必然被只读策略拒绝的可执行 SQL。
- 删除当前 Tab 绑定的数据源后，Tab 显示已删除并禁止执行，不会发往全局当前数据源。
- 模拟数据库空闲断线，第一次执行显示重连并至多自动重试一次。
- Redis Hash/List/Set/ZSet 成员可以分页查看，INFO 可刷新，危险命令不可执行。
- WAS 修改清单后可使用直接打包；非空目录按策略给出阻断、清理或确认覆盖结果。
- Compare 在高亮状态下直接修改任一侧文本，Diff 实时更新且光标不跳动。
- WSDL 页面先看到扫描，扫描后自动推荐引擎，用户仍可手工覆盖。
- 任务失败时页面出现失败提醒，托盘通知和 Webhook 测试通道可收到结构化事件。

### 11.4 集成环境

- Oracle 11g：重点验证 ROWNUM 分页、无 ORDER BY 警告和长查询超时。
- MySQL：验证派生表分页、连接池 idle timeout 和断线恢复。
- Redis：至少验证 standalone；如部署条件允许补充 cluster/sentinel。
- Windows：验证托盘通知在应用最小化、焦点不在 Kairo 时仍可见。

## 12. 验收标准

本次重构完成的最低标准：

- 三个逻辑硬伤有自动化测试，并且 UI、API、后端策略的执行语义一致。
- 数据库分页不依赖用户手写 Oracle/MySQL 分页 SQL，默认不会为总数执行昂贵全表计数。
- Redis 只读能力覆盖 String/Hash/List/Set/ZSet 和基础 INFO，所有命令有白名单和响应上限。
- WAS 日常用户一个按钮完成预检、抽取和打包；输出目录策略可解释、可审计，不误删未知文件。
- Compare 能边看边改，Java/SQL/XML 至少有高亮，代码区获得主要垂直空间。
- WSDL 扫描推荐先于引擎选择呈现。
- 任务失败可在页面第一时间感知，并至少支持 Windows 通知和企业微信/钉钉 Webhook 之一的稳定投递。
- 旧接口和旧持久化数据仍可工作；新能力可以通过 feature flag 灰度关闭。

## 13. 开发前需要确认的决策

以下决策建议按推荐项进入开发：

| 决策项 | 推荐方案 | 原因 |
|---|---|---|
| 分页总数 | 默认不查精确总数，只返回 `has_next` | 避免大表分页被 `COUNT(*)` 拖慢 |
| Redis CLI | 结构化只读命令，不开放任意 CLI | 保留安全边界并覆盖运维查询 |
| Redis 写入 | 独立 P2 能力，默认关闭 | 防止“刷新缓存”误变成删除/写入 |
| WAS 覆盖 | 默认 `fail`，可选 Kairo 产物清理，完整替换需确认 | 兼顾日常重打包和人工目录安全 |
| Compare 实现 | 抽象现有 overlay，不立即引入 Monaco | 贴合当前原生 JS，依赖和包体可控 |
| 告警 | Windows 通知 + 企业微信/钉钉 Webhook；邮件 P2 | 最快覆盖桌面运维场景 |
| Figma | 先按方案实现 token/布局，Figma 限额恢复后补组件稿 | 当前 Starter MCP 限额阻断了写入，不应阻塞代码开发 |

## 14. 明确不纳入本轮

- 不绕过只读策略开放 Oracle/MySQL 任意存储过程执行。
- 不把任意 Redis CLI 作为“方便运维”的快捷入口。
- 不默认删除用户未知文件或整个输出目录。
- 不为显示页码而默认对所有查询执行精确总数统计。
- 不在没有依赖证据时声称 WSDL 引擎识别成功。
- 不以浏览器权限通知替代 Windows 系统级失败告警。

