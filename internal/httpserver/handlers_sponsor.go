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
// 缓存:
//   - 5 分钟内存缓存 (sponsorCache), 减少对内部 Java 服务的压力
//   - 缓存命中直接返回, 不打后端
//   - 缓存过期/失效时, 成功则更新缓存; 失败但有陈旧缓存时返回陈旧数据 (优雅降级)
//   - 线程安全 (sponsorCacheMu)
//
// 失败语义 (跟前端对齐, 不兜底):
//   - 网络错 / Java 端 4xx 5xx 且无缓存 → handler 返 502 + {ok:false, error:"..."}
//   - Java 端业务失败 (OK=false) 且无缓存 → handler 返 200 + {ok:false, error:"..."}
//   - 成功 → handler 返 200 + {ok:true, entries:[...]}
package httpserver

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"kairo/internal/sponsor"
)

const sponsorCacheTTL = 5 * time.Minute

var (
	sponsorCacheMu      sync.RWMutex
	sponsorCacheEntries []sponsor.Entry
	sponsorCacheTime    time.Time
	sponsorCacheOK      bool
)

func getSponsorCache() ([]sponsor.Entry, bool, bool) {
	sponsorCacheMu.RLock()
	defer sponsorCacheMu.RUnlock()
	if sponsorCacheTime.IsZero() || time.Since(sponsorCacheTime) > sponsorCacheTTL {
		return nil, false, false
	}
	return sponsorCacheEntries, true, sponsorCacheOK
}

func setSponsorCache(entries []sponsor.Entry, ok bool) {
	sponsorCacheMu.Lock()
	defer sponsorCacheMu.Unlock()
	sponsorCacheEntries = entries
	sponsorCacheOK = ok
	sponsorCacheTime = time.Now()
}

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

	if entries, fresh, ok := getSponsorCache(); fresh {
		s.audit.Write("sponsor.leaderboard.cache_hit", "count", len(entries))
		if ok {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":      true,
				"entries": entries,
				"cached":  true,
			})
		} else {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":    false,
				"error": "排行榜服务暂时不可用, 请稍后重试",
				"cached": true,
			})
		}
		return
	}

	lr, err := sponsor.FetchLeaderboard()
	if err != nil {
		s.audit.Write("sponsor.leaderboard.error", "err", err.Error())

		sponsorCacheMu.RLock()
		hasStale := !sponsorCacheTime.IsZero() && sponsorCacheOK && len(sponsorCacheEntries) > 0
		staleEntries := sponsorCacheEntries
		sponsorCacheMu.RUnlock()

		if hasStale {
			s.audit.Write("sponsor.leaderboard.stale", "count", len(staleEntries))
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":      true,
				"entries": staleEntries,
				"stale":   true,
			})
			return
		}

		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":    false,
			"error": "排行榜服务暂时不可用, 请稍后重试",
		})
		return
	}

	if !lr.OK {
		s.audit.Write("sponsor.leaderboard.fail", "error", lr.Error)

		sponsorCacheMu.RLock()
		hasStale := !sponsorCacheTime.IsZero() && sponsorCacheOK && len(sponsorCacheEntries) > 0
		staleEntries := sponsorCacheEntries
		sponsorCacheMu.RUnlock()

		if hasStale {
			s.audit.Write("sponsor.leaderboard.stale", "count", len(staleEntries))
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":      true,
				"entries": staleEntries,
				"stale":   true,
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": lr.Error,
		})
		return
	}

	setSponsorCache(lr.Entries, true)

	s.audit.Write("sponsor.leaderboard.ok", "count", len(lr.Entries))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"entries": lr.Entries,
	})
}
