# Kairo 配置与用户数据升级改造交接文档

> 交接时间：2026-09-02  
> 当前产品版本：v0.17  
> 工作目录：`/Users/qi/Documents/spaces/ops-toolbox`  
> 交接范围：配置文件、宠物数据及全部持久化用户数据的版本管理、迁移、备份、恢复、降级保护和发布验证

## 1. 先说结论

这次改造的核心不是“把很多文件硬合并成一个文件”，而是把它们统一纳入一个升级事务。

最终规则如下：

1. 正常发布 v0.18、v0.19 时，发行包只替换程序 EXE，绝不携带或覆盖用户的 `config.yaml`。
2. 用户配置和程序安装目录分离。Windows 默认配置固定在 `%AppData%\Kairo\config.yaml`，换 EXE 不会碰它。
3. 配置新增字段时，由程序按 `schema_version` 逐版本迁移；不是重新复制整份默认配置。
4. 宠物、便笺、提醒、定时任务、凭据、数据库连接、HTTP/SOAP 用例等数据不塞进 `config.yaml`，仍按业务拆分，但全部登记进同一个升级协调器。
5. 启动时先验证和准备全部文件，任何关键文件有问题都不写磁盘；准备全部成功后才统一快照、原子提交。
6. 用户明确要求覆盖官方配置时，运行 `--reset-config`。它覆盖服务器等官方配置，但保留数据目录、下载目录、日志目录、凭据后端和 file 密钥，因此宠物等用户数据不会丢失或“找不到”。
7. 新版数据被打开后，直接运行旧版会被拒绝，防止旧程序把新字段写没。要降级必须先恢复对应升级快照。
8. 明文服务器地址、账号和密码是产品当前的内网使用约定，改造没有删除或脱敏发行配置里的这些内容。只有开发者本机旁路令牌和内部端点覆盖会从首次启动模板里清掉。

本方案的代码改造、回滚/跨版本测试、浏览器 E2E 和发布验证均已完成。当前仍需在真实 Windows 或 Windows VM 上做一次运行时人工验收；macOS 交叉编译只能证明产物可生成，不能替代 Windows 运行时验证。

## 2. 用户需求与已经确定的产品决策

以下是不能被下一次重构随意改变的约束：

- 不考虑把历史用户安装目录里的旧配置自动搬到新目录。用户此前明确说“不需要管用户现有的配置文件”，因此本轮只保证采用新方案之后的连续升级。
- 不要求把用户配置上传服务器。所有配置和用户数据继续保存在用户本机。
- 不要求隐藏真实明文配置。这里是内网产品，预填内容用于减少用户配置工作量。
- 不能靠每个版本重新复制默认 `config.yaml`，否则会覆盖用户自己添加的服务器。
- 正常升级绝不能覆盖用户修改；但必须保留一个用户明确触发的“恢复官方配置”入口。
- 覆盖官方配置时不能删除宠物、提醒、便笺、任务、凭据等个人数据。
- 所有持久化数据都要有版本边界。旧程序看到未来格式必须拒写，不能把“不认识”当成“损坏”再重建。
- 升级失败必须尽量回到升级前状态，不能出现配置已经升级而其他数据还没升级的半成品。
- 发布 ZIP 只应包含程序，不包含配置文件。

## 3. 为什么不把所有内容合并为一个配置文件

只保留一个物理文件看起来容易替换，但会带来更大的风险：

- 配置、宠物状态、凭据、历史记录的写入频率不同；任一写入失败都会影响整个大文件。
- 一个模块写回大文件时，很容易丢掉另一个模块刚写入的内容。
- 凭据、宠物和业务配置的权限与生命周期不同，不适合共用一个文件。
- 文件越大，损坏后的影响范围越大；恢复也只能整包恢复。
- 后续多人开发时，每个模块都要理解整份总配置，耦合会继续上升。

因此当前采用：

```text
一个稳定的用户配置文件 config.yaml
        +
按业务拆分的用户数据文件
        +
一个统一升级协调器（统一版本检查、快照、提交、回滚）
```

用户日常只需要关心 `config.yaml`，开发人员则通过统一升级清单管理所有持久化文件。

## 4. 目录与文件边界

### 4.1 配置位置优先级

入口在 `internal/config/location.go`，优先级为：

