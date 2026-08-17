package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kairo/internal/webservice"
)

// TestWSDLImportURL_BlocksDangerousTargets 验证 /api/wsdl/import-url 拒绝
// link-local（含云元数据 169.254.169.254）/ unspecified 目标，放行普通目标。
func TestWSDLImportURL_BlocksDangerousTargets(t *testing.T) {
	cases := []struct {
		url    string
		status int
	}{
		{"http://169.254.169.254/latest/meta-data/", 400},
		{"http://169.254.0.1/x", 400},
		{"http://0.0.0.0/x", 400},
		{"http://[fe80::1]/x", 400},
	}
	for _, c := range cases {
		body, _ := json.Marshal(map[string]string{"url": c.url})
		req := httptest.NewRequest(http.MethodPost, "/api/wsdl/import-url", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		(&Server{}).handleWSDLImportURL(rr, req)
		if rr.Code != c.status {
			t.Errorf("url=%s 期望 status=%d, 得到 %d body=%s", c.url, c.status, rr.Code, rr.Body.String())
		}
	}
}

// TestValidateEndpointURL 直接校验 webservice 的 SSRF 判定函数。
func TestValidateEndpointURL(t *testing.T) {
	blocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://169.254.0.1/x",
		"http://0.0.0.0/x",
		"http://[::]/x",
		"http://[fe80::1]/x",
		"http://224.0.0.1/x",
	}
	for _, u := range blocked {
		if err := webservice.ValidateEndpointURL(u); err == nil {
			t.Errorf("ValidateEndpointURL(%q) 应返回 error", u)
		}
	}
	// loopback / 私有网段 / 公网域名 应放行（loopback 供本机 mock 调试用）。
	allowed := []string{
		"http://127.0.0.1:8080/mock",
		"http://localhost:8080/x",
		"http://10.1.2.3/x",
		"http://192.168.1.10/x",
		"http://example.com/x",
	}
	for _, u := range allowed {
		if err := webservice.ValidateEndpointURL(u); err != nil {
			t.Errorf("ValidateEndpointURL(%q) 不应报错, 得到 %v", u, err)
		}
	}
}

// TestSOAPSend_BlocksDangerousTargets 验证 webservice.Send 拒绝 link-local 目标。
func TestSOAPSend_BlocksDangerousTargets(t *testing.T) {
	resp := webservice.Send(webservice.SendRequest{
		Endpoint: "http://169.254.169.254/latest/meta-data/",
		Body:     "<x/>",
	})
	if resp.Error == "" {
		t.Fatalf("Send 到 link-local 应返回 error")
	}
	if !strings.Contains(resp.Error, "危险地址") {
		t.Errorf("错误信息应包含拦截说明, 得到 %q", resp.Error)
	}
}
