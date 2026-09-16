package httpserver

// handlers_ssh_shell_test.go — 测 SSH shell WebSocket handler 的端到端流程。
//
// 用 in-process SSH server（支持 PTY + shell）+ gorilla/websocket client dialer，
// 覆盖：
//   - 参数校验（缺 system/server → 400）
//   - 凭据缺失 → error 帧 reason=no_password
//   - 正常连接 → banner + echo + resize + exit 帧
//   - 客户端主动关 → 槽位配平（不泄漏）
//   - 并发上限保护（max sessions）
//
// 不测：host key 强校验（BE-005 专门测试已覆盖）、audit 内容（只验不 panic）。

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"

	"kairo/internal/sshshell"
)

// fakeShellSSH 是一个支持 PTY + 交互式 shell 的 in-process SSH server。
// 与 httpssh_test.go 里的 fakeSSH 不同（那个只支持 exec），这个专门测 shell 路径。
type fakeShellSSH struct {
	listener net.Listener
	signer   ssh.Signer
	wg       sync.WaitGroup
	stopOnce sync.Once
	user     string
	pass     string
	// onShell 在 shell 请求到来时被调用；参数 ch 既是 stdin reader 又是 stdout writer。
	onShell func(ch ssh.Channel, stdin io.Reader)
}

// startFakeShellSSH 启动一个支持 PTY+shell 的 fake SSH server。
// onShell 决定 shell 的行为（echo / banner / exit-on-command 等）。
func startFakeShellSSH(t *testing.T, user, pass string, onShell func(ssh.Channel, io.Reader)) string {
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
	s := &fakeShellSSH{listener: l, signer: signer, user: user, pass: pass, onShell: onShell}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if c.User() == user && string(p) == pass {
				return nil, nil
			}
			return nil, errors.New("bad auth")
		},
	}
	cfg.AddHostKey(signer)
	s.wg.Add(1)
	go s.serve(cfg)
	t.Cleanup(s.Stop)
	return l.Addr().String()
}

func (s *fakeShellSSH) serve(cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn, cfg)
	}
}

func (s *fakeShellSSH) handle(conn net.Conn, cfg *ssh.ServerConfig) {
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

func (s *fakeShellSSH) handleChannel(ch ssh.NewChannel) {
	if ch.ChannelType() != "session" {
		_ = ch.Reject(ssh.UnknownChannelType, "unknown")
		return
	}
	ch2, requests, err := ch.Accept()
	if err != nil {
		return
	}

	// pty-req / shell / window-change / signal / env 都 reply true（除 shell 不可重复）。
	// shell 请求到来时调 onShell，由 onShell 负责 echo / exit 行为。
	go func() {
		defer ch2.Close()
		var shellStarted bool
		for req := range requests {
			switch req.Type {
			case "pty-req", "window-change", "signal", "env":
				_ = req.Reply(true, nil)
			case "shell":
				if shellStarted {
					_ = req.Reply(false, nil)
					continue
				}
				_ = req.Reply(true, nil)
				shellStarted = true
				if s.onShell != nil {
					s.onShell(ch2, ch2) // ch2 既是 reader 又是 writer
				}
				return
			default:
				_ = req.Reply(false, nil)
			}
		}
	}()
}

func (s *fakeShellSSH) Stop() {
	s.stopOnce.Do(func() { _ = s.listener.Close() })
	s.wg.Wait()
}

// ---------- 辅助：构造带 fake shell SSH 的 test server ----------

// newTestServerWithFakeShellSSH 构造 Server，配置指向 fake SSH，并设置 config 密码。
// 设 config 密码是为了让 resolveCreds 在 keyring 没存时能 fallback 到 config 密码，
// 这样 WS handler 能拿到密码 dial fake SSH。
func newTestServerWithFakeShellSSH(t *testing.T, port int, password string) *Server {
	srv, mgr, _, _ := newTestServer(t)
	cfg := mgr.Get()
	for si := range cfg.Systems {
		for sj := range cfg.Systems[si].Servers {
			if cfg.Systems[si].Servers[sj].Name == "mock-1" {
				cfg.Systems[si].Servers[sj].Host = "127.0.0.1"
				cfg.Systems[si].Servers[sj].Port = port
				cfg.Systems[si].Servers[sj].Password = password
			}
		}
	}
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	return srv
}

// wsDial 用 gorilla/websocket client 连到 httptest server 的 shell WS 端点。
func wsDial(t *testing.T, httpSrv *httptest.Server, system, server string, rows, cols int) *websocket.Conn {
	t.Helper()
	u, _ := url.Parse(httpSrv.URL)
	u.Scheme = "ws"
	u.Path = "/api/ssh/shell/ws"
	q := u.Query()
	q.Set("system", system)
	q.Set("server", server)
	if rows > 0 {
		q.Set("rows", strconv.Itoa(rows))
	}
	if cols > 0 {
		q.Set("cols", strconv.Itoa(cols))
	}
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	return conn
}

// readEvent 读一个 text frame 并解析成 wsEvent。跳过 binary frame（shell 输出）。
// 超时或读错返回 false。
func readEvent(t *testing.T, conn *websocket.Conn, timeout time.Duration) (wsEvent, bool) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			return wsEvent{}, false
		}
		if msgType == websocket.TextMessage {
			var ev wsEvent
			if err := json.Unmarshal(payload, &ev); err == nil {
				return ev, true
			}
		}
		// binary frame（shell 输出）→ 继续等 text frame
	}
}

