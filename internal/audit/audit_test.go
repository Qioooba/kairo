package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestWriteAndParseJSONL 验证：写一条 audit 记录，文件是合法 JSONL。
// 写完用 parseLine 读回来能拿到对应字段（项 15）。
func TestWriteAndParseJSONL(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, "audit.log")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	l.Write("logs.search", "system", "信贷生产", "server", "mock-1", "query", "Exception", "result", "ok")

	// 读文件原文应是单行 JSON
	b, err := os.ReadFile(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.TrimSpace(string(b))
	if !strings.HasPrefix(raw, "{") {
		t.Fatalf("应为 JSONL 行，实际: %s", raw)
	}
	if strings.Contains(raw, "ts=") || strings.Contains(raw, "op= ") {
		t.Fatalf("不应是 K=V 格式: %s", raw)
	}

	// parseLine 应能读回
	rec := parseLine(raw)
	if rec == nil {
		t.Fatalf("parseLine 失败: %s", raw)
	}
	if rec.Op != "logs.search" {
		t.Errorf("op 不对: %s", rec.Op)
	}
	if rec.KV["system"] != "信贷生产" {
		t.Errorf("system: %s", rec.KV["system"])
	}
	if rec.KV["query"] != "Exception" {
		t.Errorf("query: %s", rec.KV["query"])
	}
	if rec.KV["result"] != "ok" {
		t.Errorf("result: %s", rec.KV["result"])
	}
	// ts 应该是合法 RFC3339
	if rec.Time.IsZero() {
		t.Errorf("ts 未解析: %s", rec.KV["ts"])
	}
}

// TestParseLine_LegacyKV 验证：老 K=V 格式仍能解析（向后兼容）。
func TestParseLine_LegacyKV(t *testing.T) {
	// 显式以非 { 开头 → 走 K=V 解析
	old := "ts=2026-06-20 10:30:00.123 op=old.op system=信贷生产 result=ok"
	rec := parseLine(old)
	if rec == nil {
		t.Fatal("老格式解析失败")
	}
	if rec.Op != "old.op" {
		t.Errorf("op: %s", rec.Op)
	}
	if rec.KV["system"] != "信贷生产" {
		t.Errorf("system: %s", rec.KV["system"])
	}
}

// TestParseLine_SpacesInJSONValue 验证：JSONL 格式支持值里有空格。
func TestParseLine_SpacesInJSONValue(t *testing.T) {
	line := `{"ts":"2026-06-23T12:00:00.000+08:00","op":"x","msg":"hello world"}`
	rec := parseLine(line)
	if rec == nil {
		t.Fatal("JSON 解析失败")
	}
	if rec.KV["msg"] != "hello world" {
		t.Fatalf("msg 应含空格: %q", rec.KV["msg"])
	}
}

// TestWriteCSV_Basic 基本导出：3 条记录，所有字段都能在 CSV 里找到。
func TestWriteCSV_Basic(t *testing.T) {
	dir := t.TempDir()
	l, _ := New(dir, "audit.log")
	defer l.Close()

	l.Write("ssh.test", "system", "sysA", "server", "srv1", "result", "ok")
	l.Write("logs.list", "system", "sysA", "server", "srv1", "dir", "/opt/logs", "result", "fail", "err", "permission denied")
	l.Write("logs.search", "system", "sysB", "server", "srv2", "query", "Exception", "result", "ok", "hits", "5")

	recs, err := l.Recent(100, Filter{})
	if err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	if err := WriteCSV(&buf, recs); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// header 至少包含 ts/op/system/server/result 这 5 列
	firstLine := strings.SplitN(out, "\n", 2)[0]
	for _, col := range []string{"ts", "op", "system", "server", "result"} {
		if !strings.Contains(firstLine, col) {
			t.Errorf("header 缺少 %s: %s", col, firstLine)
		}
	}
	// 第二条记录有 dir / err，CSV 应包含这些列
	if !strings.Contains(firstLine, "dir") || !strings.Contains(firstLine, "err") {
		t.Errorf("header 应包含 dir/err 动态列: %s", firstLine)
	}
	// 关键操作名都在
	for _, op := range []string{"ssh.test", "logs.list", "logs.search"} {
		if !strings.Contains(out, op) {
			t.Errorf("CSV 应包含 %s", op)
		}
	}
}

// TestWriteCSV_Empty 空记录 → 只有 header。
func TestWriteCSV_Empty(t *testing.T) {
	var buf strings.Builder
	if err := WriteCSV(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "ts") {
		t.Errorf("空记录也应有 header: %s", buf.String())
	}
	// header 行末是 \r\n，数据行也是 \r\n（csv.NewWriter 默认行为）
	if !strings.Contains(buf.String(), "\r\n") {
		t.Errorf("CSV 应使用 CRLF 行尾: %q", buf.String())
	}
}

// TestWriteCSV_SortedAscByTs 验证导出按 ts 升序（Recent 返的是倒序）。
func TestWriteCSV_SortedAscByTs(t *testing.T) {
	dir := t.TempDir()
	l, _ := New(dir, "audit.log")
	defer l.Close()

	// 写 3 条，间隔 1ms 让 ts 有差异
	l.Write("first", "system", "s")
	time.Sleep(2 * time.Millisecond)
	l.Write("second", "system", "s")
	time.Sleep(2 * time.Millisecond)
	l.Write("third", "system", "s")

	recs, _ := l.Recent(10, Filter{})
	if len(recs) != 3 {
		t.Fatalf("期望 3 条，实际 %d", len(recs))
	}
	if recs[0].Op != "third" {
		t.Fatalf("Recent 应倒序：%v", recs)
	}

	var buf strings.Builder
	if err := WriteCSV(&buf, recs); err != nil {
		t.Fatal(err)
	}
	// 拿数据行（跳 header）
	lines := strings.Split(strings.TrimRight(buf.String(), "\r\n"), "\r\n")
	// lines[0] 是 header，1/2/3 是数据
	if len(lines) < 4 {
		t.Fatalf("行数不对: %d", len(lines))
	}
	if !strings.Contains(lines[1], "first") {
		t.Errorf("CSV 第一行应是 first: %s", lines[1])
	}
	if !strings.Contains(lines[3], "third") {
		t.Errorf("CSV 最后一行应是 third: %s", lines[3])
	}
}

// TestWriteCSV_EscapesCommasAndQuotes 验证含逗号 / 引号 / 换行的字段会被 csv 库自动转义。
func TestWriteCSV_EscapesCommasAndQuotes(t *testing.T) {
	dir := t.TempDir()
	l, _ := New(dir, "audit.log")
	defer l.Close()

	// err 含逗号 + 引号
	l.Write("logs.search", "system", "sysA", "server", "srv1", "query", `q="x",y`, "result", "fail", "err", `boom, said "user"`)

	recs, _ := l.Recent(10, Filter{})
	var buf strings.Builder
	if err := WriteCSV(&buf, recs); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// encoding/csv 默认把 " 转义为 ""
	// 所以 `boom, said "user"` 在 CSV 里应该是 `boom, said ""user""`
	if !strings.Contains(out, `""user""`) {
		t.Errorf("CSV 未正确转义引号: %s", out)
	}
}

// TestCSVFilename 验证文件名格式。
func TestCSVFilename(t *testing.T) {
	tt := time.Date(2026, 6, 23, 2, 45, 0, 0, time.UTC)
	if got := CSVFilename(tt); got != "audit-2026-06-23-024500.csv" {
		t.Errorf("CSVFilename() = %q", got)
	}
}
