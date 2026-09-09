# Oracle LOB 实现审查与真实环境测试

日期：2026-09-09。范围：用户提供的 Oracle 双驱动、LOB 投影、事务及下载方案。工作区原先已有大量未提交修改，本次没有提交或覆盖其他工作。

## 已修复的核心问题

- 统一 Oracle 后端选择与纯 Go 连接入口；显式 OCI 配置失败不再悄悄降级，纯 Go 直连不再注入空拨号器。修正 SID 描述符和客户端版本检测。
- 抽出共享 LOB 执行层：元数据校验、定位查询、正文读取使用同一连接或原事务，消除单连接池嵌套借连接造成的阻塞。失效事务直接报错。
- 修复真实 OCI 测试暴露的两类原生崩溃：借用的 LOB reader 被重复释放，以及请求取消时 Rows 被异步关闭而读取仍在使用原生句柄。游标统一管理生命周期，读取按块检查取消。
- 删除宽泛 SQL 拆词重组逻辑。只对能够完整识别的单表星号查询做投影，其他 SQL 原样执行，保留运算符、提示、绑定变量等语义。排除视图、临时表和 IOT 的自动投影。
- LOB 引用使用短期签名 token；主键优先，精确保留大整数、二进制和时间键。下载不再接受浏览器伪造的列、定位键或事务覆盖；重新检查数据源访问及数据库用户。
- 流式读取失败不再回放查询或生成成功的空文件；输出前错误返回错误状态，部分输出后错误中断传输。统一实际字节上限。
- 修复 CLOB/NCLOB 分块偏移和多字节边界，区分 NULL 与 EMPTY LOB。LONG 类型不再错误套用 DBMS_LOB。
- 前端预览按字节限制，避免切断 UTF-8；字符长度与字节大小分开显示，NCLOB 标题正确，LOB 入口支持键盘操作。
- 真实翻页发现自定义“每页 3 行”会被固定选项覆盖，现统一使用查询页状态，并显示自定义页大小。
- Windows 构建脚本默认要求 CGO/C 编译器，缺失时明确失败，不再静默生成无 OCI 能力的版本。

## 真实环境

Windows x64；Oracle XE 21.3.0.0.0，PDB XEPDB1；真实 OCI 21.3 x64 客户端。测试数据源使用 Windows 凭据库中的本地实验账号，不把密码写入测试脚本。

独立应用位于 `D:\kairo\tmp\oracle-review`，端口 18095，可执行文件 `kairo-oci-verified.exe`，前端从 `D:\kairo\web` 加载。原应用端口 18092 未替换。浏览入口：http://127.0.0.1:18095/#/database。

夹具脚本：`tests/oracle-lob-review.sql`。新建表 `KAIRO_LAB.KAIRO_LOB_REVIEW_0908`，保留供复测。9 行 × CLOB/BLOB/NCLOB，包含 NULL、EMPTY、中文、emoji、换行、零字节和高位二进制；最大文本 1,966,080 字节，最大 BLOB 1,048,576 字节。

## 验证结果

| 检查 | 结果 |
| --- | --- |
| 纯 Go `go test ./... -count=1 -timeout 3m` | 通过；有环境条件的其他集成测试可能跳过 |
| CGO `go test ./internal/dbconsole ./internal/httpserver -count=1` | 通过 |
| `node --check web/pages/database.js` | 通过 |
| `node tests/workbench-unit-tests.js` | 通过 |
| go-ora 真实 Oracle 集成测试 | 通过，9.12 秒 |
| godror 真实 Oracle 集成测试 | 通过，0.87 秒 |
| 两驱动逐项比较 27 个 LOB 单元格完整内容 | 通过，包含超过 1 MB 数据 |
| 单连接池、事务未提交内容、回滚、失效事务拒绝 | 通过 |
| 写入失败、调用前取消、首块输出后取消 | 通过 |
| Codex 内置浏览器实际连接、查询、预览和下载 | 通过 |
| 浏览器自定义三行分页 | 第一页 ID 1–3，第二页 4–6，返回第一页仍为 1–3 |
| 浏览器空 CLOB | 预览为空，完整下载成功 0 B |
| 浏览器大 CLOB | 完整下载提示 1.9 MB；预览 262,143 UTF-8 字节，无替换字符 |
| 浏览器大 BLOB | 完整下载提示 1.0 MB，Hex 与夹具字节一致 |
| 浏览器 NCLOB | 中文、emoji、换行正确，显示 NCLOB 类型 |

浏览器操作通过 Codex 的真实内置浏览器完成，没有使用模拟页面或伪造 API 响应。物理视口 1920×1080；应用 125% 缩放对应 CSS 1536×864。另检查了 CSS 1920×1080 视口。截图使用当前真实页面视口坐标，PNG 为 1920×1080。

证据位于 `D:\kairo\tmp\oracle-review\artifacts`：`go-tests-portable.log`、`goora-real-test.log`、`oci-real-test.log`、`01-lob-grid-1080p.png`、`02-blob-preview-1080p.png`、`03-clob-preview-1080p.png`。

## 复测

夹具已创建，无需再次 CREATE。使用同一 Windows 用户运行：

```powershell
$env:KAIRO_LOB_REVIEW_DATA = 'D:\kairo\data'
$env:KAIRO_LOB_REVIEW_DRIVER = 'go-ora'
go test ./internal/dbconsole -run '^TestLOBRealOracle$' -v -count=1 -timeout 5m

$env:CGO_ENABLED = '1'
$env:CC = 'E:/AI/Temp/WinGet/BrechtSanders.WinLibs.POSIX.UCRT.16.1.0-14.0.0-r4/extracted/mingw64/bin/gcc.exe'
$env:KAIRO_LOB_REVIEW_DRIVER = 'godror'
$env:KAIRO_LOB_REVIEW_LIBDIR = 'D:\app\oracle\product\21.0.0\dbhomexe\bin'
go test ./internal/dbconsole -run '^TestLOBRealOracle$' -v -count=1 -timeout 5m
```

## 验证边界

实测结论限定于以上环境与用例，不能作为所有 Oracle 版本绝无问题的保证。Oracle 11g/19c、GBK 数据库、远程断网及不同 OCI 客户端组合未在本次真实环境覆盖。OCI TLS/Wallet 与 SSH 隧道的显式不支持组合会报错。

复杂 SQL 保留原始执行语义，但没有可靠行身份时不提供完整 LOB 下载。非事务查询未实现永久固定物理会话，因此不承诺跨请求保留 ALTER SESSION、包状态或临时表状态。ROWID 回退引用只有短有效期，尚无 SCN 快照绑定；下载按当前行读取，不保证与最初网格查询时完全相同的数据库快照。签名绑定数据源和数据库用户并重新鉴权，未单独绑定应用用户身份。以上与原方案的完整目标存在差距，不能宣称方案所有能力均已实现。
