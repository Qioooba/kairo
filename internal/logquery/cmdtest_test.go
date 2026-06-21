package logquery

import (
	"strings"
	"testing"
)

func TestListCommand(t *testing.T) {
	c, err := ListCommand("/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1",
		[]string{"SystemOut*.log", "SystemErr*.log", "*.log"}, 100)
	if err != nil {
		t.Fatal("list:", err)
	}
	if !strings.Contains(c, "find . -maxdepth 1 -type f") {
		t.Fatalf("缺少 find 子句: %s", c)
	}
	if !strings.Contains(c, "SystemOut*.log") {
		t.Fatalf("缺少 SystemOut 模式: %s", c)
	}
	if !strings.Contains(c, "head -n 100") {
		t.Fatalf("缺少 head: %s", c)
	}
}

func TestSearchAnd(t *testing.T) {
	kw, err := ParseQuery("Exception && userinfo")
	if err != nil {
		t.Fatal(err)
	}
	c, err := SearchCommand("/dir", []string{"SystemOut.log"}, kw, 200, 30, "utf-8")
	if err != nil {
		t.Fatal(err)
	}
	// AND 应该是两个 grep 串联（不是 | 合并到一个 pattern）
	if !strings.Contains(c, "grep -nE") {
		t.Fatalf("缺第一个 grep: %s", c)
	}
	if !strings.Contains(c, "grep -E") {
		t.Fatalf("AND 应该用第二个 grep 串联，而不是 | 合并: %s", c)
	}
	if !strings.Contains(c, "head -n 200") {
		t.Fatalf("缺 head: %s", c)
	}
	// 绝不能出现 "Exception|userinfo" 形式（这是 OR）
	if strings.Contains(c, "\"Exception|userinfo\"") || strings.Contains(c, "'Exception|userinfo'") {
		t.Fatalf("AND 不应该用 | 合并 term: %s", c)
	}
}

func TestSearchOr(t *testing.T) {
	kw, _ := ParseQuery("Exception || Timeout")
	c, _ := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8")
	if !strings.Contains(c, "grep -nE") {
		t.Fatalf("OR 缺 grep: %s", c)
	}
	// OR 应该用 sort -u 合并多段
	if !strings.Contains(c, "sort -u") {
		t.Fatalf("OR 应该用 sort -u 合并多段: %s", c)
	}
}

func TestSearchNot(t *testing.T) {
	kw, err := ParseQuery("!DEBUG")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8")
	// 纯 NOT 也走 `grep -nE "^." -- file...`，保留 `file:lineno:` 前缀，
	// 这样前端解析多文件命中才不至于错位。改成 cat 会丢掉前缀。
	if !strings.Contains(c, `grep -nE "^." --`) {
		t.Fatalf("纯 NOT 应保留 grep -nE 前缀: %s", c)
	}
	if !strings.Contains(c, "grep -vE") || !strings.Contains(c, "DEBUG") {
		t.Fatalf("缺 grep -v: %s", c)
	}
}

func TestSearchAndNot(t *testing.T) {
	kw, err := ParseQuery("Exception && !DEBUG")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8")
	if !strings.Contains(c, "Exception") {
		t.Fatalf("AND 部分缺失: %s", c)
	}
	if !strings.Contains(c, "grep -vE") {
		t.Fatalf("NOT 部分缺失: %s", c)
	}
}

func TestSearchInjection(t *testing.T) {
	cases := []string{
		"Exception; cat /etc/passwd",
		"$(rm -rf /)",
		"`whoami`",
	}
	for _, c := range cases {
		if _, err := ParseQuery(c); err == nil {
			t.Fatalf("应该拒绝 %q 但没拒绝", c)
		}
	}
	// "A && B" 是合法表达式
	if _, err := ParseQuery("A && B"); err != nil {
		t.Fatalf("合法表达式被拒: %v", err)
	}
	// - 开头的伪选项也应拒
	if _, err := ParseQuery("Exception && --help"); err == nil {
		t.Fatal("应拒 - 开头 term")
	}
}

func TestListInjection(t *testing.T) {
	bad1, err := ListCommand("/dir; rm -rf /", []string{"*.log"}, 100)
	if err == nil {
		t.Fatalf("INJECT1 未拦截: %s", bad1)
	}
	bad2, err := ListCommand("/dir", []string{"*.log; echo pwned"}, 100)
	if err == nil {
		t.Fatalf("INJECT2 未拦截: %s", bad2)
	}
}

