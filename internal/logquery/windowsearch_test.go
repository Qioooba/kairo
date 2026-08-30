package logquery

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// runWindowCmd 在本地真实执行 WindowSearchCommand 生成的命令（sh + awk），
// 返回命令 stdout。命令本身失败（非 grep 无匹配）时 t.Fatal。
func runWindowCmd(t *testing.T, dir string, files []string, query string, window, max int, ignoreCase bool) string {
	t.Helper()
	kw, err := ParseQuery(query)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", query, err)
	}
	cmd, err := WindowSearchCommand(dir, files, kw, max, 30, "utf-8", ignoreCase, window)
	if err != nil {
		t.Fatalf("WindowSearchCommand: %v", err)
	}
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("本地执行失败: %v\ncmd=%s\nout=%s", err, cmd, out)
	}
	return string(out)
}

// hitKeys 把输出解析成 "file:lineno" 集合（升序便于比较）。
func hitKeys(t *testing.T, out string) []string {
	t.Helper()
	var keys []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		file, sep, ln, _, ok := ParseSearchHitLine(line)
		if !ok || sep != ':' {
			t.Fatalf("输出行无法解析: %q", line)
		}
		keys = append(keys, file+":"+itoa(ln))
	}
	sort.Strings(keys)
	return keys
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func writeFixture(t *testing.T, dir, name string, lines []string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWindowSearch_WithinWindow(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.log", []string{
		"line1", "line2",
		"Exception boom", // 3
		"line4", "line5", "line6", "line7",
		"userinfo xxx", // 8
		"line9", "line10",
		"Exception again", // 11（8 与 11 差 3 也成窗口，但 3 已发射）
		"line12",
		"Exception lone", // 13（离最近 userinfo@8 差 5，窗口内）
		"line14", "line15", "line16", "line17", "line18", "line19", "line20",
		"Exception far", // 21（离 userinfo@8 差 13 > 10，不应命中）
	})
	got := hitKeys(t, runWindowCmd(t, dir, []string{"a.log"}, "Exception && userinfo", 10, 200, false))
	want := []string{"a.log:3", "a.log:8", "a.log:11", "a.log:13"}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("命中不符:\n got=%v\nwant=%v", got, want)
	}
}

func TestWindowSearch_OutsideWindow(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.log", []string{
		"Exception here", // 1
		"l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9", "l10", "l11",
		"userinfo there", // 12：与 1 差 11 > 10
	})
	got := hitKeys(t, runWindowCmd(t, dir, []string{"a.log"}, "Exception && userinfo", 10, 200, false))
	if len(got) != 0 {
		t.Fatalf("窗口外不应命中: %v", got)
	}
}

func TestWindowSearch_SameLineDedup(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.log", []string{
		"l1",
		"Exception userinfo same line", // 2
		"l3",
	})
	got := hitKeys(t, runWindowCmd(t, dir, []string{"a.log"}, "Exception && userinfo", 10, 200, false))
	want := []string{"a.log:2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("同行命中应只输出一次: %v", got)
	}
}

func TestWindowSearch_ThreeTermsSpan(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.log", []string{
		"A at 1",
		"l2", "l3", "l4",
		"B at 5",
		"l6",
		"C at 7",
		"l8", "l9", "l10", "l11", "l12", "l13", "l14", "l15",
		"C far at 16",
		"l17",
	})
	// A@1 B@5 C@7 跨度 6 <= 10 → 命中 1,5,7；C@16 与 A@1 跨度 15 > 10 不构成窗口
	got := hitKeys(t, runWindowCmd(t, dir, []string{"a.log"}, "A && B && C", 10, 200, false))
	want := []string{"a.log:1", "a.log:5", "a.log:7"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("三词窗口命中不符:\n got=%v\nwant=%v", got, want)
	}
}