// readBinary 读一个 binary frame，返回 payload。超时返回 false。
// 中途遇到的 text frame（控制帧）会被跳过。
func readBinary(t *testing.T, conn *websocket.Conn, timeout time.Duration) ([]byte, bool) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			return nil, false
		}
		if msgType == websocket.BinaryMessage {
			return payload, true
		}
		// text frame（控制帧）→ 跳过
	}
}

// ---------- 测试用例 ----------

// TestSSHShell_MissingParams 缺 system/server → 400（WS 还没 upgrade）。
func TestSSHShell_MissingParams(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	// 缺 system
	u, _ := url.Parse(httpSrv.URL)
	u.Path = "/api/ssh/shell/ws"
	q := u.Query()
	q.Set("server", "mock-1")
	u.RawQuery = q.Encode()
	resp, err := httpClient.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("missing system: expected 400, got %d", resp.StatusCode)
	}

	// 缺 server
	q.Set("system", "信贷生产")
	q.Del("server")
	u.RawQuery = q.Encode()
	resp2, err := httpClient.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 400 {
		t.Errorf("missing server: expected 400, got %d", resp2.StatusCode)
	}
}

// TestSSHShell_BadSystem 不存在的 system → 400。
func TestSSHShell_BadSystem(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	u, _ := url.Parse(httpSrv.URL)
	u.Path = "/api/ssh/shell/ws"
	q := u.Query()
	q.Set("system", "nonexistent")
	q.Set("server", "mock-1")
	u.RawQuery = q.Encode()
	resp, err := httpClient.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("bad system: expected 400, got %d", resp.StatusCode)
	}
}

// TestSSHShell_NoPassword 没配密码 + keyring 没存 → error 帧 reason=no_password。
func TestSSHShell_NoPassword(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	conn := wsDial(t, httpSrv, "信贷生产", "mock-1", 24, 80)
	defer conn.Close()

	ev, ok := readEvent(t, conn, 3*time.Second)
	if !ok {
		t.Fatal("expected error frame, got nothing")
	}
	if ev.Type != "error" {
		t.Errorf("expected type=error, got %v", ev)
	}
	if ev.Reason != "no_password" {
		t.Errorf("expected reason=no_password, got %q", ev.Reason)
	}
}

