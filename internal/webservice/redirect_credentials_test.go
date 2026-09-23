package webservice

// redirect_credentials_test.go — OTH-02「WSDL 重定向凭据隔离仍有两条缺口」的回归测试。
//
// 审计复现的两个缺口：
//  1. 自定义认证头：makeSafeHTTPClient 没有使用 authHeaders 参数，只剥离
//     Authorization / Proxy-Authorization / Cookie 三个固定头，用户配置的
//     X-API-Key 等头会原样跟到跨源目标。
//  2. Authorization 多跳恢复：判定基准是 via[len(via)-1]（上一跳），而 Go 的
//     net/http 在每次重定向前都会从**初始请求**重新复制 Header，因此
//     A→B→B（同主机不同端口）的第二跳被当成"同源"，把 A 的凭据又带了上去。
//
// 这些用例一律断言"目标服务器真实收到的 Header"（httptest handler 里读取
// r.Header），而不是断言 CheckRedirect 被调用过。

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const auditXSDBody = `<?xml version="1.0" encoding="UTF-8"?>
<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="http://example.com/audit"/>`

// auditCredentialHeaders 是审计要求逐项校验的凭据头：
// Authorization / Cookie / Proxy-Authorization 三个固定敏感头 + 用户自定义头。
var auditCredentialHeaders = []string{
	"Authorization",
	"Proxy-Authorization",
	"Cookie",
	"X-Api-Key",
	"X-Custom-Auth",
}

// auditAuthHeaders 是用户配置的凭据集合，覆盖固定敏感头与自定义头。
func auditAuthHeaders() map[string]string {
	return map[string]string{
		"Authorization":       "Bearer audit-secret",
		"Proxy-Authorization": "Basic audit-proxy-secret",
		"Cookie":              "session=audit-cookie",
		"X-API-Key":           "audit-secret",
		"X-Custom-Auth":       "audit-custom-secret",
	}
}

// auditHeaderRecorder 记录每个路径上目标服务器真实收到的请求头。
type auditHeaderRecorder struct {
	mu   sync.Mutex
	seen map[string]http.Header
}

func newAuditHeaderRecorder() *auditHeaderRecorder {
	return &auditHeaderRecorder{seen: map[string]http.Header{}}
}

func (rec *auditHeaderRecorder) record(key string, r *http.Request) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.seen[key] = r.Header.Clone()
}

func (rec *auditHeaderRecorder) header(key string) http.Header {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.seen[key]
}

// assertNoCredentials 断言目标服务器真实收到的那一跳没有任何凭据头。
func assertNoCredentials(t *testing.T, label string, h http.Header) {
	t.Helper()
	if h == nil {
		t.Fatalf("%s: 目标服务器没有收到该请求，无法断言", label)
	}
	for _, name := range auditCredentialHeaders {
		if v := h.Get(name); v != "" {
			t.Errorf("%s: 跨源跳转泄露了 %s=%q（这是目标服务器真实收到的 Header）", label, name, v)
		}
	}
}

