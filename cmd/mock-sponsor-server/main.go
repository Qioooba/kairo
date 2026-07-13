// mock-sponsor-server 是一个独立的 Go 程序, 用来模拟 Java 端的
// /credit/httpInterface?serviceID=KairoSponsorLeaderboardAction 行为。
//
// 用途:
//   - 本地开发时替代真实 Java 后端 + Oracle, 跑集成测试
//   - 没有 Oracle 也能跑 (用内存 slice 存数据)
//   - 启动:  go run ./cmd/mock-sponsor-server -addr :18093 -auth TEST_TOKEN_123
//   - 调试查询:
//       curl http://localhost:18093/admin/list         # 所有 sponsor 数据
//       curl -X POST http://localhost:18093/admin/reset  # 重置回预置数据
//       curl http://localhost:18093/health
//
// 数据格式跟前端 mock 用的 brand id 一致 (cotti / luckin / milktea),
// 跟生产 Java 端 K_SPONSOR 表的字段名一致 (REAL_NAME / COTTI / LUCKY / MILKTEA / TOTAL)。
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// sponsorEntry 模拟 K_SPONSOR 表的一行
type sponsorEntry struct {
	ID       int64  `json:"id"`
	RealName string `json:"real_name"`
	Cotti    int    `json:"cotti"`
	Lucky    int    `json:"lucky"`
	Milktea  int    `json:"milktea"`
	Total    int    `json:"total"` // Java 端冗余存 (cotti+lucky+milktea), 方便 ORDER BY
	Date     string `json:"date"`  // 首次赞助日期
}

// presetData 默认预置 5 条 mock 数据, 自动算 total
func presetData() []sponsorEntry {
	raw := []sponsorEntry{
		{ID: 1, RealName: "张三", Cotti: 4, Lucky: 4, Milktea: 1, Date: "07-09"},
		{ID: 2, RealName: "李四", Cotti: 3, Lucky: 4, Milktea: 1, Date: "07-08"},
		{ID: 3, RealName: "王五", Cotti: 2, Lucky: 3, Milktea: 1, Date: "07-07"},
		{ID: 4, RealName: "赵六", Cotti: 0, Lucky: 4, Milktea: 0, Date: "07-06"},
		{ID: 5, RealName: "钱七", Cotti: 3, Lucky: 2, Milktea: 1, Date: "07-05"},
	}
	// 自动算 total (生产 Java 端也是 K_SPONSOR 表里冗余存)
	for i := range raw {
		raw[i].Total = raw[i].Cotti + raw[i].Lucky + raw[i].Milktea
	}
	return raw
}

var (
	mu      sync.Mutex
	entries = presetData()

	adminAuthToken string
)