// TestSSHShell_HappyPath 完整流程：连接 → 收 banner → 发命令收 echo → resize → ping/pong → exit 帧。
func TestSSHShell_HappyPath(t *testing.T) {
	// fake shell 行为：发 banner，然后逐行读 stdin 并 echo 回去（加 "> " 前缀）；
	// 收到 "exit" 时发 exit-status 0 + close。
	onShell := func(ch ssh.Channel, stdin io.Reader) {
		_, _ = io.WriteString(ch, "welcome to fake shell\r\n")
		buf := make([]byte, 1024)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				line := strings.TrimRight(string(buf[:n]), "\r\n")
				if line == "exit" {
					_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					return
				}
				_, _ = io.WriteString(ch, "> "+line+"\r\n")
			}
			if err != nil {
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				return
			}
		}
	}

	addr := startFakeShellSSH(t, "ops", "testpw", onShell)
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeShellSSH(t, port, "testpw")
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	conn := wsDial(t, httpSrv, "信贷生产", "mock-1", 24, 80)
	defer conn.Close()

	// 1. 收 banner（binary frame）
	banner, ok := readBinary(t, conn, 5*time.Second)
	if !ok {
		t.Fatal("expected banner binary frame, got nothing")
	}
	if !strings.Contains(string(banner), "welcome to fake shell") {
		t.Errorf("banner = %q, want contains 'welcome to fake shell'", string(banner))
	}

	// 2. 发一条命令（binary frame）→ 收 echo
	cmd := []byte("hello\r\n")
	if err := conn.WriteMessage(websocket.BinaryMessage, cmd); err != nil {
		t.Fatalf("write cmd: %v", err)
	}
	echo, ok := readBinary(t, conn, 5*time.Second)
	if !ok {
		t.Fatal("expected echo binary frame, got nothing")
	}
	if !strings.Contains(string(echo), "> hello") {
		t.Errorf("echo = %q, want contains '> hello'", string(echo))
	}

	// 3. 发 resize 控制帧（text frame JSON）→ 不应断连
	resizeMsg, _ := json.Marshal(wsControl{Type: "resize", Cols: 120, Rows: 40})
	if err := conn.WriteMessage(websocket.TextMessage, resizeMsg); err != nil {
		t.Fatalf("write resize: %v", err)
	}

	// 4. 发 ping 控制帧 → 应收 pong
	pingMsg, _ := json.Marshal(wsControl{Type: "ping"})
	if err := conn.WriteMessage(websocket.TextMessage, pingMsg); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	gotPong := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		ev, ok := readEvent(t, conn, remaining)
		if !ok {
			break
		}
		if ev.Type == "pong" {
			gotPong = true
			break
		}
	}
	if !gotPong {
		t.Errorf("expected pong after ping, got none")
	}

	// 5. 发 "exit" → 收 exit 帧
	exitCmd := []byte("exit\r\n")
	if err := conn.WriteMessage(websocket.BinaryMessage, exitCmd); err != nil {
		t.Fatalf("write exit: %v", err)
	}
	ev, ok := readEvent(t, conn, 5*time.Second)
	if !ok {
		t.Fatal("expected exit frame, got nothing")
	}
	if ev.Type != "exit" {
		t.Errorf("expected type=exit, got %v", ev)
	}
}

