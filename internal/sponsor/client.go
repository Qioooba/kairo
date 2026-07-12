// Package sponsor 客户端: 调 Java 端拉排行榜
//
// 接口形态 (跟 internal/license 激活服务完全一致, Java 端反射按 serviceID 路由):
//
//	POST /credit/httpInterface?channelID=PC&serviceID=KairoSponsorLeaderboardAction&seqNo=<秒级时间戳>
//	Header: Authorization: Basic <auth>
//	Body:   {}  (空, 排行榜不需要入参)
//	Resp:   {ok: true, entries: [{rank, real_name, cotti, lucky, milktea, total, date, updated_at}, ...]}
//	         {ok: false, error: "..."}
//
// 主备切换 + Basic Auth + Timeout 全部走 internal/endpointclient, 跟 license 同一套。
package sponsor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"kairo/internal/endpointclient"
)

// ===== 协议常量 =====
//
// serviceID 是 Java 端反射路由的 key, 不能从 config 覆盖 (跟 KairoActivateAction 同款)。
// user 后续会把它插入 Oracle 的 K_SERVICE 表, 由 Java 端 dispatcher 反射调度。
const (
	ServiceID    = "KairoSponsorLeaderboardAction"
	ChannelIDVal = "PC"
	URLPath      = "/credit/httpInterface"
)

// ===== 端点配置 (var 池, 跟 license 同款) =====
//
// 默认值硬编码到源码, 不走 ldflags 注入。
// 测试代码可以临时改 var 指向 httptest server。
// main.go 启动时如果 config.yaml 配了 internal_endpoints.sponsor_leaderboard,
// 会调 InitFromConfig() 覆盖 var (跟 license 包同款流程)。
var (
	Primary   = "http://66.0.34.199:9080/credit/httpInterface"
	Secondary = "http://66.0.34.198:9080/credit/httpInterface"
	// BasicAuthHeader POST 请求 Authorization 头的 "Basic <这里>" 部分 (base64 串, 不含 "Basic " 前缀)
	BasicAuthHeader = "anN5aDpqc3loQDEyMw=="
	// DefaultTimeout 默认 10s (排行榜比激活慢点, Java 端要算 rank)
	DefaultTimeout = 10 * time.Second
)

// InitFromConfig 用 config 段覆盖 var 池。
// 跟 internal/license.InitFromConfig 同款: 留空的字段不动 var, 让测试代码可以只覆盖部分字段。
//
// 调用方: main.go 启动时调一次, 把 config.InternalEndpoints.SponsorLeaderboard 传进来。
func InitFromConfig(primary, secondary, auth string, timeout time.Duration) {
	if primary != "" {
		Primary = primary
	}
	if secondary != "" {
		Secondary = secondary
	}
	if auth != "" {
		BasicAuthHeader = auth
	}
	if timeout > 0 {
		DefaultTimeout = timeout
	}
}

// FetchLeaderboard 调 Java 端拉排行榜, 主备自动切换。
//
// 返回值:
//   - (*LeaderboardResp{OK:true, Entries:[...]}, nil) → 成功, Entries 已经是按 rank 排好序
//   - (*LeaderboardResp{OK:false, Error:"..."}, nil) → Java 端业务失败 (比如维护中), 调用方读 OK 决定
//   - (nil, err) → 网络错 / JSON 解析失败 / 4xx 5xx (endpointclient 内部已归一化)
//
// 调用方约定: 拿到非 nil *resp 后, 永远先看 resp.OK, OK=false 时 Entries 可能是 nil。
func FetchLeaderboard() (*LeaderboardResp, error) {
	body, _ := json.Marshal(map[string]string{}) // 空对象: 排行榜不需要入参

	cfg := endpointclient.Config{
		Primary:   buildURL(Primary),
		Auth:      BasicAuthHeader,
		Timeout:   DefaultTimeout,
	}
	if Secondary != "" {
		cfg.Secondary = buildURL(Secondary)
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

	var lr LeaderboardResp
	if err := json.Unmarshal(raw, &lr); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w (body=%s)", err, string(raw))
	}
	return &lr, nil
}

// buildURL 在 base URL 上拼 3 个固定 query 参数 (channelID=PC, serviceID=..., seqNo=<秒级时间戳>)。
//
// 跟 internal/license.appendURLParams 同款:
//   - 任何 key/value 为空的就跳过
//   - 使用 net/url.Values 做 URL-encode, 避免值含空格 / & / = 等字符破坏 URL
func buildURL(base string) string {
	v := url.Values{}
	v.Set("channelID", ChannelIDVal)
	v.Set("serviceID", ServiceID)
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
