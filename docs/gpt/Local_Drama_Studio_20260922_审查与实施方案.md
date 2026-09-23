# Local Drama Studio 当日代码审查与修复实施方案

审查日期：2026年9月22日，以北京时间计算当天提交。读者：项目负责人和后续负责修改代码的 AI 开发代理。

## 阅读导航

- [结论与使用方式](#结论与使用方式)
- [优先处理的问题索引](#优先处理的问题索引)
- [分批实施顺序](#分批实施顺序)
- [解说后端详细修复方案](#解说后端详细修复方案)
- [原稿导入和项目包详细修复方案](#原稿导入和项目包详细修复方案)
- [长稿流水线和任务状态详细修复方案](#长稿流水线和任务状态详细修复方案)
- [剪辑和媒体保真详细修复方案](#剪辑和媒体保真详细修复方案)
- [页面交互与可用性详细修复方案](#页面交互与可用性详细修复方案)
- [启动和请求边界详细修复方案](#启动和请求边界详细修复方案)
- [本机真实浏览器UAT与截图验收](#本机真实浏览器uat与截图验收)
- [实现代理的执行约定](#实现代理的执行约定)
- [配套证据包的目录和复跑说明](#配套证据包的目录和复跑说明)

## 结论与使用方式

当前版本存在会中断新解说生产、使草稿修改丢失、破坏项目包恢复，以及使音画时间线不一致的确定问题。优先处理原稿和草稿的数据保护、真正的任务提交与执行交接、长稿续接后的正式应用，再处理媒体保真、界面反馈和规模扩展。本文各问题给出触发条件、证据、源码位置、具体修改设计和验收方法；正常模块不另列优化项。

新解说工作台的主要风险是：预检与启动没有使用同一完整输入，工作流任务类型与解说执行器不兼容，部分重读／渲染／导出接口返回202却没有实际持久任务。单独改按钮文案、放宽校验、增加一个stage枚举或让模拟接口测试变绿，都不能完成这一组修复。验收应走到可领取的Job、正确handler、真实文件和终态记录。

### 审查版本

| 项目 | 固定值 |
| --- | --- |
| 仓库 | [Qioooba/local_drama_studio](https://github.com/Qioooba/local_drama_studio) |
| 审查 HEAD | `f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41` |
| 对比基线 | `a1cd6acce39621381a92ebbbfc25bf5b7955703d` |
| 提交范围 | [当日12个提交的完整比较](https://github.com/Qioooba/local_drama_studio/compare/a1cd6acce39621381a92ebbbfc25bf5b7955703d...f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41) |
| 最后提交时间 | 2026-09-22 22:28:08 +08:00 |
| 改动规模 | 295个文件，新增105,975行、删除12,109行；包括生成接口与文档 |
| 实施方式 | 本轮输出审查及实施文档；产品源码未修改、未推送提交 |

源码行号全部对应上述固定HEAD。后续实现先核对当前分支：若已经修复某项，应以复现和验收结果关闭它，不能直接覆盖后续改动。

### 已完成的测试及实际限制

本轮执行了真实FastAPI路由与服务、独立SQLite迁移和事务、项目包跨库往返、线程交错、FFmpeg真实编码解码和PCM采样、OpenTimelineIO官方解析器，以及原始React组件的jsdom交互。使用合成原稿、合成项目和合成媒体。需要AI输出的编排测试只替换模型端口，数据库和业务链路仍运行真实实现。

**没有完成你本机浏览器的真实页面点击或UI截图。** 三个入口的连接结果为：当前执行环境访问127.0.0.1的3210/5173端口被拒绝，192.168.1.120:3210超时；浏览器访问局域网入口也失败。重新构建同一提交后，隔离服务不能被当前浏览器访问，共享端口启动请求被自动审批拒绝。因此本报告不把组件测试写成端到端测试，也不对间距、对比度、叠层遮挡、拖拽或响应式布局作截图级结论。文末给出尚待执行的本机UAT步骤与截图命名。

未测真实GPU生成质量、Windows上的完整宿主进程管理以及真实剪映客户端导入。文中明确区分“正在使用的业务路径复现”“尚未接入的新模块直接实测”“源码确认”和“受控阈值实验”。这些边界影响结论范围，不影响已取得的接口、数据与媒体错误证据。

| 测试范围 | 实际结果 | 解释 |
| --- | --- | --- |
| 前端完整测试 | 754项：747通过、7失败 | 失败文件复跑剩6项；这6项在基线同样失败，不归因于今天 |
| 前端新增缺陷断言 | 10项按预期正确行为断言失败 | 8项解说交互、2项一键制作提交竞态，都是本次有意补充的回归测试 |
| 剪辑新增组件断言 | 2项失败 | 首份草稿无请求、刷新覆盖未保存时长 |
| 导入与项目包现有测试 | 142通过、0失败 | 另用真实跨库和并发场景补出本文缺陷 |
| 媒体现有测试 | 54通过 | 另用真实FFmpeg和OTIO解析验证时长、源区间与语义 |
| Jobs/Pipeline等现有测试 | 63项中59通过 | 3项缺实例manifest，1项临时SQLite异常；未算产品bug，fairness单独7项通过 |
| 启动相关现有测试 | 30通过 | 另验证含#路径、语义错误manifest、未登记Host读请求 |
| 解说后端缺陷复现器 | 10项确认成功 | 断言目标是确认错误现状，不是产品验收通过 |
| 模型图、许可与路由相关测试 | 59项直接通过；另6项提供合成实例清单后通过 | 初次6项因缺model_manifest失败；补齐测试配置后走原代码，不代表真实模型设备验收 |
| 前端生产构建 | tsc、Vite、bundle budget通过 | 构建通过不代表上述业务链已贯通 |

以上分域统计不相加成总体通过率；重跑、故障探针和不同证据方向不能混算。原始日志、JSON、复现脚本与测试小媒体在配套证据包中。

## 优先处理的问题索引

P1表示会阻断核心制作或造成内容、身份、状态错误，应在对应功能验收前修复；P2表示确定的边界、可用性、规模或保真问题。本文不设置未经证明的P0。编号保留各领域前缀，便于分派；同一个业务断点在前后端分别说明时已交叉引用，不应重复统计修复数量。

| 修复组 | 编号 | 首要后果 | 证据类型 |
| --- | --- | --- | --- |
| 真正启动解说生产 | EXP-01 | 合法预检仍409；继续往下会进入错误任务handler | 真实接口与任务执行 |
| 真正提交重读／渲染／导出 | EXP-02、FE-A03 | 202或成功提示却没有任务，长期停在RENDERING／BUILDING | 路由、表计数、组件 |
| 解说创建的复合事务与重试 | EXP-03、FE-A02 | 创建失败残留空壳，同请求重放400，修正后409 | 路由、数据库、组件 |
| 讲稿和剪辑的未保存内容保护 | EXP-04、TM-01、TM-02、FE-A01 | 旧稿覆盖新稿；首次草稿无法保存；刷新／旧提交回调抹掉新编辑 | 真实数据、组件 |
| 包内身份及完整恢复 | PKG-01、PKG-02 | 原稿媒体指向源项目；EXPLAINER恢复成DRAMA空壳 | 真实跨库往返 |
| 原稿读取与并发发布 | IMP-01、IMP-02、IMP-04 | EPUB乱序／截断；真正并发时失败请求删除赢家正文 | 真实解析、文件、线程和SQLite |
| 长稿分析和正式项目同步 | PR-01、PR-03、PR-04、PR-05、续接入口补充 | 重复EP；旧资产被覆盖；FAILED变假RUNNING；新集不能应用 | 真实编排和事务，模型口替身 |
| 恢复后的下一次执行 | PR-02 | 第二次实际成功仍CANCELLED | 真实Job状态机 |
| 成片归属和人工审阅身份 | EXP-05、FE-A05 | 包混项目关联；确认成片误用问题哈希 | 真实数据库、组件 |
| 一致的帧与采样计划 | TM-03、TM-04、TM-05 | 音画错位、尾音被裁、少帧仍VERIFIED、输出fps错误 | 真实FFmpeg与ffprobe |
| 交换格式与大包 | TM-06、PKG-03 | OTIO／剪映转场丢失却losses为空；大文件ZIP64失败 | 官方解析器、阈值实验 |
| 可操作的媒体工作台 | FE-A04、FE-A06至09、FE-A11 | 无播放／试听、错误不可见、永远loading、语言时钟混用、问题与证据不可达 | 组件及源码确认 |
| 定时、规模与预算 | EXP-06至08、FE-A10 | 定时签名报错；100条后不可发现；显式0／[]变默认值 | 真实服务、列表请求、配置快照 |
| 新composition执行器接入前修复 | TM-07至10 | 跨块重复源开头、音频当视频、标签错误、采样偏移失效 | 未接线模块直接FFmpeg实测 |
| 启动和请求边界 | S1、S2、S3 | 合法路径误报不可用、可选清单拖垮API、条件性Host读缺口 | 真实启动与服务器请求层 |

## 分批实施顺序

### 第一批 保护已有内容和复制身份

处理EXP-03、EXP-04、IMP-04、PKG-01、PKG-02、TM-01、TM-02、FE-A01。将完整创建收据、当前修订头CAS、唯一临时文件和包内引用重写作为共同基础。修复前的错误数据要有可识别的修复预览：列出孤立解说项目、跨项目媒体引用、缺失extracted文件、错误产品类型包；没有原始来源时不得猜测还原。迁移与数据修复分别提交，已有数据先备份，再对合成旧状态做恢复演练。

这一批验收必须实际读取复制后的原稿、跨标签保存两段、保存首份时间线、在请求挂起时继续输入，并检查最终数据库／草稿内容，而不是只检查函数返回值。

### 第二批 接通完整任务链并校验交付归属

处理EXP-01、EXP-02、EXP-05、EXP-06、EXP-08、FE-A03、FE-A05。统一API提交→持久命令回执→Job→正确handler→产物→状态投影。每一个202都能查到真实执行ID，每一份READY／VERIFIED都有受校验的文件与归属。渲染、导出恢复可执行前，先完成成片归属检查；新的composition执行器接线前同时完成TM-07至10。

验收先使用无需GPU的固定原稿和小媒体，证明整条有限工作流能够完成、失败、取消、重启和幂等重放。真实模型设备验证随后补上，不能用模拟图片作为生成质量验收。

### 第三批 完成长稿续接和任务恢复

处理PR-01至06，并接通前端继续分析入口。将分析窗口与正式分集分开、保留带来源的累计资产、按草案修订维护应用水位。Job旧停止意图只能在对应尝试结算时消费；Outbox每次claim必须有独立所有权身份。先明确模型分析完成与正式项目应用完成是两个水位，再设计界面进度。

验收覆盖61／121窗口、单章跨多个窗口、首批先应用、第二批失败重试、任务pause→resume→下一次产物登记→成功完成。不要只断言“已重新入队”。

### 第四批 统一媒体计划和真实保真验证

处理TM-03至06、PKG-03。冻结唯一有理数帧率、每个镜头输出区间、源入出点、转场重叠帧、音频采样区间；编码器与交换格式writer都读取该计划。VERIFIED应验证解码帧／有效音频和目标计划，不能只凭FFmpeg退出0。OTIO用官方schema及解析器验证；剪映只声明已经在目标客户端验证过的能力，未表达的效果准确进入loss report。

### 第五批 补齐可操作页面和规模边界

处理FE-A04、FE-A06至11、EXP-07及S1至S3。优先让用户能看候选、听旁白、看成片、读错误、找到全部问题和来源，然后验收分页、键盘操作、缩放和布局。像素级问题必须拿到真实页面截图后再开单，不能从CSS猜测。Host缺口按文中前置条件处理，不扩大为已发生的数据泄露。



## 解说后端详细修复方案

证据目录：`evidence/explainer_backend/`。01至10的JSON为隔离接口、任务及数据库结果；上游产物夹具和能力探针替身在复现脚本中明确标注。

### EXP-01：生产主链有两个连续阻断

**定位**：[apps/api/local_drama/api/routes/explainers.py:881-892](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/api/routes/explainers.py#L881)；[apps/api/local_drama/application/explainers/production.py:1080-1102,1147-1150](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/explainers/production.py#L1080)；[apps/api/local_drama/application/automation_workflows.py:833-859](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/automation_workflows.py#L833)；[apps/api/local_drama/application/worker_handlers/automation_task.py:716-722](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/worker_handlers/automation_task.py#L716)。

**实际复现**：POST 创建原创解说作品；POST plans:preflight，传一个合法 output；预检返回 executable=true。随后将返回 hash 和同一个 output 原样 POST runs，使用默认 start_workflow=true，得到 HTTP 409 / STALE_PLAN，错误 details.submitted_outputs=[]。此时 explainer_runs=0，jobs=0。新预检多少次都无法消除此由服务端丢参引起的错误。

另一个独立用例绕过上面接口，直接让真实 ExplainerProductionService.ensure_workflow 建图，再用真实 AutomationWorkflowService 启动。生成的首 Job 类型为 AUTOMATION_WORKFLOW_TASK，item 为 `{key:explainer:RESEARCH_ACQUIRE,payload:{step_code:RESEARCH_ACQUIRE,output_kind:RESEARCH_PACKET}}`。将它交给实际 handler，立即得到 AUTOMATION_TASK_PAYLOAD_INVALID / “自动化任务 payload 缺少 action”。解说没有 episode_id，因此给 payload 随便补一个 action 也无法解决领域不兼容。

**根因**：第一层 start_run 的签名和调用只传 project/hash/key，漏掉 outputs、budget、fallback_policy；submit_run 重新预检时用空 outputs，hash 必定改变。第二层复用的自动化工作流固定生产短剧任务，解说图没有和 EXPLAINER_TASK/NARRATION_TTS/NARRATION_ALIGN 等已登记业务 handler 建立真实映射。

**实施**：

1. `start_run` 接收并向 `submit_run` 原样传递规范化后的 outputs/budget/fallback_policy；手动与定时启动都使用一个完整的 frozen plan 输入对象。更稳妥的后续设计是持久化预检 plan_id/hash/规范化请求，提交仅引用该计划，服务端核对源修订身份。
2. 在既有 Workflow→Job 交接处增加受控的产品任务解析器，按 template_code/产品类型生成真实任务类型；不要在业务服务外添加另一套自建队列。
3. Job snapshot 固定 project_id、video_id、explainer_run_id、step_binding_id、step_code、冻结 plan/input/capability 身份及已确定上游产物 ID。缺字段的任务在入队之前拒绝。
4. 为计划中的 identity、visual generation、subtitle、composition render、export 逐项登记真实调用；只登记 stage definition 或把多个 stage alias 指向同一 storyboard handler 不足以证明整条链可执行。
5. 任务完成、依赖释放和 explainer step 投影从 Job 的实际终态推进；取消、失败和人工停顿遵循同一执行权威。workflow 启动失败后的幂等重试需能补完交接，不应因为 explainer_run 已存在就提前返回一个永远未启动的 run。

**验收**：全能力可用时预检→默认启动返回202与持久 run/job ID；第一份任务可由正确 handler 接受，snapshot 不需要伪造 episode；用确定性本地端口跑完整有限任务图，最终产物关系完整；同 key 返回同 run；worker 重启、取消与失败不能产生伪成功或无归属任务。

### EXP-02：按钮收到202，实际没有后续执行者

**定位**：[apps/api/local_drama/api/routes/explainers.py:1255-1282](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/api/routes/explainers.py#L1255)（重读）；`1360-1427`（渲染，关键行1418-1426）；`1630-1662`（导出）。

**实际复现**：在真实数据库准备一个已冻结脚本及 composition 后，请求 narration:resynthesize 返回202；jobs、narration_takes、composition_renders、command_idempotencies、outbox_events 的数量全部不变。再请求 renders `{confirm:true}` 返回202、status=SUBMITTED、would_create_jobs=true；仍无 job、无 composition_render、无 outbox 增量，唯一持久变化是 edition.status=RENDERING。导出接口同样无 job/outbox，只插入 publication_packages.status=BUILDING，其 rel_path/zip_sha256/ready_at 为空。

**影响**：界面可以提示“重读已登记”“渲染已提交”，但工作不会发生；用户等待或重复点击均无效，并留下永远运行中的状态。前端轮询优化不能弥补缺失的执行交接。

**实施**：抽出显式提交服务 `submit_narration_resynthesis`、`submit_composition_render`、`submit_publication_build`，在同一短事务中校验归属与冻结产物、持久 Job/目标产物/幂等回执，再返回真实 job_id/render_id/package_id。实际长计算交给现有 worker。重读严格选 edition.frozen_script_revision_id + locale + canonical_segment_id，避免现有 segment_by_canonical 只按 video/canonical/ordinal 取到别的语言或旧修订。渲染通过 composition 验证服务冻结清单，再创建 PENDING render，worker 领取后才转 RUNNING/RENDERING；导出让 worker 调用真实 delivery builder，完成后写路径、hash、files 与 READY。接口要用明确的 accepted=false/BLOCKED 结构或非成功状态表示缺前置条件；前端只有收到有效执行ID才提示提交成功。

**验收**：每次新 key 至多产生一个可领取任务；重复 key 返回同一任务与产物；无可执行任务不得持久 RENDERING/BUILDING；上游无效时数据库零增量。重读某段必须最终得到新 take→alignment→subtitle/composition 失效闭包；本地确定性渲染夹具产生可校验文件后才允许 READY；失败、取消和重启有可恢复或可终止状态。

### EXP-03：创建原子性与创建幂等回放均失效

**定位**：[apps/api/local_drama/api/routes/explainers.py:268-309](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/api/routes/explainers.py#L268)；[apps/api/local_drama/application/projects.py:262-312](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/projects.py#L262)；[apps/api/local_drama/application/explainers/production.py:353-396](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/explainers/production.py#L353)。

**实际复现一**：成功创建一次返回201。相同请求和相同 Idempotency-Key 再请求，返回400 INVALID_REQUEST / “一个解说项目默认对应一个解说作品”，不是已创建结果。项目和 video 数量均为1，证明项目层确实重放了，但路由又重复调用 create_video。

**实际复现二**：创建时传不存在 channel_profile_id，返回404，但 projects=1、explainer_videos=0、command_idempotencies=1，项目目录也已留下。将 profile 字段改正后用原 key 重试，返回409 IDEMPOTENCY_KEY_CONFLICT。此时用户既没有可用解说作品，也无法按原请求正常重试。

**根因**：路由注释声称“一事务”，实际 ProjectService.create_project 自行提交项目/目录/幂等收据，然后另一事务执行 create_video。第二事务失败不回滚前者。重放只覆盖项目命令，并未覆盖整个解说创建命令。

**实施**：新增 `ExplainerCreationService.create_workspace` 作为唯一复合命令。先完成所有可读验证，在同一 Database.transaction 内写 project、video、初始输入引用、输出配置和作用域为 explainer:create 的完整收据；将 ProjectService 的写入能力改成可接受调用方 connection 的内部接口，复用其目录归属标记与失败清理。收据保存 project_id/video_id 与规范化完整 payload digest，重放直接返回原结果。目录分阶段创建，失败只移除本次 command 拥有的临时目录，禁止删除用户原目录。不要只在路由里判断 video 存在就返回成功，这会掩盖异请求复用同 key。

**验收**：同 key 同体始终返回相同 project_id/video_id；同 key 异体409且不写数据；在 video 验证/写入处注入失败，项目、video、收据、审计/outbox及本次目录均无残留；两个并发相同命令只创建一份复合资源。前端正文变化导致 key 是否应变化属于另一根因，应与后端回放修复分别验证。

### EXP-04：讲稿的 expected_revision 没有阻止旧稿覆盖新稿

**定位**：[apps/api/local_drama/application/explainers/narration.py:453-475,496-603](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/explainers/narration.py#L453)；路由 [apps/api/local_drama/api/routes/explainers.py:783-807](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/api/routes/explainers.py#L783)。

**实际复现**：原稿有 s1=“第一段旧内容。”、s2=“第二段旧内容。”。标签页A基于原稿修改 s1=“第一段已修正。”，返回200并创建新稿。标签页B仍使用原稿的 segment_id、expected_revision、expected_script_revision_id，修改 s2=“第二段已修正。”，也返回200。最终 current_script 中 s1 恢复“第一段旧内容。”，A刚保存的修改从当前稿丢失。

**根因**：代码只检查待修改的旧 segment 是否仍属于传入旧稿，以及该旧 segment.revision 是否匹配。旧稿/旧段本来不可变，所以这些条件永远满足。创建新稿后，又无条件把 video.current_script_revision_id 覆盖到新分支。缺少对当前稿指针的并发控制。

**实施**：以 `video_id+locale` 当前脚本指针作为修订头；patch 开始核对该指针等于 expected_script_revision_id，最后用 compare-and-swap 条件更新指针，受影响行数不为1时回滚整个新稿创建。最好把同语言的当前修订头显式建模，避免多语输出互相覆盖。冲突返回409 STALE_REVISION 并给 current_script_revision_id。若要支持自动合并，必须比较被改段的旧hash与当前hash，基于最新稿复制后仅替换该段，绝不能直接复制旧稿再覆盖新头。

**验收**：复现中的第二次旧请求必须409，或经过显式可验证的三方合并后同时保存两段；新修订、段落、dependency失效写入与头指针保持一个事务；重复/并发提交不留下未引用的大量分支。前端收到409保留用户输入，展示差异并允许重新应用。

### EXP-05：交付包的归属与幂等约束缺失

**定位**：[apps/api/local_drama/api/routes/explainers.py:1613-1638,1654-1658](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/api/routes/explainers.py#L1613)。

**实际复现**：创建A、B两个项目及各自 edition。在 A edition 的 exports 请求中传 B 项目的 render_id，返回202；publication_packages 记录的 project_id/video_id/edition_id 属于A，render_id/composition_revision_id 属于B。两个相同请求使用相同 key，得到两个不同 package ID，数据库包数量增加2，jobs没有增加。

**根因**：显式 render_id 通过 repo.find 取出后，没有验证 render.edition_id/video_id/project_id 和当前 edition/video 一致。单列外键只能证明各对象存在，不能证明它们属于同一工作空间。idempotency_key 只回显，没有收据或唯一约束。

**实施**：新增 `require_edition_render(edition_id,render_id)`，JOIN校验完整归属、composition关联、manifest hash、根成片种类与 status/integrity；同一校验供预检、导出、人工决策使用。缺失/跨项目拒绝且不得写入。添加 `explainer-export:{edition_id}` 命令scope和包含render_id/hash、目标地区、字幕/stems设置的payload digest；包与Job与收据同事务创建。交付worker再次核对冻结的授权/媒体身份，防止仅依赖创建时页面状态。

**验收**：跨项目和跨edition render请求返回400/409且零写入；同key同体只有1包和1Job；同key异体409；损坏、未完成、非根渲染不可进入READY。该问题当前实际写入的是错误关联的BUILDING包，未声称已经导出了错误文件或发生真实对外发布。

### EXP-06：栏目级定时创建的真实服务签名不匹配

**定位**：[apps/api/local_drama/application/explainers/runtime_adapters.py:800-833](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/explainers/runtime_adapters.py#L800)，关键 `812-813`；真实 [apps/api/local_drama/application/projects.py:171-197](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/projects.py#L171)。

**实际复现**：调用栏目级 `_create_scheduled_project`，使用真实 ProjectService、临时SQLite、合法题材和outputs夹具，直接抛 `TypeError: ProjectService.create_project() got an unexpected keyword argument 'channel_profile_id'`。参数绑定阶段即失败，尚未进入项目写入，也没有接触模型。

**根因**：channel profile 属于 explainer video 创建参数，却传入了旧 ProjectService；随后的 `create_video` 反而未传入这两个profile参数。宽泛的测试假实现若使用 `**kwargs` 不会发现该问题。

**实施**：定时任务复用EXP-03的复合创建服务，profile绑定传到create_video并校验版本所属关系。使用 occurrence_id 作为创建命令幂等身份，创建、绑定 occurrence.project_id、存储输出设置应可恢复；失败要写结构化 occurrence reason并正确释放租约，而不是留下后台TypeError。工作流交接随后必须遵循EXP-01修正后的路径。

**验收**：使用真实ProjectService签名的schedule集成测试，首次触发创建1个正确绑定profile的工作空间；同 occurrence 重放0重复；来源/能力不够时是可展示的明确阻断，异常重启后租约和任务状态可恢复。

### EXP-07：100条以外的工厂项目无法翻页取回

**定位**：[apps/api/local_drama/api/routes/explainers.py:186-190,200-213,231-235](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/api/routes/explainers.py#L186)。与前端 FactoryPage 的 limit=100、本地过滤共同构成用户可见缺陷。

**实际复现**：临时数据库插入101个解说项目；GET explainers?limit=100 返回100条、next_cursor=null。将第100条 project_id 作为 cursor 再请求，仍原样返回同100个ID。缺少的那条无法通过当前分页找回。

**根因**：路由声明 cursor 却未在SQL使用，next_cursor固定None。页面局部搜索只对取回集合过滤；旧作品不是单纯“翻页比较慢”，而是无法找到。

**实施**：使用稳定键集分页 `(updated_at DESC,id ASC)`，cursor编码时间和ID，SQL读取limit+1推导has_more/next_cursor；搜索/状态/类型过滤在服务端执行，并绑定于cursor的过滤摘要。将 edition_count/open_issue_count 改成按本页video_id的聚合查询，避免每条2次查询；COUNT(*)代替取出全部issue ID再len。前端用useInfiniteQuery或清晰分页，保留搜索、筛选与页面位置。

**验收**：101/1000条数据下逐页无重复无遗漏；页首时间相同也正确；搜索第101条能返回；状态过滤与游标组合稳定；测量每页固定数目的SQL，确认不随返回条数增长为2N+1。

### EXP-08：0和空数组被当作“未传”

**定位**：[apps/api/local_drama/application/explainers/production.py:600-613](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/explainers/production.py#L600)。

**实际复现**：预检提交 max_creative_repairs_per_beat=0、max_technical_retries_per_step=0，返回冻结预算均为2；提交 allowed_visual_fallbacks=[]，返回允许I2V_TO_MOTION_STILL、I2V_TO_INFORMATION_GRAPHIC。当前未实际运行重试，证据是授权快照已被扩大。

**实施**：只对字段缺失/None应用默认值，不用`value or default`；0/[]合法值原样保存并参与plan hash。预算用强类型请求schema校验，禁止字符串/布尔混入数字；fallback空数组应表示不允许回退。运行时预算消耗读取同一冻结值。

**验收**：缺失字段用默认、显式0保留0、[]保留空；0预算首次修复/重试立即被BudgetLedger拒绝；同一输入的预检与提交得到一致预算和hash。

## 原稿导入和项目包详细修复方案

证据目录：`evidence/imports_packages/`。

### IMP-04：失败的并发导入会删除已成功导入的正文

**定位**：[apps/api/local_drama/application/documents.py](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/documents.py) 第 424–441 行（事务外查重）、486–491 行（两个请求计算相同路径）、530–584 行（文件发布、写库与失败清理），以及 911–915 行（所有请求使用相同 `.partial` 名称）。最小修复关注 581–584 行。

**触发场景**：在多线程服务调用或多 API worker 等允许真正并行执行 import_document 的环境中，同一项目并发导入同名同内容文件。即使媒体层已完成原文件去重，两个请求仍可能同时在 source_documents 查询中读到“尚无版本”。它们分别创建不同 UUID，却使用相同 source code 与相同 extracted 文件名。单个进程的同步阻塞上传路由可能把两个普通网页上传串行处理，所以本次没有把服务层并发实验表述成“默认单 worker UI 双击必现”。

**实际复现**：`reproduce_concurrency.py` 使用两个真实线程、真实文件发布与真实 SQLite 事务，只用同步屏障固定“两个请求均读到无版本 → 两个请求均写完解析文件 → 竞争写库”这一合法交错。没有伪造文件/数据库返回值。单独串行化临时文件写入，是为了避免相同 `.partial` 文件的另一个重命名竞争掩盖本问题。

```json
{
  "successful_requests": 1,
  "failed_requests": 1,
  "loser_error": "UNIQUE constraint failed: source_documents.project_id, source_documents.code",
  "winner_session_status": "PREVIEW_READY",
  "winner_extracted_text_exists": false,
  "winner_get_paragraphs_error": "IMPORT_EXTRACTED_TEXT_CHANGED"
}
```

**根因**：`Database.transaction()` 已经使用 `BEGIN IMMEDIATE`，但去重读取与版本号分配发生在事务之前。输掉竞争的请求进入 `except Exception` 后，直接 `text_path.unlink()`；这个路径已经被成功事务引用。数据库回滚无法恢复被删除的文件。成功响应也不能证明原稿仍可用。

**实施方案**：

1. 将“查找同 project/raw hash/parser/layout/text hash 的 authority、决定重用/新建、分配 version_no”放入同一个短写事务。文本解析与大文件哈希仍在事务外完成；进入事务后必须重新检查已经提交的版本，不能沿用 424 行的旧 rows。
2. 用每请求唯一临时名，例如 `.<digest>.<uuid>.partial`。永久 extracted 文件是内容寻址的共享对象，任何失败请求只能删除自己的临时文件，不能直接删除永久路径。
3. 对解析世代建立明确去重键，建议持久化 `parse_identity`，以 `(source_document_id, raw_sha256, parser_version, structure_version, layout, text_sha256)` 唯一约束或等价规范键保证多进程一致性。发生唯一键竞争后重新读取赢家，返回 `reused=true`。
4. 采用“写临时文件 → 验证 → 短事务内确认赢家/发布 immutable 文件/插入引用”的顺序。数据库失败时允许留下可回收的未引用 immutable 文件；由独立清理逻辑在确认没有任何已提交引用后回收，不能以请求失败作为删除依据。
5. 现有丢失文本可在用户重导原文件、原始 hash 和解析世代一致时恢复；不能无条件把会话状态改回成功而不恢复正文。

**验收**：两个以及至少八个请求同步导入同一合成文件；最终只有一个有效解析 authority，所有响应为成功/重用或结构化可重试冲突；所有成功会话的全文 hash、段落分页、正文 commit 都正常。故意令第二个事务失败后，第一会话文件仍存在且内容不变。跨进程执行同样场景。另覆盖 `.partial` 重命名竞争、写盘失败、数据库提交失败。

### PKG-01：原稿往返只重写关系字段，漏重写 metadata 的媒体身份

**定位**：[apps/api/local_drama/application/project_packages.py](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/project_packages.py) 第 1315–1332 行为媒体分配新 UUID；1575–1577 行为原稿及会话分配新 UUID；1602–1609 行插入原稿版本时直接序列化原始 `metadata`。消费端 [apps/api/local_drama/application/documents.py](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/documents.py) 第 743–747、761–765、833 行以及 1143–1145 行分别用这个 ID 恢复最近原稿、构造下载入口、校验正文提交。

**触发场景**：项目有一份通过正常 `import_document → commit` 建立的原稿，导出项目包后“导入为副本”；在同机和迁移至新安装环境时都存在问题。

**实际复现**：同库和全新数据库分别使用真实 `export → stage_from_inbox → import_as_copy`：

| 场景 | 实际结果 | 用户后果 |
|---|---|---|
| 原项目仍在同库 | 副本“最近原稿”返回原项目的 media_version_id；SQL 确认下载媒体 owner 是原项目 | 副本仍依赖源项目；原项目媒体状态、文件变化会影响副本的打开/校验 |
| 项目包导入全新库 | import_as_copy 返回 `IMPORTED`；读取 latest_for_project 抛 `MEDIA_VERSION_NOT_FOUND` | 文件明明已复制，最近原稿和后续原稿工作流仍失败 |

**根因**：`source_document_versions.metadata_json.media_version_id` 是实际运行必需的外部引用，并非可原样复制的普通说明文字。现有 roundtrip 测试通过手写 SQL seed 原稿 metadata，未携带真实导入会生成的 media_version_id，因此只检查表行数和 hash 能通过，未验证真正的恢复工作流。

**实施方案**：

1. 在插入 source_document_versions 前，显式解析 metadata schema；取 `old_media_id`，要求它存在于 `media_version_map`，写入对应的新 ID。若引用缺失，预检返回 `PROJECT_PACKAGE_MANUSCRIPT_REFERENCE_INVALID` 并指出具体 version，禁止返回无缺项的成功。
2. 把其他 JSON 中有业务意义的 ID 纳入显式引用清单。例如 `preview.derived_from_import_session_id` 要经 `import_session_map` 重写；保留原 ID 作为审计来源时应放进明确命名的 `source_*` provenance 字段。不要对整个 JSON 进行盲目的字符串替换。
3. 在预检和导入前完整验证原文件与 `extracted_text_rel` 文件都在 manifest 中，且 text_sha256 匹配。对旧包缺少可恢复的媒体绑定，提供结构化缺项，而不是凭文件名猜测。
4. 导入事务提交前检查所有运行态引用属于副本项目；提交后对副本 `latest_for_project`、`get_paragraphs` 和原稿下载做恢复检查，并按新 source_document_id 重建 FTS 索引。
5. 新增由真实 DocumentImportService 建立 fixture 的 roundtrip 测试，保留现有手工 seed 测试用于旧 schema 兼容，但不能仅依赖后者。

**验收**：真实 TXT/EPUB 导入并 commit 后，同库复制与跨库复制均能打开“最近原稿”、下载原文件、分页读取正文并提交/派生范围；副本所有返回 media IDs 的项目归属必须是副本。删除/隔离合成源项目的文件后副本仍工作。故意移除元数据指向的媒体项，预检应明确失败且不留下半个副本。

### PKG-02：新产品类型没有纳入项目包协议

**定位**：[apps/api/local_drama/application/project_packages.py](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/project_packages.py) 第 279–280 行导出项目字段不包含 product_kind；第 409–429 行状态清单未包含 explainer 领域；第 1228–1236 行导入项目 INSERT 也不指定 product_kind。[apps/api/local_drama/application/projects.py](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/projects.py) 第 196–210 行已支持独立 EXPLAINER 项目，它不创建季/集；因此不能用原 DRAMA 包清单代表它。

**实际复现**：用真实 ProjectService 创建 `product_kind=EXPLAINER`、0 季 0 集项目，再用真实 ExplainerProductionService.create_video 建立一个解说作品，无模型调用；导出并导入全新数据库：

```json
{
  "source_product_kind": "EXPLAINER",
  "copied_product_kind": "DRAMA",
  "source_explainer_videos": 1,
  "copied_explainer_videos": 0,
  "copy_status": "IMPORTED",
  "missing_fields": [],
  "state_includes_product_kind": false
}
```

**影响**：备份/搬家结果是没有季集的 DRAMA 空壳，原解说作品的主题、输入设置及整个后续业务数据没有进入可恢复状态。将来在源项目发生损坏后，单凭这个“导出成功”的包无法恢复解说作品。此结论来自服务层真实导入链路，没有声称已在 UI 点击复现这个入口。

**实施方案**：

1. 立即在 `export()` 与 `inspect_path()` 检查 product_kind/支持的 domain 集合；在 explainer 序列化尚未实现前，返回明确的 `PROJECT_PACKAGE_PRODUCT_UNSUPPORTED`，阻止产出会被误认为完整备份的包。界面同样展示原因，不能只有后端报错。
2. 升级 state 协议，顶层 project 必须携带 `product_kind`，按 DRAMA/EXPLAINER 使用区分类型的 schema。旧 v2/v3 包仅在明确是旧短剧来源时缺省为 DRAMA；新包缺 product_kind 是格式错误，不能静默缺省。
3. 将巨型 `_state()`/`import_as_copy()` 的领域清单抽成 `DramaPackageStateAdapter` 与 `ExplainerPackageStateAdapter`。共用文件 manifest、媒体映射、哈希及事务；各领域声明自己的表、外键和 JSON 引用重写规则。
4. Explainer adapter 至少覆盖 `explainer_videos`、引用到的 `channel_profiles/channel_profile_versions`、`explainer_research_packets/sources/source_spans/claims/events/entities`、`explainer_script_revisions/chapters`、`narration_segments`、`explainer_visual_beats`、`explainer_editions`、`narration_takes/narration_alignment_revisions`、`explainer_subtitle_revisions`、`composition_revisions/items` 及其依赖媒体。对 renders/deliveries/QC/decisions 明确“保留历史事实、复制后需重新验证”的策略；运行中 jobs/runs/授权状态不能因复制而自动重新执行。
5. 新增 `included_domains/excluded_domains/incomplete_domains` 和记录计数的显式报告。存在业务数据但 export adapter 不支持时，应阻止“完整项目包”导出，或提供名称明确且用户能确认的部分导出模式。不能只返回空 missing_fields。
6. 复制时按拓扑顺序重写 video、edition、revision、segment、beat、composition 以及所有运行必需的 JSON ID；所有当前版本指针最后绑定，事务末尾做引用完整性检查。

**验收**：构造一个有主题、资料、脚本、旁白、视觉节拍和至少两个 edition 的合成 EXPLAINER 项目，跨库往返后 product_kind、关键字段、顺序及媒体 hash 完整；新项目没有假的季/集，解说工作区能够读取原业务内容；不会自动重跑任务/恢复发布授权。对每个仍排除的非空领域，预检有准确的非空缺项清单。

### IMP-01：EPUB 混合内容遍历顺序错误

**定位**：[apps/api/local_drama/application/documents.py](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/documents.py) 第 226–251 行，尤其 242–243、247–251 行。

**最小输入**：

```xml
<html><body><p>我<span>让<b>小王</b>去找<i>老李</i></span>回家。</p></body></html>
```

应提取 `我让小王去找老李回家。`，当前函数实际返回 `我让回家。老李小王去找`。这是有效 XML/XHTML 的普通行内加粗/斜体嵌套，不需要畸形文档。另一个最小例子 `A<span>B<em>C</em>D</span>E` 实际变成 `ABECD`。

**根因**：使用 LIFO 栈遍历时，仅 block 分支 reverse children，inline 分支直接正序入栈，子节点次序会倒置。`node.tail` 又在所有子节点遍历前加入 inline_text，使本应出现在父节点结束之后的正文提前。这样虽然避免了全局重复文本，但破坏了 XML 的 text → child → tail 阅读顺序。

**实施方案**：用显式 ENTER/TEXT/TAIL/EXIT 事件栈保持阅读顺序，block enter/exit 负责段落边界，行内节点只串接其文本。每个子节点按原序调度完整子树与 tail；采用反序压栈，确保出栈结果遵守文档序。跳过 head/style/script 的内容时，也应保留元素外部的合法 tail。继续保留真实重复句子，不能用全局去重掩盖遍历错误。将 SOURCE_PARSER_VERSION 升级，保留旧解析文本与 offset；用户重导后生成新世代，不原地覆盖旧会话的证据。

**验收**：至少覆盖两层/三层 span/em/b/a 嵌套、行内多兄弟、父块前后直接 text、li 内 p、真实重复句、中文英文及标点、br。逐字符比较逻辑文本顺序，并验证 paragraph offsets 截取回来的内容一致。真实 EPUB 上传后的 preview、存储正文与后续 breakdown 输入应一致。

### IMP-02：达到节点上限后被截断的正文仍作为完整原稿

**定位**：[apps/api/local_drama/application/documents.py](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/documents.py) 第 47 行 `_EPUB_TRAVERSAL_LIMIT = 20_000`、228–230 行 `break`、345–350 行拼接结果；415–416、509、554 行只要非空就写入正常预览。

**实际复现**：生成一个正常 EPUB，spine 包含单个 XHTML，body 内有 21,000 个短 `<p>`。通过真实 DocumentImportService 导入，返回 `PREVIEW_READY` 和 paragraph_count=19,998，最后一段是“段落19998”，之后的 1,002 段均未进入 extracted 文本；没有解析截断 warning。数量少 2 是因为 html/body 也占用了遍历计数。这不是界面只显示前 20 段的预览截断，而是存储的权威正文已经不完整。

**实施方案**：

1. 达到资源限制时返回明确 `DOCUMENT_STRUCTURE_TOO_COMPLEX`，包含当前 spine 文件、已访问节点和限制；在任何 media/source/session 持久化之前终止。不能 `break` 后返回非空成功。
2. 若要支持长篇小说，应使用流式 XML 解析和有界事件栈，把“复杂度/字节数/嵌套深度”作为独立上限；平面 21,000 个段落不应等同于异常深度。
3. 如果产品确实要允许部分导入，必须有单独的 `PARTIAL_PARSE` 状态、明示范围和缺失章节、独立人工确认；不得与完整 source authority 共用成功语义。

**验收**：上限前、刚达到、超过 1 个、21,000 段以及多个 spine 文件分别测试。结果只允许“全文完整、段数和末尾 sentinel 正确”，或“明确拒绝且不留下可提交的 ready 会话”。测试必须检查 extracted 全文尾部，而不是只检查预览前 20 段。

### PKG-03：流式项目包导出丢了 ZIP64 自动判定

**定位**：[apps/api/local_drama/application/project_packages.py](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/project_packages.py) 第 104–115 行，尤其 114 行 `archive.open(info, "w")`。此次从 `ZipFile.write()` 改成 `_write_path()` 后出现。

**机制与实验边界**：新建的 ZipInfo 未设置 file_size，`archive.open` 因不知道源文件会超过 ZIP64 上限而写普通头。即使外层 ZipFile 设置了 `allowZip64=True`，关闭写入流时仍可能抛 `RuntimeError: File size too large, try using force_zip64`。当前 Python 默认 ZIP64_LIMIT 为 2,147,483,647 字节。为了避免无意义地占用数 GiB，实验将 zipfile.ZIP64_LIMIT 缩小到 1,024 字节，并对 4,096 字节真实文件执行相同分支：新 `_write_path()` 报上述异常；旧 `archive.write()` 与 `force_zip64=True` 均成功。这是阈值缩小实验，未宣称实际写出了 2 GiB 测试视频。

**影响**：项目内有较大成片、中间视频或其他单个大文件时，完整项目包导出失败。协议允许高达 2 TiB 的展开总量，实际实现却在一个常见视频文件量级提前失败。

**实施方案**：保持固定 timestamp/权限的确定性输出，将源 stat 大小赋给 `info.file_size`，并使用 `archive.open(info, "w", force_zip64=needs_zip64)`；更简单的实现是统一 `force_zip64=True`，让小文件也使用合法 ZIP64 扩展，随后验证兼容性和确定性。保留现有导出期间源变更检查、流式复制与 `.partial` 清理。

**验收**：阈值缩小回归验证“不超过/刚超过”两侧、新函数与 readback digest；在具备磁盘空间的发布环境额外运行真实大于 2 GiB 单文件 roundtrip，并核对 file_size/SHA-256。固定内容两次导出仍返回同样 identity，第二次 reused。

## 长稿流水线和任务状态详细修复方案

证据目录：`evidence/pipeline_runtime/`。多批次实验使用确定性AI端口替身；部分用例将批次上限60暂设为1，用两章触发同一分支。真实运行Job、窗口、版本、SQLite与apply，不验证模型质量。

### PR-01 / P1：长章节切成多个输入窗口后，直接产生重复 EP 编号，应用草案触发唯一约束

**归属：今日新增窗口机制引入的回归。**

**代码位置**：[apps/api/local_drama/application/pipeline_orchestrator.py:1088–1104,1129–1148,1519–1527,2060–2095](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/pipeline_orchestrator.py#L1088)；实际模型服务也按窗口逐条返回 episode，见 [apps/api/local_drama/application/story_pipeline_ai.py:489–511](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/story_pipeline_ai.py#L489)。

**复现**：创建 1 集初始项目，输入短第一章 + 31,500 字第二章。真实 `_episode_specs()` 形成 4 个输入项，编号为 `[1,2,2,2]`。执行生成后，草案把这 4 个窗口直接作为 4 条分集规划；覆盖状态为 FULL。`preview_pipeline_apply(STORY_PLAN)` 返回 `can_apply=true`，新增列表里出现 3 条 EP02。真正 `apply_pipeline()` 抛出：

```text
sqlite3.IntegrityError: UNIQUE constraint failed: episodes.season_id, episodes.code
```

**根因**：新规格将 `unit_number` 与 `window_index` 分开了，但下游仍把每个窗口当成正式 Episode。窗口有同一 number/code，生成服务逐窗口 append，最终 plan 没有按 unit 合并。apply 只在事务开始时读取一次 `episode_rows`，同一事务刚插入的 EP02 没进入查重视图，于是第二个窗口重复插入。若该编号原本就已存在，则反复 UPDATE 同一集，最后窗口的 source_range 覆盖前面窗口，存在丢失正文范围的问题。

**用户影响**：长稿显示“规划完成、可应用”，点击应用失败；已有集的情形还可能静默仅保留最后窗口的源范围。不能用去重忽略重复行修复，否则会丢失后半章内容。

**设计与实现**：

1. 明确两种实体：`AnalysisWindow` 是模型输入与断点单位，`PlannedEpisode` 是正式分集单位。给窗口稳定标识 `(source_sha256, unit_number, window_index)`，保存字符起止 offset、输入 hash 和结构化结果。
2. 在 `execute_draft_generation()` 写 `draft.story_plan.episodes` 前按 `unit_number` 聚合窗口，生成唯一 number/code 的一条分集。逐窗合并情节证据和实体观察；source_range 取覆盖的完整源区间，不使用最后窗口覆盖。
3. 如果产品确实希望一个长章拆成多集，应增加明确的“章节窗口→分集规划”阶段，并给每集独立且稳定的编号。不要把技术输入长度直接变成重复分集。
4. 为 `_pipeline_quality_report()` 加 BLOCKER：episode number/code 唯一、窗口结果归属完整、所有完成窗口可映射到计划分集；preview 与 apply 共用此校验。
5. apply 内改成 `episodes_by_number` 字典，新插入后立即更新字典，并以唯一业务键做乐观冲突检查，作为最后防线；该防线不能替代上游正确聚合。

**验收**：短第1章+超长第2章、无章超长单段、已有EP02、新建EP02分别完整走 generate→preview→apply→读取正式集；没有重复编号，preview允许时apply成功，正式源范围包含头尾内容。断在同一章多个窗口之间恢复后结果一致。

### PR-02 / P1：运行中暂停后立即恢复，旧尝试结算虽重新入队，下一次成功仍被判为取消

**归属：今日统一状态机与恢复逻辑的回归。**

**代码位置**：[apps/api/local_drama/application/jobs.py:120–134,1084–1098,1250–1273,1485–1488](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/jobs.py#L120)；同类 worker session 回收也应统一修复。

**复现**：`create(max_attempts=3) → claim(first) → pause → resume(旧尝试仍active) → complete(first,success=false) → claim(second) → complete(second,success=true)`。第二次调用明明传 `success=True`，返回 `job_state=CANCELLED, attempt_state=CANCELLED`。把第一次 complete 换成 lease 过期后 `reconcile()`，结果相同。日志中 Job 的 `cancel_requested_at` 保留第一次 pause 的时间。

**根因**：pause 使用 `cancel_requested_at` 作为旧 attempt 的合作停止信号；resume pending 只写 `next_run_at`。统一状态机在 PAUSED+resume 分支返回 QUEUED，但各持久化调用者只写 state/next_run_at，没在消费恢复意图时清掉旧停止标志。下一次结算读取残留标志，在 `facts.cancel_requested` 分支优先返回 CANCELLED。另外新 artifact gate `jobs.py:1753` 同样检查该时间戳，所以真实 worker 可能在登记新产物前就遭 `ARTIFACT_PUBLISH_FORBIDDEN`，不必等到 complete。

**实现**：

1. 给 `JobStateDecision` 增加明确的“消费旧停止意图”结果字段，或统一 `persist_job_state_decision(connection,job,decision,...)`。仅当原因是 `resume_intent_after_attempt_settled`，在与旧 attempt 结算/资源释放同一事务中设置 `cancel_requested_at=NULL`。
2. `complete()`、lease `reconcile()`、worker-session recovery 共用这个持久化逻辑，不能各自手工同步字段。
3. 不要在用户刚点 resume 时立即清标志/把job设RUNNING；旧执行仍需要看到停止请求。也不要给所有QUEUED状态盲目清除取消标志，必须确保当前事务确认是已授权 resume、旧尝试已结算，且取消未随后覆盖。
4. 长期把 `pause_requested_at`、`cancel_requested_at`、`resume_requested_at` 分开，避免同字段承担两种意图；迁移时保留旧语义转换。

**验收**：除原有“返回QUEUED”断言外，要继续执行第二 attempt 的 heartbeat、产物登记和 complete，最终SUCCEEDED。覆盖 complete、lease expiry、session stop；另测 pause→resume→cancel，在每个竞态窗口取消始终优先。检查资源lease无重复持有。

### PR-03 / P1：继续解析后保留旧分集，却用新批次的资产与故事总纲覆盖整剧内容

**归属：今日新增继续分析机制引入。**

**代码位置**：[apps/api/local_drama/application/pipeline_orchestrator.py:1458–1463,1505–1533,1576–1581](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/pipeline_orchestrator.py#L1458)；[apps/api/local_drama/application/story_pipeline_ai.py:515–517](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/story_pipeline_ai.py#L515)。

**实测结果**：第一批第1章返回角色1和第1章世界观事实；第二批只处理第2章返回角色2。最终分集是 `[1,2]`，但 `assets.characters` 从 `[角色1]` 变成 `[角色2]`，`story_bible.continuity_facts` 只剩第2章事实。见 `assets_repro.log` 的 AUDIT_ASSETS 输出。

**根因**：继续分析把 `remaining_specs` 单独给 `generate()`，其 synthesis只综合本批episodes。编排合并了 `saved_episodes+ai_episodes`，却直接取 `generated['assets']`、`generated['story_bible']` 当整剧产物。既没有把旧总纲/资产传给 synthesis，也没有合并旧结果；注释称“合并核心人物与连续性记忆”，实际是覆盖。

**影响**：大于60个窗口的小说只保留最后一批识别的核心人物/场景/道具与记忆；前面仍可见的分集拿不到对应资产，人物设定可能前后冲突。已经应用过的项目还会面临旧正式资产与新草案不一致。

**设计与实现**：

1. 保留每窗口/每分集的 `entity_observations` 和 compact digest，不要在唯一持久化结果中先删掉再指望后续恢复。
2. 分离 `plan_windows()` 与 `synthesise_story()`。续接只新增缺失窗口；全剧综合基于已完成窗口的所有 compact digests 或带来源的增量记忆，而不是仅本批。
3. 给资产建立稳定实体ID及别名映射；新增观察按实体ID合并，名字相似但身份不确定时进入冲突复核。总纲事实按来源片段键累积，明确替换/否认历史事实的行为需可追踪，不能盲目最后一次覆盖。
4. 新草案、资产汇总、故事记忆与cursor在一个可验证版本上发布，保留上一版本用于失败恢复。只更新当前batch的暂存结果，成功后再切换整剧快照。

**验收**：61章/121章、多批角色仅在首批出现、末批新增、同人别名、相互冲突设定四组fixture。首批资产不得无理由消失；最终总纲包含各批来源证据；中途失败/重启不改变已经发布的整剧快照。

### PR-04 / P1：失败续接再次点击继续，会重放 FAILED Job 并把运行记录挂成永久 RUNNING

**归属：今日新增 continue_analysis 的幂等处理缺口。**

**代码位置**：[apps/api/local_drama/application/pipeline_orchestrator.py:980–992,1034–1055](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/pipeline_orchestrator.py#L980)；幂等返回点 [apps/api/local_drama/application/jobs.py:420–431](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/jobs.py#L420)。

**复现**：第一批成功；调用continue；第二批注入可恢复的模型异常，使run和job都FAILED；按最新revision再调用continue。返回run为RUNNING、stage=QUEUED，但是 job_id 与上一次完全相同，数据库job仍FAILED，`JobService.claim()`返回None。后续retry_pipeline还要求run=FAILED，此时用户已经失去正常重试入口。

**根因**：续接键固定为 `pipeline-continue:{run_id}:{next_window}`。失败不推进cursor，再次提交遇相同键，`create_job_in_transaction()`回放原响应但不重新排队。调用者没检查回放Job当前状态，直接把pipeline投影写RUNNING。

**实现**：

1. 明确区分“重复同一次命令”和“用户授权重试本窗口”。相同命令键重放必须返回当前Job/run真实状态，不得改变状态；新的retry命令使用新的command id并引用同一window batch。
2. 在同一事务内读取持久化Job状态，QUEUED/CLAIMED/RUNNING重放已有活跃任务；FAILED走授权retry helper或返回明确可恢复状态；SUCCEEDED核对cursor再决定剩余窗口。不得仅信创建时缓存response里的QUEUED。
3. 提取公开事务内retry方法供pipeline使用，避免再次嵌套 `BEGIN IMMEDIATE`。重试Job和run状态更新必须原子。
4. API返回 `job_state`/`recovery_action`，前端按服务端动作展示“重试本批”与“继续下一批”；前端审查同时确认前端根本没有调用continueAnalysis，需要一并接通入口。

**验收**：相同cursor失败后重试成功；同请求重复提交不增添Job；并发继续只创建一任务；服务重启重放仍一致；Job终态与run RUNNING不一致时有对账收敛机制，不能永久占住工作台。

### PR-05 / P1：部分草案已经应用后继续分析，新分集无法再应用到项目

**归属：今日 continue_analysis 与既有 apply_state 协议未衔接。**

**代码位置**：[apps/api/local_drama/application/pipeline_orchestrator.py:980–1055,1559–1587](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/pipeline_orchestrator.py#L980) 未处理新一版草案的应用水位；`preview_pipeline_apply:1875–1878`、`apply_pipeline:1976–1977` 仍按run级别APPLIED一次性拒绝。

**复现**：两章按一章一批处理；第一批是PARTIAL且preview允许，用户apply第1集成功；继续第2章成功，最终 `coverage=FULL, planned=[1,2], apply_state=APPLIED`，正式DB只有 `[1]`。再次preview抛 `PIPELINE_STATE_INVALID`，直接apply会 `PIPELINE_ALREADY_APPLIED`。

**影响**：产品支持先做部分分集，但后续解析产物只存在于草案，无法进入正式项目。整部生产会话若已基于旧范围冻结，也不会自动包含后来集数。不能简单把apply_state清NOT_APPLIED再把全稿重写，已有镜头/资产会被重复或覆写。

**设计与实现**：

1. 将应用状态从run单个布尔语义改成 `draft_revision + applied_revision/hash + applied_episode_ids/window_watermark`。每次续接发布新的draft revision，旧版本保持已应用事实，新版本存在待应用差量。
2. Preview输出新增分集、保留已制作分集、仅文本可更新项、故事总纲/资产影响。apply按照这个冻结diff和hash执行，成功后推进应用水位，不回写已经生产的镜头。
3. 自动授权应用需要为新revision建立新的依赖Job，不能复用第一批已经SUCCEEDED的continuation job；授权必须明确包含哪些续接范围。对于已开始production session的新集，生成新session或通过显式可审查的扩展计划加入，保持会话输入快照不可变。
4. 迁移已有APPLIED run：从现有应用记录/正式集映射导出已应用水位；无法确认的历史项进入preview确认，不猜测全部已应用。

**验收**：首批apply→部分镜头生产→续接下一批→预览差量→应用→正式集完整；首批镜头ID、媒体ID、已确认文本不变化；重复apply同一revision幂等；121章三批及自动授权同样通过。

### PR-06 / P2：Outbox旧调度者的清理会释放新调度者租约，真实成功响应丢失确认

**归属：今日新增 `_release_unsent()` 扩大了旧owner写入窗口；根本缺陷是没有claim身份。**

**代码位置**：[apps/api/local_drama/application/outbox_delivery.py:81–90,135–153,211–216,258–271,274–284,297–300](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/outbox_delivery.py#L81)。

**隔离并发复现**：

1. A领取事件1、2，在事件1的HTTP等待；事件2还是未发送IN_FLIGHT。
2. 事件2租约过期，B回收并领取事件2，进入ATTEMPTING并等待接收端响应。
3. A的事件1失败进入finally，`_release_unsent([事件2])`只判断相同delivery_id及状态，将属于B的ATTEMPTING改为RETRYING。
4. B拿到HTTP 204，确认UPDATE查不到ATTEMPTING，返回 `WEBHOOK_DELIVERY_CLAIM_LOST`；最终事件2仍RETRYING，已接收的消息将来会重复发送。

本次只替换HTTP transport，用事件屏障控制顺序；没有真实网络出站。租约过期用更新合成账本时间模拟。生产中默认批量20，全部claim共享30秒租约且HTTP逐条执行，后排事件在慢接收端时有自然过期机会。即便延长租约，调度进程挂起/恢复也会保留同类竞态。

**根因**：claim/begin/ack/failure/release全凭状态，没有owner token或claim generation；“状态是ATTEMPTING”无法证明是本进程持有。新的 `_release_unsent` 还允许更改ATTEMPTING，进一步使另一个正在发送的owner丢锁。ack和_record_failure也同样可误结算新owner的发送。

**实现**：

1. 增加随机 `claim_token` 或递增 `claim_generation`；领取后返回给item。所有begin/续租/成功/失败/释放使用 `WHERE id=? AND claim_token=? AND status=?`，rowcount为0时返回失去所有权，不写对方账本。
2. `_release_unsent`只释放本token且未进入ATTEMPTING的行；发出请求的事件由它自己的成功/失败/过期恢复路径处理。不要清理别人活跃发送。
3. 在真实开始发送时检查并续租；大批量最好逐条或小窗口领取。应用层仍坚持at-least-once，接收端用event_id去重，不能声称状态CAS实现exactly-once。
4. 过期ATTEMPTING回收时核对MAX_ATTEMPTS；已耗尽转DEAD_LETTER，避免每次崩溃都可越过预算。现有测试竟期待MAX_ATTEMPTS+1，应改成上限边界契约。

**验收**：两个dispatcher+过期回收+旧owner失败finally，B最终204必须DELIVERED；旧owner迟到的500/204不能改新generation；crash前未发送不计次数，真正发送每次计一次，总数不超过已声明预算；失败后余下未发送事件及时可领。

### PR-07 继续解析缺少页面入口

**级别：P1；证据：前端调用关系核对与后端执行契约。** 后端 [apps/api/local_drama/application/pipeline_orchestrator.py:30,1462,1598-1608](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/pipeline_orchestrator.py#L30) 每次最多执行60个分析窗口，保存cursor后不会自动提交下一批。前端 [apps/web/src/features/pipeline/pipelineClient.ts:330](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/pipeline/pipelineClient.ts#L330) 定义了continuePipelineAnalysis，但全站没有调用方；工作台只显示PARTIAL警告。现有后端续接测试也以显式POST完成后续窗口。

为OneClickPipelineWorkbench接入服务端`analysis_cursor.has_more_windows`、`next_window_index`、总窗口数与恢复动作。PARTIAL完成时显示“继续解析剩余内容”，提交最新revision和独立operation key；该动作期间锁定重复提交，成功后显示真实Job状态。若产品选择自动续接，则由已有编排器在同一授权范围内调度并记录真实Job，前端仍须显示边界与停止操作，不允许只把PARTIAL文案改成FULL。

此入口必须与PR-03、PR-04、PR-05一起验收：有入口后，失败重试、资产累积和新集差量应用仍需正确。用61/121窗口以及单章多窗口原稿从可见入口操作，末尾sentinel、完整来源范围与正式项目集数均正确；中途失败只重试失败批次，不能重新生成已经应用的镜头。


## 剪辑和媒体保真详细修复方案

证据目录：`evidence/timeline_media/`。TM-01与02为组件测试；TM-03至06运行真实媒体或官方解析器；TM-07至10是当前业务尚未接入的新模块直接实测。

### TM-01｜P1｜首份时间线不修改参数就无法保存

**位置**：[apps/web/src/features/edit-v2/EpisodeEditWorkspace.tsx:133–161,222–236](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/edit-v2/EpisodeEditWorkspace.tsx#L133)。

**触发**：新一集已经采用视频，工作区 `latest_revision=null`、`allowed_actions` 包含 `CREATE_DRAFT`。按默认编排直接点击“保存新草稿”。

**实测**：按钮启用，点击后 `createEpisodeTimelineDraftV2` 的调用次数为 **0**，没有建立首份 revision。现有实现只有先随便改一次时长/开关才可能发起创建请求。复现日志的断言是“应调用 1 次”，实际 0 次。

**根因**：载入上游建议编排时已经把该建议写为 `baseline`；保存函数仅凭 `submittedSignature===baselineRef.current` 返回 `saved`。它没有区分“未修改的建议编排”和“已经持久化的时间线版本”。草稿干净状态不能证明数据库中有一版可冻结的时间线。

**实施方案**：

1. 将 `loadedSnapshotRef` 的来源拆为 `UPSTREAM_SUGGESTION` 与 `PERSISTED_REVISION`，同时保存对应 revision id 和 upstream fingerprint。
2. 保存按钮始终按“建立可冻结版本”的意图处理。仅在存在当前有效的持久化等价 revision、输入指纹仍一致时才可省略创建；`latest_revision=null` 必须发 `createEpisodeTimelineDraftV2`。
3. 将“是否需要新 revision”和“是否有未保存编辑”独立计算，例如 `mustCreateRevision = !latestRevision || freshness !== CURRENT || dirty`。避免旧版本已经过期却因为编辑面板刚刷新而再次短路。
4. 创建成功后使用响应 revision 建立新的持久化基线，再刷新；对可省略的重复保存显示“当前草稿已保存”，避免无响应感。

**验收**：首次进入有素材但无 timeline 的一集，零修改点击保存应 POST 一次，出现 v1，随后可冻结；重复点击在请求中禁用；当前等价持久化草稿不应产生重复版本；STALE 输入允许重新建立版本。

### TM-02｜P1｜上游指纹变化会静默覆盖未保存剪辑

**位置**：[apps/web/src/features/edit-v2/EpisodeEditWorkspace.tsx:133–161](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/edit-v2/EpisodeEditWorkspace.tsx#L133)，尤其 146–148 与 153–159。

**触发**：已有草稿 `tl-1`，本地将镜头时长从 1 秒改成 1.5 秒；随后 React Query 得到同一个 revision id、不同 `upstream_fingerprint` 的响应。上游视频/字幕/音频变化可以产生这类刷新。

**实测**：刷新前输入 1.5 秒、registry dirty=true；刷新后输入回到 **1 秒**，未保存改动被覆盖。`audit_timeline.test.tsx` 中正确行为断言失败：expected 1.5，received 1。代码在覆盖时还调用 `publishTimeline(..., false)`，会撤销离开页面时的草稿保护。

**根因**：保护分支只判断“revision id 变了”，而 effect 同时订阅 upstream fingerprint。版本 id 不变的上游刷新不进保护分支，直接 `setClips`、`setAudioOptions`、`setBaseline`。

**实施方案**：

1. 任何外部载入与当前本地草稿不等价且本地 dirty 时，都保留本地输入；不能以 revision id 变化作为唯一条件。
2. 独立维护 `lastAppliedRevisionId`、`lastAppliedUpstreamFingerprint`、`pendingServerSnapshot`；接收响应只更新 pending，直到用户显式“载入服务器版本”或合并。
3. 自己刚完成的保存用提交时的 snapshot/version 判定并吸收相应响应；若保存期间又编辑，应保留之后的编辑并维持 dirty。
4. “载入服务器版本”应经过现有草稿放弃确认协调器，不能直接调用 `adoptSnapshot` 消除未保存改动。

**验收**：分别改变上游指纹、revision id、字幕版本、音频开关，在本地 dirty 状态刷新应全部保留输入、继续阻止冻结并保持离页保护；同一界面提示服务器有新输入；无本地编辑时可自动更新。

### TM-03｜P1｜叠化压缩了视频，原声仍按原时长串接，末尾声音被截掉

**位置**：[apps/api/local_drama/application/timeline.py:3233–3251,3274–3283,3411–3414,3500–3516](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/timeline.py#L3233)。

**接入范围**：已接入的 `TimelineService.render_episode` 使用此拼接路径。源音轨问题在用户启用“模型原声”时直接影响成片；独立对白/字幕仍使用原绝对时间轴，需和同一时间映射一起修复。

**真实复现**：三段各 2 秒，依次红/440Hz、蓝/880Hz、绿/1320Hz，两处 0.5 秒 DISSOLVE。输出总时长 5 秒；输出 **3.6 秒**采样画面 RGB `[1,128,1]` 已是第三段绿色，音频仍为 **880Hz** 的第二段，第三段音频到约 4 秒才进入。

另一个末尾标记测试中，第二段蓝色视频只有其源 1.8–2.0 秒存在标记音。两段叠化后视频 3.5 秒，该标记按视频映射应在成片 3.3–3.5 秒；实测成片该区间 **RMS=0**，源区间 RMS=11584.3，标记音完全丢失。

**根因**：视频 `xfade offset=current_duration-transition_duration` 使下一镜头提前；音频依然 `concat=n=2:v=0:a=1`，第二镜头音频不提前。末尾再 `atrim=target_duration` 截到视频时长，只掩盖容器超长，不能修复镜头对应关系，并会永久裁掉尾音。多转场累积偏移。

**实施方案**：

1. 在冻结渲染计划中保存每个镜头输出起止帧、源入出点、转场重叠帧、输出音频起止采样，提供一致的源时间→成片时间映射。
2. 模型原声若需随视频叠化，使用同一重叠时长的 `acrossfade`，或按明确策略对齐 `adelay` 后混合；不能普通 concat 再裁总尾部。只硬切镜头仍用硬切音频。
3. 对已绑定镜头的 DIALOGUE/SFX/字幕按镜头映射转换一次；若产品定义它们锁定绝对成片时间，则必须同时调整视频时间轴编辑语义并清晰展示，不能两种轴混用。
4. 整片尾部 trim 只作为帧/采样舍入保护；不得用于修复丢失的源区间。音画映射检查应在登记 VERIFIED 前完成。

**验收**：本次三色三频测试每个镜头非转场中心区域的颜色/频率应来自同一源；2–10 个连续叠化不累积音画漂移；源尾部标记保留；源音轨开启/关闭、对白叠加、字幕尾句分别测试。使用 PCM 与解码帧检测，不只断言 FFmpeg 返回 0 或容器总时长。

### TM-04｜P1｜短镜头使预检计划与实际转场时长不同，少帧成片仍标 VERIFIED

**位置**：[apps/api/local_drama/application/timeline.py:2797–2803,3066–3069,3221–3223,3251,3279–3283](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/timeline.py#L2797)。

**真实复现**：三个镜头长度依次 2 秒、0.5 秒、2 秒，第二与第三镜头设 DISSOLVE。

- 冻结计划用相邻单镜头时长计算：重叠 0.25+0.25=**0.5 秒**，计划成片 4 秒，即 24fps 下 96 帧。
- 渲染器第二次以累计视频长度代替前一镜头长度：执行重叠 0.25+0.5=**0.75 秒**。
- 真实 `render_episode` 结果 status=**VERIFIED**；视频流 **3.750 秒/90 帧**，音频流 **4.000 秒**。证据 `contract_results.json.short_middle.registered_render`。

**根因**：`_timeline_transition_overlap_us` 取 `video_items[index-1]` 的时长，`_concat_timeline_videos` 却将 `current_frames / target_fps` 传给相同函数，两次“重新推导”的入参含义不同。预检与渲染快照相等没有证明实际命令按快照执行；最终检查没有拒绝该视频/音频差值。

**实施方案**：冻结计划加入 `transitions:[{from_item_id,to_item_id,duration_frames,offset_frames}]` 与 `expected_video_frames`，渲染只读取该明细，不再调用时长启发式；若暂不改 schema，至少统一前镜头参数。登记前以 ffprobe `nb_read_frames` 或全解码帧数校验 expected_frames，音频用采样数/有效结束时间校验，分清 AAC 编码填充与实际播放时长。计划不符返回结构化 `RENDER_FRAME_COUNT_MISMATCH` 并保留诊断证据，禁止登记 VERIFIED。

**验收**：2/0.5/2 秒例子必须实际渲染 96 帧、视频与有效音频 4 秒；新增短镜头位于首/中/末、连续短镜头、24/25/30000÷1001 的参数矩阵；断言每个执行转场都等于冻结明细，不只比较两个 JSON 快照。

### TM-05｜P2｜项目设置 24fps，首素材 30fps 时仍导出 30fps

**位置**：[apps/api/local_drama/application/timeline.py:250–256,2815,2864–2895](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/timeline.py#L250)。

**真实复现**：通过 ProjectService 创建明确 fps_num=24/fps_den=1 项目，导入第一段 30fps、第二段 24fps，创建与渲染时间线。`_episode` 返回对象没有 `fps_num` 字段；预检 `timeline_plan` 选择 30/1，真实输出 `avg_frame_rate=30/1` 且 VERIFIED。`contract_results.json.project_fps` 保存完整结果。

**根因**：新 `_resolve_timeline_fps` 的首选分支读取 `episode['fps_num']`，但 `_episode` SQL 只选 `e.*`、project_id、root_rel、subtitle_mode，未选项目 fps。首选分支因此永远拿不到显式项目设置，退到第一素材帧率。已有矩阵测试将项目 fps 设置成首素材 fps，未覆盖两者不一致。

**实施方案**：扩展只读 episode/render context 的明确字段，读取 `p.fps_num AS project_fps_num,p.fps_den AS project_fps_den` 或权威 production spec 的交付帧率；显式优先级为冻结交付配置→项目配置→仅在未配置时的素材推断。后续渲染、视频帧网格、字幕与 OTIO 时间码必须共用这一有理数，记录来源以便排查。

**验收**：项目24/首素材30/次素材25 输出24；项目30/首素材24输出30；30000/1001保持有理数而非round30；无配置才允许推断；预检、命令、最终流参数与交换包 fps 一致。

### TM-06｜P2｜OTIO 把叠化写成 1 倍速效果，剪映也漏转场，却宣称无损失

**位置**：[apps/api/local_drama/application/timeline_exports.py:78–97,216–225,474–501,541–664,755–774,805–807](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/timeline_exports.py#L78)。

**真实复现**：冻结两个 2 秒 VIDEO，第二项参数 `DISSOLVE + transition_duration_seconds=0.5`；调用真实 `export_revision`，再用官方 OpenTimelineIO **0.18.1** `adapters.read_from_file` 解析实际 `.otio`。

- 解析成功，但视频 track 的 children 是 `[Clip, Clip]`，**Transition 数为 0**；第二个 clip 上只有 `LinearTimeWarp`，`time_scalar=1.0`。
- OTIO track 时长 **4 秒**，相同时间线本机 MP4 成片 **3.5 秒**；export 返回 `losses=[]`、`fidelity=BASE_EDITING_INTERCHANGE`。
- 相同时间线剪映导出 `materials` 只有 audios/texts/videos，segment 未写转场信息；其 losses 也为空。未在真实剪映桌面导入，不声称已做剪映客户端验收。

**根因**：代码注释“OTIO has no first-class cross-dissolve object”不符合官方契约。OTIO 的 [Transition 类](https://opentimelineio.readthedocs.io/en/latest/api/python/opentimelineio.schema.html#opentimelineio.schema.Transition) 明确表达相邻条目之间的转场，支持 `SMPTE_Dissolve`、`in_offset`、`out_offset`；[LinearTimeWarp 类](https://opentimelineio.readthedocs.io/en/latest/api/python/opentimelineio.schema.html#opentimelineio.schema.LinearTimeWarp) 表达素材变速。将名称改为 dissolve 不改变对象语义。`_export_losses` 只看存在正转场时长就把它排除，且剪映复用了此规则，未根据 writer 的真实能力判断。

**实施方案**：

1. 使用官方 OTIO schema writer 在相邻 clip 之间插入 `Transition`，显式设置 `SMPTE_Dissolve` 和有理数 offset，依据 TM-03/TM-04 统一后的输出计划计算剪辑范围与可用 handles。不能仅在 effects 填自定义字段。
2. 按 `OTIO`、`CMX3600`、`JIANYING` 分别定义已实现能力，losses 带 `format`；OTIO 编码能力不应让剪映自动宣称保留。
3. 在剪映 writer 未实现与验证转场前，始终报告该 item 的 `TRANSITION_DISSOLVE`/`TRANSITION_FADE` 损失，并提供 manifest 人工恢复信息。FADE 不应不加说明地当 DISSOLVE。
4. 增加 writer_version 并将其纳入 export hash，避免升级后命中旧缓存文件；保留旧导出记录的版本。
5. 回归断言以官方解析器的 `isinstance(child,otio.schema.Transition)`、in/out offset 与 timeline duration 为准；替换现有只检查 effect_name 字符串的测试。

**验收**：本例 OTIO 可由官方 parser 读取且包含正确 Transition，片长/源入出点符合冻结计划；转场不可表达时必须有逐格式 loss；剪映测试在真实目标版本导入前继续标记 best-effort，不能把文件写出等同完整保真。

### 以下为新模块直接实测：当前业务尚未接入

`rg` 在 `apps/api/local_drama` 与测试中检索 `atomic_render`、`build_chunk_command`、`build_mix_command`、`FfmpegRunner`，未发现 `infrastructure/composition/ffmpeg_renderer.py` 之外的业务调用。以下均是 **P2 / 接线前阻断**：直接调用新增模块，用真实 FFmpeg 复现；不宣称用户现成渲染路径已触发这些问题，也不把这部分的功能当作已经完成的端到端能力。

### TM-07｜P2 / 接线前阻断｜跨块长镜头重复读取源开头

**位置**：[apps/api/local_drama/infrastructure/composition/ffmpeg_renderer.py:633–637,713–729,744–745,781–801](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/infrastructure/composition/ffmpeg_renderer.py#L633)。

**复现**：一段 4 秒素材前两秒红440Hz、后两秒蓝880Hz，clip 覆盖 frames[0,96)，两块[0,48)、[48,96)。分别用原始 `build_chunk_command` 编码，两块返回0、均正常产物；第二块0.5秒位置仍为红RGB[254,0,0]、440Hz，正确应是原素材2.5秒处蓝880Hz。第一块与第二块生成的 trim 源起点都是0。

**根因**：只筛选和 chunk 相交的 clip，却未计算交集的源偏移；始终 `source_in_us` 起读，并用 clip 全长后在输出端截断。跨块重复头部仍能满足帧数检查，所以现有 frame-count 验证不会报错。

**实施方案**：对每个视频 clip 计算 `intersection_start=max(clip.start_frame,decode_start)`、`intersection_end=min(clip.end_frame_exclusive,decode_end)`；通过有理数 fps 把 `intersection_start-clip.start_frame` 转为源入点增量，以交集帧数/样本数精确 trim；handle 在相同坐标系裁一次。先排序并校验视频轨覆盖，不把全片片段长度当作本块片段长度。禁止无声明 tpad 克隆长缺口以伪造满足帧数。

**验收**：跨块画面序列与一次性不分块渲染逐帧对应；分别覆盖一个长 clip 跨3块、一个块含多镜、非0 source_in、非整数fps、非0 handles；验证帧内容/时间码与声音标记，不能只比帧数。

### TM-08｜P2 / 接线前阻断｜多轨 manifest 的旁白 WAV 被当视频解码

**位置**：[apps/api/local_drama/infrastructure/composition/ffmpeg_renderer.py:633–636,683–708,727–749](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/infrastructure/composition/ffmpeg_renderer.py#L633)。

**复现**：RenderManifest 同时含 VIDEO MP4 与 NARRATION WAV，二者覆盖本块。builder将两者都加入视频 concat；真实 FFmpeg 返回234，`[1:v:0] ... matches no streams`，无法成片。

**根因**：`manifest.clips` 是所有轨道，builder只按时间相交过滤，未按 track/item_kind 分派。即使素材无音轨，`atomic_render` 也未传 has_audio_lookup，builder默认每个视频有音频，会对纯静音视频引用不存在的`:a:0`。

**实施方案**：VIDEO 主画面只编译视频轨，OVERLAY/TEXT_LAYER 用独立层合成，NARRATION/BGM/SFX 用音频计划，SUBTITLE 进入字幕编译；不支持的已声明 item_kind 应预检报明确 blocker，不能当黑色填充或普通视频静默降级。atomic_render 在命令构建前取得受校验的媒体流事实，把 has_audio_lookup传入；image/motion需要各自输入策略。缺音频的视频采用显式静音，音频文件永远不请求视频流。

**验收**：完整VIDEO+NARRATION+BGM+SUBTITLE manifest可按支持的轨道渲染；静音MP4、普通带音MP4、WAV、PNG逐类验证，未知类型明确阻断；旁白不增加画面时长。

### TM-09｜P2 / 接线前阻断｜两段及以上旁白混音引用未定义标签

**位置**：[apps/api/local_drama/infrastructure/composition/ffmpeg_renderer.py:992–999](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/infrastructure/composition/ffmpeg_renderer.py#L992)。

**复现**：两段1秒旁白WAV，调用 `build_mix_command(narration_paths=[voice1,voice2])` 后执行真实FFmpeg。命令输出标签是 `[voice_raw]`，后续输入标签却为 `[voice]`，返回234：`Invalid stream specifier: voice`。一个旁白分支能执行，多旁白分支必然失败。

**实施方案**：将 concat 的输出标签与后续 voice_label统一，或显式新增 `[voice_raw]... [voice]` 的时间映射链。建议图构建器维护 producer/consumer 集合，在调用FFmpeg前拒绝未定义label、重复producer、未消费输出，以捕获这种结构错误；label中间状态不得仅依赖字符串约定。

**验收**：0/1/2/10段旁白 × 有无BGM/SFX，全部实际FFmpeg执行；按顺序检测各段标记音并验证总采样数。不能只快照filter字符串。

### TM-10｜P2 / 接线前阻断｜旁白起始采样参数只写日志，实际音频仍从0开始

**位置**：[apps/api/local_drama/infrastructure/composition/ffmpeg_renderer.py:945–957,981–1003,1005–1030,1085–1108](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/infrastructure/composition/ffmpeg_renderer.py#L945)。

**复现**：48kHz、总4秒manifest，一段1秒音频，`narration_start_sample=48000`即希望1秒起播。执行成功返回0；输出0.25秒 RMS=6906.5，1.25秒 RMS=0。实际声音在0–1秒，未在1–2秒；传入起点仅出现在note。同一路径对sfx_paths也只逐个从0混合，未读取manifest中sample位置和源范围。

**实施方案**：用manifest冻结的sample_start/sample_end/source_in作为音频编译输入，按每段`atrim`、`asetpts`、以采样为单位的延时对齐后amix，不以简单路径列表隐式决定布局。兼容参数 `narration_start_sample` 必须转为实际延时或显式废弃并拒绝非0，不能接受后不执行；旁白空隙需声明并保留，不可concat消掉。BGM/SFX各自执行源入点、增益、循环、淡入淡出与目标结束采样。

**验收**：0/48000/48123采样三个起点，解码WAV验证前置静音与标记音落点误差≤1采样；两段旁白中间带明确停顿、SFX放在2秒、短BGM循环、源入点非0分别验证；包含ducking时仍不得丢失旁白空隙。

## 页面交互与可用性详细修复方案

证据目录：`evidence/frontend_audit/`。FE-A01至09由原始React组件及受控API响应验证，FE-A11为源码确认；均未执行真实浏览器截图。

### FE-A01 · P1：一键制作预检期间继续编辑，会让新正文被误标为已提交并丢失导航保护

**定位**：[apps/web/src/features/pipeline/OneClickPipelineWorkbench.tsx:479–501, 808–812, 846–848](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/pipeline/OneClickPipelineWorkbench.tsx#L479)。

**复现**：在 AI 制作 → 粘贴正文输入文稿 A；点击“开始 AI 制作”；预检尚未返回时输入文稿 B；让第一次请求成功。实测请求中的 `raw_text` 仍为 A，但是 `draftRegistry` 中 B 的 `dirty` 被置为 `false`。同一过程中，编辑输入会执行 `launchMutation.reset()`，使开始按钮在原请求仍挂起时重新可点击。两个独立组件断言确认这两个表现。

**影响**：用户会把 B 当作已提交；成功回调同时删除 session/localStorage 草稿，离开或重新打开后 B 可能丢失。连续点击还会产生并行的启动意图。不能只在 UI 上加一句“请勿重复点击”解决。

**根因**：mutation 捕获的是旧 `payload`；`onSuccess` 读取的是最新 `rawTextRef/activeSourceRef`，没有核对提交版本。`reset()` 重置 observer 状态，不会取消已经运行的请求。

**实施**：

1. 定义 `LaunchSnapshot = { manuscriptVersion, manuscript, payload, operationId }`，在按钮事件一次性冻结，作为 mutation 参数；`mutationFn` 和回调只通过这个快照关联同一次操作。
2. `onSuccess(result, submitted)` 仅在 `manuscriptVersionRef.current === submitted.manuscriptVersion` 且当前签名等于提交签名时清除草稿。存在后续修改时保留 B 与 dirty，并显示“已提交上一版；当前有未提交改动”。
3. 请求未完成时禁止 `reset()`，或把错误展示清理与请求执行状态分开；使用独立 in-flight operation 锁，不能只依赖 mutation observer 的 pending。
4. 对来源选择、上传完成、时长、风格、授权范围修改使用同一操作快照规则；要么允许继续编辑但保留新草稿，要么暂时禁用表单并明确展示已提交快照。

**验收**：A 正在提交时编辑 B；A 成功、失败、超时三种情况下都不把 B 变 clean；导航必须提示保存/放弃；新打开恢复 B；连续点击只创建一个运行。已有无改动单击启动流程仍只提交一次。

### FE-A02 · P1：新建解说的幂等键忽略实际参数，修正配置后重试会冲突

**定位**：[apps/web/src/features/explainers/CreatePage.tsx:135–156](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/CreatePage.tsx#L135)；后端核对 [apps/api/local_drama/api/routes/explainers.py:267–284](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/api/routes/explainers.py#L267)、[apps/api/local_drama/application/projects.py:354–375](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/projects.py#L354)。

**复现**：相同标题、TOPIC 模式、300 秒，先填主题 A 并创建，预检阻塞后改主题为 B 再点击。组件实测两次 payload.topic 不同，Idempotency-Key 却完全相同。标题、方式、时长之外的语言、画幅、联网策略、正文、输出组合也不进入该键。后端按完整 payload hash 检查，同键不同请求会抛 `IDEMPOTENCY_KEY_CONFLICT`。

**影响**：页面明示“处理后重新点击”，但常见的修正参数重试路径会失败；用户不得不修改标题或时长来绕过。相同参数再次有意新建作品也会永久复用稳定键，而不是新操作。

**实施**：把“创建项目”和“已有项目修正后重新预检”拆成明确状态。项目创建后保存 `createdProjectId`，后续重试使用该项目的更新/导入/预检命令；新建动作使用 `operationIdempotencyKey(scope, normalizedPayload)`，scope 含本次创建会话 ID；网络重试复用键，成功后完成操作，下次有意新建重新开会话。不要仅把更多字段拼入永恒 `stableIdempotencyKey` 就认为恢复闭环已完成。文件导入也应保存已导入资料 ID/指纹，避免每次重试重复导入。

**验收**：仅变主题、正文、语言、画幅、文件、联网策略各一次；无幂等冲突且真正使用改后的输入；同一超时请求重试不复制项目；再次有意新建同名作品是独立操作，重试已创建作品不额外建项目。

### FE-A03 · P1：渲染返回 BLOCKED 仍自动确认并展示“已提交分块渲染”

**定位**：[apps/web/src/features/explainers/ReviewPage.tsx:53–72](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/ReviewPage.tsx#L53)；后端 `api/routes/explainers.py:1368–1392`。同类模式还在 `ReviewPage.tsx:75–98,128–142` 的修复/导出，应统一核验。

**复现**：edition 已冻结讲稿但尚无 composition；渲染 API 返回 2xx 及 `{status:'BLOCKED', blockers:[...], would_create_jobs:false}`。组件仍发送第二次 `confirm:true`，并将响应当成功显示“已提交分块渲染（manifest …）”。新增测试精确捕获两次 API 调用和错误成功状态。

**根因**：用 HTTP Promise resolve 等同业务成功，直接把 `Record<string,unknown>` 强转取字段；没有检查 status/blockers、manifest、composition id。

**实施**：生成客户端定义可辨识联合类型：`RenderPlanBlocked | RenderPlanReady | RenderSubmissionAccepted`。预检只有 `READY_TO_START` 且存在 composition ID/hash 才能确认；BLOCKED 显示逐项 blocker 和下一步。正式提交只有携带真实持久任务/运行 ID 的已受理结果才写成功提示。导出和局部修复采用同一模式，保留确认响应，不能丢掉结果而返回前一次预检对象。

**验收**：2xx BLOCKED、409 STALE、缺 manifest、真正 SUBMITTED 四种响应分别验证；阻塞时只发一次预检，不发确认；无 job/operation 证据不显示后台任务已创建。后端真正排队问题由后端报告合并处理。

### FE-A04 · P1：解说审片、候选比较和旁白试听缺少实际媒体控件

**定位**：[apps/web/src/features/explainers/ReviewPage.tsx:207–237](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/ReviewPage.tsx#L207)；`StoryboardPage.tsx:178–205`；`AudioPage.tsx:145–168`。后端 `api/routes/explainers.py:1092` 的 current_render 也只给 id/hash/integrity。

**确认内容**：审片页即使有 `current_render` 仍无 `<video>`，只渲染 `MediaPlaceholder`；“前一帧/后一帧”只修改本地数值，并用写死的 40ms 推算时间；时间轴固定“起始/主体/收束”。分镜候选只显示编号与哈希，没有图像或视频。旁白有实际 take 时仍没有 `<audio>` 试听。新测试以有 current_render 的事实数据渲染组件，确证 video 不存在。这不是由缺模型或空数据造成的空态。

**影响**：用户无法完成看候选后采用、听读音后重读、看成片后确认的主要操作链。当前不能把这三页称为完整审片工作台。

**实施**：

1. 读模型提供明确 `media_version_id`、受控播放 URL、MIME、duration、fps_num/fps_den、frame_count、字幕 revision 与媒体校验状态；URL 沿用项目 `mediaPlaybackPolicy`，避免暴露原始绝对路径。
2. 审片页用真实 `<video controls>`，加载状态、媒体加载失败与重试可见；逐帧功能按冻结 manifest 的 PTS/帧率 seek，帧边界 clamp，不能把 `frame * 40` 当所有版本时钟。
3. 分镜候选展示 thumbnail/视频控件，选择状态与实际候选一致，锁定/采用前能查看真实画面；旁白段落展示选中 take 的试听，onerror 可重试。
4. 时间轴从实际 clips/cues/mix lanes 构造；暂无可播放媒体时禁用逐帧操作，保留诚实空态。大列表缩略图 lazy-load，播放实例只保留当前选择。

**验收**：本机真实 MP4/WAV 可播放且产生 Range/206 或成功媒体请求；前后帧修改真实 video.currentTime/对应帧；25fps 和 30000/1001fps 分别校验；末帧不越界；媒体损坏有可见错误；候选采用前可看图/视频，重读前可试听。此项必须补真实浏览器验收，不能用占位图截图代替。

### FE-A05 · P1：人工确认成片时，混用了选中问题的哈希

**定位**：[apps/web/src/features/explainers/ReviewPage.tsx:101–114](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/ReviewPage.tsx#L101)；后端 `application/explainers/quality.py:3190–3208`。

**复现**：当前成片 id=r1/hash=R；第一个问题来自旁白或旧版本，subject_hash=Q。点击“确认当前成片”。组件实际发送 `subject_kind:'COMPOSITION_RENDER', subject_revision_id:'r1', subject_hash:Q`。测试证明 Q 优先覆盖 R；后端会正确拒绝 `STALE_REVISION`，但页面会让用户误以为成片本身刚被修改。

**实施**：成片决定只从同一个当前 render 对象取得 kind/id/hash；问题选择只影响问题详情。分别构造 `reviewSubjectForRender(render)` 与 `reviewSubjectForIssue(issue)`，不要 nullish 合并不同主体。无真实 render 或哈希时禁用“确认当前成片/发布授权”。增加实际审阅区间、备注输入，并绑定该媒体版本；当前固定 `reviewed_intervals:[]` 和模板 note 无法表达用户看了哪一段。

**验收**：任意切换到脚本、音频、旧成片问题不改变“确认当前成片”的对象三元组；成片变更后明确提示重新审阅；决策必须绑定有效媒体与范围，不能退回 edition id 冒充 render id。

### FE-A06 · P2：分镜页保存失败、锁定失败、更换影响都没有展示

**定位**：[apps/web/src/features/explainers/StoryboardPage.tsx:34–35,45–67](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/StoryboardPage.tsx#L34) 和该文件整段 JSX。

**复现**：点击候选，服务端返回 `CANDIDATE_REVISION_CONFLICT`。onError 写入 error，但 JSX 不读取 error/feedback，也未引入 `InlineError/InlineOk`；DOM 没有 role=alert。点击“查看更换影响”成功也只更新不可见的 feedback。候选列表读取失败同样没有自己的错误分支。

**实施**：在主要操作区域展示 `InlineError`/`InlineOk`；独立处理 `candidatesQuery.isError` 并给重试；冲突时保留选择并刷新 revision；反馈明确绑定 beat/candidate，切换 beat 清理旧提示。影响返回使用专用影响面板展示受影响版本/数量，不能只产生被忽略的字符串。

**验收**：采用、锁定、影响预览三类操作的成功/409/500都可见；错误不使按钮永久禁用；重试前刷新修订；读候选失败与“没有候选”区分。

### FE-A07 · P2：声音与字幕页无版本时永久显示载入中

**定位**：[apps/web/src/features/explainers/AudioPage.tsx:24–31,58–63](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/AudioPage.tsx#L24)；`useExplainerQueries.ts:129–136`。

**复现**：新建项目，`editions=[]` 已成功返回，打开声音与字幕。由于 narration query 的 enabled=false，但 isPending 仍为 true，代码先检查 `narration.isPending`，永远走“正在载入声音与字幕”，不会到“还没有输出版本”。定向组件测试确认。

**实施**：按依赖层级判断：editions pending → editions error → editions empty → 有 edition 时 narration pending/error → 声音/字幕状态；不要把被禁用 query 的 isPending 等同正在请求。字幕读取错误独立显示，并为 unavailable/empty 提供返回讲稿或总览的动作。

**验收**：无版本、版本接口500、旁白接口500、字幕接口500、正常无音频各展示正确状态；空态没有后台不断请求旁白，也不无限 loading。

### FE-A08 · P1：切换字幕语言会错误切换当前输出版本的旁白时钟

**定位**：[apps/web/src/features/explainers/AudioPage.tsx:24–31,101–120](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/AudioPage.tsx#L24)；后端 `api/routes/explainers.py:1192–1224`。

**复现**：保持中文配音 edition，点击“字幕语言”的 English tab。客户端不仅取英文字幕，还发 `getExplainerNarration(e1,'en-US')`；页面配音标签仍显示中文。组件测试直接记录该错误 locale。随后“重读这一句”的 API 又按 edition 的中文 voice_locale执行，读视图与写命令语言不同。

**影响**：中文音轨页面显示另一语言的 takes/时长或“未生成”；混淆独立英文配音版与同一中文配音版的英文字幕。错误时钟会影响审阅判断。

**实施**：分离 `voiceLocale=activeEdition.voice_locale` 与 `subtitleLocale`。旁白只跟 edition 的 voiceLocale；字幕语言只能来自当前 edition 的 subtitle_locales，不硬编码中英。edition/locale 都放进路由参数，并在切 edition 时清除不适用 subtitle locale。重读提交显式绑定 frozen_script_revision、take/voice locale，后端拒绝不一致。

**验收**：中文配音+中英字幕只切字幕不变配音时长；切到独立英文 edition 后才换英文 takes/时钟；返回链接/刷新仍选同一版本；不支持的字幕语言不显示可点击 tab。

### FE-A09 · P2：第 9 个及以后的审片问题不可从页面选择

**定位**：[apps/web/src/features/explainers/ReviewPage.tsx:265–294](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/ReviewPage.tsx#L265)。

**复现**：返回9条问题；数量徽章显示9，列表仅 `issues.slice(0,8)`，没有查看更多、分页或搜索。定向测试确证第9条不存在于页面。手工拼 `?issue=id` 不构成可发现的用户操作路径。

**实施**：提供服务器分页/过滤或小规模完整渲染；总数、已加载数、下一页清晰；按 severity/status 排序，保留深链与选择。若需要限制首屏，必须提供“查看全部 N 项”；不要依靠自动修复前8条后第9条恰巧出现。

**验收**：0/1/8/9/1000条问题都能找到目标问题；页码/筛选切换可恢复，选择不会错到第一条；大数据限制节点数量且不丢可达性。

### FE-A10 工厂分页问题并入EXP-07

这是同一问题的前端部分，不单独计数。FactoryPage固定limit=100且只过滤本地结果；按EXP-07接通服务端搜索、cursor与加载更多，验收第101／105条作品可发现。

### FE-A11 · P2：解说事实来源页只有标签，不能打开原句或修正事实状态

**定位**：[apps/web/src/features/explainers/ScriptPage.tsx:228–241,302–347](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/features/explainers/ScriptPage.tsx#L228)；[apps/web/src/generated/api.ts](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/web/src/generated/api.ts) 的 `patchExplainerClaim` 只有定义，解说页面未调用。

**确认方式**：源码审查。段落“事实 Cxxx”按钮仅向 URL 写 claim 参数，但组件从未读取该参数；账本只显示代码、状态、重要度，不显示 claim 原文/来源 span 或操作。来源列表只有标题/日期/hash。遇到需人工澄清的事实冲突时，当前页面无法完成“查看证据→修正/排除→再冻结”的闭环。

**实施**：读取 selectedClaimId，增加事实详情与引用原句/页码/span定位；提供状态修正和理由、修订校验，调用 patchExplainerClaim；修改后只使相关事实/讲稿/下游失效。来源原文按保存的span提供可读窗口，而不是展示全量大文档。

**验收**：点击事实链接真实展开对应原句与来源，复制深链后仍定位；冲突条目经有理由的处理可进入正确状态；没有来源的断言不能仅靠标签变成有证据支持。

## 启动和请求边界详细修复方案

证据目录：`evidence/pipeline_runtime/`。S1为当日新增回归；S2、S3是本日改造仍遗漏的历史问题。S3只有服务器请求层证据，没有真实浏览器DNS重绑定利用。

### S1 / P1：数据目录含 `#` 时，新的只读 readiness 会读取错误数据库、误报 503，并创建意外文件

**归属：今日新增代码造成的回归。**

**代码位置**：[apps/api/local_drama/infrastructure/database/readiness.py:224–249](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/infrastructure/database/readiness.py#L224)，尤其第 226 行；调用者 [apps/api/local_drama/main.py:154,320–326](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/main.py#L154)。

**触发条件**：将实例数据放在合法含 `#` 的文件夹中，例如 `work#draft/data/local_drama.sqlite3`。普通用户项目/磁盘目录名含 `#` 应可用；当前 Settings 对该目录合法接受，业务数据库连接也正常。

**实际复现结果**：

1. 使用发布迁移链生成数据库，并通过 SQLite backup 拷贝到含 `#` 路径，正常连接查得 head=`0102_explainer_factory_foundation`。
2. 调用真实 `inspect_schema_readiness()`，返回 `schema_incomplete`；返回的 missing_tables 是所有核心表，而真实数据库完整。
3. 真正启动应用 lifespan 后，`GET /api/v1/health/ready` 返回 **503**，错误为 `DATABASE_SCHEMA_INCOMPLETE`，必需审核模板步骤被跳过。
4. 同一个应用 `GET /api/v1/projects` 返回 **200**，证明业务 Database(path) 读的是真实可用文件。
5. 被声明为只读的探测在 `work#draft` 前缀的同级，创建了原本不存在的 **`work` 零字节文件**。

**根因**：第 226 行手工拼接 `file:{path}?mode=ro` 后传 `uri=True`。SQLite 将 `#draft/...?...` 解释为 URI fragment，实际访问的是 `#` 之前的路径；`mode=ro` 也落入 fragment，失去只读限制。百分号编码序列和问号目录也存在同一类 URI/路径混用风险，其中 Windows 不支持问号目录，`#` 不受此限制。

**影响**：合法路径部署被误判为不可服务；必需内置审核初始化不执行；监控/宿主等消费 readiness 时会阻塞启动；探测本身意外改变文件系统。数据库原文件未观察到损坏，不能描述成数据被破坏。

**实现方案**：

```python
# readiness.py：只对真正的文件路径进行 URI 编码，不能直接拼接原始路径。
database_uri = database_path.resolve().as_uri() + "?mode=ro"
connection = sqlite3.connect(database_uri, uri=True)
```

保留现有 `expected_heads`、核心表列检查和启动缓存。不要使用 `immutable=1` 绕过 WAL；发布数据库可能正在使用 WAL。统一维护一个 `sqlite_readonly_uri(path)` 工具时，应同步检索维护/检查模块里的其他手工 `file:` 拼接，但先单独修复本次回归。

**验收**：

- 新建参数化用例覆盖普通路径、空格、中文、`#`、字面 `%23`；Linux 追加 `?`，Windows 跑真实驱动器路径。
- 所有完整数据库都必须得到 `state=ready`、HTTP 200，且 6 个默认审核模板可正常查询。
- 以调用前后目录快照核对不产生截断文件；对于只读 inspect，不允许新建任何主数据库文件。
- 原来的不存在、不可读、旧 revision、缺表/缺列用例继续保持 503。

### S2 / P1：可选模型清单的语义错误仍能使 API 整体无法启动

**归属：今日拆分可选初始化后仍残留的旧缺陷；不是今天首次引入。** 旧代码的窄异常捕获亦会遗漏这些异常；新实现注释与验收目标明确要求可选模型不能阻断本地导入/审核/CPU 后期，因此仍需修复。

**代码位置**：[apps/api/local_drama/main.py:261–297](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/main.py#L261)（第 281 行只捕获三种异常）；[apps/api/local_drama/infrastructure/manifest.py:23–24,41–46,71–87](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/infrastructure/manifest.py#L23)；[apps/api/local_drama/application/profiles.py:175–222](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/application/profiles.py#L175)。

**实际复现**：从完整有效的 manifest fixture 每次只改一个字段，然后以干净、迁移完整的数据库启动实际 TestClient lifespan。未执行模型请求。

| 合法 JSON 但字段不合法的情况 | 实际异常 | 结果 |
| --- | --- | --- |
| 删除 `manifest_version` | `KeyError: 'manifest_version'` | lifespan 失败，API 无法进入服务阶段 |
| 设置 `runtime: null` | `TypeError: 'NoneType' object is not iterable` | lifespan 失败 |
| 设置 `models: null` | `AttributeError: 'NoneType' object has no attribute 'get'` | lifespan 失败 |

现有测试只覆盖文件不存在、语法无效 JSON 和有效清单；无法覆盖本表中的“可解析但语义不合法”。

**根因**：`load_manifest()` 只验证外层类型、manifest_type、read_only_inventory、canonical_model_root.path；后续属性和 sync 假定其他字段的存在及字典结构。KeyError/TypeError/AttributeError 越过可选步骤的异常边界，直接传播到 lifespan。缺 `manifest_version` 的情形还可能到 sync 最后的结果组织阶段才失败，应该在任何数据库写入前完成清单结构验证。

**实现方案**：

1. 在 `infrastructure/manifest.py` 增加完整的清单结构验证，至少验证 `manifest_version` 为非空字符串、runtime/models/h3_capabilities/authoritative_current_state 是符合约定的对象，`models.partitions` 和 `runtime.comfyui_api` 类型正确。
2. 可以用专门的 Pydantic 结构模型，或保持当前字典 API 并添加具名验证函数；所有输入结构失败统一包装成 `ManifestValidationError`，带字段路径，禁止暴露凭据。不要通过 `dict(value or {})` 静默吞掉语义错误。
3. `ProfileService.sync_manifest()` 先完成清单与引用结构验证，再进入事务，避免验证失败时留下半同步状态。
4. 可选步骤应有明确故障隔离边界：记录 `model_manifest.status=failed` 与错误类型，保留 required 步骤结果。若需要兜底 `Exception`，只包住该可选步骤、完整日志保留堆栈，不捕获 `BaseException`，不能把失败记录为 completed。

**验收**：

- 对上述三种输入分别执行真实 lifespan，API 可以启动，ready=200，`initialization.model_manifest=failed`，默认审核模板仍为 6 种。
- 补数组/数值代替字典、缺少版本、空版本等参数化测试；错误为 ManifestValidationError 或统一可配置错误，不传播 Python 实现异常。
- 快照检查无模型注册/能力 Profile 的部分写入；修正文件并重启能成功同步。
- 缺数据库/坏数据库继续由必需 schema 初始化阻断 readiness，不能被可选故障隔离掩盖。

### S3 / P1（有前置条件）：新增 Host 信任检查只保护写请求，陌生 Host 仍能读取业务数据

**归属：今日 DNS rebinding 写路径防护未覆盖的读取路径；旧版亦存在。** 本次证据证明服务器请求层的 Host 缺口，**没有实施真实 DNS 切换或浏览器端到端利用**。利用还依赖攻击者能让浏览器向本地服务发出相应请求、浏览器本地网络策略等条件，不能写成用户资料已经泄露。

**代码位置**：[apps/api/local_drama/middleware.py:394–419](https://github.com/Qioooba/local_drama_studio/blob/f1251e1aed7d5b1a8f00d55b2c824cb6a45b7d41/apps/api/local_drama/middleware.py#L394)；新增信任函数 `:93–117` 只经 `_is_allowed_write_origin()` 被调用。`main.py:352–371` 未挂全请求 Host 检查。

**实际复现**：

1. 按产品支持配置启动 `network_mode=LAN_SERVICE`、`host=0.0.0.0`、`trusted_lan_unauthenticated=True` 的隔离 TestClient。
2. 创建合成项目 `review_secret / PRIVATE_SYNTHETIC_SCRIPT`，服务返回 201。
3. 请求 `GET /api/v1/projects`，同时带 `Host: untrusted.example:3210` 与 `Origin: http://untrusted.example:3210`，该域名未配置在 allowed_origins/trusted_hosts。
4. 返回 **200**，JSON 含该项目标题、ID、root_rel 和业务元信息。`/api/v1/health/live` 在相同 Host 下也返回 200。

**根因/影响**：当前分支只有 `state_changing and origin` 时才检查 Host；GET 不进入。DNS rebinding 场景会使用同一攻击域名的 Origin/Host，读取路径不需要写令牌；CORS 不等价于服务器拒绝不受信的 Host。同源 GET 还常常不带 Origin，因此不能只扩展为 `if origin: ...`。这是条件性业务信息读取风险；不要求将可信 LAN 部署整体改为登录产品。

**实现方案**：

1. 将请求 Host 的独立信任检查放到所有路由/静态文件处理之前，对 GET/HEAD/POST 等一致执行；可以新增 `TrustedRequestHostMiddleware`，或在当前 middleware 顶部调用现有 `_host_is_trusted()`。域名/IPv4/IPv6/端口必须规范化。
2. 未登记域名返回统一 400 或 403 与稳定 `UNTRUSTED_HOST`；缺失、异常、重复 Host 采取拒绝策略。
3. `trusted_hosts=None` 时保留约定的 loopback 和 LAN IP 字面量行为；显式列表时执行运维配置含义。已登记域名仍可正常部署。
4. Origin 和实例令牌的写保护继续执行，二者职责分开。测试环境显式允许 `testserver`，不要给真实网络请求添加跳过规则。

**验收**：

- 带/不带 Origin 的未登记 Host：GET /projects、GET 媒体资源、GET session bootstrap、HEAD 和写请求都在路由读取前拒绝，响应不含业务数据。
- loopback IP、localhost、LAN IP、合法登记域名按照配置正常读写；显式空 trusted_hosts 的语义有回归用例。
- 补一项真实浏览器 DNS rebinding 防护验证时明确记录浏览器版本、本地网络访问权限与实际请求路径。服务器层单元/集成断言可先独立完成。

## 本机真实浏览器UAT与截图验收


**执行状态：本文件是后续验收步骤，当前审计环境未执行。没有浏览器截图或端到端通过结论。**

目标版本 `f1251e1`，修复后需重新记录实际 commit。浏览器基础地址按真实启动方式选择 `http://127.0.0.1:3210`（新鲜 build）或 `http://127.0.0.1:5173`（开发）。同一份测试结果不得混合两个不同构建。不要在用户已有生产项目上导入压力数据或确认审核；用可删除的审计项目。

### 1. 执行和证据规则

- Playwright 使用真实 Chromium，保留 trace、console error、pageerror、失败请求与 request/response 关键字段。主验收全部通过可见按钮、输入框、选择器操作。
- 无模型/无实际媒体时，把依赖完整生产的用例记 BLOCKED 并说明缺什么；不能用占位图或改DOM得出媒体已可播放。
- 每项截图使用 `UAT-编号-步骤-结果.png`，包含完整页面标题、目标操作区与结果。失败保留截图，修复后再拍同一视口对照。
- 正常真实后端链路和 `page.route` 延迟/错误注入分开报告。错误注入验证的是 UI 错误处理，不等于后端实际创建任务。
- 建议桌面 1440×900、1366×768；响应式至少 390×844。主工作台再检查 125% 浏览器缩放、侧栏开/关、长中文/英文标题。任何布局结论必须以实际截图为据。
- 页面上使用的 projectId/editionId/episodeId 必须来自当前测试项目，不能复制另一个项目的 ID。

### 2. 准备测试数据

| 数据集 | 必要事实 | 用途 |
|---|---|---|
| F-empty | 新建解说项目，0 editions，0 takes，0 renders | 空状态 |
| F-media | 一份中文讲稿，至少2段；中文配音版+双语字幕，独立英文配音版；选定且校验真实的 MP4/WAV、真实 current_render、至少2个候选 | 媒体播放和语言时钟 |
| F-issues | 同一项目9条以上未关闭问题，至少一条问题属于旁白且其哈希不同于current_render；问题有真实来源范围/片段 | 全量问题定位、审核主体 |
| F-manuscript | 独立短剧项目，20字以上 A/B 两份不同原稿 | 慢请求草稿竞态 |
| F-list | 105个有唯一代号的解说作品，第101和105条用唯一标题 `审计-101-不可遗漏` / `审计-105-不可遗漏` | 分页与搜索 |
| F-timeline | 一集至少2个真实工作视频，无已保存timeline；另一集有timeline草稿 | 首次保存、dirty保护 |
| F-long | 段落数量足够跨多个 bounded-window 的长稿，明确总字数/哈希/首末段 | 长文续读完整性 |

媒体应在修好的正常数据链路中生成/导入并绑定；如果使用预先生成的测试媒体，应明确标为fixture，记录SHA256、时长和帧率。不能通过填一串不存在的 media id/hash冒充真实媒体。

### 3. 逐项用例

#### UAT-01：已渲染成片真的能播放和逐帧

1. 打开解说工厂，进入 F-media → 审片与导出。
2. 确认页面能显示真实 `<video>`；点击播放，等待 currentTime 增长且有实际画面，记录对应媒体请求成功。
3. 暂停，点击后一帧/前一帧，检查真实播放位置和画面变化，记录帧号/时间。分别验证25fps和30000/1001fps素材。
4. 移到首/末边界，验证不会负数或越界；切换edition后播放器/时轴/QC主体同时更新。
5. 故意撤销测试媒体可用性，再刷新，确认可见加载失败和恢复操作。

**截图**：有画面播放态、逐帧前后两帧、缺媒体失败态。**不通过标准**：只有“渲染版本xx”文字、只增加帧数字、静态起始/主体/收束时间轴、404却显示可审核。

#### UAT-02：候选预览、采用、锁定和更换影响

1. F-media → 分镜与画面；分别打开候选1/2，实际查看缩略图/视频。
2. 点采用，等待真实响应与选中状态；锁定后刷新应仍锁定。
3. 点查看更换影响，页面显示受影响版本数/对象。
4. 通过另一标签修改该beat或在独立错误注入用例中返回409；再采用旧revision，观察可见错误、保留选择和刷新入口。

**截图**：两个真实候选、采用成功、409失败、更换影响详情。**不通过标准**：点击后无变化/无反馈、仅显示哈希供用户盲选。

#### UAT-03：旁白试听与字幕语言互不串线

1. F-media → 声音与字幕；保持中文配音edition，播放段落1。
2. 记录中文总时长和take ID，点字幕English tab：只切字幕，中文旁白take/总时长不变。
3. 切到独立英文配音edition，试听应变英文，其实测时长来自独立英文takes。
4. 刷新或复制URL新开，仍定位到同一edition/字幕语言。
5. 重读一段，观察真实持久任务→新take→相邻拼接重检，其他段落不重做。

**截图**：中文+英文字幕、独立英文配音版、单段重读任务及完成试听。缺真实任务记BLOCKED。

#### UAT-04：无版本/失败不伪装成无限载入

1. F-empty → 声音与字幕，等待editions请求完成。
2. 应展示“还没有输出版本”和返回生产的入口；旁白API不应被禁用状态拖成无限loading。
3. 独立注入editions500、narration500、subtitles500，每种显示对应失败和重试；恢复后能自行更新。

**截图**：0版本空态、三种失败态；每张附实际HTTP状态。

#### UAT-05：渲染阻塞不误报已提交

1. 使用有冻结讲稿、无composition的测试edition；点击冻结并渲染。
2. 后端若返回 `{status:'BLOCKED',would_create_jobs:false}`（可能是202），页面应显示阻塞原因，不再发confirm=true。
3. 独立检查render冻结冲突409、缺manifest、不完整提交响应；均不得展示“已提交分块渲染”。
4. 修好依赖后真实提交，只有持久job/run ID确已存在才显示已受理；最终成片应绑定同一manifest。

**截图/日志**：阻塞反馈、网络调用次数、真正成功的任务ID/manifest。错误注入结果单独标注。

#### UAT-06：新建解说参数修正后的恢复

1. 新建标题T，主题A，300秒；点击检查，故意在预检阻塞处停下。
2. 保留标题T/方式/时长，改为主题B，再提交。
3. 分别重复只改配音语言、画幅、联网策略、粘贴正文/参考文件的情况。
4. 验证无 `IDEMPOTENCY_KEY_CONFLICT`，且真正输入变为新内容；预检重试应继续已有项目，不额外复制项目。
5. 另外有意重新“新建”一份同名同参数作品，必须作为新操作区分；网络重试同一操作仍只创建一次。

**截图**：修改前后表单和成功结果；保留key/payload hash对照，避免记录敏感原稿全文。

#### UAT-07：预检慢请求时新稿不得丢失

1. 在 F-manuscript → AI制作 → 粘贴正文输入A，启动；对预检做受控延迟，让请求暂不结束。
2. 把正文改成B；开始按钮不得因为清理错误状态而恢复可重复提交。
3. 放行A预检/启动，验证后台请求正文是A，同时B仍明确标为未提交。
4. 点击离开：必须显示保存/放弃/取消。选择保存后重开，应恢复B；取消应留在B；放弃仅在明确选择后发生。
5. 分别让A启动失败/超时，重复验证B保留；测试从文件模式切粘贴模式同样不会串稿。

**截图/日志**：A pending、B仍dirty、导航确认、重开B；注明使用延迟注入。

#### UAT-08：首次时间线保存和上游刷新

1. F-timeline 无timeline的分集进入剪辑，保留建议编排不改，点击保存新草稿。
2. 应产生真实 `createEpisodeTimelineDraftV2` 请求及revision1，刷新可见并允许冻结。
3. 有draft的分集改片段时长但不保存；另一标签更换上游媒体/字幕，让上游指纹变化；回到当前页触发刷新。
4. 未保存时长应保留并显式报告服务器变化；不能silent reset，也不能把旧payload配新expected fingerprint强行提交。

**截图**：首次保存成功revision、并发更新保留dirty。和时间线专项报告验收合并。

#### UAT-09：第9个以及更多问题仍能找到

1. F-issues 审片页读取9条问题，总数应等于可通过分页/查看更多找到的数量。
2. 通过可见控件定位第9条，展开证据，点击“只修这个问题”。
3. 核查提交issue ID等于第9条，无误修第一条；复制深链后保持定位。
4. 对1000条问题重做搜索/分页，检查DOM大小与页面响应。

**截图**：第9条选择态、对应修复payload、分页总数。

#### UAT-10：人工确认绑定成片，不能绑定所选旁白问题哈希

1. F-issues 中选中一个音频/旧版本问题，使问题subject hash与当前render不同。
2. 播放当前成片，填写实际审阅范围/备注后确认。
3. Network中kind/id/hash必须全部来自当前render；更换所选问题不应改变这一三元组。
4. 另一个标签更新render，再用旧版本确认：应明确请求重新审阅，不得静默转确认新版本。

**截图/日志**：当前render、所选问题、审核范围、提交三元组和结果。

#### UAT-11：105个解说项目完整搜索与分页

1. 工厂默认列表确认“已加载/共计/还有更多”真实。
2. 直接搜索第101条/105条唯一标题，不先加载前100条，也应找到。
3. 清空搜索逐页走到底，每个project ID只出现一次，统计105。
4. 使下一页请求失败，重试应继续同一页，不回到空列表、不遗漏作品。

**截图**：第105条搜索结果、末页总数、下一页错误恢复。

#### UAT-12：来源事实跳转和人工修正

1. 资料与解说稿中点事实链接，页面真实定位到对应claim与来源原句，不只改变URL。
2. 查看原句/span/页码、来源身份与冲突说明。
3. 有理由地修正/排除冲突项，核查expected revision/理由持久化及受影响下游过期。
4. 返回讲稿再次冻结，阻塞原因应按最新事实重新评估。

**截图**：来源原句、事实修正表单、修正后状态和冻结结果。

#### UAT-13：长文跨窗口连续解析

1. F-long 导入后记录源文档hash、总段落、首末段。
2. 启动完整稿件解析，观察已完成窗口/全部窗口/覆盖范围。
3. 若自动暂停在PARTIAL，必须有可见“继续解析”操作或真实后台自动续接；不能只能看到提示而无动作。
4. 重启worker/刷新页面后继续，最终coverage证明无遗漏并包含末段；旧成功窗口不得全部重跑。

**截图/证据**：PARTIAL可继续、续接任务、最终完整覆盖范围。后端行为以长文专项报告为准。

### 4. 结果记录模板

| 用例 | commit/build | 浏览器/视口 | 数据集ID | 真实后端或故障注入 | PASS/FAIL/BLOCKED | 实际表现 | 截图/trace/请求证据 |
|---|---|---|---|---|---|---|---|
| UAT-01 | 待填 | 待填 | 待填 | 真实后端 | 未执行 | 待填 | 待填 |

验收报告必须保留未执行/阻塞项。不得用“构建成功”“所有原有单测通过”替代以上媒体与交互闭环验证。


## 实现代理的执行约定

1. 固定基线和工作分支，先读本文对应编号及原始证据；检查当前分支是否已有修复。报告不是可以盲目套用的补丁。
2. 一次完成一组相互依赖的修复。每次PR描述写明具体触发条件、改变后的行为、数据迁移／回滚方法和验收证据。不要为了通过测试而弱化正确行为断言。
3. 测试应覆盖最终状态。例如包复制后实际打开原稿，resume后完成第二次产物登记，续接后读正式episodes，渲染后解码帧与音频采样，OTIO读回Transition与片长。函数返回、202、状态字符串和文件存在不是充分验收。
4. 解说服务复用Job、媒体完整性、命令幂等与迁移框架；新增分层应针对本报告的事务边界、身份映射和执行交接，避免并存第二套队列或双重状态权威。
5. JSON和生成客户端逐步替换关键`Record<string,unknown>`强转。先强类型化创建、预检、提交、渲染、导出和审核的返回联合类型，再重新生成OpenAPI与客户端并核对实际契约。没有业务结果schema时，新增endpoint数量不能证明功能完成。
6. 数据库事务与文件系统操作不共享自动回滚。临时对象必须有命令归属标记，共享永久文件由引用检查后回收；失败不能删除另一个成功命令的文件。修复历史数据先产出可审查的dry-run清单。
7. 分页在静态快照下要求无重复无遗漏；`updated_at`会变化，动态翻页需查询快照或明确的刷新语义，不能承诺普通键集分页自动解决并发排序变化。
8. OTIO除了Transition对象还要修正source_range、offset和可用handles，以本机成片相同的冻结计划为准。新composition执行器要先通过TM-07至10，再接通后端公开的渲染入口。
9. 最终交付应包括修复commit、变更文件清单、迁移说明、针对性测试命令及结果、13项本机UAT的PASS／FAIL／BLOCKED记录和真实截图。未执行项保持未执行，不用构建或组件测试替代。

## 配套证据包的目录和复跑说明

`Local_Drama_Studio_20260922_evidence.zip`包含本文、各领域原始发现、原始日志／JSON、复现测试、合成媒体和未执行的本机UAT清单，不包含用户数据、数据库、模型或第三方依赖目录。

| 目录 | 内容 |
| --- | --- |
| evidence/explainer_backend | 10个路由／数据／handler／分页／预算实测结果与复现器 |
| evidence/imports_packages | 原稿解析、跨库恢复、并发删除、ZIP64阈值实验及日志 |
| evidence/pipeline_runtime | 长稿、Job恢复、Outbox、startup原始测试与证据 |
| evidence/timeline_media | 小MP4／WAV、解码帧、ffprobe／PCM数据、OTIO读回、组件断言 |
| evidence/frontend_audit | HEAD／基线Vitest结果、10项定向测试、构建日志、本机UAT清单 |
| evidence/model_platform | 初跑与补齐合成实例清单后的结果、测试配置适配器 |
| evidence/audit_manifest.json | 固定SHA、基线、提交日期与当日提交清单 |

复现脚本保留本次环境的原始路径，便于追溯；配套`rerun_python_reproductions.py`可在独立checkout上复制到新输出目录并适配路径，原始证据不会被覆写。它只处理Python复现器，前端Vitest请按对应配置的root提示适配。该便携包装器经过语法和参数检查，未在你的Windows环境执行。请使用Python3.12及仓库开发依赖；媒体复现需要FFmpeg/ffprobe，OTIO语义复现另需OpenTimelineIO0.18.1。

```bash
python rerun_python_reproductions.py --repo /path/to/local_drama_studio --out /path/to/new-audit --suite imports
python rerun_python_reproductions.py --repo /path/to/local_drama_studio --out /path/to/new-audit --suite explainer
python rerun_python_reproductions.py --repo /path/to/local_drama_studio --out /path/to/new-audit --suite pipeline
python rerun_python_reproductions.py --repo /path/to/local_drama_studio --out /path/to/new-audit --suite media
```

包中同时存在两种方向的测试：`test_reproductions.py`等部分复现器断言错误现状，从而“通过”证明缺陷；`test_audit_repros.py`、前端定向测试等按正确行为断言，所以当前“失败”证明缺陷。接入产品回归时必须统一为正确行为断言，不能把复现器的成功直接当作修复通过。
