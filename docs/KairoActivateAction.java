package com.kairo.auth;

import java.io.BufferedReader;
import java.io.IOException;
import java.util.HashMap;
import java.util.Map;
import javax.servlet.http.HttpServletRequest;

import com.kairo.framework.JsonUtil;     // 替换为你们项目里的 JSON 工具类
import com.kairo.framework.Logger;       // 替换为你们项目里的日志工具
import com.kairo.framework.Transaction;  // 替换为你们项目里的事务对象

/**
 * Kairo 工具箱激活接口（v1.0 + 审计日志）。
 *
 * <p>接入方式：根据你们项目的 Action / Servlet 框架，把 {@link #execute} 路由到
 * {@code POST /kairo/auth/activate}。调用方式：框架反射 / XML 配置均可。
 *
 * <h3>请求格式</h3>
 * <pre>
 * POST /kairo/auth/activate?k1=v1&k2=v2&k3=v3 HTTP/1.1
 * Host: ...
 * Authorization: Basic &lt;固定字符串&gt;
 * Content-Type: application/json
 *
 * {"secret_key":"&lt;激活码&gt;", "ip":"&lt;客户端 IP&gt;"}
 * </pre>
 *
 * <h3>响应格式</h3>
 * <pre>
 * {"ok": true}                                       // 通过
 * {"ok": false, "error": "激活码无效"}                // 业务拒绝
 * </pre>
 *
 * <h3>依赖的数据库表</h3>
 * <pre>
 * CREATE TABLE K_ACT_CODE (
 *     CODE      VARCHAR2(64) PRIMARY KEY,
 *     IP        VARCHAR2(64),
 *     USED_AT   DATE
 * );
 *
 * CREATE TABLE K_AUDIT (
 *     ID         NUMBER(18) PRIMARY KEY,
 *     EVT        VARCHAR2(32) NOT NULL,
 *     CODE       VARCHAR2(64),
 *     IP         VARCHAR2(64),
 *     INFO       VARCHAR2(255),
 *     CREATED_AT DATE DEFAULT SYSDATE
 * );
 * CREATE SEQUENCE SEQ_K_AUDIT START WITH 1 INCREMENT BY 1;
 * </pre>
 *
 * <h3>使用说明</h3>
 * <ol>
 *   <li>把 {@link #BASIC_AUTH_TOKEN} 替换为你们约定的 Basic 认证串</li>
 *   <li>把 {@link #execute} 方法体里的框架调用（Transaction / JsonUtil / Logger）
 *       替换为你们项目里的对应实现</li>
 *   <li>URL 上的 3 个 query 参数 k1/v1, k2/v2, k3/v3 由 Go 客户端自动拼接，
 *       这里不用处理</li>
 *   <li>{@link #writeAudit} 方法里的 SEQ_K_AUDIT.NEXTVAL 按你们项目序列号生成方式调整</li>
 * </ol>
 */
public class KairoActivateAction {

    /**
     * Basic 认证固定字符串（放在 Authorization: Basic 后面）。
     * 由 Go 客户端配置 ldflags 注入相同的字符串。
     * TODO: 跟 Go 端同步这个值（用户后续会给具体字符串）
     */
    private static final String BASIC_AUTH_TOKEN = "PLACEHOLDER_BASIC_AUTH_TOKEN";

    /**
     * 审计事件类型常量。
     */
    private static final String EVT_ACTIVATE_OK         = "ACTIVATE_OK";
    private static final String EVT_REACTIVATE_OK       = "REACTIVATE_OK";
    private static final String EVT_REJECT_IP_MISMATCH  = "REJECT_IP_MISMATCH";
    private static final String EVT_REJECT_CODE_INVALID = "REJECT_CODE_INVALID";
    private static final String EVT_REJECT_BAD_AUTH     = "REJECT_BAD_AUTH";

