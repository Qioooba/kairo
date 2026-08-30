package logquery

import (
	"fmt"
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
	c, err := SearchCommand("/dir", []string{"SystemOut.log"}, kw, 200, 30, "utf-8", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"LC_ALL=C awk", `-v ng=1`, `-v npos="2"`, `-v w=0`, "KP_1_1=$(printf %b", "KP_1_2=$(printf %b", "head -n 200"} {
		if !strings.Contains(c, want) {
			t.Fatalf("同行 AND 命令缺少 %q: %s", want, c)
		}
	}
	if strings.Contains(c, "grep") {
		t.Fatalf("统一搜索引擎不应再拼 grep 管道: %s", c)
	}
}

func TestSearchOr(t *testing.T) {
	kw, _ := ParseQuery("Exception || Timeout")
	c, _ := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false)
	if !strings.Contains(c, `-v ng=2`) || !strings.Contains(c, `-v npos="1,1"`) {
		t.Fatalf("OR 分组未传入统一执行器: %s", c)
	}
}

func TestSearchNot(t *testing.T) {
	kw, err := ParseQuery("!DEBUG")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false)
	if !strings.Contains(c, `-v npos="0"`) || !strings.Contains(c, `-v nneg="1"`) || !strings.Contains(c, "KN_1_1=$(printf %b") {
		t.Fatalf("纯 NOT 未编码进统一执行器: %s", c)
	}
}

func TestSearchAndNot(t *testing.T) {
	kw, err := ParseQuery("Exception && !DEBUG")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false)
	if !strings.Contains(c, "KP_1_1=$(printf %b") || !strings.Contains(c, "KN_1_1=$(printf %b") {
		t.Fatalf("AND/NOT 关键词未安全编码: %s", c)
	}
}

// 关键词全部先编码为 \xHH 字节再放进 awk 环境变量，可打印字符不会参与
// shell/awk 语法。只拒绝文本协议不能稳定承载的控制字符。
func TestSearchInjection(t *testing.T) {
	rejectedCases := []string{
		"a\u0000b", "a\nb", "a\rb", "a\tb",
	}
	for _, c := range rejectedCases {
		if _, err := ParseQuery(c); err == nil {
			t.Fatalf("应该拒绝 %q 但没拒绝", c)
		}
	}

	acceptedCases := []struct {
		in        string
		wantValue string
	}{
		{"Exception at (Foo.java:123)", "(Foo.java:123)"},
		{"a && b", "a"}, {"a || b", "a"}, {"!DEBUG", "DEBUG"},
		{"x*y", "x*y"}, {"x?y", "x?y"}, {"x|y", "x|y"},
		{"x&y", "x&y"}, {"x;y", "x;y"}, {"x~y", "x~y"},
		{"`whoami`", "`whoami`"}, {"$USER", "$USER"}, {"a\\b", "a\\b"},
		{`a"b`, `a"b`}, {"[INFO]", "[INFO]"}, {"{key}", "{key}"},
		{"path/to/file", "path/to/file"}, {"O'Brien", "O'Brien"},
		{"--help", "--help"},
		{"com.example+svc^", "com.example+svc^"}, // 已存在的 . + ^ 用例
	}
	for _, c := range acceptedCases {
		kw, err := ParseQuery(c.in)
		if err != nil {
			t.Fatalf("应该接受 %q 但被拒: %v", c.in, err)
		}
		// 在所有 term 里找 value 等于 wantValue 的，断言特殊字符没被静默删
		found := false
		for _, k := range kw {
			if k.Op == "term" && k.Value == c.wantValue {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("输入 %q：期望能找到 term %q（特殊字符没被删），实际 ParseQuery 结果: %+v", c.in, c.wantValue, kw)
		}
	}

	// "A && B" 合法表达式
	if _, err := ParseQuery("A && B"); err != nil {
		t.Fatalf("合法表达式被拒: %v", err)
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
	c, _ = SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false)
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
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "gbk", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "printf %b") {
		t.Fatalf("GBK 搜索应使用 printf %%b 转义，实际: %s", c)
	}
	if !strings.Contains(c, "LC_ALL=C awk") {
		t.Fatalf("应使用统一 awk 搜索引擎，实际: %s", c)
	}
	// && || ! 仍然存在，未被转码吃掉
}

