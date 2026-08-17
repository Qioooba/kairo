package audit

import (
	"sync"
	"testing"
)

// recordingSub 测试用订阅者：记录收到的 OpEvent。
type recordingSub struct {
	mu     sync.Mutex
	events []OpEvent
}

func (s *recordingSub) OnOp(ev OpEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *recordingSub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func (s *recordingSub) last() OpEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.events[len(s.events)-1]
}

// panicSub 测试用订阅者：回调直接 panic，验证 audit 包 recover。
type panicSub struct{}

func (panicSub) OnOp(OpEvent) { panic("订阅者故意崩溃") }

func TestSubscribe_ReceivesOpAndFields(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, "audit.log")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	sub := &recordingSub{}
	unsub := l.Subscribe(sub)
	defer unsub()

	l.Write("ssh.shell.start", "system", "sysA", "host", "10.0.0.1", "result", "ok")

	if sub.count() != 1 {
		t.Fatalf("订阅者应收到 1 条事件, 实际 %d", sub.count())
	}
	ev := sub.last()
	if ev.Op != "ssh.shell.start" {
		t.Fatalf("Op 不正确: %q", ev.Op)
	}
	// Fields 是 key/value 交替切片，顺序与 Write 传入一致
	want := []any{"system", "sysA", "host", "10.0.0.1", "result", "ok"}
	if len(ev.Fields) != len(want) {
		t.Fatalf("Fields 长度不正确: %d", len(ev.Fields))
	}
	for i := range want {
		if ev.Fields[i] != want[i] {
			t.Fatalf("Fields[%d] = %v, 期望 %v", i, ev.Fields[i], want[i])
		}
	}
}

func TestSubscribe_UnsubscribeIdempotent(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, "audit.log")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	sub := &recordingSub{}
	unsub := l.Subscribe(sub)
	unsub()
	unsub() // 重复注销应幂等、不 panic

	l.Write("ssh.shell.start")
	if sub.count() != 0 {
		t.Fatalf("注销后不应再收到事件, 实际 %d", sub.count())
	}
}

func TestSubscribe_PanicDoesNotBreakWrite(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, "audit.log")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	okSub := &recordingSub{}
	l.Subscribe(okSub)
	l.Subscribe(panicSub{}) // 会 panic，应被 recover

	// Write 本身不应 panic，正常订阅者仍应收到事件
	l.Write("ssh.shell.start", "system", "sysA")
	if okSub.count() != 1 {
		t.Fatalf("正常订阅者应收到 1 条事件, 实际 %d", okSub.count())
	}

	// 审计文件仍正常写盘
	recs, err := l.Recent(10, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Op != "ssh.shell.start" {
		t.Fatalf("审计记录异常: %+v", recs)
	}
}

func TestSubscribe_ZeroSubscribers(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, "audit.log")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// 无订阅者时 Write 正常（notifySubs 直接返回）
	l.Write("ssh.shell.start", "system", "sysA")
	recs, err := l.Recent(10, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("期望 1 条审计记录, 实际 %d", len(recs))
	}
}

func TestSubscribe_NilSubscriber(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, "audit.log")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// nil 订阅者：返回空注销函数，不 panic
	unsub := l.Subscribe(nil)
	unsub()
	l.Write("ssh.shell.start")
	recs, err := l.Recent(10, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("期望 1 条审计记录, 实际 %d", len(recs))
	}
}

// unsubHookSub 测试用订阅者：回调时执行一次 hook（用于"回调内注销"场景）。
type unsubHookSub struct {
	hook func()
}

func (s *unsubHookSub) OnOp(ev OpEvent) {
	if s.hook != nil {
		h := s.hook
		s.hook = nil
		h()
	}
}

// TestSubscribe_UnsubscribeDuringCallback 注销发生在回调执行期间。
// 通知走快照：当次 Write 的剩余订阅者仍会收到当次事件，之后的 Write 不再通知。
func TestSubscribe_UnsubscribeDuringCallback(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, "audit.log")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	other := &recordingSub{}
	unsubOther := l.Subscribe(other)
	hooker := &unsubHookSub{}
	hooker.hook = func() { unsubOther() }

	l.Subscribe(hooker)

	l.Write("op.a") // 第一次：hooker 回调内注销 other，但快照里 other 仍收到本次
	l.Write("op.b") // other 已注销，不应再收到

	if other.count() != 1 {
		t.Fatalf("回调内注销的订阅者应只收到当次快照事件, 实际 %d", other.count())
	}
	if other.last().Op != "op.a" {
		t.Fatalf("other 收到的应是 op.a, 实际 %q", other.last().Op)
	}
}
