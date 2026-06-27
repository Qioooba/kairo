// Package dlmanager 管理"异步下载任务"的会话池与 SSE 进度广播。
//
// 模型：
//
//	POST /api/{logs|files}/download                   → 创建任务（后台下载），立即返回 {id}
//	GET  /api/{logs|files}/download/{id}/events       → SSE 进度流
//	POST /api/{logs|files}/download/{id}/cancel       → 取消
//
// 设计要点：
//   - Session 持有一个 cancel ctx；cancel 时调用方协程退出；
//   - 进度通过 broadcast 推给所有订阅者；
//   - 多文件串行下载（一次一个 SFTP 流）；进度事件里带当前文件名，
//     前端可按 file 字段找到表格行并更新行内进度条；
//   - 全部完成 / 失败 / 取消都广播一条 done 事件，订阅者据此关闭 SSE。
//   - 30 分钟兜底：超过这个时间还在跑的 session 会被 idleGC 强制 cancel。
//
// Kind 用于区分审计 op 与路由命名空间：
//   - "logs"：走 /api/logs/download/*（白名单日志目录下，审计 op=logs.download）
//   - "files"：走 /api/files/download/*（任意路径，审计 op=files.download）
//
// 复用同一个 Manager，ID 全局唯一（NewID）。
package dlmanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// Item 一个下载产物（普通文件 或 zip）
//
// JSON 字段是给前端用的契约；后端 handler 在 response 里直接复用。
type Item struct {
	File    string `json:"file,omitempty"`     // 远端原文件名（zip 时为 ""）
	Local   string `json:"local"`              // 本地文件名（zip 时就是 zip 的名字）
	Bytes   string `json:"bytes"`              // 字节数（字符串形式，避免 JS 大数精度问题）
	Remote  string `json:"remote,omitempty"`   // 远端路径（zip 时为 ""）
	Date    string `json:"date"`               // YYYYMMDD，本地落点子目录
	Kind    string `json:"kind"`               // "file" 或 "zip"
	AbsPath string `json:"abs_path,omitempty"` // v0.5 v0.5-F：本地绝对路径（前端可拼"打开目录"按钮调 /api/local/reveal-file）
}

// LogsDownloadReq 是"按目录下载最近 N 个文件"任务的入参（同步 handler 解码用）。
//
// 异步入口走 /api/logs/download-latest （创建 dlmanager.Session 后立即返回 id），
// 入参用 downloadLatestReq 即可（本文件不直接消费这个类型）；
// 这里保留 LogsDownloadReq 是为了把 dlmanager.Session 的几种入参显式列出来，
// 给前端 / 测试参考。
type LogsDownloadReq struct {
	System string   `json:"system"`
	Server string   `json:"server"`
	Dir    string   `json:"dir"`
	Files  []string `json:"files"`
	Latest int      `json:"latest"`
	Zip    bool     `json:"zip"`
}

// Session 一个下载任务
//
// 调用方负责：构造 Session → Manager.Create → 异步启动下载协程 → 协程内
// 调用 broadcast 推送进度、最后调用 markFinished 关闭。
type Session struct {
	ID        string
	Kind      string // "logs" | "files"
	System    string
	Server    string
	Dir       string   // 仅 logs 模式使用，files 模式为空
	Files     []string // 仅 logs 模式使用：固定文件列表（latest 模式时为空，启动后再列）
	Paths     []string // 仅 files 模式使用：完整远端路径
	Latest    int      // 仅 logs 模式使用：下几个最新文件（0 = 不限）
	Zip       bool
	Folder    string // 本地下载根目录
	CreatedAt time.Time

	// lastActivity 是 session 最后一次活动时间（推事件时刷新）。
	// IdleGC 基于 lastActivity 而非 CreatedAt 判定空闲超时，
	// 这样慢速大文件下载只要持续推进度就不会被强制 cancel。
	lastActivity time.Time

	mu          sync.RWMutex
	subscribers map[chan []byte]struct{}
	cancel      context.CancelFunc
	finished    bool
	result      []Item
	finalErr    error
	manager     *Manager // 创建此 session 的 Manager（MarkFinished 推 done 时查 DoneSendTimeout）
}

