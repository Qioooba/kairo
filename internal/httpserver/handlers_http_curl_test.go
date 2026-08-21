package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ---------- cURL 解析 ----------

func TestParseCurlCommand_Basic(t *testing.T) {
	cmd := "curl -X POST 'https://api.example.com/v1/order' \\\n" +
		"  -H 'Content-Type: application/json' \\\n" +
		"  -H 'X-Trace: abc123' \\\n" +
		"  --data-raw '{\"a\":1}'"
	resp, err := parseCurlCommand(cmd)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resp.Method != "POST" {
		t.Errorf("method 应为 POST，实际 %q", resp.Method)
	}
	if resp.URL != "https://api.example.com/v1/order" {
		t.Errorf("url 解析错误: %q", resp.URL)
	}
	if resp.Headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type 解析错误: %q", resp.Headers["Content-Type"])
	}
	if resp.Headers["X-Trace"] != "abc123" {
		t.Errorf("X-Trace 解析错误: %q", resp.Headers["X-Trace"])
	}
	if resp.BodyMode != "raw" || resp.BodyType != "json" || resp.Body != `{"a":1}` {
		t.Errorf("body 解析错误: mode=%q type=%q body=%q", resp.BodyMode, resp.BodyType, resp.Body)
	}
}

func TestParseCurlCommand_URLEncoded(t *testing.T) {
	cmd := `curl 'https://example.com/login' -d 'user=foo&pass=bar baz'`
	resp, err := parseCurlCommand(cmd)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resp.Method != "GET" {
		t.Errorf("默认 method 应为 GET，实际 %q", resp.Method)
	}
	if resp.BodyMode != "urlencoded" {
		t.Errorf("body_mode 应为 urlencoded，实际 %q", resp.BodyMode)
	}
	if resp.BodyForm["user"] != "foo" || resp.BodyForm["pass"] != "bar baz" {
		t.Errorf("body_form 解析错误: %+v", resp.BodyForm)
	}
}

func TestParseCurlCommand_Flags(t *testing.T) {
	cmd := "curl -k -m 20 --max-redirs 0 -I https://example.com"
	resp, err := parseCurlCommand(cmd)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !resp.InsecureTLS {
		t.Error("insecure_tls 应为 true")
	}
	if resp.TimeoutMs != 20000 {
		t.Errorf("timeout_ms 应为 20000，实际 %d", resp.TimeoutMs)
	}
	if resp.FollowRedirect {
		t.Error("follow_redirect 应为 false（--max-redirs 0）")
	}
	if resp.Method != "HEAD" {
		t.Errorf("method 应为 HEAD，实际 %q", resp.Method)
	}
}

func TestParseCurlCommand_UserAgentAndAuth(t *testing.T) {
	cmd := `curl -u 'user:pw' -A 'my-agent' -b 'sid=1' -e 'http://ref.example.com' https://example.com`
	resp, err := parseCurlCommand(cmd)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resp.Headers["Authorization"] != "Basic dXNlcjpwdw==" {
		t.Errorf("Authorization 解析错误: %q", resp.Headers["Authorization"])
	}
	if resp.Headers["User-Agent"] != "my-agent" {
		t.Errorf("User-Agent 解析错误: %q", resp.Headers["User-Agent"])
	}
	if resp.Headers["Cookie"] != "sid=1" {
		t.Errorf("Cookie 解析错误: %q", resp.Headers["Cookie"])
	}
	if resp.Headers["Referer"] != "http://ref.example.com" {
		t.Errorf("Referer 解析错误: %q", resp.Headers["Referer"])
	}
}

func TestParseCurlCommand_Errors(t *testing.T) {
	if _, err := parseCurlCommand("wget https://example.com"); err == nil {
		t.Error("非 curl 命令应报错")
	}
	if _, err := parseCurlCommand("curl -H 'a: b'"); err == nil {
		t.Error("无 URL 应报错")
	}
}

func TestParseCurlCommand_Form(t *testing.T) {
	cmd := `curl -F 'name=张三' -F 'avatar=@file.png' https://example.com/upload`
	resp, err := parseCurlCommand(cmd)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resp.BodyMode != "formdata" {
		t.Errorf("body_mode 应为 formdata，实际 %q", resp.BodyMode)
	}
	if resp.BodyForm["name"] != "张三" {
		t.Errorf("form 字段解析错误: %+v", resp.BodyForm)
	}
}

// ---------- 断言评估 ----------

func TestEvaluateAssertions(t *testing.T) {
	resp := &httpRequestResp{
		Ok:      true,
		Status:  200,
		Headers: map[string]string{"Content-Type": "application/json", "X-Server": "nginx/1.20"},
		Body:    `{"code":0,"msg":"ok"}`,
	}
	as := []HTTPAssertion{
		{Type: "status", Value: "200"},
		{Type: "status", Value: "2xx"},
		{Type: "body_contains", Value: `"code":0`},
		{Type: "body_not_contains", Value: "error"},
		{Type: "header_contains", Key: "x-server", Value: "nginx"},
	}
	results := evaluateAssertions(as, resp)
	if len(results) != 5 {
		t.Fatalf("结果数应为 5，实际 %d", len(results))
	}
	for i, r := range results {
		if !r.Pass {
			t.Errorf("断言 %d 应通过，实际 %+v", i, r)
		}
	}
}

