# LocalDrama Studio：2026-09-22 提交问题审查与 AI 实施方案

**审查快照：`f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41`**  
比较基线：`a1cd6acce39621381a92ebbbfc25bf5b7955703d`  
仓库：Qioooba/local_drama_studio  
交付内容：18 组待修问题、代码级改造方案、实际隔离实验、6 张浏览器截图、回归与验收清单。  
本报告没有修改仓库，也没有把建议补丁应用到产品。

> **首要结论：应先修“操作真正执行、输入真正保存、产物版本正确、媒体真正能审”，再做视觉润色。**
> 新增解说链路存在只返回“已提交”而不保存执行请求的入口；创建重试、正文持久化、二进制文档解析、多段旁白混音、跨块视频取样、审片读写对象和草稿保护也有具体缺口。部分页面仍是基础占位实现，不应按完整生产功能验收。

## 0. 审查与测试边界

### 0.1 “今天”的范围与覆盖

按北京时间 2026-09-22 理解“今天”。比较范围包括 UTC 当日的 11 笔提交，以及北京时间同日、UTC 前一天的 `8a63c604`，共 12 笔。所有正文结论都针对上面的固定 HEAD；后续新增提交不在本报告内。报告主张的是这些问题在本次快照中存在；除明确的新文件/改动外，不把每条问题都未经逐历史追踪地归因为某一笔提交刚刚引入。

| 提交 | 本次相关范围 |
|---|---|
| `f1251e1` | 生成契约、文档与依赖锁定 |
| `6eda28bc` | 前端一键制作、生产、交付、剪辑工作区与草稿 |
| `4bfe5c0a` | 时间线、剪辑、字幕与导出 |
| `62054272` | 项目包、归属与交付完整性 |
| `27b059bc` | 任务权威状态、outbox 与调度 |
| `78d3f151` | Qwen Image 2.1 接入 |
| `bc76131d` | 文档解析与迁移 |
| `99067714` | 解说前端 |
| `836fc081` | 能力就绪状态 |
| `3feb28b1` | 解说基础、迁移、编排与合成器 |
| `7cbe615c` | 忽略规则 |
| `8a63c604` | 关键帧复用与生产会话验收相关改动 |

**深查集中于：** 解说创建/来源/旁白/渲染/审片/导出 API，相关应用服务和仓库，FFmpeg 合成器，解说前端及旧 `OneClickPipelineWorkbench` 新增草稿逻辑。其他提交做了范围与差异初筛，**没有逐文件完整审完，更没有据此宣称没有问题**。基础任务系统、所有模型适配器、完整旧剪辑工作区以及 Windows/GPU 环境的全量回归仍需后续验收。

### 0.2 实际完成了什么

| 证据类型 | 实际执行 | 能证明与不能证明 |
|---|---|---|
| 固定版本源码追踪 | GitHub 读取具体文件、函数和调用方 | 能确认已检查链路中的分支、参数与副作用；不能替代整站运行 |
| FFmpeg 实验 | 真实 FFmpeg 7.1.5；合成音频、测试视频、输出日志与逐帧哈希 | 滤镜图按源码重建后实际执行；不是完整仓库渲染器导入测试 |
| 纯函数/SQL 隔离实验 | 文本解码、正文输入投影、分页 SQL、页面状态表达式 | 外围依赖替换为小型夹具；不包含生产迁移和全部数据约束 |
| 路由函数摘录实验 | `_resynthesize`、`_start_render`、`_start_export` 配合记录型仓库 | 能观察函数调用了哪些读写；不等于 FastAPI、队列和数据库的集成测试 |
| 浏览器交互截图 | Chromium 144、React 18.2；4 组状态/交互，6 张截图 | 生产 JSX/CSS 的相关摘录与测试数据；未运行完整 React 19 页面、AppShell 或 API |
| 后续表达式复核 | 时长求和、审片 subject、异步成功回调、幂等键输入等 | 只算源码表达式演算，不计入端到端测试通过率 |

完整仓库克隆/依赖下载受到执行环境网络限制；浏览器访问本地 HTTP 也被环境策略阻断。截图改用浏览器支持的离线 `page.set_content`，所有网络请求均中止，未修改网络限制。没有取得用户正在运行的实例、GPU 模型、正式数据库或云模型凭据。

**未执行：** 仓库完整 `pytest`、Vitest、TypeScript 构建、完整 Alembic 升级、整站 Playwright、真实模型生成、长片运行、人工听审/看片、Windows 打包与 GPU 压测。仓库实施记录中的通过数量是作者记录，不是本次实测结果。

### 0.3 证据与优先级约定

- **E1：外部工具实跑。** 本报告主要指真实 FFmpeg，对图/命令的重建范围有说明。
- **E2：源码摘录执行或浏览器隔离交互。** 依赖是夹具，不能延伸为整站通过。
- **E3：源码与调用链确认/表达式演算。** 不宣称在完整服务中复现。
- **P1：生产使用前应修。** 涉及执行断点、输入/草稿丢失、错误产物、错误版本或无法审听。
- **P2：紧随其后修。** 涉及可达性、性能、窄屏布局、潜在分支与错误治理。

没有因“未发现异常”而给某模块写通过结论，也没有把低证据猜测列为确认缺陷。

## 1. 问题索引与实施次序

| 编号 | 优先级 | 问题组 | 核心证据 |
|---|---|---|---|
| LDS-01 | P1 | 多个命令入口“已提交”但未持久化执行意图 | E2/E3 |
| LDS-02 | P1 | 创建幂等只覆盖项目，重试整条流程失败；事务边界拆裂 | E3，键输入演算 |
| LDS-03 | P1 | 粘贴讲稿正文在创建链路中丢失 | E2/E3 |
| LDS-04 | P1 | DOCX/PDF/EPUB 入口误用文本编码解码 | E2；DOCX 实际夹具 |
| LDS-05 | P1 | 多段旁白混音连接了不存在的 `voice` 标签 | E1 |
| LDS-06 | P1 | 跨分块长镜头没有推进源时间，重复前段画面 | E1/E3 |
| LDS-07 | P1 | 显式 render 引用缺少 edition/project 一致性校验 | E2/E3 |
| LDS-08 | P1 | 审片读写 subject 不一致，返工 revision 缺失 | E3 |
| LDS-09 | P1 | 已有媒体仍无播放器；逐帧与时间线未接真实数据 | E2/E3 |
| LDS-10 | P1 | 声音页空/错/对齐状态与真实数据脱节，历史 take 误计时长 | E2/E3 |
| LDS-11 | P1 | 解说稿切换段落直接丢掉未保存输入 | E2/E3 |
| LDS-12 | P1 | 旧一键制作成功回调错误清除请求期间产生的新草稿 | E3 |
| LDS-13 | P1 | 预检通过被显示为生产已提交；导出确认参数未生效 | E2/E3 |
| LDS-14 | P2 | 列表 cursor 无效且存在逐条查询开销 | E2/E3 |
| LDS-15 | P2 | 审片问题只显示前 8 条，后续问题无可见入口 | E3 |
| LDS-16 | P2 | 窄屏摘要卡被内部枚举撑宽，旁列被挤压 | E2 |
| LDS-17 | P2 | 永久编码错误被归为可重试，日志“截尾”并未限制采集内存 | E3 |
| LDS-18 | P2 | 旁白起始采样偏移无效；采样裁切选项错误 | E1/E3 |

