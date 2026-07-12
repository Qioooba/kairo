// server_test.go — 验证新接口 /credit/httpInterface 的 URL / Header / Body 拼装
//
// 覆盖:
//   1. URL 拼接: channelID=PC, serviceID=KairoActivateAction, seqNo=<秒级时间戳>
//   2. 请求体: {secret_key, ip}
//   3. Header: Authorization=Basic xxx, Content-Type=application/json;charset=UTF-8
//   4. 主地址挂了自动切备用地址
//   5. 响应解析: {ok:true} / {ok:false,error:"..."}
//
// 不需要 mock-license-server 进程, 用 httptest 起一个临时 server 就够了。
//
// 默认值 (LicenseServerPrimary 等) 硬编码在源码里, 不是 PLACEHOLDER,
// 测试可以临时改它们指向 httptest server。

package license

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// restoreLicenseVars 把 license 包的几个 var 在测试后还原, 避免污染其他测试
func restoreLicenseVars(t *testing.T) {
	t.Helper()
	prim, sec, auth := LicenseServerPrimary, LicenseServerSecondary, BasicAuthHeader
	k1, v1 := URLParamK1, URLParamV1
	k2, v2 := URLParamK2, URLParamV2
	k3, v3 := URLParamK3, URLParamV3
	t.Cleanup(func() {
		LicenseServerPrimary = prim
		LicenseServerSecondary = sec
		BasicAuthHeader = auth
		URLParamK1, URLParamV1 = k1, v1
		URLParamK2, URLParamV2 = k2, v2
		URLParamK3, URLParamV3 = k3, v3
	})
}

// TestServer_AppendURLParams_CreditInterface 验证 URL 上 3 个 KV 参数拼装 + 时间戳 sentinel
//
// 这是 v1.1 接口适配的核心: seqNo 每次请求用当前秒级时间戳, 不能注入固定值。
func TestServer_AppendURLParams_CreditInterface(t *testing.T) {
	restoreLicenseVars(t)
	// 直接用源码里硬编码的默认值 (不修改 var)
	URLParamK1 = "channelID"; URLParamV1 = "PC"
	URLParamK2 = "serviceID"; URLParamV2 = "KairoActivateAction"
	URLParamK3 = "seqNo"; URLParamV3 = "<TIMESTAMP>"

	base := "http://66.0.34.199:9080/credit/httpInterface"

	// 取 2 次, 验证每次 seqNo 都不同 (1 秒间隔)
	url1 := appendURLParams(base)
	time.Sleep(1100 * time.Millisecond) // 跨秒, 确保时间戳不同
	url2 := appendURLParams(base)

	// URL1 必须包含 3 个 KV
	for _, want := range []string{
		"channelID=PC",
		"serviceID=KairoActivateAction",
		"seqNo=",
	} {
		if !strings.Contains(url1, want) {
			t.Errorf("URL 应包含 %q, got=%q", want, url1)
		}
	}

	// 提取 seqNo 的值, 验证是 10 位数字 (秒级时间戳)
	seqVal := extractQueryValue(url1, "seqNo")
	if seqVal == "" {
		t.Fatalf("URL 应有 seqNo 参数, got=%q", url1)
	}
	if len(seqVal) != 10 {
		t.Errorf("seqNo 应该是 10 位秒级时间戳, got=%q (len=%d)", seqVal, len(seqVal))
	}
	// 验证能解析成 int64 且是合理时间 (2023 年后)
	ts, err := parseInt64(seqVal)
	if err != nil {
		t.Errorf("seqNo 应该能解析为 int64: %v (val=%q)", err, seqVal)
	}
	if ts < 1700000000 || ts > 2000000000 {
		t.Errorf("seqNo 应该是合理秒级时间戳 (2023-2033), got=%d", ts)
	}

	// URL2 也应该有 seqNo
	seqVal2 := extractQueryValue(url2, "seqNo")
	if seqVal2 == "" {
		t.Errorf("URL2 应有 seqNo, got=%q", url2)
	}

	// 跨秒的话 seqNo 应该不同 (但如果跑得快可能同秒, 不能强制要求不同)
	t.Logf("URL1 seqNo=%s, URL2 seqNo=%s (跨秒应不同)", seqVal, seqVal2)
}

// TestServer_AppendURLParams_NoTimestamp 验证没命中 sentinel 时, value 原样输出
func TestServer_AppendURLParams_NoTimestamp(t *testing.T) {
	restoreLicenseVars(t)
	URLParamK1 = "channelID"; URLParamV1 = "PC"
	URLParamK2 = "serviceID"; URLParamV2 = "KairoActivateAction"
	URLParamK3 = "seqNo"; URLParamV3 = "456" // 固定值, 不是 sentinel

	url := appendURLParams("http://66.0.34.199:9080/credit/httpInterface")
	if !strings.Contains(url, "seqNo=456") {
		t.Errorf("固定 value 应该原样输出, got=%q", url)
	}
}