// assertCredentials 断言同源目标仍然携带凭据（同源能力不得退化）。
func assertCredentials(t *testing.T, label string, h http.Header, want map[string]string) {
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

// TestAuditSchemaRedirectCredentialIsolation 覆盖外部 XSD 拉取的 A→B / A→B→B /
// 同源 / 跨源直接 import 全部组合，并断言目标端真实收到的 Header。
func TestAuditSchemaRedirectCredentialIsolation(t *testing.T) {
	rec := newAuditHeaderRecorder()
	auth := auditAuthHeaders()

	var serverA, serverB *httptest.Server

	// 目标服务 B：跨源目标，且带一条 B→B（同主机不同端口）的第二跳。
	serverB = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/one":
			rec.record("B-one", r)
			http.Redirect(w, r, serverB.URL+"/two", http.StatusFound)
		case "/two":
			rec.record("B-two", r)
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(auditXSDBody))
		case "/one-back":
			rec.record("B-one-back", r)
			http.Redirect(w, r, serverA.URL+"/back-target.xsd", http.StatusFound)
		case "/direct.xsd":
			rec.record("B-direct", r)
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(auditXSDBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverB.Close()

	// 可信源 A：同源目标 + 跨源重定向入口。
	serverA = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/direct-same.xsd":
			rec.record("A-direct", r)
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(auditXSDBody))
		case "/same-origin":
			http.Redirect(w, r, serverA.URL+"/same-target.xsd", http.StatusFound)
		case "/same-target.xsd":
			rec.record("A-same-target", r)
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(auditXSDBody))
		case "/redir-to-b":
			http.Redirect(w, r, serverB.URL+"/one", http.StatusFound)
		case "/redir-b-back":
			http.Redirect(w, r, serverB.URL+"/one-back", http.StatusFound)
		case "/back-target.xsd":
			rec.record("A-back-target", r)
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(auditXSDBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverA.Close()

	baseURI := serverA.URL + "/service.wsdl"
	resolve := func(t *testing.T, location string) {
		t.Helper()
		res := NewSchemaResolver(SchemaResolverConfig{BaseURI: baseURI, AuthHeaders: auth})
		deps, warns := res.ResolveGraph([]xsdImport{{SchemaLocation: location}}, newSchemaIndex())
		if len(deps) == 0 || deps[0].Status != "loaded" {
			t.Fatalf("加载失败: location=%s deps=%+v warns=%v", location, deps, warns)
		}
	}

	// 1. 同源直接 import：凭据必须保留。
	resolve(t, serverA.URL+"/direct-same.xsd")
	assertCredentials(t, "同源直接 import", rec.header("A-direct"), auth)

	// 2. 同源重定向：凭据必须保留。
	resolve(t, serverA.URL+"/same-origin")
	assertCredentials(t, "同源重定向", rec.header("A-same-target"), auth)

	// 3. 跨源直接 import（WSDL-01 既有修复）：不得携带凭据。
	resolve(t, serverB.URL+"/direct.xsd")
	assertNoCredentials(t, "跨源直接 import (WSDL-01)", rec.header("B-direct"))

	// 4. A→B→B：第一跳跨源，第二跳同主机不同端口；两跳都不得携带凭据。
	//    这正是审计缺口 2：旧实现按 via[len(via)-1] 判定，第二跳会恢复 A 的凭据。
	resolve(t, serverA.URL+"/redir-to-b")
	assertNoCredentials(t, "A→B 第一跳", rec.header("B-one"))
	assertNoCredentials(t, "A→B→B 第二跳（同主机不同端口）", rec.header("B-two"))

	// 5. A→B→A：跨源后回到最初携带凭据的可信源，允许在该可信源上恢复凭据；
	//    中间跳 B 仍然不得携带凭据。
	resolve(t, serverA.URL+"/redir-b-back")
	assertNoCredentials(t, "A→B→A 的 B 跳", rec.header("B-one-back"))
	assertCredentials(t, "A→B→A 回到可信源", rec.header("A-back-target"), auth)
}