func TestContextCommand(t *testing.T) {
	c, err := ContextCommand("/dir", "SystemOut.log", 12030, 30, 30, 30)
	if err != nil {
		t.Fatal(err)
	}
	// 行号用双引号包，shell 拼接更安全（行号万一被注入也不至于命令拆分）。
	// sed 对 `sed -n "a,bp"` 和 `sed -n a,bp` 都支持。
	if !strings.Contains(c, `sed -n "12000,12060p"`) {
		t.Fatalf("sed 行号不对: %s", c)
	}
}

func TestParseListOutput(t *testing.T) {
	in := "1234\t1700000000.5\t./SystemOut.log\n" +
		"999\t1699990000.0\t./SystemErr.log\n"
	files, err := ParseListOutput(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("期望 2 条, 得到 %d", len(files))
	}
	if files[0].Name != "SystemOut.log" {
		t.Fatalf("倒序失败: %s", files[0].Name)
	}
}

func TestNoTimeoutInCommand(t *testing.T) {
	// 服务器端 `timeout` 命令不存在时（精简镜像）也能跑
	c, err := ListCommand("/dir", []string{"*.log"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c, "timeout") {
		t.Fatalf("命令不应该依赖 Linux timeout: %s", c)
	}
	kw, _ := ParseQuery("Exception")
	c, _ = SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8")
	if strings.Contains(c, "timeout") {
		t.Fatalf("搜索命令不应该依赖 Linux timeout: %s", c)
	}
	c, _ = ContextCommand("/dir", "a.log", 10, 5, 5, 30)
	if strings.Contains(c, "timeout") {
		t.Fatalf("上下文命令不应该依赖 Linux timeout: %s", c)
	}
}

func TestSearchGBK(t *testing.T) {
	// 验证 GBK 编码下中文关键词被转成 printf 字节流，整条命令的 shell 结构不动。
	esc, err := ToEncodingEscaped("信贷系统", "gbk")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(esc, "\\xd0\\xc5") {
		t.Fatalf("GBK 编码 信贷 应以 d0 c5 开头，得到: %s", esc)
	}
	// 把中文 term 编进 search 命令，结构里不应出现 \\xd0 这种转义之外的 raw GBK 字节
	kw, err := ParseQuery("信贷系统")
	if err != nil {
		t.Fatal(err)
	}
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "gbk")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "printf %b") {
		t.Fatalf("GBK 搜索应使用 printf %%b 转义，实际: %s", c)
	}
	if !strings.Contains(c, "grep -nE") {
		t.Fatalf("应保留 grep -nE 结构，实际: %s", c)
	}
	// && || ! 仍然存在，未被转码吃掉
}

func TestSearchUTF8(t *testing.T) {
	kw, _ := ParseQuery("Exception && 信贷系统")
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8")
	if err != nil {
		t.Fatal(err)
	}
	// UTF-8 走 %q，不走 printf
	if strings.Contains(c, "printf %b") {
		t.Fatalf("UTF-8 不应使用 printf %%b: %s", c)
	}
	if !strings.Contains(c, "信贷系统") {
		t.Fatalf("UTF-8 关键词应原样在命令里: %s", c)
	}
}

func TestTailCommand(t *testing.T) {
	c, err := TailCommand("/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1", "SystemOut.log", 100)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "tail -n 100 -F") {
		t.Fatalf("应包含 tail -n 100 -F: %s", c)
	}
	if !strings.Contains(c, "SystemOut.log") {
		t.Fatalf("应包含文件名: %s", c)
	}
	if !strings.Contains(c, "2>/dev/null") {
		t.Fatalf("stderr 应丢弃: %s", c)
	}
}

func TestTailCommand_LinesClamp(t *testing.T) {
	c, err := TailCommand("/dir", "a.log", 99999)
	if err != nil {
		t.Fatal(err)
	}
	// 大于 1000 会被压回 1000
	if !strings.Contains(c, "tail -n 1000 -F") {
		t.Fatalf("lines > 1000 应压回 1000: %s", c)
	}
}

func TestTailCommand_RejectsEmptyFile(t *testing.T) {
	if _, err := TailCommand("/dir", "", 10); err == nil {
		t.Fatal("空 file 应报错")
	}
}

func TestTailCommand_RejectsInjection(t *testing.T) {
	if _, err := TailCommand("/dir; rm -rf /", "a.log", 10); err == nil {
		t.Fatal("dir 注入应被拒")
	}
	if _, err := TailCommand("/dir", "a.log; cat /etc/passwd", 10); err == nil {
		t.Fatal("file 注入应被拒")
	}
}
