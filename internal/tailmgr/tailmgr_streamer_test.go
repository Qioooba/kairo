package tailmgr

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStreamer 是 Streamer 的可控 mock，用于 tailmgr 的单测。
//
// 用法：
//   f := &fakeStreamer{}
//   f.lines = []string{"a", "b"}
//   f.exitCode = 0
//   f.err = nil       // 或 f.err = ctx.Err() 模拟 ctx 取消
//   f.delayBeforeEmit = 50 * time.Millisecond  // 控制 timing
//
// 真实测试代码：
//   m := NewManager()
//   m.GCInterval = 20*time.Millisecond
//   s, err := m.Start(f, ...)
//   ch, _ := s.Subscribe()
//   for line := range ch { ... }
type fakeStreamer struct {
	mu       sync.Mutex
	lines    []string
	exitCode int
	err      error

	// delayBeforeEmit 在调 onLine 前等一下，便于订阅者先 Subscribe
	delayBeforeEmit time.Duration
	// delayBeforeReturn 在所有 line 推完后等一下再返回，便于测试订阅者能收齐
	delayBeforeReturn time.Duration

	started chan struct{} // 第一次调 Stream 时关闭；让测试能 join
}

func (f *fakeStreamer) Stream(ctx context.Context, _ /*command*/, _ /*encoding*/ string, onLine func(string)) (int, error) {
	f.mu.Lock()
	if f.started == nil {
		f.started = make(chan struct{})
	}
	f.mu.Unlock()
	select {
	case <-f.started:
	default:
		close(f.started)
	}
	if f.delayBeforeEmit > 0 {
		select {
		case <-time.After(f.delayBeforeEmit):
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}
	for _, line := range f.lines {
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		default:
		}
		onLine(line)
	}
	if f.delayBeforeReturn > 0 {
		select {
		case <-time.After(f.delayBeforeReturn):
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}
	return f.exitCode, f.err
}

// collectN 收 ch 直到拿到 n 条或超时
func collectN(t *testing.T, ch <-chan []byte, n int, timeout time.Duration) []string {
	t.Helper()
	got := make([]string, 0, n)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for len(got) < n {
		select {
		case line, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, string(line))
		case <-deadline.C:
			t.Fatalf("只收到 %d/%d 行就超时", len(got), n)
		}
	}
	return got
}

// collectAll 收到 channel 关闭为止
func collectAll(t *testing.T, ch <-chan []byte, timeout time.Duration) []string {
	t.Helper()
	got := []string{}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case line, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, string(line))
		case <-deadline.C:
			t.Fatalf("收 %d 行就超时（channel 未关）", len(got))
		}
	}
}

// ---------- Start 全链路 ----------