1. `--config <绝对或相对路径>`
2. `KAIRO_HOME`
3. `--portable`
4. 操作系统用户配置目录

Windows 默认最终路径：

```text
%AppData%\Kairo\config.yaml
```

默认情况下 `data_dir`、`download_dir`、`log_dir` 等相对路径都以配置文件所在目录为基准解析，而不是以 EXE 所在目录为基准。因此用户替换 EXE 不会切换数据位置。

### 4.2 哪些属于用户持久化数据

统一清单入口为 `internal/upgrade/assets.go` 的 `DefaultAssets`。当前完整登记如下。

| 逻辑名称 | 默认文件 | 关键文件 | 当前格式版本 | 说明 |
| --- | --- | ---: | ---: | --- |
| config | `config.yaml` | 是 | 2 | 系统、服务器、日志目录及应用配置 |
| preferences | `data/preferences.json` | 是 | 1 | 用户偏好 |
| credentials | `data/credentials.json` | 是 | 1 | file 模式凭据 |
| database-sources | `data/database-sources.json` | 是 | 1 | 数据库连接配置 |
| pet | `data/pet.json` | 是 | 1 | 宠物状态 |
| browser-state | `data/browser_state.json` | 否 | 1 | 浏览器状态 |
| ssh-compat-profiles | `data/ssh_compat_profiles.json` | 否 | 1 | SSH 兼容配置 |
| reminders | `data/reminders.json` | 是 | 1 | 提醒 |
| notes | `data/notes.json` | 是 | 1 | 便笺 |
| scheduled-tasks | `data/sched_tasks.json` | 是 | 1 | 定时任务定义 |
| scheduled-task-runs | `data/sched_task_runs.json` | 是 | 1 | 定时任务执行记录 |
| http-cases | `data/http_cases.json` | 是 | 1 | HTTP 用例 |
| wsdl-projects | `data/wsdl_projects.json` | 是 | 1 | WSDL 项目 |
| soap-templates | `data/soap_templates.json` | 是 | 1 | SOAP 模板 |
| soap-history | `data/soap_history.json` | 否 | 1 | SOAP 历史 |
| soap-mocks | `data/soap_mocks.json` | 是 | 1 | SOAP Mock |
| soap-mock-records | `data/soap_mock_records.json` | 否 | 1 | SOAP Mock 记录 |
| desktop-pet-preference | `data/deskpet.json` | 否 | 1 | 桌面宠物开关等偏好 |
| credential-key | `data/.credkey` | 是 | 1 | file 凭据加密密钥 |
| pet-key | `data/.petkey` | 是 | 1 | 宠物数据密钥 |
| download-metadata | `<download_dir>/.kairo-meta.json` | 否 | 1 | 下载元数据索引 |
| license-certificate | `~/.kairo/license.dat` | 是 | 1 | 本机许可证证书 |

注意：这里的“关键”表示损坏时必须阻止升级提交，避免数据丢失；“非关键”表示可以警告后继续启动，但升级器也绝不会擅自覆盖损坏文件。

### 4.3 明确不纳入升级的数据

以下内容是可重建或无须迁移的运行产物，不做长期格式迁移：

- 日志
- 缓存
- 临时编辑文件
- 下载后的实际文件内容
- 测试临时目录
- 构建产物

下载文件本体归用户所有，不会被升级器改写；只有下载索引 `.kairo-meta.json` 纳入版本保护。

## 5. 启动升级事务

主入口在 `main.go`，正常启动顺序必须保持如下：

```text
解析 --config / --portable 等参数
        ↓
读取内嵌发行配置模板
        ↓
config.PrepareForUpgrade
仅在内存解析/迁移旧 config，用它定位 data_dir
        ↓
创建必要目录
        ↓
若指定 --restore-upgrade：执行恢复并退出
        ↓
upgrade.Run
锁定 → 校验全部文件 → 检查文件版本/产品降级 → 内存迁移
        ↓
全部准备成功后创建全量快照
        ↓
逐文件原子替换，upgrade-state.json 最后提交
        ↓
若提交失败，自动恢复升级前快照
        ↓
config.Open 重新读取磁盘上的已提交配置
        ↓
初始化凭据、宠物、提醒、HTTP 服务等业务模块
```

