package httpserver

// ---------- /api/http/ws/* ----------
//
// HTTP 测试页的 WebSocket 测试（v0.15+）。后端作为 WS 客户端连到目标服务，
// 前端通过普通 HTTP 接口驱动会话：
//
//   POST /api/http/ws/connect {url, headers, timeout_ms, insecure_tls} → {session_id}
//   POST /api/http/ws/send    {session_id, message}                     → 发送文本帧
//   POST /api/http/ws/poll    {session_id, after_seq}                   → 拉取增量事件
//   POST /api/http/ws/close   {session_id}                              → 主动关闭
//
// 设计要点：
//   - 用 gorilla/websocket Dialer（项目已有依赖，SSH shell 也在用）
//   - SSRF 防护与 HTTP 测试一致：拒绝 loopback/private/link-local
//   - 会话注册表上限 16 个，事件缓冲上限 2000 条 / 会话；连接后 10 分钟不活跃自动回收
//   - 事件模型：recv（收到帧）/ sent（发出帧）/ error / closed（含原因）

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type wsClientEvent struct {
	Seq  int64  `json:"seq"`
	Kind string `json:"kind"` // recv | sent | error | closed
	Text string `json:"text"`
	At   int64  `json:"at"` // unix 毫秒
}

type wsClientSession struct {
	id        string
	conn      *websocket.Conn
	url       string
	mu        sync.Mutex
	events    []wsClientEvent
	seq       int64
	closed    bool
	createdAt time.Time
	lastUsed  time.Time
	closeOnce sync.Once
}

const (
	wsMaxSessions         = 16
	wsMaxEventsPerSession = 2000
	wsSessionTTL          = 10 * time.Minute
	wsMaxFrameBytes       = 4 * 1024 * 1024 // 单帧 4MB 上限（读）
	wsDefaultTimeoutMs    = 10_000
	wsMaxTimeoutMs        = 60_000
)

var (
	wsSessionsMu sync.Mutex
	wsSessions   = map[string]*wsClientSession{}
)

func (s *wsClientSession) push(kind, text string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.events = append(s.events, wsClientEvent{Seq: s.seq, Kind: kind, Text: text, At: time.Now().UnixMilli()})
	if len(s.events) > wsMaxEventsPerSession {
		s.events = s.events[len(s.events)-wsMaxEventsPerSession:]
	}
	s.lastUsed = time.Now()
	return s.seq
}

// close 幂等关闭：推送 closed 事件并关底层连接（会话保留在注册表里，
// 等前端 poll 到 connected=false 后由 poll 负责摘除）。
func (s *wsClientSession) close(reason string) {
	s.closeOnce.Do(func() {
		_ = s.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason))
		_ = s.conn.Close()
		s.push("closed", reason)
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
	})
}

