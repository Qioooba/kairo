# 投喂作者排行榜 · 部署说明 (v0.14)

投喂作者页面前后端实现 + Java 后端部署参考。

## 文件清单

| 文件 | 作用 | 给谁 |
|------|------|------|
| `K_SPONSOR.sql` | Oracle 建表 + 序列 + 索引 + 触发器 + 5 条示例数据 | DBA |
| `KairoSponsorLeaderboardAction.java` | Java Action 端点实现 (调 K_SPONSOR 查排行榜) | Java 后端开发 |

## 部署步骤

### 第 1 步：DBA 跑建表 SQL

```bash
# 用 sqlplus / dbeaver 登录 Oracle, 跑 K_SPONSOR.sql 整个文件
sqlplus username/password@database @K_SPONSOR.sql
```

跑完会看到：
- `K_SPONSOR` 表已建
- `SEQ_K_SPONSOR` 序列已建
- `IDX_K_SPONSOR_TOTAL` 索引已建
- 3 个触发器 (`TRG_K_SPONSOR_TOTAL` / `TRG_K_SPONSOR_CREATED` / `TRG_K_SPONSOR_ID`) 已建
- 5 条示例数据已插入 (张三/李四/王五/赵六/钱七)

最后一行 `SELECT ... ORDER BY TOTAL DESC, ID ASC` 应该返：
```
REAL_NAME  COTTI  LUCKY  MILKTEA  TOTAL
---------  -----  -----  -------  -----
张三        4      4      1        9
李四        3      4      1        8
王五        2      3      1        6
钱七        3      2      1        6
赵六        0      4      0        4
```

注意：钱七 (ID=5) 总杯数跟王五 (ID=3) 一样 (6)，按 ID 升序排后面。

### 第 2 步：Java 后端集成

1. 把 `KairoSponsorLeaderboardAction.java` 放到跟 `KairoActivateAction.java` 同样的 package 下：
   ```
   src/cn/com/jscb/httpinterface/pc/kairo/KairoSponsorLeaderboardAction.java
   ```

2. 在 dispatcher 配置文件里注册 serviceID 跟 Action 的映射（按你们项目 dispatcher 风格）：
   ```
   KairoSponsorLeaderboardAction  →  cn.com.jscb.httpinterface.pc.kairo.KairoSponsorLeaderboardAction
   ```

3. 重新编译部署 Java 后端。

### 第 3 步：录入真实数据

Java Action 只**读取** K_SPONSOR，**不**录入。录入方式 2 选 1：

**A. 手工 SQL 录入**（最简，最适合初期几条数据）：
```sql
INSERT INTO K_SPONSOR (REAL_NAME, COTTI, LUCKY, MILKTEA)
VALUES ('某同事', 1, 2, 0);
COMMIT;
```

**B. 写个简单录入页面**（如果数量多了建议）

每加一条数据，触发器自动算 `TOTAL = COTTI + LUCKY + MILKTEA`，下次前端拉数据就显示出来。

## 端到端验证

```bash
# 1. Java 端起来后, 直接 curl 测 Action 是否通
curl -X POST \
  -H "Authorization: Basic anN5aDpqc3loQDEyMw==" \
  -H "Content-Type: application/json" \
  "http://66.0.34.199:9080/credit/httpInterface?channelID=PC&serviceID=KairoSponsorLeaderboardAction&seqNo=$(date +%s)" \
  -d '{}'
```

期望响应：
```json
{
  "ok": true,
  "entries": [
    {"rank":1,"real_name":"张三","cotti":4,"lucky":4,"milktea":1,"total":9,"date":"07-09","updated_at":"..."},
    {"rank":2,"real_name":"李四","cotti":3,"lucky":4,"milktea":1,"total":8,"date":"07-08","updated_at":"..."},
    ...
  ]
}
```

```bash
# 2. Kairo 工具箱起来, 浏览器开 "投喂作者" 页面
# 应该看到 5 条排行, 名字是 "武林神话·张三" / "一代武神·李四" / "剑道至尊·王五" / ...
```

## 跟激活服务对照

| | 激活 (`KairoActivateAction`) | 排行榜 (`KairoSponsorLeaderboardAction`) |
|---|---|---|
| serviceID | KairoActivateAction | KairoSponsorLeaderboardAction |
| URL | /credit/httpInterface | /credit/httpInterface (同) |
| 请求体 | `{secret_key, ip}` | `{}` (空) |
| 响应 | `{ok, error?}` | `{ok, entries[]}` |
| 表 | K_ACT_CODE | K_SPONSOR |
| 审计 | 写 K_AUDIT (防内鬼) | **不**审计 (只读查询) |
| 鉴权 | Basic Auth | Basic Auth (同) |

## 关键设计点

- **同款风格**：`KairoSponsorLeaderboardAction` 完全模仿 `KairoActivateAction` 的代码风格 (package/import/类签名/SQL 风格/异常处理/JSON 序列化), Java 同事 review 起来零负担
- **serviceID 是 Java 反射路由 key** —— 跟 `KairoActivateAction` 一样的设计, 走 `/credit/httpInterface` + `serviceID=` 区分
- **不需要 K_AUDIT**：排行榜只读, 没有"敏感事件" (激活防内鬼, 排行榜没必要)
- **TOTAL 触发器维护** —— 跟 K_ACT_CODE 写 USED_AT 触发器同款思路, 避免应用层忘记算
- **ROW_NUMBER 同分按 ID 升序** —— 保证稳定, 避免刷新页面时名次跳来跳去
- **前 50 名跟前端 NICKNAMES 1:1** —— 超过 50 名后端会返真实姓名但没昵称, 前端代码 `var nick = NICKNAMES[(e.rank || 1) - 1]; var displayName = nick ? (nick + '·' + e.real_name) : e.real_name;` 自动降级
- **3 列字段命名** 跟前端 brand id 完全一致 (cotti/lucky/milktea), 跟 mock-sponsor-server 一致, 跟前端 NICKNAMES + 真实姓名渲染逻辑一致

## Go 端相关文件 (参考)

如果 Java 端想了解 Go 端怎么调，可以看这些文件了解契约：

| 文件 | 作用 |
|------|------|
| `internal/sponsor/client.go` | Go 端调 Java 的 HTTP 客户端 |
| `internal/sponsor/types.go` | Go 端反序列化 Java 响应的 struct |
| `internal/httpserver/handlers_sponsor.go` | Go 端 /api/sponsor/leaderboard handler |
| `cmd/mock-sponsor-server/main.go` | Go 端 mock server (开发期替代 Java 端) |
| `config.yaml` `internal_endpoints.sponsor_leaderboard` 段 | Java 端地址配置 |
| `web/pages/sponsor.js` | 前端页面 (含 NICKNAMES 数组 + 渲染逻辑) |
