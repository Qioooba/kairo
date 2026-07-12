package cn.com.jscb.httpinterface.pc.kairo;

import cn.com.jscb.httpinterface.servlet.HttpPostUtil;

import com.alibaba.fastjson.JSONObject;

import com.amarsoft.are.ARE;
import com.amarsoft.are.sql.Transaction;

import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * Kairo 投喂作者排行榜 Action (v0.14)
 *
 * 入口:  POST /credit/httpInterface
 *        Query: channelID=PC&serviceID=KairoSponsorLeaderboardAction&seqNo=<秒级时间戳>
 *        Body: {}  (空, 排行榜不需要入参)
 *
 * 响应:  {ok: true, entries: [{rank, real_name, cotti, lucky, milktea, total, date, updated_at}, ...]}
 *         {ok: false, error: "..."}
 *
 * 字段命名跟 K_SPONSOR 表 1:1 对齐:
 *   库迪 = COTTI     (cotti = 库迪官方英文)
 *   瑞幸 = LUCKY     (用户原话: "瑞幸是LUCKY")
 *   奶茶 = MILKTEA   (用户原话: "random 就是奶茶啊 随机奶茶数量")
 *
 * 排名用 ROW_NUMBER() OVER (ORDER BY TOTAL DESC) 算, 不跳号.
 * 最多返 50 条, 跟前端 NICKNAMES 数组 1:1 绑定 (前 50 名拿对应昵称).
 *
 * 跟 KairoActivateAction 同款风格: package 一致, 用 HttpPostUtil 读 body,
 * 用 Transaction 执行 SQL, 返 Map + JSONObject.toJSONString.
 *
 * SQL 兼容性:
 *   使用 ROWNUM 子查询 + 外层 ORDER BY, 兼容 Oracle 11g / 12c+ (FETCH FIRST ... 12c 才有)
 *
 * 资源管理:
 *   用 try-with-resources (Java 7+) 关 rs/ps, 避免异常路径泄漏连接池
 *
 * 不需要 K_AUDIT: 排行榜是只读查询, 没有"敏感事件"需要审计.
 */
public class KairoSponsorLeaderboardAction {

    private final Transaction sqlca;

    public KairoSponsorLeaderboardAction(Transaction sqlca) {
        this.sqlca = sqlca;
    }

    /**
     * Action 入口方法.
     *
     * @param requestParam JSON 字符串 (本场景是空 {} 即可)
     * @param seqNo        秒级时间戳, 跟请求 query string 里的 seqNo 一致
     * @return 响应 JSON 字符串, 框架负责返给客户端
     */
    public String execute(String requestParam, String seqNo) {
        ARE.getLog().info(seqNo + "---HttpPost对方交易请求参数body: ~~~getSponsorLeaderboard~~~~~~~~~~~~~~~~~~~~~~ " + requestParam);
        Map<String, Object> resp = new HashMap<String, Object>();

        // 兼容 Oracle 11g (用 ROWNUM 子查询, 不用 FETCH FIRST 12c+ 语法)
        //   内层: ROW_NUMBER() 算 rank + 全部字段
        //   外层: WHERE ROWNUM <= 50 截前 50 + ORDER BY 稳定排序 (同分按 ID 升序)
        String sql = "SELECT * FROM (" +
                     "  SELECT ID, REAL_NAME, COTTI, LUCKY, MILKTEA, TOTAL, " +
                     "         ROW_NUMBER() OVER (ORDER BY TOTAL DESC, ID ASC) AS RNK, " +
                     "         TO_CHAR(CREATED_AT, 'MM-DD') AS DATE_STR, " +
                     "         UPDATED_AT " +
                     "  FROM K_SPONSOR " +
                     ") WHERE ROWNUM <= 50 " +
                     "ORDER BY TOTAL DESC, ID ASC";

        // try-with-resources 自动关 rs/ps, 异常路径也不漏连接池
        try (PreparedStatement ps = sqlca.conn.prepareStatement(sql);
             ResultSet rs = ps.executeQuery()) {

            List<Map<String, Object>> entries = new ArrayList<Map<String, Object>>();
            while (rs.next()) {
                Map<String, Object> entry = new HashMap<String, Object>();
                entry.put("rank",       rs.getInt("RNK"));
                entry.put("real_name",  rs.getString("REAL_NAME"));
                entry.put("cotti",      rs.getInt("COTTI"));
                entry.put("lucky",      rs.getInt("LUCKY"));
                entry.put("milktea",    rs.getInt("MILKTEA"));
                entry.put("total",      rs.getInt("TOTAL"));
                entry.put("date",       rs.getString("DATE_STR"));
                entry.put("updated_at", rs.getTimestamp("UPDATED_AT"));
                entries.add(entry);
            }

            resp.put("ok", true);
            resp.put("entries", entries);
            return JSONObject.toJSONString(resp);

        } catch (Exception e) {
            ARE.getLog().error("[KairoSponsorLeaderboard] error: " + e.getMessage());
            resp.put("ok", false);
            resp.put("error", "服务器异常: " + e.getMessage());
            return JSONObject.toJSONString(resp);
        }
    }
}
