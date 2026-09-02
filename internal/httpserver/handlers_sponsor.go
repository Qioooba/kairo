// Package httpserver - sponsor 排行榜相关端点 (v0.14)
//
// 端点清单:
//
//	GET /api/sponsor/leaderboard    前端拉投喂作者排行榜
//
// 流程:
//
//	前端 → /api/sponsor/leaderboard → Go 端 sponsor.FetchLeaderboard()
//	                                 → 走 endpointclient (主备切换 + Basic Auth)
//	                                 → Java 端 /credit/httpInterface
//	                                 → 返前 50 名 (按 total 倒序, 含 rank)
//
// 缓存:
//   - 启动预热和首次请求共享同一把拉取锁，避免冷启动时并发打爆 Java 端
//   - 短时间内优先返回成功缓存，避免前端首次打开时重复等待冷查询
//   - 查询成功则更新内存缓存 (sponsorCache), 供后续查询失败时兜底
//   - 查询失败 (网络错 / Java 端业务失败) 且有历史缓存时, 返回陈旧缓存 (优雅降级)
//   - 线程安全 (sponsorCacheMu)
//
// 失败语义 (跟前端对齐, 不兜底):
//   - 网络错 / Java 端 4xx 5xx 且无缓存 → handler 返 502 + {ok:false, error:"..."}
//   - Java 端业务失败 (OK=false) 且无缓存 → handler 返 200 + {ok:false, error:"..."}
//   - 成功 → handler 返 200 + {ok:true, entries:[...]}
package httpserver

import (
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"kairo/internal/sponsor"
)

var (
	sponsorCacheMu      sync.RWMutex
	sponsorCacheEntries []sponsor.Entry
	sponsorCacheTime    time.Time
	sponsorCacheOK      bool
	sponsorFetchMu      sync.Mutex
)

const sponsorCacheMaxAge = 5 * time.Minute

func setSponsorCache(entries []sponsor.Entry, ok bool) {
	sponsorCacheMu.Lock()
	defer sponsorCacheMu.Unlock()
	sponsorCacheEntries = append([]sponsor.Entry(nil), entries...)
	sponsorCacheOK = ok
	sponsorCacheTime = time.Now()
}

func sponsorCacheSnapshot(maxAge time.Duration) ([]sponsor.Entry, bool) {
	sponsorCacheMu.RLock()
	defer sponsorCacheMu.RUnlock()
	if !sponsorCacheOK || sponsorCacheTime.IsZero() || time.Since(sponsorCacheTime) > maxAge {
		return nil, false
	}
	return append([]sponsor.Entry(nil), sponsorCacheEntries...), true
}

func sponsorStaleSnapshot() ([]sponsor.Entry, bool) {
	sponsorCacheMu.RLock()
	defer sponsorCacheMu.RUnlock()
	if !sponsorCacheOK || sponsorCacheTime.IsZero() {
		return nil, false
	}
	return append([]sponsor.Entry(nil), sponsorCacheEntries...), true
}

// WarmSponsorCache 启动预热：后台拉排行榜填充缓存，带重试。
//
// 背景（用户反馈"第一次运行武林排行榜加载不出来"）：
//   - 首次请求时 Java 网关冷启动（反射路由 + Oracle 查询），常超过 10s 客户端超时；
//   - 之后 Java 端连接池热了，第二次请求就快了——表现为"多点几下就好了"。
//
// 策略：启动后 0s/4s/12s 最多尝试 3 次，成功则填充 sponsorCache；
// 全部失败只记日志，不影响功能。前端也有 3 次重试兜底，双保险。
func WarmSponsorCache() {
	go func() {
		delays := []time.Duration{0, 4 * time.Second, 8 * time.Second}
		for i, d := range delays {
			if d > 0 {
				time.Sleep(d)
			}
			sponsorFetchMu.Lock()
			lr, err := sponsor.FetchLeaderboard()
			sponsorFetchMu.Unlock()
			if err == nil && lr != nil && lr.OK {
				setSponsorCache(lr.Entries, true)
				log.Printf("sponsor: 启动预热成功（第 %d 次），排行榜缓存 %d 条", i+1, len(lr.Entries))
				return
			}
			reason := "未知错误"
			if err != nil {
				reason = err.Error()
			} else if lr != nil && lr.Error != "" {
				reason = lr.Error
			}
			log.Printf("sponsor: 启动预热第 %d 次失败（%s），%s", i+1, reason, func() string {
				if i < len(delays)-1 {
					return "稍后重试"
				}
				return "不再重试，用户首次访问时还会实时拉取"
			}())
		}
	}()
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

	if entries, ok := sponsorCacheSnapshot(sponsorCacheMaxAge); ok {
		s.audit.Write("sponsor.leaderboard.cache", "count", len(entries))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"entries": entries,
			"cached":  true,
		})
		return
	}

	// WarmSponsorCache may be in flight. Serialize the real fetch and check the
	// cache again after waiting so the first page request can use its result.
	sponsorFetchMu.Lock()
	if entries, ok := sponsorCacheSnapshot(sponsorCacheMaxAge); ok {
		sponsorFetchMu.Unlock()
		s.audit.Write("sponsor.leaderboard.cache", "count", len(entries))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"entries": entries,
			"cached":  true,
		})
		return
	}
	lr, err := sponsor.FetchLeaderboard()
	sponsorFetchMu.Unlock()
	if err != nil {
		s.audit.Write("sponsor.leaderboard.error", "err", err.Error())

		staleEntries, hasStale := sponsorStaleSnapshot()

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

	if lr == nil || !lr.OK {
		reason := "排行榜服务暂时不可用"
		if lr != nil && lr.Error != "" {
			reason = lr.Error
		}
		s.audit.Write("sponsor.leaderboard.fail", "error", reason)

		staleEntries, hasStale := sponsorStaleSnapshot()

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
			"error": reason,
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
