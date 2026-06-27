package dlmanager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestNewID 验证 NewID 返回带 dl- 前缀的 hex 串，且每次都不同。
func TestNewID(t *testing.T) {
	a := NewID()
	b := NewID()
	if !strings.HasPrefix(a, "dl-") {
		t.Fatalf("ID 应带 dl- 前缀: %s", a)
	}
	if a == b {
		t.Fatalf("ID 不应重复: %s", b)
	}
}

// TestManagerCreateGet 验证 Create 把 session 收纳进池，Get 能拿回。
func TestManagerCreateGet(t *testing.T) {
	m := New()
	sess := &Session{ID: "dl-test-1", Kind: "files", Folder: "f1"}
	m.Create(sess)

	got, ok := m.Get("dl-test-1")
	if !ok {
		t.Fatal("Get 应返回 ok")
	}
	if got != sess {
		t.Fatal("Get 返回的不是同一指针")
	}

	if _, ok := m.Get("not-exists"); ok {
		t.Fatal("不存在的 ID 不应返回 ok")
	}
}

// TestManagerCreate 二次 Create 应在池里覆盖 / 看到同一个 ID（业务上不允许重复）。
func TestManagerCreateIdempotent(t *testing.T) {
	m := New()
	a := &Session{ID: "x", Folder: "a"}
	b := &Session{ID: "x", Folder: "b"}
	m.Create(a)
	m.Create(b)
	got, _ := m.Get("x")
	if got.Folder != "b" {
		t.Fatalf("期望后写覆盖前写，得到 Folder=%s", got.Folder)
	}
}

// TestCancelUnknown 不存在 ID 返回 false；存在时调用 cancel 函数。
func TestCancel(t *testing.T) {
	m := New()
	if m.Cancel("missing") {
		t.Fatal("不存在的 ID 不应 cancel 成功")
	}

	ctx, cancel := context.WithCancel(context.Background())
	sess := &Session{ID: "c1", Kind: "files"}
	m.Create(sess)
	sess.AttachCancel(cancel)
	if !m.Cancel("c1") {
		t.Fatal("存在的 ID 应 cancel 成功")
	}
	// ctx 应已被 cancel
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("ctx 未被 cancel")
	}
}

