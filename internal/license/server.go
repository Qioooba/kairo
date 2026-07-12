package license

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

// timestampSentinelValue 是 URLParamV3 的特殊值, 命中后在拼 URL 时用
// 当前秒级时间戳 (time.Now().Unix()) 替换。用于对方接口要求"每次请求
// seqNo 不同"的场景。
const timestampSentinelValue = "<TIMESTAMP>"

// ===== 激活服务配置 =====
//
// 默认值硬编码在源码里, 直接 go build 即可生效, 不用 ldflags 注入。
// 改完下面这些 var 的值, 重新编译就能切换到另一套激活服务
// (主备地址 / 认证串 / URL 参数)。
//
// 接口形态: POST /credit/httpInterface?channelID=PC&serviceID=KairoActivateAction&seqNo=<秒级时间戳>
// 请求体:   {"secret_key": "<激活码>", "ip": "<本机IP>"}
// 响应:     {"ok": true/false, "error": "..."}
//
// 用 var (不是 const) 是为了:
//
//	1) 测试可以临时改值指向 httptest server
//	2) 万一未来要"按环境切换" (开发态指向 mock, 生产态指向真实), 不用改架构
//
// 默认值是真实生产配置, 不是 PLACEHOLDER。
var (
	// LicenseServerPrimary 主激活服务地址
	LicenseServerPrimary = "http://66.0.34.199:9080/credit/httpInterface"
	// LicenseServerSecondary 备用激活服务地址 (主地址挂了自动切)
	LicenseServerSecondary = "http://66.0.34.198:9080/credit/httpInterface"
	// BasicAuthHeader POST 请求 Authorization 头的 "Basic <这里>" 部分 (base64 串, 不含 "Basic " 前缀)
	BasicAuthHeader = "anN5aDpqc3loQDEyMw=="

	// URL 上的 3 个固定 query 参数
	URLParamK1 = "channelID"
	URLParamV1 = "PC"
	URLParamK2 = "serviceID"
	URLParamV2 = "KairoActivateAction"
	URLParamK3 = "seqNo"
	// URLParamV3 是时间戳 sentinel, 拼 URL 时自动用当前秒级时间戳替换
	URLParamV3 = timestampSentinelValue
)

// ActivateResp Java 端返回的格式 (Map → JSON)。
type ActivateResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// callActivate 调 Java 端激活, 主地址失败自动试备用地址。
//
// 触发条件:
//   - 由 httpserver 的 /api/license/activate handler 调用
//   - 内部已经拿到前端输的激活码 + 本机 IP
//
// 返回:
//   - (*ActivateResp{OK:true}, nil) → 服务端通过, 本地写证书
//   - (*ActivateResp{OK:false, Error:"..."}, nil) → 服务端拒绝 (激活码无效/IP 不匹配等)
//   - (nil, error) → 网络错误 / 所有地址都不可达
func callActivate(code, ip string) (*ActivateResp, error) {
	body, _ := json.Marshal(map[string]string{
		"secret_key": code,
		"ip":         ip,
	})

	urls := buildActivateURLs()
	if len(urls) == 0 {
		return nil, fmt.Errorf("激活服务地址未配置")
	}

	var lastErr error
	for _, url := range urls {
		resp, err := postActivateJSON(url, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("所有激活服务都不可达: %v", lastErr)
}

// buildActivateURLs 组装实际请求的 URL 列表 (主 + 备)。
func buildActivateURLs() []string {
	var urls []string
	for _, base := range []string{LicenseServerPrimary, LicenseServerSecondary} {
		if base == "" {
			continue
		}
		urls = append(urls, appendURLParams(base))
	}
	return urls
}

// appendURLParams 在 URL 上拼接 3 个固定 query 参数 (kv1=v1&kv2=v2&kv3=v3)。
//
// 约定:
//   - 任何 key/value 为空的就跳过
//   - value == "<TIMESTAMP>" 的, 用当前秒级时间戳替换 (time.Now().Unix())
//   - 使用 net/url.Values 做 URL-encode, 避免值含空格 / & / = 等字符破坏 URL
func appendURLParams(base string) string {
	v := url.Values{}
	for _, kv := range [][2]string{
		{URLParamK1, URLParamV1},
		{URLParamK2, URLParamV2},
		{URLParamK3, URLParamV3},
	} {
		k, val := kv[0], kv[1]
		if k == "" || val == "" {
			continue
		}
		// 命中时间戳 sentinel: 用当前秒级时间戳替换
		if val == timestampSentinelValue {
			val = strconv.FormatInt(time.Now().Unix(), 10)
		}
		v.Set(k, val)
	}
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

// postActivateJSON POST JSON body 到指定 URL, 带 Basic auth header。
//
// 内部走 internal/endpointclient, 享受主备切换 + 4xx/5xx 错误归一化。
// 函数签名 / 行为完全兼容历史版本 (server_test.go / simulate_test.go 直接调它)。
//
// 超时: 5 秒 (激活请求应该秒回, 5s 还连不上视为不可用)。
func postActivateJSON(url string, body []byte) (*ActivateResp, error) {
	resp, err := endpointclient.Call(endpointclient.Config{
		Primary: url,
		Auth:    BasicAuthHeader,
		Timeout: 5 * time.Second,
	}, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var ar ActivateResp
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w (body=%s)", err, string(raw))
	}
	return &ar, nil
}