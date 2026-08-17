package webservice

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSafeDialContext_MultiIPFallback 实证验证：localhost 解析出 ::1 + 127.0.0.1，
// 服务只监听 IPv4。旧实现只拨 ips[0]（可能是 ::1）会失败；新实现 IPv4 优先 + 逐个尝试必须成功。
func TestSafeDialContext_MultiIPFallback(t *testing.T) {
	ips, err := net.LookupIP("localhost")
	if err != nil || len(ips) < 2 {
		t.Skipf("本机 localhost 只解析出一个 IP (%v)，跳过", ips)
	}
	t.Logf("localhost 解析出 %v 个 IP: %v", len(ips), ips)

	// 只监听 IPv4 loopback
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp4: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	conn, err := SafeDialContext(context.Background(), "tcp", net.JoinHostPort("localhost", itoa(port)))
	if err != nil {
		t.Fatalf("SafeDialContext 拨 localhost 失败（IPv6 优先时旧实现会挂在这里）: %v", err)
	}
	_ = conn.Close()
	ln.Close()
	<-srvDone
}

// TestSend_MultiIPFallback 全链路验证：Send 到 localhost（多 IP 解析）也能成功。
func TestSend_MultiIPFallback(t *testing.T) {
	ips, _ := net.LookupIP("localhost")
	if len(ips) < 2 {
		t.Skip("本机 localhost 单 IP，跳过")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><ok/>`))
	}))
	defer srv.Close()
	// httptest 默认监听 127.0.0.1，把 URL 的主机名换成 localhost（解析出多 IP）
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	endpoint := "http://localhost:" + port
	resp := Send(SendRequest{Endpoint: endpoint, Body: "<x/>", TimeoutMs: 5000})
	if !resp.OK {
		t.Fatalf("Send 到 %s 失败: %s", endpoint, resp.Error)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
