// sync.go - 宠物排行榜服务器同步客户端 (v0.16)
//
// 跟 internal/sponsor 同款模式:
//   - POST /credit/httpInterface?channelID=PC&serviceID=KairoPetLeaderboardAction&seqNo=<秒级时间戳>
//   - Header: Authorization: Basic <auth>
//   - 主备切换 + Basic Auth + Timeout 全部走 internal/endpointclient
//
// 与 sponsor 的关键差异:
//   - 请求带本地状态 + 未同步流水 (ledger), 响应带 board_exp (认可分) / rank /
//     server_time / 周榜 Top N。
//   - 端点默认值硬编码在源码 (跟 sponsor 同款, 主备地址 + Basic Auth 共用同一套
//     Java httpInterface 网关), config.yaml 可覆盖; 若被显式清空 (syncPrimary==""),
//     SyncNow 直接返 "pet leaderboard endpoint 未配置", 前端显示「未配置服务器, 仅本地展示」。
//
// 注意: 本文件的 var 池命名都带 sync 前缀 (syncPrimary / syncMu...),
// 避免跟引擎核心文件 (engine.go / rules.go 等) 里的包级变量撞名。
package pet

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"kairo/internal/endpointclient"
)

// ===== 协议常量 =====
//
// serviceID 是 Java 端反射路由的 key, 不能从 config 覆盖 (跟 sponsor 同款)。
const (
	serviceID = "KairoPetLeaderboardAction"
	channelID = "PC"
)

// ===== 端点配置 (var 池, 跟 sponsor 同款) =====
//
// 默认值硬编码在源码里, 不走 config.yaml —— 与武林排行榜 (internal/sponsor) 完全一致:
// 主备地址 + Basic Auth 共用同一套 Java httpInterface 网关 (docs/pet/宠物排行榜接口与后端代码.txt
// 说同一 httpInterface 网关按 serviceID 反射路由, Basic Auth 与 KairoSponsorLeaderboardAction 同一个)。
// 开发者可在 config.yaml 配 internal_endpoints.pet_leaderboard 覆盖 (空值不覆盖, 老测试零修改)。
var (
	syncMu sync.RWMutex
	// syncPrimary 主排行榜服务地址 (硬编码在源码, 不走 config, 与 sponsor 主地址一致)
	syncPrimary = "http://66.0.34.199:9080/credit/httpInterface"
	// syncSecondary 备用排行榜服务地址 (硬编码在源码, 不走 config, 与 sponsor 备地址一致)
	syncSecondary = "http://66.0.34.198:9080/credit/httpInterface"
	// syncAuth POST 请求 Authorization 头的 "Basic <这里>" 部分 (base64 串, 不含 "Basic " 前缀,
	// 与 sponsor.BasicAuthHeader 同一个)
	syncAuth = "anN5aDpqc3loQDEyMw=="
	// syncTimeout 默认 10s (跟 sponsor 一致, Java 端要算 rank + 写库)
	syncTimeout = 10 * time.Second
)

// InitFromConfig 用 config 段覆盖 var 池。
// 跟 internal/sponsor.InitFromConfig 同款: 留空的字段不动 var。
//
// 调用方: main.go 启动时调一次, 把 config.InternalEndpoints.PetLeaderboard 传进来。
func InitFromConfig(primary, secondary, auth string, timeout time.Duration) {
	syncMu.Lock()
	defer syncMu.Unlock()
	if primary != "" {
		syncPrimary = primary
	}
	if secondary != "" {
		syncSecondary = secondary
	}
	if auth != "" {
		syncAuth = auth
	}
	if timeout > 0 {
		syncTimeout = timeout
	}
}

// Endpoints 端点池的一次完整快照 (诊断 / 测试还原用)。
//
// 与 InitFromConfig 的"空值不覆盖"不同, SetEndpoints 是**完整覆盖**:
// 空字符串和零值同样写入, 因此可以用来显式关闭备用端点, 也可以让测试
// 在注入本地 mock 之后把整池状态完整还原回去。
//
// 背景 (QA-01): 源码里 primary / secondary 都硬编码了真实 Java 网关地址,
// InitFromConfig 无法清空 secondary, 于是"primary 指向本地 mock、secondary
// 留空"的单元测试在 mock 返 5xx 时仍会切到真实备用上游发请求。测试必须
// 用 SetEndpoints 同时把两个端点都钉住。
type Endpoints struct {
	Primary   string
	Secondary string
	Auth      string
	Timeout   time.Duration
}

// SetEndpoints 完整覆盖端点池 (空值也生效), 见 Endpoints 说明。
func SetEndpoints(e Endpoints) {
	syncMu.Lock()
	defer syncMu.Unlock()
	syncPrimary = e.Primary
	syncSecondary = e.Secondary
	syncAuth = e.Auth
	syncTimeout = e.Timeout
}

// CurrentEndpoints 返回端点池快照, 配合 SetEndpoints 做还原。
func CurrentEndpoints() Endpoints {
	syncMu.RLock()
	defer syncMu.RUnlock()
	return Endpoints{
		Primary:   syncPrimary,
		Secondary: syncSecondary,
		Auth:      syncAuth,
		Timeout:   syncTimeout,
	}
}