建议先提交 LDS-05/06 的小型合成器修复及回归，同时推进 LDS-01/02/03/04/07/08 的后端契约；再完成 LDS-09～13 的真实前端闭环，最后处理 P2。不要用“把按钮禁用”替代应有的业务实现；缺少执行能力时可以先明确禁用并说明原因，作为临时防误导措施。

## 2. LDS-01｜多个命令只返回描述，没有真正安排执行

**优先级 P1。影响：重读、局部返工、手动渲染、政策重跑、发布包构建。**

### 代码定位与根因

检查 [api/routes/explainers.py][src-routes] 的 `_resynthesize`、`plan_explainer_repairs`、`_start_render`、`record_explainer_decision` 的 `rerun_policy` 分支、`_start_export`；同时检查 [production.py][src-production] 的 `plan_repairs`，以及 [explainer_repository.py][src-repo] 的通用 `insert/update`。

| 路径 | 实际副作用 | 返回给调用方 |
|---|---|---|
| `_resynthesize` | 查询 edition/video/segment；没有写入 | `requested_stage=NARRATION_TTS`，以及一组 invalidates/preserves 描述 |
| `plan_explainer_repairs(confirm=True)` | 调用只读 `plan_repairs` | `submitted=True`；内部 plan 同时写着 `would_create_jobs=False` |
| `_start_render(confirm=True)` | 必要时冻结 composition；把 edition 改为 `RENDERING` | `SUBMITTED`，但没有 job/run/持久化命令标识 |
| `rerun_policy=True` | 直接构造返回字典 | “已请求内部处理器按冻结政策重跑” |
| `_start_export` | 插入 `BUILDING` 的 publication package | `BUILDING`；入口未调用排队端口 |

通用仓库的读写只是 SQL，不会在 `update` 内隐式提交作业。因此“worker handler 已实现”不等于“这些按钮触发了 handler”。对于导出，入口可确认没有直接排队；未执行驻留系统全量扫描验证，不将此扩大为“任何情况下永远不会被处理”。对于重读/政策重跑，甚至没有可供后续扫描识别的持久化意图。

### 实际隔离结果

`API-reread-durable`：返回 `NARRATION_TTS`，记录型仓库写入次数为 **0**。  
`API-render-durable`：返回 `SUBMITTED`，只观察到 edition 状态更新，没有任务/命令创建。

证据：`evidence/route_probe_results.json`、`source_excerpts/routes.py`。这些是路由内部函数摘录执行，不是完整 HTTP 测试。

### 实施方案

**新增建议：** `application/explainers/commands.py`，作为该领域的命令应用服务。路由只负责入参、错误映射和调用。应接入现有 `AutomationWorkflowService`、jobs 与 outbox，不再创建第二套 claim 队列。

统一约定返回三种不同结果：

```python
# 接口草案，不是仓库现有类；实际字段名与现有队列契约对齐。
Accepted = {
    "status": "ACCEPTED",
    "operation_id": "...",
    "workflow_run_id": "...",  # 或现有执行权威标识
    "job_ids": ["..."],        # 异步展开时允许为空，但须有持久意图与状态入口
    "subject": {"kind": "...", "id": "...", "hash": "..."},
}
Blocked = {"status": "BLOCKED", "blockers": [...], "operation_id": None}
Preview = {"status": "PREVIEW", "plan_hash": "...", "impact": {...}}
```

一个事务内完成：校验归属与 expected revision → 查幂等记录 → 固定输入/版本/预算 → 写入现有任务或 outbox 意图 → 保存幂等响应。事务提交后才能返回 `ACCEPTED`。不能在数据库事务里等待模型或 FFmpeg，也不能通过 HTTP 内存后台任务充当持久队列。

重读必须固定 edition、讲稿 revision、canonical segment、朗读文本 hash、voice/model 参数及此次请求的原因。局部返工必须固定问题证据、影响闭包、人工锁定排除集合与预算。渲染必须固定 composition/manifest，而不是执行时重新读取“最新”。导出必须固定渲染哈希与发布参数。

未接线的命令应返回现有结构化 `CAPABILITY_UNAVAILABLE` 或明确未开放状态，不能返回“已提交”。

### 验收与回归

建立真实 API + 数据库 + dispatcher 合同测试。每个命令验证：首次请求产生唯一持久意图；重复键/相同 payload 返回同一执行对象；同键/不同 payload 冲突；服务响应丢失后重试不重复执行；进程在提交后重启仍可领取；成功必须有业务产物登记，不只看 HTTP 2xx。重读至少产生新 take，返工至少产生受影响闭包的执行记录，渲染至少由真实 worker 领取，导出至少推进到实际文件校验。

## 3. LDS-02｜创建重试不是幂等的完整业务操作

**优先级 P1。影响：预检失败后的重试、网络重试、同标题新作品、创建中途异常。**

### 代码定位与可触发链路

[CreatePage.tsx][src-create] 的 `createAndPreflight` 每次都调用创建接口。前端创建键仅包含 `title + inputKind + targetSeconds`，没有包含正文、输出组合、来源、研究策略等字段。

[create_explainer][src-routes] 先调用 `ProjectService.create_project`，再调用 `ExplainerProductionService.create_video`。检查 [projects.py][src-projects] 可见前者在自己的事务中提交，并在命中幂等记录后返回已有 project；检查 [create_video][src-production] 可见后者另开事务，发现已有 video 就抛 `INVALID_REQUEST`。

因此：

1. 第一次创建成功，但后续上传、预检、提交失败；再次点击，前端又创建。
2. 请求完全相同时，project 幂等重放成功，但第二步 `create_video` 拒绝已有 video，整个业务操作重放失败。
3. 只改正文而未改标题/类型/时长，前端仍生成同一 key，后端完整 payload digest 已改变，触发幂等冲突。
4. project 事务已提交后，video 创建因栏目版本或参数校验失败，不能由后者事务自动回滚前者。
5. `_derive_project_code(title)` 是确定性的标题哈希；相同标题会得到相同 code。`ProjectService` 检查 code 唯一，所以“有意再做一条同名作品”也会遇到冲突。这与其注释“把同名视频区分开”不符。

### 实施方案

创建幂等 scope 从“project:create”扩展为完整的“explainer:create”业务操作。复用现有 `command_idempotencies`，但保存的是 project/video/输入引用/初始输出配置的完整响应，不只 project_id。

新增应用层创建入口，提供可复用的“在给定 connection 内插入项目”的内部能力，避免调用两个各自提交的公开服务方法。目录创建保持既有 ownership marker 与安全回滚规则；文件系统无法与 SQLite 真正原子提交，须明示 staged/committed 状态和补偿，而不是把“同一数据库事务”写成覆盖整个文件系统的原子性。

前端按操作意图生成 `operationId`。同一意图的网络重试保留 key；用户明确新建另一作品时生成新 key。编辑尚未提交表单可以形成新意图；已创建后进入上传/预检阶段，应使用 `createdProjectId` 继续流程，而不是重复创建。标题只做展示，项目 code 加唯一后缀或由服务分配。

