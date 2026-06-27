package httpserver

import (
	"context"
	"testing"
	"time"
)

// TestSSRF_LoopbackIPBlocked 验证 url=http://127.0.0.1 被两层防护同时拦截：
//   - validateOutboundHTTPURL（外层 URL 校验，应在拨号前就拒掉回环 IP 字面量）
//   - safeHTTPDialContext（内层拨号校验，BE-018 新增的 DNS rebinding 双重校验也要拦掉）
//
// 这两个调用都不会实际发起网络连接。
func TestSSRF_LoopbackIPBlocked(t *testing.T) {
	// 外层：validateOutboundHTTPURL 应直接拒掉 http://127.0.0.1
	if err := validateOutboundHTTPURL("http://127.0.0.1"); err == nil {
		t.Errorf(`validateOutboundHTTPURL("http://127.0.0.1") 应返回 error，实际 nil`)
	}

	// 内层：safeHTTPDialContext 对 127.0.0.1:80 也必须拒（即便外层漏过）。
	// 即便 rejectPrivateHost 第一道没拦住，BE-018 在 LookupIPAddr 之后新增的
	// isBlockedHTTPIP 双重校验也要拦住。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := safeHTTPDialContext(ctx, "tcp", "127.0.0.1:80")
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf(`safeHTTPDialContext("127.0.0.1:80") 应返回 error，实际成功`)
	}
}

// TestSSRF_BlockedIPLiterals 验证各类内网/本机 IP 字面量都被 validateOutboundHTTPURL 拦截。
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
		if err := validateOutboundHTTPURL(url); err == nil {
			t.Errorf("validateOutboundHTTPURL(%q) 应返回 error", url)
		}
	}
}
