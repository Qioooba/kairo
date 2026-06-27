package httpserver

// handlers_ssh_be005_test.go — BE-005 修复回归测试：
// 未配 host_key_sha256 且 allow_insecure_host_key=false（默认）时，
// /api/ssh/test 必须返回清晰错误，且不发起任何 SSH 网络连接。

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

// TestSSHTest_NoHostKey_DefaultForbidden 验证 BE-005 fail-closed 默认行为：
// app.allow_insecure_host_key 未配置（nil → false），server 未配 host_key_sha256
// → /api/ssh/test 应返 5xx 且错误信息包含 host_key 提示，引导用户配置。
func TestSSHTest_NoHostKey_DefaultForbidden(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	srv, mgr, _, _ := newTestServer(t)
	// 显式把 AllowInsecureHostKey 改回 nil（模拟默认 fail-closed）
	cfg := mgr.Get()
	cfg.App.AllowInsecureHostKey = nil
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	// 把 mock-1 指向 fake SSH
	cfg = mgr.Get()
	for si := range cfg.Systems {
		for sj := range cfg.Systems[si].Servers {
			if cfg.Systems[si].Servers[sj].Name == "mock-1" {
				cfg.Systems[si].Servers[sj].Host = "127.0.0.1"
				cfg.Systems[si].Servers[sj].Port = port
			}
		}
	}
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}

	w := doRequest(srv, "POST", "/api/ssh/test", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "testpw",
	})
	// fail-closed：应当返回 5xx（默认 502/500 都行），错误信息必须引导用户配置 host key
	if w.Code < 500 {
		t.Errorf("BE-005: 未配 host_key 且默认 fail-closed 应返 5xx，得到 %d body=%s",
			w.Code, w.Body.String())
	}
	body := w.Body.String()
	// 错误信息应清晰提示是 host_key_sha256 / allow_insecure_host_key 问题，
	// 而不是含糊的 SSH 协议错误
	if !strings.Contains(body, "host_key") && !strings.Contains(body, "allow_insecure") {
		t.Errorf("BE-005: 错误信息应引导用户配 host_key_sha256 或 allow_insecure_host_key，得到: %s", body)
	}
}

// TestSSHTest_NoHostKey_AllowInsecure_True 验证向后兼容：
// app.allow_insecure_host_key=true 时，未配 host_key_sha256 也能连（旧内网行为）。
func TestSSHTest_NoHostKey_AllowInsecure_True(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)
	// newTestServer 默认已配 allow_insecure_host_key=true，应该能连通

	w := doRequest(srv, "POST", "/api/ssh/test", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Errorf("显式 allow_insecure_host_key=true 应 200，得到 %d body=%s",
			w.Code, w.Body.String())
	}
}

// TestSSHTest_BadHostKeyFormat 验证 fail-closed：
// 配了 host_key_sha256 但格式非法（不是合法 base64 SHA256）→ 拒绝连接（不再"按 insecure 处理"）。
func TestSSHTest_BadHostKeyFormat(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	srv, mgr, _, _ := newTestServer(t)
	// newTestServer 默认 allow_insecure_host_key=true，但配 host_key_sha256 后
	// 应忽略 allow_insecure_host_key，走强校验路径
	cfg := mgr.Get()
	for si := range cfg.Systems {
		for sj := range cfg.Systems[si].Servers {
			if cfg.Systems[si].Servers[sj].Name == "mock-1" {
				cfg.Systems[si].Servers[sj].Host = "127.0.0.1"
				cfg.Systems[si].Servers[sj].Port = port
				// 故意写一个非法的 host_key_sha256（不是 base64 SHA256）
				cfg.Systems[si].Servers[sj].HostKeySHA256 = "not-a-valid-sha256-fingerprint"
			}
		}
	}
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}

	w := doRequest(srv, "POST", "/api/ssh/test", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "testpw",
	})
	// 配错 host_key_sha256 格式 → fail-closed，拒绝连接
	if w.Code < 500 {
		t.Errorf("配错的 host_key_sha256 应返 5xx，得到 %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(strings.ToLower(body), "hostkeysha256") && !strings.Contains(body, "HostKeySHA256") {
		t.Errorf("错误信息应提到 HostKeySHA256 配错，得到: %s", body)
	}
}