建议将创建页面过程表达为：

```text
EDITING → CREATING → PROJECT_CREATED → SOURCE_READY → PREFLIGHT_READY
                                             ↘ BLOCKED（保留项目与原稿）
PREFLIGHT_READY → SUBMITTING → ACCEPTED
                      ↘ SUBMISSION_FAILED（重试提交，不重新创建）
```

每个阶段保留操作键、项目 ID、已完成来源 hash 和下一步；不要把整个链路合成一次不可恢复 mutation。

### 验收

同请求连续调用两次，project/video 都只有一条且返回相同 ID；第一次 video 校验失败不能留下被当成完整作品的孤儿项目；预检阻塞后改配置再试不新建项目；同名不同意图允许创建；正文变化不会误复用旧 key；网络超时重试与人工再次新建要有不同语义。已存在孤儿项目需迁移/维护脚本识别并供用户恢复或清理，禁止直接删除用户目录。

## 4. LDS-03｜粘贴正文没有被持久化为正文

**优先级 P1。代码级确定，已执行输入投影实验。**

### 根因

[CreatePage.tsx][src-create] 对非 TOPIC 输入会传 `topic: title.trim()`，同时传 `pasted_text`。[create_explainer][src-routes] 使用 `topic=payload.topic or payload.pasted_text or ""`，所以这里取到的是标题。

`_input_payload` 只保存：

```python
{
    "source_refs": [...],
    "reference_urls": [...],
    "pasted_text_present": bool(payload.pasted_text),
    "pasted_text_length": len(payload.pasted_text or ""),
}
```

随后 `create_video` 原样写入 `input_payload_json`。正文自身既没有进入 topic，也没有进入输入内容；长度和请求 hash 无法还原原文。不能把本问题表述为“所有调用方都丢正文”：没有 topic 的纯 API 调用可能将正文放入 topic；**已确认受影响的是当前前端粘贴稿路径**。

### 实施方案

复用解说来源/讲稿版本体系，创建不可变的粘贴输入记录。保存规范化正文、原始内容哈希、字符数、语言、来源类型与处理版本。`input_payload_json` 保存稳定引用和 hash，不只 `present/length`。

PASTED_SCRIPT 的目标是“尊重用户已有稿件”，应明确是导入为初始 ScriptRevision 还是仅作研究来源；界面不能把这两种行为混为一谈。默认保留已有稿件，进一步改写要成为显式操作。预检 plan_hash 必须包含实际内容引用/hash；重开页面能还原输入，worker 不依赖浏览器内存。

### 验收

上传/粘贴包含唯一标记的长稿，创建后重启 API 再读取，正文逐字可核对；不同正文同长度产生不同内容 hash；前端、API、研究阶段、初始讲稿都能追到同一个版本；空白、GBK 转换、换行规范化均明确，不静默截断。

## 5. LDS-04｜二进制文档上传走了文本解码器

**优先级 P1。DOCX 已用真实文件夹具复现；PDF/EPUB 是同入口静态风险，未执行相应完整解析实验。**

### 根因与证据

[import_explainer_source][src-routes] 允许 `.docx/.pdf/.epub`，却统一调用 `decode_document_bytes(temporary_path.read_bytes())`。[sources.py][src-sources] 只处理 BOM、UTF-8 和 GB18030，并用 replacement character 数量决定是否 decoded。

真实 `fixture_document.docx` 中的测试段落没有被提取出来。DOCX/EPUB 的压缩容器、PDF 的文档结构不能用换编码代替解析；“编码无法识别，请选择编码”也不是这类文件的正确补救。

### 实施方案

优先复用这次提交已经涉及的共享文档导入/解析能力，而不是为解说单独造第二套解析器。实现统一 `DocumentExtractionPort`：输入受控临时文件、检测格式、大小预算、页/段限制；输出正文、章节/页定位、提取质量和可追踪错误。

按文件实际类型分派：纯文本使用现有编码处理；DOCX 提取段落与表格；EPUB 按 spine 顺序提取正文；有文本层 PDF 使用文档解析；扫描件明确 `TEXT_LAYER_MISSING`。OCR 是独立、显式可选能力，不默认把识别结果当成可靠原文。

限制不应只检查上传文件本身的字节数：还要检查解压后体积、文件数量、嵌套/异常容器、提取耗时与最终字符预算。不要在 async 路由中对大文件执行无边界的同步解析；复用后台导入任务或受控线程/进程。

### 验收

真实 TXT UTF-8/GB18030/UTF-16、含表格 DOCX、文本层 PDF、扫描 PDF、含多章节 EPUB 各一个夹具；正文非空不等于正确，断言关键段落和顺序。损坏压缩包、异常扩张文件、超时、空文档均返回可操作错误；不要把 zip/pdf 字节当作“低置信度正文”继续喂给模型。

## 6. LDS-05｜多段旁白连接到了不存在的标签

**优先级 P1。真实 FFmpeg 已复现。**

### 定位与实验

[ffmpeg_renderer.py][src-renderer] 的 `build_mix_command` 在旁白数量大于 1 时，concat 输出为 `[voice_raw]`，后续 `voice_label` 却设为 `voice`。后续压缩/混音/响度处理消费的 `[voice]` 没有生产者。

```text
[n0][n1]concat=n=2:v=0:a=1[voice_raw]
[voice]...                       ← 标签没有接上
```

实跑单旁白对照成功；多旁白不带 BGM 和带 BGM 都退出 **234**。日志包括：

```text
Filter 'concat:out:a0' has output 0 (voice_raw) unconnected
Error binding filtergraph inputs/outputs: Invalid argument
```

只修正生产/消费标签后，两组同输入对照都成功。`atomic_render` 确实调用 `build_mix_command`，不是只存在于一个未接入的辅助函数中。实验仍是重建图执行，不是产品补丁已上线。

### 修复方案

最小补丁统一标签，例如 concat 直接产出 `voice`；或者后续引用 `voice_raw`。不要顺手重写整段混音以致无法隔离根因。

随后为结构化 FilterGraph 增加构建时校验：每个内部标签只有一个生产者；每个消费标签有生产者；除声明映射到输出外，不允许悬空输出；需要一份流供多处分支使用时显式 split/asplit。标签构建接口返回标签对象，减少手写字符串漂移。

### 验收

0/1/2/10 段旁白；有/无 BGM；有/无 SFX；不同采样率输入；短于/长于目标时长。真实 FFmpeg 运行后检查采样数、输出时长、声道、可解码性和关键片段声音。图字符串快照不能替代真实子进程回归。

## 7. LDS-06｜长镜头跨块时重复源片段

**优先级 P1。真实测试视频与逐帧哈希已复现。**

### 根因

[build_chunk_command][src-renderer] 用全局时间范围判断 clip 是否落入某块，但裁切源仍从 `clip.source_in_us` 开始，且使用完整 clip 帧数，没有把 `chunk.decode_start_frame` 与 clip 起点的交集偏移换算到源时间。

