package sshclient

import (
	"errors"
	"strings"
	"testing"
)

// failWriteCloser 模拟底层 stdin 写失败（真正的管道断开）。
type failWriteCloser struct{}

func (failWriteCloser) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
func (failWriteCloser) Close() error              { return nil }

// P2（审核第 11 项）：GBK 终端粘贴不可编码字符（emoji）时必须保持会话。
// 旧实现直接返回编码错误，wsReader 把它当成 stdin 管道断开并关闭整个终端会话。
func TestGBKStdinEncoder_UnencodableRuneKeepsSession(t *testing.T) {
	mem := &memWriteCloser{}
	enc := newGBKStdinEncoder(mem) // 默认 GBK

	n, err := enc.Write([]byte("a🙂b"))
	if err != nil {
		t.Fatalf("不可编码字符不能返回错误（会导致终端掉线），got: %v", err)
	}
	if n != len("a🙂b") {
		t.Fatalf("Write n=%d, want %d", n, len("a🙂b"))
	}
	out := mem.Bytes()
	if len(out) == 0 {
		t.Fatal("可编码部分必须照常写出")
	}
	if !strings.HasPrefix(string(out), "a") || !strings.HasSuffix(string(out), "b") {
		t.Fatalf("可编码的 ASCII 必须保留，got %q", out)
	}
	if !strings.Contains(string(out), "?") {
		t.Fatalf("不可编码字符应替换为 '?'，got %q", out)
	}
}

// P2（审核第 11 项）：CJK 扩展 B 汉字（𠮷）同样不能断流。
func TestGBKStdinEncoder_UnencodableCJKExtensionKeepsSession(t *testing.T) {
	mem := &memWriteCloser{}
	enc := newGBKStdinEncoder(mem)
	if _, err := enc.Write([]byte("echo 𠮷\r")); err != nil {
		t.Fatalf("不可编码字符不能返回错误，got: %v", err)
	}
	out := mem.Bytes()
	if len(out) == 0 || out[len(out)-1] != '\r' {
		t.Fatalf("命令结尾的 \\r 必须照常写出，got %q", out)
	}
}

// P2（审核第 11 项）：不可编码字符被拆到两次 Write（跨边界）也不能报错或丢字节。
func TestGBKStdinEncoder_UnencodableSplitWrite(t *testing.T) {
	mem := &memWriteCloser{}
	enc := newGBKStdinEncoder(mem)
	full := []byte("a🙂b") // 🙂 是 4 字节
	if _, err := enc.Write(full[:2]); err != nil {
		t.Fatalf("Write1 err: %v", err)
	}
	if _, err := enc.Write(full[2:]); err != nil {
		t.Fatalf("Write2 err: %v", err)
	}
	got := mem.Bytes()
	if !strings.HasPrefix(string(got), "a") || !strings.HasSuffix(string(got), "b") {
		t.Fatalf("跨边界写入后的内容不对: %q", got)
	}
}

// P2（审核第 11 项）：真正的底层写错误仍必须向上传递，不能被当成"编码问题"吞掉。
func TestGBKStdinEncoder_RealWriteErrorStillPropagates(t *testing.T) {
	enc := newGBKStdinEncoder(failWriteCloser{})
	if _, err := enc.Write([]byte("hello")); err == nil {
		t.Fatal("底层管道错误必须继续返回给上层")
	}
}

// P2（审核第 11 项）：可编码内容与不可编码内容混合时，可编码部分按 GBK 正常转换。
func TestEncodeUTF8ToGBKReplacing_MixedContent(t *testing.T) {
	enc := newGBKStdinEncoder(&memWriteCloser{})
	got := encodeUTF8ToGBKReplacing(enc.enc, []byte("中🙂文"))
	wantPrefix := []byte{0xD6, 0xD0} // "中"
	wantSuffix := []byte{0xCE, 0xC4} // "文"
	if len(got) != len(wantPrefix)+1+len(wantSuffix) {
		t.Fatalf("长度不对: %x", got)
	}
	if got[0] != wantPrefix[0] || got[1] != wantPrefix[1] {
		t.Fatalf("前缀 GBK 字节不对: %x", got)
	}
	if got[2] != '?' {
		t.Fatalf("不可编码字符应为 '?': %x", got)
	}
	if got[3] != wantSuffix[0] || got[4] != wantSuffix[1] {
		t.Fatalf("后缀 GBK 字节不对: %x", got)
	}
}

// P2（审核第 11 项）：splitCompleteUTF8 的边界行为。
func TestSplitCompleteUTF8(t *testing.T) {
	full := []byte("中")
	if complete, tail := splitCompleteUTF8(full); len(tail) != 0 || len(complete) != len(full) {
		t.Fatalf("完整序列不应被截断: complete=%x tail=%x", complete, tail)
	}
	if complete, tail := splitCompleteUTF8(full[:2]); len(complete) != 0 || len(tail) != 2 {
		t.Fatalf("半个汉字必须留到下一次: complete=%x tail=%x", complete, tail)
	}
	if complete, tail := splitCompleteUTF8([]byte("ab")); len(tail) != 0 || string(complete) != "ab" {
		t.Fatalf("纯 ASCII 应整体通过: complete=%q tail=%x", complete, tail)
	}
}