func TestWindowSearch_OrUnion(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.log", []string{
		"l1", "l2",
		"Alpha here", // 3
		"l4",
		"Beta there", // 5
		"l6",
		"Gamma only", // 7（|| 段单独成立）
	})
	got := hitKeys(t, runWindowCmd(t, dir, []string{"a.log"}, "Alpha && Beta || Gamma", 10, 200, false))
	want := []string{"a.log:3", "a.log:5", "a.log:7"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("OR 合并不符:\n got=%v\nwant=%v", got, want)
	}
}

func TestWindowSearch_NegExcludesLine(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.log", []string{
		"l1",
		"Exception ok",    // 2 → 命中
		"DEBUG Exception", // 3 → 含 DEBUG，排除
		"l4",
		"Exception another", // 5 → 命中
	})
	got := hitKeys(t, runWindowCmd(t, dir, []string{"a.log"}, "Exception && !DEBUG", 10, 200, false))
	want := []string{"a.log:2", "a.log:5"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("取反排除不符:\n got=%v\nwant=%v", got, want)
	}
}

func TestWindowSearch_PureNeg(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.log", []string{
		"keep 1",
		"DEBUG noise",
		"keep 3",
	})
	got := hitKeys(t, runWindowCmd(t, dir, []string{"a.log"}, "!DEBUG", 10, 200, false))
	want := []string{"a.log:1", "a.log:3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("纯取反不符:\n got=%v\nwant=%v", got, want)
	}
}

func TestWindowSearch_IgnoreCase(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.log", []string{
		"l1",
		"EXCEPTION x", // 2
		"l3",
		"USERINFO y", // 4
	})
	got := hitKeys(t, runWindowCmd(t, dir, []string{"a.log"}, "exception && userinfo", 10, 200, true))
	want := []string{"a.log:2", "a.log:4"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("忽略大小写不符:\n got=%v\nwant=%v", got, want)
	}
}

func TestWindowSearch_BackslashLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.log", []string{
		"l1",
		`path is C:\temp\dir`, // 2
		"l3",
		"normal line", // 4（不匹配）
	})
	got := hitKeys(t, runWindowCmd(t, dir, []string{"a.log"}, `C:\temp`, 10, 200, false))
	want := []string{"a.log:2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("反斜杠字面匹配不符:\n got=%v\nwant=%v", got, want)
	}
}

func TestWindowSearch_ChineseUTF8(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.log", []string{
		"l1",
		"信贷系统 交易失败", // 2
		"l3",
		"异常 处理超时", // 4
	})
	got := hitKeys(t, runWindowCmd(t, dir, []string{"a.log"}, "信贷系统 && 处理超时", 10, 200, false))
	want := []string{"a.log:2", "a.log:4"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("中文 UTF-8 不符:\n got=%v\nwant=%v", got, want)
	}
}

func TestWindowSearch_TotalLimit(t *testing.T) {
	dir := t.TempDir()
	lines := []string{}
	for i := 0; i < 100; i++ {
		lines = append(lines, "AAA hit "+itoa(i))
	}
	writeFixture(t, dir, "a.log", lines)
	out := runWindowCmd(t, dir, []string{"a.log"}, "AAA", 10, 20, false)
	got := strings.Split(strings.TrimSpace(out), "\n")
	if len(got) > 20 {
		t.Fatalf("总命中应被 head -n 20 截断，实际 %d 行", len(got))
	}
}

func TestWindowSearch_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.log", []string{"l1", "X here", "l3", "Y there"})
	writeFixture(t, dir, "b.log", []string{"l1", "X b1", "l3", "l4", "Y b2"})
	got := hitKeys(t, runWindowCmd(t, dir, []string{"a.log", "b.log"}, "X && Y", 10, 200, false))
	want := []string{"a.log:2", "a.log:4", "b.log:2", "b.log:5"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("多文件不符:\n got=%v\nwant=%v", got, want)
	}
}

