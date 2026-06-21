package sshclient

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakeSSHServer 起一个本地 SSH server，提供两种能力：
//   - "echo ok" → 立刻返回 "ok\n"，exit=0
//   - "slow"     → 5 秒后返回，模拟慢命令
//   - "gbk 你好" → 直接按 GBK 字节返回（用于测编码）
//   - "exit <N>" → 退出码 N
type fakeSSHServer struct {
	listener net.Listener
	signer   ssh.Signer
	wg       sync.WaitGroup
	stopOnce sync.Once
}

func startFakeSSH(t *testing.T) (*fakeSSHServer, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 1024) // 测试用 1024 加快
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSSHServer{listener: l, signer: signer}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "ops" && string(pass) == "testpw" {
				return nil, nil
			}
			return nil, fmt.Errorf("bad auth for %s", c.User())
		},
	}
	cfg.AddHostKey(signer)
	s.wg.Add(1)
	go s.serve(cfg)
	t.Cleanup(s.Stop)
	return s, l.Addr().String()
}

func (s *fakeSSHServer) serve(cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn, cfg)
	}
}

func (s *fakeSSHServer) handleConn(conn net.Conn, cfg *ssh.ServerConfig) {
	serverConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		_ = conn.Close()
		return
	}
	defer serverConn.Close()
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		go s.handleChannel(newCh)
	}
}

func (s *fakeSSHServer) handleChannel(ch ssh.NewChannel) {
	if ch.ChannelType() != "session" {
		_ = ch.Reject(ssh.UnknownChannelType, "unknown")
		return
	}
	channel, requests, err := ch.Accept()
	if err != nil {
		return
	}
	go func() {
		// 处理 requests 但**不 defer close** — 让 execCommand 决定什么时候关
		// 客户端依赖 server 关 channel 来知道 session 结束
		execDone := make(chan struct{})
		go func() {
			<-execDone
			_ = channel.Close()
		}()
		for req := range requests {
			switch req.Type {
			case "exec":
				var payload struct{ Command string }
				_ = ssh.Unmarshal(req.Payload, &payload)
				_ = req.Reply(true, nil)
				s.execCommand(channel, payload.Command)
				close(execDone)
				// 不再处理后续 request（简单测试 server）
				return
			default:
				_ = req.Reply(true, nil)
			}
		}
	}()
}

func (s *fakeSSHServer) execCommand(ch ssh.Channel, cmd string) {
	// 注：不要用 defer SendRequest(exit-status, 0) — 非 0 退出码会冲突
	sendExit := func(code uint32) {
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
	}
	switch {
	case cmd == "echo ok":
		_, _ = ch.Write([]byte("ok\n"))
		sendExit(0)
	case cmd == "slow":
		time.Sleep(2 * time.Second)
		_, _ = ch.Write([]byte("slow\n"))
		sendExit(0)
	case cmd == "gbk 你好":
		// 直接写 GBK 字节流："你好" = 0xC4 0xE3 0xBA 0xC3
		_, _ = ch.Write([]byte{0xC4, 0xE3, 0xBA, 0xC3})
		sendExit(0)
	case cmd == "exit 1":
		sendExit(1)
		return
	case len(cmd) > 5 && cmd[:5] == "exit ":
		// exit N
		if n, err := strconv.Atoi(cmd[5:]); err == nil {
			sendExit(uint32(n))
			return
		}
		fallthrough
	default:
		_, _ = ch.Write([]byte("unknown: " + cmd + "\n"))
		sendExit(127)
	}
}

func (s *fakeSSHServer) Stop() {
	s.stopOnce.Do(func() {
		_ = s.listener.Close()
	})
	s.wg.Wait()
}

// ---------- 用 fakeSSH 跑的真测试 ----------

