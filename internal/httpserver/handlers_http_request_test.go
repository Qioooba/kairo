package httpserver

import (
	"context"
	"testing"
	"time"
)

// TestSSRF_GuardEnabledBlocksPrivate 验证 auth 启用（ssrfGuard=true）时，
// 内网/本机地址被两层防护拦截：
//   - validateOutboundHTTPURL（外层 URL 校验，应在拨号前就拒掉回环 IP 字面量）
//   - safeHTTPDialContext（内层拨号校验，DNS rebinding 双重校验也要拦掉）
//
// 这两个调用都不会实际发起网络连接。
func TestSSRF_GuardEnabledBlocksPrivate(t *testing.T) {
	// 外层：validateOutboundHTTPURL 应直接拒掉 http://127.0.0.1
	if err := validateOutboundHTTPURL("http://127.0.0.1", true); err == nil {
		t.Errorf(`validateOutboundHTTPURL("http://127.0.0.1", true) 应返回 error，实际 nil`)
	}

	// 内层：safeHTTPDialContext 对 127.0.0.1:80 也必须拒（即便外层漏过）。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := safeHTTPDialContext(ctx, "tcp", "127.0.0.1:80", true)
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf(`safeHTTPDialContext("127.0.0.1:80", true) 应返回 error，实际成功`)
	}
}

// TestSSRF_BlockedIPLiterals 验证 auth 启用时各类内网/本机 IP 字面量都被 validateOutboundHTTPURL 拦截。
func TestSSRF_BlockedIPLiterals(t *testing.T) {
	cases := []string{
		"http://127.0.0.1",
		"http://127.0.0.1:8080",
		"http://10.0.0.1",
		"http://192.168.1.1",
		"http://172.16.0.1",
		"http://169.254.1.1",
		"http://[::1]",
		"http://0.0.0.0",
	}
	for _, url := range cases {
		if err := validateOutboundHTTPURL(url, true); err == nil {
			t.Errorf("validateOutboundHTTPURL(%q, true) 应返回 error", url)
		}
	}
}

// TestSSRF_GuardDisabledAllowsPrivate 验证本机无 auth 场景（ssrfGuard=false）时，
// 内网/本机地址放行 —— 这正是「内网 HTTP 测试台」的核心场景。
func TestSSRF_GuardDisabledAllowsPrivate(t *testing.T) {
	cases := []string{
		"http://127.0.0.1",
		"http://127.0.0.1:8080",
		"http://localhost:8080",
		"http://10.0.0.1",
		"http://192.168.1.1",
		"http://172.16.0.1",
		"http://169.254.169.254",
		"http://[::1]",
	}
	for _, url := range cases {
		if err := validateOutboundHTTPURL(url, false); err != nil {
			t.Errorf("validateOutboundHTTPURL(%q, false) 不应报错, 得到 %v", url, err)
		}
	}
}
