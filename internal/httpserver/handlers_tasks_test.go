// /api/tasks/* 端点测试
//
// 覆盖:
//  1. 未注入 Manager → 列表空数组 / 写操作 503
//  2. CRUD: 增(合法/非法 cron) / 改 / 删 / 不存在 404
//  3. toggle 启停 / run 立即执行(异步) / runs 历史
//  4. 非法方法 405 / 未知子路径 404
//  5. 命令超时被终止(StatusTimeout) — P0-3 回归
//
// 测试基础设施复用 httpserver_test.go 的 newTestServer / doRequest。
package httpserver

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kairo/internal/schedtask"
)

// newTestServerWithTasks 构造带 schedtask.Manager 的 Server (存储落在 t.TempDir)。
func newTestServerWithTasks(t *testing.T) (*Server, *schedtask.Manager) {
	t.Helper()
	srv, _, _, _ := newTestServer(t)
	dir := t.TempDir()
	st := schedtask.NewStore(filepath.Join(dir, "tasks.json"), filepath.Join(dir, "runs.json"))
	if err := st.EnsurePath(); err != nil {
		t.Fatalf("EnsurePath: %v", err)
	}
	m, err := schedtask.NewManager(st)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Stop)
	srv.SetTasks(m)
	return srv, m
}

// addTask 发一条任务定义并返回响应。
func addTask(t *testing.T, srv *Server, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return doRequest(srv, "POST", "/api/tasks", body)
}

// ---------- 未注入 Manager ----------

// TestTasksList_NoManager 未注入时列表返回空数组，写操作返回 503。
func TestTasksList_NoManager(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/tasks", nil)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("未注入应返回 [], got=%s", w.Body.String())
	}
	w = doRequest(srv, "POST", "/api/tasks", map[string]any{"name": "x", "cron": "* * * * *", "command": "echo x"})
	if w.Code != 503 {
		t.Errorf("未注入 add 应 503, got=%d body=%s", w.Code, w.Body.String())
	}
}

// ---------- CRUD ----------

// TestTasksAdd_Valid 新增合法任务 → 200 + id + enabled 默认 true + next_run_at。
func TestTasksAdd_Valid(t *testing.T) {
	srv, _ := newTestServerWithTasks(t)
	w := addTask(t, srv, map[string]any{
		"name": "定时同步", "cron": "*/5 * * * *", "command": "git pull",
		"work_dir": "/tmp", "timeout_sec": 60,
	})
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	got := decodeJSON(t, w.Body.Bytes())
	if got["id"] == "" || got["name"] != "定时同步" || got["enabled"] != true {
		t.Errorf("Add 返回字段不符: %+v", got)
	}
	if got["next_run_at"] == "" {
		t.Errorf("应返回 next_run_at: %+v", got)
	}
}

// TestTasksAdd_InvalidCron 非法 cron / 缺名字 / 缺命令 / 超时超限 → 400。
func TestTasksAdd_InvalidCron(t *testing.T) {
	srv, _ := newTestServerWithTasks(t)
	for _, body := range []map[string]any{
		{"name": "bad", "cron": "not-a-cron", "command": "echo x"},
		{"name": "", "cron": "* * * * *", "command": "echo x"},
		{"name": "x", "cron": "* * * * *", "command": "   "},
		{"name": "x", "cron": "* * * * *", "command": "echo", "timeout_sec": 99999},
	} {
		w := addTask(t, srv, body)
		if w.Code != 400 {
			t.Errorf("非法输入应 400, body=%+v got=%d %s", body, w.Code, w.Body.String())
		}
	}
}

// TestTasksUpdate 更新字段；enabled 被忽略保留原值；不存在 404。
func TestTasksUpdate(t *testing.T) {
	srv, _ := newTestServerWithTasks(t)
	added := addTaskOrDie(t, srv)
	id := decodeJSON(t, added)["id"].(string)

	w := doRequest(srv, "PUT", "/api/tasks/"+id, map[string]any{
		"name": "改名", "cron": "0 1 * * *", "command": "echo y",
	})
	if w.Code != 200 {
		t.Fatalf("update code=%d body=%s", w.Code, w.Body.String())
	}
	got := decodeJSON(t, w.Body.Bytes())
	if got["name"] != "改名" || got["cron"] != "0 1 * * *" || got["command"] != "echo y" {
		t.Errorf("update 字段不符: %+v", got)
	}
	// enabled 保留原值（新增默认启用）
	if got["enabled"] != true {
		t.Errorf("PUT 不应改 enabled: %+v", got)
	}

	// 不存在 → 404
	w = doRequest(srv, "PUT", "/api/tasks/does-not-exist", map[string]any{"name": "n", "cron": "* * * * *", "command": "echo"})
	if w.Code != 404 {
		t.Errorf("update 不存在应 404, got=%d", w.Code)
	}
}

// TestTasksDelete 删除成功 200；删除不存在 404。
func TestTasksDelete(t *testing.T) {
	srv, _ := newTestServerWithTasks(t)
	id := decodeJSON(t, addTaskOrDie(t, srv))["id"].(string)

	w := doRequest(srv, "DELETE", "/api/tasks/"+id, nil)
	if w.Code != 200 {
		t.Fatalf("delete code=%d body=%s", w.Code, w.Body.String())
	}
	// 列表已空
	w = doRequest(srv, "GET", "/api/tasks", nil)
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("删除后列表应空: %s", w.Body.String())
	}
	w = doRequest(srv, "DELETE", "/api/tasks/"+id, nil)
	if w.Code != 404 {
		t.Errorf("删除不存在应 404, got=%d", w.Code)
	}
}

