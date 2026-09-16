package sshclient

import (
	"bytes"
	"sync"
	"testing"
)

// memWriteCloser 是内存版 io.WriteCloser，用于捕获编码器输出。
type memWriteCloser struct {
	buf    bytes.Buffer
	closed bool
	mu     sync.Mutex
}

func (m *memWriteCloser) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.Write(p)
}
func (m *memWriteCloser) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}
func (m *memWriteCloser) Bytes() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.buf.Bytes()...)
}

// TestGBKStdinEncoder_Chinese：UTF-8 "你好" 应转成 GBK 字节 C4 E3 BA C3。
// 这正是"乱码文件夹进不去"的核心：cd 中文目录时输入字节必须与磁盘 GBK 一致。
func TestGBKStdinEncoder_Chinese(t *testing.T) {
	mem := &memWriteCloser{}
	enc := newGBKStdinEncoder(mem)
	n, err := enc.Write([]byte("你好"))
	if err != nil {
		t.Fatalf("Write err: %v", err)
	}
	if n != len("你好") {
		t.Fatalf("Write n=%d, want %d", n, len("你好"))
	}
	got := mem.Bytes()
	want := []byte{0xC4, 0xE3, 0xBA, 0xC3}
	if !bytes.Equal(got, want) {
		t.Fatalf("GBK encode: got %x, want %x", got, want)
	}
}

// TestGBKStdinEncoder_ASCII：纯 ASCII（含 cd / \r \n ESC）必须逐字节直通，
// cwd 查询注入的 printf 命令不受影响。
func TestGBKStdinEncoder_ASCII(t *testing.T) {
	mem := &memWriteCloser{}
	enc := newGBKStdinEncoder(mem)
	cmd := "cd /tmp\rprintf 'hi'\x1b[0m\n"
	if _, err := enc.Write([]byte(cmd)); err != nil {
		t.Fatalf("Write err: %v", err)
	}
	if got := string(mem.Bytes()); got != cmd {
		t.Fatalf("ASCII passthrough: got %q, want %q", got, cmd)
	}
}

// TestGBKStdinEncoder_SplitWrite：UTF-8 汉字被拆成两次 Write 也要能拼回去。
// 前端粘贴/IME 分片时可能出现这种边界。
func TestGBKStdinEncoder_SplitWrite(t *testing.T) {
	mem := &memWriteCloser{}
	enc := newGBKStdinEncoder(mem)
	full := []byte("你好")
	// "你" 的 UTF-8 是 3 字节，拆成 1+2 再写 "好"
	if _, err := enc.Write(full[:1]); err != nil {
		t.Fatalf("Write1 err: %v", err)
	}
	if _, err := enc.Write(full[1:]); err != nil {
		t.Fatalf("Write2 err: %v", err)
	}
	got := mem.Bytes()
	want := []byte{0xC4, 0xE3, 0xBA, 0xC3}
	if !bytes.Equal(got, want) {
		t.Fatalf("split encode: got %x, want %x", got, want)
	}
}

// TestGBKStdinEncoder_MixedCD：模拟真实输入 "cd 测试目录\r"。
func TestGBKStdinEncoder_MixedCD(t *testing.T) {
	mem := &memWriteCloser{}
	enc := newGBKStdinEncoder(mem)
	if _, err := enc.Write([]byte("cd ")); err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write([]byte("测试目录")); err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	got := mem.Bytes()
	// 开头 "cd " + 结尾 \r 必须是 ASCII
	if !bytes.HasPrefix(got, []byte("cd ")) || got[len(got)-1] != '\r' {
		t.Fatalf("mixed cd framing broken: %x", got)
	}
	// 中间 4 个汉字 = 8 个 GBK 字节，总长 3+8+1=12
	if len(got) != 12 {
		t.Fatalf("mixed cd len=%d, want 12 (got %x)", len(got), got)
	}
}

// TestIsGBKEncoding：大小写/空格容忍。
func TestIsGBKEncoding(t *testing.T) {
	for _, enc := range []string{"gbk", "GBK", " gbk ", "gb18030", "GB18030"} {
		if !isGBKEncoding(enc) {
			t.Errorf("%q should be GBK", enc)
		}
	}
	for _, enc := range []string{"", "utf-8", "UTF-8", "utf8", "latin1"} {
		if isGBKEncoding(enc) {
			t.Errorf("%q should NOT be GBK", enc)
		}
	}
}

// TestEncodeUTF8ToGBK_Helper：一次性 helper 与流式编码器结果一致。
func TestEncodeUTF8ToGBK_Helper(t *testing.T) {
	got := encodeUTF8ToGBK([]byte("中文目录"))
	want := []byte{0xD6, 0xD0, 0xCE, 0xC4, 0xC4, 0xBF, 0xC2, 0xBC}
	if !bytes.Equal(got, want) {
		t.Fatalf("helper: got %x, want %x", got, want)
	}
	if got := encodeUTF8ToGBK(nil); len(got) != 0 {
		t.Fatalf("empty: got %x", got)
	}
}