// LeaderboardEntry 服务器榜单条目 (响应里 /api/pet 返回给前端)。
//
// 跟 sponsor.Entry 的差别:
//   - Name 是宠物名字 (用户自己起的), 不是真实姓名
//   - Exp 是服务器认可的 board_exp, 不是本地 exp_total
//   - Ops 是该用户主要操作聚合, 服务器可选返回 (空则 omitempty 掉)
type LeaderboardEntry struct {
	Rank  int          `json:"rank"`
	Name  string       `json:"name"`
	Level int          `json:"level"`
	Exp   int64        `json:"exp"`           // board_exp 认可分
	Ops   []OpStatView `json:"ops,omitempty"` // 该用户主要操作聚合 (服务器可选返回)
}

// OpStatView 单条操作聚合视图 (op → 次数 + 认可分)。
type OpStatView struct {
	Op    string `json:"op"`
	Count int64  `json:"count"`
	Exp   int64  `json:"exp"`
}

// SyncResult 一次同步的服务器响应。
//
// 业务响应都是 HTTP 200, 通过 ok 字段区分成功/失败 (跟 sponsor 同款):
//   - 成功: {ok: true, board_exp, rank, server_time, leaderboard: [...]}
//   - 失败: {ok: false, error: "..."}
type SyncResult struct {
	OK         bool               `json:"ok"`
	Error      string             `json:"error,omitempty"`
	BoardExp   int64              `json:"board_exp"`
	Rank       int                `json:"rank"`
	ServerTime string             `json:"server_time,omitempty"`
	Entries    []LeaderboardEntry `json:"leaderboard,omitempty"`
}

// syncRequest 发给服务器的请求体。
//
// 字段与设计文档 §8.1 一致: 本地状态全量 + 未同步流水 + 客户端时间戳。
// 服务器按 user_id 落库 (pets 表), ledger 流水留痕 (pet_ledger 表, 可选)。
type syncRequest struct {
	UserID   string        `json:"user_id"`
	License  string        `json:"license_code,omitempty"` // 激活码（服务器做唯一绑定 + user_id 校验）
	Name     string        `json:"name"`
	Level    int           `json:"level"`
	Stage    string        `json:"stage"`
	ExpTotal int64         `json:"exp_total"`
	BoardExp int64         `json:"board_exp"`
	Ledger   []LedgerEntry `json:"ledger"`
	Ts       string        `json:"ts"`
}

// SyncNow 把当前状态同步到服务器排行榜 (总是发请求, 不打本地短路 —— 短路由 handler 缓存决定)。
//
// 返回值约定 (跟 sponsor.FetchLeaderboard 同款):
//   - (*SyncResult{OK:true, ...}, nil) → 成功, 已调 MarkSynced 落盘认可分
//   - (*SyncResult{OK:false, Error:"..."}, nil) → Java 端业务失败, 不调 MarkSynced
//   - (nil, err) → 端点未配置 / 网络错 / JSON 解析失败 / 4xx 5xx
func (e *Engine) SyncNow() (*SyncResult, error) {
	syncMu.RLock()
	primary := syncPrimary
	secondary := syncSecondary
	auth := syncAuth
	timeout := syncTimeout
	syncMu.RUnlock()

	if primary == "" {
		return nil, errors.New("pet leaderboard endpoint 未配置")
	}

	st := e.State()
	if st == nil {
		return nil, errors.New("宠物状态不可用")
	}

	now := time.Now()
	ledger := e.TakeLedger()
	if ledger == nil {
		ledger = []LedgerEntry{} // 空流水序列化成 [] 而不是 null
	}

	// 激活码上送：服务器据此做"一个激活码唯一一只宠物"的硬绑定与 user_id 校验。
	licenseCode := ""
	if e.codeSource != nil {
		if c, ok := e.codeSource(); ok {
			licenseCode = c
		}
	}

	req := syncRequest{
		UserID:   st.ID,
		License:  licenseCode,
		Name:     st.Name,
		Level:    st.Level,
		Stage:    st.Stage,
		ExpTotal: st.TotalEarned,
		BoardExp: st.BoardExp,
		Ledger:   ledger,
		Ts:       now.Format(time.RFC3339),
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}

	cfg := endpointclient.Config{
		Primary: buildSyncURL(primary),
		Auth:    auth,
		Timeout: timeout,
	}
	if secondary != "" {
		cfg.Secondary = buildSyncURL(secondary)
	}

	resp, err := endpointclient.Call(cfg, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读响应失败: %w", err)
	}

	var sr SyncResult
	if err := json.Unmarshal(raw, &sr); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w (body=%s)", err, string(raw))
	}

	// 只有服务器明确认可 (ok=true) 才回写本地认可分 + 清 dirty。
	// ok=false 时保留旧 BoardExp 和 dirty 位, 下次同步重试。
	if sr.OK {
		e.MarkSynced(sr.BoardExp, now.Format(time.RFC3339))
	}
	return &sr, nil
}

// buildSyncURL 在 base URL 上拼 3 个固定 query 参数
// (channelID=PC, serviceID=KairoPetLeaderboardAction, seqNo=<秒级时间戳>)。
//
// 跟 internal/sponsor.buildURL 同款:
//   - 任何 key/value 为空的就跳过
//   - 使用 net/url.Values 做 URL-encode, 避免值含空格 / & / = 等字符破坏 URL
func buildSyncURL(base string) string {
	v := url.Values{}
	v.Set("channelID", channelID)
	v.Set("serviceID", serviceID)
	v.Set("seqNo", strconv.FormatInt(time.Now().Unix(), 10))
	enc := v.Encode()
	if enc == "" {
		return base
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + enc
}