// ---------- toggle / run / runs ----------

// TestTasksToggle 启停翻转。
func TestTasksToggle(t *testing.T) {
	srv, _ := newTestServerWithTasks(t)
	id := decodeJSON(t, addTaskOrDie(t, srv))["id"].(string)

	w := doRequest(srv, "POST", "/api/tasks/"+id+"/toggle", nil)
	if w.Code != 200 {
		t.Fatalf("toggle code=%d body=%s", w.Code, w.Body.String())
	}
	if decodeJSON(t, w.Body.Bytes())["enabled"] != false {
		t.Errorf("toggle 后应 disabled: %s", w.Body.String())
	}
	w = doRequest(srv, "POST", "/api/tasks/"+id+"/toggle", nil)
	if decodeJSON(t, w.Body.Bytes())["enabled"] != true {
		t.Errorf("再 toggle 应 enabled: %s", w.Body.String())
	}
}

// TestTasksRunAndRuns 立即执行 → 200 started；runs 历史有记录。
func TestTasksRunAndRuns(t *testing.T) {
	srv, _ := newTestServerWithTasks(t)
	id := decodeJSON(t, addTaskOrDie(t, srv))["id"].(string)

	w := doRequest(srv, "POST", "/api/tasks/"+id+"/run", nil)
	if w.Code != 200 {
		t.Fatalf("run code=%d body=%s", w.Code, w.Body.String())
	}
	if decodeJSON(t, w.Body.Bytes())["started"] != true {
		t.Errorf("run 应返回 started:true: %s", w.Body.String())
	}

	// 轮询等异步完成（最多 3s）
	deadline := time.Now().Add(3 * time.Second)
	for {
		w = doRequest(srv, "GET", "/api/tasks/"+id+"/runs", nil)
		if w.Code != 200 {
			t.Fatalf("runs code=%d body=%s", w.Code, w.Body.String())
		}
		var runs []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &runs); err != nil {
			t.Fatalf("runs 非 JSON: %v (%s)", err, w.Body.String())
		}
		if len(runs) > 0 {
			if runs[0]["status"] != "success" || runs[0]["exit_code"] != float64(0) {
				t.Errorf("runs 状态不符: %+v", runs[0])
			}
			if runs[0]["trigger"] != "manual" {
				t.Errorf("手动触发应标 manual: %+v", runs[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runs 未在 3s 内出现")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ---------- 命令超时被终止 (P0-3 回归) ----------

// TestTasksRun_TimeoutKill 命令 sleep 10 + timeout_sec=1 → runs 记录 status=timeout。
func TestTasksRun_TimeoutKill(t *testing.T) {
	srv, _ := newTestServerWithTasks(t)
	w := addTask(t, srv, map[string]any{"name": "超时任务", "cron": "*/5 * * * *", "command": "sleep 10", "timeout_sec": 1})
	if w.Code != 200 {
		t.Fatalf("add code=%d body=%s", w.Code, w.Body.String())
	}
	id := decodeJSON(t, w.Body.Bytes())["id"].(string)

	if w := doRequest(srv, "POST", "/api/tasks/"+id+"/run", nil); w.Code != 200 {
		t.Fatalf("run code=%d body=%s", w.Code, w.Body.String())
	}

	// 轮询等 runs 出现 timeout 记录（最多 5s）
	deadline := time.Now().Add(5 * time.Second)
	for {
		w = doRequest(srv, "GET", "/api/tasks/"+id+"/runs", nil)
		var runs []map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &runs)
		if len(runs) > 0 && runs[0]["status"] == "timeout" {
			if runs[0]["exit_code"] != float64(-1) {
				t.Errorf("timeout 应 exit_code=-1: %+v", runs[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("未在 5s 内出现 timeout 记录: %s", w.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ---------- 路由边界 ----------

// TestTasksRoute_Errors 非法方法 405 / 未知子路径 404 / 不存在 id 各操作 404。
func TestTasksRoute_Errors(t *testing.T) {
	srv, _ := newTestServerWithTasks(t)

	if w := doRequest(srv, "DELETE", "/api/tasks", nil); w.Code != 405 {
		t.Errorf("DELETE 根路径应 405, got=%d", w.Code)
	}
	if w := doRequest(srv, "GET", "/api/tasks/xyz", nil); w.Code != 405 {
		t.Errorf("GET 单任务应 405, got=%d", w.Code)
	}
	if w := doRequest(srv, "POST", "/api/tasks/nope/run", nil); w.Code != 404 {
		t.Errorf("run 不存在应 404, got=%d", w.Code)
	}
	if w := doRequest(srv, "POST", "/api/tasks/xyz/wat", nil); w.Code != 404 {
		t.Errorf("未知子路径应 404, got=%d", w.Code)
	}
	if w := doRequest(srv, "POST", "/api/tasks/xyz/toggle", nil); w.Code != 404 {
		t.Errorf("toggle 不存在应 404, got=%d", w.Code)
	}
	if w := doRequest(srv, "GET", "/api/tasks/xyz/runs", nil); w.Code != 404 {
		t.Errorf("runs 不存在应 404, got=%d", w.Code)
	}
}

// addTaskOrDie 新增一条默认任务，返回 200 响应体。
func addTaskOrDie(t *testing.T, srv *Server) []byte {
	t.Helper()
	w := addTask(t, srv, map[string]any{"name": "任务A", "cron": "*/5 * * * *", "command": "echo hello"})
	if w.Code != 200 {
		t.Fatalf("add task failed code=%d body=%s", w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}