func TestEvaluateAssertions_Failures(t *testing.T) {
	resp := &httpRequestResp{
		Ok:      true,
		Status:  500,
		Headers: map[string]string{"Content-Type": "text/plain"},
		Body:    "internal error",
	}
	as := []HTTPAssertion{
		{Type: "status", Value: "200"},
		{Type: "body_contains", Value: "ok"},
		{Type: "header_contains", Key: "X-Server", Value: "nginx"},
	}
	results := evaluateAssertions(as, resp)
	for i, r := range results {
		if r.Pass {
			t.Errorf("断言 %d 应失败，实际通过", i)
		}
	}
	if results[0].Actual != "500" {
		t.Errorf("status 断言 actual 应为 500，实际 %q", results[0].Actual)
	}
}

func TestEvaluateAssertions_RequestFailed(t *testing.T) {
	resp := &httpRequestResp{Ok: false, Error: "请求失败: dial tcp"}
	results := evaluateAssertions([]HTTPAssertion{{Type: "status", Value: "200"}}, resp)
	if len(results) != 1 || results[0].Pass {
		t.Errorf("请求失败时断言应失败，实际 %+v", results)
	}
}

// ---------- WebSocket 会话 ----------

// TestWSRoundTrip 用 in-process WS server 验证 connect/send/poll/close 全流程。
func TestWSRoundTrip(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			// echo 回去，方便断言
			_ = conn.WriteMessage(websocket.TextMessage, append([]byte("echo:"), msg...))
		}
	}))
	defer wsSrv.Close()

	// 事件管道测试：手工构造会话（绕过 SSRF，因为 httptest 是 loopback）
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(wsSrv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial 失败: %v", err)
	}
	_ = resp
	sess := &wsClientSession{
		id:        newHTTPID(),
		conn:      conn,
		url:       wsSrv.URL,
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}
	wsSessionsMu.Lock()
	wsSessions[sess.id] = sess
	wsSessionsMu.Unlock()
	defer func() {
		wsSessionsMu.Lock()
		delete(wsSessions, sess.id)
		wsSessionsMu.Unlock()
		_ = conn.Close()
	}()

	go sess.readerLoop()

	// 发一条消息（走会话连接，验证 readerLoop 事件管道）
	if err := sess.conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("发送失败: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var got []wsClientEvent
	for time.Now().Before(deadline) {
		sess.mu.Lock()
		got = append([]wsClientEvent(nil), sess.events...)
		sess.mu.Unlock()
		if len(got) >= 1 && got[len(got)-1].Kind == "recv" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(got) == 0 || got[len(got)-1].Kind != "recv" {
		t.Fatalf("未收到 echo 事件: %+v", got)
	}
	if got[len(got)-1].Text != "echo:hello" {
		t.Errorf("echo 内容错误: %q", got[len(got)-1].Text)
	}
}

// TestWSConnect_SSRFBlocked 验证 auth 启用（远程访问）时 ws://127.0.0.1 被拒。
func TestWSConnect_SSRFBlocked(t *testing.T) {
	srv := newTestServerWithAuth(t, rbacTokens())
	body := strings.NewReader(`{"url":"ws://127.0.0.1:1234/socket"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/http/ws/connect", body)
	rec := httptest.NewRecorder()
	srv.handleHTTPWsConnect(rec, req)
	if rec.Code != 400 {
		t.Errorf("应返回 400，实际 %d（body: %s）", rec.Code, rec.Body.String())
	}
}

// TestWSConnect_InvalidScheme 验证非 ws/wss scheme 被拒。
func TestWSConnect_InvalidScheme(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/http/ws/connect",
		strings.NewReader(`{"url":"http://example.com/socket"}`))
	rec := httptest.NewRecorder()
	srv.handleHTTPWsConnect(rec, req)
	if rec.Code != 400 {
		t.Errorf("应返回 400，实际 %d", rec.Code)
	}
}

// TestWSPoll_UnknownSession 未知会话返回 ok:false + connected:false。
func TestWSPoll_UnknownSession(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/http/ws/poll",
		strings.NewReader(`{"session_id":"nope","after_seq":0}`))
	rec := httptest.NewRecorder()
	srv.handleHTTPWsPoll(rec, req)
	var resp struct {
		OK        bool `json:"ok"`
		Connected bool `json:"connected"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if resp.OK {
		t.Error("未知会话应 ok:false")
	}
}

// ---------- cURL parse handler ----------

func TestHandleHTTPCurlParse(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/http/curl-parse",
		strings.NewReader(`{"curl":"curl -X POST 'https://a.example.com/x' -H 'Content-Type: application/json' --data-raw '{}'"}`))
	rec := httptest.NewRecorder()
	srv.handleHTTPCurlParse(rec, req)
	if rec.Code != 200 {
		t.Fatalf("应返回 200，实际 %d（body: %s）", rec.Code, rec.Body.String())
	}
	var resp httpCurlParseResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if resp.Method != "POST" || resp.URL != "https://a.example.com/x" {
		t.Errorf("解析结果错误: %+v", resp)
	}
}
