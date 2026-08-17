// Package httpserver - 宠物彩蛋相关端点 (v0.16)
//
// 端点清单 (统一挂 /api/pet/*, 路由分发进 handlePetDispatch):
//
//	GET  /api/pet/state         宠物状态 (未解锁返 {enabled:false})
//	POST /api/pet/enable        解锁宠物 (前端「关于」点 6 次触发, 幂等)
//	POST /api/pet/name          改名 (服务端校验)
//	POST /api/pet/pos           拖动松手后保存位置
//	POST /api/pet/skin          晃动换肤后保存皮肤索引
//	POST /api/pet/sync          触发服务器同步 (dirty 时), 返回最新榜单 + rank + 认可分
//	GET  /api/pet/leaderboard   本地缓存的上次榜单 (无网络请求)
//	/api/pet/skills/*、/api/pet/battle/*  v1 不注册 → 404 (命名空间预留)
//
// 缓存:
//   - /api/pet/sync 成功时更新内存缓存 (petCache*), 供失败时兜底 + leaderboard 直读
//   - dirty 或缓存空时打网络; 不 dirty 且有缓存时直接回缓存 (零网络)
//   - 失败 (网络错 / Java 端业务失败) 且有历史缓存 → 返陈旧缓存 (stale:true, 优雅降级)
//   - 失败且无缓存 → 502 {ok:false, error:"排行榜服务暂时不可用, 请稍后重试"}
//   - 线程安全 (petCacheMu)
//
// 审计: 宠物自身操作 (enable/rename/pos/skin) 写 audit 但不给经验
// (经验表白名单由 internal/pet 的规则管, pet.* op 不在表内)。
package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"kairo/internal/pet"
)

// ===== 同步缓存 (跟 sponsorCache 同款: 包级 var + RWMutex) =====

var (
	petCacheMu       sync.RWMutex
	petCacheEntries  []pet.LeaderboardEntry
	petCacheRank     int
	petCacheBoardExp int64
	petCacheTime     time.Time
	petCacheOK       bool
)

// setPetCache 用一次成功的同步结果整体覆盖缓存。
func setPetCache(res *pet.SyncResult) {
	petCacheMu.Lock()
	defer petCacheMu.Unlock()
	petCacheEntries = res.Entries
	petCacheRank = res.Rank
	petCacheBoardExp = res.BoardExp
	petCacheOK = true
	petCacheTime = time.Now()
}

// readPetCache 读缓存快照。hasStale = 有可用历史缓存 (跟 sponsor 同款判定)。
func readPetCache() (entries []pet.LeaderboardEntry, rank int, boardExp int64, hasStale bool) {
	petCacheMu.RLock()
	defer petCacheMu.RUnlock()
	hasStale = !petCacheTime.IsZero() && petCacheOK && len(petCacheEntries) > 0
	return petCacheEntries, petCacheRank, petCacheBoardExp, hasStale
}

// petUnavailable 宠物功能不可用 (engine 未注入或未解锁) 的统一 404。
func petUnavailable(w http.ResponseWriter) {
	writeErr(w, http.StatusNotFound, errors.New("宠物功能不可用"))
}

// writePetState 把引擎状态视图 + enabled 标记写回。
// 防御性: 复制 StateView 的 map, 不污染引擎内部视图。
func writePetState(w http.ResponseWriter, e *pet.Engine) {
	view := e.StateView()
	resp := make(map[string]any, len(view)+1)
	for k, v := range view {
		resp[k] = v
	}
	resp["enabled"] = true
	writeJSON(w, http.StatusOK, resp)
}

// ===== 路由分发 =====

// handlePetDispatch 把 /api/pet/* 按后缀分发到具体 handler。
//
// 路由:
//
//	GET  /api/pet/state         (未解锁也 200, 返 {enabled:false})
//	POST /api/pet/enable        (无需先解锁, 幂等)
//	POST /api/pet/name|pos|skin (需已解锁, 否则 404)
//	POST /api/pet/sync          (需已解锁, 否则 404)
//	GET  /api/pet/leaderboard   (需已解锁, 否则 404)
//	其他 (skills/battle 命名空间预留, v1 不注册) → 404
func (s *Server) handlePetDispatch(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/pet")

	switch {
	case path == "/state":
		s.handlePetState(w, r)
	case path == "/enable":
		s.handlePetEnable(w, r)
	case path == "/name":
		s.handlePetName(w, r)
	case path == "/pos":
		s.handlePetPos(w, r)
	case path == "/skin":
		s.handlePetSkin(w, r)
	case path == "/sync":
		s.handlePetSync(w, r)
	case path == "/leaderboard":
		s.handlePetLeaderboard(w, r)
	default:
		// skills/battle 命名空间预留 (v1 不实现, 不留重构债)
		writeErr(w, http.StatusNotFound, errors.New("宠物接口不存在"))
	}
}

// ===== GET /api/pet/state =====

// handlePetState 返宠物当前状态。
//   - engine 未注入 → 404
//   - 未解锁 → 200 {enabled:false} (前端据此零渲染宠物 UI)
//   - 已解锁 → 200 {enabled:true, ...引擎状态视图}
func (s *Server) handlePetState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
		return
	}
	e := s.pet
	if e == nil {
		petUnavailable(w)
		return
	}
	if !e.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	writePetState(w, e)
}

