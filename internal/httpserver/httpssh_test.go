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
	case strings.Contains(cmd, "ENVIRON") && strings.Contains(cmd, "awk"):
		// 多行窗口搜索（WindowSearchCommand）：返回两条命中行（100 / 108 行）
		_, _ = io.WriteString(ch, "SystemOut.log:100:Exception at Foo\nSystemOut.log:108:userinfo uid=42\n")
		sendExit(0)
	case strings.Contains(cmd, "is_hit") && strings.Contains(cmd, "awk"):
		// 上下文补全（ContextLinesForHitsCommand）：命中行用 `:`、上下文行用 `-` 分隔
		_, _ = io.WriteString(ch, "SystemOut.log-99:ctx-before-99\nSystemOut.log:100:Exception at Foo\nSystemOut.log-101:ctx-after-101\nSystemOut.log:108:userinfo uid=42\n")
		sendExit(0)
	case strings.Contains(cmd, "sed -n"):
		// 「上下文」按钮走 ContextCommand → `sh -c '... sed -n "START,ENDp" file'`。
		// 按区间生成逼真多行数据，便于验证行号/Hit 标记/内容透传。
		start, end, ok := fakeSedRange(cmd)
		if !ok {
			_, _ = io.WriteString(ch, "")
			sendExit(1)
			return
		}
		var b strings.Builder
		for ln := start; ln <= end; ln++ {
			fmt.Fprintf(&b, "[mock] line-%d 2026-08-18 15:48:%02d:00.000 CST 00000079 SystemOut O ...\n", ln, ln%60)
		}
		_, _ = io.WriteString(ch, b.String())
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