func TestDial_Happy(t *testing.T) {
	_, addr := startFakeSSH(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := Dial(ctx, Server{Host: "127.0.0.1", Port: portOf(addr), Username: "ops"},
		Credentials{Password: "testpw"}, 3*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cli.Close()
	if cli.RawConn() == nil {
		t.Error("RawConn nil")
	}
}

func TestDial_BadPassword(t *testing.T) {
	_, addr := startFakeSSH(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Dial(ctx, Server{Host: "127.0.0.1", Port: portOf(addr), Username: "ops"},
		Credentials{Password: "wrong"}, 3*time.Second)
	if err == nil {
		t.Fatal("bad password should fail")
	}
	if !contains(err.Error(), "失败") {
		t.Errorf("err: %v", err)
	}
}

func TestRun_EchoOK(t *testing.T) {
	_, addr := startFakeSSH(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := Dial(ctx, Server{Host: "127.0.0.1", Port: portOf(addr), Username: "ops"},
		Credentials{Password: "testpw"}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	stdout, _, code, err := cli.Run(ctx, "echo ok", 3*time.Second, "utf-8")
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Errorf("code=%d", code)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout=%q", stdout)
	}
}

func TestRun_NonZeroExit(t *testing.T) {
	_, addr := startFakeSSH(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, _ := Dial(ctx, Server{Host: "127.0.0.1", Port: portOf(addr), Username: "ops"},
		Credentials{Password: "testpw"}, 3*time.Second)
	defer cli.Close()
	_, _, code, err := cli.Run(ctx, "exit 7", 3*time.Second, "utf-8")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if code != 7 {
		t.Errorf("code=%d", code)
	}
}

func TestRun_Timeout(t *testing.T) {
	_, addr := startFakeSSH(t)
	ctx := context.Background()
	cli, _ := Dial(ctx, Server{Host: "127.0.0.1", Port: portOf(addr), Username: "ops"},
		Credentials{Password: "testpw"}, 3*time.Second)
	defer cli.Close()
	start := time.Now()
	_, _, code, err := cli.Run(ctx, "slow", 200*time.Millisecond, "utf-8")
	elapsed := time.Since(start)
	if err == nil {
		t.Error("expected timeout error")
	}
	if code != -1 {
		t.Errorf("expected code -1, got %d", code)
	}
	if elapsed > 3*time.Second {
		t.Errorf("killSession didn't return fast: %v", elapsed)
	}
}

func TestRun_ContextCancel(t *testing.T) {
	_, addr := startFakeSSH(t)
	cli, _ := Dial(context.Background(), Server{Host: "127.0.0.1", Port: portOf(addr), Username: "ops"},
		Credentials{Password: "testpw"}, 3*time.Second)
	defer cli.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	_, _, code, err := cli.Run(ctx, "slow", 5*time.Second, "utf-8")
	elapsed := time.Since(start)
	if err == nil {
		t.Error("expected cancel error")
	}
	if code != -1 {
		t.Errorf("expected code -1, got %d", code)
	}
	if elapsed > 3*time.Second {
		t.Errorf("killSession didn't return fast: %v", elapsed)
	}
}

func TestRun_GBK(t *testing.T) {
	_, addr := startFakeSSH(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, _ := Dial(ctx, Server{Host: "127.0.0.1", Port: portOf(addr), Username: "ops"},
		Credentials{Password: "testpw"}, 3*time.Second)
	defer cli.Close()
	stdout, _, code, err := cli.Run(ctx, "gbk 你好", 3*time.Second, "gbk")
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Errorf("code=%d", code)
	}
	if stdout != "你好" {
		t.Errorf("gbk decoded: got %q, want 你好", stdout)
	}
}

func TestStream_Basic(t *testing.T) {
	_, addr := startFakeSSH(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, _ := Dial(ctx, Server{Host: "127.0.0.1", Port: portOf(addr), Username: "ops"},
		Credentials{Password: "testpw"}, 3*time.Second)
	defer cli.Close()
	var got string
	code, err := cli.Stream(ctx, "echo ok", "utf-8", func(line string) {
		got = line
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Errorf("code=%d", code)
	}
	if got != "ok" {
		t.Errorf("got=%q", got)
	}
}

func TestStream_ContextCancel(t *testing.T) {
	_, addr := startFakeSSH(t)
	cli, _ := Dial(context.Background(), Server{Host: "127.0.0.1", Port: portOf(addr), Username: "ops"},
		Credentials{Password: "testpw"}, 3*time.Second)
	defer cli.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := cli.Stream(ctx, "slow", "utf-8", func(string) {})
	if err == nil {
		t.Error("expected cancel error")
	}
}

// ---------- 工具 ----------

func portOf(addr string) int {
	_, ps, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(ps)
	return p
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
