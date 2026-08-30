# 数据库工作台 V1 设计与 Oracle 11g 验收

## 目标与边界

V1 解决生产排障中的高频只读查询，不把 Kairo 做成完整数据库 IDE。主线运行时升级为 Go 1.24+，面向 Windows 10/11；Win7 / Go 1.20 在 legacy 分支维护，不能再约束主线数据库驱动、安全更新和测试矩阵。

V1 必须包含：

- Oracle 11g、MySQL、Redis 数据源的集中管理、连接测试和按用户授权；
- Oracle/MySQL 单条只读查询、对象/字段浏览、取消、CSV 导出；
- Redis `SCAN` 分页，以及 string/hash/list/set/zset/stream 的有限预览；
- 密码与连接元数据分离，查询审计不记录 SQL 正文；
- 超时、行数、响应字节、单元格、连接池和全局并发上限。

V1 明确不做：写 SQL、事务编辑、存储过程执行、Redis 写命令、跨库 JOIN、SQL 收藏/分享、结构变更和 PostgreSQL。后续版本不能通过放宽 V1 只读入口来实现这些能力，应建立独立的授权与审批模型。

## 架构

```text
database.js
    │ HTTP JSON / NDJSON + AbortController
handlers_database.go
    │ RBAC / source scope / audit / error redaction
internal/dbconsole.Manager
    ├── Store ── data/database-sources.json (0600, atomic rename)
    ├── credentials.Resource* ── keyring or AES-GCM file backend
    ├── database/sql pools ── go-ora / go-sql-driver
    └── go-redis pool
```

`Source` 不含密码。密码以 `database + sourceID + username` 的无碰撞资源键保存；API 只返回 `has_password`。数据源文件采用临时文件、`fsync`、原子替换，写盘失败时不发布新的内存快照。

## Oracle 11g 决策

主线使用 `github.com/sijms/go-ora/v2` 的纯 Go thin driver，不依赖 Oracle Instant Client，支持 Service Name 和 SID。没有采用 Oracle 官方 `godror` 作为 V1 默认驱动，因为其运行时依赖 Oracle Client，而且官方当前支持矩阵以较新的数据库版本为主；本项目的硬目标是现有 Oracle 11g。

Oracle 默认池参数比 MySQL/Redis 保守：`max_open=4`、`max_idle=1`、连接寿命 10 分钟。查询默认 30 秒、1000 行、16 MiB；一个单元格最多预览 256 KiB。用户查询由执行层统一改写为“可见上限 + 1”行：Oracle 使用兼容 11g 的外层 `ROWNUM`，MySQL `SELECT/WITH` 使用派生表 `LIMIT`，不能向 11g 发送 `FETCH FIRST`。Oracle 驱动预取设为 50 行，以减少网络往返并控制宽表内存。

## 安全模型

词法只读校验是防误操作，不是最终权限边界。生产数据库账号必须只授予需要的 `SELECT` / 字典视图权限，Redis 账号必须使用 ACL 限定到只读命令。

- SQL 仅允许 `SELECT/WITH`；MySQL 额外允许 `SHOW/DESC/DESCRIBE/EXPLAIN`；
- 拒绝多语句、DML、DDL、执行型关键字、`FOR UPDATE`、`INTO OUTFILE/DUMPFILE`；
- 用户 SQL 始终运行在数据库级只读事务中（MySQL `START TRANSACTION READ ONLY`；Oracle `SET TRANSACTION READ ONLY`）；
- 管理员才能新增、编辑、删除数据源；普通用户还要通过数据源 `allowed_users`；留空时仅管理员可用，显式 `*` 才授权全部普通用户；
- 审计记录 source ID、类型、SQL SHA-256 短标识、行数、耗时、截断与结果，不记录 SQL 正文或结果；
- 驱动错误返回前再次移除密码的明文与 URL 编码形式；
- TLS 的 `skip-verify` 是显式管理选项，生产默认应使用证书校验。

## 性能预算

