package sshshell

// manager_test.go — sshshell.Manager 的单元测试。
//
// 测试覆盖：
//   - New(0) 走默认上限（DefaultMaxSessions）
//   - New(N) 自定义上限
//   - Acquire / Release / ActiveCount 的基本计数
//   - 达到上限时 Acquire 返回 error
//   - Release 不会把计数减到负数（防御性）
//   - RegisterCancel 返回的 unregister 调用后从 map 移除（不泄漏）
//   - ShutdownAll 调用所有已注册的 cancel，并清空 map
//   - ShutdownAll 等待活跃会话降到 0（带超时）
//
// 不测试 handler 层（那需要 fake SSH + ws dialer，在 httpserver 包里测）。

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNew_DefaultMax(t *testing.T) {
	m := New(0)
	if m.MaxSessions() != DefaultMaxSessions {
		t.Errorf("New(0).MaxSessions()=%d, want %d", m.MaxSessions(), DefaultMaxSessions)
	}
	if m.ActiveCount() != 0 {
		t.Errorf("initial ActiveCount=%d, want 0", m.ActiveCount())
	}
}

func TestNew_CustomMax(t *testing.T) {
	m := New(5)
	if m.MaxSessions() != 5 {
		t.Errorf("MaxSessions=%d, want 5", m.MaxSessions())
	}
}

func TestAcquireRelease_Basic(t *testing.T) {
	m := New(3)
	if err := m.Acquire(); err != nil {
		t.Fatalf("Acquire 1: %v", err)
	}
	if err := m.Acquire(); err != nil {
		t.Fatalf("Acquire 2: %v", err)
	}
	if got := m.ActiveCount(); got != 2 {
		t.Errorf("ActiveCount=%d, want 2", got)
	}
	m.Release()
	if got := m.ActiveCount(); got != 1 {
		t.Errorf("after Release ActiveCount=%d, want 1", got)
	}
	m.Release()
	if got := m.ActiveCount(); got != 0 {
		t.Errorf("after 2nd Release ActiveCount=%d, want 0", got)
	}
}

func TestAcquire_AtMax(t *testing.T) {
	m := New(2)
	_ = m.Acquire()
	_ = m.Acquire()
	if err := m.Acquire(); err == nil {
		t.Errorf("Acquire at max should fail")
	}
	// 释放一个后又能 Acquire
	m.Release()
	if err := m.Acquire(); err != nil {
		t.Errorf("Acquire after Release: %v", err)
	}
}

func TestRelease_Underflow(t *testing.T) {
	m := New(2)
	// 多调 Release 不应把计数减到负数
	m.Release()
	m.Release()
	m.Release()
	if got := m.ActiveCount(); got != 0 {
		t.Errorf("underflow ActiveCount=%d, want 0", got)
	}
	// 仍能正常 Acquire
	if err := m.Acquire(); err != nil {
		t.Errorf("Acquire after underflow: %v", err)
	}
}

func TestRegisterCancel_Unregister(t *testing.T) {
	m := New(5)
	called := int32(0)
	cancel := func() { atomic.AddInt32(&called, 1) }

	unreg := m.RegisterCancel(cancel)

	// 取消应该还在 map 里
	m.mu.Lock()
	n := len(m.cancels)
	m.mu.Unlock()
	if n != 1 {
		t.Errorf("after RegisterCancel, cancels len=%d, want 1", n)
	}

	unreg()

	m.mu.Lock()
	n = len(m.cancels)
	m.mu.Unlock()
	if n != 0 {
		t.Errorf("after unregister, cancels len=%d, want 0", n)
	}

	// ShutdownAll 不应调用已 unregister 的 cancel
	m.ShutdownAll()
	if got := atomic.LoadInt32(&called); got != 0 {
		t.Errorf("unregistered cancel was called %d times", got)
	}
}

func TestRegisterCancel_Multiple(t *testing.T) {
	m := New(5)
	var called int32
	makeCancel := func() context.CancelFunc {
		return func() { atomic.AddInt32(&called, 1) }
	}
	c1 := makeCancel()
	c2 := makeCancel()
	c3 := makeCancel()
	unreg1 := m.RegisterCancel(c1)
	_ = m.RegisterCancel(c2)
	_ = m.RegisterCancel(c3)

	// 注销第 1 个，剩下 2 个
	unreg1()

	m.ShutdownAll()
	if got := atomic.LoadInt32(&called); got != 2 {
		t.Errorf("ShutdownAll called %d cancels, want 2 (c2 + c3)", got)
	}

	// ShutdownAll 后 map 应清空
	m.mu.Lock()
	n := len(m.cancels)
	m.mu.Unlock()
	if n != 0 {
		t.Errorf("after ShutdownAll, cancels len=%d, want 0", n)
	}
}

func TestShutdownAll_WaitsForActiveToDrop(t *testing.T) {
	m := New(5)
	_ = m.Acquire()

	// 模拟一个会话：注册 cancel，等被调用后 Release
	done := make(chan struct{})
	cancel := func() {
		// 模拟 session 收到 cancel 后做点清理工作再 Release
		time.Sleep(20 * time.Millisecond)
		m.Release()
		close(done)
	}
	_ = m.RegisterCancel(cancel)

	start := time.Now()
	m.ShutdownAll()
	elapsed := time.Since(start)

	// ShutdownAll 应该等到 active 降到 0
	if m.ActiveCount() != 0 {
		t.Errorf("after ShutdownAll ActiveCount=%d, want 0", m.ActiveCount())
	}
	// 应该至少等了 cancel 里的 20ms
	if elapsed < 20*time.Millisecond {
		t.Errorf("ShutdownAll elapsed=%v, want >= 20ms", elapsed)
	}

	// done 应该已关闭（cancel 被调过）
	select {
	case <-done:
	default:
		t.Errorf("cancel was not invoked by ShutdownAll")
	}
}

func TestShutdownAll_TimeoutStillReturns(t *testing.T) {
	// 一个不 Release 的"卡死"会话，ShutdownAll 应在 2s 后超时返回，不死锁
	m := New(5)
	_ = m.Acquire()
	cancelCalled := int32(0)
	_ = m.RegisterCancel(func() { atomic.AddInt32(&cancelCalled, 1) })

	start := time.Now()
	m.ShutdownAll()
	elapsed := time.Since(start)

	// 应在 ~2s 后返回（允许 ±500ms 抖动）
	if elapsed < 1500*time.Millisecond {
		t.Errorf("ShutdownAll elapsed=%v, want >= 1.5s (timeout)", elapsed)
	}
	if elapsed > 4*time.Second {
		t.Errorf("ShutdownAll elapsed=%v, want < 4s (should not block too long)", elapsed)
	}
	if atomic.LoadInt32(&cancelCalled) != 1 {
		t.Errorf("cancel not called")
	}
	// active 仍是 1（没人 Release）
	if m.ActiveCount() != 1 {
		t.Errorf("ActiveCount=%d, want 1 (no Release)", m.ActiveCount())
	}
}

// TestShutdownAll_Concurrent 验证 ShutdownAll 在并发 RegisterCancel/unregister 时不会 panic / 死锁。
func TestShutdownAll_Concurrent(t *testing.T) {
	m := New(100)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cancel := func() {}
			unreg := m.RegisterCancel(cancel)
			// 一半注销，一半留下让 ShutdownAll 调
			if time.Now().UnixNano()%2 == 0 {
				unreg()
			}
		}()
	}
	wg.Wait()
	m.ShutdownAll() // 不应 panic
}
