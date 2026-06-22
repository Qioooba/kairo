package tailmgr

import (
	"testing"
	"time"
)

func TestFormatOutput(t *testing.T) {
	cases := []struct {
		name string
		in   Output
		want string
	}{
		{"line with quotes", Output{Kind: "line", Line: `say "hi"`}, `{"kind":"line","line":"say \"hi\""}` + "\n"},
		{"info", Output{Kind: "info", Msg: "start"}, `{"kind":"info","msg":"start"}` + "\n"},
		{"error", Output{Kind: "error", Msg: "boom"}, `{"kind":"error","msg":"boom"}` + "\n"},
		{"escape", Output{Kind: "line", Line: "a\nb\tc"}, `{"kind":"line","line":"a\nb\tc"}` + "\n"},
		{"control", Output{Kind: "line", Line: "\x01abc"}, `{"kind":"line","line":"\u0001abc"}` + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(formatOutput(tc.in))
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestJsonString(t *testing.T) {
	if got := jsonString("plain"); got != `"plain"` {
		t.Fatalf("plain: %s", got)
	}
	if got := jsonString(`a"b`); got != `"a\"b"` {
		t.Fatalf("escape quote: %s", got)
	}
	if got := jsonString("a\nb"); got != `"a\nb"` {
		t.Fatalf("escape newline: %s", got)
	}
}

func TestSubscribe_BroadcastAndUnsubscribe(t *testing.T) {
	s := &Session{subscribers: make(map[chan []byte]struct{})}
	sub1, cancel1 := s.Subscribe()
	sub2, cancel2 := s.Subscribe()
	ch1 := (<-chan []byte)(sub1)
	ch2 := (<-chan []byte)(sub2)
	defer cancel2()

	s.broadcast([]byte("hello"))
	// 都收到
	for i, ch := range []<-chan []byte{ch1, ch2} {
		select {
		case got := <-ch:
			if string(got) != "hello" {
				t.Fatalf("sub %d: %q", i, got)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("sub %d: 收不到", i)
		}
	}

	cancel1()
	// 再 broadcast，只有 ch2 收
	s.broadcast([]byte("world"))
	select {
	case got := <-ch2:
		if string(got) != "world" {
			t.Fatalf("ch2 want world, got %q", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch2 收不到")
	}
	// ch1 已 close，read 应当立即返回
	if got, ok := <-ch1; ok {
		t.Fatalf("ch1 已 unsub 但仍能收到: %q", got)
	}
}

func TestSession_StoppedAndMarkDone(t *testing.T) {
	s := &Session{subscribers: make(map[chan []byte]struct{})}
	ch, _ := s.Subscribe()

	stopped, err := s.Stopped()
	if stopped || err != nil {
		t.Fatalf("初始状态不对: stopped=%v err=%v", stopped, err)
	}

	s.markDone(nil)
	stopped, _ = s.Stopped()
	if !stopped {
		t.Fatal("markDone 后应 stopped=true")
	}
	// 订阅者应被关
	if _, ok := <-ch; ok {
		t.Fatal("markDone 应关闭订阅者 chan")
	}
}

func TestNewID(t *testing.T) {
	a := newID()
	b := newID()
	if a == b {
		t.Fatal("两次 ID 不应相同")
	}
	if len(a) < 8 || a[:5] != "tail-" {
		t.Fatalf("ID 格式不对: %s", a)
	}
}
