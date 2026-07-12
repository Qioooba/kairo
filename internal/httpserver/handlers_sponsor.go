// Package httpserver - sponsor 排行榜相关端点 (v0.14)
//
// 端点清单:
//   GET /api/sponsor/leaderboard    前端拉投喂作者排行榜
//
// 流程:
//   前端 → /api/sponsor/leaderboard → Go 端 sponsor.FetchLeaderboard()
//                                    → 走 endpointclient (主备切换 + Basic Auth)
//                                    → Java 端 /credit/httpInterface
//                                    → 返前 50 名 (按 total 倒序, 含 rank)
//
// 失败语义 (跟前端对齐, 不兜底):
//   - 网络错 / Java 端 4xx 5xx → handler 返 502 + {ok:false, error:"..."}
//   - Java 端业务失败 (OK=false) → handler 返 200 + {ok:false, error:"..."}
//   - 成功 → handler 返 200 + {ok:true, entries:[...]}
package httpserver

import (
	"errors"
	"net/http"

	"kairo/internal/sponsor"
)

// handleSponsorLeaderboard GET /api/sponsor/leaderboard
//
// 返回格式 (跟前端 sponsor.js 期望的格式对齐):
//   - 成功: {ok: true, entries: [{rank, real_name, cotti, lucky, milktea, total, date, updated_at}, ...]}
//   - 失败: {ok: false, error: "..."}
//
// 前端收到 entries 后:
//   - 按 rank 拿 NICKNAMES[rank-1] 拼成 "<昵称>·<真实姓名>"
//   - 按 total 排, 1-3 名给奖牌, 4+ 给数字 (前端已实现, 这里只给数据)
func (s *Server) handleSponsorLeaderboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
		return
	}

	lr, err := sponsor.FetchLeaderboard()
	if err != nil {
		// 网络错 / JSON 解析失败 / 4xx 5xx (endpointclient 已归一化)
		// 详细错误写审计日志, 不反显给前端 (避免泄露内网地址)
		s.audit.Write("sponsor.leaderboard.error", "err", err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":    false,
			"error": "排行榜服务暂时不可用, 请稍后重试",
		})
		return
	}

	if !lr.OK {
		// Java 端业务失败 (比如 K_SPONSOR 表锁住)
		s.audit.Write("sponsor.leaderboard.fail", "error", lr.Error)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": lr.Error,
		})
		return
	}

	// 成功 - 返 entries 给前端
	s.audit.Write("sponsor.leaderboard.ok", "count", len(lr.Entries))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"entries": lr.Entries,
	})
}