// leaderboardEntry 返给前端的格式 (含 rank, 按 total 倒序)
//
// v0.16: 跟 internal/sponsor.Entry 对齐, 数字字段 (rank/cotti/lucky/milktea/total) 留 int,
// UpdatedAt 改 string 透传 (模拟生产 Java 端真实格式: "2006-01-02 15:04:05.0"
// 空格分隔 + ".0" 毫秒后缀 —— 这是 Go 端之前 UpdatedAt time.Time 解析炸的真实格式)。
//
// 数字字段保留 int 是因为 Java 端 JSONObject 转 JSON 时数字字段是 JSON number
// (例: "cotti":4), Go encoding/json 严格区分 number/string, 收 number 进 string
// 字段会直接拒掉, 跟 updated_at 错误同类。所以"全 string" 不行, 只 UpdatedAt 改 string。
type leaderboardEntry struct {
	Rank      int    `json:"rank"`
	RealName  string `json:"real_name"`
	Cotti     int    `json:"cotti"`
	Lucky     int    `json:"lucky"`
	Milktea   int    `json:"milktea"`
	Total     int    `json:"total"`
	Date      string `json:"date"`
	UpdatedAt string `json:"updated_at"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18093", "监听地址 (默认仅本机, 避免误暴露)")
	authToken := flag.String("auth", "TEST_TOKEN_123", "Basic auth 校验串 (放在 Authorization: Basic 后面)")
	flag.Parse()

	adminAuthToken = *authToken
	log.Printf("mock-sponsor-server 启动: addr=%s auth=<masked> 预置 %d 条 sponsor 数据", *addr, len(entries))
	for _, e := range entries {
		log.Printf("  - %s (cotti=%d lucky=%d milktea=%d total=%d)", e.RealName, e.Cotti, e.Lucky, e.Milktea, e.Total)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/credit/httpInterface", handleLeaderboard)
	mux.HandleFunc("/admin/list", requireAdminAuth(handleAdminList))
	mux.HandleFunc("/admin/reset", requireAdminAuth(handleAdminReset))
	mux.HandleFunc("/health", handleHealth)

	log.Printf("可用端点:")
	log.Printf("  POST /credit/httpInterface    排行榜接口 (serviceID=KairoSponsorLeaderboardAction)")
	log.Printf("  GET  /admin/list             查看所有 sponsor 数据 (需 Basic auth)")
	log.Printf("  POST /admin/reset            重置回预置数据 (需 Basic auth)")
	log.Printf("  GET  /health                 健康检查")
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// handleLeaderboard 模拟 Java 端, 按 total 倒序排, 最多返 50 条
func handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	// 0. 校验 HTTP 方法 (生产 Java 端只接受 POST)
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "仅支持 POST"})
		return
	}

	// 1. Basic auth 校验
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Basic ") {
		writeJSON(w, 401, map[string]any{"ok": false, "error": "missing Basic auth"})
		return
	}
	token := strings.TrimPrefix(auth, "Basic ")
	if !mockAuthPass(token) {
		writeJSON(w, 401, map[string]any{"ok": false, "error": "bad auth token"})
		return
	}

	// 2. 校验 serviceID (生产 Java 端会按 serviceID 反射, mock 简单校验一下)
	if r.URL.Query().Get("serviceID") != "KairoSponsorLeaderboardAction" {
		writeJSON(w, 200, map[string]any{
			"ok":    false,
			"error": "serviceID 不是 KairoSponsorLeaderboardAction, 走错 Action 了",
		})
		return
	}

	// 3. 排序 + 算 rank
	mu.Lock()
	defer mu.Unlock()

	sorted := make([]sponsorEntry, len(entries))
	copy(sorted, entries)
	sortEntriesByTotalDesc(sorted)

	limit := 50
	if len(sorted) < limit {
		limit = len(sorted)
	}
	result := make([]leaderboardEntry, 0, limit)
	// 模拟生产 Java 端 (Oracle/MySQL DATETIME 文本格式):
	//   "2006-01-02 15:04:05.0"  ← 空格分隔 (不是 RFC3339 的 T) + ".0" 毫秒后缀
	// 这是 Go 端之前 UpdatedAt time.Time 解析炸的真实格式; 改 string 透传后能正确收。
	// Truncate(time.Second) 抹掉亚秒, 让输出是 ".0" (跟生产 Java 端 Oracle TIMESTAMP 精度一致,
	// 不是 ".600" 这种带实际毫秒数的, 否则跟生产不一致会让 mock 偏离 1:1)。
	now := time.Now().Truncate(time.Second).Format("2006-01-02 15:04:05.0")
	for i := 0; i < limit; i++ {
		result = append(result, leaderboardEntry{
			Rank:      i + 1,
			RealName:  sorted[i].RealName,
			Cotti:     sorted[i].Cotti,
			Lucky:     sorted[i].Lucky,
			Milktea:   sorted[i].Milktea,
			Total:     sorted[i].Total,
			Date:      sorted[i].Date,
			UpdatedAt: now,
		})
	}

	writeJSON(w, 200, map[string]any{
		"ok":      true,
		"entries": result,
	})
}

// sortEntriesByTotalDesc 按 total 倒序 (同分时按 id 升序保证稳定)
func sortEntriesByTotalDesc(s []sponsorEntry) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Total != s[j].Total {
			return s[i].Total > s[j].Total
		}
		return s[i].ID < s[j].ID
	})
}

// handleAdminList 查看所有 sponsor 数据
func handleAdminList(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	out := make([]sponsorEntry, len(entries))
	copy(out, entries)
	writeJSON(w, 200, out)
}

// handleAdminReset 重置回预置数据
func handleAdminReset(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	entries = presetData()
	writeJSON(w, 200, map[string]any{"ok": true, "reset_count": len(entries)})
}

// handleHealth 健康检查
func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "service": "mock-sponsor-server"})
}

// requireAdminAuth 包装 /admin/* handler, 要求 Basic auth 与 -auth 配置的一致
func requireAdminAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Basic ") {
			w.Header().Set("WWW-Authenticate", `Basic realm="mock-admin"`)
			writeJSON(w, 401, map[string]any{"error": "admin requires auth"})
			return
		}
		token := strings.TrimPrefix(auth, "Basic ")
		if !mockAuthPass(token) {
			w.Header().Set("WWW-Authenticate", `Basic realm="mock-admin"`)
			writeJSON(w, 401, map[string]any{"error": "invalid auth token"})
			return
		}
		handler(w, r)
	}
}

// mockAuthPass 校验 token 是否与 -auth 配置的一致
func mockAuthPass(token string) bool {
	if token == "" || token == "PLACEHOLDER_BASIC_AUTH" {
		return false
	}
	return token == adminAuthToken
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