实验用 4 秒、25 fps、共 100 帧的变化测试视频拆成两块：第二块的 50 帧实际与第一块相同，而不是从源视频第 50 帧开始。证据在 `chunk_*.framemd5` 和对应命令日志中。该实验缩小画面且只测视频，未覆盖转场、变速和音频。

### 修复设计

引入一个可纯函数测试的切片计算对象，例如建议新建 `application/composition/slices.py`，让 FFmpeg builder 只消费计算结果，不在字符串拼接处重复做时间换算。

对于全局 clip 区间 `[Cs, Ce)` 和含 handles 的块解码区间 `[Ds, De)`：

```text
L = max(Cs, Ds)
R = min(Ce, De)
若 L >= R：不参与该块
本块片段帧数 = R - L
相对于 clip 的推进帧数 = L - Cs
源起点 = source_in + 经过播放速度映射后的推进时间
```

25 fps、1 倍速时可直接换算；一般情况使用有理数 `fps_num/fps_den` 和明确的源采样映射，禁止反复浮点四舍五入。片段本地 PTS 归零后按其在块内的真实位置放置。handles 应在统一层裁切一次，不能既在输入段裁掉又在拼接时再次裁掉。

存在变速、静帧补齐、源不足或转场时使用 manifest 的明确策略，不擅自循环同一小段来凑时长。音轨用同一时轴换算样本区间。

### 验收

单 clip 跨 2/3 块；多个 clip 在边界切换；带前后 handles；跨转场；24/25/30/30000÷1001 fps；源 offset 非零；尾块不足整块；音频边界连续。使用带帧号/变化图案的片源核对每个边界前后至少 3 帧。验收不仅总帧数正确，还要帧内容与时间位置正确。

## 8. LDS-07｜render 引用与当前作品缺少一致性校验

**优先级 P1。属于数据作用域/交付完整性问题；不将本地应用问题夸大为已验证的远程攻击。**

### 定位

[_start_export][src-routes] 如果请求指定 render_id，就直接 `repo.find("composition_renders", payload.render_id)`，然后用当前 edition/video/project 和这条 render/composition 建发布包，没有核对 render 是否属于当前 edition。

[_qc_view][src-routes] 对显式 render_id 也使用该 render 的 subject 查询报告，没有先建立与 URL edition 的归属关系。[0102 迁移][src-migration] 的 publication_packages 是各字段独立外键，存在性约束不能保证它们属于同一作品。

隔离函数调用：URL edition=`eA`，render=`rB`（属于 `eB`），产生了 editionA + renderB 的 package。记录型仓库不模拟完整迁移；源码和独立外键结构共同支持需要补校验的结论。

### 代码方案

在 [explainer_repository.py][src-repo] 增加共享作用域读取方法（建议名 `require_render_for_edition`），使用 JOIN 沿 render→composition→edition→video→project 校验。导出、QC、decision subject 解析和下载入口统一使用它，不复制不同版本的校验逻辑。

`_subject_hash` 接受 edition_id 却未使用；需一并改为“先解析所属作用域，再返回 subject/hash”。本报告没有完整执行 human decision 服务，不把后续层可能存在的校验漏掉后直接宣称授权绕过。

调用方显式指定资源 ID 时，不匹配应返回明确 `INVALID_REQUEST/NOT_FOUND`，不能悄悄回退到当前第一版。对当前没有完整成片、完整性失败、已 stale 的版本，也应拒绝作为可交付来源。

### 验收

同项目不同 edition、不同项目、旧 render、已删除资源、错误 hash、合法同 edition 六类测试；失败均须发生在任何状态变更/包写入之前。补一次离线数据审计，列出已有 package 的 project/video/edition/render 归属不一致记录；先隔离和标记，不删除媒体。

## 9. LDS-08｜审片读写对象不一致，返工缺少真实 revision

**优先级 P1。前后端契约问题，不是刷新按钮可以解决。**

### 两条确定的断链

**第一条：** [ReviewPage.tsx][src-review] 用 `useExplainerQc(editionId, null)` 读取 QC；[_qc_view][src-routes] 在 render_id 为空时查询 subject=`EDITION`。但人工确认写入的是 `COMPOSITION_RENDER` + 当前 render_id/hash。因此确认后刷新仍读另一类 subject，新的确认记录、成片 QC 和覆盖率可能不出现。

**第二条：** [production.overview][src-production] 的 video 投影没有 revision；ReviewPage 发起返工时使用 `Number(overview.data?.video?.revision ?? 1)`。后端 `plan_repairs` 检查实际 video revision，因此数据一旦大于 1，前端回退值就会制造过期冲突。通用 repository.update 会递增 mutable 表的 revision。

### 修复设计

建立明确的当前审片目标 DTO，复用当前 render 与 edition 元数据，包含：project_id、video_id、video_revision、edition_id、edition_revision、render_id、render_sha256、composition_revision_id、manifest_hash、媒体引用与可用状态。

前端查询键必须包含 edition_id + render_id + render_hash；没有 render 时展示“尚无成片”，不要把 edition QC 伪装成成片 QC。决定、返工、导出与播放器都从同一个目标快照取值。切换版本清理不属于当前 subject 的 issue 参数，并取消/隔离旧响应。

返工 revision 不允许 `?? 1` 兜底；读不到真实 revision 时禁用提交并提示重新读取。对选中版不存在的 URL 参数，执行显式纠正/提示，不能“显示第一版、请求仍用旧 ID”。

路由返回模型从宽泛 dict/`response_model=None` 收敛为这些关键 DTO 的显式 schema，重新生成客户端；不要求一次重写所有 37 个接口。优先让编译器暴露 revision、subject、media 字段缺失。

### 验收

对 renderR1 写人工确认后刷新仍可见；切换 R2 不沿用 R1 的批准；R1 hash 变化后旧决策失效；video revision=3 时可正常规划返工；过期 revision=2 返回409且展示差异；不存在的 URL edition 不得写入其他 edition。

## 10. LDS-09｜已有媒体仍是占位预览，审听和逐帧功能未闭环

**优先级 P1（从“基础 UI”升级为真实生产工具时）。**

### 当前实际表现

[ReviewPage.tsx][src-review] 即使 `current_render` 非空，也渲染 [MediaPlaceholder][src-components]；后者始终显示“尚未生成媒体”。前后帧按钮只修改本地整数，没有 `<video>`、seek 或当前帧同步。时间用 `frame * 40`，等价于写死 25 fps；没有按成片总帧数限制上界。下方时间线是固定的三段画面、几段音轨与 cue，不是 manifest 数据。

[AudioPage.tsx][src-audio] 同样没有真实 `<audio>`，已有 take 也无法试听。仓库实施记录明确将预览定位为占位，这里按“功能尚未交付”记录，不指控其伪造真实成片。

浏览器夹具提供非空 render、30 fps、60 帧。实际 DOM 中 video 数量为 **0**；点击 62 次后计数到 **62**，超出夹具范围。该夹具的媒体 ID 只是测试值，没有真实后端资源请求。

![已有 render 的隔离审片组件仍显示占位；不是整站截图](screenshots/01_review_existing_render.png)

### 实施方案