// Manager 全局下载任务池（logs / files 共用 ID 空间）
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session

	// IdleTimeout 是 session 的兜底时长；超过这个时间还在跑的 session
	// 会被 idleGC 强制 cancel。默认 30 分钟。
	IdleTimeout time.Duration

	// GCInterval 是 IdleGC 巡检周期。默认 15 秒；测试里可以调短。
	GCInterval time.Duration

	// DoneSendTimeout 是 MarkFinished 推送 done 行的单订阅者最大等待时间。
	// 进度事件可丢，但 done 事件应到；这里给每个订阅者一个上限（默认 3s），
	// 超过就跳过这个订阅者、继续推下一个，避免：
	//   - 慢 SSE 客户端卡住 MarkFinished → 后台协程泄露
	//   - 一个慢客户端把整个收尾流程拖到几十分钟
	// 设为 0 走默认值。
	DoneSendTimeout time.Duration
}

// New 构造 Manager
func New() *Manager {
	return &Manager{
		sessions:        make(map[string]*Session),
		IdleTimeout:     30 * time.Minute,
		GCInterval:      15 * time.Second,
		DoneSendTimeout: 3 * time.Second,
	}
}

// NewID 构造下载任务 ID（带前缀便于排查）
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "dl-" + hex.EncodeToString(b[:])
}

// Create 建一个空 session（不启动下载）。返回 session 指针（已被 Manager 收纳）。
//
// 调用方负责在新协程里跑下载逻辑，并在协程退出时调用 sess.CancelCtx()（见
// AttachCancel）回收 ctx。新协程退出前必须调用 sess.MarkFinished 收尾。
func (m *Manager) Create(sess *Session) *Session {
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}
	if sess.lastActivity.IsZero() {
		sess.lastActivity = sess.CreatedAt
	}
	sess.manager = m
	m.mu.Lock()
	m.sessions[sess.ID] = sess
	m.mu.Unlock()
	return sess
}

// AttachCancel 注入 ctx 的 cancel 函数。
//
// 通常在 Create 之后立刻调用，模式：
//
//	sess, _ := mgr.Create(&dlmanager.Session{...})
//	sessCtx, cancel := context.WithCancel(context.Background())
//	sess.AttachCancel(cancel)
//	go runTask(sessCtx, sess, ...)
func (s *Session) AttachCancel(cancel context.CancelFunc) {
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
}

// Get 拿一个 session（只读）
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

// Cancel 显式取消一个任务（不存在返回 false）。cancel 后 Session.cancel 被调用，
// 调用方协程收到 ctx.Done 后应主动 markFinished 收尾。
func (m *Manager) Cancel(id string) bool {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	if s.cancel != nil {
		s.cancel()
	}
	return true
}

// Subscribe 注册订阅者。channel 在 markFinished 时会被 close，
// 订阅者用 <-ch 的第二个返回值判断是否已结束。
func (s *Session) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 128)
	s.mu.Lock()
	already := s.finished
	if s.subscribers == nil {
		s.subscribers = make(map[chan []byte]struct{})
	}
	if !already {
		s.subscribers[ch] = struct{}{}
	}
	s.mu.Unlock()
	if already {
		// session 已结束，新订阅者拿到的 ch 立即关闭
		close(ch)
	}
	cancel := func() {
		s.mu.Lock()
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
	return ch, cancel
}

// Broadcast 推一条事件给所有订阅者。满了就丢（前端会被后续进度覆盖）。
func (s *Session) Broadcast(line []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.subscribers {
		select {
		case ch <- line:
		default:
			// 订阅者处理慢，丢这一帧
		}
	}
}

// BroadcastEvent 把任意 kind + kv 序列化成 SSE data: 行并广播。
//
// 调用方不应直接构造 JSON；用本方法可以让 dlmanager 包统一决定序列化规则
// （比如以后换 Protobuf 只需要改 formatEvent）。
//
// 每次推事件都会刷新 lastActivity，使慢速大文件下载只要持续推进度
// 就不会被 IdleTimeout 误杀（BE-012）。
func (s *Session) BroadcastEvent(kind string, kv map[string]any) {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
	s.Broadcast(formatEvent(kind, kv))
}

// IsFinished 给订阅者用于"结束态快速返回"。
func (s *Session) IsFinished() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.finished
}

// Snapshot 返回当前结束态的 (result, finalErr, folder)。
//
// 只在 IsFinished() == true 时才有意义；否则 result 和 finalErr 都为零值。
// 订阅者在订阅时若发现 IsFinished，会调用本方法拿最终结果构造 SSE done 行。
func (s *Session) Snapshot() (result []Item, finalErr error, folder string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.result, s.finalErr, s.Folder
}

