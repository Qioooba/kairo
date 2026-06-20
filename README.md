<p align="center">
  <img src="docs/banner.svg" alt="OpsToolbox · 内网运维工具箱" width="100%">
</p>

# OpsToolbox · 内网运维工具箱

> Windows 绿色版 · 纯 Go · 单 exe · 启动即用 · 默认仅本机访问

一个面向内网运维场景的本地工具箱。当前**第一阶段**实现了：

- 首页导航（深色科技风卡片宫格）
- 配置文件读取（`config.yaml`，白名单校验）
- **WebSphere 日志助手**：测试连接、列出日志文件、下载最新日志、关键词搜索、查看上下文
- 报文格式化：JSON / XML 格式化、压缩、校验
- 系统配置只读展示
- 本地审计日志（`logs/audit.log`，不写密码）

> 第二阶段计划：日期 / 日历、文本处理、常用命令模板、操作历史、密码本地保存（DPAPI）、多文件 zip 打包下载等。
> 第二阶段**未开始**。当前重点是把第一阶段跑稳并通过验收。验收清单见 [`docs/ACCEPTANCE.md`](docs/ACCEPTANCE.md)。

---

## 1. 给同事发什么

打包一个目录，里面是：

```
OpsToolbox.exe              # 主程序（Win10/11）
# 或者 OpsToolbox_win7.exe  # Win7 兼容版（用 Go 1.20 编译）
config.yaml                 # 配置文件（按需改 IP / 用户名 / 路径 / 模式）
README.md                   # 本文件
downloads/                  # 下载文件保存目录
logs/                       # 程序与审计日志
data/                       # 预留数据目录
```

**不要**把源码、scripts、docs、dist 目录发出去。

打包方式：

```bash
# macOS / Linux
./scripts/build_windows_amd64.sh v0.1.0      # Win10/11
GO120_HOME=~/sdk/go120 ./scripts/build_windows_amd64_win7_go120.sh v0.1.0  # Win7
./scripts/package_windows.sh v0.1.0
# → dist/ops-toolbox-v0.1.0-windows.zip
```

---

## 2. Windows 上怎么跑

**不要先双击 exe**，先用 cmd 启动看完整日志：

```bat
cd /d D:\ops-toolbox
OpsToolbox.exe
```

正常启动后控制台会输出：

```
[OpsToolbox] 2026/06/19 10:00:00.000 工具箱已启动: http://127.0.0.1:18080
[OpsToolbox] 2026/06/19 10:00:00.000 工作目录: D:\ops-toolbox
[OpsToolbox] 2026/06/19 10:00:00.000 下载目录: D:\ops-toolbox\downloads
[OpsToolbox] 2026/06/19 10:00:00.000 审计日志: D:\ops-toolbox\logs\audit.log
```

浏览器自动打开 `http://127.0.0.1:18080`。如果没自动打开，手动访问这个地址。

关闭程序：直接关闭控制台窗口。

---

## 3. 改 config.yaml

打开 `config.yaml`，按需修改：

```yaml
app:
  name: 内网运维工具箱
  host: 127.0.0.1                # 必须是 127.0.0.1 或 localhost，0.0.0.0 启动会被拒
  port: 18080                    # 端口被占用就换一个
  auto_open_browser: true
  download_dir: ./downloads
  log_dir: ./logs
  data_dir: ./data

systems:
  - name: 信贷生产                # 业务系统名
    description: WebSphere 生产集群
    servers:
      - name: prod-node-1
        host: 10.10.10.11
        port: 22
        username: wasadmin       # 页面也可以覆盖
        auth_type: password      # 第一版仅支持 password
        log_dirs:
          - name: server1日志
            path: /opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1
            patterns:
              - SystemOut*.log
              - SystemErr*.log
              - "*.log"
            encoding: utf-8      # utf-8 (默认) 或 gbk
```

**重要约束**：

- 密码**不要**写在这里 — 每次在 Web 页面输入
- `log_dirs.path` 是白名单，工具只访问这里声明的目录
- `patterns` 禁止包含 `;` `|` `&` `\` `` ` `` `$` `(` `)` `<` `>` `"` `'` `*` `?` 等
- `encoding` 仅支持 `utf-8` / `gbk` / `gb18030`
- 远程日志是 GBK 时：`encoding: gbk`（自动转 UTF-8 给页面）

