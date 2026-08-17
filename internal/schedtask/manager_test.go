package schedtask

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "tasks.json"), filepath.Join(dir, "runs.json"))
	m, err := NewManager(st)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Stop)
	return m
}

// waitFinish 轮询等任务跑完（异步执行，最多 3 秒）。
func waitFinish(t *testing.T, m *Manager, id string) View {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		v, _ := m.Get(id)
		if !v.Running && v.LastStatus != "" && v.LastStatus != StatusRunning {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("任务未在 3s 内完成: %+v", v)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestManagerCRUD(t *testing.T) {
	m := newTestManager(t)

	added, err := m.Add(testTask())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.ID == "" || added.CreatedAt == "" {
		t.Fatalf("Add 应分配 ID / CreatedAt: %+v", added)
	}

	list := m.List()
	if len(list) != 1 || list[0].ID != added.ID {
		t.Fatalf("List 不符: %+v", list)
	}
	if list[0].NextRunAt == "" {
		t.Fatal("启用任务应有 next_run_at")
	}

	// Update 改 cron，保留运行态字段
	upd, err := m.Update(added.ID, Task{Name: "新名字", Cron: "0 1 * * *", Command: "echo x"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.Name != "新名字" || upd.Cron != "0 1 * * *" || upd.ID != added.ID {
		t.Fatalf("Update 结果不符: %+v", upd)
	}

	// Toggle
	toggled, err := m.Toggle(added.ID)
	if err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	if toggled.Enabled {
		t.Fatal("Toggle 后应为禁用")
	}
	v, _ := m.Get(added.ID)
	if v.NextRunAt != "" {
		t.Fatal("禁用后不应有 next_run_at")
	}

	// Delete 后 Get 不到
	if err := m.Delete(added.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := m.Get(added.ID); ok {
		t.Fatal("删除后 Get 应返回 false")
	}
	if err := m.Delete(added.ID); err == nil {
		t.Fatal("重复删除应报错")
	}
}

func TestManagerUpdateKeepsEnabled(t *testing.T) {
	m := newTestManager(t)
	added, _ := m.Add(testTask())
	if _, err := m.Toggle(added.ID); err != nil { // → disabled
		t.Fatal(err)
	}
	upd, err := m.Update(added.ID, Task{Name: "x", Cron: "* * * * *", Command: "true", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Enabled {
		t.Fatal("Update 不应改 enabled（PUT 契约：启停走 toggle）")
	}
}

func TestManagerRunNow(t *testing.T) {
	m := newTestManager(t)
	added, err := m.Add(testTask()) // echo hello
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RunNow(added.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	v := waitFinish(t, m, added.ID)
	if v.LastStatus != StatusSuccess {
		t.Fatalf("期望 success，得到 %+v", v)
	}
	if v.RunCount != 1 {
		t.Fatalf("RunCount = %d, 期望 1", v.RunCount)
	}
	runs, err := m.Runs(added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Trigger != "manual" || runs[0].Status != StatusSuccess {
		t.Fatalf("运行历史不符: %+v", runs)
	}
	if !strings.Contains(runs[0].Output, "hello") {
		t.Fatalf("输出应含 hello: %q", runs[0].Output)
	}
}

func TestManagerRunNowFailure(t *testing.T) {
	m := newTestManager(t)
	task := testTask()
	task.Command = "exit 3"
	added, err := m.Add(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RunNow(added.ID); err != nil {
		t.Fatal(err)
	}
	v := waitFinish(t, m, added.ID)
	if v.LastStatus != StatusFailed || v.LastError == "" {
		t.Fatalf("失败任务状态不符: %+v", v)
	}
	runs, _ := m.Runs(added.ID)
	if len(runs) != 1 || runs[0].ExitCode != 3 {
		t.Fatalf("历史退出码不符: %+v", runs)
	}
}

func TestManagerRunNowNotFound(t *testing.T) {
	m := newTestManager(t)
	if err := m.RunNow("no-such-id"); err == nil {
		t.Fatal("不存在 ID 应报错")
	}
}

// TestManagerRunNowBadWorkDir 启动失败（目录被删）时，历史应能看出原因。
// 保存时的 Validate 会拦截"目录本来就不存在"的路径，所以这里先保存合法任务，
// 再删掉目录模拟运行期目录丢失。
func TestManagerRunNowBadWorkDir(t *testing.T) {
	m := newTestManager(t)
	dir := t.TempDir()
	task := testTask()
	task.WorkDir = dir
	added, err := m.Add(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := m.RunNow(added.ID); err != nil {
		t.Fatal(err)
	}
	v := waitFinish(t, m, added.ID)
	if v.LastStatus != StatusFailed {
		t.Fatalf("期望 failed，得到 %+v", v)
	}
	if v.LastError == "" {
		t.Fatal("启动失败应记录 LastError")
	}
	runs, _ := m.Runs(added.ID)
	if len(runs) != 1 {
		t.Fatalf("应有 1 条历史: %+v", runs)
	}
	if !strings.Contains(runs[0].Output, "启动失败") {
		t.Fatalf("历史 output 应含启动失败原因: %q", runs[0].Output)
	}
	if runs[0].ExitCode != -1 {
		t.Fatalf("启动失败 exit code 应为 -1，得到 %d", runs[0].ExitCode)
	}
}

func TestManagerFireRunsDueTask(t *testing.T) {
	m := newTestManager(t)
	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.Local)
	now := base
	m.now = func() time.Time { return now }

	task := testTask()
	task.Cron = "* * * * * *" // 每秒
	added, err := m.Add(task)
	if err != nil {
		t.Fatal(err)
	}
	// 槽在 10:00:01；推进到该时刻并手动触发 fire（模拟 timer 到点）
	now = base.Add(time.Second)
	m.fire()

	v := waitFinish(t, m, added.ID)
	if v.RunCount < 1 {
		t.Fatal("fire 后任务未执行")
	}
	runs, _ := m.Runs(added.ID)
	if len(runs) == 0 || runs[0].Trigger != "cron" {
		t.Fatalf("trigger 应为 cron: %+v", runs)
	}
}

// TestManagerFireNoRefire 回归：5 段 cron 触发后重算下一槽必须跨到下一个
// 命中分钟，不能在同一个匹配分钟内逐秒连发（曾实测每分钟连发 60 次）。
func TestManagerFireNoRefire(t *testing.T) {
	m := newTestManager(t)
	base := time.Date(2026, 8, 16, 10, 4, 59, 500000000, time.Local)
	now := base
	m.now = func() time.Time { return now }

	task := testTask() // */5 * * * * → 槽在 10:05:00
	added, err := m.Add(task)
	if err != nil {
		t.Fatal(err)
	}
	// 到点触发一次
	now = time.Date(2026, 8, 16, 10, 5, 0, 100000000, time.Local)
	m.fire()
	waitFinish(t, m, added.ID)

	// 触发后 Rebuild 的下一槽必须是 10:10:00，不能还在 10:05 这一分钟里
	now = time.Date(2026, 8, 16, 10, 5, 0, 200000000, time.Local)
	v, _ := m.Get(added.ID)
	if v.NextRunAt == "" {
		t.Fatal("触发后应有下次运行时间")
	}
	next, err := time.ParseInLocation(time.RFC3339, v.NextRunAt, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 16, 10, 10, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Fatalf("触发后下一槽 = %s，期望 %s（不能在同一分钟内连发）", next, want)
	}

	// 同一分钟内反复 fire 不应再跑
	for sec := 1; sec <= 30; sec++ {
		now = time.Date(2026, 8, 16, 10, 5, sec, 0, time.Local)
		m.fire()
	}
	v, _ = m.Get(added.ID)
	if v.RunCount != 1 {
		t.Fatalf("同一分钟内重复 fire 不应重复执行，RunCount = %d（期望 1）", v.RunCount)
	}
}

func TestManagerFireSkipsStaleSlot(t *testing.T) {
	m := newTestManager(t)
	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.Local)
	now := base
	m.now = func() time.Time { return now }

	task := testTask()
	task.Cron = "* * * * * *"
	if _, err := m.Add(task); err != nil {
		t.Fatal(err)
	}
	// 推进到容差窗口之外（休眠唤醒场景）→ 不补跑
	now = base.Add(catchUpWindow + time.Minute)
	m.fire()

	runs, _ := m.Runs(task.ID)
	if len(runs) != 0 {
		t.Fatalf("超出容差窗口不应补跑: %+v", runs)
	}
}

func TestManagerOverlapSkipped(t *testing.T) {
	m := newTestManager(t)
	// 先手动启动一个长跑任务占住 running 位
	slow := testTask()
	slow.Command = "sleep 2"
	slowAdded, err := m.Add(slow)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RunNow(slowAdded.ID); err != nil {
		t.Fatal(err)
	}
	// 运行中再次手动执行 → ErrRunning
	if err := m.RunNow(slowAdded.ID); err != ErrRunning {
		t.Fatalf("运行中再次执行应返回 ErrRunning，得到 %v", err)
	}
	// 运行中调度触发 → 记 skipped 历史
	m.startRun(slowAdded, "cron")
	runs, _ := m.Runs(slowAdded.ID)
	if len(runs) != 1 || runs[0].Status != StatusSkipped {
		t.Fatalf("重叠调度应记 skipped: %+v", runs)
	}
}

func TestManagerReloadResetsRunningState(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "tasks.json"), filepath.Join(dir, "runs.json"))
	m1, err := NewManager(st)
	if err != nil {
		t.Fatal(err)
	}
	added, _ := m1.Add(testTask())

	// 模拟"上次退出时任务还在跑"：直接改内存状态并落盘
	m1.mu.Lock()
	m1.items[added.ID].LastStatus = StatusRunning
	m1.mu.Unlock()
	if err := m1.persistTasks(); err != nil {
		t.Fatal(err)
	}
	m1.Stop()

	m2, err := NewManager(st)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Stop()
	v, _ := m2.Get(added.ID)
	if v.LastStatus == StatusRunning {
		t.Fatal("重启后 running 状态应被重置")
	}
	if v.LastStatus != StatusFailed || !strings.Contains(v.LastError, "中断") {
		t.Fatalf("重启后应标记失败+中断: %+v", v)
	}
}
