package httpserver

// httpssh_test.go — 用 in-process SSH server 测 httpserver 的 SSH 依赖路径

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

type fakeSSH struct {
	listener net.Listener
	signer   ssh.Signer
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// startFakeSSH 启一个只支持 exec 的 SSH server
func startFakeSSH(t *testing.T, user, pass string) (addr string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	signer, _ := ssh.NewSignerFromKey(key)
	s := &fakeSSH{listener: l, signer: signer}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if c.User() == user && string(p) == pass {
				return nil, nil
			}
			return nil, fmt.Errorf("bad auth")
		},
	}
	cfg.AddHostKey(signer)
	s.wg.Add(1)
	go s.serve(cfg)
	t.Cleanup(s.Stop)
	return l.Addr().String()
}

func (s *fakeSSH) serve(cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn, cfg)
	}
}

func (s *fakeSSH) handle(conn net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		_ = conn.Close()
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		s.handleChannel(ch)
	}
}

func (s *fakeSSH) handleChannel(ch ssh.NewChannel) {
	if ch.ChannelType() != "session" {
		_ = ch.Reject(ssh.UnknownChannelType, "unknown")
		return
	}
	ch2, requests, err := ch.Accept()
	if err != nil {
		return
	}
	go func() {
		execDone := make(chan struct{})
		go func() { <-execDone; _ = ch2.Close() }()
		for req := range requests {
			if req.Type == "exec" {
				var p struct{ Command string }
				_ = ssh.Unmarshal(req.Payload, &p)
				_ = req.Reply(true, nil)
				s.exec(ch2, p.Command)
				close(execDone)
				return
			}
			_ = req.Reply(true, nil)
		}
	}()
}

func (s *fakeSSH) exec(ch ssh.Channel, cmd string) {
	sendExit := func(code uint32) {
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
	}
	switch {
	case cmd == "echo ok":
		_, _ = io.WriteString(ch, "ok\n")
		sendExit(0)
	case strings.HasPrefix(cmd, "ls -l "):
		_, _ = io.WriteString(ch, "1024\t1700000000.0\t./SystemOut.log\n2048\t1699900000.0\t./SystemOut.log.20260620\n512\t1699800000.0\t./SystemOut.log.20260619\n")
		sendExit(0)
	case strings.HasPrefix(cmd, "sh -c") && strings.Contains(cmd, "find"):
		_, _ = io.WriteString(ch, "1024\t1700000000.0\t./SystemOut.log\n")
		sendExit(0)
	case strings.HasPrefix(cmd, "tail ") || strings.HasPrefix(cmd, "grep ") || strings.Contains(cmd, "grep"):
		_, _ = io.WriteString(ch, "")
		sendExit(1)
	case strings.HasPrefix(cmd, "sed "):
		_, _ = io.WriteString(ch, "a\nb\nc\n")
		sendExit(0)
	case strings.HasPrefix(cmd, "exit "):
		if n, err := strconv.Atoi(strings.TrimPrefix(cmd, "exit ")); err == nil {
			sendExit(uint32(n))
			return
		}
		sendExit(1)
	default:
		_, _ = io.WriteString(ch, "")
		sendExit(0)
	}
}

func (s *fakeSSH) Stop() {
	s.stopOnce.Do(func() { _ = s.listener.Close() })
	s.wg.Wait()
}

// ---------- 用 fake SSH 的 httpserver 测试 ----------

// newTestServerWithFakeSSH 构造一个 Server，并把配置里的 host 指向 fake SSH。
func newTestServerWithFakeSSH(t *testing.T, port int) *Server {
	srv, mgr, al, _ := newTestServer(t)
	cfg := mgr.Get()
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
	_ = al
	return srv
}

func TestSSHTest_Happy(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	w := doRequest(srv, "POST", "/api/ssh/test", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Errorf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSSHTest_BadPassword(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	w := doRequest(srv, "POST", "/api/ssh/test", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "wrong",
	})
	if w.Code != 502 {
		t.Errorf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
	// 5xx 响应不应含 password 字面量（P2-5）
	if strings.Contains(w.Body.String(), "password") {
		t.Errorf("502 响应泄漏 password 字面量: %s", w.Body.String())
	}
}

func TestLogsList_WithFakeSSH(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	w := doRequest(srv, "POST", "/api/logs/list", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "SystemOut",
		"username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Errorf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = jsonDecode(w.Body.Bytes(), &got)
	if got["files"] == nil {
		t.Errorf("missing files: %v", got)
	}
}

func TestLogsSearch_NoMatch(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	w := doRequest(srv, "POST", "/api/logs/search", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "SystemOut",
		"files": 1, "query": "nonexistent",
		"username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Errorf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestLogsContext_Happy(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	w := doRequest(srv, "POST", "/api/logs/context", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "SystemOut",
		"file": "SystemOut.log", "line": 2,
		"username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Errorf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestLogsSearchMulti_Happy(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "servers": []string{"mock-1"},
		"dir": "SystemOut", "files": 1, "query": "x",
		"username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Errorf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestLogsSearchMulti_AllBadServers(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "servers": []string{"nonexistent"},
		"dir": "SystemOut", "files": 1, "query": "x",
		"username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Errorf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = jsonDecode(w.Body.Bytes(), &got)
	if got["ok_count"].(float64) != 0 {
		t.Errorf("ok_count: %v", got["ok_count"])
	}
}

// jsonDecode 简化版
func jsonDecode(b []byte, v *map[string]any) error {
	return json.Unmarshal(b, v)
}