func TestManager_Start_HappyPath(t *testing.T) {
	m := NewManager()
	m.GCInterval = 20 * time.Millisecond
	defer m.ShutdownAll()

	f := &fakeStreamer{
		lines:             []string{"hello", "world"},
		exitCode:          0,
		delayBeforeEmit:   30 * time.Millisecond, // 给 Subscribe 时间
		delayBeforeReturn: 10 * time.Millisecond,
	}
	s, err := m.Start(f, "prod-1", "10.0.0.1", "/var/log", "app.log", "utf-8", 10)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.ID == "" {
		t.Error("session ID 不应为空")
	}

	// 订阅收 line + done
	ch, unsub := s.Subscribe()
	defer unsub()

	// 至少要收到两条 line + 一条 info + 一条 done
	got := collectN(t, ch, 4, 2*time.Second)
	joined := strings.Join(got, "\n")
	for _, want := range []string{`"kind":"info"`, `"kind":"line","line":"hello"`, `"kind":"line","line":"world"`, `"kind":"done"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("应包含 %s，实际: %s", want, joined)
		}
	}
}

func TestManager_Start_NilClient_Streamer(t *testing.T) {
	m := NewManager()
	defer m.ShutdownAll()
	// typed nil 也应被识别（Streamer 接口的 nil）
	var nilStreamer Streamer
	_, err := m.Start(nilStreamer, "s", "1.1.1.1", "/d", "f", "utf-8", 0)
	if err == nil {
		t.Fatal("typed-nil streamer 也应报错")
	}
}

func TestManager_Start_StreamErr(t *testing.T) {
	m := NewManager()
	m.GCInterval = 20 * time.Millisecond
	defer m.ShutdownAll()

	wantErr := errors.New("ssh broken")
	f := &fakeStreamer{
		lines:             []string{"first"},
		err:               wantErr,
		delayBeforeReturn: 5 * time.Millisecond,
	}
	s, err := m.Start(f, "s", "1.1.1.1", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatal(err)
	}

	ch, _ := s.Subscribe()
	// 收完整：info + line + error + done → 用 collectAll 等 channel 关闭
	got := collectAll(t, ch, 2*time.Second)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, `"kind":"error"`) || !strings.Contains(joined, "ssh broken") {
		t.Errorf("应包含 error 事件含原因: %s", joined)
	}
	if !strings.Contains(joined, `"kind":"done"`) {
		t.Errorf("error 后应推 done: %s", joined)
	}

	stopped, sErr := s.Stopped()
	if !stopped {
		t.Error("session 应已停止")
	}
	if sErr != wantErr {
		t.Errorf("stopped err 应等于 streamErr: got %v", sErr)
	}
}

func TestManager_Start_CtxCancel_PropagatesAsCanceled(t *testing.T) {
	m := NewManager()
	m.GCInterval = 20 * time.Millisecond
	defer m.ShutdownAll()

	// Streamer 模拟：阻塞直到 ctx 取消
	f := &fakeStreamer{
		delayBeforeReturn: 5 * time.Second, // 实际靠 ctx 取消提前返回
	}
	s, err := m.Start(f, "s", "1.1.1.1", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatal(err)
	}

	// 订阅但不读
	ch, _ := s.Subscribe()

	// 显式 stop → killSSH → ctx cancel
	if err := m.Stop(s.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// 应该很快收到 done（不会因为 ctx 取消而推 error）
	got := collectN(t, ch, 2, 2*time.Second)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, `"kind":"done"`) {
		t.Errorf("ctx 取消后应推 done: %s", joined)
	}
	if strings.Contains(joined, `"kind":"error"`) {
		t.Errorf("ctx 取消不应推 error 事件: %s", joined)
	}
}

func TestManager_Stop_TriggersCancel(t *testing.T) {
	m := NewManager()
	m.GCInterval = 20 * time.Millisecond
	defer m.ShutdownAll()

	f := &fakeStreamer{}
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(s.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// killSSH 被调用过：直接调一次，看 ctx 是否 done
	// killSSH 是私有；这里靠后续行为（Stream 返回 ctx.Err）间接验证
	// 给 Streamer 一点时间看到 ctx 取消
	time.Sleep(50 * time.Millisecond)
	if _, ok := m.Get(s.ID); !ok {
		// session 可能已经被 idleGC 回收（GCInterval=20ms 跑过两轮了）
		// 也可以仍存在但 stopped=true
		return
	}
	stopped, _ := s.Stopped()
	if !stopped {
		t.Error("Stop 后 session 应 stopped")
	}
}

func TestManager_Get_AfterStart(t *testing.T) {
	m := NewManager()
	m.GCInterval = 20 * time.Millisecond
	defer m.ShutdownAll()

	f := &fakeStreamer{delayBeforeReturn: 100 * time.Millisecond}
	s, err := m.Start(f, "prod", "10.0.0.1", "/var/log", "x.log", "utf-8", 5)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m.Get(s.ID)
	if !ok {
		t.Fatal("Get 应能找到刚 Start 的 session")
	}
	if got.ServerName != "prod" || got.File != "x.log" || got.Encoding != "utf-8" {
		t.Errorf("session 字段不对: %+v", got)
	}
}

func TestManager_ShutdownAll_KillsAll(t *testing.T) {
	m := NewManager()
	m.GCInterval = 20 * time.Millisecond

	f1 := &fakeStreamer{delayBeforeReturn: 200 * time.Millisecond}
	f2 := &fakeStreamer{delayBeforeReturn: 200 * time.Millisecond}
	s1, _ := m.Start(f1, "a", "1.1.1.1", "/d", "f", "utf-8", 0)
	s2, _ := m.Start(f2, "b", "2.2.2.2", "/d", "f", "utf-8", 0)

	m.ShutdownAll()

	// 给协程一点时间响应 ctx 取消
	time.Sleep(100 * time.Millisecond)

	for _, s := range []*Session{s1, s2} {
		stopped, _ := s.Stopped()
		if !stopped {
			t.Errorf("session %s 应被 ShutdownAll 杀死", s.ID)
		}
	}
}

// ---------- idleGC ----------

func TestManager_IdleGC_RecycleFinished(t *testing.T) {
	m := NewManager()
	m.GCInterval = 15 * time.Millisecond

	f := &fakeStreamer{
		delayBeforeEmit:   5 * time.Millisecond,
		delayBeforeReturn: 5 * time.Millisecond,
	}
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatal(err)
	}

	// 订阅 → 读完 → unsubscribe → 等 idleGC 回收
	ch, unsub := s.Subscribe()
	collectN(t, ch, 1, 2*time.Second)
	unsub()

	// 给 idleGC 几轮时间
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := m.Get(s.ID); !ok {
			return // 回收成功
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("idleGC 未回收已结束且无订阅者的 session")
}

func TestManager_IdleGC_KeepWhileSubscribed(t *testing.T) {
	m := NewManager()
	m.GCInterval = 15 * time.Millisecond
	defer m.ShutdownAll()

	f := &fakeStreamer{delayBeforeReturn: 100 * time.Millisecond}
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatal(err)
	}

	// 持续订阅中：session 不应被回收
	ch, _ := s.Subscribe()
	defer func() {
		// 排空后再 unsub，避免 idleGC 跟测试退出抢
		go func() {
			for range ch {
			}
		}()
	}()

	time.Sleep(100 * time.Millisecond)
	if _, ok := m.Get(s.ID); !ok {
		t.Fatal("仍有订阅者的 session 不应被回收")
	}
}

// ---------- formatOutput / jsonString 已由 tailmgr_test.go 覆盖，这里只补缺 ----------

func TestFormatOutput_UnknownKind(t *testing.T) {
	got := string(formatOutput(Output{Kind: "bogus"}))
	if !strings.Contains(got, `"kind":"error"`) {
		t.Errorf("未知 kind 应降级为 error: %s", got)
	}
}

func TestJsonString_ControlChar(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hi", `"hi"`},
		{"a\"b", `"a\"b"`},
		{"a\\b", `"a\\b"`},
		{"a\nb", `"a\nb"`},
		{"a\rb", `"a\rb"`},
		{"a\tb", `"a\tb"`},
		{"a\x01b", `"a\u0001b"`},
		{"中文", `"中文"`},
	}
	for _, c := range cases {
		if got := jsonString(c.in); got != c.want {
			t.Errorf("jsonString(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

// ---------- idleAfter 兜底 ----------

func TestManager_IdleAfterForcesKill(t *testing.T) {
	m := NewManager()
	m.GCInterval = 15 * time.Millisecond
	m.idleAfter = 50 * time.Millisecond // 缩短便于测试
	defer m.ShutdownAll()

	f := &fakeStreamer{delayBeforeReturn: 5 * time.Second}
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatal(err)
	}
	s.CreatedAt = time.Now().Add(-time.Hour) // 假装很老

	ch, _ := s.Subscribe()
	// 等到 idleAfter 触发 → killSSH → ctx 取消 → Stream 返回
	got := collectN(t, ch, 2, 3*time.Second)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, `"kind":"done"`) {
		t.Errorf("超时兜底应让 Stream 返回并推 done: %s", joined)
	}
}