后端 `_editions_view` 当前只暴露 render id/integrity/hash，需提供经归属校验的 media_version_id、预览资源引用、时长、总帧数、fps 和渲染完整性。沿用安全媒体路由；不向浏览器输出任意本地路径，不允许任意 path 参数读盘。

新增建议组件 `RenderPlayer.tsx` 与 `NarrationPlayer.tsx`。媒体加载前后明确区分：未生成、生成中、校验失败、可播放、读取失败。preview 解码失败给重试与诊断，不再统一说“没有媒体”。

普通播放用 HTMLMediaElement；按钮时间按 `frame * fps_den / fps_num` 计算，限制 `0…total_frames-1`。浏览器 seek 对可变帧率与压缩关键帧并不保证精确逐帧，因此要么明确“按帧定位预览”的限制，要么接后端按帧索引提取/缓存的审查帧接口。不要把改一个整数称为逐帧审片。当前显示位置以实际解码/seek 完成事件更新，不能先行写成目标帧已显示。

时间线按冻结 manifest 的 clip/audio/subtitle 区间绘制；点击问题 seek 到证据区间。若本期不做可编辑时间线，可以先做只读、真实对齐的审片时间线，不必另造完整视频编辑器。

人工确认表单绑定渲染 hash，可填写审查说明与范围。没有可播放/可检查媒体时不提供无条件“确认当前成片”成功路径。导出完成后提供状态、清单、经授权下载入口，不只显示已提交文本。

### 验收

真实短 MP4 与 WAV，不调用 GPU：可播放、有声音、seek 可见变化；24/25/30 fps 显示正确；无媒体/404/不支持编码有正确状态；字幕时间轴源于真实 cue；issue 点击定位有效；版本切换停止旧媒体并清理旧审片标记。

## 11. LDS-10｜声音页状态、对齐与时长读模型不可信

**优先级 P1。包含同一声音工作流的四处契约不一致。**

### 根因与观测

