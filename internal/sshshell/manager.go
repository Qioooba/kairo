// Package sshshell 提供 WebSocket ↔ SSH shell 的双向转发。
//
// 设计要点（v0.10 新增，docs/SSH-TERMINAL-DESIGN.md）：
//   - 一个 WebSocket 连接对应一个 SSH shell session（1:1，无订阅者复用）；
//   - binary frame = 字节流（SSH stdin/stdout 透传）；
//   - text frame = JSON 控制指令（resize / signal / ping）；
//   - Manager 只负责：活跃会话计数 + 上限保护 + ShutdownAll（进程退出清理）。
//
// 与 tailmgr 的区别：
//   - tailmgr 是 1 session → N SSE 订阅者，需要 broadcast；
//   - sshshell 是 1 WS → 1 SSH session，无 broadcast，结构更简单。
//
// 安全：
//   - 不接受前端 ws upgrade 帧里的 password，凭据由 server 端 resolveCreds 解析；
//   - 审计只记 start/end，不记命令内容（输入流密集且含密码可能）；
//   - 复用 sshclient.Dial 的 3 套 compat profile（6.2p2 / AIX 自动 fallback）。
package sshshell

import (
	"context"
	"errors"
	"sync"
	"time"
)

// DefaultMaxSessions 默认最大并发 shell 会话数。
// 单 session 平均内存 ~6MB（xterm 缓冲 5MB + ssh 缓冲 1MB），32 个 ≈ 192MB，
// 在 8GB 内网工具机上完全可接受；超过则拒绝新连接，保护进程不 OOM。
const DefaultMaxSessions = 32

// Manager 跟踪活跃 shell 会话，提供上限保护和 ShutdownAll。
//
// 不存储 session ID（每个 WS 自己 owns 一个 SSH session，生命周期绑死），
// 只维护计数 + 注册的 cancel funcs（用于进程退出时批量清理）。
//
// 注意：context.CancelFunc 是函数类型，不能直接当 map key（Go 规范：函数类型
// 不可比较），这里用自增 int ID 作为 key，cancel func 作为 value。
type Manager struct {
	mu      sync.Mutex
	active  int
	max     int
	nextID  int
	cancels map[int]context.CancelFunc
}

// New 创建 Manager。max <= 0 时走 DefaultMaxSessions。
func New(max int) *Manager {
	if max <= 0 {
		max = DefaultMaxSessions
	}
	return &Manager{
		max:     max,
		cancels: make(map[int]context.CancelFunc),
	}
}

// Acquire 占一个会话槽。达到上限时返回 error，调用方应拒绝 ws upgrade。
// 调用方必须在 ws 关闭时调 Release，否则槽位泄漏。
func (m *Manager) Acquire() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active >= m.max {
		return errors.New("SSH 终端并发会话已达上限，请先关闭部分 tab")
	}
	m.active++
	return nil
}

// Release 释放一个会话槽。重复 Release 是 no-op（防御性）。
func (m *Manager) Release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active > 0 {
		m.active--
	}
}

// ActiveCount 当前活跃会话数（观测 / 健康检查用）。
func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

// MaxSessions 上限（前端展示用）。
func (m *Manager) MaxSessions() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.max
}

// RegisterCancel 注册一个 cancel func，用于 ShutdownAll 时批量调用。
// 返回 unregister 函数，ws 关闭时必须调一次，避免 map 无限增长。
func (m *Manager) RegisterCancel(cancel context.CancelFunc) (unregister func()) {
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.cancels[id] = cancel
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.cancels, id)
		m.mu.Unlock()
	}
}

// ShutdownAll 取消所有活跃会话（进程退出时调用）。
// 调用后所有 WS 会收到 ctx canceled，触发 ssh.Close + ws.Close。
func (m *Manager) ShutdownAll() {
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.cancels))
	for _, c := range m.cancels {
		cancels = append(cancels, c)
	}
	m.cancels = make(map[int]context.CancelFunc)
	m.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	// 给在途会话 2s 优雅退出，避免进程退出时阻塞太久。
	// 不强求等所有 session 退出 —— 进程退出时 OS 会清理 fd。
	deadline := time.Now().Add(2 * time.Second)
	for {
		if time.Now().After(deadline) {
			break
		}
		m.mu.Lock()
		active := m.active
		m.mu.Unlock()
		if active == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}
