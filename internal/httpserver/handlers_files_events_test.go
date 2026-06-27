package httpserver

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ops-toolbox/internal/dlmanager"
)

// TestSSE_DoneEvent_CarriesPayload 验证已结束 session 的 SSE 流只发一个
// `event: done` 命名事件且 data 携带真实 payload（含 downloads / folder 字段，
// 而非空 `{}`）。
//
// 回归 BE-009：旧实现先写 `data: <payload>\n\n`（默认 message 事件），再写
// `event: done\ndata: {}\n\n`（done 命名事件但 data 空）。前端只听 done 时拿
// 不到结果，且两条路径行为不一致。
func TestSSE_DoneEvent_CarriesPayload(t *testing.T) {
	srv, _, _, _ := newTestServer(t)

	// 构造一个已经结束的 files 下载 session：直接调 MarkFinished 入队一个 Item，
	// 再用 Create 注册到 mgr。events 路径会走 IsFinished() 分支。
	id := dlmanager.NewID()
	sess := &dlmanager.Session{
		ID:        id,
		Kind:      "files",
		System:    "信贷生产",
		Server:    "mock-1",
		Folder:    "/tmp/test-downloads",
		CreatedAt: time.Now(),
	}
	srv.downloads.Create(sess)
	sess.MarkFinished([]dlmanager.Item{
		{
			Local: "SystemOut.log",
			Bytes: "128",
			Date:  "20260627",
			Kind:  "file",
		},
	}, nil)

	// 调 GET /api/files/download/{id}/events
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/files/download/"+id+"/events", nil)
	srv.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type 应是 text/event-stream，得到 %q", ct)
	}

	body := w.Body.String()
	t.Logf("SSE body:\n%s", body)

	// 解析 SSE 行：按空行分块，每块是一个事件
	events := parseSSEEvents(body)
	if len(events) == 0 {
		t.Fatalf("应至少有一个事件，body=%q", body)
	}

	// 期望：只有一个 event，且 event 名是 done，data 含 downloads 或 folder 字段
	var doneCount int
	var seenNonEmptyPayload bool
	for _, ev := range events {
		if ev.eventName != "done" {
			t.Errorf("期望所有事件都是 done，但出现 %q（data=%s）", ev.eventName, ev.data)
			continue
		}
		doneCount++
		// data 应能解析成 JSON 且含 downloads 或 folder 字段
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.data), &payload); err != nil {
			t.Fatalf("done 事件 data 不是合法 JSON: %v, data=%q", err, ev.data)
		}
		if _, ok := payload["downloads"]; ok {
			seenNonEmptyPayload = true
		}
		if _, ok := payload["folder"]; ok {
			seenNonEmptyPayload = true
		}
		if !seenNonEmptyPayload {
			t.Errorf("done 事件 data 缺少 downloads / folder 字段，data=%s", ev.data)
		}
		// kind 应为 done（dlmanager.formatEvent 注入的）
		if kind, _ := payload["kind"].(string); kind != "done" {
			t.Errorf("期望 kind=done，得到 %q", kind)
		}
	}
	if doneCount != 1 {
		t.Errorf("期望只发一个 event: done，得到 %d 个（旧 bug 是 1 个 message + 1 个空 done）", doneCount)
	}
	if !seenNonEmptyPayload {
		t.Errorf("done 事件 payload 应含真实 downloads/folder 字段，body=%s", body)
	}
}

// sseEvent 解析后的 SSE 事件
type sseEvent struct {
	eventName string // event: 字段（默认 "message"）
	data      string // 多行 data: 字段以 \n 拼接
}

// parseSSEEvents 把 SSE 文本按空行切成事件块，每块解析 event / data 行。
func parseSSEEvents(body string) []sseEvent {
	var events []sseEvent
	var cur sseEvent
	var hasContent bool

	flush := func() {
		if hasContent {
			if cur.eventName == "" {
				cur.eventName = "message"
			}
			events = append(events, cur)
		}
		cur = sseEvent{}
		hasContent = false
	}

	for _, line := range strings.Split(body, "\n") {
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			cur.eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			hasContent = true
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimPrefix(data, " ") // 允许 "data: foo" 和 "data:foo"
			if cur.data != "" {
				cur.data += "\n"
			}
			cur.data += data
			hasContent = true
		case strings.HasPrefix(line, ":"):
			// SSE 注释行（如 keepalive），忽略
		default:
			// 未知行也忽略（不破坏解析）
		}
	}
	flush()
	return events
}
