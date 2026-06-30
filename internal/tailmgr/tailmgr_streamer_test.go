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
//
//	f := &fakeStreamer{}
//	f.lines = []string{"a", "b"}
//	f.exitCode = 0
//	f.err = nil       // 或 f.err = ctx.Err() 模拟 ctx 取消
//	f.delayBeforeEmit = 50 * time.Millisecond  // 控制 timing
//
// 真实测试代码：
//
//	m := NewManager()
//	m.GCInterval = 20*time.Millisecond
//	s, err := m.Start(f, ...)
//	ch, _ := s.Subscribe()
//	for line := range ch { ... }
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

	// baselineOutput / baselineErr 控制 RunOutput 的返回值：
	//   baselineOutput 为 "" 表示"拿不到 baseline"（前端 fallback）。
	// 真实 baseline 测试：把 "1234" 写到 baselineOutput 即可。
	baselineOutput string
	baselineErr    error

	// recordRunOutput 让 RunOutput 调一次后置 true，单测可断言"被调过"。
	runOutputCalled bool
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

// RunOutput fake 实现：返回 (baselineOutput, baselineErr)；命令/编码都忽略。
// 用于 tail start 前的 baseline 取值（wc/awk 一次性命令）。
func (f *fakeStreamer) RunOutput(ctx context.Context, _ string, _ string, _ time.Duration) (string, error) {
	f.mu.Lock()
	f.runOutputCalled = true
	out := f.baselineOutput
	err := f.baselineErr
	f.mu.Unlock()
	if err != nil {
		return "", err
	}
	return out, nil
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

	// 至少要收到两条 line + 一条 info（done 不再走 channel 广播，BE-019）
	got := collectN(t, ch, 3, 2*time.Second)
	joined := strings.Join(got, "\n")
	for _, want := range []string{`"kind":"info"`, `"kind":"line","line":"hello"`, `"kind":"line","line":"world"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("应包含 %s，实际: %s", want, joined)
		}
	}
	// done 消息由 setDoneMsg 设置（不广播），handler 的 !open 分支读 DoneMsg 发 SSE
	if !strings.Contains(s.DoneMsg(), "tail 结束") {
		t.Errorf("DoneMsg 应包含结束消息，实际: %q", s.DoneMsg())
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
	// 收完整：info + line + error → 用 collectAll 等 channel 关闭
	// done 不再走 channel 广播（BE-019），改为 setDoneMsg + handler !open 分支发 SSE
	got := collectAll(t, ch, 2*time.Second)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, `"kind":"error"`) || !strings.Contains(joined, "ssh broken") {
		t.Errorf("应包含 error 事件含原因: %s", joined)
	}
	if !strings.Contains(s.DoneMsg(), "tail 结束") {
		t.Errorf("error 后应设置 doneMsg: %q", s.DoneMsg())
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

	// 应该很快收到 info（不会因为 ctx 取消而推 error）。
	// done 不再走 channel 广播（BE-019），改为 setDoneMsg。
	got := collectN(t, ch, 1, 2*time.Second)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, `"kind":"error"`) {
		t.Errorf("ctx 取消不应推 error 事件: %s", joined)
	}
	if !strings.Contains(s.DoneMsg(), "tail 结束") {
		t.Errorf("ctx 取消后应设置 doneMsg: %q", s.DoneMsg())
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

// TestManager_IdleAfterForcesKill 验证 BE-002 后的兜底语义：
// 无订阅者 + lastActivity 超过 idleAfter → idleGC 调 killSSH → session stopped。
// 注意：BE-002 修复后"有订阅者时永不 idle-kill"，所以本测试不能 Subscribe。
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
	// 不订阅：lastActivity = CreatedAt = now，idleAfter 后被视为空闲 → killSSH
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if stopped, _ := s.Stopped(); stopped {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("无订阅 + 超过 idleAfter 应被 idleGC kill（BE-002 修复后兜底）")
}

// continuousStreamer 持续每隔 interval 产一行日志，直到 ctx 被取消。
// 用于 BE-002 测试：持续有日志输出时不应被 idleGC kill。
type continuousStreamer struct {
	interval time.Duration
	started  chan struct{}
	once     sync.Once

	// baselineOutput / runOutputCalled 同 fakeStreamer；测试里都用同一套默认值。
	baselineOutput  string
	baselineErr     error
	runOutputCalled bool
}

func (c *continuousStreamer) Stream(ctx context.Context, _, _ string, onLine func(string)) (int, error) {
	c.once.Do(func() {
		if c.started == nil {
			c.started = make(chan struct{})
		}
		close(c.started)
	})
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-ticker.C:
			onLine("tick")
		}
	}
}

func (c *continuousStreamer) RunOutput(_ context.Context, _ string, _ string, _ time.Duration) (string, error) {
	c.runOutputCalled = true
	if c.baselineErr != nil {
		return "", c.baselineErr
	}
	return c.baselineOutput, nil
}

// TestManager_IdleGC_KeepAliveWhileProducingLogs 验证 BE-002 核心修复：
// 持续有日志输出（lastActivity 持续更新）时，即使超过 idleAfter 也不应被 kill。
// 对应用户要求："持续有订阅/持续有日志输出时，超过 6 分钟不应断开"。
func TestManager_IdleGC_KeepAliveWhileProducingLogs(t *testing.T) {
	// idleAfter=80ms，GCInterval=15ms；streamer 每 20ms 产一行（远小于 idleAfter）。
	// 持续 400ms（5x idleAfter）后，session 应仍存活。
	m := NewManagerWithIdle(80 * time.Millisecond)
	m.GCInterval = 15 * time.Millisecond
	defer m.ShutdownAll()

	f := &continuousStreamer{interval: 20 * time.Millisecond}
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatal(err)
	}

	// 订阅并后台排空 ch；broadcast 用 default-drop 不阻塞 streamer，但排空便于观察。
	// 不 join drain goroutine：ShutdownAll 会 markDone → close(ch) → goroutine 自然退出。
	ch, unsub := s.Subscribe()
	defer unsub()
	go func() {
		for range ch {
		}
	}()

	// 等 400ms，远超 idleAfter=80ms，但 streamer 每 20ms 产一行 → lastActivity 持续更新
	time.Sleep(400 * time.Millisecond)

	if stopped, _ := s.Stopped(); stopped {
		t.Fatal("持续有日志输出时 session 不应被 idleGC kill（BE-002 修复）")
	}
	if _, ok := m.Get(s.ID); !ok {
		t.Fatal("session 应仍在 map 中")
	}
}

// TestManager_IdleGC_KeepAliveWhileSubscribed 验证 BE-002 另一核心场景：
// 有订阅者但无日志输出时，即使超过 idleAfter 也不应被 kill。
// 对应用户要求："持续有订阅时，超过 6 分钟不应断开"。
func TestManager_IdleGC_KeepAliveWhileSubscribed(t *testing.T) {
	// idleAfter=80ms，GCInterval=15ms；streamer 阻塞不产日志（模拟"有订阅但无日志"）。
	m := NewManagerWithIdle(80 * time.Millisecond)
	m.GCInterval = 15 * time.Millisecond
	defer m.ShutdownAll()

	f := &fakeStreamer{delayBeforeReturn: 5 * time.Second} // 阻塞，不产日志
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatal(err)
	}

	ch, _ := s.Subscribe()
	defer func() {
		go func() {
			for range ch {
			}
		}()
	}()

	// 等 400ms，远超 idleAfter=80ms，但有订阅者 → 不应被 kill
	time.Sleep(400 * time.Millisecond)

	if stopped, _ := s.Stopped(); stopped {
		t.Fatal("有订阅者时 session 不应被 idleGC kill（BE-002 修复）")
	}
	if _, ok := m.Get(s.ID); !ok {
		t.Fatal("session 应仍在 map 中")
	}
}

// ---------- Baseline（v0.10 起：tail 启动瞬间拿文件真实行号） ----------

// TestManager_Start_BaselineFromRunOutput 验证 happy path：fakeStreamer 的 baselineOutput
// 是合法数字时，Session.Baseline = 该数字。
func TestManager_Start_BaselineFromRunOutput(t *testing.T) {
	m := NewManager()
	m.GCInterval = 20 * time.Millisecond
	defer m.ShutdownAll()

	f := &fakeStreamer{
		lines:          []string{"a", "b"},
		baselineOutput: "1234",
	}
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 100)
	if err != nil {
		t.Fatal(err)
	}
	if !f.runOutputCalled {
		t.Fatal("RunOutput 应被调用过一次（取 baseline）")
	}
	if got := s.GetBaseline(); got != 1234 {
		t.Fatalf("Baseline 应等于 1234，实际 %d", got)
	}
}

// TestManager_Start_BaselineEmpty_FallbackToNegative 验证 baseline stdout 为空时
// 走 -1 兜底（前端 fallback，不阻塞 tail）。
func TestManager_Start_BaselineEmpty_FallbackToNegative(t *testing.T) {
	m := NewManager()
	m.GCInterval = 20 * time.Millisecond
	defer m.ShutdownAll()

	f := &fakeStreamer{
		lines:          []string{"hello"},
		baselineOutput: "", // 模拟 awk 没出 stdout（文件不存在等）
	}
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 50)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.GetBaseline(); got != -1 {
		t.Fatalf("空 stdout 应 fallback 为 -1，实际 %d", got)
	}
}

// TestManager_Start_BaselineErr_FallbackToNegative 验证 RunOutput 报错时
// Session 仍能成功创建（不阻塞 tail），baseline 走 -1。
func TestManager_Start_BaselineErr_FallbackToNegative(t *testing.T) {
	m := NewManager()
	m.GCInterval = 20 * time.Millisecond
	defer m.ShutdownAll()

	f := &fakeStreamer{
		lines:       []string{"x"},
		baselineErr: errors.New("wc 远端失败"),
	}
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 0)
	if err != nil {
		t.Fatalf("RunOutput 报错不应让 Start 失败: %v", err)
	}
	if !f.runOutputCalled {
		t.Fatal("RunOutput 应被尝试调用过")
	}
	if got := s.GetBaseline(); got != -1 {
		t.Fatalf("RunOutput 报错应 fallback 为 -1，实际 %d", got)
	}
}

// TestManager_Start_BaselineNonNumeric_FallbackToNegative 验证 stdout 不是合法数字
// （带单位 / 含字母 / 含换行外字符）时 fallback 为 -1。
func TestManager_Start_BaselineNonNumeric_FallbackToNegative(t *testing.T) {
	m := NewManager()
	m.GCInterval = 20 * time.Millisecond
	defer m.ShutdownAll()

	cases := []string{"abc", "12 abc", "\x00123", "-5"}
	for _, bad := range cases {
		f := &fakeStreamer{
			lines:          []string{"x"},
			baselineOutput: bad,
		}
		s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 0)
		if err != nil {
			t.Fatalf("非数字 baseline 不应让 Start 失败: %v", err)
		}
		if got := s.GetBaseline(); got != -1 {
			t.Fatalf("baseline %q 应 fallback 为 -1，实际 %d", bad, got)
		}
	}
}

// TestManager_Start_BaselineTrimmedWhitespace 验证 stdout 前后有空格/换行也能正确解析。
func TestManager_Start_BaselineTrimmedWhitespace(t *testing.T) {
	m := NewManager()
	m.GCInterval = 20 * time.Millisecond
	defer m.ShutdownAll()

	f := &fakeStreamer{
		baselineOutput: "  5000\n",
	}
	s, err := m.Start(f, "s", "h", "/d", "f", "utf-8", 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.GetBaseline(); got != 5000 {
		t.Fatalf("应 trim 后解析为 5000，实际 %d", got)
	}
}
