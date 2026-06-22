// Package tailmgr 管理"远程文件实时 tail"会话。
//
// 核心模型：
//
//	POST /api/logs/tail/start  → 创建会话（开 SSH，跑 tail -F），返回 tail_id
//	GET  /api/logs/tail/{id}/events  → SSE 流，订阅该 tail 的新行
//	POST /api/logs/tail/{id}/stop    → 显式停止
//
// 设计要点：
//   - 一个 tail 会话有一个 Streamer（生产是 *sshclient.Client，测试可注入 mock）
//   - 一个后台 goroutine 跑 Stream；
//   - 任意时刻可以挂多个 SSE 订阅者，新行通过 chan 广播；
//   - 显式 stop 立即 cancel ctx；session 自然退出也会走完收尾；
//   - 所有 SSH 凭据 / 路径都已在 handler 校验，manager 只负责生命周期。
package tailmgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"ops-toolbox/internal/logquery"
)

// Streamer 是 tailmgr 对底层 SSH 客户端的最小依赖抽象。
//
// 生产环境用 *sshclient.Client（已满足）；测试里可以注入 mock，
// 不需要拉起真 SSH。
type Streamer interface {
	Stream(ctx context.Context, command, encoding string, onLine func(line string)) (exitCode int, err error)
}

// Session 一个 tail 会话
type Session struct {
	ID         string
	ServerName string
	ServerHost string
	Dir        string
	File       string
	Encoding   string
	CreatedAt  time.Time

	// 内嵌状态
	mu          sync.RWMutex
	subscribers map[chan []byte]struct{}
	killSSH     context.CancelFunc
	stopped     bool
	stopErr     error
	stopOnce    sync.Once
}

// Output 表示一条流式输出（按行）
type Output struct {
	Kind string `json:"kind"` // "line" / "error" / "info" / "done"
	Line string `json:"line,omitempty"`
	Msg  string `json:"msg,omitempty"`
}

// Subscribe 注册一个订阅者，返回事件 chan 和取消函数
func (s *Session) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	s.mu.Lock()
	if s.subscribers == nil {
		s.subscribers = make(map[chan []byte]struct{})
	}
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
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

// broadcast 把一行推给所有订阅者；满了就丢旧的（防阻塞 SSH reader）
func (s *Session) broadcast(line []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.subscribers {
		select {
		case ch <- line:
		default:
			// 订阅者处理慢，丢这一行
		}
	}
}

// markDone 关闭所有订阅者，标记 session 已结束
func (s *Session) markDone(err error) {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.stopErr = err
		for ch := range s.subscribers {
			close(ch)
		}
		s.subscribers = nil
		s.mu.Unlock()
	})
}

// Stopped 是否已结束
func (s *Session) Stopped() (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stopped, s.stopErr
}

// Manager 全局 tail 会话池
type Manager struct {
	mu        sync.RWMutex
	sessions  map[string]*Session
	idleAfter time.Duration
	// GCInterval 是 idleGC 的巡检周期。默认 15s；测试里调短。
	// 0 走默认值。
	GCInterval time.Duration
}

// NewManager 创建 Manager
func NewManager() *Manager {
	return &Manager{
		sessions:   make(map[string]*Session),
		idleAfter:  5 * time.Minute,
		GCInterval: 15 * time.Second,
	}
}

// Start 开一个新 tail 会话
//
// cli（生产是 *sshclient.Client，测试可以是 mock Streamer）必须非空；
// dir / file / encoding 由调用方校验（白名单目录、文件名来自 ls）。
func (m *Manager) Start(cli Streamer, serverName, serverHost, dir, file, encoding string, lines int) (*Session, error) {
	if cli == nil {
		return nil, errors.New("ssh 客户端为空")
	}
	cmd, err := logquery.TailCommand(dir, file, lines)
	if err != nil {
		return nil, fmt.Errorf("构造 tail 命令失败: %w", err)
	}

	id := newID()
	sessCtx, cancel := context.WithCancel(context.Background())
	s := &Session{
		ID:          id,
		ServerName:  serverName,
		ServerHost:  serverHost,
		Dir:         dir,
		File:        file,
		Encoding:    encoding,
		CreatedAt:   time.Now(),
		killSSH:     cancel,
		subscribers: make(map[chan []byte]struct{}),
	}

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	// 后台跑 tail，line 一行一行 broadcast
	go func() {
		// 标记开始：推一条 info 给前端
		s.broadcast(formatOutput(Output{Kind: "info", Msg: fmt.Sprintf("开始跟踪 %s:%s", dir, file)}))

		exitCode, streamErr := cli.Stream(sessCtx, cmd, encoding, func(line string) {
			s.broadcast(formatOutput(Output{Kind: "line", Line: line}))
		})
		if streamErr != nil {
			// ctx 取消不报错（正常 stop）
			if !errors.Is(streamErr, context.Canceled) {
				s.broadcast(formatOutput(Output{Kind: "error", Msg: "tail 异常: " + streamErr.Error()}))
			}
		}
		s.broadcast(formatOutput(Output{
			Kind: "done",
			Msg:  fmt.Sprintf("tail 结束 (exit=%d)", exitCode),
		}))
		s.markDone(streamErr)
	}()

	// 空闲清理：定期检查，如果 session 已结束且无订阅者，从 map 移除
	go m.idleGC(s)

	return s, nil
}

// Stop 显式停止一个 tail 会话
func (m *Manager) Stop(id string) error {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return errors.New("tail 会话不存在")
	}
	s.killSSH()
	return nil
}

// Get 拿一个会话（只读信息）
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

// ShutdownAll 关闭所有会话（用于进程退出）
func (m *Manager) ShutdownAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		s.killSSH()
	}
}

// idleGC 定期清理已结束且无订阅者的会话
func (m *Manager) idleGC(s *Session) {
	interval := m.GCInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		<-ticker.C
		s.mu.RLock()
		stopped := s.stopped
		subs := len(s.subscribers)
		s.mu.RUnlock()
		if stopped && subs == 0 {
			m.mu.Lock()
			delete(m.sessions, s.ID)
			m.mu.Unlock()
			return
		}
		if time.Since(s.CreatedAt) > m.idleAfter {
			// 太老的会话（即使还活着）也清掉，防止内存泄漏
			s.killSSH()
		}
	}
}

// formatOutput 把 Output 序列化成 SSE 友好的行（JSON + 换行）
func formatOutput(o Output) []byte {
	// 简单手工 JSON 序列化，避免引号转义麻烦
	switch o.Kind {
	case "line":
		return []byte(`{"kind":"line","line":` + jsonString(o.Line) + `}` + "\n")
	case "error":
		return []byte(`{"kind":"error","msg":` + jsonString(o.Msg) + `}` + "\n")
	case "info":
		return []byte(`{"kind":"info","msg":` + jsonString(o.Msg) + `}` + "\n")
	case "done":
		return []byte(`{"kind":"done","msg":` + jsonString(o.Msg) + `}` + "\n")
	}
	return []byte(`{"kind":"error","msg":"unknown"}` + "\n")
}

func jsonString(s string) string {
	// 最小化 JSON 字符串转义：\" \\ \n \r \t；多字节 rune 走 utf8 编码
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			if r < 0x20 {
				out = append(out, []byte(fmt.Sprintf(`\u%04x`, r))...)
			} else {
				var buf [4]byte
				n := utf8.EncodeRune(buf[:], r)
				out = append(out, buf[:n]...)
			}
		}
	}
	out = append(out, '"')
	return string(out)
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "tail-" + hex.EncodeToString(b[:])
}
