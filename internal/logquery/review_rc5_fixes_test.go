package logquery

import (
	"testing"
)

// REVIEW-rc5 #1：filename 含 `:` 或 `-` 时，原 parseSearchOutput 会错位
// （find-first-IndexAny ":-"）。新算法 parseSearchLine 要求 lineno 必须是纯数字，
// 这一个约束彻底消除了歧义。
func TestParseSearchLine_FilenameWithColon_REVIEW1(t *testing.T) {
	// AIX 老系统日志命名常见：SystemOut:20260619.log
	// grep -H 输出：SystemOut:20260619.log:123:error msg
	// 原算法会切成 file="SystemOut" lineno="20260619.log" → Atoi 失败 → 整行丢弃
	// 新算法正确切成 file="SystemOut:20260619.log" lineno=123
	gotFile, gotSep, gotLine, gotContent, ok := ParseSearchHitLine("SystemOut:20260619.log:123:error msg")
	if !ok {
		t.Fatalf("expected ok=true, got false")
	}
	if gotFile != "SystemOut:20260619.log" {
		t.Errorf("file mismatch: got %q want %q", gotFile, "SystemOut:20260619.log")
	}
	if gotSep != ':' {
		t.Errorf("sep mismatch: got %q want ':'", gotSep)
	}
	if gotLine != 123 {
		t.Errorf("line mismatch: got %d want 123", gotLine)
	}
	if gotContent != "error msg" {
		t.Errorf("content mismatch: got %q want %q", gotContent, "error msg")
	}
}

// 文件名含 `-`（Linux 允许）也应该正确解析。
func TestParseSearchLine_FilenameWithDash_REVIEW1(t *testing.T) {
	// log-2024-06-19.log:42:some content
	// 原算法 strings.IndexAny(":-") 会把第一个 `-` 当成上下文分隔符 → file 错误
	gotFile, gotSep, gotLine, gotContent, ok := ParseSearchHitLine("log-2024-06-19.log:42:some content")
	if !ok {
		t.Fatalf("expected ok=true, got false")
	}
	if gotFile != "log-2024-06-19.log" {
		t.Errorf("file mismatch: got %q want %q", gotFile, "log-2024-06-19.log")
	}
	if gotSep != ':' {
		t.Errorf("sep mismatch: got %q want ':'", gotSep)
	}
	if gotLine != 42 {
		t.Errorf("line mismatch: got %d want 42", gotLine)
	}
	if gotContent != "some content" {
		t.Errorf("content mismatch: got %q want %q", gotContent, "some content")
	}
}

// 上下文行（SEP = '-'）正确识别。
func TestParseSearchLine_ContextLine(t *testing.T) {
	gotFile, gotSep, gotLine, _, ok := ParseSearchHitLine("log.txt-7:context content")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if gotFile != "log.txt" || gotSep != '-' || gotLine != 7 {
		t.Errorf("got file=%q sep=%q line=%d want log.txt - 7", gotFile, gotSep, gotLine)
	}
}

// 常规 hit 行（最常见格式）。
func TestParseSearchLine_NormalHitLine(t *testing.T) {
	gotFile, gotSep, gotLine, gotContent, ok := ParseSearchHitLine("log.txt:1:hello")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if gotFile != "log.txt" || gotSep != ':' || gotLine != 1 || gotContent != "hello" {
		t.Errorf("got file=%q sep=%q line=%d content=%q want log.txt : 1 hello",
			gotFile, gotSep, gotLine, gotContent)
	}
}

// 垃圾输入应返回 ok=false（不 panic）。
func TestParseSearchLine_Garbage(t *testing.T) {
	for _, in := range []string{"", "no_colon", ":", "-", "abc.def", "log.txt:abc:content"} {
		if _, _, _, _, ok := ParseSearchHitLine(in); ok {
			t.Errorf("expected ok=false for %q", in)
		}
	}
}

// REVIEW-rc5 #3：cleanFile 已删除 ReplaceAll("'", "") 的静默删单引号行为，
// 改用 assert + 报错。这里验证 SearchCommand 在 file 含 ' 时直接报错。
func TestSearchCommand_FileQuoteRejected_REVIEW3(t *testing.T) {
	kw := []SearchKeyword{{Op: "term", Value: "x"}}
	// 直接传 file=O'Brien.log，上游 SearchCommand 已经校验 file 不含 '，
	// 应该返回 error。
	_, err := SearchCommand("/dir", []string{"O'Brien.log"}, kw, 10, 30, "utf-8", false)
	if err == nil {
		t.Errorf("expected error for file containing single quote")
	}
}

// REVIEW-rc5 #3：TailCommand 同理。
func TestTailCommand_FileQuoteRejected_REVIEW3(t *testing.T) {
	_, err := TailCommand("/dir", "O'Brien.log", 100)
	if err == nil {
		t.Errorf("expected error for file containing single quote")
	}
}

// REVIEW-rc5 #5：posix_ls pattern 字符白名单改"排除法"，中文 / Unicode 必须保留。
func TestListCommandPosix_ChinesePattern_REVIEW5(t *testing.T) {
	// pattern "日志.*.gz" 包含中文，旧白名单会丢弃；新算法应保留。
	cmd, err := ListCommand("/var/log", []string{"日志.*.gz"}, 5, "posix_ls")
	if err != nil {
		t.Fatalf("ListCommand should accept Chinese pattern: %v", err)
	}
	// 简单校验：生成的命令里包含中文 pattern（通过 printf %b 或 grep -E）
	if !contains(cmd, "日志") {
		t.Errorf("Chinese pattern dropped from command: %s", cmd)
	}
}

// helper
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}