改完保存即可，**不用重启**…等等 — 抱歉，第一版**需要重启**。下个版本再热加载。

---

## 4. macOS 上交叉编译 Windows exe

```bash
cd ops-toolbox

# 主版本（Win10/11，需要 Go 1.20+ 工具链，本机默认 Go 1.22+）
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o OpsToolbox.exe .
```

或者用脚本：

```bash
./scripts/build_windows_amd64.sh v0.1.0
./scripts/package_windows.sh v0.1.0
# → dist/ops-toolbox-v0.1.0-windows.zip
```

### 4.1 编译 Win7 兼容版

Go 1.21+ **不再支持** Windows 7/8/Server 2008/2012（要求 Win10/Server 2016+）。
要兼容 Win7，**必须**用 Go 1.20.x 编译。

```bash
# 1) 准备一个独立的 Go 1.20.x（不要 brew install）
curl -L -o /tmp/go1.20.14.darwin-amd64.tar.gz \
  https://go.dev/dl/go1.20.14.darwin-amd64.tar.gz
mkdir -p ~/sdk/go120
tar -C ~/sdk/go120 -xzf /tmp/go1.20.14.darwin-amd64.tar.gz --strip-components=1

# 2) 编译
export GO120_HOME=~/sdk/go120
./scripts/build_windows_amd64_win7_go120.sh v0.1.0
# → dist/ops-toolbox-v0.1.0-win7/OpsToolbox_win7.exe
```

> `go.mod` 顶部 `go 1.20` 已设置，依赖 (`x/crypto v0.21`、`x/text v0.14`) 都是 Go 1.20 兼容版本。

---

## 5. 搜索语法

搜索框支持简单表达式：

| 表达式 | 含义 |
| --- | --- |
| `Exception` | 包含 Exception |
| `ORA-00060 && userinfo` | 同时包含两个关键词（同一行） |
| `SocketTimeoutException \|\| No more data` | 任一关键词 |
| `Exception && !DEBUG` | 含 Exception 且不含 DEBUG |
| `!DEBUG` | 排除含 DEBUG 的所有行 |

第一版只支持 `&&` / `||` / `!`，括号、字段语法暂不支持。

搜索结果中点 **查看上下文**，会用 `sed -n a,bp` 拿到命中行前后各 30 行（在配置中可调），不会下载整个文件。

---

## 6. 远程命令兼容性

工具依赖服务器端以下命令（除 `timeout` 外）：

| 命令 | 用途 | 必需 |
| --- | --- | --- |
| `find` | 列日志文件 | 是 |
| `grep` | 搜索 | 是 |
| `sed` | 取上下文行 | 是 |
| `sh` | 跑 shell | 是 |
| `sort` | 排序 | 是（一般 coreutils 自带） |
| `head` | 截断 | 是 |
| `cat` | 纯 NOT 模式时 | 是（busybox 也有） |
| `timeout` | ~~旧版用~~ | **否**（已移除，依赖 Go 侧 ctx） |

**`timeout` 命令不需要安装**。第一版原来在命令模板里带 `timeout`，后来改成了 Go 客户端的 `context + 内部 timer` + 超时后 `SIGTERM → 1s → SIGKILL` 三段式，保证老 Linux / Alpine / 精简镜像也能跑。

如要排查命令兼容性，可以在目标服务器上手工跑一下：

```bash
cd /opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1
find . -maxdepth 1 -type f \( -name "SystemOut*.log" -o -name "*.log" \) \
  -printf '%s\t%T@\t%p\n' | sort -k2,2nr | head -n 100
```

如果这条命令在远端能跑出文件列表，那工具就能列出文件。

---

## 7. 编码（GBK / UTF-8）

老 WebSphere / Oracle / JSP 系统日志经常是 **GBK** 编码。

工具默认按 UTF-8 解码，遇到中文乱码时：

```yaml
log_dirs:
  - name: 老系统日志
    path: /opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1
    patterns:
      - SystemOut*.log
    encoding: gbk        # 加这一行
```

支持的值：`utf-8`（默认）、`gbk`、`gb18030`。
底层用 `golang.org/x/text/encoding/simplifiedchinese.GBK` 自动转换。