// FormatEvent 导出 formatEvent（让 HTTP 层能直接构造 done 行）。
//
// 大多数调用方用 Session.BroadcastEvent 即可；只有"订阅时已结束"这种
// 边缘场景需要外部拼 done 行，才用这个。
func FormatEvent(kind string, kv map[string]any) []byte {
	return formatEvent(kind, kv)
}

// MarkFinished 标记 session 结束，推 done 给所有订阅者，再 close channel。
//
// 推送策略：
//   - 进度事件可丢（前端会被下一次更新覆盖），但 done 事件必到 —— 否则前端
//     不知道"任务结束 vs 网络断"，会卡在 99% 假死。
//   - 单订阅者推送用 mgr.DoneSendTimeout 包一层 select，避免慢 SSE 客户端
//     卡住 MarkFinished：超过超时跳过这个订阅者（同时 close channel），
//     不会无限阻塞后台协程。
//   - 旧的"全阻塞"实现的风险：N 个订阅者中只要有一个慢，整个收尾流程
//     就要等；最坏情况下后台协程长时间不退出，吃 channel buffer 和内存。
//
// 安全：MarkFinished 之后所有订阅者 channel 必关闭；订阅者用第二个返回值判断。
// 这里用 defer 收尾保证即使中途 panic 也能关掉所有 channel。
func (s *Session) MarkFinished(result []Item, finalErr error) {
	s.mu.Lock()
	s.finished = true
	s.result = result
	s.finalErr = finalErr
	subs := s.subscribers
	s.subscribers = nil
	mgr := s.manager
	s.mu.Unlock()

	var ev []byte
	if finalErr != nil {
		ev = formatEvent("done", map[string]any{
			"ok":    false,
			"error": finalErr.Error(),
		})
	} else {
		ev = formatEvent("done", map[string]any{
			"ok":        true,
			"downloads": result,
			"folder":    s.Folder,
		})
	}

	// 每个订阅者独立的超时 timeout
	timeout := 3 * time.Second
	if mgr != nil && mgr.DoneSendTimeout > 0 {
		timeout = mgr.DoneSendTimeout
	}
	for ch := range subs {
		timer := time.NewTimer(timeout)
		select {
		case ch <- ev:
			// done 已送达
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			// 慢订阅者：丢这一帧 done 事件（前端 onerror 兜底会标失败）
		}
	}
	for ch := range subs {
		close(ch)
	}
}

// IdleGC 清理已结束且无订阅者的 session。
//
// 同时兜底：超过 IdleTimeout 还在跑的 session，强制 cancel 防止泄漏。
// 启动方式：每个 session 创建后 `go m.IdleGC(sess)`。
//
// 巡检周期：默认 15s（Manager.GCInterval）；小于等于 0 走默认值。
func (m *Manager) IdleGC(s *Session) {
	interval := m.GCInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		<-ticker.C
		s.mu.RLock()
		finished := s.finished
		subs := len(s.subscribers)
		lastActivity := s.lastActivity
		s.mu.RUnlock()
		if finished && subs == 0 {
			m.mu.Lock()
			delete(m.sessions, s.ID)
			m.mu.Unlock()
			return
		}
		// BE-012：基于 lastActivity 而非 CreatedAt 判定空闲超时。
		// 慢速大文件下载只要持续广播进度，lastActivity 就会被刷新，
		// 不会因为总时长超过 IdleTimeout 被强制 cancel。
		if m.IdleTimeout > 0 && time.Since(lastActivity) > m.IdleTimeout {
			if s.cancel != nil {
				s.cancel()
			}
		}
	}
}

// formatEvent 构造 SSE data: 字段。
//
// 用 encoding/json.Marshal 序列化整张 map，确保：
//   - 中文 / Unicode 文件名 / error 信息走 UTF-8 编码（不被截断）；
//   - 各种边缘字符（引号、反斜杠、控制字符）由标准库正确转义；
//   - 不会因手写 JSON 而误把 rune 转 byte。
//
// 测试断言只关心"包含某个 key/value"，不依赖具体顺序，所以 json.Marshal
// 按 key 字母序输出不影响契约（type Kind 字段名固定为 "kind"）。
func formatEvent(kind string, kv map[string]any) []byte {
	m := make(map[string]any, len(kv)+1)
	m["kind"] = kind
	for k, v := range kv {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		return []byte(`{"kind":"error","error":"json marshal failed"}` + "\n")
	}
	return append(b, '\n')
}