func TestSearchUTF8(t *testing.T) {
	kw, _ := ParseQuery("Exception && 信贷系统")
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "printf %b") || strings.Contains(c, "信贷系统") {
		t.Fatalf("UTF-8 关键词应安全编码且不暴露原文: %s", c)
	}
}

func TestSearchCommand_TermsAreRegexEscapedLiterals(t *testing.T) {
	kw, err := ParseQuery("com.example+svc^")
	if err != nil {
		t.Fatal(err)
	}
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, `\x63\x6f\x6d\x2e`) || strings.Contains(c, "com.example+svc^") {
		t.Fatalf("关键词应按字节编码并由 index() 字面匹配: %s", c)
	}
}

func TestSearchCommand_QuotesFileWithSpacesInsideShC(t *testing.T) {
	kw, err := ParseQuery("Exception")
	if err != nil {
		t.Fatal(err)
	}
	c, err := SearchCommand("/dir with space", []string{"System Out.log"}, kw, 200, 30, "utf-8", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, `'\''System Out.log'\''`) {
		t.Fatalf("带空格文件名必须在内层 sh 脚本里保持引用: %s", c)
	}
	if !strings.Contains(c, `cd '\''/dir with space'\''`) {
		t.Fatalf("带空格目录必须在内层 sh 脚本里保持引用: %s", c)
	}
}

func TestSearchCommand_GBKPrintfQuotedInsideShC(t *testing.T) {
	kw, err := ParseQuery("信贷系统")
	if err != nil {
		t.Fatal(err)
	}
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "gbk", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, `$(printf %b '\''\x`) {
		t.Fatalf("GBK printf 字节串必须在外层 sh -c 中正确转义: %s", c)
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

// ---------- 统一执行器显式输出 filename 前缀 ----------

func TestSearchCommand_SingleFile_EmitsFilename(t *testing.T) {
	kw, _ := ParseQuery("Exception")
	c, _ := SearchCommand("/dir", []string{"SystemOut.log"}, kw, 200, 30, "utf-8", false)
	if !strings.Contains(c, `-v fname='\''SystemOut.log'\''`) || !strings.Contains(c, `printf "%s:%d:%s\n"`) {
		t.Fatalf("单文件搜索必须显式输出 filename:line:content: %s", c)
	}
}

func TestSearchCommand_NotOnly_EmitsFilename(t *testing.T) {
	kw, _ := ParseQuery("!DEBUG")
	c, _ := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false)
	if !strings.Contains(c, `-v fname='\''a.log'\''`) || !strings.Contains(c, "KN_1_1=$(printf %b") {
		t.Fatalf("纯 NOT 搜索必须保留文件名并安全传入负关键词: %s", c)
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
	kw, err := ParseQuery("Exception || Timeout")
	if err != nil {
		t.Fatal(err)
	}
	c, err := SearchCommand("/dir", []string{"a.log", "b.log"}, kw, 200, 30, "utf-8", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`-v ng=2`, `-v npos="1,1"`, "KP_1_1=$(printf %b", "KP_2_1=$(printf %b", "head -n 200"} {
		if !strings.Contains(c, want) {
			t.Fatalf("OR 命令缺少 %q: %s", want, c)
		}
	}
}

func TestSearchCommand_OR_WithNegation(t *testing.T) {
	kw, err := ParseQuery("Exception || !DEBUG")
	if err != nil {
		t.Fatal(err)
	}
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, `-v npos="1,0"`) || !strings.Contains(c, `-v nneg="0,1"`) || !strings.Contains(c, "KP_1_1=$(printf %b") || !strings.Contains(c, "KN_2_1=$(printf %b") {
		t.Fatalf("OR + NEG 分组未正确编码: %s", c)
	}
}