// TestAuditCredentialRedirectGuardOriginMatrix 用真实可解析的 127.0.0.1 / localhost
// 组合逐项校验 origin 判定（协议、主机、有效端口），走完整的 CheckRedirect 策略，
// 并确认程序自己设置的非敏感头在任何跳转下都保留。
func TestAuditCredentialRedirectGuardOriginMatrix(t *testing.T) {
	const trusted = "http://127.0.0.1:18080"
	creds := auditAuthHeaders()
	guard := newCredentialRedirectGuard(trusted, creds)

	cases := []struct {
		name         string
		next         string
		wantStripped bool
	}{
		{"同源不同路径深度", trusted + "/a/b.xsd", false},
		{"同源带默认端口写法", "http://127.0.0.1:18080/c.xsd", false},
		{"同主机换端口", "http://127.0.0.1:18081/c.xsd", true},
		{"同主机不同写法(localhost)", "http://localhost:18080/c.xsd", true},
		{"协议降级 http→https", "https://127.0.0.1:18080/c.xsd", true},
		{"回到可信源", trusted + "/back.xsd", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			via := []*http.Request{httptest.NewRequest(http.MethodGet, trusted+"/start.xsd", nil)}
			req := httptest.NewRequest(http.MethodGet, tc.next, nil)
			for k, v := range creds {
				req.Header.Set(k, v)
			}
			// 程序自己设置的非敏感头，任何跳转都必须保留。
			req.Header.Set("User-Agent", "kairo-wsdl/0.1")
			req.Header.Set("Accept", "text/xml")

			if err := guard(req, via); err != nil {
				t.Fatalf("guard(%s) 返回错误: %v", tc.next, err)
			}
			if ua := req.Header.Get("User-Agent"); ua != "kairo-wsdl/0.1" {
				t.Errorf("程序设置的 User-Agent 被误删: %q", ua)
			}
			if acc := req.Header.Get("Accept"); acc != "text/xml" {
				t.Errorf("程序设置的 Accept 被误删: %q", acc)
			}
			for name := range creds {
				got := req.Header.Get(name)
				if tc.wantStripped && got != "" {
					t.Errorf("跨源跳转 %s 未剥离 %s=%q", tc.next, name, got)
				}
				if !tc.wantStripped && got == "" {
					t.Errorf("同源跳转 %s 误删了 %s", tc.next, name)
				}
			}
		})
	}
}

// TestAuditCredentialRedirectPolicySubdomainChains 覆盖子域链路。
//
// 本机没有可解析的子域 fixture：ValidateEndpointURL 对非 IP 主机做真实 DNS 校验，
// 联网解析 *.example.com 会让测试依赖网络。因此这一组直接校验 origin 判定，
// 结果同样只取决于这一行判定（端到端"目标真实收到的 Header"由上面的 httptest 用例覆盖）。
func TestAuditCredentialRedirectPolicySubdomainChains(t *testing.T) {
	cases := []struct {
		name      string
		trusted   string
		next      string
		wantStrip bool
	}{
		// A 与其子域不同源：Go 标准库按 hostname/subdomain 认为"安全"不自动剥离，
		// 因此必须由这里的严格同源判定兜住。
		{"可信源→子域", "https://example.com", "https://sub.example.com/x", true},
		{"可信源→另一子域", "https://example.com", "https://other.example.com/x", true},
		// 子域内部继续跳转同样不得恢复 A 的凭据。
		{"子域→同子域", "https://example.com", "https://sub.example.com/y", true},
		// 可信基准是子域时，子域自身仍是同源。
		{"子域可信基准→自身", "https://sub.example.com", "https://sub.example.com/y", false},
		// 父域与子域互不同源（双向）。
		{"子域可信基准→父域", "https://sub.example.com", "https://example.com/y", true},
		{"可信源显式 443 端口", "https://example.com", "https://example.com:443/x", false},
		{"无凭据基准", "", "https://example.com/x", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldStripCredentialsOnRedirect(tc.trusted, tc.next); got != tc.wantStrip {
				t.Errorf("shouldStripCredentialsOnRedirect(%q, %q) = %v, want %v",
					tc.trusted, tc.next, got, tc.wantStrip)
			}
		})
	}
}

// TestAuditRootOriginIsTheExplicitTarget 固化"根 URL 即可信源"的口径：
// 用户显式填写的根 WSDL URL 就是凭据本该去的源，只有它的重定向目标才需要判定。
func TestAuditRootOriginIsTheExplicitTarget(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"http://127.0.0.1:8080/service?wsdl", "http://127.0.0.1:8080"},
		{"https://Example.COM/WSDL", "https://example.com"},
		{"http://example.com:80/x", "http://example.com:80"},
		{"file:///C:/tmp/x.wsdl", ""},
		{"memory:///inline", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := OriginOf(tc.raw); got != tc.want {
			t.Errorf("OriginOf(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
