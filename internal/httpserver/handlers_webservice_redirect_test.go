package httpserver

// handlers_webservice_redirect_test.go — OTH-02 根 WSDL 下载器的回归测试。
//
// 审计指出 internal/httpserver/handlers_webservice.go 里的根 WSDL URL 下载器
// 复制了 schema_resolver.go 的同一套有缺陷的重定向策略（只按上一跳判跨源、
// 只删三个固定头）。修复后两者共用 internal/webservice 里的同一份策略，
// 本文件断言目标服务器**真实收到**的 Header。

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// rootWsdlCredentialHeaders 是逐项校验的凭据头（固定敏感头 + 用户自定义头）。
var rootWsdlCredentialHeaders = []string{
	"Authorization",
	"Proxy-Authorization",
	"Cookie",
	"X-Api-Key",
	"X-Custom-Auth",
}

func rootWsdlAuthHeaders() map[string]string {
	return map[string]string{
		"Authorization":       "Bearer audit-secret",
		"Proxy-Authorization": "Basic audit-proxy-secret",
		"Cookie":              "session=audit-cookie",
		"X-API-Key":           "audit-secret",
		"X-Custom-Auth":       "audit-custom-secret",
	}
}

type rootWsdlRecorder struct {
	mu   sync.Mutex
	seen map[string]http.Header
}

func newRootWsdlRecorder() *rootWsdlRecorder {
	return &rootWsdlRecorder{seen: map[string]http.Header{}}
}

func (rec *rootWsdlRecorder) record(key string, r *http.Request) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.seen[key] = r.Header.Clone()
}

func (rec *rootWsdlRecorder) header(key string) http.Header {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.seen[key]
}

func assertRootWsdlNoCredentials(t *testing.T, label string, h http.Header) {
	t.Helper()
	if h == nil {
		t.Fatalf("%s: 目标服务器没有收到该请求，无法断言", label)
	}
	for _, name := range rootWsdlCredentialHeaders {
		if v := h.Get(name); v != "" {
			t.Errorf("%s: 跨源跳转泄露了 %s=%q（这是目标服务器真实收到的 Header）", label, name, v)
		}
	}
}

func assertRootWsdlCredentials(t *testing.T, label string, h http.Header, want map[string]string) {
	t.Helper()
	if h == nil {
		t.Fatalf("%s: 目标服务器没有收到该请求，无法断言", label)
	}
	for name, value := range want {
		if got := h.Get(name); got != value {
			t.Errorf("%s: 同源请求丢失 %s，want %q got %q", label, name, value, got)
		}
	}
}

// TestAuditRootWSDLRedirectCredentialIsolation 覆盖根 WSDL 拉取的
// 同源直连 / 同源重定向 / 跨源直连 / A→B→B（同主机不同端口）。
func TestAuditRootWSDLRedirectCredentialIsolation(t *testing.T) {
	rec := newRootWsdlRecorder()
	auth := rootWsdlAuthHeaders()

	var serverA, serverB *httptest.Server

	serverB = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/one":
			rec.record("B-one", r)
			http.Redirect(w, r, serverB.URL+"/two", http.StatusFound)
		case "/two":
			rec.record("B-two", r)
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(sampleWSDLForHandler))
		case "/direct.wsdl":
			rec.record("B-direct", r)
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(sampleWSDLForHandler))
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverB.Close()

	serverA = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same-direct.wsdl":
			rec.record("A-direct", r)
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(sampleWSDLForHandler))
		case "/same-root.wsdl":
			http.Redirect(w, r, serverA.URL+"/wsdl-target", http.StatusFound)
		case "/wsdl-target":
			rec.record("A-target", r)
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(sampleWSDLForHandler))
		case "/root.wsdl":
			http.Redirect(w, r, serverB.URL+"/one", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverA.Close()

	srv, _, _, _ := newTestServer(t)
	importURL := func(t *testing.T, target string) {
		t.Helper()
		w := doRequest(srv, "POST", "/api/wsdl/import-url", map[string]any{
			"url":     target,
			"headers": auth,
		})
		if w.Code != 200 {
			t.Fatalf("导入 %s 失败: status=%d body=%s", target, w.Code, w.Body.String())
		}
	}

	// 1. 同源直连：凭据必须保留。
	importURL(t, serverA.URL+"/same-direct.wsdl")
	assertRootWsdlCredentials(t, "根 WSDL 同源直连", rec.header("A-direct"), auth)

	// 2. 同源重定向：凭据必须保留。
	importURL(t, serverA.URL+"/same-root.wsdl")
	assertRootWsdlCredentials(t, "根 WSDL 同源重定向", rec.header("A-target"), auth)

	// 3. 跨源直连：根 URL 是用户显式填写并配置凭据的目标，因此它本身就是
	//    "首次携带凭据的可信源"，凭据必须照常发送（修复不得把这里误伤成不透传）。
	//    跨源"直接 import"（无重定向）的隔离由 schema resolver 承担，见
	//    internal/webservice/redirect_credentials_test.go。
	importURL(t, serverB.URL+"/direct.wsdl")
	assertRootWsdlCredentials(t, "根 WSDL 直连（用户指定的目标源）", rec.header("B-direct"), auth)

	// 4. A→B→B：两跳都不得携带凭据；程序设置的 User-Agent 仍须保留。
	importURL(t, serverA.URL+"/root.wsdl")
	assertRootWsdlNoCredentials(t, "根 WSDL A→B 第一跳", rec.header("B-one"))
	assertRootWsdlNoCredentials(t, "根 WSDL A→B→B 第二跳（同主机不同端口）", rec.header("B-two"))
	if ua := rec.header("B-two").Get("User-Agent"); ua != "kairo-wsdl/0.1" {
		t.Errorf("跨源跳转丢失了程序设置的 User-Agent，got %q", ua)
	}
}