最关键的设计点是：配置迁移也不能在其他文件预检完成之前先落盘。`PrepareForUpgrade` 对已有文件只做内存迁移；真正写回交给 `upgrade.Run`。这避免了“config v2 已写入，但宠物或任务文件升级失败”的半升级状态。

首次启动是例外：没有旧配置需要保护时，会立即根据内嵌模板创建用户配置，然后再把整套数据纳入升级管理。

## 6. 配置文件版本与兼容策略

### 6.1 当前 schema

`config.yaml` 当前为：

```yaml
schema_version: 2
```

迁移注册表在 `internal/config/document.go`：

- v0 → v1：把历史无版本配置纳入正式版本管理，业务字段不变。
- v1 → v2：为系统、服务器和日志目录补永久 ID。

每次迁移只能做 `N → N+1`，不能直接从 v1 跳到 v4。这样跨多个版本升级时可以按顺序稳定重放。

### 6.2 永久 ID

以下对象已经增加稳定 ID：

- `SystemConfig`
- `ServerConfig`
- `LogDirEntry`

目的：未来官方需要定向更新某个内置系统、服务器或日志目录时，必须按 ID 定位，不能按数组下标或用户可修改的名称定位。

规则：

- 旧对象没有 ID 时，迁移器生成确定性 ID。
- 原对象 ID 保持不变。
- 用户在前端复制一个对象导致 ID 重复时，保留第一个对象的 ID，给复制出来的对象重新生成 ID。
- 即使文件写着 `schema_version: 2`，但缺 ID 或 ID 重复，检测器也会把它逻辑上视为需要从 v1 规范化，并纳入统一事务。

仓库发行配置 `config.yaml` 已写入固定 ID。下一位开发者不能为了“好看”随意重新生成这些 ID；它们已经是后续官方升级定位的长期主键。

### 6.3 新增 key 怎么做

以后在 v0.18/v0.19 给配置增加 key 时，分两类：

1. **可以安全使用默认值的可选字段**：结构体增加字段并在 `Defaults()` 中补默认值。如果旧程序写回不会造成严重数据损失，可以不升 schema。
2. **会改变语义、需要转换旧值、或旧程序写回会丢失的重要字段**：必须提升 `CurrentSchemaVersion`，增加严格的逐版本迁移器，并增加未来版本拒写和升级测试。

迁移器必须保留：

- 用户增加的系统和服务器
- 用户修改过的服务器内容
- 当前实现不认识但 YAML 解码/编码链条允许保留的内容
- 数据目录与凭据寻址信息

如果未来要“更新官方服务器 A 的端口，但不能碰用户服务器”，应按稳定 ID 找到官方服务器 A，并事先定义冲突策略，例如：

- 用户未修改过该字段：自动升级官方值。
- 用户修改过：保留用户值并记录提示。
- 强制恢复官方值：只允许通过明确的重置操作。

当前代码已经提供稳定 ID 基础，但“逐字段判断用户是否改过官方值”的三方合并算法尚未实现。v0.18 如果需要此能力，建议在 schema v3 中增加 `managed_revision` 或官方基线摘要，而不是直接覆盖。

## 7. JSON 数据版本与旧格式兼容

统一规则：

- 普通 JSON 根节点使用 `version`。
- 宠物历史格式使用 `v`，检测器兼容它。
- 历史数组或普通对象没有版本号时，按已发布的 v1 旧表示读取。
- 文件中存在业务字段名 `version` 但值不是整数时，不把它误判成格式版本。
- 发现版本高于当前程序支持版本时，读取或写入必须失败。
- 失败前不截断、不重建、不覆盖原文件。

已经转换为版本外壳的主要格式：

```json
{
  "version": 1,
  "entries": {}
}
```

用于凭据；旧版根对象仍可读取，下次成功写入时规范化。

```json
{
  "version": 1,
  "items": []
}
```

用于多个 SOAP/WSDL 数组存储；旧版裸数组仍可读取。

SSH 兼容配置使用 `version + profiles` 外壳。HTTP 用例、浏览器状态、许可证、桌面宠物偏好等也都增加或检查版本边界。

## 8. 宠物数据的处理

宠物不是配置项，不能在恢复官方 `config.yaml` 时顺带覆盖。

当前拆分如下：

- `data/pet.json`：宠物主要状态，关键数据。
- `data/.petkey`：宠物密钥，关键数据。
- `data/deskpet.json`：桌面宠物显示偏好，非关键数据。