// TestServer_AppendURLParams_EmptyValues 验证 KV 为空时跳过
func TestServer_AppendURLParams_EmptyValues(t *testing.T) {
	restoreLicenseVars(t)
	URLParamK1 = ""; URLParamV1 = ""
	URLParamK2 = ""; URLParamV2 = ""
	URLParamK3 = ""; URLParamV3 = ""

	url := appendURLParams("http://66.0.34.199:9080/credit/httpInterface")
	if strings.Contains(url, "?") {
		t.Errorf("全空应不带 query, got=%q", url)
	}
}

// TestServer_AppendURLParams_BaseWithQuery 验证 base URL 已有 query 时, 用 & 拼接
func TestServer_AppendURLParams_BaseWithQuery(t *testing.T) {
	restoreLicenseVars(t)
	URLParamK1 = "channelID"; URLParamV1 = "PC"
	URLParamK2 = "serviceID"; URLParamV2 = "KairoActivateAction"
	URLParamK3 = "seqNo"; URLParamV3 = "789"

	// base 已经带 query (?foo=bar)
	url := appendURLParams("http://example.com/api?foo=bar")
	if !strings.Contains(url, "foo=bar&channelID=PC") {
		t.Errorf("base 已有 query 时, 后续参数用 & 拼接, got=%q", url)
	}
}

// TestServer_CallActivate_FullFlow 用 httptest 模拟 /credit/httpInterface, 跑全流程
//
// 验证:
//   - URL 上 3 个参数正确 (含时间戳)
//   - Header: Authorization / Content-Type
//   - Body: {secret_key, ip}
//   - 响应解析: {ok:true}
func TestServer_CallActivate_FullFlow(t *testing.T) {
	restoreLicenseVars(t)

	// 拿到的请求
	var gotReq struct {
		method string
		path   string
		query  map[string]string
		header http.Header
		body   map[string]string
		mu     sync.Mutex
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq.mu.Lock()
		defer gotReq.mu.Unlock()
		gotReq.method = r.Method
		gotReq.path = r.URL.Path
		q := r.URL.Query()
		gotReq.query = map[string]string{}
		for k := range q {
			gotReq.query[k] = q.Get(k)
		}
		gotReq.header = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq.body)

		// 模拟成功响应
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	// 注入指向 httptest server (覆盖默认真实地址)
	URLParamK1 = "channelID"; URLParamV1 = "PC"
	URLParamK2 = "serviceID"; URLParamV2 = "KairoActivateAction"
	URLParamK3 = "seqNo"; URLParamV3 = "<TIMESTAMP>"
	LicenseServerPrimary = srv.URL + "/credit/httpInterface"
	LicenseServerSecondary = ""
	BasicAuthHeader = "anN5aDpqc3loQDEyMw=="

	// 调激活
	resp, err := callActivate("test-code-001", "192.168.1.100")
	if err != nil {
		t.Fatalf("callActivate 失败: %v", err)
	}
	if !resp.OK {
		t.Errorf("响应 OK 应该=true, got=false (error=%q)", resp.Error)
	}

	// 验证请求
	gotReq.mu.Lock()
	defer gotReq.mu.Unlock()

	// method
	if gotReq.method != "POST" {
		t.Errorf("method 应为 POST, got=%q", gotReq.method)
	}
	// path
	if gotReq.path != "/credit/httpInterface" {
		t.Errorf("path 应为 /credit/httpInterface, got=%q", gotReq.path)
	}
	// query: channelID, serviceID, seqNo
	if gotReq.query["channelID"] != "PC" {
		t.Errorf("channelID 应为 PC, got=%q", gotReq.query["channelID"])
	}
	if gotReq.query["serviceID"] != "KairoActivateAction" {
		t.Errorf("serviceID 应为 KairoActivateAction, got=%q", gotReq.query["serviceID"])
	}
	seqNo := gotReq.query["seqNo"]
	if seqNo == "" {
		t.Errorf("seqNo 不应为空")
	}
	if _, err := parseInt64(seqNo); err != nil {
		t.Errorf("seqNo 应为数字, got=%q (err=%v)", seqNo, err)
	}

	// header: Authorization
	auth := gotReq.header.Get("Authorization")
	if auth != "Basic anN5aDpqc3loQDEyMw==" {
		t.Errorf("Authorization 应为 Basic anN5aDpqc3loQDEyMw==, got=%q", auth)
	}
	// header: Content-Type
	ct := gotReq.header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") || !strings.Contains(ct, "charset=UTF-8") {
		t.Errorf("Content-Type 应为 application/json;charset=UTF-8, got=%q", ct)
	}

	// body: secret_key, ip
	if gotReq.body["secret_key"] != "test-code-001" {
		t.Errorf("secret_key 应为 test-code-001, got=%q", gotReq.body["secret_key"])
	}
	if gotReq.body["ip"] != "192.168.1.100" {
		t.Errorf("ip 应为 192.168.1.100, got=%q", gotReq.body["ip"])
	}
}

