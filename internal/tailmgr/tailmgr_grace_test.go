package tailmgr

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestGrace_KillAfterSubsLost 验证 v1.0 fix：用户 unsub（前端 SSE 关闭）后超过
// subsLostGrace 立即 killSSH，不必等 idleAfter。
//
// 设计意图：用户切走页面（前端 SSE 关闭 → unsub）但 SSH session 仍连着，旧实现要等
// 30 分钟 idleAfter 才 kill，浪费 SSH 资源 30 分钟。v1.0 加 subsLostGrace（默认 60s），
// 无订阅者超过 grace 就 kill。
//
// 测试方法：
//   1. 启动 tail，streamer 持续产行（模拟"远端持续产日志"）；
//   2. Subscribe → 拿到一行 → unsub（模拟前端 SSE 关闭）；
//   3. 等 grace + 几轮 GC；
//   4. 断言 session.Stopped() == true（killSSH 生效 → SSH ctx cancel → reader 收到 ctx.Done() → markDone）。
//
// 关键验证：unsub 后**即使 SSH 行持续来**，enqueueLine 也不再 touchActivity（v1.0 改动），
// grace 计时才能生效。否则旧的 broadcast 默认 touchActivity，会让 lastActivity 持续更新，
// grace 永远不触发。
func TestGrace_KillAfterSubsLost(t *testing.T) {
	m := NewManagerWithGrace(80 * time.Millisecond) // 短 grace 便于测试
	m.idleAfter = 5 * time.Minute                  // 故意 idleAfter > grace，确保走 grace 路径而非 idleAfter
	m.GCInterval = 15 * time.Millisecond
	defer m.ShutdownAll()

	f := &continuousStreamer{interval: 10 * time.Millisecond} // 10ms 一行（远小于 grace）
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe 拿一行就 unsub（模拟前端关 SSE）
	ch, unsub := s.Subscribe()
	// 排空 ch（防止 ch 满了阻塞 broadcast，default-drop 是 fallback 但更稳是排空）
	go func() {
		for range ch {
		}
	}()
	time.Sleep(30 * time.Millisecond) // 让 streamer 推几行
	unsub()                           // 前端关 SSE

	// 等 grace + GC 跑几轮：80ms grace + 30ms ticker 余量 + 一些 buffer
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if stopped, _ := s.Stopped(); stopped {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	stopped, _ := s.Stopped()
	if !stopped {
		t.Fatalf("unsub 后超过 grace (%v) 应被 kill，但 session 仍存活", m.subsLostGrace)
	}
}

// TestGrace_KeepAliveWhileSubscribed 验证 grace 不影响"有订阅者"的路径：
// 有订阅者时 idleGC 永不 kill（哪怕 grace 已过），这是 BE-002 已有的保护。
func TestGrace_KeepAliveWhileSubscribed(t *testing.T) {
	m := NewManagerWithGrace(50 * time.Millisecond)
	m.idleAfter = 100 * time.Millisecond
	m.GCInterval = 15 * time.Millisecond
	defer m.ShutdownAll()

	f := &fakeStreamer{delayBeforeReturn: 5 * time.Second} // streamer 阻塞不产日志
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatal(err)
	}

	ch, _ := s.Subscribe()
	go func() {
		for range ch {
		}
	}()

	// 等超过 grace 但保持订阅
	time.Sleep(200 * time.Millisecond)

	if stopped, _ := s.Stopped(); stopped {
		t.Fatal("有订阅者时即使超过 grace 也不应被 kill")
	}
}

// TestGrace_ResubscribeResetsActivity 模拟"用户切走又切回"场景：
//   - Subscribe → unsub（grace 计时启动）
//   - 短时间内再 Subscribe（lastActivity 应被重置为 now，grace 重置）
//
// 这个测试间接验证：unsub 不会污染 lastActivity（v1.0 改动），Subscribe 才 touch。
func TestGrace_ResubscribeResetsActivity(t *testing.T) {
	m := NewManagerWithGrace(150 * time.Millisecond)
	m.idleAfter = 5 * time.Minute
	m.GCInterval = 15 * time.Millisecond
	defer m.ShutdownAll()

	f := &continuousStreamer{interval: 30 * time.Millisecond}
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatal(err)
	}

	// 第一次 subscribe + unsub
	ch1, unsub1 := s.Subscribe()
	go func() {
		for range ch1 {
		}
	}()
	time.Sleep(50 * time.Millisecond)
	unsub1()

	// 80ms 后再 subscribe（还没到 grace 150ms）
	time.Sleep(80 * time.Millisecond)
	ch2, unsub2 := s.Subscribe()
	go func() {
		for range ch2 {
		}
	}()
	defer unsub2()

	// 再等 100ms（总耗时 230ms = 50+80+100，超 grace 150ms，但 grace 应被 reset）
	time.Sleep(100 * time.Millisecond)

	if stopped, _ := s.Stopped(); stopped {
		t.Fatal("resubscribe 后 grace 应被 reset，session 不应被 kill")
	}
}

// TestGrace_NoSubsLost_AtStart 验证边界：刚 Start 还没 Subscribe 的 session，
// grace 计时从 CreatedAt 开始 —— 用户如果一直不连接，grace 仍按预期 kill（防泄漏）。
func TestGrace_NoSubsLost_AtStart(t *testing.T) {
	m := NewManagerWithGrace(100 * time.Millisecond)
	m.idleAfter = 5 * time.Minute
	m.GCInterval = 15 * time.Millisecond
	defer m.ShutdownAll()

	f := &fakeStreamer{delayBeforeReturn: 5 * time.Second}
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatal(err)
	}

	// 不 Subscribe，等 grace 触发
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if stopped, _ := s.Stopped(); stopped {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	if stopped, _ := s.Stopped(); !stopped {
		t.Fatal("无订阅者超过 grace 应被 kill（防 SSH session 泄漏）")
	}
}

// 防止 continuousStreamer 的 sync.Once 在多个测试间复用
var _ = sync.Once{}
var _ = context.Background