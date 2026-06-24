package tailmgr

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestEnqueueLine_BatchesUnderThreshold 验证：enqueue 不到 100 行时不会立刻
// 发 flushSignal，由 ticker 兜底 flush。这保证 50ms 内的少量行能合并成一次 channel send。
func TestEnqueueLine_BatchesUnderThreshold(t *testing.T) {
	s := newTestSessionForBatcher()
	const N = 5
	for i := 0; i < N; i++ {
		s.enqueueLine("line-" + string(rune('a'+i)))
	}
	if got := s.pendingN; got != N {
		t.Fatalf("pendingN=%d, want %d", got, N)
	}
	if got := strings.Count(string(s.pendingBuf), `"kind":"line"`); got != N {
		t.Fatalf("pendingBuf 里 line NDJSON 个数=%d, want %d", got, N)
	}
}

// TestEnqueueLine_SignalsAtThreshold 验证：满 100 行时立刻给 flushSignal
// 推一个信号（不阻塞），让 batcher 提前 flush。
func TestEnqueueLine_SignalsAtThreshold(t *testing.T) {
	s := newTestSessionForBatcher()
	for i := 0; i < 100; i++ {
		s.enqueueLine("x")
	}
	select {
	case <-s.flushSignal:
		// 期望：信号已到
	default:
		t.Fatal("满 100 行应该立即 signal flushSignal,实际没有")
	}
}

// TestFlushPending_BroadcastsAndClears 验证：flushPending 把 pendingBuf
// 整体推给订阅者，并清空 buffer；多次 flush 之间不串。
func TestFlushPending_BroadcastsAndClears(t *testing.T) {
	s := newTestSessionForBatcher()
	ch, cancelSub := s.Subscribe()
	defer cancelSub()

	for i := 0; i < 3; i++ {
		s.enqueueLine("hello-" + string(rune('A'+i)))
	}
	s.flushPending()

	select {
	case payload := <-ch:
		for _, want := range []string{"hello-A", "hello-B", "hello-C"} {
			if !strings.Contains(string(payload), want) {
				t.Fatalf("一次 send 应包含 %q,实际: %s", want, payload)
			}
		}
		if got := strings.Count(string(payload), "\n"); got != 3 {
			t.Fatalf("期望 3 个换行,实际 %d: %q", got, payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout 等 batch")
	}

	if s.pendingN != 0 {
		t.Fatalf("flush 后 pendingN=%d, want 0", s.pendingN)
	}
	if len(s.pendingBuf) != 0 {
		t.Fatalf("flush 后 pendingBuf 还有 %d 字节", len(s.pendingBuf))
	}
}

// TestFlushPending_EmptyNoop 验证：空 buffer 调 flushPending 不广播。
func TestFlushPending_EmptyNoop(t *testing.T) {
	s := newTestSessionForBatcher()
	ch, cancelSub := s.Subscribe()
	defer cancelSub()

	s.flushPending()
	select {
	case payload := <-ch:
		t.Fatalf("空 flush 不应发,收到: %q", payload)
	case <-time.After(50 * time.Millisecond):
		// 期望：什么都没收到
	}
}

// TestPushImmediate_BypassesBatcher 验证：pushImmediate 立即广播，
// 不进 pendingBuf，让控制消息能及时到订阅者。
func TestPushImmediate_BypassesBatcher(t *testing.T) {
	s := newTestSessionForBatcher()
	ch, cancelSub := s.Subscribe()
	defer cancelSub()

	s.enqueueLine("queued")
	if s.pendingN != 1 {
		t.Fatalf("pendingN=%d, want 1", s.pendingN)
	}
	s.pushImmediate(formatOutput(Output{Kind: "info", Msg: "started"}))

	select {
	case payload := <-ch:
		if !strings.Contains(string(payload), `"kind":"info"`) {
			t.Fatalf("期望 info NDJSON,实际: %q", payload)
		}
		if strings.Contains(string(payload), "queued") {
			t.Fatalf("pushImmediate 不应带 queued 行,实际: %q", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout 等 pushImmediate")
	}
}

// TestRunBatcher_FlushesOnTicker 端到端：enqueue 行 → 50ms tick → flush。
func TestRunBatcher_FlushesOnTicker(t *testing.T) {
	s := newTestSessionForBatcher()
	ch, cancelSub := s.Subscribe()
	defer cancelSub()

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()
	go s.runBatcher(ctx)

	for i := 0; i < 4; i++ {
		s.enqueueLine("tick-" + string(rune('0'+i)))
	}
	select {
	case payload := <-ch:
		for _, want := range []string{"tick-0", "tick-1", "tick-2", "tick-3"} {
			if !strings.Contains(string(payload), want) {
				t.Fatalf("batcher flush 应包含 %q,实际: %q", want, payload)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout 等 batcher tick")
	}
}

// TestRunBatcher_FlushesOnSignal 验证：满 100 行时信号触发的 flush。
func TestRunBatcher_FlushesOnSignal(t *testing.T) {
	s := newTestSessionForBatcher()
	ch, cancelSub := s.Subscribe()
	defer cancelSub()

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()
	go s.runBatcher(ctx)

	for i := 0; i < 100; i++ {
		s.enqueueLine("burst")
	}
	select {
	case payload := <-ch:
		if n := strings.Count(string(payload), `"line":"burst"`); n != 100 {
			t.Fatalf("期望 100 行 burst,实际 %d 行: %q", n, string(payload)[:min(200, len(payload))])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout 等 batcher signal flush")
	}
}

// TestEnqueueLine_ConcurrentSafe 验证：多 goroutine 并发 enqueue 不会丢行 / 不会 panic。
func TestEnqueueLine_ConcurrentSafe(t *testing.T) {
	s := newTestSessionForBatcher()
	const G = 8
	const perG = 50
	var wg sync.WaitGroup
	wg.Add(G)
	for g := 0; g < G; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				s.enqueueLine("c")
			}
		}()
	}
	wg.Wait()
	if got := s.pendingN; got != G*perG {
		t.Fatalf("并发 enqueue 后 pendingN=%d, want %d", got, G*perG)
	}
}

// TestRunBatcher_FlushesOnCtxDone 验证：ctx 取消时 batcher 退出前最后 flush 一次，
// 防止残余行丢失（这个不变量 P0-3 文档里写明了）。
func TestRunBatcher_FlushesOnCtxDone(t *testing.T) {
	s := newTestSessionForBatcher()
	ch, cancelSub := s.Subscribe()
	defer cancelSub()

	ctx, cancelCtx := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.runBatcher(ctx)
		close(done)
	}()

	for i := 0; i < 7; i++ {
		s.enqueueLine("tail-line")
	}
	cancelCtx()
	select {
	case payload := <-ch:
		if !strings.Contains(string(payload), "tail-line") {
			t.Fatalf("ctx 取消时 batcher 应 flush 残余,实际: %q", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout 等 ctx-cancel flush")
	}
	select {
	case <-done:
		// 期望：batcher 已退出
	case <-time.After(time.Second):
		t.Fatal("batcher 没在 ctx 取消后退出")
	}
}

// newTestSessionForBatcher 构造一个最小可用的 Session 给 batcher 测试。
// 不开 SSH / Manager，因为 batcher 方法只依赖 Session 自身的状态。
func newTestSessionForBatcher() *Session {
	return &Session{
		subscribers: make(map[chan []byte]struct{}),
		flushSignal: make(chan struct{}, 1),
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
