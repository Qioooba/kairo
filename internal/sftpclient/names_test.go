package sftpclient

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// "中文" 的两种字节：UTF-8 展示形 vs GBK 磁盘形。
var (
	utf8ZhongWen = "中文"                                   // E4 B8 AD E6 96 87
	gbkZhongWen  = string([]byte{0xD6, 0xD0, 0xCE, 0xC4}) // D6 D0 CE C4（AIX/老盘常见）
)

func TestDecodeServerName_UTF8Passthrough(t *testing.T) {
	for _, s := range []string{"", "abc", "/tmp/SystemOut.log", "中文目录", utf8ZhongWen} {
		if got := DecodeServerName(s); got != s {
			t.Errorf("passthrough: %q -> %q", s, got)
		}
	}
}

func TestDecodeServerName_GBK(t *testing.T) {
	if got := DecodeServerName(gbkZhongWen); got != utf8ZhongWen {
		t.Errorf("GBK decode: got %q (%x), want %q", got, []byte(got), utf8ZhongWen)
	}
	// 带 ASCII 前缀的混合名也应解出
	raw := "/tmp/" + gbkZhongWen
	if got := DecodeServerName(raw); got != "/tmp/"+utf8ZhongWen {
		t.Errorf("mixed decode: got %q", got)
	}
}

func TestEncodePathCandidates_ASCII(t *testing.T) {
	cands := EncodePathCandidates("/tmp/logs")
	if len(cands) != 1 || cands[0] != "/tmp/logs" {
		t.Fatalf("ASCII: got %q", cands)
	}
}

func TestEncodePathCandidates_ChineseRoundTrip(t *testing.T) {
	display := "/tmp/" + utf8ZhongWen
	cands := EncodePathCandidates(display)
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates, got %q", cands)
	}
	if cands[0] != display {
		t.Errorf("cand[0] = %q, want display", cands[0])
	}
	if cands[1] != "/tmp/"+gbkZhongWen {
		t.Errorf("cand[1] = %x, want GBK", []byte(cands[1]))
	}
	// 往返：GBK 原始名解成展示名，再编回去必须等于原始字节
	if back := EncodePathCandidates(DecodeServerName("/tmp/" + gbkZhongWen)); back[1] != "/tmp/"+gbkZhongWen {
		t.Errorf("roundtrip broken: %q", back)
	}
}

// TestReadDir_GBKFallback：远端只有 GBK 字节目录，前端拿 UTF-8 展示路径来列，
// 必须回退命中，且条目名解成中文（JSON 可序列化，可再点进）。
func TestReadDir_GBKFallback(t *testing.T) {
	gbkDir := "/tmp/" + gbkZhongWen
	backend := &mockBackend{
		dirs: map[string][]os.FileInfo{
			gbkDir: {
				fakeFileInfo{name: gbkZhongWen, isDir: true, mode: os.ModeDir | 0o755, mtime: time.Now()},
				fakeFileInfo{name: "SystemOut.log", size: 10, mode: 0o644},
			},
		},
	}
	c := newWithBackend(backend)
	defer c.Close()

	infos, err := c.ReadDir("/tmp/" + utf8ZhongWen)
	if err != nil {
		t.Fatalf("ReadDir fallback: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("entries=%d, want 2", len(infos))
	}
	if infos[0].Name() != utf8ZhongWen {
		t.Errorf("entry[0] = %q (%x), want 中文", infos[0].Name(), []byte(infos[0].Name()))
	}
}

// TestReadDir_UTF8FastPath：纯 UTF-8 盘不受影响，一次即中。
func TestReadDir_UTF8FastPath(t *testing.T) {
	backend := &mockBackend{
		dirs: map[string][]os.FileInfo{
			"/tmp/" + utf8ZhongWen: {
				fakeFileInfo{name: utf8ZhongWen, isDir: true, mode: os.ModeDir | 0o755},
			},
		},
	}
	c := newWithBackend(backend)
	defer c.Close()
	infos, err := c.ReadDir("/tmp/" + utf8ZhongWen)
	if err != nil {
		t.Fatalf("ReadDir utf8: %v", err)
	}
	if len(infos) != 1 || infos[0].Name() != utf8ZhongWen {
		t.Fatalf("entries=%v", infos)
	}
}

// TestStatOpen_GBKFallback：预览/下载链路（Stat+Open）同样回退。
func TestStatOpen_GBKFallback(t *testing.T) {
	gbkFile := "/tmp/" + gbkZhongWen + ".log"
	backend := &mockBackend{
		files: map[string][]byte{gbkFile: []byte("gbk file body")},
	}
	c := newWithBackend(backend)
	defer c.Close()

	display := "/tmp/" + utf8ZhongWen + ".log"
	if _, err := c.Stat(display); err != nil {
		t.Fatalf("Stat fallback: %v", err)
	}
	f, err := c.Open(display)
	if err != nil {
		t.Fatalf("Open fallback: %v", err)
	}
	defer f.Close()
	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	if string(buf[:n]) != "gbk file body" {
		t.Errorf("content=%q", buf[:n])
	}
}

// TestDownload_GBKFallback：下载用展示路径也能落盘。
func TestDownload_GBKFallback(t *testing.T) {
	gbkFile := "/tmp/" + gbkZhongWen + ".log"
	backend := &mockBackend{files: map[string][]byte{gbkFile: []byte("hello gbk")}}
	c := newWithBackend(backend)
	defer c.Close()

	local := filepath.Join(t.TempDir(), "out.log")
	n, err := c.DownloadFile("/tmp/"+utf8ZhongWen+".log", local)
	if err != nil {
		t.Fatalf("Download fallback: %v", err)
	}
	if n != int64(len("hello gbk")) {
		t.Errorf("bytes=%d", n)
	}
	got, _ := os.ReadFile(local)
	if string(got) != "hello gbk" {
		t.Errorf("content=%q", got)
	}
}
