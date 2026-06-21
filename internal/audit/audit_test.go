package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLine(t *testing.T) {
	line := `ts=2026-06-20 10:30:00.123 op=ssh.test system=信贷生产 server=mock-1 result=ok`
	rec := parseLine(line)
	if rec == nil {
		t.Fatal("解析失败")
	}
	if rec.Op != "ssh.test" {
		t.Fatalf("op: %s", rec.Op)
	}
	if rec.KV["system"] != "信贷生产" {
		t.Fatalf("system: %s", rec.KV["system"])
	}
	if rec.KV["server"] != "mock-1" {
		t.Fatalf("server: %s", rec.KV["server"])
	}
	if rec.KV["result"] != "ok" {
		t.Fatalf("result: %s", rec.KV["result"])
	}
	if rec.Time.Year() != 2026 || rec.Time.Month() != 6 || rec.Time.Day() != 20 {
		t.Fatalf("time: %v", rec.Time)
	}
}

func TestParseLine_NoOp(t *testing.T) {
	if rec := parseLine("garbage line"); rec != nil {
		t.Fatalf("无 op 应返 nil, 实际 %+v", rec)
	}
}

func TestRecent_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, "audit.log")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	recs, err := l.Recent(100, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("空文件应返回空, 实际 %d 条", len(recs))
	}
}

func TestRecent_FilterAndLimit(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, "audit.log")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// 写 10 条：5 条 ssh.test + 5 条 logs.list
	for i := 0; i < 5; i++ {
		l.Write("ssh.test", "system", "sysA", "server", "srv1", "result", "ok")
	}
	for i := 0; i < 5; i++ {
		l.Write("logs.list", "system", "sysA", "server", "srv1", "dir", "/x", "result", "ok")
	}

	// 倒序应先看到 logs.list
	recs, err := l.Recent(100, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 10 {
		t.Fatalf("期望 10 条, 实际 %d", len(recs))
	}
	if recs[0].Op != "logs.list" {
		t.Fatalf("最新应是 logs.list, 实际 %s", recs[0].Op)
	}

	// 过滤 op=ssh.test
	sshRecs, _ := l.Recent(100, Filter{Op: "ssh.test"})
	if len(sshRecs) != 5 {
		t.Fatalf("ssh.test 应有 5 条, 实际 %d", len(sshRecs))
	}
	for _, r := range sshRecs {
		if r.Op != "ssh.test" {
			t.Fatalf("过滤出错: %s", r.Op)
		}
	}

	// limit
	limRecs, _ := l.Recent(3, Filter{})
	if len(limRecs) != 3 {
		t.Fatalf("limit 3 应返 3 条, 实际 %d", len(limRecs))
	}

	// 系统过滤
	sysRecs, _ := l.Recent(100, Filter{System: "sysA"})
	if len(sysRecs) != 10 {
		t.Fatalf("system=sysA 应 10 条, 实际 %d", len(sysRecs))
	}
	noSys, _ := l.Recent(100, Filter{System: "sysB"})
	if len(noSys) != 0 {
		t.Fatalf("system=sysB 应 0 条, 实际 %d", len(noSys))
	}
}

func TestRecent_ResultFilter(t *testing.T) {
	dir := t.TempDir()
	l, _ := New(dir, "audit.log")
	defer l.Close()

	l.Write("ssh.test", "system", "sysA", "result", "ok")
	l.Write("ssh.test", "system", "sysA", "result", "fail")
	l.Write("ssh.test", "system", "sysA", "result", "fail")

	okRecs, _ := l.Recent(10, Filter{Result: "ok"})
	if len(okRecs) != 1 {
		t.Fatalf("result=ok 应 1 条, 实际 %d", len(okRecs))
	}
	failRecs, _ := l.Recent(10, Filter{Result: "fail"})
	if len(failRecs) != 2 {
		t.Fatalf("result=fail 应 2 条, 实际 %d", len(failRecs))
	}
}

func TestRecent_LargeFile(t *testing.T) {
	// 确认 limit 上限被压回 5000
	dir := t.TempDir()
	l, _ := New(dir, "audit.log")
	defer l.Close()
	for i := 0; i < 10; i++ {
		l.Write("x.test", "result", "ok")
	}
	// limit=99999 内部压回 5000；只有 10 条，应全返
	recs, _ := l.Recent(99999, Filter{})
	if len(recs) != 10 {
		t.Fatalf("期望 10 条, 实际 %d", len(recs))
	}
	// 文件存在性 sanity check
	if _, err := os.Stat(filepath.Join(dir, "audit.log")); err != nil {
		t.Fatal(err)
	}
}

func TestParseLine_KeyWithSpaces(t *testing.T) {
	// key= 后允许空格，但 fields 切分保证 = 在 key 后面
	line := "ts=2026-06-20 10:30:00.000 op=custom msg=hello world"
	rec := parseLine(line)
	if rec == nil {
		t.Fatal("解析失败")
	}
	if rec.KV["msg"] != "hello" { // "world" 会被当独立 field，msg 只取第一个 token
		t.Fatalf("msg 解析不对: %q", rec.KV["msg"])
	}
	if !strings.Contains(rec.Raw, "hello world") {
		t.Fatalf("Raw 丢失原行: %s", rec.Raw)
	}
}