保护规则：

- 三者都在升级总清单中，会进入统一快照。
- `pet.json` 发现未来版本时，`NewEngine` 返回错误，绝不再把它当损坏文件重建。
- `.petkey` 需要通过十六进制密钥校验。
- `deskpet.json` 发现未来版本时不可写，且使用较严格文件权限。
- `--reset-config` 保留原 `data_dir`，所以覆盖服务器配置后仍会读到原宠物数据。
- 只有用户主动恢复某个历史升级快照时，宠物才会跟其他用户数据一起恢复到同一时间点，避免“配置旧、宠物新”的混合状态。

## 9. 备份、回滚和恢复

### 9.1 自动升级快照

快照目录：

```text
<data_dir>/backups/upgrades/upgrade-<时间戳>/
```

每个快照包含：

- 所有已登记且存在的持久化文件副本
- `upgrade-state.json` 的升级前副本或“不存在”状态
- `snapshot.json`
- 原始路径
- 文件是否存在
- 原文件权限
- SHA-256

文件不存在也是快照状态的一部分。如果恢复到较老时间点，而某文件是新版后来新增的，恢复过程会删除该文件，避免留下半新半旧数据。

### 9.2 快照安全限制

- 只接受普通文件，不接受符号链接或特殊文件。
- 单个受管文件最大 256 MiB。
- 恢复路径必须位于 `<data_dir>/backups/upgrades` 内部。
- 快照中的逻辑名称和原目标路径必须与当前登记清单完全匹配。
- 备份文件名必须是纯文件名，禁止路径穿越。
- SHA-256 不一致时拒绝恢复，当前文件不变。

### 9.3 提交失败回滚

升级器先准备全部新内容，再创建快照，再逐个原子写入。`upgrade-state.json` 最后写入，作为整次升级提交标志。

任一提交失败：

1. 恢复升级前所有文件。
2. 恢复升级前升级清单。
3. 返回错误并阻止业务模块启动。

如果连回滚也失败，错误会同时包含原失败和回滚失败，便于人工处理。

### 9.4 人工恢复

命令：

```text
Kairo_win10.exe --restore-upgrade "<data目录>\backups\upgrades\upgrade-..."
```

恢复前会先对“当前状态”再做一个安全快照。恢复成功后程序退出，必须重新启动；新启动会按当前 EXE 的规则重新检查数据。

## 10. 主动覆盖官方配置

命令：

```text
Kairo_win10.exe --reset-config
```

实现入口：`internal/config/document.go` 的 `ResetToDistribution`。

它会：

1. 读取 EXE 内嵌的当前官方配置。
2. 备份当前用户 `config.yaml` 到配置目录下 `backups/`。
3. 用官方配置覆盖系统、服务器和其他发行配置。
4. 从旧配置保留以下字段：
   - `app.download_dir`
   - `app.log_dir`
   - `app.data_dir`
   - `app.credential_store`
   - `app.credential_key`
5. 原子写入新配置。
6. 随后仍会经过统一升级校验再启动。

它不会：

- 删除 `data_dir`
- 删除宠物、便笺、提醒、任务
- 删除凭据文件或密钥
- 删除用户下载
- 修改许可证

`--reset-config` 和 `--restore-upgrade` 不能同时使用，主程序会明确拒绝。

## 11. 降级保护

升级清单位置：

```text
<data_dir>/upgrade-state.json
```

记录内容包括：

- 清单 schema
- 最后成功打开它的产品版本
- 每个资产的格式版本
- 每个资产的 SHA-256
- 更新时间

两层保护：

1. **产品版本保护**：如果数据记录为 v0.19，而当前 EXE 是 v0.18，启动时拒绝降级写入。
2. **文件格式保护**：即使升级清单丢失或陈旧，也会从每个真实文件检测格式版本；发现未来版本仍然拒绝。

清单损坏本身不会导致用户数据不可恢复。升级器会验证用户文件、先快照，然后重建内部清单。但用户关键文件损坏会阻止启动，不会被自动清空。

当前产品版本比较器适合 `v0.17`、`0.18`、`v0.19.1` 这类数字版本。对于无法解析的开发版本字符串，只依赖文件格式保护。预发布版本的完整 SemVer 排序不是本轮目标。

