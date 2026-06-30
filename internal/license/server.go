package license

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LicenseServerPrimary 主激活服务地址 (编译时 ldflags 注入)。
//
//   go build -ldflags "-X 'kairo/internal/license.LicenseServerPrimary=http://10.0.0.5:8080/kairo/auth/activate'"
//
// 占位符约定: 编译前保持 PLACEHOLDER, 注入后才替换为真实地址。
// runActive() 会自动跳过 PLACEHOLDER 状态, 避免上线前误调用。
var LicenseServerPrimary = "http://PLACEHOLDER-PRIMARY/kairo/auth/activate"

// LicenseServerSecondary 备用激活服务地址 (主地址挂了自动切)。
//
//   go build -ldflags "-X 'kairo/internal/license.LicenseServerSecondary=http://10.0.0.6:8080/kairo/auth/activate'"
var LicenseServerSecondary = "http://PLACEHOLDER-SECONDARY/kairo/auth/activate"

// BasicAuthHeader POST 请求 Authorization 头的 "Basic <这里>" 部分。
// 用户后续给具体字符串, 用 ldflags 注入。
//
//   go build -ldflags "-X 'kairo/internal/license.BasicAuthHeader=xxx'"
var BasicAuthHeader = "PLACEHOLDER_BASIC_AUTH"

// URL 上的 3 个固定 query 参数 (KV 形式, 用户后续给具体值)。
// 三个 key + 三个 value, 编译时全部 ldflags 注入。
var (
	URLParamK1 = "PLACEHOLDER_K1"
	URLParamV1 = "PLACEHOLDER_V1"
	URLParamK2 = "PLACEHOLDER_K2"
	URLParamV2 = "PLACEHOLDER_V2"
	URLParamK3 = "PLACEHOLDER_K3"
	URLParamV3 = "PLACEHOLDER_V3"
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
		return nil, fmt.Errorf("激活服务地址未配置 (ldflags 没注入 LicenseServerPrimary)")
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

// buildActivateURLs 组装实际请求的 URL 列表 (主 + 备, 过滤掉 PLACEHOLDER)。
func buildActivateURLs() []string {
	var urls []string
	for _, base := range []string{LicenseServerPrimary, LicenseServerSecondary} {
		if base == "" || strings.Contains(base, "PLACEHOLDER") {
			continue
		}
		urls = append(urls, appendURLParams(base))
	}
	return urls
}

// appendURLParams 在 URL 上拼接 3 个固定 query 参数 (kv1=v1&kv2=v2&kv3=v3)。
//
// 约定: 任何 key / value 还是 PLACEHOLDER 的就跳过 (用户还没注入的不发)。
func appendURLParams(base string) string {
	var params []string
	for _, kv := range [][2]string{
		{URLParamK1, URLParamV1},
		{URLParamK2, URLParamV2},
		{URLParamK3, URLParamV3},
	} {
		k, v := kv[0], kv[1]
		if k == "" || v == "" || strings.Contains(k, "PLACEHOLDER") || strings.Contains(v, "PLACEHOLDER") {
			continue
		}
		params = append(params, fmt.Sprintf("%s=%s", k, v))
	}
	if len(params) == 0 {
		return base
	}

	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + strings.Join(params, "&")
}

// postActivateJSON POST JSON body 到指定 URL, 带 Basic auth header。
//
// 超时: 5 秒 (激活请求应该秒回, 5s 还连不上视为不可用)。
func postActivateJSON(url string, body []byte) (*ActivateResp, error) {
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if BasicAuthHeader != "" && !strings.Contains(BasicAuthHeader, "PLACEHOLDER") {
		req.Header.Set("Authorization", "Basic "+BasicAuthHeader)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
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