// TestSSHShell_ClientClose 客户端主动关 ws → server 端槽位 Release（不泄漏）。
func TestSSHShell_ClientClose(t *testing.T) {
	onShell := func(ch ssh.Channel, stdin io.Reader) {
		_, _ = io.WriteString(ch, "ready\r\n")
		buf := make([]byte, 1024)
		for {
			// 阻塞读 stdin，等 client 关 ws 后 stdin 会 EOF
			if _, err := stdin.Read(buf); err != nil {
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				return
			}
		}
	}
	addr := startFakeShellSSH(t, "ops", "testpw", onShell)
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeShellSSH(t, port, "testpw")
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	before := srv.shells.ActiveCount()
	conn := wsDial(t, httpSrv, "信贷生产", "mock-1", 24, 80)

	// 等 banner 确认连接建立
	_, ok := readBinary(t, conn, 5*time.Second)
	if !ok {
		t.Fatal("expected banner")
	}
	// 此时 active 应该 +1
	if got := srv.shells.ActiveCount(); got != before+1 {
		t.Errorf("after connect ActiveCount=%d, want %d", got, before+1)
	}

	// 客户端主动关
	_ = conn.Close()

	// 等 server 端清理（最多 5s，shell.Close + Wait 最多 1s + 转发 goroutine 1s）
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if srv.shells.ActiveCount() == before {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := srv.shells.ActiveCount(); got != before {
		t.Errorf("after client close ActiveCount=%d, want %d (slot leak?)", got, before)
	}
}

// TestSSHShell_MaxSessions 达到上限后新连接被拒（error 帧 reason=max_sessions）。
func TestSSHShell_MaxSessions(t *testing.T) {
	onShell := func(ch ssh.Channel, stdin io.Reader) {
		_, _ = io.WriteString(ch, "hold\r\n")
		buf := make([]byte, 1024)
		// 阻塞读，不主动退出，占住槽位
		for {
			if _, err := stdin.Read(buf); err != nil {
				return
			}
		}
	}
	addr := startFakeShellSSH(t, "ops", "testpw", onShell)
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	srv, mgr, _, _ := newTestServer(t)
	cfg := mgr.Get()
	for si := range cfg.Systems {
		for sj := range cfg.Systems[si].Servers {
			if cfg.Systems[si].Servers[sj].Name == "mock-1" {
				cfg.Systems[si].Servers[sj].Host = "127.0.0.1"
				cfg.Systems[si].Servers[sj].Port = port
				cfg.Systems[si].Servers[sj].Password = "testpw"
			}
		}
	}
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	// 替换为 max=1 的 Manager，第 2 个连接必被拒
	srv.shells = sshshell.New(1)

	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	// 第 1 个连接：成功
	conn1 := wsDial(t, httpSrv, "信贷生产", "mock-1", 24, 80)
	defer conn1.Close()
	_, ok := readBinary(t, conn1, 5*time.Second)
	if !ok {
		t.Fatal("conn1: expected banner")
	}

	// 第 2 个连接：应被拒（max_sessions）
	conn2 := wsDial(t, httpSrv, "信贷生产", "mock-1", 24, 80)
	defer conn2.Close()
	ev, ok := readEvent(t, conn2, 3*time.Second)
	if !ok {
		t.Fatal("conn2: expected error frame")
	}
	if ev.Type != "error" {
		t.Errorf("conn2: expected type=error, got %v", ev)
	}
	if ev.Reason != "max_sessions" {
		t.Errorf("conn2: expected reason=max_sessions, got %q", ev.Reason)
	}
}

// TestSSHShell_GBKStdinEncodesToGBK GBK 模式下前端 UTF-8 输入应转 GBK 再发远端。
// 回归"乱码文件夹进不去"：之前 stdin 未编码，`cd 中文目录` 发 UTF-8 字节，
// GBK 磁盘文件名对不上，报 No such file。
func TestSSHShell_GBKStdinEncodesToGBK(t *testing.T) {
	gotCh := make(chan []byte, 1)
	onShell := func(ch ssh.Channel, stdin io.Reader) {
		_, _ = io.WriteString(ch, "ready\r\n")
		buf := make([]byte, 1024)
		n, err := stdin.Read(buf)
		if n > 0 {
			cp := make([]byte, n)
			copy(cp, buf[:n])
			select {
			case gotCh <- cp:
			default:
			}
			_, _ = ch.Write(cp) // echo 回去，便于 WS 侧确认
		}
		if err != nil {
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			return
		}
		// 保持会话，别立刻退出
		for {
			if _, err := stdin.Read(buf); err != nil {
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				return
			}
		}
	}
	addr := startFakeShellSSH(t, "ops", "testpw", onShell)
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeShellSSH(t, port, "testpw")
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	// 带 encoding=gbk 的 WS dial（wsDial 默认不带 encoding）
	u, _ := url.Parse(httpSrv.URL)
	u.Scheme = "ws"
	u.Path = "/api/ssh/shell/ws"
	q := u.Query()
	q.Set("system", "信贷生产")
	q.Set("server", "mock-1")
	q.Set("rows", "24")
	q.Set("cols", "80")
	q.Set("encoding", "gbk")
	u.RawQuery = q.Encode()
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("ws dial gbk: %v", err)
	}
	defer conn.Close()

	// 等 ready banner
	if _, ok := readBinary(t, conn, 5*time.Second); !ok {
		t.Fatal("expected ready banner")
	}
	// 前端永远发 UTF-8："你好" = E4 BD A0 E5 A5 BD
	utf8hello := []byte("你好")
	if err := conn.WriteMessage(websocket.BinaryMessage, utf8hello); err != nil {
		t.Fatalf("write utf8: %v", err)
	}
	select {
	case got := <-gotCh:
		want := []byte{0xC4, 0xE3, 0xBA, 0xC3}
		if string(got) != string(want) {
			t.Fatalf("GBK stdin: server got %x, want GBK %x (frontend sent UTF-8 %x)", got, want, utf8hello)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server stdin")
	}
}

// ---------- 测试辅助 ----------

// httpClient 是标准 http client，用于非 WS 的 400 测试。
var httpClient = &http.Client{Timeout: 5 * time.Second}