> 远程命令**不会**去改文件编码 — 只是在拿到 stdout 之后在 Go 侧做转换。

---

## 8. 接口列表

| Method | Path | 用途 |
| --- | --- | --- |
| GET  | `/api/config` | 读取配置（不返回密码） |
| POST | `/api/ssh/test` | 测试 SSH 连接 |
| POST | `/api/logs/list` | 列出日志目录下的文件 |
| POST | `/api/logs/download-latest` | 下载最近 1~5 个日志文件 |
| POST | `/api/logs/search` | 关键词搜索 |
| POST | `/api/logs/context` | 查看某行的上下文 |
| POST | `/api/format/json` | JSON 格式化 / 压缩 / 校验 |
| POST | `/api/format/xml` | XML 格式化 / 压缩 |
| GET  | `/downloads/<file>` | 下载本地 downloads/ 中的文件 |

所有接口都只允许本机访问（`127.0.0.1`）。

---

## 9. 审计日志

所有用户操作（连接、列文件、下载、搜索、失败信息）都会写入 `logs/audit.log`，例如：

```
ts=2026-06-19 10:30:00.123 op=ssh.test system=信贷生产 server=prod-node-1 result=ok
ts=2026-06-19 10:30:05.456 op=logs.list system=信贷生产 server=prod-node-1 dir=/opt/... result=ok count=12
ts=2026-06-19 10:30:10.789 op=logs.search system=信贷生产 server=prod-node-1 dir=/opt/... query=Exception && userinfo result=ok hits=8
ts=2026-06-19 10:30:12.000 op=logs.download system=信贷生产 server=prod-node-1 dir=/opt/... file=SystemOut.log result=ok bytes=2048576
```

**不记录的**：SSH 密码、日志正文。

---

## 10. 安全设计

1. HTTP 服务只监听 `127.0.0.1`（`0.0.0.0` 会被配置校验拒绝）
2. 不允许用户输入任意 shell 命令
3. 所有远程命令由后端固定模板生成（`find` / `grep` / `sed` / `sort` / `head` 组合）
4. 目录必须来自配置白名单，文件名只能来自 `find` 列出结果
5. 搜索关键词做严格转义 + 白名单校验，禁止 ` ' \` $ ; & | < > ( ) { } [ ]` 等
6. 密码不会写入审计日志，也不会出现在错误信息中
7. 不提供删除 / 修改 / 上传远程文件的能力
8. 下载文件只保存到 `downloads/`，URL 路径穿越会被拒绝
9. 命令超时由客户端 ctx + SIGTERM/SIGKILL 控制，不依赖服务器端 `timeout`

---

## 11. 第一阶段未做

- 密码本地保存（后续可考虑 Windows DPAPI）
- 多文件 zip 打包下载
- 实时 tail
- 任意命令执行（设计为禁止）
- 多服务器并发搜索（当前顺序）
- 复杂日历（工作日 / 节假日）
- 数据库连接
- 文本处理 / 常用命令模块（占位页面）

---

## 12. 常见问题

**Q: 启动后浏览器没自动打开？**
A: 检查 `config.yaml` 的 `auto_open_browser: true`；或者手动访问 `http://127.0.0.1:18080`。

**Q: 端口被占用？**
A: 修改 `app.port`，例如改成 `18090`。

**Q: 搜索中文 / 特殊字符失败？**
A: 含 `;` `|` `&` `$` `\` `` ` `` 等的关键词会被直接拒绝（不是过滤，是拒绝）；去掉这些字符。

**Q: 中文乱码？**
A: 远端日志是 GBK 的话，在 `log_dirs` 里加 `encoding: gbk`。

**Q: 改了 config.yaml 不生效？**
A: 当前需要重启 OpsToolbox.exe。下版本做热加载。

**Q: 想在多台服务器上并发搜索？**
A: 第一版请在页面上一次选一台，逐一搜索。第二阶段会加并发开关。

**Q: Win7 上跑不起来？**
A: 必须用 Go 1.20.x 编译的 `OpsToolbox_win7.exe`，主版本 `OpsToolbox.exe` 在 Win7 上跑不起来（Go 1.21+ 不再支持 Win7）。