func (s *wsClientSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// sweepWSSessions 清理过期 / 已关闭且被消费完的会话。
// 注意锁序：wsSessionsMu 与 sess.mu 不能同时持有（poll 里是先 sess.mu 后 wsSessionsMu）。
func sweepWSSessions() {
	now := time.Now()
	var toClose []*wsClientSession
	wsSessionsMu.Lock()
	for id, sess := range wsSessions {
		if now.Sub(sess.createdAt) > wsSessionTTL {
			toClose = append(toClose, sess)
			delete(wsSessions, id)
			continue
		}
		if sess.isClosed() && now.Sub(sess.lastUsed) > 30*time.Second {
			delete(wsSessions, id)
		}
	}
	wsSessionsMu.Unlock()
	for _, sess := range toClose {
		sess.close("会话超时，已回收")
	}
}

func wsSessionByID(id string) *wsClientSession {
	wsSessionsMu.Lock()
	defer wsSessionsMu.Unlock()
	return wsSessions[id]
}

// wsSafeDial 是 gorilla Dialer 的 NetDialContext：对每次实际拨号做 SSRF 校验。
// ssrfGuard=false（本机无 auth 场景）时跳过校验，直接拨号，允许连内网/本机地址。
func wsSafeDial(ctx context.Context, network, address string, ssrfGuard bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if !ssrfGuard {
		dialer := &net.Dialer{}
		return dialer.DialContext(ctx, network, address)
	}
	if err := rejectPrivateHost(ctx, host); err != nil {
		return nil, err
	}
	dialer := &net.Dialer{}
	return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
}

func (s *Server) handleHTTPWsConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req struct {
		URL         string            `json:"url"`
		Headers     map[string]string `json:"headers"`
		TimeoutMs   int               `json:"timeout_ms"`
		InsecureTLS bool              `json:"insecure_tls"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("JSON 解析失败: "+err.Error()))
		return
	}
	target := strings.TrimSpace(req.URL)
	if target == "" {
		writeErr(w, 400, errors.New("url 不能为空"))
		return
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "ws" && u.Scheme != "wss" {
		writeErr(w, 400, errors.New("url 必须以 ws:// 或 wss:// 开头"))
		return
	}
	if u.Hostname() == "" {
		writeErr(w, 400, errors.New("url host 不能为空"))
		return
	}
	ssrfGuard := s.ssrfGuard()
	// SSRF：仅 auth 启用（远程访问）时拦截内网或本机地址；
	// 本机无 auth 场景放行，方便调试本机/内网 WebSocket 服务。
	if ssrfGuard {
		if err := rejectPrivateHost(r.Context(), u.Hostname()); err != nil {
			writeErr(w, 400, errors.New("拒绝连接内网或本机地址: "+u.Hostname()))
			return
		}
	}

	timeoutMs := req.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = wsDefaultTimeoutMs
	}
	if timeoutMs > wsMaxTimeoutMs {
		timeoutMs = wsMaxTimeoutMs
	}

	header := http.Header{}
	for k, v := range req.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		header.Set(k, v)
	}
	if header.Get("User-Agent") == "" {
		header.Set("User-Agent", "kairo-ws-test/0.15")
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: time.Duration(timeoutMs) * time.Millisecond,
		NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return wsSafeDial(ctx, network, address, ssrfGuard)
		},
	}
	if req.InsecureTLS || u.Scheme == "ws" {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	conn, _, err := dialer.Dial(target, header)
	if err != nil {
		writeErr(w, 502, fmt.Errorf("WebSocket 连接失败: %v", err))
		return
	}
	conn.SetReadLimit(wsMaxFrameBytes)

	sweepWSSessions()

	sess := &wsClientSession{
		id:        newHTTPID(),
		conn:      conn,
		url:       target,
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}

	wsSessionsMu.Lock()
	if len(wsSessions) >= wsMaxSessions {
		// 挤掉最早创建的会话
		var oldestID string
		var oldest *wsClientSession
		for id, ex := range wsSessions {
			if oldest == nil || ex.createdAt.Before(oldest.createdAt) {
				oldest, oldestID = ex, id
			}
		}
		if oldest != nil {
			oldest.close("会话数超上限，连接被挤下线")
			delete(wsSessions, oldestID)
		}
	}
	wsSessions[sess.id] = sess
	wsSessionsMu.Unlock()

	go sess.readerLoop()
	s.audit.Write("http.ws.connect", "url", target)
	writeJSON(w, 200, map[string]any{"ok": true, "session_id": sess.id})
}

func (sess *wsClientSession) readerLoop() {
	for {
		msgType, data, err := sess.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
				sess.close("服务端关闭了连接")
			} else {
				sess.close("连接断开: " + err.Error())
			}
			return
		}
		var text string
		if msgType == websocket.BinaryMessage {
			text = fmt.Sprintf("[二进制消息 %d 字节]", len(data))
		} else {
			text = string(data)
		}
		sess.push("recv", text)
	}
}

func (s *Server) handleHTTPWsSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("JSON 解析失败: "+err.Error()))
		return
	}
	sess := wsSessionByID(req.SessionID)
	if sess == nil || sess.isClosed() {
		writeErr(w, 404, errors.New("会话不存在或已关闭"))
		return
	}
	if err := sess.conn.WriteMessage(websocket.TextMessage, []byte(req.Message)); err != nil {
		sess.close("发送失败: " + err.Error())
		writeErr(w, 500, fmt.Errorf("发送失败: %v", err))
		return
	}
	sess.push("sent", req.Message)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleHTTPWsPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		AfterSeq  int64  `json:"after_seq"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("JSON 解析失败: "+err.Error()))
		return
	}
	sess := wsSessionByID(req.SessionID)
	if sess == nil {
		writeJSON(w, 200, map[string]any{"ok": false, "connected": false, "error": "会话不存在或已关闭", "events": []wsClientEvent{}})
		return
	}
	sess.mu.Lock()
	events := make([]wsClientEvent, 0, 8)
	for _, ev := range sess.events {
		if ev.Seq > req.AfterSeq {
			events = append(events, ev)
		}
	}
	// 截掉已消费的事件，防止会话事件无限增长
	keep := sess.events[:0]
	for _, ev := range sess.events {
		if ev.Seq > req.AfterSeq {
			keep = append(keep, ev)
		}
	}
	sess.events = keep
	closed := sess.closed
	sess.mu.Unlock()

	if closed {
		wsSessionsMu.Lock()
		delete(wsSessions, req.SessionID)
		wsSessionsMu.Unlock()
	}
	writeJSON(w, 200, map[string]any{"ok": true, "connected": !closed, "events": events})
}

func (s *Server) handleHTTPWsClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("JSON 解析失败: "+err.Error()))
		return
	}
	sess := wsSessionByID(req.SessionID)
	if sess != nil {
		sess.close("客户端关闭连接")
		wsSessionsMu.Lock()
		delete(wsSessions, req.SessionID)
		wsSessionsMu.Unlock()
	}
	s.audit.Write("http.ws.close", "session", req.SessionID)
	writeJSON(w, 200, map[string]any{"ok": true})
}