## 12. 并发与锁

锁文件：

```text
<data_dir>/.upgrade.lock
```

- 同一数据目录同时只能有一个升级事务。
- 超过 15 分钟的锁视为异常退出遗留锁，可以自动接管。
- 这个 15 分钟不是网络超时，也不是业务请求超时；只是判断“上一次升级进程是否已经死掉”的保护时间。
- 不应把该时间随意缩短，否则慢磁盘或大文件快照期间可能误判仍在运行的升级。

## 13. 关键代码地图

### 配置

- `internal/config/location.go`：稳定配置路径、`--config`、`KAIRO_HOME`、`--portable` 优先级。
- `internal/config/document.go`：首次创建、内存预迁移、配置迁移注册表、未来 schema 拒绝、重置官方配置。
- `internal/config/config.go`：配置结构、默认值、校验、稳定 ID 生成与重复 ID 修复。
- `config.yaml`：当前 v2 发行配置和永久 ID。

### 跨文件升级

- `internal/upgrade/upgrade.go`：总事务、锁、准备、版本判断、提交、回滚和升级清单。
- `internal/upgrade/assets.go`：所有持久化文件的唯一总清单。
- `internal/upgrade/snapshot.go`：快照生成和底层恢复。
- `internal/upgrade/restore.go`：人工恢复前的合法性、路径、校验和安全快照。
- `internal/upgrade/files.go`：普通文件限制、原子写入等文件操作。
- `internal/upgrade/upgrade_test.go`：跨文件升级与恢复核心测试。

### 业务存储保护

- `internal/browserpref/browserpref.go`
- `internal/credentials/credentials.go`
- `internal/dbconsole/` 的数据源存储
- `internal/downloads/downloads.go`
- `internal/httpserver/handlers_http_cases.go`
- `internal/license/crypto.go`
- `internal/note/store.go`
- `internal/pet/engine.go`、`internal/pet/pet.go`
- `internal/reminder/store.go`
- `internal/schedtask/store.go`
- `internal/sshclient/profilestore.go`
- `internal/webservice/store.go`
- `internal/deskpet/deskpet_windows.go`

### 程序与发布

- `main.go`：把升级事务接入所有业务模块之前；实现重置和恢复参数。
- `scripts/release.sh`：正确的完整发布验证范围和 Windows 打包。
- `scripts/e2e.sh`：隔离 E2E 配置、数据目录和 Mock 服务端口。
- `docs/CONFIG-UPGRADES.md`：面向长期维护的精简规范。
- `README.md`：已经链接升级规范。

## 14. 已经完成的测试

### 14.1 Go 全量测试

已通过：

```bash
go test -mod=vendor -count=1 -timeout 300s . ./cmd/... ./internal/...
```

说明：不要直接把 `go test ./...` 的结果当产品结果。本仓库包含 `sdk/go120/test` 的 Go 编译器测试语料，直接执行会把这套外部测试也跑进去并失败。项目自己的正确范围就是根包、`cmd` 和 `internal`，发布脚本也使用这个范围。

### 14.2 静态检查

已通过：

```bash
go vet -mod=vendor . ./cmd/... ./internal/...
```

### 14.3 竞态测试

针对本次持久化相关包的 `go test -race` 已通过。下一位模型如继续修改升级或存储逻辑，应再次运行相同范围，至少覆盖：

```text
internal/upgrade
internal/config
internal/browserpref
internal/credentials
internal/dbconsole
internal/downloads
internal/httpserver
internal/license
internal/note
internal/pet
internal/preferences
internal/reminder
internal/schedtask
internal/sshclient
internal/webservice
```

### 14.4 前端测试

已通过：

```bash
node web/app.test.js
```

结果：20 项通过，0 项失败。

已通过：

```bash
node web/pages/webservice.test.js
```

### 14.5 版本一致性与发布包

已通过：

```bash
node scripts/check-version.js
./scripts/release.sh
```

结果：

- v0.17 版本声明一致。
- Windows EXE 构建成功。
- 生成 `dist/kairo-v0.17/Kairo_win10.exe`。
- 生成 `dist/kairo-v0.17-windows.zip`。
- ZIP 内只有版本目录和 EXE，不包含 `config.yaml`。

### 14.6 核心升级测试覆盖