func TestSearchCommand_OR_PureNegOnly(t *testing.T) {
	kw, err := ParseQuery("!A || !B")
	if err != nil {
		t.Fatal(err)
	}
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, `-v npos="0,0"`) || !strings.Contains(c, `-v nneg="1,1"`) || !strings.Contains(c, "KN_1_1=$(printf %b") || !strings.Contains(c, "KN_2_1=$(printf %b") {
		t.Fatalf("纯负 OR 分组未正确编码: %s", c)
	}
}

// ---------- v0.14 修复：ParseContextEnrichedOutput 排序 ----------
//
// 设计：
//   - 同 file 内部按 LineNo 降序（最晚的命中在最上面，符合用户"最新的排最上方"的直觉）
//   - 跨 file 按 mtime 倒序（最近修改的文件在最上面）
//   - mtime 解析失败 / file 不在白名单 → 排到最末
//
// 用户原话：
//
//	"它应该把最晚出现的排在最上面……如果这样不好实现……你就把这个文件里面
//	 最早出现的排在最上面。但是我发现就是现在是最早出现排在最上面，但是往下翻的过程中，
//	 这个行号好像还有更早出现的，会排在这个它的下面。"
//
// 早期版本是 file asc + line asc，导致跨 file 顺序没意义、用户看不到最新。
func TestParseContextEnrichedOutput_SortByFileMtimeThenLineDesc(t *testing.T) {
	// 构造两个 file：SystemOut.log（mtime 早）vs SystemErr.log（mtime 晚）
	// 输出故意打乱顺序：err 的小行号、out 的小行号、err 的大行号、out 的大行号
	raw := strings.Join([]string{
		"SystemErr.log:30:err-old-line",  // file B (newer), line 30
		"SystemOut.log:50:out-old-line",  // file A (older), line 50
		"SystemErr.log:80:err-new-line",  // file B, line 80
		"SystemOut.log:200:out-new-line", // file A, line 200
		"",                               // 空行应被跳过
		"junk-line-without-valid-format", // 解析失败的行应被跳过
	}, "\n")

	// mtime：SystemOut.log 是 2020-01-01（旧），SystemErr.log 是 2025-01-01（新）
	files := []FileEntry{
		{Name: "SystemOut.log", FullPath: "SystemOut.log", Size: 1024, ModTime: "2020-01-01T00:00:00Z", IsReadable: true},
		{Name: "SystemErr.log", FullPath: "SystemErr.log", Size: 1024, ModTime: "2025-01-01T00:00:00Z", IsReadable: true},
	}
	hits := ParseContextEnrichedOutput(raw, "srv1", "/var/log", files)
	if len(hits) != 4 {
		t.Fatalf("期望 4 条 hit，实际 %d：%+v", len(hits), hits)
	}

	// 期望顺序：mtime 新的 file (SystemErr) 在前，mtime 旧的 (SystemOut) 在后；
	// 同 file 内 line 降序。
	want := []struct {
		file string
		line int
	}{
		{"SystemErr.log", 80}, // newer file first, line desc
		{"SystemErr.log", 30},
		{"SystemOut.log", 200},
		{"SystemOut.log", 50},
	}
	for i, w := range want {
		if hits[i].File != w.file || hits[i].LineNo != w.line {
			t.Errorf("hits[%d] = %s:%d，期望 %s:%d\n完整序列: %s", i, hits[i].File, hits[i].LineNo, w.file, w.line, hitsSummary(hits))
		}
	}
}