**空/错被加载状态遮住。** [useExplainerQueries.ts][src-hooks] 在没有 editionId 时禁用 narration 查询；无缓存的禁用查询可为 pending/idle，参照 [TanStack Query 官方禁用查询文档](https://tanstack.com/query/latest/docs/framework/react/guides/disabling-queries)。[AudioPage.tsx][src-audio] 却先判断 `editions.isPending || narration.isPending`，后判断“没有 edition”。所以已经成功返回空版本列表，仍显示加载；版本请求错误时也可能被禁用子查询的 pending 遮住。

![没有任何请求运行，空版本仍显示加载；隔离状态夹具](screenshots/02_audio_empty_loading.png)

**有 take 不代表已对齐。** 当前绿色“已对齐”只依赖 selected take 存在，没有核对 alignment 输出、哈希和状态。截图夹具故意给 take 附带 FAILED 测试标记，用来证明这个渲染条件完全不读取对齐结果；这个附加字段不是在宣称生产 take schema 具备该列。真正的 alignment 在 API 返回中是单独列表。

**总时长把历史重读也加进去。** [_narration_view][src-routes] 查 video_id+locale 下所有 takes，`measured_total_ms=sum(all takes)`；没有限定当前冻结讲稿与选中 take。表达式实验：当前选中 2000ms + 历史未选中 1000ms，得到 3000ms。页面取选中音频，摘要却显示所有历史总和。

**字幕语言和配音语言混用。** 页面相同 locale 同时用于旁白与字幕查询；后端旁白查询又采用 `locale or edition.voice_locale`。用户切换字幕查看语言时，可能实际改变旁白查询目标，与独立英语 edition 概念冲突。

![存在音频引用，但未接试听；仅凭 take 存在显示“已对齐”](screenshots/03_audio_ready_no_player.png)

### 实施方案

页面先判断父查询：错误→重试；加载→加载；成功且无版本→空状态；有有效版本后才判断其子查询。不能单独用 disabled query 的 `isPending` 代表正在发请求。保留旧数据刷新失败时展示 stale 提示，不清空内容。

后端建立 `NarrationEditionView`，每个冻结段落仅返回当前选中 take 的明确引用，并关联该 take 的当前对齐 revision、媒体 hash、对齐状态和错误。`measured_total_ms` 来自当前编排实际选用的片段/时间线，不来自历史 take 表简单求和；区分片段时长之和与含停顿/重叠后的成片时间轴长度。

前端按 `NOT_GENERATED / AUDIO_READY / ALIGNING / ALIGNED / FAILED / STALE` 显示文案；不要先把所有 hasAudio 渲染为 ALIGNED。配音语言固定来自 active edition，字幕预览语言是独立状态；不存在的语言以“尚未生成该字幕”表示，不跳读另一种配音。

### 验收

父查询空、父查询错、子查询禁用、子查询错、音频有而对齐无、对齐失败、对齐旧 hash、当前对齐成功、重复重读 3 次、切换中英字幕十类状态。摘要时长不能因保存历史版本而增加；切字幕不触发英文 TTS/take 切换。

## 12. LDS-11｜讲稿编辑切换段落会丢未保存输入

**优先级 P1。真实浏览器交互已复现。**

### 根因

[ScriptPage.tsx][src-script] 只有单个 `editing` 对象。点击另一段“修改这一段”直接替换对象，没有脏稿检查，也没有接入已有 `draftRegistry`。保存成功回调还无条件 `setEditing(null)`，可能在保存 A 期间清除后来开始编辑的 B。

浏览器步骤：修改 A → 输入“第一段刚输入、尚未保存的新内容：应该保留。” → 点击 B 编辑 → 返回 A，文本恢复为“第一段原文”。

![切换前：第一段存在未保存的新输入](screenshots/05_script_unsaved_before_switch.png)

![切换后：第一段输入已回到原文](screenshots/06_script_unsaved_lost.png)

### 实施方案

复用现有 `features/drafts/draftRegistry`。草稿 key 至少包含 project/video、script revision 与 canonical segment；不要只用易随版本变动的临时 UI 行号。

```ts
// 建议结构；与现有 DraftHandle/DraftSaveResult 契约适配。
type SegmentDraft = {
  canonicalSegmentId: string;
  baseRevision: number;
  version: number;
  display: string;
  spoken: string;
  dirty: boolean;
};
```

切换段落时保留对应草稿；离开工作区走统一保存/丢弃/取消。保存 mutation 的 variables 必须固定 submitted segment/revision/local version/text；成功后只更新相应草稿的已保存基线，不清除其他段落，不清除保存期间新增编辑。

409 时保留本地文本，展示服务器新版本与差异，允许重新合并，不自动以服务器覆盖本地。长稿草稿建议 IndexedDB；采用节流/空闲写入，而非每个字符同步写大量 localStorage。

### 验收

A→B→A 不丢稿；保存 A 同时编辑 B，B 保留；保存 A 时继续输入 A，后输入仍 dirty；刷新恢复；导航取消不丢；并发409保留文本；保存明确失败不把草稿标成已保存。

## 13. LDS-12｜旧一键制作的成功回调误清新草稿

**优先级 P1。今天新草稿保护逻辑中的竞态；不是旧“没有草稿注册”的问题。**

### 定位与因果

[OneClickPipelineWorkbench.tsx][src-pipeline] 的 launch mutation 在异步操作前捕获 `startPayload()`，但 onSuccess 用的是最新 `rawTextRef.current/activeSourceRef.current`，并删除本机/会话草稿，随后把当前 version 标为 clean。

用户提交 A 后，在预检/启动等待中输入 B；成功返回的执行对象对应 A，回调却将 B 当作已提交基线，并清掉 B 的保护。页面输入并没有统一在启动时锁定。部分 onChange 还调用 `launchMutation.reset()`，需要专门测“observer 重置并不等于底层请求取消”的行为；本报告未运行真实 TanStack mutation，后者不算已完成动态复现。

另一个重试分支先 `void invalidateQueries(...)`，立刻 `retryMutation.mutate()`，没有等待新的 run revision 到达。已发生409时可能再用旧 revision 提交。

### 实施方案

```ts
// 设计伪代码：mutation 的输入成为不可变操作快照。
const submitted = {
  projectId,
  commandKey,
  payload: buildPayload(),
  manuscriptVersion: manuscriptVersionRef.current,
  sourceIntent: sourceIntentRef.current,
};
launch.mutate(submitted);

// onSuccess(result, submitted)
// 只把 submitted 对应的内容记入已提交基线。
// 当前版本更高：保留当前草稿与 dirty 状态，绝不能清空缓冲。
```

将“保存/提交基线”与“当前输入”分开。成功消费版本 v1 后，当前 v2 仍需保存；只有当当前版本、来源意图和项目都匹配 submitted 快照时，才能删除相应本机草稿。避免一个全局 reset 让另一在途请求的状态丢失。

重试先 await 显式 refetch 获取最新 run；读取失败停止重试；提交 mutation 的 variables 带该次读取的 revision。需要用户重新确认的影响范围变化，不应自动沿用旧授权。

### 验收

用 deferred promise 控制先后：发 A → 编辑 B → 让 A 成功 → 断言 B 保留、dirty、可恢复；A 失败也同样保留 B。项目切换后 A 的迟到结果不得清 B 项目缓存。409重试记录的 expected_revision 必须来自已完成的最新读取，而非闭包旧值。

## 14. LDS-13｜预检、接受、完成三种状态混在成功提示里

**优先级 P1。涉及用户是否可以离页、是否会消耗资源、是否已得到产物。**

### 确认的问题

[CreatePage.tsx][src-create] 只要 `preflight.executable` 就显示“预检通过，已自动提交生产”。但 run 提交是后续异步步骤；提交仍在进行或已经失败时，这段文字仍可能显示，形成成功与失败并存。

[ExplainerExportRequest][src-schemas] 的 `confirm` 默认 false，但 [_start_export][src-routes] 完全没有读取该字段；隔离调用 confirm=false 仍创建 `BUILDING` 包。同一个 key 调用两次生成两个 package ID，key 只被回显，没有用于该入口去重。

### 实施方案

将状态提示建立在命令响应的 discriminated union 上，而非 HTTP onSuccess 或 preflight boolean。`PREVIEW` 只说明可执行性；`ACCEPTED` 必须有持久执行标识；`COMPLETED` 必须有经过完整性验证的产物。提示区同时展示 operation ID 与可恢复的当前步骤。

确认语义需要统一：如果导出产品设计保留 preview/confirm 两阶段，confirm=false 只能算计划、许可范围与成本，不创建 BUILDING；confirm=true 校验冻结 plan_hash 后持久化。若实际产品不需要两阶段，则删除该未生效参数并调整调用方/文档，不能留一个默认 false 的虚假保护开关。

相同操作 key/payload 返回相同 package，不同 payload 返回冲突。确认与幂等的底层实现合并进 LDS-01 命令服务，避免每个路由重复写。

### 验收

预检通过但提交超时，只能显示“预检已通过、提交结果待确认”，不得显示“已开始”；提交明确失败后没有绿色已提交提示；导出 confirm=false 无包写入；相同 key 20 次重放仍一个包；结果未知时通过 operation ID 查询，不能盲目新建另一包。

## 15. LDS-14｜分页参数无效，列表与详情有重复查询

**优先级 P2。列表超过默认条数后既有功能缺失，也有扩展性问题。**

### 根因与实验

[list_explainers][src-routes] 接受 cursor，但 SQL 只有 `WHERE product_kind = ? ORDER BY updated_at DESC, id LIMIT ?`，完全未使用 cursor，返回的 next_cursor 永远为 null。默认查询 50 条；当作品数超过默认 limit，调用方不能靠该契约继续翻页。

隔离 SQLite 插入 75 条记录、每次 limit=50，更换 cursor 仍返回同一页。不是“超过 200 条必崩溃”，而是明确分页功能没有实现。

该函数还对每条作品分别查 edition_count 和未关闭 issue ID 列表，再 `len`。50 个有效作品即有约 1+2N 次查询；这是按代码结构计算，不是实际耗时测量。`_editions_view` 又在每个 edition 内反复查询同 video 的全部字幕 revision，将其作为各 edition 字幕数量。

### 实施方案

采用 keyset 分页。若排序为 updated_at DESC、id ASC，则下一页条件为：

```sql
WHERE product_kind = :kind
  AND (updated_at < :last_updated
       OR (updated_at = :last_updated AND id > :last_id))
ORDER BY updated_at DESC, id ASC
LIMIT :limit_plus_one;
```

cursor 版本化编码排序键和筛选条件，校验长度/格式，不把未校验字符串拼接进 SQL。取 limit+1 判断还有下一页。说明实时更新会改变顺序；需要稳定快照时增加明确列表快照边界，不能承诺普通 updated_at 分页在并发更新下绝无重复。

先拿当前页项目/视频 ID，再各做一次有界聚合统计，并映射回当前页；不要为优化 N+1 改成每次聚合全库。字幕统计按 edition_id 分组，而不是每个 edition 使用整个 video 的总数。

前端 `useExplainerList` 接游标或 infinite query，保留已加载页、重试和筛选状态。增补与筛选/排序一致的索引，并用 EXPLAIN QUERY PLAN 验证；索引方向与 SQLite 实际版本对齐。

### 验收/性能目标

75 个作品分成 50+25，集合不重不漏；空页/最后一页/同更新时间/错误 cursor 测试；每页查询数量控制为常数，建议不超过 4 次主要查询；1000 作品夹具记录真实查询计数、响应字节、p50/p95。此处数字是建议验收目标，不是当前已测性能。

## 16. LDS-15｜第 9 个及之后的问题无法从审片列表进入

**优先级 P2。**

[ReviewPage.tsx][src-review] 对 issues 使用 `slice(0, 8)`，计数却来自全部 issues，未提供显示剩余问题的按钮、分页或完整列表入口。用户看到“阻塞 12”却可能只能操作前 8 项。通过 URL 手工指定 issue 不是正常产品可达性。

增加严重度/状态/检测器筛选与“查看全部 N 项”；较大列表分页或虚拟化。点击问题同时设置选择与播放器证据位置，选中不因 refetch 丢失。默认按 BLOCKER→MAJOR 和时间排序，但顺序稳定，不要每轮刷新跳动。

验收至少放 12 条不同严重度问题，键盘和鼠标均能进入第 9/12 条；修复后计数与列表一致，已关闭问题不继续混为待处理。与 LDS-08 的 subject 隔离一起验证，防止跨版本问题混排。

## 17. LDS-16｜窄屏卡片被内部枚举撑开

**优先级 P2。截图属于隔离样式环境，不能当作整个产品所有窄屏页面的结论。**

### 实测

[explainers.css][src-css] 的窄屏 `.explainer-summary-strip` 使用 `1fr 1fr`。[AudioPage][src-audio] 直接展示 `NATURAL_NARRATION` 等内部枚举。

390px 视口下，浏览器测得两列约 **228.7px / 79.8px**。长枚举的最小内容宽度挤压右侧，字幕方式等字段窄列换行。此次测量没有发现卡片自身 scrollWidth 横向溢出，所以问题应描述为“列宽挤压与信息难读”，不是“已证实横向滚动”。

![390px 视口摘要卡窄列挤压；隔离组件截图](screenshots/04_audio_mobile.png)

### 修复

```css
/* 建议补丁；先检查全站样式叠加，再合入相应断点。 */
.explainer-summary-strip {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}
.explainer-summary-strip > div { min-width: 0; }
.explainer-summary-strip strong {
  display: block;
  overflow-wrap: anywhere;
}
@media (max-width: 820px) {
  .explainer-summary-strip {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
```

优先将枚举本地化为“随旁白自然时长”等产品文案；完整技术值放详情/复制诊断，不将机器枚举当主信息。时长、状态、主操作优先，解释性段落折叠到次级详情。保持移动端最小可操作尺寸，并在真实 AppShell 内复查，而不是仅凭隔离截图决定断点。

验收 390/820/1366/1920px、200% 文本缩放、长英文标题、最长枚举/ID，检查列宽、文字可见、焦点、按钮换行和滚动容器；不能靠全局 overflow:hidden 把问题藏起来。

## 18. LDS-17｜错误分类会重试永久错误，日志采集并非有界

**优先级 P2。主要为任务成本、恢复行为与长时运行风险；未做真实 GPU 重试次数或 RSS 压测。**

### 根因

[classify_process_failure][src-renderer] 用 output_path 判断是否产出文件；[FfmpegRunner.run][src-renderer] 调用它时没有传 output_path。普通非零退出且不是磁盘/负退出码分支时，`produced_output=False`，于是走 `RECOVERABLE_FAILED/NO_OUTPUT`。像 LDS-05 的确定性滤镜错误也会被当作可重试，而重试同一图不会自己恢复。

`_default_process_runner` 的注释声称采集 bounded tails，实际使用 `subprocess.run(capture_output=True, text=True)`；之后才截取 stderr 尾部。这限制的是返回内容，不是采集过程的内存上限。长时高频日志可能积累，当前未测具体内存涨幅。

### 修复

将错误分类拆成工具缺失、输入缺失、参数/滤镜无效、资源不足、超时、用户取消、未知失败。先匹配确定性的输入/配置错误，再决定是否允许有预算的技术重试。磁盘不足需要清理/人工动作后再试，用户取消不得自动重试。传入实际输出路径，但不能把“有半文件”当成功或可恢复的唯一依据。

进程 runner 采用边读边写日志文件 + 有界内存环形尾部；记录总日志字节、是否截断、路径和诊断摘要。取消和超时终止整个子进程树并收集退出结果。保持参数数组调用，不引入 shell 命令字符串。

建议新增测试：无效滤镜最多产生一次终止/配置失败；磁盘不足经恢复动作后可重试；用户取消不重试；模拟大量 stderr 时内存尾部有上限且日志可追溯；超时能清理子进程。是否采用现有统一 runner，应先检查共享实现，避免再复制一套进程管理。

## 19. LDS-18｜旁白采样偏移参数无效，采样裁切选项错误

**优先级 P2。与 LDS-05 同文件，但为独立的时间接口缺陷。当前默认生产调用未必触发非零偏移。**

[build_mix_command][src-renderer] 的 `narration_start_sample` 没有进入实际滤镜/argv，只出现在描述中。实验 0 与 48000 得到同一滤镜图；48kHz 下请求偏移 1 秒，实际开头仍有声音。不能据此说“所有片子都错位”，但非零偏移能力当前无效。

FilterGraph 的采样裁切构建会生成 `atrim=sample_count=24000`。真实 FFmpeg 报选项无效；换成 `end_sample=24000` 的控制组成功。这是潜在被调用分支，不应与主链路故障混算通过率。

实现 sample-domain 的延迟/补齐/裁切，统一换算到明确的采样率；使用 FFmpeg 支持的 start_sample/end_sample 语义；最终按 manifest 的总样本数校验，而非只比较毫秒。旁白段落拼接和插入偏移须与视频时间轴共享同一时基转换规则。

测试 0/1/48000 样本偏移、不同输入采样率、起点恰在块边界、总样本数精确、首段静音存在且长度正确。生产尚不支持的偏移应拒绝并说明，不接受参数后忽略。

## 20. 实际实验记录与解释

### 20.1 核心实验（17 项断言）

`tests/run_probes.py` 的 17 项断言中，7 项控制/基线符合预期，10 项目标行为断言失败，失败用于复现问题。**不是仓库测试套件“7 通过、10 不通过”的统计。**

| 实验 ID | 观测 |
|---|---|
| FF-single | 单旁白真实 FFmpeg 控制成功 |
| FF-multiple / FF-multiple_bgm | 多旁白两类图均退出234 |
| CTRL-multiple-fix / CTRL-multiple_bgm-fix | 仅改标签后相同输入成功 |
| FF-offset | 48000样本偏移未改变图，开头音频不静音 |
| FF-atrim / 对照 | sample_count 无效；end_sample 可执行 |
| FF-chunk-offset | 第二块重复第一块帧内容 |
| 三种文本编码控制 | UTF-8/GB18030/UTF-16 纯文本正确 |
| DOCX 解码 | 文档段落没有被提取为正文 |
| 粘贴输入投影 | 正文没有进入持久化输入投影 |
| Audio 空/错状态 | pending 子查询遮住空/错误分支 |
| SQLite 分页 | 75条数据的第二次请求仍回同一前50条 |

以 `evidence/probe_results.json` 的实际 ID 与输出为准；以上部分行合并展示多个控制。生成音调和测试视频不代表 AI 配音/视频质量测试。

### 20.2 路由与表达式复核

`tests/route_probes.py` 前 5 项运行转录函数：重读无写入、渲染只有状态写入、导出确认参数被忽略、同键重复包、跨 edition render 可写入。后 5 项是读模型/回调/键/分类表达式演算，已标为 E3；不属于 FastAPI、React Query 或 SQLite 集成测试。

### 20.3 浏览器结果

4 组隔离案例：review、audio-empty、audio-ready、script-switch。6 张截图包含桌面、移动端以及输入丢失前后。浏览器没有 pageerror；这只说明测试宿主成功渲染，不说明产品不存在运行错误。

截图用真实浏览器渲染，不是设计稿。测试宿主移除了路由、QueryClient 和 AppShell，保留被审查的条件、相关 JSX/CSS；某些外围标签和数据映射简化为固定夹具。生产 React 19 与测试 React 18.2 的差异必须在正式回归时消除。

## 21. 给实施 AI 的分批提交计划

每批先加入“表达期望行为”的失败测试，再改实现；不要写断言去接受现有错误。以下测试文件名均为建议新增或归并位置，不是声称仓库已有这些文件。

| 批次 | 修改边界 | 关联问题 | 必交证据 |
|---|---|---|---|
| PR-A 合成器热修 | ffmpeg_renderer、建议 slices 纯函数、真实 FFmpeg 测试 | 05/06/18 | 原故障失败日志、修复后真实输出、帧/样本校验 |
| PR-B 创建与来源 | routes、projects 内部事务能力、production、来源解析入口、CreatePage阶段状态 | 02/03/04 | 幂等重放、原文重启读取、真实文档夹具 |
| PR-C 命令接线 | 建议 commands 服务、既有队列/outbox、现有 worker handlers | 01/13 | 真实API写入→dispatcher领取→产物，重启/迟到/取消测试 |
| PR-D 作用域与读模型 | repository scope helper、DTO/schema、客户端生成、QC查询键 | 07/08/10 | 跨项目拒绝、同render读写一致、当前take统计 |
| PR-E 真实审片 | Review/Audio播放器、manifest时间线、issue定位、导出下载 | 09/15 | 不用GPU的真实MP4/WAV UI录屏/截图 |
| PR-F 草稿与异步 | ScriptPage、OneClickPipelineWorkbench、draftRegistry适配 | 11/12 | deferred promise 竞态测试与刷新恢复 |
| PR-G 性能与视觉 | 游标/聚合查询、声音布局、runner错误与日志 | 14/16/17 | 查询计数、p50/p95、窄屏截图、日志内存边界 |

PR-A 可独立先合；PR-C 与 PR-D 契约先统一再接 PR-E；PR-F 可以并行，但不要修改公共 registry 的既有语义而不跑旧工作区回归。全部必须兼容 DRAMA 域；不能为了 EXPLAINER 打断旧分集/剪辑/交付链路。

## 22. 必须补的正式验收矩阵

本节是实施后的验收任务，**当前状态全部 NOT_RUN**，除非在第20节明确标成隔离实验。

| 层次 | 最小验收内容 | 不可接受的替代 |
|---|---|---|
| 数据迁移 | 旧库备份→升级→完整性与外键校验→孤儿/跨scope检查 | 仅新建空库通过 |
| 命令 | 真实API、真实事务、持久任务、真实dispatcher，断电式重启恢复 | mock submit 返回假job_id |
| 创建/来源 | 粘贴+DOCX→重启→仍有原文→预检/重试不新建 | 只验证201/字段长度 |
| 合成 | 真实FFmpeg，单/多段、跨块、NTSC时基、样本偏移 | 只看字符串包含concat |
| 版本/审批 | 播放、QC、决定、返工、导出都绑定同render/hash | 使用“最新”或??1兜底 |
| UI | React19完整AppShell/API，错误/空/运行/成功/过期全状态 | 仅隔离组件截图 |
| 草稿 | 跨段、跨页、刷新、在途保存、项目切换、409 | 只测输入后本页不丢 |
| 性能 | 1000作品、长稿、多take、多issue；查询次数与浏览器交互耗时 | 主观说“明显优化” |
| 产品闭环 | 输入→来源→讲稿→旁白→分镜→渲染→QC→人工确认→包下载 | “页面点到了最后一步” |
| 模型质量 | 用户实际本地模型与目标分辨率下生成、听审、看片 | 用测试音调和测试图案代替 |

正式 UI 截图至少留：1366×900、1920×1080、390×844；新建页阻塞/提交失败、声音空/错/可听/对齐失败、审片真实视频/第12条问题、草稿离开确认、发布包完成/失败。固定测试数据与字体环境，附 commit、路由、数据 seed、控制台错误和网络请求摘要。

## 23. 可直接交给 AI 的执行要求

```text
请基于本报告固定快照 f1251e1 与当前分支差异进行修复。
先复核每条问题是否仍存在，已被后续提交修复的不要重复修改。

优先级：
1. 修复虚假命令受理、完整创建幂等、正文持久化、真实文档解析。
2. 修复多旁白滤镜和跨块源时间映射，加入真实 FFmpeg 测试。
3. 统一 render/edition/project 作用域和 revision/subject DTO。
4. 接真实媒体播放器与审查定位，再修草稿异步竞态。
5. 修分页、查询数量、窄屏信息展示和日志/重试治理。

实现限制：
复用现有 jobs/automation_workflow/outbox、媒体、草稿与导入能力；
禁止另造第二套任务队列；禁止返回占位job_id；
禁止通过 mock 成功、静音、重复画面或临时假产物绕过验收；
不要修改既有人工批准/发布授权的权威边界；
不要把云模型调用或发布行为加入没有获得授权的自动步骤；
不要直接删除用户已有项目、媒体或历史版本。

每批交付：
问题编号、修改文件与函数、失败测试、修复、回归命令与原始结果、
真实截图/视频、迁移及回滚说明、未执行项。
E2摘录实验不能冒充整站测试，源表达式演算不能冒充真实API集成。
未具备GPU/正式账号时明确BLOCKED，不写成通过。
每次只提交职责清晰的一批，保留可独立回滚的提交。
```

## 24. 证据包使用说明

目录中的 `tests/run_probes.py`、`tests/route_probes.py` 会在复现现有异常时以非零状态退出，这是正常的失败断言，不是“运行脚本损坏”。运行依赖见 `README_证据与复现.md`。UI 截图脚本单独输出结果 JSON，需查看 `desired_behavior_pass`，不能仅凭脚本退出0判断业务通过。

`source_excerpts` 是人工转录/裁剪的审查摘录，不是完整 checkout；`ui_probe` 是隔离测试宿主，不是可替换产品前端。控制组的一行修正仅存在于实验中。对生产仓库应用任何修改前，必须对照固定版本原文件重新确认。

本报告刻意不写无证据的 GPU 吞吐、显存占用、全站响应速度、完整发布能力结论，也不把未深查模块列为无问题。当前应优先关闭的不是“代码看起来复杂”，而是上述可定位的业务断点。

[src-routes]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/api/routes/explainers.py
[src-production]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/explainers/production.py
[src-repo]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/infrastructure/database/explainer_repository.py
[src-projects]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/projects.py
[src-sources]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/explainers/sources.py
[src-renderer]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/infrastructure/composition/ffmpeg_renderer.py
[src-migration]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/alembic/versions/0102_explainer_factory_foundation.py
[src-schemas]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/api/schemas/explainers.py
[src-create]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/CreatePage.tsx
[src-review]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/ReviewPage.tsx
[src-audio]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/AudioPage.tsx
[src-script]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/ScriptPage.tsx
[src-hooks]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/useExplainerQueries.ts
[src-components]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/components.tsx
[src-css]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/explainers.css
[src-pipeline]: https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/pipeline/OneClickPipelineWorkbench.tsx
