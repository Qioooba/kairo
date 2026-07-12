// endpointclient 单元测试
//
// 覆盖:
//   1. 主地址成功, 不会打备地址
//   2. 主地址 5xx, 自动切备地址成功
//   3. 主备都挂, 返回合并错误
//   4. Basic Auth header 正确带上
//   5. 没设 primary 直接报错
//   6. Timeout 生效
package endpointclient

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestCall_PrimaryOK 验证主地址成功就直接返回, 不会打到备地址。
func TestCall_PrimaryOK(t *testing.T) {
	var primaryHit, secondaryHit int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryHit, 1)
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondaryHit, 1)
		w.WriteHeader(200)
	}))
	defer secondary.Close()

	cfg := Config{
		Primary:   primary.URL,
		Secondary: secondary.URL,
		Auth:      "dGVzdA==",
		Timeout:   2 * time.Second,
	}
	resp, err := Call(cfg, []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("Call 应该成功: %v", err)
	}
	defer resp.Body.Close()
	if atomic.LoadInt32(&primaryHit) != 1 {
		t.Errorf("primary 应被调用 1 次, got=%d", primaryHit)
	}
	if atomic.LoadInt32(&secondaryHit) != 0 {
		t.Errorf("secondary 不应被调用, got=%d", secondaryHit)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("body 不对: %s", body)
	}
}

// TestCall_PrimaryFail_UseSecondary 验证主地址挂自动切备地址。
func TestCall_PrimaryFail_UseSecondary(t *testing.T) {
	var secondaryHit int32
	// primary 是个不存在端口, 模拟"挂"
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondaryHit, 1)
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true,"from":"secondary"}`))
	}))
	defer secondary.Close()

	cfg := Config{
		Primary:   "http://127.0.0.1:1/will/fail", // 端口 1 没人监听
		Secondary: secondary.URL,
		Timeout:   1 * time.Second,
	}
	resp, err := Call(cfg, []byte(`{}`))
	if err != nil {
		t.Fatalf("Call 应该成功 (切到备地址): %v", err)
	}
	defer resp.Body.Close()
	if atomic.LoadInt32(&secondaryHit) != 1 {
		t.Errorf("secondary 应被调用 1 次, got=%d", secondaryHit)
	}
}

// TestCall_Primary5xx_AutoSwitch 验证主地址返回 5xx 也自动切备。
func TestCall_Primary5xx_AutoSwitch(t *testing.T) {
	var secondaryHit int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondaryHit, 1)
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer secondary.Close()

	cfg := Config{
		Primary:   primary.URL,
		Secondary: secondary.URL,
		Timeout:   1 * time.Second,
	}
	resp, err := Call(cfg, []byte(`{}`))
	if err != nil {
		t.Fatalf("Call 应该成功 (切到备): %v", err)
	}
	defer resp.Body.Close()
	if atomic.LoadInt32(&secondaryHit) != 1 {
		t.Errorf("secondary 应被调用 1 次, got=%d", secondaryHit)
	}
}

// TestCall_BothFail 验证主备都挂返回合并错误。
func TestCall_BothFail(t *testing.T) {
	cfg := Config{
		Primary:   "http://127.0.0.1:1/primary",
		Secondary: "http://127.0.0.1:2/secondary",
		Timeout:   500 * time.Millisecond,
	}
	_, err := Call(cfg, []byte(`{}`))
	if err == nil {
		t.Fatal("应该返回错误 (主备都挂)")
	}
	// 错误信息应该包含两个地址
	msg := err.Error()
	if !strings.Contains(msg, "primary") || !strings.Contains(msg, "secondary") {
		t.Errorf("错误应包含两个地址, got: %s", msg)
	}
}

// TestCall_AuthHeader 验证 Authorization: Basic xxx header 正确带上。
func TestCall_AuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		// 同时验 Content-Type
		ct := r.Header.Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type 应含 application/json, got=%q", ct)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cfg := Config{
		Primary: srv.URL,
		Auth:    "dGVzdDp0ZXN0", // "test:test"
		Timeout: 2 * time.Second,
	}
	resp, err := Call(cfg, []byte(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp.Body.Close()
	if gotAuth != "Basic dGVzdDp0ZXN0" {
		t.Errorf("Authorization header 不对, got=%q want=%q", gotAuth, "Basic dGVzdDp0ZXN0")
	}
}

// TestCall_NoAuthHeader 验证 Auth 为空时不带 Authorization header。
func TestCall_NoAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cfg := Config{
		Primary: srv.URL,
		Auth:    "", // 显式空
		Timeout: 2 * time.Second,
	}
	resp, err := Call(cfg, []byte(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp.Body.Close()
	if gotAuth != "" {
		t.Errorf("Auth 为空时不应带 Authorization header, got=%q", gotAuth)
	}
}

// TestCall_NoPrimary 验证没配 primary 直接报错 (不 panic)。
func TestCall_NoPrimary(t *testing.T) {
	cfg := Config{
		Primary:   "",
		Secondary: "http://127.0.0.1:1/", // 有 secondary 也不算, primary 是必填
		Timeout:   1 * time.Second,
	}
	_, err := Call(cfg, []byte(`{}`))
	if err == nil {
		t.Fatal("没配 primary 应报错")
	}
	if !strings.Contains(err.Error(), "primary") {
		t.Errorf("错误应提到 primary, got: %s", err)
	}
}

// TestCall_Timeout 验证 timeout 真生效 (用慢 server 触发)。
func TestCall_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // 慢响应
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cfg := Config{
		Primary: srv.URL,
		Timeout: 300 * time.Millisecond, // < 2s
	}
	start := time.Now()
	_, err := Call(cfg, []byte(`{}`))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("应该超时")
	}
	if elapsed > 1*time.Second {
		t.Errorf("应该 300ms 内就超时, got=%v", elapsed)
	}
}
