# Kairo 工具箱 - License 激活系统 (v1.0)

> 防止工具箱被未授权传播给未授权人。

## 整体架构

```
┌────────────────────────────────────────────────────────────────┐
│                                                                │
│   [浏览器前端]                                                  │
│     └─ web/license.js  激活弹窗 UI                              │
│          │                                                     │
│          │ POST /api/license/activate {code}                    │
│          ↓                                                     │
│   [Go 端 ops-toolbox]                                          │
│     ├─ internal/license/     核心 license 逻辑                 │
│     ├─ internal/httpserver/  转发端点                           │
│     │   handlers_license.go                                     │
│     └─ main.go               启动时调 Check()                  │
│          │                                                      │
│          │ HTTP POST {secret_key, ip} + Header + URL 参数        │
│          ↓ (主地址失败 → 备地址)                                 │
│                                                                │
│   [Java 端 WebSphere]                                          │
│     └─ /kairo/auth/activate                                    │
│          │                                                      │
│          ↓ SQL                                                   │
│   [Oracle K_ACT_CODE 表]                                       │
│     CODE | IP | USED_AT                                         │
│                                                                │
└────────────────────────────────────────────────────────────────┘
```

## 安全链

| 攻击 | 拦截点 |
|------|--------|
| 拿到二进制直接给 B 跑 | B 启动 → 没有本地证书 → 弹激活窗 → 没激活码过不了 |
| A 把 license.dat 复制给 B | B 启动 → GCM AAD 校验失败（AAD=IP+MAC，B 机器的 MAC 不同）→ 走激活 → 用 cert.code 重激活 → Java 端 IP 不匹配 → **拒绝** |
| A 把 license.dat + secret 复制给 B（B 知道 secret 但 IP 不同）| 同上 |
| 攻击者反编译 Go 找 AES key，能伪造 cert | 必须**同时**知道受害者 IP 和 MAC 才能构造有效 cert。攻击者用错 MAC → AAD 校验失败 → 解密失败 |
| 多个机器同时拿同一个 code 第一次激活（并发竞态）| Java 端 UPDATE 检查影响行数, 0 行 → 当作"被抢" → 拒绝 + 写审计 |
| 攻击者反编译并改二进制 | 成本太高，不如找用户要激活码 |
| 内部人员想给朋友用 | 没激活码过不了 → 必须找用户拿 |

### AAD 设计 (防伪造核心)

```go
AAD = IP + "|" + MAC fingerprint (sha256[:8] hex)
```

- 攻击者拿到 AES key 也没用，必须同时知道受害者 IP **和** MAC
- IP 通过内网扫描能拿到（不算秘密）
- MAC 通过 ARP 扫描也能拿到（也不算秘密）
- 但**两个都猜对** + **主动发给特定受害者** + **对方接受并使用** → 门槛足够拦住同事级别的威胁

### 并发安全 (漏洞 #9 修复)

```java
int rowsAffected = tx.execute(updateSql);
if (rowsAffected == 0) {
    // 并发竞态: 我们看到 IP 是空, 但 UPDATE 时已被别人抢了
    // 再查一次确认, 写 REJECT_IP_MISMATCH 审计, 返回拒绝
}
```

测试覆盖: `TestIntegration_ConcurrentActivate` - 5 个不同 IP 同时激活同一个 code, 恰好 1 个成功, 4 个被拒。

## 启动顺序（启动一次 Check()）

```
1. 开发者白名单:  config.yaml 的 kairo 字段 == "111222"? → 直接通过
2. 本地证书:      ~/.kairo/license.dat 能解密 + IP 匹配? → 通过
3. 否则:          前端弹激活窗 → 用户输码 → POST 给 Java → 成功 reload
```

## 文件清单

| 文件 | 类型 | 作用 |
|------|------|------|
| `internal/license/license.go` | 新增 | 主入口: Check / Activate / GetStatus |
| `internal/license/bypass.go` | 新增 | 开发者白名单 (key=kairo, value=111222) |
| `internal/license/fingerprint.go` | 新增 | 取本机 IP |
| `internal/license/crypto.go` | 新增 | 本地证书 AES-GCM 加密/解密 |
| `internal/license/server.go` | 新增 | 调 Java 端的 HTTP 客户端 (主备 fallback) |
| `internal/license/license_test.go` | 新增 | 单元测试 (白名单/加密/IP不匹配等) |
| `internal/config/config.go` | 改 | 加 `KairoInternalToken` 字段 |
| `internal/httpserver/handlers_license.go` | 新增 | `/api/license/status` + `/api/license/activate` |
| `internal/httpserver/httpserver.go` | 改 | 注册 license 路由 |
| `main.go` | 改 | 注入 license 的 config provider + 启动时 Check() |
| `cmd/mock-license-server/main.go` | 新增 | 本地模拟 Java 服务端 (跑测试用) |
| `web/license.js` | 新增 | 激活弹窗 UI |
| `web/index.html` | 改 | 引入 license.js |
| `docs/KairoActivateAction.java` | 新增 | Java 服务端参考实现 (你复制到项目里改) |

