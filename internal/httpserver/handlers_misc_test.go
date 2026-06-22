package httpserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// TestServeDownload_RejectsControlChar 验证注入式文件名（含 \r\n）会被拒绝。
// 这是 P0 修的注入 bug 的回归测试。
//
// 注意：httptest.NewRequest 会因 malformed request line 直接 panic，
// 所以这里直接构造 *http.Request，绕过其校验（让 Server 的代码去拦截）。
func TestServeDownload_RejectsControlChar(t *testing.T) {
	srv, _, _, _ := newTestServer(t)

	evilName := "x\r\nX-Evil-Header: hacked"
	w := httptest.NewRecorder()
	r := &http.Request{
		Method: "GET",
		URL: &url.URL{
			Path: "/downloads/" + evilName,
		},
		Header: make(http.Header),
	}
	srv.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Fatalf("含控制字符的路径应被拒，期望 404，得到 %d", w.Code)
	}
	if w.Header().Get("X-Evil-Header") != "" {
		t.Fatal("绝不允许注入响应头")
	}
}

// TestContentDispositionFilename_ASCII ASCII 文件名（含 " 与 \）：应做转义。
func TestContentDispositionFilename_ASCII(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"report.zip", `attachment; filename="report.zip"`},
		{`bad"name.zip`, `attachment; filename="bad\"name.zip"`},
		{`bad\name.zip`, `attachment; filename="bad\\name.zip"`},
	}
	for _, c := range cases {
		got := contentDispositionFilename(c.in)
		if got != c.want {
			t.Errorf("contentDispositionFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestContentDispositionFilename_UTF8 非 ASCII 文件名：应同时给 filename= 和 filename*=。
func TestContentDispositionFilename_UTF8(t *testing.T) {
	got := contentDispositionFilename("日志.zip")
	if !strings.HasPrefix(got, `attachment; filename=`) {
		t.Fatalf("应以 attachment; filename= 开头: %s", got)
	}
	if !strings.Contains(got, `filename*=UTF-8''`) {
		t.Fatalf("应包含 RFC 5987 filename*: %s", got)
	}
	// 文件名含中文，必须 percent-encode 后没有原始字节
	idx := strings.Index(got, "filename*=UTF-8''")
	if idx < 0 {
		t.Fatal("找不到 filename*=UTF-8''")
	}
	encoded := got[idx+len("filename*=UTF-8''"):]
	if strings.Contains(encoded, "日") || strings.Contains(encoded, "志") {
		t.Fatalf("filename* 段必须 percent-encoded，得到 %s", got)
	}
	if !strings.Contains(encoded, "%E6%97%A5") || !strings.Contains(encoded, "%E5%BF%97") {
		t.Errorf("filename* 编码不符合预期: %s", encoded)
	}
}

// TestServeDownload_UTF8Filename 端到端：中文文件名下载，响应头里同时有 filename= 和 filename*=。
func TestServeDownload_UTF8Filename(t *testing.T) {
	srv, _, _, dir := newTestServer(t)
	if err := os.WriteFile(dir+"/日志.zip", []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/downloads/"+url.PathEscape("日志.zip"), nil)
	srv.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, `filename*=UTF-8''`) {
		t.Errorf("中文文件名应同时给 filename*=: %s", cd)
	}
}

// TestContainsControlChar 单元测试
func TestContainsControlChar(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"normal", false},
		{"中文.zip", false},
		{"a\nb", true},
		{"a\rb", true},
		{"a\tb", true},
		{"a\x00b", true},
		{"a\x7fb", true},
		{"", false},
	}
	for _, c := range cases {
		if got := containsControlChar(c.in); got != c.want {
			t.Errorf("containsControlChar(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestUrlEncodePath 单元测试
func TestUrlEncodePath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"abc", "abc"},
		{"a b", "a%20b"},
		{"中文", "%E4%B8%AD%E6%96%87"},
		{"a/b", "a%2Fb"},
		{"a~b", "a~b"},
		{"a-b_c.d", "a-b_c.d"},
	}
	for _, c := range cases {
		if got := urlEncodePath(c.in); got != c.want {
			t.Errorf("urlEncodePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