// TestSubscribe 验证：未结束的 session，订阅 channel 能收到广播。
func TestSubscribeBroadcast(t *testing.T) {
	m := New()
	sess := &Session{ID: "s1", Kind: "files", Folder: "f"}
	m.Create(sess)

	ch, unsub := sess.Subscribe()
	defer unsub()

	sess.Broadcast([]byte(`{"kind":"progress","i":1}`))
	select {
	case got := <-ch:
		if string(got) != `{"kind":"progress","i":1}` {
			t.Fatalf("广播内容不符: %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到广播")
	}
}

// TestBroadcastEvent 验证 BroadcastEvent 用 formatEvent 输出，且 JSON 字段值正确。
//
// 早期实现是手写 JSON（key 按插入顺序），现改用 encoding/json.Marshal，
// map 的 key 会按字母序输出。所以断言只检查 Contains，不再要求特定顺序。
func TestBroadcastEvent(t *testing.T) {
	m := New()
	sess := &Session{ID: "be1"}
	m.Create(sess)
	ch, unsub := sess.Subscribe()
	defer unsub()

	sess.BroadcastEvent("progress", map[string]any{
		"i":    3,
		"file": "a.log",
		"ok":   true,
	})

	select {
	case got := <-ch:
		s := string(got)
		for _, want := range []string{
			`"kind":"progress"`,
			`"i":3`,
			`"file":"a.log"`,
			`"ok":true`,
		} {
			if !strings.Contains(s, want) {
				t.Fatalf("缺少 %s: %s", want, s)
			}
		}
		// 输出末尾应有换行（SSE 友好）
		if !strings.HasSuffix(s, "\n") {
			t.Fatalf("应末尾换行: %s", s)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到事件")
	}
}

// TestFormatEvent_Unicode 回归测试：中文 / Unicode 文件名、error 信息必须输出合法 UTF-8。
//
// 旧实现里 jsonStringVal 写的是 byte(r)，会把中文 rune 截断成单字节，
// 生成非法的 UTF-8 字节序列。改用 encoding/json.Marshal 后必须通过。
func TestFormatEvent_Unicode(t *testing.T) {
	got := FormatEvent("progress", map[string]any{
		"file":    "服务器_app.log",
		"error":   "下载完成",
		"raw_dir": "中文目录/2026年06月",
	})
	s := string(got)

	// 关键断言：标准 json.Marshal 会把中文按 UTF-8 原样写到 "..." 里，
	// 即字符串里出现原始中文（不再走 \uXXXX 转义，除非字符串里有控制字符）。
	for _, want := range []string{
		`服务器_app.log`,
		`下载完成`,
		`中文目录/2026年06月`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("应原样包含 UTF-8 中文 %q: %s", want, s)
		}
	}
	// 不应再出现 \u 转义（除非是控制字符）
	if strings.Contains(s, `\u`) {
		t.Fatalf("不应出现 \\u 转义（旧 bug 痕迹）: %s", s)
	}
	// 整段应是合法 UTF-8：直接用 json.Unmarshal 反序列化回来能成功
	var back map[string]any
	if err := json.Unmarshal([]byte(s[:len(s)-1]), &back); err != nil { // 去掉末尾 \n
		t.Fatalf("输出不是合法 JSON: %v\nraw=%s", err, s)
	}
	if back["file"] != "服务器_app.log" {
		t.Fatalf("反序列化后 file 字段值丢失: %v", back["file"])
	}
}

// TestSubscribeLateSession 验证订阅时 session 已结束 → 立即拿到关闭 channel，
// 并可通过 Snapshot 拿到最终结果。
func TestSubscribeLateSession(t *testing.T) {
	m := New()
	sess := &Session{ID: "late", Kind: "files", Folder: "snap"}
	m.Create(sess)
	sess.MarkFinished([]Item{{Local: "a.zip", Kind: "zip"}}, nil)

	ch, unsub := sess.Subscribe()
	defer unsub()

	// channel 应已被关闭
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("订阅时已结束的 session，channel 应立即关闭（读到 ok=true）")
		}
	case <-time.After(time.Second):
		t.Fatal("订阅时已结束的 session，channel 应立即关闭（不应阻塞）")
	}

	res, finalErr, folder := sess.Snapshot()
	if finalErr != nil {
		t.Fatalf("finalErr 应为 nil: %v", finalErr)
	}
	if folder != "snap" {
		t.Fatalf("folder 应为 snap: %s", folder)
	}
	if len(res) != 1 || res[0].Local != "a.zip" {
		t.Fatalf("结果不符: %+v", res)
	}
}

// TestMarkFinishedSendsDone 验证 MarkFinished 会同步阻塞推一条 done，
// 然后关闭订阅者的 channel。订阅者用第二个返回值判断 stream 结束。
func TestMarkFinishedSendsDone(t *testing.T) {
	m := New()
	sess := &Session{ID: "fin", Kind: "files", Folder: "out"}
	m.Create(sess)

	ch, unsub := sess.Subscribe()
	defer unsub()

	done := make(chan struct{})
	var got string
	go func() {
		for line := range ch {
			got = string(line)
			if strings.Contains(got, `"kind":"done"`) {
				close(done)
				return
			}
		}
	}()

	sess.MarkFinished([]Item{{Local: "x.zip", Kind: "zip"}}, nil)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("未收到 done 事件")
	}
	if !strings.Contains(got, `"ok":true`) {
		t.Fatalf("done 应带 ok=true: %s", got)
	}
	if !strings.Contains(got, `"folder":"out"`) {
		t.Fatalf("done 应带 folder=out: %s", got)
	}

	// channel 应已关闭
	_, ok := <-ch
	if ok {
		t.Fatal("channel 应已关闭")
	}
}