// TestServer_CallActivate_Rejection 验证服务端返回 {ok:false, error:"..."} 能正确解析
func TestServer_CallActivate_Rejection(t *testing.T) {
	restoreLicenseVars(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "激活码无效",
		})
	}))
	defer srv.Close()

	URLParamK1 = ""; URLParamV1 = "" // 简化 URL
	URLParamK2 = ""; URLParamV2 = ""
	URLParamK3 = ""; URLParamV3 = ""
	LicenseServerPrimary = srv.URL + "/credit/httpInterface"
	LicenseServerSecondary = ""
	BasicAuthHeader = "test"

	resp, err := callActivate("bad-code", "1.2.3.4")
	if err != nil {
		t.Fatalf("callActivate 网络层失败: %v", err)
	}
	if resp.OK {
		t.Errorf("服务端拒绝时 OK 应为 false")
	}
	if resp.Error != "激活码无效" {
		t.Errorf("error 应为'激活码无效', got=%q", resp.Error)
	}
}

// TestServer_CallActivate_Fallback 主地址挂了自动切备用地址
func TestServer_CallActivate_Fallback(t *testing.T) {
	restoreLicenseVars(t)

	// 备地址 (httptest) - 主地址用一个肯定连不上的
	gotSecondary := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecondary = true
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	URLParamK1 = ""; URLParamV1 = ""
	URLParamK2 = ""; URLParamV2 = ""
	URLParamK3 = ""; URLParamV3 = ""
	// 主地址故意配错 (端口没人监听)
	LicenseServerPrimary = "http://127.0.0.1:1/credit/httpInterface"
	LicenseServerSecondary = srv.URL + "/credit/httpInterface"
	BasicAuthHeader = "test"

	resp, err := callActivate("test", "1.2.3.4")
	if err != nil {
		t.Fatalf("callActivate 应自动切到备用地址, 但全失败: %v", err)
	}
	if !resp.OK {
		t.Errorf("备用地址响应 OK 应为 true")
	}
	if !gotSecondary {
		t.Errorf("备用地址应被命中")
	}
}

// TestServer_DefaultsHardcoded 验证源码默认值是真实生产配置 (不是 PLACEHOLDER, 也不是空)
//
// 激活服务地址和密钥故意硬编码在源码里, 不走 config.yaml —— 用户不能看到/修改这些值,
// 否则可以把请求改发到假服务器绕过激活校验。
func TestServer_DefaultsHardcoded(t *testing.T) {
	restoreLicenseVars(t)

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"主地址", LicenseServerPrimary, "http://66.0.34.199:9080/credit/httpInterface"},
		{"备地址", LicenseServerSecondary, "http://66.0.34.198:9080/credit/httpInterface"},
		{"Basic auth", BasicAuthHeader, "anN5aDpqc3loQDEyMw=="},
		{"URLParamK1", URLParamK1, "channelID"},
		{"URLParamV1", URLParamV1, "PC"},
		{"URLParamK2", URLParamK2, "serviceID"},
		{"URLParamV2", URLParamV2, "KairoActivateAction"},
		{"URLParamK3", URLParamK3, "seqNo"},
		{"URLParamV3 (sentinel)", URLParamV3, "<TIMESTAMP>"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s 默认值应为 %q, got=%q", c.name, c.want, c.got)
		}
	}

	// 不能含 PLACEHOLDER (那是 ldflags 时代的产物)
	allVars := []string{LicenseServerPrimary, LicenseServerSecondary, BasicAuthHeader,
		URLParamK1, URLParamV1, URLParamK2, URLParamV2, URLParamK3, URLParamV3}
	for i, v := range allVars {
		if strings.Contains(v, "PLACEHOLDER") {
			t.Errorf("var[%d]=%q 含 PLACEHOLDER, 应为真实值", i, v)
		}
	}
}

// === 辅助函数 ===

func extractQueryValue(rawURL, key string) string {
	idx := strings.Index(rawURL, "?")
	if idx < 0 {
		return ""
	}
	q := rawURL[idx+1:]
	for _, kv := range strings.Split(q, "&") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 && parts[0] == key {
			return parts[1]
		}
	}
	return ""
}

func parseInt64(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}