// fakeSedRange 从 ContextCommand 生成的 `sh -c '... sed -n "START,ENDp" file'` 命令里
// 解析出 sed 的行区间，用于生成逼真的上下文数据。
func fakeSedRange(cmd string) (start, end int, ok bool) {
	const prefix = `sed -n "`
	i := strings.Index(cmd, prefix)
	if i < 0 {
		return 0, 0, false
	}
	rest := cmd[i+len(prefix):]
	comma := strings.Index(rest, ",")
	if comma < 0 {
		return 0, 0, false
	}
	endStr := rest[comma+1:]
	p := strings.Index(endStr, "p")
	if p < 0 {
		return 0, 0, false
	}
	start, err1 := strconv.Atoi(rest[:comma])
	end, err2 := strconv.Atoi(endStr[:p])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return start, end, true
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
	if w.Code != 401 {
		t.Errorf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = jsonDecode(w.Body.Bytes(), &got)
	if got["category"] != "auth" {
		t.Errorf("expected category=auth, got %v body=%s", got["category"], w.Body.String())
	}
	// 认证失败响应不应含 password 字面量（P2-5）
	if strings.Contains(w.Body.String(), "password") {
		t.Errorf("401 响应泄漏 password 字面量: %s", w.Body.String())
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

// TestLogsContext_DataFlow 「上下文」按钮的数据流测试：请求 /api/logs/context，
// 验证返回的行数、首行行号、命中行（Hit）标记与内容透传。覆盖常规、仅命中行、
// 行号越界 clamp（line-before<1 时首行回到 1）、以及 before/after 不对称四种场景。
func TestLogsContext_DataFlow(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	cases := []struct {
		name      string
		line      int
		before    int
		after     int
		wantFirst int
		wantCount int
	}{
		{"常规前后3行", 100, 3, 3, 97, 7},
		{"仅命中行", 50, 0, 0, 50, 1},
		{"行号越界clamp到1", 2, 5, 5, 1, 7},
		{"前后不对称", 10, 2, 4, 8, 7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(srv, "POST", "/api/logs/context", map[string]any{
				"system": "信贷生产", "server": "mock-1", "dir": "SystemOut",
				"file": "SystemOut.log", "line": tc.line,
				"before": tc.before, "after": tc.after,
				"username": "ops", "password": "testpw",
			})
			if w.Code != 200 {
				t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
			}
			var got struct {
				Lines []struct {
					LineNo  int    `json:"line_no"`
					Content string `json:"content"`
					Hit     bool   `json:"hit"`
				} `json:"lines"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("解析响应失败: %v body=%s", err, w.Body.String())
			}
			if len(got.Lines) != tc.wantCount {
				t.Fatalf("行数不符: 期望 %d，实际 %d body=%s", tc.wantCount, len(got.Lines), w.Body.String())
			}
			if len(got.Lines) > 0 && got.Lines[0].LineNo != tc.wantFirst {
				t.Fatalf("首行行号不符: 期望 %d，实际 %d body=%s", tc.wantFirst, got.Lines[0].LineNo, w.Body.String())
			}
			hitCount := 0
			for _, l := range got.Lines {
				if l.Content == "" {
					t.Fatalf("内容为空: body=%s", w.Body.String())
				}
				if l.Hit {
					hitCount++
					if l.LineNo != tc.line {
						t.Fatalf("Hit 行号错误: 期望 %d，实际 %d body=%s", tc.line, l.LineNo, w.Body.String())
					}
				}
			}
			if hitCount != 1 {
				t.Fatalf("Hit 标记数量不符: 期望 1，实际 %d body=%s", hitCount, w.Body.String())
			}
		})
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

// v0.15：match_window 字段走 WindowSearchCommand 路径（fake SSH 对 awk 命令
// 返回空输出 → 0 命中，验证整条链路 200 不报错）；超界值被钳制也不应报错。
func TestLogsSearchMulti_MatchWindow(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	for _, win := range []int{10, 999, -3} {
		w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
			"system": "信贷生产", "servers": []string{"mock-1"},
			"dir": "SystemOut", "files": 1, "query": "Exception && userinfo",
			"match_window": win,
			"username":     "ops", "password": "testpw",
		})
		if w.Code != 200 {
			t.Errorf("match_window=%d expected 200, got %d body=%s", win, w.Code, w.Body.String())
		}
	}
}

// v0.15.1：搜索结果只返回命中行，不再内嵌上下文——
// 曾有两个来源导致"命中行上方多出 N 条无效日志"：(1) 窗口匹配强制
// effContextN=max(contextN,matchWindow)；(2) 请求体 context 字段让后端给每个
// 命中行补 N 行上下文。本用例同时携带 match_window 与 context，断言结果仍只有
// 命中行、不含任何 is_context 行（前端显示为带 ┊ 的上下文行）。
func TestLogsSearchMulti_MatchWindow_NoForcedContext(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "servers": []string{"mock-1"},
		"dir": "SystemOut", "files": 1, "query": "Exception",
		"match_window": 8,
		"context":      8,
		"username":     "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		TotalHits int `json:"total_hits"`
		Servers   []struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
			Hits  []struct {
				LineNo    int    `json:"line_no"`
				Content   string `json:"content"`
				IsContext bool   `json:"is_context"`
			} `json:"hits"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析响应失败: %v body=%s", err, w.Body.String())
	}
	if got.TotalHits != 2 {
		t.Fatalf("预期 2 条命中，实际 %d body=%s", got.TotalHits, w.Body.String())
	}
	for _, s := range got.Servers {
		if !s.OK {
			t.Fatalf("服务器搜索失败: %s body=%s", s.Error, w.Body.String())
		}
		for _, h := range s.Hits {
			if h.IsContext {
				t.Fatalf("搜索结果不应返回上下文行（行 %d: %q），body=%s", h.LineNo, h.Content, w.Body.String())
			}
		}
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

// TestLogsSearchMulti_ScopeSelected v0.5-G #8：scope_mode=selected 精确指定文件名
func TestLogsSearchMulti_ScopeSelected(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	// 非法 scope_mode → 400
	if w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "servers": []string{"mock-1"},
		"dir": "SystemOut", "scope_mode": "bogus",
		"query": "x", "username": "ops", "password": "testpw",
	}); w.Code != 400 {
		t.Errorf("非法 scope_mode 应 400，得到 %d body=%s", w.Code, w.Body.String())
	}

	// 合法 selected + 含非法文件名（路径穿越）→ 500 (per-server error)
	w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "servers": []string{"mock-1"},
		"dir":            "SystemOut",
		"scope_mode":     "selected",
		"selected_files": []string{"../etc/passwd"},
		"query":          "x", "username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Errorf("selected 路径穿越应返 200 + per-server error，得到 %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = jsonDecode(w.Body.Bytes(), &got)
	servers, _ := got["servers"].([]any)
	if len(servers) == 1 {
		first := servers[0].(map[string]any)
		if first["ok"].(bool) {
			t.Errorf("selected 含 ../ 应该 ok=false，得到 %v", first)
		}
		if errStr, _ := first["error"].(string); !strings.Contains(errStr, "非法") {
			t.Errorf("error 应提到'非法'，得到 %q", errStr)
		}
	}

	// 合法 selected + 合法文件名 → 200
	w = doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "servers": []string{"mock-1"},
		"dir":            "SystemOut",
		"scope_mode":     "selected",
		"selected_files": []string{"SystemOut.log", "SystemErr.log"},
		"query":          "x", "username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Errorf("合法 selected 应 200，得到 %d body=%s", w.Code, w.Body.String())
	}
}

// TestLogsSearchMulti_ScopeBackwardCompat v0.5-G #8：不传 scope_mode 时按 selected_files/file_patterns 自动判断
func TestLogsSearchMulti_ScopeBackwardCompat(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	// 空 + 有 selected_files → 自动 selected 模式
	w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "servers": []string{"mock-1"},
		"dir": "SystemOut",
		// scope_mode 故意不传
		"selected_files": []string{"a.log"},
		"query":          "x", "username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Errorf("向后兼容（自动 selected）应 200，得到 %d body=%s", w.Code, w.Body.String())
	}

	// 空 + 有 file_patterns → 自动 glob 模式
	w = doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "servers": []string{"mock-1"},
		"dir":           "SystemOut",
		"file_patterns": []string{"*.log"},
		"query":         "x", "username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Errorf("向后兼容（自动 glob）应 200，得到 %d", w.Code)
	}

	// 空 + 空 → 自动 latest 模式（与原行为一致）
	w = doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "servers": []string{"mock-1"},
		"dir":   "SystemOut",
		"files": 1, "query": "x",
		"username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Errorf("向后兼容（自动 latest）应 200，得到 %d", w.Code)
	}
}

// jsonDecode 简化版
func jsonDecode(b []byte, v *map[string]any) error {
	return json.Unmarshal(b, v)
}

// jsonDecodeArr 简化版（针对数组）。
// 单独写一个是因为 Go 不支持把 []map[string]any 直接喂给 *map[string]any；
// 测试里到处是 []map[string]any，拎出来方便。
func jsonDecodeArr(b []byte, v *[]map[string]any) error {
	return json.Unmarshal(b, v)
}

// TestLogsListTargets_Basic v0.5-G：多目标列文件（一次请求多 target 并发）
func TestLogsListTargets_Basic(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)

	// system 缺失 → 400
	if w := doRequest(srv, "POST", "/api/logs/list/targets", map[string]any{
		"targets":  []map[string]string{{"server": "mock-1", "dir": "SystemOut"}},
		"username": "ops", "password": "testpw",
	}); w.Code != 400 {
		t.Errorf("空 system 应 400，得到 %d", w.Code)
	}

	// targets 缺失 → 400
	if w := doRequest(srv, "POST", "/api/logs/list/targets", map[string]any{
		"system":   "信贷生产",
		"username": "ops", "password": "testpw",
	}); w.Code != 400 {
		t.Errorf("空 targets 应 400，得到 %d", w.Code)
	}

	// 合法：1 个 target
	w := doRequest(srv, "POST", "/api/logs/list/targets", map[string]any{
		"system":   "信贷生产",
		"targets":  []map[string]string{{"server": "mock-1", "dir": "SystemOut"}},
		"username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Fatalf("合法 1 target 应 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = jsonDecode(w.Body.Bytes(), &got)
	if got["servers"] == nil {
		t.Errorf("响应缺 servers: %v", got)
	}
	if got["ok_count"].(float64) != 1 {
		t.Errorf("ok_count 期望 1，得到 %v", got["ok_count"])
	}

	// 部分失败：1 个合法 + 1 个不存在 server
	w = doRequest(srv, "POST", "/api/logs/list/targets", map[string]any{
		"system": "信贷生产",
		"targets": []map[string]string{
			{"server": "mock-1", "dir": "SystemOut"},
			{"server": "nonexistent", "dir": "SystemOut"},
		},
		"username": "ops", "password": "testpw",
	})
	if w.Code != 200 {
		t.Fatalf("部分失败应 200，得到 %d", w.Code)
	}
	_ = jsonDecode(w.Body.Bytes(), &got)
	if got["ok_count"].(float64) != 1 {
		t.Errorf("部分失败 ok_count 期望 1，得到 %v", got["ok_count"])
	}
	if got["fail_count"].(float64) != 1 {
		t.Errorf("部分失败 fail_count 期望 1，得到 %v", got["fail_count"])
	}
}
