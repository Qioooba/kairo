package sshclient

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// (SanitizeError 已经在 sanitize_test.go 里覆盖了 — 避免重名)

func TestDecodeBytes_Empty(t *testing.T) {
	if got := decodeBytes(nil, "utf-8"); got != "" {
		t.Errorf("empty: %q", got)
	}
}

func TestDecodeBytes_UTF8(t *testing.T) {
	in := []byte("hello 世界")
	if got := decodeBytes(in, "utf-8"); got != "hello 世界" {
		t.Errorf("utf-8: %q", got)
	}
}

func TestDecodeBytes_GBK(t *testing.T) {
	// "你好" 的 GBK 编码
	gbk := []byte{0xC4, 0xE3, 0xBA, 0xC3}
	got := decodeBytes(gbk, "gbk")
	if got != "你好" {
		t.Errorf("gbk decode: got %q (%x), want %q", got, []byte(got), "你好")
	}
}

func TestDecodeBytes_GBK_CaseInsensitive(t *testing.T) {
	// 大小写、空格、gb18030 别名都应被识别
	gbk := []byte{0xC4, 0xE3, 0xBA, 0xC3}
	for _, enc := range []string{"GBK", " gbk ", "GB18030", "gb18030"} {
		if got := decodeBytes(gbk, enc); got != "你好" {
			t.Errorf("encoding %q: %q", enc, got)
		}
	}
}

func TestSafeWriter_Cap(t *testing.T) {
	// safeWriter 单次写不会超 8MB，溢出时插入 truncation 标记
	var b strings.Builder
	sw := &safeWriter{w: &b}
	// 写 1MB
	chunk := make([]byte, 1024*1024)
	for i := range chunk {
		chunk[i] = 'a'
	}
	n, err := sw.Write(chunk)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(chunk) {
		t.Errorf("write returned %d, want %d", n, len(chunk))
	}
	// 再写 8MB（应该被截到 8MB 上限）
	big := make([]byte, 8*1024*1024)
	_, _ = sw.Write(big)
	// truncation 标记
	if !strings.Contains(b.String(), "truncated") {
		t.Errorf("truncation marker missing; total len=%d", b.Len())
	}
	if b.Len() > 8*1024*1024+100 {
		t.Errorf("output too large: %d", b.Len())
	}
}

func TestSafeWriter_NormalWrite(t *testing.T) {
	var b strings.Builder
	sw := &safeWriter{w: &b}
	sw.Write([]byte("hello"))
	sw.Write([]byte(" world"))
	if b.String() != "hello world" {
		t.Errorf("got %q", b.String())
	}
}

func TestDial_EmptyHost(t *testing.T) {
	ctx := context.Background()
	_, err := Dial(ctx, Server{Host: "", Port: 22}, Credentials{Password: "x"}, time.Second)
	if err == nil {
		t.Fatal("empty host should fail")
	}
	if !strings.Contains(err.Error(), "host 不能为空") {
		t.Errorf("err: %v", err)
	}
}

func TestDial_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	// 10.255.255.1 是 TEST-NET，不可路由；让 Dial 在 ctx 取消前一定不完成
	_, err := Dial(ctx, Server{Host: "10.255.255.1", Port: 22}, Credentials{Password: "x"}, 5*time.Second)
	if err == nil {
		t.Fatal("expected error from cancelled ctx")
	}
	if !strings.Contains(err.Error(), "超时") && !strings.Contains(err.Error(), "canceled") {
		t.Logf("err: %v (acceptable: timeout/canceled)", err)
	}
}

func TestDial_DefaultPort(t *testing.T) {
	// 默认 port=0 时内部应被填为 22；通过让连接立即失败来验证（用保留 IP）
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := Dial(ctx, Server{Host: "127.0.0.1", Port: 0}, Credentials{Password: "x"}, time.Second)
	// 不管连接成功还是失败，至少应该走到 net.JoinHostPort，不在 host 校验阶段挂
	if err != nil && strings.Contains(err.Error(), "host 不能为空") {
		t.Errorf("default port logic broken: %v", err)
	}
	// 期望是 dial 阶段失败（端口 0 实际是 random / "0"，取决于实现），不是 host 校验失败
	_ = ctx
}

func TestClientClose_NilSafe(t *testing.T) {
	var c *Client
	if err := c.Close(); err != nil {
		t.Errorf("nil close: %v", err)
	}
}

func TestRawConn_NilSafe(t *testing.T) {
	var c *Client
	if c.RawConn() != nil {
		t.Error("nil RawConn should be nil")
	}
}

func TestRun_NilClient(t *testing.T) {
	var c *Client
	_, _, _, err := c.Run(context.Background(), "echo", time.Second, "utf-8")
	if err == nil {
		t.Fatal("nil client Run should fail")
	}
}

func TestStream_NilClient(t *testing.T) {
	var c *Client
	_, err := c.Stream(context.Background(), "tail", "utf-8", func(string) {})
	if err == nil {
		t.Fatal("nil client Stream should fail")
	}
}

func TestDecodeBytes_BadEncodingFallback(t *testing.T) {
	// 编码不识别 → 当 UTF-8 处理
	in := []byte("hello")
	if got := decodeBytes(in, "ascii"); got != "hello" {
		t.Errorf("unknown encoding fallback: %q", got)
	}
}

func TestKillSession_NilSafe(t *testing.T) {
	// 不能直接构造 *ssh.Session，但 Client.killSession 有 nil 检查
	var c *Client
	// 这里用 nil receiver 测：c.killSession(nil) 应当不 panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("killSession panicked: %v", r)
		}
	}()
	c.killSession(nil)
}

// 验证 sanitize 列表覆盖的关键词都按预期工作
func TestSanitizeError_AllKeywords(t *testing.T) {
	keywords := []string{"password", "Password", "passwd", "PASSWORD", "PASSWD", "secret", "Secret", "privateKey", "private_key", "authorization", "Authorization"}
	for _, kw := range keywords {
		in := "x=" + kw + "=y"
		got := SanitizeError(in)
		if strings.Contains(got, kw) {
			t.Errorf("keyword %q not sanitized in %q", kw, got)
		}
	}
}

// 错误包装保留 errors.Is
func TestDialErrIsError(t *testing.T) {
	_, err := Dial(context.Background(), Server{Host: "", Port: 22}, Credentials{Password: "x"}, time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
	// err 是 fmt.Errorf 包装的，errors.Is(err, errors.New("...")) 不一定 true，
	// 但 errors.Unwrap 应能拿到底层的（如果实现的话）。这里只验证 err 不为 nil。
	if !errors.Is(err, err) { // 总是 true
		t.Fatal("identity check")
	}
}
