package httpserver

// handlers_ssh_shell.go — SSH 交互式 shell 的 WebSocket handler。
//
// 协议（与 docs/SSH-TERMINAL-DESIGN.md §4 一致）：
//   - WS upgrade：GET /api/ssh/shell/ws?system=&server=&cols=&rows=
//   - 客户端 → 服务端：
//       binary frame → SSH stdin（按键 / 粘贴 / 命令）
//       text frame   → JSON 控制指令 {"type":"resize|signal|ping", ...}
//   - 服务端 → 客户端：
//       binary frame → SSH stdout/stderr 合并字节流
//       text frame   → JSON {"type":"exit|error|pong", ...}
//
// 安全：
//   - 不接受 query/body 里的 password，凭据由 server 端 resolveCreds 解析；
//   - auth 已在 ServeHTTP 入口校验；
//   - 跨域走现有 allowLocalOrigin；
//   - 审计只写 start/end（不含命令内容）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"kairo/internal/sshclient"
	"golang.org/x/crypto/ssh"
)

// sshShellUpgrader 复用项目的同源/本地跨域策略。
// allowLocalOrigin 已在 ServeHTTP 入口对 /api/* 做了检查，但 gorilla/websocket
// 自己还会再 CheckOrigin 一次，这里保持一致。
var sshShellUpgrader = websocket.Upgrader{
	// 同源 / 本地放行（与 allowLocalOrigin 一致）
	CheckOrigin: func(r *http.Request) bool {
		return allowLocalOrigin(r)
	},
	// 默认 ReadBufferSize / WriteBufferSize = 4096，足够终端字节流。
	// 不设大缓冲是为了避免单 ws 占用太多内存（32 session x 64KB = 2MB 已足够）。
}

// ws 控制消息的 JSON schema（仅 v1 最小集）
type wsControl struct {
	Type   string `json:"type"`             // resize | signal | ping | query_cwd
	Cols   int    `json:"cols,omitempty"`   // resize
	Rows   int    `json:"rows,omitempty"`   // resize
	Signal string `json:"signal,omitempty"` // signal: SIGINT | SIGTERM | SIGKILL
	CwdID  string `json:"cwd_id,omitempty"` // query_cwd: 前端生成的请求 ID
}

// ws 服务端推送的 JSON schema
type wsEvent struct {
	Type    string `json:"type"`              // exit | error | pong | cwd
	Code    int    `json:"code,omitempty"`    // exit
	Reason  string `json:"reason,omitempty"`  // exit / error
	Message string `json:"message,omitempty"` // error
	CwdID   string `json:"cwd_id,omitempty"`  // cwd: 对应请求 ID
	CwdPath string `json:"path,omitempty"`    // cwd: 当前工作目录路径
}

// wsWriteTimeout 写 ws 的超时。终端字节流密集，30s 足够；
// 超过说明客户端 TCP 已断或处理极慢，主动关连接避免 goroutine 泄漏。
const wsWriteTimeout = 30 * time.Second

// wsPingInterval 心跳间隔。30s 一次 ping/pong，让 ws 不被反向代理超时断开。
const wsPingInterval = 30 * time.Second

