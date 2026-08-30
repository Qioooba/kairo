package schedtask

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const schedtaskHelperEnv = "KAIRO_SCHEDTASK_TEST_HELPER"

// TestSchedtaskHelperProcess 由本测试二进制的子进程执行，用真实进程层级验证
// shell -> helper -> grandchild 的整树终止。父测试进程不会进入 helper 分支。
func TestSchedtaskHelperProcess(t *testing.T) {
	if os.Getenv(schedtaskHelperEnv) != "1" {
		return
	}
	sep := -1
	for i, arg := range os.Args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || sep+1 >= len(os.Args) {
		os.Exit(90)
	}
	args := os.Args[sep+1:]
	switch args[0] {
	case "wait":
		time.Sleep(30 * time.Second)
	case "cwd":
		wd, err := os.Getwd()
		if err != nil || len(args) < 2 || os.WriteFile(args[1], []byte(wd), 0o600) != nil {
			os.Exit(91)
		}
	case "spawn-marker":
		if len(args) < 2 {
			os.Exit(92)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestSchedtaskHelperProcess$", "--", "write-marker", args[1])
		cmd.Env = os.Environ()
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			os.Exit(93)
		}
	case "write-marker":
		if len(args) < 2 {
			os.Exit(94)
		}
		time.Sleep(2 * time.Second)
		if err := os.WriteFile(args[1], []byte("escaped"), 0o600); err != nil {
			os.Exit(95)
		}
	default:
		os.Exit(96)
	}
	os.Exit(0)
}

func shellQuoteForTest(s string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return `'` + strings.ReplaceAll(s, `'`, `'"'"'`) + `'`
}

func helperCommand(mode string, args ...string) string {
	parts := []string{
		shellQuoteForTest(os.Args[0]),
		"-test.run=^TestSchedtaskHelperProcess$",
		"--",
		mode,
	}
	for _, arg := range args {
		parts = append(parts, shellQuoteForTest(arg))
	}
	return strings.Join(parts, " ")
}

func TestManagerUsesConfiguredDefaultWorkDir(t *testing.T) {
	t.Setenv(schedtaskHelperEnv, "1")
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "cwd.txt")
	store := NewStore(filepath.Join(t.TempDir(), "tasks.json"), filepath.Join(t.TempDir(), "runs.json"))
	m, err := NewManagerWithOptions(store, ManagerOptions{DefaultWorkDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	task := testTask()
	task.Command = helperCommand("cwd", marker)
	added, err := m.Add(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RunNow(added.ID); err != nil {
		t.Fatal(err)
	}
	v := waitFinish(t, m, added.ID)
	if v.LastStatus != StatusSuccess {
		t.Fatalf("任务失败: %+v", v)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	got := filepath.Clean(string(raw))
	want := filepath.Clean(root)
	if runtime.GOOS == "windows" {
		if !strings.EqualFold(got, want) {
			t.Fatalf("默认工作目录=%q，期望 %q", got, want)
		}
	} else if got != want {
		t.Fatalf("默认工作目录=%q，期望 %q", got, want)
	}
}

func TestDeleteRejectsRunningTask(t *testing.T) {
	t.Setenv(schedtaskHelperEnv, "1")
	m := newTestManager(t)
	task := testTask()
	task.Command = helperCommand("wait")
	task.TimeoutSec = 30
	added, err := m.Add(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RunNow(added.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(added.ID); !errors.Is(err, ErrRunning) {
		t.Fatalf("删除运行中任务应返回 ErrRunning，得到 %v", err)
	}
	if _, ok := m.Get(added.ID); !ok {
		t.Fatal("删除被拒后任务不应消失")
	}
}

func TestConcurrentRunNowHasSingleWinner(t *testing.T) {
	t.Setenv(schedtaskHelperEnv, "1")
	m := newTestManager(t)
	task := testTask()
	task.Command = helperCommand("wait")
	task.TimeoutSec = 30
	added, err := m.Add(task)
	if err != nil {
		t.Fatal(err)
	}

	const callers = 16
	start := make(chan struct{})
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- m.RunNow(added.ID)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners, rejected := 0, 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrRunning):
			rejected++
		default:
			t.Fatalf("意外错误: %v", err)
		}
	}
	if winners != 1 || rejected != callers-1 {
		t.Fatalf("并发立即执行结果 winners=%d rejected=%d", winners, rejected)
	}
}

func TestTimeoutKillsDescendantProcess(t *testing.T) {
	t.Setenv(schedtaskHelperEnv, "1")
	m := newTestManager(t)
	marker := filepath.Join(t.TempDir(), "escaped.txt")
	task := testTask()
	task.Command = helperCommand("spawn-marker", marker)
	task.TimeoutSec = 1
	added, err := m.Add(task)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := m.RunNow(added.ID); err != nil {
		t.Fatal(err)
	}
	v := waitFinish(t, m, added.ID)
	if v.LastStatus != StatusTimeout {
		t.Fatalf("期望 timeout，得到 %+v", v)
	}
	// marker 子孙进程在启动后 2 秒写文件；等到该时刻以后仍不存在才证明整树被杀。
	remaining := 2500*time.Millisecond - time.Since(started)
	if remaining > 0 {
		time.Sleep(remaining)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("超时后孙进程仍写出了文件，进程树未被终止: %v", err)
	}
}

func TestStopCancelsAndWaitsForRunningTask(t *testing.T) {
	t.Setenv(schedtaskHelperEnv, "1")
	dir := t.TempDir()
	m, err := NewManager(NewStore(filepath.Join(dir, "tasks.json"), filepath.Join(dir, "runs.json")))
	if err != nil {
		t.Fatal(err)
	}
	task := testTask()
	task.Command = helperCommand("wait")
	task.TimeoutSec = 30
	added, err := m.Add(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RunNow(added.ID); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	m.Stop()
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("Stop 取消任务耗时过长: %s", elapsed)
	}
	v, ok := m.Get(added.ID)
	if !ok || v.Running || v.LastStatus != StatusCanceled {
		t.Fatalf("Stop 后状态不正确: %+v ok=%v", v, ok)
	}
	if err := m.RunNow(added.ID); err == nil {
		t.Fatal("Stop 后不应再允许启动任务")
	}
}

func TestHelperCommandContainsCurrentTestBinary(t *testing.T) {
	// 小型纯函数回归，避免 Windows 路径含空格时 helper 命令丢失引号。
	cmd := helperCommand("cwd", "a b")
	if !strings.Contains(cmd, filepath.Base(os.Args[0])) || !strings.Contains(cmd, strconv.Quote("a b")[1:4]) {
		t.Fatalf("helper 命令构造异常: %s", cmd)
	}
}