// mtime 解析失败（空字符串 / 非 RFC3339 格式）应该走"file 字典序倒序"兜底，
// 排在 mtime 已解析 file 之后，保证已知时间线的 file 永远在前。
func TestParseContextEnrichedOutput_MtimeParseFailFallback(t *testing.T) {
	raw := strings.Join([]string{
		"z_last.log:10:z-line",
		"a_first.log:20:a-line",
		"a_first.log:5:a-old-line",
	}, "\n")
	files := []FileEntry{
		{Name: "z_last.log", FullPath: "z_last.log", ModTime: "garbage-not-rfc3339", IsReadable: true},
		{Name: "a_first.log", FullPath: "a_first.log", ModTime: "2024-06-01T00:00:00Z", IsReadable: true},
	}
	hits := ParseContextEnrichedOutput(raw, "srv", "/d", files)
	if len(hits) != 3 {
		t.Fatalf("期望 3 条 hit，实际 %d", len(hits))
	}
	// a_first.log (mtime 已知) 排前面，且内部 line 降序
	// z_last.log (mtime 解析失败) 排后面，字典序倒序就是 z 在前（只有一个）
	if hits[0].File != "a_first.log" || hits[0].LineNo != 20 {
		t.Errorf("hits[0] = %s:%d，期望 a_first.log:20", hits[0].File, hits[0].LineNo)
	}
	if hits[1].File != "a_first.log" || hits[1].LineNo != 5 {
		t.Errorf("hits[1] = %s:%d，期望 a_first.log:5（同 file 内 line 降序）", hits[1].File, hits[1].LineNo)
	}
	if hits[2].File != "z_last.log" {
		t.Errorf("hits[2] = %s，期望 z_last.log（mtime 解析失败的兜底到末尾）", hits[2].File)
	}
}

// hits 里的 file 不在 files 白名单时（防御性场景），应该排到最末。
// 实际产品中 ParseContextEnrichedOutput 解析的 file 必然来自白名单，
// 但排序逻辑要 defensive。
func TestParseContextEnrichedOutput_UnknownFileGoesToEnd(t *testing.T) {
	raw := strings.Join([]string{
		"orphan.log:99:orphan-line", // 不在 files 白名单
		"known.log:50:known-line",
		"known.log:10:known-old-line",
	}, "\n")
	files := []FileEntry{
		{Name: "known.log", ModTime: "2024-01-01T00:00:00Z", IsReadable: true},
		// 注意：orphan.log 不在这里
	}
	hits := ParseContextEnrichedOutput(raw, "srv", "/d", files)
	if len(hits) != 2 {
		t.Fatalf("orphan.log 应被白名单拒收，期望 2 条 hit，实际 %d", len(hits))
	}
	// 同 file 内 line 降序
	if hits[0].LineNo != 50 || hits[1].LineNo != 10 {
		t.Errorf("同 file 内期望 line 降序 50, 10，实际 %d, %d", hits[0].LineNo, hits[1].LineNo)
	}
}

// helpers
func hitsSummary(hits []SearchHit) string {
	var parts []string
	for _, h := range hits {
		parts = append(parts, fmt.Sprintf("%s:%d", h.File, h.LineNo))
	}
	return strings.Join(parts, " | ")
}

// v0.14 用户报障场景一：用户输入"123>"这种带尖括号的关键词。
// 早期版本会被拒（illegalKey 包含 >），现在应该通过。
// 同时验证搜出来的实际命令里 `<` `>` 字符都正确 escape 成 grep 字面匹配。
func TestUserReport_AngleBracketsInKeyword(t *testing.T) {
	kw, err := ParseQuery("123>")
	if err != nil {
		t.Fatalf("用户报障场景 '123>' 应被接受：%v", err)
	}
	if len(kw) != 1 || kw[0].Value != "123>" {
		t.Fatalf("term 应该是 '123>'，实际 %+v", kw)
	}
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false)
	if err != nil {
		t.Fatalf("构造 SearchCommand 失败：%v", err)
	}
	if !strings.Contains(c, `\x31\x32\x33\x3e`) || strings.Contains(c, "123>") {
		t.Fatalf("尖括号关键词应只以安全字节编码出现：\n%s", c)
	}
}

// v0.14 用户报障场景二：用户搜 SQL 里的 "<>" 不等号（早期拒收）。
func TestUserReport_SQLNotEqual(t *testing.T) {
	kw, err := ParseQuery("a<>b")
	if err != nil {
		t.Fatalf("'a<>b' 应被接受：%v", err)
	}
	if len(kw) != 1 || kw[0].Value != "a<>b" {
		t.Fatalf("term 应该是 'a<>b'，实际 %+v", kw)
	}
	// SearchCommand 也能成功生成
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false)
	if err != nil {
		t.Fatalf("SearchCommand 失败：%v", err)
	}
	if !strings.Contains(c, `\x61\x3c\x3e\x62`) || strings.Contains(c, "a<>b") {
		t.Fatalf("'<>' 应只以安全字节编码出现：\n%s", c)
	}
}

