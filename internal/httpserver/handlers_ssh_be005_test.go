package httpserver

// handlers_ssh_be005_test.go — SSH host key 校验行为测试：
// 默认 fail-closed（BE-005 修复）；显式 true 才放行（向后兼容旧内网）。

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

// TestSSHTest_NoHostKey_DefaultForbidden 验证默认 fail-closed 行为：
// app.allow_insecure_host_key 未配置（nil → false），server 未配 host_key_sha256
// → 拒绝连接，应返 5xx（BE-005 修复后默认行为）。
func TestSSHTest_NoHostKey_DefaultForbidden(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	srv, mgr, _, _ := newTestServer(t)
	// 默认 nil → fail-closed（BE-005 修复后默认行为）
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
	// fail-closed：未配 host_key 默认拒绝，应返 5xx
	if w.Code < 500 {
		t.Errorf("默认 fail-closed 未配 host_key 应 5xx，得到 %d body=%s",
			w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "host_key") && !strings.Contains(body, "allow_insecure") {
		t.Errorf("错误信息应引导用户配 host_key_sha256 或 allow_insecure_host_key，得到: %s", body)
	}
}

// TestSSHTest_NoHostKey_ExplicitForbidden 验证显式 fail-closed：
// app.allow_insecure_host_key=false，server 未配 host_key_sha256
// → /api/ssh/test 应返 5xx 且错误信息包含 host_key 提示。
func TestSSHTest_NoHostKey_ExplicitForbidden(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	srv, mgr, _, _ := newTestServer(t)
	// 显式 false → fail-closed
	cfg := mgr.Get()
	b := false
	cfg.App.AllowInsecureHostKey = &b
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}
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
	if w.Code < 500 {
		t.Errorf("显式 allow_insecure_host_key=false 应返 5xx，得到 %d body=%s",
			w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "host_key") && !strings.Contains(body, "allow_insecure") {
		t.Errorf("错误信息应引导用户配 host_key_sha256 或 allow_insecure_host_key，得到: %s", body)
	}
}

// TestSSHTest_NoHostKey_AllowInsecure_True 验证向后兼容：
// app.allow_insecure_host_key=true 时，未配 host_key_sha256 也能连（旧内网行为）。
// 这是 BE-005 修复特意保留的向后兼容路径。
func TestSSHTest_NoHostKey_AllowInsecure_True(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	srv, mgr, _, _ := newTestServer(t)
	// 显式 true → 允许未配 host_key_sha256 时连接
	cfg := mgr.Get()
	b := true
	cfg.App.AllowInsecureHostKey = &b
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}
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
	// 向后兼容：allow_insecure_host_key=true 应能连通
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