    /**
     * Action 入口方法。根据你们框架签名调整（servlet / struts / spring mvc 都行）。
     *
     * @param req    HTTP 请求（用于读取 Header）
     * @param params 已解析好的请求体 Map（key 是 JSON 字段名）
     * @return 响应 Map，框架负责序列化为 JSON
     */
    public Map<String, Object> execute(HttpServletRequest req, Map<String, Object> params) {
        Map<String, Object> resp = new HashMap<>();

        // 1. 校验 Basic 认证头
        String auth = req.getHeader("Authorization");
        if (auth == null || !auth.startsWith("Basic ")) {
            writeAudit(null, null, EVT_REJECT_BAD_AUTH, "missing Basic auth header");
            resp.put("ok", false);
            resp.put("error", "未授权");
            return resp;
        }
        String token = auth.substring("Basic ".length()).trim();
        if (!BASIC_AUTH_TOKEN.equals(token)) {
            writeAudit(null, null, EVT_REJECT_BAD_AUTH, "bad auth token: " + maskToken(token));
            resp.put("ok", false);
            resp.put("error", "认证失败");
            return resp;
        }

        // 2. 取参数
        String code = (String) params.get("secret_key");
        String ip   = (String) params.get("ip");

        if (code == null || code.trim().isEmpty() || ip == null || ip.trim().isEmpty()) {
            writeAudit(code, ip, "REJECT_BAD_PARAMS", "missing secret_key or ip");
            resp.put("ok", false);
            resp.put("error", "参数缺失 (secret_key / ip)");
            return resp;
        }

        code = code.trim();
        ip   = ip.trim();

        // 3. SQL 拼接前做基本转义（防单引号语法错，不是防注入 - 你们项目不 care 注入）
        String codeSafe = escapeSingleQuote(code);
        String ipSafe   = escapeSingleQuote(ip);

        // 4. 业务逻辑 - 用你们项目里的 Transaction 对象获取
        Transaction tx = Transaction.current(); // 替换为你们项目里获取事务的方式
        try {
            // 4.1 查激活码
            String sql1 = "SELECT CODE, IP, USED_AT FROM K_ACT_CODE WHERE CODE = '" + codeSafe + "'";
            Object rs = tx.getResultSet(sql1); // 替换为你们项目里的 ResultSet 调用

            String dbIp  = null;
            boolean found = false;

            // 4.2 遍历 ResultSet（替换为你们项目里的遍历方式）
            while (rsHasNext(rs)) {
                dbIp  = rsGetString(rs, "IP");
                found = true;
                break;
            }
            closeRs(rs);

            // 4.3 激活码不存在 - 写审计
            if (!found) {
                writeAudit(code, ip, EVT_REJECT_CODE_INVALID, "code not found");
                resp.put("ok", false);
                resp.put("error", "激活码无效");
                return resp;
            }

            // 4.4 首次激活（IP IS NULL）- 必须检查 UPDATE 影响行数防并发竞态!
            //
            // 并发场景: A 和 B 同时拿 code 第一次激活
            //   - A 先到: SELECT IP IS NULL → UPDATE 影响 1 行 → 成功
            //   - B 后到: SELECT IP IS NULL (A 还没 commit? 或者事务隔离级别问题) → UPDATE 影响 0 行
            //   - 如果不检查影响行数, B 会错误地认为自己激活成功
            //
            // 修复: 检查 UPDATE 返回的影响行数, 0 行 → 当作"已被其他机器使用"处理
            if (dbIp == null || dbIp.trim().isEmpty()) {
                String updateSql = "UPDATE K_ACT_CODE SET IP = '" + ipSafe +
                                   "', USED_AT = SYSDATE WHERE CODE = '" + codeSafe +
                                   "' AND IP IS NULL";
                int rowsAffected = tx.execute(updateSql);  // 返回 int 表示影响行数
                if (rowsAffected == 0) {
                    // 并发场景: 我们看到 IP 是空, 但 UPDATE 时已经被别人抢了
                    // 再查一次确认实际状态
                    String sqlCheck = "SELECT IP FROM K_ACT_CODE WHERE CODE = '" + codeSafe + "'";
                    Object rsCheck = tx.getResultSet(sqlCheck);
                    String actualIp = null;
                    while (rsHasNext(rsCheck)) {
                        actualIp = rsGetString(rsCheck, "IP");
                        break;
                    }
                    closeRs(rsCheck);

                    writeAudit(code, ip, EVT_REJECT_IP_MISMATCH,
                              "concurrent activate race, actual_ip=" + actualIp);
                    resp.put("ok", false);
                    resp.put("error", "激活码已被其他机器使用 (并发激活冲突)");
                    return resp;
                }
                writeAudit(code, ip, EVT_ACTIVATE_OK, "first activate");
                resp.put("ok", true);
                return resp;
            }

            // 4.5 IP 不匹配 → 已被其他机器使用 - 写审计 (这是防内鬼的关键点!)
            if (!dbIp.trim().equals(ip)) {
                String info = "db_ip=" + dbIp + " req_ip=" + ip + " (可能内鬼给了别人)";
                writeAudit(code, ip, EVT_REJECT_IP_MISMATCH, info);
                resp.put("ok", false);
                resp.put("error", "激活码已被其他机器使用");
                return resp;
            }

            // 4.6 重激活（同 IP）
            String updateSql = "UPDATE K_ACT_CODE SET USED_AT = SYSDATE WHERE CODE = '" + codeSafe + "'";
            tx.execute(updateSql);
            writeAudit(code, ip, EVT_REACTIVATE_OK, "reactivate");
            resp.put("ok", true);
            return resp;

        } catch (Exception e) {
            writeAudit(code, ip, "ACTIVATE_ERROR", e.getMessage());
            resp.put("ok", false);
            resp.put("error", "服务器异常: " + e.getMessage());
            return resp;
        }
    }