`internal/upgrade/upgrade_test.go` 及各业务包新增测试已经覆盖：

- 首次纳管快照
- 重复启动幂等
- 配置 v1 → v2 实际迁移
- 迁移保留用户字段
- 其他资产准备失败时配置零改动
- 未来文件版本拒绝
- 升级清单陈旧时仍以真实文件版本为准
- 产品降级拒绝
- 关键文件损坏阻断
- 非关键文件损坏只警告且不覆盖
- 损坏内部升级清单重建
- 陈旧锁接管
- 快照恢复成功
- 恢复前安全快照
- 快照篡改拒绝且当前文件不变
- 资产清单完整性
- 配置稳定 ID 和复制对象重复 ID 修复
- 重置官方配置保留数据寻址
- 各业务存储未来版本拒写
- 凭据旧根对象读取并在成功写入时升级
- SOAP/WSDL 旧裸数组读取

## 15. E2E 状态与已修复的问题

第一次执行 E2E 时发现测试脚本本身存在隔离问题：

1. Bash 3 在 `set -u` 下读取空数组会报未绑定变量。
2. 服务探测失败时拼出了错误的 `000000` 状态码。
3. 最严重的是主服务使用 `go run .`，没有传测试配置，导致它错误地在真实用户配置目录创建首次启动配置。

处理结果：

- 发现后立即停止服务。
- 错误创建的目录已以可恢复方式移动到废纸篓：`/Users/qi/.Trash/Kairo-e2e-20260902-011039`。
- 当前真实用户配置路径下没有残留该测试目录。
- `scripts/e2e.sh` 已强制传入：

```text
--config /Users/qi/Documents/spaces/ops-toolbox/tmp/e2e/run/config.yaml
```

- Mock 端口调整为 2225，相关页面测试数据同步修改。

最终完整 E2E 已确认：

- 配置和数据全部位于 `tmp/e2e/run`，没有再写真实用户目录。
- 历史无 schema 配置被成功纳入统一事务并迁移。
- 主程序正常启动，许可证旁路正常。
- 18 个页面按钮扫描完成，点击失败 0 次。
- 完整用例总计 1504 个：1305 通过、0 失败、199 跳过。
- 报告已生成到 `test-results/report.md`，主服务、Mock SSH、Mock sponsor 均已正常清理。
- E2E 专用配置和数据全部位于 `tmp/e2e/run`，未写入真实用户目录。
- Mock SSH 端口统一为 2225，Mock sponsor 端口为 18093；API 覆盖测试中的旧端口引用已全部修正。

## 16. 验收记录与后续事项

### 已完成的 P0 验收项

- 已阅读本文和 `docs/CONFIG-UPGRADES.md`，并保留既定边界。
- 已核对 `git status` 和完整 diff，未重置或覆盖用户原有工作。
- 已完整跑完 `./scripts/e2e.sh --with-mock`：1504 总用例、0 失败，并检查报告。
- 已将 `tests/e2e/tests/20-api-coverage.js` 的 5 处旧 2222 端口统一为 2225，并复核 E2E 端口。
- 已重新运行发布脚本；Windows ZIP 只包含版本目录和 EXE，不包含 `config.yaml` 或用户数据。
- 已增加中途提交失败回滚测试，以及 v0.17→模拟 v0.18→模拟 v0.19→降级拒绝→快照恢复测试。

### 外部环境仍需人工完成

- 在真实 Windows 或 Windows VM 上手工验证默认 `%AppData%\Kairo` 路径、替换 EXE、重置、恢复和降级提示。macOS 交叉编译成功不能代替 Windows 运行时验证。

### 已完成的升级安全测试

已增加并通过以下版本跨度夹具：

```text
v0.17 用户数据
  → v0.18 EXE/格式夹具
  → v0.19 EXE/格式夹具
  → 重复启动
  → 尝试退回 v0.18（必须拒绝）
  → 恢复 v0.18 前快照
  → v0.18 能再次打开
```

当前仓库产品仍是 v0.17，没有真实 v0.18/v0.19 schema，因此测试以构造版本和资产迁移器模拟跨版本。正式发布新版本时仍必须用真实发行格式重复这套测试。

### P1：发布前产品化事项