| 保护项 | 默认 | 硬上限/行为 |
| --- | ---: | --- |
| SQL 行数 | 1,000 | 每数据源最多 20,000，超限立即关闭 rows |
| SQL 结果字节 | 16 MiB | 每数据源最多 64 MiB |
| SQL 推送批次 | 100 行 | NDJSON 流式发送，浏览器不等待全量结果 |
| 单元格预览 | 256 KiB | 二进制只给 4 KiB Base64 预览 |
| Redis 单次元素 | 200 | SCAN，不使用生产禁忌的 `KEYS *` |
| Redis 详情预算 | 8 MiB | 同时受数据源结果字节上限约束 |
| 全局数据库操作 | 8 | 等待也受请求 context/超时控制 |
| Oracle 连接池 | 4/1 | open/idle，可由管理员在安全范围内调整 |

元数据请求同样计入全局并发，并缓存 2 分钟；管理员点击刷新会主动失效当前数据源缓存。Redis 键和值统一按原始字节处理：有效 UTF-8 显示文本，其他内容提供 Base64 预览，二进制键通过 Base64 标识传输。

前端结果区使用固定行高虚拟表格；批次到达只更新可见行，不重复创建整张表。切换路由、切换数据源或点击取消都会中止 fetch，后端 `QueryContext` 同步取消。CSV 由后端重新执行同一条受限只读查询并流式生成；支持 File System Access API 的 Chromium 直接写入用户选择的文件，其他浏览器降级为 Blob 下载。

## Oracle 11g 上线前验收矩阵

以下测试必须连接测试环境或生产同版本只读实例完成；仓库 CI 没有 Oracle 11g 实例，因此不能用单元测试冒充兼容性验证。

可用同一条真实后端路径执行自动探针（环境变量中的密码不会写入数据源文件或测试输出）：

```bash
KAIRO_TEST_ORACLE_HOST=10.0.0.8 \
KAIRO_TEST_ORACLE_PORT=1521 \
KAIRO_TEST_ORACLE_USER=kairo_readonly \
KAIRO_TEST_ORACLE_PASSWORD='***' \
KAIRO_TEST_ORACLE_SERVICE=PROD \
go test -mod=vendor ./internal/dbconsole -run TestOracle11gIntegration -count=1 -v
```

SID 环境将 `KAIRO_TEST_ORACLE_CONNECT_BY=sid`；特殊字符集可再传 `KAIRO_TEST_ORACLE_CHARSET`。

| 场景 | 验收 SQL/操作 | 通过标准 |
| --- | --- | --- |
| 版本 | 连接测试 | 返回 `Oracle Database 11g` banner，连接延迟可见 |
| Service Name | 页面选择 Service Name | 可 Ping、加载 Owner、执行查询 |
| SID | 页面选择 SID | 可 Ping、加载 Owner、执行查询 |
| 中文字符 | `SELECT '中文验证' FROM dual` | 页面与 CSV 均无乱码；如实例字符集特殊，再显式配置 client charset |
| NUMBER/DATE | 查询 `NUMBER(20,2)`、`DATE`、`TIMESTAMP` | 数值和时间可读，无扫描错误 |
| CLOB/BLOB | 直接查询大对象列，再改用 `DBMS_LOB.SUBSTR(col,4000,1)` | 直接查询被拒绝并给出提示；受控预览可正常显示 |
| 元数据 | Owner → Table/View → Fields | 使用 11g 语法成功，最多返回 500 个对象 |
| 行数截断 | 查询超过页面最大行数的数据 | 数据库端最多返回“上限 + 1”行，页面仅展示上限并显示“已截断” |
| 超时/取消 | 执行慢查询后点击取消 | 浏览器停止接收，连接回到池中，后续查询正常 |
| 权限 | 用非授权 Kairo 用户访问 | 数据源不可见，直接请求返回 403 |
| DB 最小权限 | 尝试 DML/DDL 与敏感表查询 | Kairo 先拒绝写 SQL；数据库账号也拒绝未授权对象 |
| 并发 | 8 个查询并发，持续 10 分钟 | 无连接泄漏；Oracle session 数不超过配置池上限 |

## 发布门槛

1. `go test -mod=vendor ./internal/dbconsole ./internal/credentials ./internal/httpserver` 中数据库相关用例通过；
2. `node --check web/pages/database.js` 通过；
3. `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -mod=vendor .` 通过，证明无需 Oracle 本地客户端；
4. 完成上面的 Oracle 11g 实机矩阵，并把实例版本、字符集、Service/SID、耗时和问题记录到发布验收单；
5. 先给少量只读账号灰度，观察 Oracle session、慢 SQL、Kairo 内存和审计日志，再扩大使用范围。