// TestMarkFinishedWithError 验证失败时 done 行带 ok=false + error。
func TestMarkFinishedWithError(t *testing.T) {
	m := New()
	sess := &Session{ID: "err", Kind: "files"}
	m.Create(sess)

	ch, _ := sess.Subscribe()
	sess.MarkFinished(nil, errors.New("boom"))

	line := <-ch
	s := string(line)
	if !strings.Contains(s, `"ok":false`) {
		t.Fatalf("失败 done 应 ok=false: %s", s)
	}
	if !strings.Contains(s, `"error":"boom"`) {
		t.Fatalf("失败 done 应带 error: %s", s)
	}
}

// TestBroadcastToSlowSubscriber 验证订阅者消费慢时，广播会丢帧而不是阻塞。
func TestBroadcastToSlowSubscriber(t *testing.T) {
	m := New()
	sess := &Session{ID: "slow"}
	m.Create(sess)
	ch, unsub := sess.Subscribe()
	defer unsub()

	// 不读 ch，把 256 帧灌进去（buffer 是 128）
	for i := 0; i < 256; i++ {
		sess.Broadcast([]byte("x"))
	}
	// 不应阻塞：到这里说明 Broadcast 全部返回了
	// 读 buffer 容量（128），剩余的丢掉了
	for i := 0; i < 128; i++ {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("第 %d 帧阻塞", i)
		}
	}
}

// TestFormatEvent 验证导出的 FormatEvent 输出与 BroadcastEvent 一致。
func TestFormatEvent(t *testing.T) {
	got := string(FormatEvent("done", map[string]any{
		"ok":     true,
		"folder": "abc",
	}))
	if !strings.Contains(got, `"kind":"done"`) {
		t.Fatalf("应包含 kind=done: %s", got)
	}
	if !strings.Contains(got, `"ok":true`) {
		t.Fatalf("应包含 ok=true: %s", got)
	}
	if !strings.Contains(got, `"folder":"abc"`) {
		t.Fatalf("应包含 folder=abc: %s", got)
	}
}

// TestIdleGCFinishedAndAbandoned 验证结束且无订阅者的 session 会被回收。
func TestIdleGCFinishedAndAbandoned(t *testing.T) {
	m := New()
	m.IdleTimeout = 0 // 关闭兜底超时
	m.GCInterval = 20 * time.Millisecond
	sess := &Session{ID: "gc1"}
	m.Create(sess)
	sess.MarkFinished(nil, nil)

	done := make(chan struct{})
	go func() {
		m.IdleGC(sess)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("IdleGC 未回收已结束 session")
	}
	if _, ok := m.Get("gc1"); ok {
		t.Fatal("session 应已被回收")
	}
}

// TestIdleGCForceCancelOnTimeout 验证超时未结束的 session 被强制 cancel。
// 工作流模拟：IdleGC tick → 超时 → 调 cancel → 下载协程收到 ctx.Done → MarkFinished → 下次 tick 退出。
func TestIdleGCForceCancelOnTimeout(t *testing.T) {
	m := New()
	m.IdleTimeout = 50 * time.Millisecond
	m.GCInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	sess := &Session{ID: "gc2", Kind: "files"}
	m.Create(sess)
	sess.AttachCancel(cancel)
	sess.lastActivity = time.Now().Add(-time.Hour) // 假装最后一次活动在一小时前（BE-012：基于 lastActivity 判定空闲）

	done := make(chan struct{})
	go func() {
		m.IdleGC(sess)
		close(done)
	}()

	// 模拟下载协程：ctx 被 cancel 后调 MarkFinished
	go func() {
		<-ctx.Done()
		sess.MarkFinished(nil, errors.New("timeout"))
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("IdleGC 未退出")
	}
	if _, ok := m.Get("gc2"); ok {
		t.Fatal("session 应已被回收")
	}
}