// ===== POST /api/pet/enable =====

// handlePetEnable 解锁宠物 (前端「关于」点 6 次触发)。
// 幂等: 已解锁时再调直接返当前状态。
func (s *Server) handlePetEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	e := s.pet
	if e == nil {
		petUnavailable(w)
		return
	}
	if _, err := e.Enable(); err != nil {
		s.audit.Write("pet.enable", "result", "fail", "error", err.Error())
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.audit.Write("pet.enable", "result", "ok")
	writePetState(w, e)
}

// ===== POST /api/pet/name =====

// handlePetName 改名 (长度/控制字符校验在引擎侧, 校验失败返 400)。
func (s *Server) handlePetName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	e := s.pet
	if e == nil || !e.Enabled() {
		petUnavailable(w)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("请求体不是合法 JSON"))
		return
	}
	if err := e.Rename(req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.audit.Write("pet.rename", "name", req.Name)
	writePetState(w, e)
}

// ===== POST /api/pet/pos =====

// handlePetPos 保存宠物位置 (拖动松手后, 前端防抖调用)。
// x/y 是相对屏幕的比例 (0..1), 范围校验在引擎侧, 校验失败返 400。
func (s *Server) handlePetPos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	e := s.pet
	if e == nil || !e.Enabled() {
		petUnavailable(w)
		return
	}
	var req struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("请求体不是合法 JSON"))
		return
	}
	if err := e.SetPos(req.X, req.Y); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.audit.Write("pet.pos", "x", fmt.Sprintf("%.2f", req.X), "y", fmt.Sprintf("%.2f", req.Y))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ===== POST /api/pet/skin =====

// handlePetSkin 保存皮肤（v2：语义 id，如 "orange-cat"）。
// 存在性 + 解锁等级校验在引擎侧（skins.json 清单驱动），校验失败返 400。
func (s *Server) handlePetSkin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	e := s.pet
	if e == nil || !e.Enabled() {
		petUnavailable(w)
		return
	}
	var req struct {
		Skin string `json:"skin"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("请求体不是合法 JSON"))
		return
	}
	if err := e.SetSkin(req.Skin); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.audit.Write("pet.skin", "skin", req.Skin)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ===== POST /api/pet/sync =====

// handlePetSync 触发服务器同步 (dirty 或缓存空时才打网络)。
//
// 返回 (200):
//   - 同步成功: {ok:true, entries, rank, board_exp, stale:false, server_time}
//   - 走缓存短路: {ok:true, entries, rank, board_exp, stale:false}
//   - 失败兜底: {ok:true, entries, rank, board_exp, stale:true} (历史缓存)
//
// 返回 (502):
//   - {ok:false, error:"排行榜服务暂时不可用, 请稍后重试"} (无缓存可兜底)
func (s *Server) handlePetSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	e := s.pet
	if e == nil || !e.Enabled() {
		petUnavailable(w)
		return
	}

	// 快速路径: 不 dirty 且有可用缓存 → 直接回缓存, 不打网络。
	st := e.State()
	dirty := st != nil && st.Dirty
	if !dirty {
		if entries, rank, boardExp, hasStale := readPetCache(); hasStale {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":        true,
				"entries":   entries,
				"rank":      rank,
				"board_exp": boardExp,
				"stale":     false,
			})
			return
		}
	}

	res, err := e.SyncNow()
	if err != nil {
		s.audit.Write("pet.sync.error", "err", err.Error())

		entries, rank, boardExp, hasStale := readPetCache()
		if hasStale {
			s.audit.Write("pet.sync.stale", "count", len(entries))
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":        true,
				"entries":   entries,
				"rank":      rank,
				"board_exp": boardExp,
				"stale":     true,
			})
			return
		}

		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":    false,
			"error": "排行榜服务暂时不可用, 请稍后重试",
		})
		return
	}

	if !res.OK {
		s.audit.Write("pet.sync.fail", "error", res.Error)

		entries, rank, boardExp, hasStale := readPetCache()
		if hasStale {
			s.audit.Write("pet.sync.stale", "count", len(entries))
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":        true,
				"entries":   entries,
				"rank":      rank,
				"board_exp": boardExp,
				"stale":     true,
			})
			return
		}

		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":    false,
			"error": "排行榜服务暂时不可用, 请稍后重试",
		})
		return
	}

	setPetCache(res)
	s.audit.Write("pet.sync.ok", "rank", res.Rank, "board_exp", res.BoardExp, "count", len(res.Entries))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"entries":     res.Entries,
		"rank":        res.Rank,
		"board_exp":   res.BoardExp,
		"stale":       false,
		"server_time": res.ServerTime,
	})
}

// ===== GET /api/pet/leaderboard =====

// handlePetLeaderboard 返本地缓存的上次榜单 (零网络请求)。
// stale = 缓存为空 (前端据此显示「暂无榜单, 去同步」)。
func (s *Server) handlePetLeaderboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
		return
	}
	e := s.pet
	if e == nil || !e.Enabled() {
		petUnavailable(w)
		return
	}

	entries, rank, boardExp, _ := readPetCache()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"entries":   entries,
		"rank":      rank,
		"board_exp": boardExp,
		"stale":     len(entries) == 0,
		"cached":    true,
	})
}
