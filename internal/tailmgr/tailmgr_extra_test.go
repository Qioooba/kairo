package tailmgr

import (
	"strings"
	"testing"
	"time"
)

// 注：tailmgr_test.go 已覆盖 formatOutput/jsonString/Subscribe/markDone/newID 的大部分。
// 这里只补新场景：满 channel 行为、Manager 边界。

func TestSession_BroadcastDropsOnFull(t *testing.T) {
	s := &Session{subscribers: make(map[chan []byte]struct{})}
	// Subscribe 返回的是 <-chan，broadcast 写入时用内部 map 里的 chan
	// 这里直接构造两个 chan 塞进 map，模拟满 + 慢消费者
	full := make(chan []byte, 64)
	s.subscribers[full] = struct{}{}
	for i := 0; i < 64; i++ {
		full <- []byte("fill")
	}
	// 这次 broadcast 应该被丢（channel 满，default 分支走）
	s.broadcast([]byte("overflow"))
	// 验证 channel 还是满的，且头部还是 "fill"
	select {
	case got := <-full:
		if string(got) != "fill" {
			t.Errorf("overflow shouldn't replace head: got %q", got)
		}
	default:
		t.Fatal("channel unexpectedly empty")
	}
}

func TestSession_BroadcastToMultiple(t *testing.T) {
	s := &Session{subscribers: make(map[chan []byte]struct{})}
	c1 := make(chan []byte, 4)
	c2 := make(chan []byte, 4)
	s.subscribers[c1] = struct{}{}
	s.subscribers[c2] = struct{}{}
	s.broadcast([]byte("hi"))
	for i, c := range []chan []byte{c1, c2} {
		select {
		case got := <-c:
			if string(got) != "hi" {
				t.Errorf("sub %d: %q", i, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d timeout", i)
		}
	}
}

func TestSession_MarkDone_WithError(t *testing.T) {
	s := &Session{subscribers: make(map[chan []byte]struct{})}
	myErr := &testErr{msg: "boom"}
	s.markDone(myErr)
	stopped, err := s.Stopped()
	if !stopped {
		t.Error("should be stopped")
	}
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err: %v", err)
	}
}

func TestManager_StopMissing(t *testing.T) {
	m := NewManager()
	defer m.ShutdownAll()
	if err := m.Stop("nonexistent"); err == nil {
		t.Fatal("expected error")
	}
}

func TestManager_GetMissing(t *testing.T) {
	m := NewManager()
	defer m.ShutdownAll()
	if _, ok := m.Get("nope"); ok {
		t.Error("should not exist")
	}
}

func TestManager_Start_NilClient(t *testing.T) {
	m := NewManager()
	defer m.ShutdownAll()
	_, err := m.Start(nil, "s", "1.1.1.1", "/d", "f", "utf-8", 0)
	if err == nil {
		t.Fatal("nil client should fail")
	}
}

func TestManager_ShutdownAll_NoSessions(t *testing.T) {
	m := NewManager()
	m.ShutdownAll() // 空 sessions 不应 panic
}

func TestManager_NewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("nil")
	}
	if m.sessions == nil {
		t.Error("sessions map not init")
	}
	if m.idleAfter <= 0 {
		t.Error("idleAfter should be > 0")
	}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
