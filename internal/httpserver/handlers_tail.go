package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ops-toolbox/internal/sshclient"
)

// ---------- /api/logs/tail/* ----------

type tailStartReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Dir      string `json:"dir"`
	File     string `json:"file"`
	Lines    int    `json:"lines"` // 启动时先吐的最近 N 行；0 = 只追新增；最大 1000
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleTailStart 创建一个 tail 会话
//
// 注意：返回 200 立即返回，tail 的输出通过 /api/logs/tail/{id}/events 订阅。
func (s *Server) handleTailStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	// BE-020：入口取一次配置快照，后续整个 handler 复用同一份，避免 TOCTOU。
	cur := s.cur()
	var req tailStartReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	_, srv, ok := cur.FindServer(req.System, req.Server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return
	}
	ld, ok := findLogDir(srv, req.Dir)
	if !ok {
		writeErr(w, 400, errors.New("目录不在白名单中"))
		return
	}
	if strings.TrimSpace(req.File) == "" {
		writeErr(w, 400, errors.New("file 不能为空"))
		return
	}
	creds, err := s.resolveCreds(req.Username, req.Password, req.System, req.Server, srv.Username, srv.Password)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if creds.Password == "" {
		writeErr(w, 400, errors.New("缺少密码（输入或勾选「记住密码」）"))
		return
	}
	username := creds.Username

	// 开 SSH（统一超时：外层 45s / 单 profile 10s，给老 sshd + 3 套 profile fallback 留够时间）
	dialCtx, dialCancel := context.WithTimeout(r.Context(), sshDialOuterTimeout)
	defer dialCancel()
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srv.Name, Host: srv.Host, Port: srv.Port, Username: username,
		HostKeySHA256: srv.HostKeySHA256, SSHProfile: srv.SSHProfile,
		AllowInsecureHostKey: cur.App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: creds.Password}, sshAttemptTimeout)
	if err != nil {
		auditErr(w, s.audit, "logs.tail", "system", req.System, "server", req.Server, "dir", ld.Path, "file", req.File, "result", "fail", err)
		writeErrSanitized(w, 502, err)
		return
	}
	// 不要在这里 Close — tail session 接管 client 生命周期
	sess, err := s.tails.Start(cli, srv.Name, srv.Host, ld.Path, req.File, ld.Encoding, req.Lines)
	if err != nil {
		_ = cli.Close()
		writeErrSanitized(w, 500, err)
		return
	}
	s.audit.Write("logs.tail", "system", req.System, "server", req.Server, "dir", ld.Path, "file", req.File, "result", "ok", "id", sess.ID, "lines", req.Lines)
	writeJSON(w, 200, map[string]any{
		"id":     sess.ID,
		"server": srv.Name,
		"dir":    ld.Path,
		"file":   req.File,
	})
}

// handleTailEventsOrStop 根据子路径分发：
//   - GET  /api/logs/tail/{id}/events → SSE 流
//   - POST /api/logs/tail/{id}/stop    → 显式停止
func (s *Server) handleTailEventsOrStop(w http.ResponseWriter, r *http.Request) {
	// path = /api/logs/tail/{id}/events 或 /api/logs/tail/{id}/stop
	rest := strings.TrimPrefix(r.URL.Path, "/api/logs/tail/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	action := parts[1]
	switch action {
	case "events":
		s.streamTailEvents(w, r, id)
	case "stop":
		s.stopTail(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

// streamTailEvents 用 SSE 把 tail 输出推给前端
//
// SSE 格式：data: {json}\n\n
// 这里不专门做 event: 分类，前端把 data 解析为 JSON 看 kind 即可。
func (s *Server) streamTailEvents(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := s.tails.Get(id)
	if !ok {
		writeErr(w, 404, errors.New("tail 会话不存在"))
		return
	}
	// SSE 必备响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // 关 nginx 缓冲（如有反代）

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErrSanitized(w, 500, errors.New("response writer 不支持 flush"))
		return
	}

	ch, unsub := sess.Subscribe()
	defer unsub()

	// 周期性心跳：15s 没新行就推 :keepalive
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, open := <-ch:
			if !open {
				// 会话结束：读取 tailmgr 在 markDone 前设置的 doneMsg，
				// 作为 SSE done 事件的 data（BE-019：避免 pushImmediate 广播 + !open 双 done）。
				// doneMsg 为空 → 发空对象；是 JSON → 直接用；纯文本 → 包装成 {"kind":"done","msg":"..."}。
				doneMsg := sess.DoneMsg()
				var data string
				if doneMsg == "" {
					data = "{}"
				} else if strings.HasPrefix(strings.TrimSpace(doneMsg), "{") {
					data = doneMsg
				} else {
					b, _ := json.Marshal(map[string]string{"kind": "done", "msg": doneMsg})
					data = string(b)
				}
				_, _ = fmt.Fprintf(w, "event: done\ndata: %s\n\n", data)
				flusher.Flush()
				return
			}
			// 批量 NDJSON 可能包含多行（每行一个 JSON 对象）。
			// SSE 协议：事件之间必须用空行分隔；多行 data 会被浏览器合并为一个事件。
			// 我们需要把每个 JSON 对象作为独立事件发送（之间用 \n\n 分隔），
			// 这样前端 onmessage 每次收到一个可直接 JSON.parse 的单独对象。
			for _, l := range strings.Split(string(line), "\n") {
				l = strings.TrimSpace(l)
				if l != "" {
					_, _ = fmt.Fprintf(w, "data: %s\n\n", l)
				}
			}
			flusher.Flush()
		case <-keepalive.C:
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// stopTail 显式停止一个 tail 会话
func (s *Server) stopTail(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	if err := s.tails.Stop(id); err != nil {
		writeErr(w, 404, err)
		return
	}
	s.audit.Write("logs.tail", "id", id, "result", "ok", "stage", "stop")
	writeJSON(w, 200, map[string]any{"ok": true, "id": id})
}
