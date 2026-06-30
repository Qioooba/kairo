package sshclient

import (
	"io"
	"testing"
	"time"
)

// gbkDecoder 在 UTF-8 输入（mock 输出是 UTF-8）下应该能跑通：
// 见到非 GBK 字节要么替换要么抛错，但不能卡死。
func TestGbkDecoder_UTF8Input_DoesNotBlock(t *testing.T) {
	src := newChunkedReader([]byte("hello world\n[mock] user@host$ "))
	dec := newGBKDecoder(src)

	done := make(chan struct{})
	var got []byte
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := dec.Read(buf)
			if n > 0 {
				got = append(got, buf[:n]...)
			}
			if err == io.EOF {
				close(done)
				return
			}
			if err != nil {
				t.Errorf("unexpected err: %v", err)
				close(done)
				return
			}
		}
	}()

	select {
	case <-done:
		t.Logf("decoded %d bytes: %q", len(got), got)
	case <-time.After(2 * time.Second):
		t.Fatal("TIMEOUT: gbkDecoder blocked on UTF-8 input")
	}
}

// gbkDecoder 在 UTF-8 中文输入下也不应该卡死。
func TestGbkDecoder_UTF8Chinese_DoesNotBlock(t *testing.T) {
	src := newChunkedReader([]byte("中文测试 hello\n"))
	dec := newGBKDecoder(src)

	done := make(chan struct{})
	var got []byte
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := dec.Read(buf)
			if n > 0 {
				got = append(got, buf[:n]...)
			}
			if err == io.EOF {
				close(done)
				return
			}
			if err != nil {
				t.Errorf("unexpected err: %v", err)
				close(done)
				return
			}
		}
	}()

	select {
	case <-done:
		t.Logf("decoded %d bytes", len(got))
	case <-time.After(2 * time.Second):
		t.Fatal("TIMEOUT: gbkDecoder blocked on UTF-8 chinese input")
	}
}

// 一个简单的 Reader：每次 Read 返回 1 字节，模拟流式输入。
type chunkedReader struct {
	data []byte
	off  int
}

func newChunkedReader(data []byte) *chunkedReader { return &chunkedReader{data: data} }

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:r.off+1])
	r.off += n
	return n, nil
}