// v0.14 用户报障场景三：用户搜 Java 异常堆栈 "at com.example.Foo.bar(Foo.java:123)"。
// 早期版本 `(` `)` `:` `.` 里的 `(` `)` 被拒，现在全过。
func TestUserReport_JavaStackTrace(t *testing.T) {
	// 这个 query 按空白切，"(Foo.java:123)" 是单 token
	kw, err := ParseQuery("(Foo.java:123)")
	if err != nil {
		t.Fatalf("'(Foo.java:123)' 应被接受：%v", err)
	}
	if len(kw) != 1 || kw[0].Value != "(Foo.java:123)" {
		t.Fatalf("term 应保留所有字符：%+v", kw)
	}
	c, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, "utf-8", false)
	if err != nil {
		t.Fatalf("SearchCommand 失败：%v", err)
	}
	if !strings.Contains(c, `\x28\x46\x6f\x6f\x2e`) || strings.Contains(c, "(Foo.java:123)") {
		t.Fatalf("堆栈关键词应只以安全字节编码出现：\n%s", c)
	}
}

// v0.14 用户报障场景四：用户报"现在是最早出现排在最上面，但往下翻的过程中，
// 这个行号好像还有更早出现的"。这意味着同 file 内排序不一致。
//
// 早期 ParseContextEnrichedOutput 用 sort.SliceStable + file asc + line asc，
// 排序本身是对的，但跨 file 用了 file 字典序（SystemErr < SystemOut）导致
// 跨 file 的 hits 互相穿插，用户看到 A file 的 line 100 之后是 B file 的 line 30，
// 觉得"line 30 应该排在 line 100 前面"。
//
// 现在的修复：同 file 聚类 + file 内 line 降序 + file 之间按 mtime 倒序。
// 关键测试：同 file 内的 hits 必须严格按 line 降序，相邻 file 切换不穿插。
func TestUserReport_NoCrossFileInterleave(t *testing.T) {
	// 模拟用户视角看到的"穿插"：file A 排在 file B 之前，但 A 的最大行号
	// 远大于 B 的最小行号——这正是用户觉得"穿插"的根因。
	raw := strings.Join([]string{
		"SystemOut.log:1000:out-line-large",
		"SystemErr.log:30:err-line-small", // 早期实现：穿插进 A 之后
		"SystemErr.log:80:err-line-medium",
		"SystemOut.log:50:out-line-small", // 早期实现：穿插到 B 之后
	}, "\n")
	// mtime: SystemOut (新) > SystemErr (旧) —— 新文件排前面
	files := []FileEntry{
		{Name: "SystemErr.log", ModTime: "2020-01-01T00:00:00Z", IsReadable: true},
		{Name: "SystemOut.log", ModTime: "2025-01-01T00:00:00Z", IsReadable: true},
	}
	hits := ParseContextEnrichedOutput(raw, "srv", "/d", files)
	if len(hits) != 4 {
		t.Fatalf("期望 4 条 hit：%+v", hits)
	}
	// 新文件先：SystemOut.log → 老文件：SystemErr.log
	// 同 file 内 line 降序
	want := []struct {
		file string
		line int
	}{
		{"SystemOut.log", 1000},
		{"SystemOut.log", 50},
		{"SystemErr.log", 80},
		{"SystemErr.log", 30},
	}
	for i, w := range want {
		if hits[i].File != w.file || hits[i].LineNo != w.line {
			t.Errorf("hits[%d] = %s:%d，期望 %s:%d\n完整序列: %s", i, hits[i].File, hits[i].LineNo, w.file, w.line, hitsSummary(hits))
		}
	}
}
