package logquery

import (
	"strings"
	"testing"
)

func TestListCommand(t *testing.T) {
	c, err := ListCommand("/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1",
		[]string{"SystemOut*.log", "SystemErr*.log", "*.log"}, 100, "gnu_find")
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

func TestListCommand_POSIX_LS(t *testing.T) {
	c, err := ListCommand("/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1",
		[]string{"SystemOut*.log", "SystemErr*.log"}, 50, "posix_ls")
	if err != nil {
		t.Fatal("list posix_ls:", err)
	}
	// 应该用 ls -lt 而不是 find -printf
	if !strings.Contains(c, "ls -lt") {
		t.Fatalf("posix_ls 模式应该用 ls -lt: %s", c)
	}
	if strings.Contains(c, "find . -maxdepth") {
		t.Fatalf("posix_ls 模式不应该用 find: %s", c)
	}
	if !strings.Contains(c, "grep -E") {
		t.Fatalf("posix_ls 模式应该用 grep -E 过滤 pattern: %s", c)
	}
}

func TestListCommand_UnknownListMode(t *testing.T) {
	_, err := ListCommand("/dir", []string{"*.log"}, 10, "totally_made_up_mode")
	if err == nil {
		t.Fatal("未知 list_mode 应该报错")
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
	// AND 应该是两个 grep 串联（不是 | 合并到一个 pattern）。
	// v6 修复：第一个 grep 必须用 -HnE（单文件也能输出 filename:line: 前缀）。
	if !strings.Contains(c, "grep -HnE") {
		t.Fatalf("缺第一个 grep -HnE: %s", c)
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
	if !strings.Contains(c, "grep -HnE") {
		t.Fatalf("OR 缺 grep -HnE: %s", c)
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
	// 纯 NOT 也走 `grep -HnE "^." -- file...`，保留 `file:lineno:` 前缀，
	// 这样前端解析多文件命中才不至于错位。改成 cat 会丢掉前缀。
	if !strings.Contains(c, `grep -HnE "^." --`) {
		t.Fatalf("纯 NOT 应保留 grep -HnE 前缀: %s", c)
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
	bad1, err := ListCommand("/dir; rm -rf /", []string{"*.log"}, 100, "gnu_find")
	if err == nil {
		t.Fatalf("INJECT1 未拦截: %s", bad1)
	}
	bad2, err := ListCommand("/dir", []string{"*.log; echo pwned"}, 100, "gnu_find")
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
	c, err := ListCommand("/dir", []string{"*.log"}, 10, "gnu_find")
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
	if !strings.Contains(c, "grep -HnE") {
		t.Fatalf("应保留 grep -HnE 结构，实际: %s", c)
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

// ---------- v6 修复：grep -H 单文件 filename 前缀 ----------

func TestSearchCommand_SingleFile_Uses_GrepH(t *testing.T) {
	// v6 修复：单文件搜索必须用 `grep -HnE`，否则 grep 输出 lineno:content
	// （不带 filename），parseSearchOutput 解析失败整行被丢。
	kw, _ := ParseQuery("Exception")
	c, _ := SearchCommand("/dir", []string{"SystemOut.log"}, kw, 200, 30, "utf-8")
	if !strings.Contains(c, "grep -HnE") {
		t.Fatalf("单文件搜索应使用 grep -HnE（带 -H 输出 filename）: %s", c)
	}
	if strings.Contains(c, "grep -nE") && !strings.Contains(c, "grep -HnE") {
		t.Fatalf("单文件搜索不能用裸 grep -nE（会丢 filename）: %s", c)
	}
}

func TestSearchCommand_NotOnly_Uses_GrepH(t *testing.T) {
	// 纯 NOT 也必须保留 filename:lineno: 前缀。
	kw, _ := ParseQuery("!DEBUG")
	c, _ := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8")
	if !strings.Contains(c, "grep -HnE") {
		t.Fatalf("纯 NOT 搜索必须用 grep -HnE 保留前缀: %s", c)
	}
}

// ---------- v6 修复：context 路径穿越防护 ----------

func TestContextCommand_RejectsPathTraversal(t *testing.T) {
	cases := []struct {
		file string
		why  string
	}{
		{"../etc/passwd", "父目录引用"},
		{"..", "两个点"},
		{".", "单个点"},
		{"subdir/a.log", "子目录路径"},
		{`back\slash.log`, "反斜杠"},
		{"foo..bar.log", "文件名含两个点"},
		{"/abs/path.log", "绝对路径"},
	}
	for _, tc := range cases {
		if _, err := ContextCommand("/dir", tc.file, 1, 1, 1, 30); err == nil {
			t.Errorf("file=%q (%s) 应被拒，但通过了", tc.file, tc.why)
		}
	}
}

func TestTailCommand_RejectsPathTraversal(t *testing.T) {
	cases := []struct {
		file string
		why  string
	}{
		{"../etc/passwd", "父目录引用"},
		{"subdir/a.log", "子目录路径"},
		{"/abs/path.log", "绝对路径"},
		{"foo..bar.log", "文件名含 .."},
	}
	for _, tc := range cases {
		if _, err := TailCommand("/dir", tc.file, 10); err == nil {
			t.Errorf("file=%q (%s) 应被 TailCommand 拒，但通过了", tc.file, tc.why)
		}
	}
}

// ---------- v6 修复：POSIX ls 输出解析 ----------

func TestParseListOutputPOSIX_Basic(t *testing.T) {
	in := "total 24\n" +
		"-rw-r--r-- 1 wasuser wasgrp 12345 Jun 21 10:00 SystemOut.log\n" +
		"-rw-r--r-- 1 wasuser wasgrp 67890 Jun 21 09:30 SystemOut_20260619.log\n"
	files, err := ParseListOutputPOSIX(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("期望 2 条，得到 %d", len(files))
	}
	if files[0].Name != "SystemOut.log" {
		t.Errorf("名字错: %s", files[0].Name)
	}
	if files[0].Size != 12345 {
		t.Errorf("size 错: %d", files[0].Size)
	}
}

func TestParseListOutput_AutoDetectsPOSIX(t *testing.T) {
	// 输入像 ls -lt 的输出时，ParseListOutput 应自动走 POSIX 路径。
	in := "total 4\n-rw-r--r-- 1 user grp 100 Jun 21 10:00 a.log\n"
	files, err := ParseListOutput(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "a.log" {
		t.Errorf("auto-detect POSIX 失败: %+v", files)
	}
	if files[0].Size != 100 {
		t.Errorf("size 解析错: %d", files[0].Size)
	}
}

// ---------- v0.4 修复：ParseQuery 状态机校验 ----------

func TestParseQuery_RejectsTrailingAndOr(t *testing.T) {
	// v0.4 修复：尾随的 && / || 必须报错，不能默默接受。
	cases := []string{
		"A &&",
		"A ||",
		"!DEBUG &&",
		"A && B ||",
		"!A || !B &&",
	}
	for _, q := range cases {
		if _, err := ParseQuery(q); err == nil {
			t.Errorf("q=%q 应被拒（尾随操作符），但 ParseQuery 没报错", q)
		}
	}
}

func TestParseQuery_RejectsConsecutiveOps(t *testing.T) {
	// v0.4 修复：连续操作符必须报错。
	cases := []string{
		"A && && B",
		"A && || B",
		"A || && B",
		"A || || B",
	}
	for _, q := range cases {
		if _, err := ParseQuery(q); err == nil {
			t.Errorf("q=%q 应被拒（连续操作符），但 ParseQuery 没报错", q)
		}
	}
}

func TestParseQuery_RejectsLeadingOps(t *testing.T) {
	// && / || 开头必须报错。
	cases := []string{
		"&& B",
		"|| B",
		"&&",
		"||",
	}
	for _, q := range cases {
		if _, err := ParseQuery(q); err == nil {
			t.Errorf("q=%q 应被拒（开头操作符 / 空），但 ParseQuery 没报错", q)
		}
	}
}

func TestParseQuery_AcceptsNegationForms(t *testing.T) {
	// 这些必须被接受（之前会被接受，回归测试确保修复没误伤合法用法）。
	cases := []string{
		"!DEBUG",
		"Exception && !DEBUG",
		"!A || !B",
		"Exception && Timeout && !DEBUG",
	}
	for _, q := range cases {
		if _, err := ParseQuery(q); err != nil {
			t.Errorf("q=%q 合法，但被 ParseQuery 拒了: %v", q, err)
		}
	}
}

func TestParseQuery_NegateOnlyRejected(t *testing.T) {
	// 只剩 "!" / "! !" 必须拒。
	cases := []string{"!", "!  !"}
	for _, q := range cases {
		if _, err := ParseQuery(q); err == nil {
			t.Errorf("q=%q 应被拒（只有 !），但 ParseQuery 没报错", q)
		}
	}
}

// ---------- v0.4 修复：SearchCommand OR 段含纯 neg ----------

func TestSearchCommand_OR_IncludesFirstBranch(t *testing.T) {
	// "A || B" 的命令结构应包含：
	//   1. 第一段（grep A）在管道头部；
	//   2. sort -u 用于合并去重；
	//   3. head -n max 截断在最后；
	//   4. 整体是 4 段管道（grep A | (grep B) | sort -u | head）。
	kw, err := ParseQuery("Exception || Timeout")
	if err != nil {
		t.Fatal(err)
	}
	c, err := SearchCommand("/dir", []string{"a.log", "b.log"}, kw, 200, 30, "utf-8")
	if err != nil {
		t.Fatal(err)
	}
	// 第一个分支（grep A）必须在 head 之前的管道里出现
	if !strings.Contains(c, "grep -HnE") {
		t.Fatalf("缺第一段 grep -HnE: %s", c)
	}
	// sort -u 必须在场
	if !strings.Contains(c, "sort -u") {
		t.Fatalf("OR 必须用 sort -u 去重: %s", c)
	}
	// 管道结构：第一段 + 第二段括号 + sort -u + head -n
	// 通过 LC_ALL=C sort -u + head -n 顺序验证
	idxSort := strings.Index(c, "sort -u")
	idxHead := strings.Index(c, "head -n")
	if idxSort < 0 || idxHead < 0 || idxSort > idxHead {
		t.Fatalf("sort -u 必须在 head -n 之前: sort=%d head=%d\ncmd=%s", idxSort, idxHead, c)
	}
	// head -n 200 必须存在
	if !strings.Contains(c, "head -n 200") {
		t.Fatalf("缺 head -n 200: %s", c)
	}
}

func TestSearchCommand_OR_WithNegation(t *testing.T) {
	// "A || !B" 必须：
	//   1. 第一段（grep A）保留；
	//   2. 第二段是纯 neg：先 `grep -HnE "^." -- files` 拿全部行，再 grep -vE B；
	//   3. sort -u 仍然在最后（去重两条分支的输出）。
	kw, err := ParseQuery("Exception || !DEBUG")
	if err != nil {
		t.Fatal(err)
	}
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "Exception") {
		t.Fatalf("第一段缺 Exception: %s", c)
	}
	if !strings.Contains(c, "grep -vE") {
		t.Fatalf("纯 neg 段缺 grep -vE: %s", c)
	}
	if !strings.Contains(c, "DEBUG") {
		t.Fatalf("neg 段缺 DEBUG 关键字: %s", c)
	}
	// 必须有 sort -u 把两段合并去重
	if !strings.Contains(c, "sort -u") {
		t.Fatalf("OR + NEG 必须用 sort -u 合并: %s", c)
	}
	// 纯 neg 段必须用 grep -HnE "^." 而不是 cat（保留 filename:lineno: 前缀）
	// 验证：第二段（括号内）必须含 grep -HnE "^." 然后 grep -vE DEBUG
	if !strings.Contains(c, `grep -HnE "^."`) {
		t.Fatalf("纯 neg 段应用 grep -HnE \"^.\" 保留前缀: %s", c)
	}
}

func TestSearchCommand_OR_PureNegOnly(t *testing.T) {
	// "!A || !B" 必须：
	//   1. 第一段是 `grep -HnE "^." -- files | grep -vE A`；
	//   2. 第二段是 `grep -HnE "^." -- files | grep -vE B`；
	//   3. sort -u 合并。
	kw, err := ParseQuery("!A || !B")
	if err != nil {
		t.Fatal(err)
	}
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "sort -u") {
		t.Fatalf("OR 必须有 sort -u: %s", c)
	}
	// 第一段必须保留 grep -HnE "^." 前缀
	if !strings.Contains(c, `grep -HnE "^." --`) {
		t.Fatalf("第一段必须保留 grep -HnE \"^.\" 前缀: %s", c)
	}
	// 两个 grep -vE 都必须存在
	if got := strings.Count(c, "grep -vE"); got < 2 {
		t.Fatalf("!A || !B 应有 ≥ 2 个 grep -vE, 实际 %d\ncmd=%s", got, c)
	}
}
