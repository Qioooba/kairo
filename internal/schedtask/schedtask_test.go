package schedtask

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testTask() Task {
	return Task{
		Name:    "测试任务",
		Enabled: true,
		Cron:    "*/5 * * * *",
		Command: "echo hello",
	}
}

func writeFileRaw(p, content string) error {
	return os.WriteFile(p, []byte(content), 0o600)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ---- Validate ----

func TestValidateOK(t *testing.T) {
	task := testTask()
	if err := task.Validate(); err != nil {
		t.Fatalf("合法任务校验失败: %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Task)
		want   string
	}{
		{"空名称", func(tk *Task) { tk.Name = " " }, "名称不能为空"},
		{"空命令", func(tk *Task) { tk.Command = "" }, "命令不能为空"},
		{"空cron", func(tk *Task) { tk.Cron = "" }, "cron"},
		{"坏cron", func(tk *Task) { tk.Cron = "not a cron" }, "不合法"},
		{"超时下限", func(tk *Task) { tk.TimeoutSec = -1 }, "超时"},
		{"超时上限", func(tk *Task) { tk.TimeoutSec = MaxTimeoutSec + 1 }, "超时"},
		{"坏目录", func(tk *Task) { tk.WorkDir = "/no/such/dir/exists-xyz" }, "工作目录"},
		{"长命令", func(tk *Task) { tk.Command = strings.Repeat("a", maxCommandLen+1) }, "不能超过"},
		{"长名称", func(tk *Task) { tk.Name = strings.Repeat("名", maxNameLen+1) }, "不能超过"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			task := testTask()
			c.mutate(&task)
			err := task.Validate()
			if err == nil {
				t.Fatalf("期望报错（含 %q），实际通过", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误信息 %q 不含期望子串 %q", err.Error(), c.want)
			}
		})
	}
}

func TestValidateDescriptorAndSixFields(t *testing.T) {
	for _, expr := range []string{"@hourly", "@daily", "0 9 * * 1-5", "*/30 * * * * *"} {
		task := testTask()
		task.Cron = expr
		if err := task.Validate(); err != nil {
			t.Fatalf("cron %q 应合法: %v", expr, err)
		}
	}
}

// ---- NextRun ----

func TestNextRunDisabled(t *testing.T) {
	task := testTask()
	task.Enabled = false
	if got := task.NextRun(time.Now()); !got.IsZero() {
		t.Fatalf("禁用任务不应有下次运行时间，得到 %v", got)
	}
}

func TestNextRunEvery5Min(t *testing.T) {
	task := testTask() // */5 * * * *
	now := time.Date(2026, 8, 16, 10, 3, 30, 0, time.Local)
	got := task.NextRun(now)
	want := time.Date(2026, 8, 16, 10, 5, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("NextRun = %v, 期望 %v", got, want)
	}
}

func TestNextRunDaily(t *testing.T) {
	task := testTask()
	task.Cron = "0 9 * * *"
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.Local) // 今天 9 点已过
	got := task.NextRun(now)
	want := time.Date(2026, 8, 17, 9, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("NextRun = %v, 期望 %v", got, want)
	}
}

func TestNextRunBadCron(t *testing.T) {
	task := testTask()
	task.Cron = "垃圾"
	if got := task.NextRun(time.Now()); !got.IsZero() {
		t.Fatalf("坏 cron 应返回零值，得到 %v", got)
	}
}

// ---- Store ----

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "tasks.json"), filepath.Join(dir, "runs.json"))

	tasks := []Task{testTask()}
	tasks[0].ID = "abc"
	if err := st.SaveTasks(tasks); err != nil {
		t.Fatalf("SaveTasks: %v", err)
	}
	got, err := st.LoadTasks()
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	if len(got) != 1 || got[0].ID != "abc" || got[0].Cron != "*/5 * * * *" {
		t.Fatalf("LoadTasks 结果不符: %+v", got)
	}

	runs := map[string][]RunRecord{
		"abc": {{TaskID: "abc", Status: StatusSuccess, ExitCode: 0, Output: "ok"}},
	}
	if err := st.SaveRuns(runs); err != nil {
		t.Fatalf("SaveRuns: %v", err)
	}
	gotRuns, err := st.LoadRuns()
	if err != nil {
		t.Fatalf("LoadRuns: %v", err)
	}
	if len(gotRuns["abc"]) != 1 || gotRuns["abc"][0].Output != "ok" {
		t.Fatalf("LoadRuns 结果不符: %+v", gotRuns)
	}
}

func TestStoreLoadMissing(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "none.json"), filepath.Join(dir, "none2.json"))
	tasks, err := st.LoadTasks()
	if err != nil || len(tasks) != 0 {
		t.Fatalf("缺失文件应返回空列表: tasks=%v err=%v", tasks, err)
	}
	runs, err := st.LoadRuns()
	if err != nil || len(runs) != 0 {
		t.Fatalf("缺失文件应返回空表: runs=%v err=%v", runs, err)
	}
}

func TestStoreCorruptBackup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tasks.json")
	st := NewStore(p, filepath.Join(dir, "runs.json"))
	if err := st.EnsurePath(); err != nil {
		t.Fatal(err)
	}
	if err := writeFileRaw(p, "{not json"); err != nil {
		t.Fatal(err)
	}
	tasks, err := st.LoadTasks()
	if err == nil {
		t.Fatal("损坏 JSON 应返回错误")
	}
	if len(tasks) != 0 {
		t.Fatalf("损坏 JSON 应返回空列表: %v", tasks)
	}
	if !fileExists(p + ".bak") {
		t.Fatal("损坏文件应备份到 .bak")
	}
}