## 数据库 (Oracle)

```sql
-- 激活码表 (核心)
CREATE TABLE K_ACT_CODE (
    CODE      VARCHAR2(64) PRIMARY KEY,
    IP        VARCHAR2(64),
    USED_AT   DATE
);
COMMENT ON TABLE K_ACT_CODE IS 'Kairo 工具箱激活码表';
COMMENT ON COLUMN K_ACT_CODE.CODE IS '激活码';
COMMENT ON COLUMN K_ACT_CODE.IP IS '首次激活的客户端 IP, NULL=未激活';
COMMENT ON COLUMN K_ACT_CODE.USED_AT IS '激活时间';

-- 审计日志表 (防内鬼: 记录所有激活行为, 特别是 IP 不匹配的拒绝事件)
CREATE TABLE K_AUDIT (
    ID         NUMBER(18)    NOT NULL,
    EVT        VARCHAR2(32)  NOT NULL,
    CODE       VARCHAR2(64),
    IP         VARCHAR2(64),
    INFO       VARCHAR2(255),
    CREATED_AT DATE          DEFAULT SYSDATE,
    CONSTRAINT PK_K_AUDIT PRIMARY KEY (ID)
);
CREATE INDEX IDX_K_AUDIT_CODE     ON K_AUDIT(CODE);
CREATE INDEX IDX_K_AUDIT_EVT      ON K_AUDIT(EVT);
CREATE INDEX IDX_K_AUDIT_CREATED  ON K_AUDIT(CREATED_AT);
CREATE SEQUENCE SEQ_K_AUDIT START WITH 1 INCREMENT BY 1;
COMMENT ON TABLE K_AUDIT IS 'Kairo 工具箱激活审计日志 (防内鬼 - 记录所有激活请求, 特别是异常拒绝)';
COMMENT ON COLUMN K_AUDIT.EVT IS '事件类型: ACTIVATE_OK / REACTIVATE_OK / REJECT_IP_MISMATCH / REJECT_CODE_INVALID / REJECT_BAD_AUTH';
COMMENT ON COLUMN K_AUDIT.CODE IS '涉及的激活码';
COMMENT ON COLUMN K_AUDIT.IP IS '请求方 IP';
COMMENT ON COLUMN K_AUDIT.INFO IS '详细信息, 如 DB IP 与请求 IP 的对比 / 失败原因';
```

**手动发激活码**（你给每个同事一个）:
```sql
INSERT INTO K_ACT_CODE (CODE) VALUES ('zhangsan-001');
INSERT INTO K_ACT_CODE (CODE) VALUES ('lisi-002');
INSERT INTO K_ACT_CODE (CODE) VALUES ('wangwu-003');
```

**查可疑记录**（内鬼检测）:
```sql
-- 同一激活码被多个 IP 尝试 (重点关注!)
SELECT CODE, COUNT(DISTINCT IP) AS ip_count, LISTAGG(IP, ',') WITHIN GROUP (ORDER BY CREATED_AT) AS ips
FROM K_AUDIT
WHERE EVT IN ('REJECT_IP_MISMATCH', 'ACTIVATE_OK')
GROUP BY CODE
HAVING COUNT(DISTINCT IP) > 1
ORDER BY MAX(CREATED_AT) DESC;

-- 最近 IP 不匹配的拒绝记录
SELECT CREATED_AT, CODE, IP, INFO
FROM K_AUDIT
WHERE EVT = 'REJECT_IP_MISMATCH'
ORDER BY CREATED_AT DESC
FETCH FIRST 50 ROWS ONLY;
```

## Java 端 (你部署到 WebSphere 项目里)

参考实现: `docs/KairoActivateAction.java`

接口路径: `POST /kairo/auth/activate`

请求格式:
```
POST /kairo/auth/activate?k1=v1&k2=v2&k3=v3 HTTP/1.1
Authorization: Basic <固定字符串>
Content-Type: application/json

{"secret_key": "<激活码>", "ip": "<客户端 IP>"}
```

返回:
```json
{"ok": true}
{"ok": false, "error": "激活码已被其他机器使用"}
```

