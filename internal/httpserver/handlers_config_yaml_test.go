package httpserver

// handlers_config_yaml_test.go — BE-004 回归测试：
// 验证 redactConfigYAML 能正确处理 YAML 多行字符串、flow 风格、嵌套 map，
// 非敏感字段保留原值，YAML 语法错误时回退到逐行脱敏。

import (
	"os"
	"strings"
	"testing"
)

// TestRedactConfigYAML_MultilinePassword 验证 YAML 多行字符串（|- 块标量）密码被脱敏。
// 旧的逐行扫描只能匹配 "key: value" 单行格式，多行密码会原样泄露。
func TestRedactConfigYAML_MultilinePassword(t *testing.T) {
	in := []byte("password: |-\n  MULTI\n  LINE\n  SECRETVAL\n")
	out := redactConfigYAML(in)
	s := string(out)
	if strings.Contains(s, "MULTI") || strings.Contains(s, "SECRETVAL") {
		t.Errorf("multiline password not redacted: %s", s)
	}
	if !strings.Contains(s, "***") {
		t.Errorf("expected *** in output: %s", s)
	}
}

// TestRedactConfigYAML_FlowStyle 验证 flow 风格 YAML 中的密码被脱敏。
// 旧逻辑只看行首，flow 风格 "servers: [{password: \"abc123\"}]" 不会被命中。
func TestRedactConfigYAML_FlowStyle(t *testing.T) {
	in := []byte("servers: [{password: \"abc123\"}]\n")
	out := redactConfigYAML(in)
	s := string(out)
	if strings.Contains(s, "abc123") {
		t.Errorf("flow style password not redacted: %s", s)
	}
	if !strings.Contains(s, "***") {
		t.Errorf("expected *** in output: %s", s)
	}
}

// TestRedactConfigYAML_NestedMap 验证多层嵌套 map 中的密码被脱敏。
func TestRedactConfigYAML_NestedMap(t *testing.T) {
	in := []byte("db:\n  connection:\n    password: deepsecret\n")
	out := redactConfigYAML(in)
	s := string(out)
	if strings.Contains(s, "deepsecret") {
		t.Errorf("nested map password not redacted: %s", s)
	}
	if !strings.Contains(s, "***") {
		t.Errorf("expected *** in output: %s", s)
	}
}

// TestRedactConfigYAML_NonSensitivePreserved 验证非敏感字段保留原值。
func TestRedactConfigYAML_NonSensitivePreserved(t *testing.T) {
	in := []byte("name: \"信贷生产\"\nport: 18080\n")
	out := redactConfigYAML(in)
	s := string(out)
	if !strings.Contains(s, "信贷生产") {
		t.Errorf("non-sensitive name field should be preserved: %s", s)
	}
	if strings.Contains(s, "***") {
		t.Errorf("non-sensitive fields should not be redacted: %s", s)
	}
}

// TestRedactConfigYAML_CaseInsensitive 验证大小写不敏感匹配。
func TestRedactConfigYAML_CaseInsensitive(t *testing.T) {
	in := []byte("PASSWORD: caseUpper\ntoken: caseLower\n")
	out := redactConfigYAML(in)
	s := string(out)
	if strings.Contains(s, "caseUpper") {
		t.Errorf("uppercase PASSWORD not redacted: %s", s)
	}
	if strings.Contains(s, "caseLower") {
		t.Errorf("token not redacted: %s", s)
	}
}

// TestRedactConfigYAML_InvalidYAMLFallback 验证 YAML 语法错误时回退到逐行脱敏，
// 避免完全无法导出。
func TestRedactConfigYAML_InvalidYAMLFallback(t *testing.T) {
	// 未闭合的 flow sequence — yaml.Unmarshal 会报错
	in := []byte("name: \"ok\"\npassword: [unclosed\n")
	out := redactConfigYAML(in)
	s := string(out)
	if !strings.Contains(s, "***") {
		t.Errorf("invalid YAML fallback should still redact password: %s", s)
	}
	if !strings.Contains(s, "ok") {
		t.Errorf("non-sensitive field should be preserved in fallback: %s", s)
	}
}

// TestConfigExport_RedactsPassword 集成测试：调用 GET /api/config/export，
// 验证响应体不含明文密码（覆盖多行 + 嵌套场景）。
func TestConfigExport_RedactsPassword(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)
	cfgContent := []byte("app:\n  name: \"信贷生产\"\n  host: \"127.0.0.1\"\nsystems:\n  - name: \"信贷生产\"\n    servers:\n      - name: \"mock-1\"\n        host: \"10.0.0.1\"\n        port: 22\n        username: \"ops\"\n        auth_type: \"password\"\n        password: |\n          PLAINTEXT_PW_LINE1\n          PLAINTEXT_PW_LINE2\n        log_dirs:\n          - name: \"SystemOut\"\n            path: \"/opt/logs/SystemOut\"\n")
	if err := os.WriteFile(mgr.Path(), cfgContent, 0o600); err != nil {
		t.Fatal(err)
	}

	w := doRequest(srv, "GET", "/api/config/export", nil)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "PLAINTEXT_PW_LINE1") || strings.Contains(body, "PLAINTEXT_PW_LINE2") {
		t.Errorf("exported config should not contain plaintext password: %s", body)
	}
	if !strings.Contains(body, "***") {
		t.Errorf("exported config should contain redacted password: %s", body)
	}
	if !strings.Contains(body, "信贷生产") {
		t.Errorf("non-sensitive field should be preserved: %s", body)
	}
}

// TestConfigExport_RedactsKairoAndEndpointAuth 验证导出脱敏 kairo token 与
// internal_endpoints.*.auth，同时保留顶层 auth 容器（enabled/tokens 名称）。
func TestConfigExport_RedactsKairoAndEndpointAuth(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)
	cfgContent := []byte(`app:
  name: "信贷生产"
  kairo: "111222"
auth:
  enabled: true
  tokens:
    - name: ops
      token: TOP_SECRET_TOKEN
internal_endpoints:
  license_activate:
    primary: "http://66.0.34.199:9080/credit/httpInterface"
    auth: "BASE64_AUTH_SECRET"
`)
	if err := os.WriteFile(mgr.Path(), cfgContent, 0o600); err != nil {
		t.Fatal(err)
	}

	w := doRequest(srv, "GET", "/api/config/export", nil)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "111222") {
		t.Errorf("exported config should not contain kairo token: %s", body)
	}
	if strings.Contains(body, "BASE64_AUTH_SECRET") {
		t.Errorf("exported config should not contain endpoint auth: %s", body)
	}
	if strings.Contains(body, "TOP_SECRET_TOKEN") {
		t.Errorf("exported config should not contain token secret: %s", body)
	}
	// 顶层 auth 容器不应被整段脱敏（值为 map 时保留并递归）：
	// enabled 字段应保留，而 tokens 列表因命中 "token" 子串被整段脱敏（既有行为）。
	if !strings.Contains(body, "enabled") {
		t.Errorf("top-level auth.enabled should be preserved: %s", body)
	}
}