    /**
     * 写入审计日志（防内鬼核心）。
     * 所有激活请求都记录, 重点关注 REJECT_IP_MISMATCH 事件:
     *   - 同一激活码被多个 IP 尝试 → 可能有人把码给了别人
     *   - 查 K_AUDIT WHERE EVT='REJECT_IP_MISMATCH' 即可定位
     *
     * @param code 激活码 (可能为 null, 比如 bad auth 场景)
     * @param ip   请求 IP (可能为 null)
     * @param evt  事件类型 (见上方常量)
     * @param info 详细信息
     */
    private void writeAudit(String code, String ip, String evt, String info) {
        try {
            // 转义防 SQL 语法错
            String codeSafe = escapeSingleQuote(code);
            String ipSafe   = escapeSingleQuote(ip);
            String evtSafe  = escapeSingleQuote(evt);
            String infoSafe = escapeSingleQuote(truncate(info, 250));

            String sql = "INSERT INTO K_AUDIT (ID, EVT, CODE, IP, INFO) VALUES (" +
                         "SEQ_K_AUDIT.NEXTVAL, " +
                         "'" + evtSafe + "', " +
                         "'" + codeSafe + "', " +
                         "'" + ipSafe + "', " +
                         "'" + infoSafe + "')";
            // 替换为你们项目里的事务执行方式
            Transaction.current().execute(sql);
            log("INFO", "audit: " + evt + " code=" + code + " ip=" + ip + " info=" + info);
        } catch (Exception e) {
            // 审计失败不能影响主业务, 只记录日志
            log("ERROR", "writeAudit failed: evt=" + evt + " code=" + code + " ip=" + ip + " err=" + e.getMessage());
        }
    }

    // ========================================================================
    // 工具方法 - 按你们项目实际框架替换
    // ========================================================================

    /** 单引号转义（防 SQL 语法错，不是防注入） */
    private static String escapeSingleQuote(String s) {
        if (s == null) return "";
        return s.replace("'", "''");
    }

    /** 截断字符串到指定长度, 避免 INFO 字段超长 */
    private static String truncate(String s, int maxLen) {
        if (s == null) return "";
        if (s.length() <= maxLen) return s;
        return s.substring(0, maxLen - 3) + "...";
    }

    /** 日志（替换为你们项目里的 Logger） */
    private static void log(String level, String msg) {
        // Logger.log(level, "[KairoActivate] " + msg);
        System.out.println("[" + level + "] [KairoActivate] " + msg);
    }

    /** Basic auth 串脱敏（避免日志泄露完整 token） */
    private static String maskToken(String t) {
        if (t == null || t.length() < 4) return "***";
        return t.substring(0, 2) + "***" + t.substring(t.length() - 2);
    }

    /** ResultSet 遍历 - 替换为你们项目里的方式 */
    private static boolean rsHasNext(Object rs) {
        // 示例: ((java.sql.ResultSet) rs).next();
        return false; // TODO: 替换为真实实现
    }

    private static String rsGetString(Object rs, String col) {
        // 示例: ((java.sql.ResultSet) rs).getString(col);
        return null; // TODO: 替换为真实实现
    }

    private static void closeRs(Object rs) {
        // 示例: ((java.sql.ResultSet) rs).close();
        // TODO: 替换为真实实现
    }
}