// handleSSHShellWS 处理 GET /api/ssh/shell/ws
//
// 流程：
//  1. 解析 query (system / server / cols / rows)
//  2. 升级 WS
//  3. Acquire 会话槽（达到上限拒绝）
//  4. resolveCreds（无密码 → 发 error 帧，前端弹输入框）
//  5. sshclient.Dial（3 套 compat profile 自动 fallback）
//  6. cli.Shell() 开 PTY + 交互式 shell
//  7. 启动 3 个 goroutine：
//     - wsReader：ws → SSH stdin（binary）+ 控制帧（text JSON）
//     - sshReader：SSH stdout → ws binary
//     - pinger：定期发 ping
//  8. 等任意一个结束 → cancel ctx → 优雅关闭 → 发 exit 帧
//  9. Release 槽位
func (s *Server) handleSSHShellWS(w http.ResponseWriter, r *http.Request) {
	cur := s.cur()

	// ---- 1. 解析参数 ----
	system := r.URL.Query().Get("system")
	server := r.URL.Query().Get("server")
	if system == "" || server == "" {
		writeErr(w, 400, errors.New("缺少 system / server 参数"))
		return
	}
	_, srvCfg, ok := cur.FindServer(system, server)
	if !ok {
		writeErr(w, 400, errors.New("系统或服务器不存在"))
		return
	}
	rows, cols := parseRowsCols(r)
	// encoding：终端输出编码，默认 utf-8，可选 gbk（老 WebSphere / Oracle 终端）
	encoding := r.URL.Query().Get("encoding")
	if encoding == "" {
		encoding = "utf-8"
	}
	username := srvCfg.Username

	// ---- 2. 升级 WS ----
	// 升级后所有错误走 JSON text frame（前端能拿到具体原因）。
	ws, err := sshShellUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade 失败时 gorilla 已自己写了 HTTP 错误响应，这里只记审计。
		s.audit.Write("ssh.shell.start", "system", system, "server", server,
			"user", username, "result", "fail", "stage", "ws_upgrade", "err", err.Error())
		return
	}
	// 注意：Upgrade 后 ws 必须由本 handler 关闭（defer）。
	// ws.Close() 后 w 已不能再用（gorilla 已 flush 过 101 响应）。

	// 关键：把 ws 设为有限缓冲的 writer，避免慢客户端拖死 server goroutine。
	// SetWriteDeadline 在每次写之前都会调。
	ws.SetReadLimit(1 << 20) // 1MB：单次粘贴上限，超过直接断（防恶意/误粘巨量文本）

	// 发送 JSON 控制帧的 helper（带写超时 + 互斥）
	var wsWriteMu sync.Mutex
	writeJSON := func(ev wsEvent) error {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		return ws.WriteJSON(ev)
	}
	// 发送 binary 帧的 helper
	writeBinary := func(b []byte) error {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		return ws.WriteMessage(websocket.BinaryMessage, b)
	}

	// sendErrorThenClose 发一条 error 帧然后关 ws。
	// 用于连接阶段失败（凭据缺失 / Dial 失败 / Shell 失败）。
	sendErrorThenClose := func(reason, message string) {
		_ = writeJSON(wsEvent{Type: "error", Reason: reason, Message: message})
		_ = ws.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, message))
		_ = ws.Close()
	}

	// ---- 3. Acquire 会话槽 ----
	if err := s.shells.Acquire(); err != nil {
		sendErrorThenClose("max_sessions", err.Error())
		return
	}
	defer s.shells.Release()

	// ---- 4. resolveCreds ----
	// ws 不接受前端传 password；这里只走 keyring / file / 配置默认值。
	// 若都没有，发 error 帧让前端弹一次性输入框（用户输完走 /api/credentials/save 落 keyring，
	// 然后重连 ws）。
	creds, err := s.resolveCreds("", "", system, server, srvCfg.Username, srvCfg.Password)
	if err != nil {
		sendErrorThenClose("cred_resolve", err.Error())
		return
	}
	if creds.Password == "" {
		// 前端收到 no_password 后弹输入框；用户输入后用 POST /api/credentials/save 保存，
		// 然后重连 ws（resolveCreds 这次能拿到密码）。
		sendErrorThenClose("no_password", "需要 SSH 密码，请点左侧主机右侧的「输入密码」按钮")
		return
	}

	// 审计 start（不含密码）
	s.audit.Write("ssh.shell.start", "system", system, "server", server,
		"user", creds.Username, "result", "start")
	startTime := time.Now()

	// ---- 5. Dial ----
	dialCtx, cancelDial := context.WithTimeout(r.Context(), sshDialOuterTimeout)
	cli, err := sshclient.Dial(dialCtx, sshclient.Server{
		Name: srvCfg.Name, Host: srvCfg.Host, Port: srvCfg.Port, Username: creds.Username,
		HostKeySHA256: srvCfg.HostKeySHA256, SSHProfile: srvCfg.SSHProfile,
		AllowInsecureHostKey: cur.App.AllowInsecureHostKeyEnabled(),
	}, sshclient.Credentials{Password: creds.Password}, sshAttemptTimeout)
	cancelDial()
	if err != nil {
		clean := sshclient.SanitizeError(err.Error())
		diag := sshclient.Diagnose(err)
		s.audit.Write("ssh.shell.end", "system", system, "server", server,
			"user", creds.Username, "result", "fail", "stage", "dial",
			"err", clean, "category", string(diag.Category), "reason", diag.Reason,
			"elapsed_sec", time.Since(startTime).Seconds())
		sendErrorThenClose("dial_failed", clean)
		return
	}
	defer cli.Close()

	// ---- 6. Shell ----
	shellCtx, cancelShell := context.WithCancel(context.Background())
	defer cancelShell()
	shell, err := cli.Shell(shellCtx, sshclient.DefaultTERM, rows, cols, nil, encoding)
	if err != nil {
		clean := sshclient.SanitizeError(err.Error())
		s.audit.Write("ssh.shell.end", "system", system, "server", server,
			"user", creds.Username, "result", "fail", "stage", "shell",
			"err", clean, "elapsed_sec", time.Since(startTime).Seconds())
		sendErrorThenClose("shell_failed", clean)
		return
	}
	// 注册 cancel 到 Manager，进程退出时能批量清理
	unregister := s.shells.RegisterCancel(cancelShell)
	defer unregister()
	defer shell.Close()

	// ---- 7. 转发 goroutine ----
	// 任意一个退出都触发 cancelShell → 其他两个也跟着退。
	var wg sync.WaitGroup
	wg.Add(3)

	// cwd 查询状态：前端发 query_cwd 控制帧 → 注入 pwd 命令到 stdin →
	// sshReader 在 stdout 字节流里扫描起止标记 → 提取路径 → 发 cwd 事件。
	// 用 mutex 保护，因为 wsReader 写、sshReader 读。
	//
	// 标记策略（自适应降级）：
	//   - 默认使用 OSC 999 私有序列（ESC ] 999 ; <path> ESC \），xterm.js 忽略未知 OSC，终端无可见输出；
	//   - 老 SSH server/PTY（AIX、WebSphere、某些嵌入式 sshd）可能剥掉 OSC 序列导致超时；
	//   - 单次查询内：OSC 注入后 2.5s 未命中，自动在同一个 query_cwd 请求里注入字面标记重试；
	//   - 会话级：连续 2 次 OSC 最终失败（连字面重试都没拿到），后续查询直接用字面标记，不再等 OSC 超时。
	type cwdQueryState struct {
		mu         sync.Mutex
		active     bool
		id         string
		startMark  string
		endMark    string
		sentAt     time.Time
		retried    bool   // 本次查询是否已用字面标记重试
		useLiteral bool   // 会话级降级：后续直接用字面标记
		failCount  int    // OSC 最终连续失败次数
	}
	cwdState := &cwdQueryState{}

	const cwdOscTimeout = 2500 * time.Millisecond // OSC 单次快速超时，超时后自动降级
	const cwdTotalTimeout = 5 * time.Second       // 整个查询（含重试）总超时
	const cwdFailThreshold = 2                    // 会话级降级阈值

	// cwdMarker 返回指定策略下的起止标记和注入命令
	cwdMarker := func(id string, useLiteral bool) (start, end, cmd string) {
		if useLiteral {
			return "__KAIRO_CWD_S_" + id + "__", "__KAIRO_CWD_E_" + id + "__",
				"\rprintf '__KAIRO_CWD_S_" + id + "__%s__KAIRO_CWD_E_" + id + "__' \"$PWD\"\r"
		}
		return "\x1b]999;", "\x1b\\",
			"\rprintf '\\033]999;%s\\033\\\\' \"$PWD\"\r"
	}

	// isValidCwdPath 校验提取的路径是否为合法绝对路径：
	// 必须以 / 开头，长度合理，不含控制字符 / 百分号 / 引号等格式串残留。
	isValidCwdPath := func(p string) bool {
		if p == "" || p[0] != '/' {
			return false
		}
		if len(p) > 4096 {
			return false
		}
		for i := 0; i < len(p); i++ {
			c := p[i]
			if c < 0x20 || c == 0x7f {
				return false
			}
			if c == '%' || c == '"' || c == '\\' {
				return false
			}
		}
		return true
	}

	// 7a. ws → SSH stdin（binary）+ 控制帧（text JSON）
	go func() {
		defer wg.Done()
		defer cancelShell()
		for {
			msgType, payload, err := ws.ReadMessage()
			if err != nil {
				// ws 读失败：客户端断开 / abnormal close / read limit 触发
				return
			}
			switch msgType {
			case websocket.TextMessage:
				var ctrl wsControl
				if err := json.Unmarshal(payload, &ctrl); err != nil {
					continue // 非法 JSON 忽略，不阻断流
				}
				switch ctrl.Type {
				case "resize":
					if ctrl.Cols > 0 && ctrl.Rows > 0 {
						_ = shell.WindowChange(ctrl.Rows, ctrl.Cols) // 老 sshd 不支持时忽略
					}
				case "signal":
					_ = shell.Signal(parseSSHSignal(ctrl.Signal))
				case "ping":
					_ = writeJSON(wsEvent{Type: "pong"})
				case "query_cwd":
					cwdState.mu.Lock()
					if cwdState.active {
						cwdState.mu.Unlock()
						continue // 已有查询在进行中，忽略重复请求
					}
					qid := ctrl.CwdID
					if qid == "" {
						qid = fmt.Sprintf("%d", time.Now().UnixNano())
					}
					useLit := cwdState.useLiteral // 会话级降级则直接用字面标记
					sm, em, cmd := cwdMarker(qid, useLit)
					cwdState.active = true
					cwdState.id = qid
					cwdState.startMark = sm
					cwdState.endMark = em
					cwdState.sentAt = time.Now()
					cwdState.retried = useLit // 降级模式下视为已经过重试
					cwdState.mu.Unlock()
					_, _ = shell.Stdin.Write([]byte(cmd))
				}
			case websocket.BinaryMessage:
				if len(payload) > 0 {
					if _, err := shell.Stdin.Write(payload); err != nil {
						return // stdin pipe 断（shell 退出）
					}
				}
			}
		}
	}()

	// 7b. SSH stdout → ws binary（带 cwd 标记扫描）
	go func() {
		defer wg.Done()
		defer cancelShell()
		buf := make([]byte, 8*1024) // 8KB 读缓冲，够终端字节流
		var cwdAccum []byte
		for {
			n, err := shell.Stdout.Read(buf)
			if n > 0 {
				data := buf[:n]
				// 检查是否有活跃的 cwd 查询，扫描标记
				cwdState.mu.Lock()
				if cwdState.active {
					elapsed := time.Since(cwdState.sentAt)
					// 阶段 1：OSC 快速超时（未重试过）→ 自动注入字面标记重试
					if !cwdState.retried && elapsed > cwdOscTimeout {
						cwdState.retried = true
						sm2, em2, cmd2 := cwdMarker(cwdState.id, true)
						cwdState.startMark = sm2
						cwdState.endMark = em2
						cwdAccum = nil // 丢弃之前的 OSC 缓冲，重新扫描字面标记
						cwdState.mu.Unlock()
						_, _ = shell.Stdin.Write([]byte(cmd2))
					} else if elapsed > cwdTotalTimeout {
						// 阶段 2：总超时（含字面重试也没拿到）→ 本次查询失败
						cwdState.active = false
						cwdAccum = nil
						if !cwdState.useLiteral {
							cwdState.failCount++
							if cwdState.failCount >= cwdFailThreshold {
								cwdState.useLiteral = true
							}
						}
						cwdState.mu.Unlock()
					} else {
						cwdAccum = append(cwdAccum, data...)
						sm := cwdState.startMark
						em := cwdState.endMark
						cwdDone := false
						for {
							startIdx := strings.Index(string(cwdAccum), sm)
							if startIdx < 0 {
								if len(cwdAccum) > 1024 {
									cwdAccum = nil
								}
								break
							}
							afterStart := cwdAccum[startIdx+len(sm):]
							endIdx := strings.Index(string(afterStart), em)
							if endIdx < 0 {
								if len(cwdAccum) > 8192 {
									cwdAccum = cwdAccum[startIdx+len(sm):]
									if len(cwdAccum) > 4096 {
										cwdAccum = cwdAccum[len(cwdAccum)-4096:]
									}
									continue
								}
								break
							}
							path := strings.TrimSpace(string(afterStart[:endIdx]))
							if isValidCwdPath(path) {
								qid := cwdState.id
								cwdState.active = false
								cwdAccum = nil
								// OSC 直接成功（未触发重试）→ 重置失败计数，保持 OSC 模式
								if !cwdState.retried {
									cwdState.failCount = 0
								}
								// 字面重试成功或会话级降级下成功：不回切 OSC，
								// 因为若 server 剥 OSC，回切只会再次触发 2.5s 延迟 + 重试
								cwdState.mu.Unlock()
								_ = writeJSON(wsEvent{Type: "cwd", CwdID: qid, CwdPath: path})
								cwdDone = true
								break
							}
							skipTo := startIdx + len(sm) + endIdx + len(em)
							if skipTo < len(cwdAccum) {
								cwdAccum = cwdAccum[skipTo:]
							} else {
								cwdAccum = nil
								break
							}
						}
						if !cwdDone {
							cwdState.mu.Unlock()
						}
					}
				} else {
					cwdAccum = nil
					cwdState.mu.Unlock()
				}
				if werr := writeBinary(data); werr != nil {
					return // ws 写失败
				}
			}
			if err != nil {
				return // shell stdout EOF（shell 退出）
			}
		}
	}()

	// 7c. pinger：定期发 ws ping，反向探测 + 保活
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-shellCtx.Done():
				return
			case <-ticker.C:
				wsWriteMu.Lock()
				_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
				werr := ws.WriteMessage(websocket.PingMessage, nil)
				wsWriteMu.Unlock()
				if werr != nil {
					return
				}
			}
		}
	}()

	// ---- 8. 等待退出 ----
	// shell.Wait() 在 shell 自然退出（exit/logout）时返回 exit code。
	// ctx cancel（关 tab / 网络断 / ShutdownAll）会让转发 goroutine 退出，
	// shell.Stdin 被 close 后 shell 也会退出。
	//
	// 注意：ssh.Session.Wait() 只能调一次（内部 channel 消费后不再返回），
	// 所以这里只调 shell.Wait() 一次，把 exit code 通过 channel 传出，不在后面再调 Ssh.Wait()。
	type waitResult struct {
		code int
		ok   bool // true = shell 自然退出（Wait 返回）
	}
	waitCh := make(chan waitResult, 1)
	go func() {
		code, _ := shell.Wait()
		// shellCtx cancel 时不阻塞在此 goroutine
		select {
		case waitCh <- waitResult{code: code, ok: true}:
		case <-shellCtx.Done():
		}
	}()

	exitCode := -1
	select {
	case r := <-waitCh:
		// shell 自然退出 → 拿到真实 exit code
		exitCode = r.code
	case <-shellCtx.Done():
		// 外部 cancel（关 tab / 进程退出）→ exitCode 保持 -1
	}

	// 等转发 goroutine 退出（最多 1s），避免 ws close 时还在写
	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(1 * time.Second):
	}

	// ---- 9. 发 exit 帧 + 关 ws ----
	_ = writeJSON(wsEvent{Type: "exit", Code: exitCode, Reason: "normal"})
	_ = ws.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shell exited"))
	_ = ws.Close()

	// 审计 end
	endResult := "ok"
	if exitCode != 0 {
		endResult = "fail"
	}
	s.audit.Write("ssh.shell.end", "system", system, "server", server,
		"user", creds.Username, "result", endResult,
		"exit_code", exitCode, "elapsed_sec", time.Since(startTime).Seconds())
}

// parseRowsCols 从 query 解析 rows / cols，缺失给默认 24x80。
// 非法值（负数 / 0 / 超大）退回默认，避免 PTY 握手失败。
func parseRowsCols(r *http.Request) (rows, cols int) {
	rows = 24
	cols = 80
	if s := r.URL.Query().Get("rows"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n < 1000 {
			rows = n
		}
	}
	if s := r.URL.Query().Get("cols"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n < 1000 {
			cols = n
		}
	}
	return
}

// parseSSHSignal 把字符串转 ssh.Signal；未知值返回 SIGINT（最常用）。
func parseSSHSignal(s string) ssh.Signal {
	switch s {
	case "SIGINT", "INT":
		return ssh.SIGINT
	case "SIGTERM", "TERM":
		return ssh.SIGTERM
	case "SIGKILL", "KILL":
		return ssh.SIGKILL
	case "SIGHUP", "HUP":
		return ssh.SIGHUP
	case "SIGQUIT", "QUIT":
		return ssh.SIGQUIT
	default:
		return ssh.SIGINT
	}
}