**你部署时改 3 处**:
1. `BASIC_AUTH_TOKEN` 常量 → 你跟 Go 端约定的 Basic 认证串
2. `Transaction.current()` / `tx.getResultSet()` / `tx.execute()` → 你们项目里的实际 API
3. `Logger.log()` / `System.out.println()` → 你们项目里的日志工具

## Go 端编译 (重要)

**占位符需要替换**:
- `LicenseServerPrimary` / `LicenseServerSecondary`: Java 服务地址
- `BasicAuthHeader`: Basic 认证串 (跟 Java 端 BASIC_AUTH_TOKEN 一致)
- `URLParamK1` / `URLParamV1` / `URLParamK2` / `URLParamV2` / `URLParamK3` / `URLParamV3`: URL 上 3 个固定 KV 参数

```bash
go build -ldflags "\
  -X 'kairo/internal/license.LicenseServerPrimary=http://10.0.0.5:8080/kairo/auth/activate' \
  -X 'kairo/internal/license.LicenseServerSecondary=http://10.0.0.6:8080/kairo/auth/activate' \
  -X 'kairo/internal/license.BasicAuthHeader=YOUR_BASIC_TOKEN' \
  -X 'kairo/internal/license.URLParamK1=k1' -X 'kairo/internal/license.URLParamV1=v1' \
  -X 'kairo/internal/license.URLParamK2=k2' -X 'kairo/internal/license.URLParamV2=v2' \
  -X 'kairo/internal/license.URLParamK3=k3' -X 'kairo/internal/license.URLParamV3=v3'" \
  -o kairo
```

## 部署流程

1. **建表**: Oracle 里跑上面的 CREATE TABLE
2. **Java 端**: 把 `docs/KairoActivateAction.java` 复制到 WebSphere 项目，按上面说的 3 处改
3. **批量发激活码**: SQL 插入 N 条记录
4. **编译 Go 端**: 按上面 ldflags 命令注入你的真实地址 / 认证串 / 参数
5. **开发者自用**: 你的 `config.yaml` 加 `app: { kairo: "111222" }` 一行
6. **打包给同事**: 不要带这一行

## 测试

### 单元测试
```bash
cd ops-toolbox
go test ./internal/license/ -v
```

### 集成测试 (本地 mock 服务端)
```bash
# 终端 1: 启动 mock 服务端
go run ./cmd/mock-license-server -addr :18091 -auth TEST_TOKEN_123

# 终端 2: 启动 ops-toolbox, 编译时指向 mock 服务
go run -ldflags "\
  -X 'kairo/internal/license.LicenseServerPrimary=http://localhost:18091/kairo/auth/activate' \
  -X 'kairo/internal/license.BasicAuthHeader=TEST_TOKEN_123'" .
```

### 场景验证清单

| 场景 | 验证方法 |
|------|---------|
| 开发者白名单生效 | config.yaml 加 `kairo: "111222"` → 启动 → 不弹框 |
| 本地证书有效 | 激活成功后再次启动 → 不弹框 |
| 首次激活 | 删 `~/.kairo/license.dat` → 启动 → 弹框 → 输码 → 激活成功 |
| 重激活 | 已经激活 → 删 `~/.kairo/license.dat` → 启动 → 弹框 → 输同一个码 → 成功 |
| 激活码无效 | 弹框 → 输 `wrong-code` → Java 返回 "激活码无效" |
| IP 不匹配 | A 机器激活 → 删 A 的 license.dat → 把 A 的 license.dat 复制到 B → B 启动 → 弹框 → 输 A 的码 → Java 拒绝 |
| MAC 不匹配 (AAD) | 攻击者反编译 + 知道 IP 但不知道 MAC → 构造的 cert 在目标机器 AAD 校验失败 → 启动失败 |
| 并发激活 | 5 个不同 IP 同时拿同一个 code 激活 → 恰好 1 个成功, 4 个被拒 + 写审计 |
| 主地址挂了 | 关掉 mock server 主实例 (如果有备地址) → 客户端自动切备地址 |
| 特殊字符激活码 | code 含单引号 / 中文 / emoji 都能正确加解密 |
| 空激活码 | 客户端直接拒绝, 不发请求 |
| 内鬼检测 | 查 `K_AUDIT WHERE EVT='REJECT_IP_MISMATCH'` 或 `/admin/suspicious` 端点 |

## 部署后续

你给我以下值，我替换占位符重新编译:
1. Java 服务的主地址 + 备用地址
2. Basic 认证串
3. URL 上 3 个 KV 参数的 key 和 value

替换后重新 `go build` 一次，发给同事即可。