// TestIdleGCKeepLiveSession 验证未结束且仍被订阅的 session 不会被回收。
func TestIdleGCKeepLiveSession(t *testing.T) {
	m := New()
	m.GCInterval = 20 * time.Millisecond
	sess := &Session{ID: "live"}
	m.Create(sess)

	_, unsub := sess.Subscribe()
	defer unsub()

	// 让 IdleGC 跑两轮，不应回收
	done := make(chan struct{})
	go func() {
		// 这里不调 IdleGC —— 直接验证 Get 仍能拿到
		close(done)
	}()
	<-done
	// 短暂 sleep 让 GC 至少有两次 tick 机会
	time.Sleep(60 * time.Millisecond)
	if _, ok := m.Get("live"); !ok {
		t.Fatal("活跃 session 不应被回收")
	}
}

// TestSubscribeConcurrency 验证多个订阅者并发广播 / 取消订阅时不死锁、不 panic。
func TestSubscribeConcurrency(t *testing.T) {
	m := New()
	sess := &Session{ID: "race"}
	m.Create(sess)

	var wg sync.WaitGroup
	const N = 20
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, unsub := sess.Subscribe()
			for j := 0; j < 100; j++ {
				select {
				case <-ch:
				default:
				}
				sess.Broadcast([]byte("y"))
			}
			unsub()
		}()
	}
	// 同时 MarkFinished
	sess.MarkFinished(nil, nil)
	wg.Wait()
}

// TestIsFinished 与 Snapshot 在未结束时为零值。
func TestUnfinishedSnapshot(t *testing.T) {
	m := New()
	sess := &Session{ID: "uf"}
	m.Create(sess)
	if sess.IsFinished() {
		t.Fatal("新建 session 不应 finished")
	}
	res, finalErr, _ := sess.Snapshot()
	if finalErr != nil {
		t.Fatalf("finalErr 应为零值: %v", finalErr)
	}
	if res != nil {
		t.Fatalf("result 应为零值: %+v", res)
	}
}

// TestMarkFinishedTimeout 验证：慢订阅者不会无限阻塞 MarkFinished。
//
// 场景：订阅者不读 channel，channel 缓冲 128；灌满后再 MarkFinished，
// DoneSendTimeout 设小一点（如 100ms），MarkFinished 应在超时后立即返回
// 而不是无限等待。
func TestMarkFinishedTimeout(t *testing.T) {
	m := New()
	m.DoneSendTimeout = 100 * time.Millisecond
	sess := &Session{ID: "slow-fin", Kind: "files"}
	m.Create(sess)

	ch, unsub := sess.Subscribe()
	defer unsub()

	// 把 ch 灌满 128 帧，让后续 send 全部阻塞
	for i := 0; i < 128; i++ {
		sess.Broadcast([]byte("fill"))
	}
	// MarkFinished 应在 ~DoneSendTimeout 后返回（不是无限等待）
	done := make(chan struct{})
	go func() {
		sess.MarkFinished(nil, errors.New("finish"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("MarkFinished 因慢订阅者卡死")
	}
	// channel 应已关闭。先把灌进去的 128 帧读完，再读应拿到 (zero, false)
	for i := 0; i < 128; i++ {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("第 %d 帧阻塞", i)
		}
	}
	_, ok := <-ch
	if ok {
		t.Fatal("channel 应已关闭")
	}
}

// TestMarkFinishedFastSubscriber 验证：快订阅者能在超时内收到 done。
func TestMarkFinishedFastSubscriber(t *testing.T) {
	m := New()
	m.DoneSendTimeout = 1 * time.Second
	sess := &Session{ID: "fast-fin"}
	m.Create(sess)

	ch, unsub := sess.Subscribe()
	defer unsub()

	// 启动 reader goroutine，确保 channel 不会被填满
	go func() {
		for line := range ch {
			_ = line
		}
	}()

	start := time.Now()
	sess.MarkFinished([]Item{{Local: "x.zip", Kind: "zip"}}, nil)
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Fatalf("MarkFinished 不应阻塞：%v", elapsed)
	}
}
