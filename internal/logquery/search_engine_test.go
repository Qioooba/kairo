package logquery

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func runSearchEngine(t *testing.T, dir string, files []string, query string, max int, ignoreCase bool) string {
	t.Helper()
	kw, err := ParseQuery(query)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", query, err)
	}
	cmd, err := SearchCommand(dir, files, kw, max, 30, "utf-8", ignoreCase)
	if err != nil {
		t.Fatalf("SearchCommand: %v", err)
	}
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("执行搜索失败: %v\ncmd=%s\nout=%s", err, cmd, out)
	}
	return string(out)
}

func outputLineNumbers(t *testing.T, out string) []int {
	t.Helper()
	var got []int
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		_, sep, n, _, ok := ParseSearchHitLine(line)
		if !ok || sep != ':' {
			t.Fatalf("无法解析搜索输出 %q", line)
		}
		got = append(got, n)
	}
	return got
}

func requireLines(t *testing.T, out string, want ...int) {
	t.Helper()
	got := outputLineNumbers(t, out)
	if len(got) != len(want) {
		t.Fatalf("命中行数不符: got=%v want=%v\nout=%s", got, want, out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("命中行不符: got=%v want=%v\nout=%s", got, want, out)
		}
	}
}

func TestSearchEngine_BooleanSemanticsUseContentOnly(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "B.log", []string{
		"A only",
		"A B both",
		"A TRACE",
		"B only",
		"plain",
	})

	// 后续关键词不能命中文件名 B.log 或行号前缀。
	requireLines(t, runSearchEngine(t, dir, []string{"B.log"}, "A && B", 200, false), 2)
	requireLines(t, runSearchEngine(t, dir, []string{"B.log"}, "A && 1", 200, false))
	// NOT 只检查正文，不能因为文件名含 B 把所有 A 行排除。
	requireLines(t, runSearchEngine(t, dir, []string{"B.log"}, "A && !B", 200, false), 3, 1)
	// OR 结果按最新行在前，并跨分支去重。
	requireLines(t, runSearchEngine(t, dir, []string{"B.log"}, "A || B", 200, false), 4, 3, 2, 1)
}

func TestSearchEngine_NoPrefilterFalseNegative(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 1001)
	for i := 0; i < 1000; i++ {
		lines[i] = "A common candidate"
	}
	lines[1000] = "A B valid at the end"
	writeFixture(t, dir, "late.log", lines)

	// 旧实现会在前 50 个 A 后停止，永远看不到第 1001 行。
	requireLines(t, runSearchEngine(t, dir, []string{"late.log"}, "A && B", 20, false), 1001)
}

func TestSearchEngine_PureNegativeScansWholeFileAndKeepsLatest(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 10250)
	for i := 0; i < 10000; i++ {
		lines[i] = "DEBUG filtered"
	}
	for i := 10000; i < len(lines); i++ {
		lines[i] = "keep " + strconv.Itoa(i+1)
	}
	writeFixture(t, dir, "negative.log", lines)

	got := outputLineNumbers(t, runSearchEngine(t, dir, []string{"negative.log"}, "!DEBUG", 200, false))
	if len(got) != 200 || got[0] != 10250 || got[len(got)-1] != 10051 {
		t.Fatalf("应保留最后 200 条有效行并倒序输出，got first=%v last=%v len=%d", got[0], got[len(got)-1], len(got))
	}
}

func TestSearchEngine_ShellMetacharactersAreLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "literal.log", []string{
		"`touch>PWNED`",
		"$USER",
		"$(id)",
		"O'Brien",
		"path/to/file",
		"--help",
		"a<>b [INFO] {key} C:\\temp",
	})

	cases := []struct {
		query string
		line  int
	}{
		{"`touch>PWNED`", 1}, {"$USER", 2}, {"$(id)", 3}, {"O'Brien", 4},
		{"path/to/file", 5}, {"--help", 6}, {"a<>b", 7}, {"[INFO]", 7}, {`C:\temp`, 7},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			requireLines(t, runSearchEngine(t, dir, []string{"literal.log"}, tc.query, 20, false), tc.line)
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "PWNED")); !os.IsNotExist(err) {
		t.Fatalf("反引号关键词被 shell 执行，PWNED 状态: %v", err)
	}
}

func TestSearchEngine_IgnoreCaseAndLiteralRegexChars(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "case.log", []string{
		"Exception com.example+svc^",
		"EXCEPTION comXexampleeeeeesvc",
	})
	requireLines(t, runSearchEngine(t, dir, []string{"case.log"}, "exception && com.example+svc^", 20, true), 1)
}

func TestSearchEngine_WindowReturnsEveryRepeatedTermLine(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "window.log", []string{
		"A first",
		"A second",
		"neutral",
		"B fourth",
	})
	// 旧实现只返回 A@2/B@4；A@1 被“最后一次出现”覆盖。
	requireLines(t, runWindowCmd(t, dir, []string{"window.log"}, "A && B", 5, 200, false), 4, 2, 1)
}

func TestSearchEngine_GBKExecutesLiteralSearch(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "gbk.log"))
	if err != nil {
		t.Fatal(err)
	}
	w := transform.NewWriter(f, simplifiedchinese.GBK.NewEncoder())
	_, writeErr := w.Write([]byte("普通行\n信贷系统 交易异常\n"))
	closeErr := w.Close()
	fileErr := f.Close()
	if writeErr != nil || closeErr != nil || fileErr != nil {
		t.Fatalf("写 GBK fixture: write=%v transformClose=%v fileClose=%v", writeErr, closeErr, fileErr)
	}
	kw, err := ParseQuery("信贷系统")
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := SearchCommand(dir, []string{"gbk.log"}, kw, 20, 30, "gbk", false)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("GBK 搜索失败: %v out=%q", err, out)
	}
	requireLines(t, string(out), 2)
}

func TestSearchEngine_LargeData500K(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过 50 万行压力测试")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "large.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	for i := 1; i <= 500000; i++ {
		line := "INFO request completed id=" + strconv.Itoa(i)
		switch i {
		case 499995:
			line = "LARGE_A first"
		case 499997:
			line = "LARGE_A second"
		case 499999:
			line = "LARGE_B window partner"
		case 500000:
			line = "LARGE_A LARGE_B newest same-line match"
		}
		if _, err := bw.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	requireLines(t, runSearchEngine(t, dir, []string{"large.log"}, "LARGE_A && LARGE_B", 50, false), 500000)
	requireLines(t, runWindowCmd(t, dir, []string{"large.log"}, "LARGE_A && LARGE_B", 5, 50, false), 500000, 499999, 499997, 499995)
}