func TestWindowSearch_CommandStructure(t *testing.T) {
	kw, err := ParseQuery("Exception && userinfo")
	if err != nil {
		t.Fatal(err)
	}
	c, err := WindowSearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LC_ALL=C awk",
		"KP_1_1=$(printf %b",
		"KP_1_2=$(printf %b",
		"-v w=10",
		"ENVIRON",
		"function finish",
		"head -n 200",
		"sh -c",
	} {
		if !strings.Contains(c, want) {
			t.Fatalf("命令缺少 %q:\n%s", want, c)
		}
	}
	if strings.Contains(c, "grep") {
		t.Fatalf("窗口模式不应使用 grep:\n%s", c)
	}
}

func TestWindowSearch_GBKCommandStructure(t *testing.T) {
	kw, err := ParseQuery("信贷系统 && 异常")
	if err != nil {
		t.Fatal(err)
	}
	c, err := WindowSearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "gbk", false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "KP_1_1=$(printf %b") || !strings.Contains(c, `\xd0`) {
		t.Fatalf("GBK 关键词应转成 \\xHH 字节转义:\n%s", c)
	}
	if strings.Contains(c, "信贷系统") {
		t.Fatalf("GBK 模式下命令不应直接出现 UTF-8 原文:\n%s", c)
	}
}

func TestWindowSearch_WindowClamp(t *testing.T) {
	kw, err := ParseQuery("A")
	if err != nil {
		t.Fatal(err)
	}
	c, err := WindowSearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false, 999)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "-v w=50") {
		t.Fatalf("window 应被钳到 50:\n%s", c)
	}
	c, err = WindowSearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "-v w=10") {
		t.Fatalf("window<=0 应回退默认 10:\n%s", c)
	}
}

func TestWindowSearch_RejectsInjection(t *testing.T) {
	if _, err := WindowSearchCommand("/dir'; rm -rf /", []string{"a.log"}, mustKW(t, "A"), 200, 30, "utf-8", false, 10); err == nil {
		t.Fatal("dir 注入应被拒")
	}
	if _, err := WindowSearchCommand("/dir", []string{"a.log; rm -rf /"}, mustKW(t, "A"), 200, 30, "utf-8", false, 10); err == nil {
		t.Fatal("file 注入应被拒")
	}
	if _, err := WindowSearchCommand("", []string{"a.log"}, mustKW(t, "A"), 200, 30, "utf-8", false, 10); err == nil {
		t.Fatal("空 dir 应被拒")
	}
	if _, err := WindowSearchCommand("/dir", nil, mustKW(t, "A"), 200, 30, "utf-8", false, 10); err == nil {
		t.Fatal("空 files 应被拒")
	}
}

func mustKW(t *testing.T, q string) []SearchKeyword {
	t.Helper()
	kw, err := ParseQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	return kw
}

// TestContextLinesForHitsCommand_RunsLocally REVIEW-v0.15：曾经把 awk 脚本的
// 换行压成空格导致 "split(...) for (...)" 语法错误（exit 2 被上层静默吞掉），
// 上下文行永远补不上。这里本地真实跑一遍命令 + 解析，锁死行为。
func TestContextLinesForHitsCommand_RunsLocally(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.log", []string{
		"l1", "l2",
		"hit A", // 3
		"l4", "l5",
		"hit B", // 6
		"l7", "l8", "l9",
	})
	cmd, err := ContextLinesForHitsCommand(dir, "a.log", []int{3, 6}, 1)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("本地执行失败: %v\ncmd=%s\nout=%s", err, cmd, out)
	}
	hits := ParseContextEnrichedOutput(string(out), "srv", dir, nil)
	var hitLines, ctxLines []int
	for _, h := range hits {
		if h.IsContext {
			ctxLines = append(ctxLines, h.LineNo)
		} else {
			hitLines = append(hitLines, h.LineNo)
		}
	}
	sort.Ints(hitLines)
	sort.Ints(ctxLines)
	if strings.Join(itoas(hitLines), ",") != "3,6" {
		t.Fatalf("命中行不符: %v", hitLines)
	}
	if strings.Join(itoas(ctxLines), ",") != "2,4,5,7" {
		t.Fatalf("上下文行不符: %v", ctxLines)
	}
}

func itoas(ns []int) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, itoa(n))
	}
	return out
}