- 当前快照没有自动保留数量/清理策略。长期运行后备份目录可能增长，需要产品确定保留最近 N 份、保留天数或只人工清理；在没有明确策略前不要自动删除用户备份。
- 重置和恢复目前是 CLI 参数，没有图形界面。如果目标用户不会用命令行，应在设置页增加明确确认、展示备份位置和重启提示。
- 正式发布前应把失败弹窗文案再按非技术用户检查一遍，尤其是“未来版本”“降级”“快照恢复”的说明。
- 如果 v0.18 要定向更新某些官方服务器默认值，需要先设计三方合并/官方基线，不要只凭 ID 直接覆盖用户修改。
- 可为升级快照增加可选的人工备注或来源产品版本，便于用户选择恢复点。

## 17. 当前工作区注意事项

工作区是未提交状态，所有改造都还在本地 diff 中。不要执行 `git reset --hard`、`git checkout -- .` 或其他可能抹掉用户工作和本次改造的命令。

本轮曾对 `internal/license` 目录运行格式化，以下原本无语义改动的文件目前只多了文件末尾换行：

- `internal/license/bypass.go`
- `internal/license/integration_test.go`
- `internal/license/license.go`
- `internal/license/server.go`
- `internal/license/server_test.go`
- `internal/license/simulate_test.go`

可以保留这些标准化换行，也可以由下一位模型在认真核对 diff 后精确移除；不要用整目录回退，因为：

- `internal/license/crypto.go`
- `internal/license/license_test.go`

包含本次真实的许可证格式版本保护改动。

`dist/`、`tmp/e2e/`、测试报告等可能是忽略的生成产物。删除前先确认是否需要保留给人工查看。

## 18. 新增持久化文件时的强制清单

以后任何模型新增一个会跨重启保留的文件，必须同时完成：

1. 定义文件的格式版本。
2. 定义旧格式读取策略。
3. 定义未来版本拒写策略。
4. 登记到 `upgrade.DefaultAssets`。
5. 决定它是关键还是非关键文件。
6. 提供格式验证器和真实版本检测器。
7. 若格式升级，增加逐版本迁移器。
8. 增加清单完整性测试。
9. 增加旧格式、未来格式、损坏格式测试。
10. 增加快照恢复测试。
11. 验证 `--reset-config` 是否应该影响它；默认答案应是“不影响”。
12. 验证降级时旧程序不会写坏它。

不能只在某个 Store 内“尽量兼容”，却忘记把文件放进全局升级事务。

## 19. v0.18/v0.19 发布验收标准

只有全部满足才可宣称升级功能完成：

- 从真实上一正式版用户目录直接启动成功。
- 用户添加的服务器、修改的服务器字段全部保留。
- 新增配置 key 按预期补齐。
- 所有稳定 ID 不发生无意义变化。
- 宠物、宠物密钥、便笺、提醒、任务、凭据、HTTP/SOAP 数据都能继续使用。
- 升级前自动快照存在且校验通过。
- 同一新版连续启动多次不重复迁移、不重复破坏数据。
- 任一关键文件损坏时零提交。
- 模拟中途提交失败时完整回滚。
- 用旧版直接打开新版数据会明确拒绝且文件不变。
- 恢复对应旧快照后，旧版可以重新打开。
- `--reset-config` 覆盖官方服务器，但用户数据和数据路径保持。
- Windows 默认路径正确，portable 和显式 config 路径也正确。
- 完整 Go 测试、vet、race、前端测试、E2E、发布脚本全部通过。
- 最终 ZIP 中不存在 `config.yaml`、用户数据或测试数据。

## 20. 推荐给下一位模型的接手指令

如果后续模型继续处理 v0.18/v0.19 正式格式，可以直接把下面这段作为首条任务：

```text
请先完整阅读 docs/CONFIG-UPGRADE-HANDOFF.md 和 docs/CONFIG-UPGRADES.md，再检查当前未提交 diff。不要重置或覆盖现有改动，不要删除发行配置中的内网明文。新增真实 v0.18/v0.19 schema 时，沿用现有升级协调器、快照、未来版本拒写、降级保护和逐版本迁移测试；完成后重新跑 Go 全量测试、vet、持久化包 race、前端测试、E2E 和 scripts/release.sh，确认 Windows ZIP 只包含 EXE。发现问题应做完整重构，不要用临时补丁绕过，也不要在任何失败情况下覆盖用户配置或宠物等用户数据。
```
