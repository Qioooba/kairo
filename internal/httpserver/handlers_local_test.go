package httpserver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLocalReveal_RejectsOutsideRoot 验证 /api/local/reveal-file 的白名单校验。
//
// 不真起 reveal 进程（用 testServer 注入不会调命令的 reveal 是不可能的；
// 我们走白名单校验提前拒，revealInFileManager 不会被调到）。
func TestLocalReveal_RejectsOutsideRoot(t *testing.T) {
	srv, mgr, _, tmpDir := newTestServer(t)
	// cfg 下载目录 = tmpDir（newTestServer 固定）
	if err := mgr.Replace(mgr.Get()); err != nil {
		t.Fatalf("cfg replace: %v", err)
	}
	_ = srv

	// 真下个文件 + 选它（合法路径）
	goodFile := filepath.Join(tmpDir, "20260101", "a.log")
	if err := os.MkdirAll(filepath.Dir(goodFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goodFile, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 跳出 tmpDir 的恶意路径
	badPath := filepath.Join(tmpDir, "..", "etc", "passwd")

	cases := []struct {
		name string
		path string
		want int
	}{
		{"空路径", "", 400},
		{"含控制字符", tmpDir + "\nrm", 403}, // 含控制字符被 openPathAllowed 当 403 拒（更严格）
		{"跳出白名单", badPath, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(srv, "POST", "/api/local/reveal-file", map[string]any{
				"path": tc.path,
			})
			if w.Code != tc.want {
				t.Errorf("期望 %d，得到 %d body=%s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}

// TestLocalOpenFolder_RejectsOutsideRoot 同上，针对 /api/local/open-folder
func TestLocalOpenFolder_RejectsOutsideRoot(t *testing.T) {
	srv, mgr, _, tmpDir := newTestServer(t)
	_ = mgr
	_ = srv

	badPath := filepath.Join(tmpDir, "..", "etc", "passwd")

	cases := []struct {
		name string
		path string
		want int
	}{
		{"空路径", "", 400},
		{"含控制字符", tmpDir + "\nrm", 403}, // 含控制字符被 openPathAllowed 当 403 拒
		{"跳出白名单", badPath, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(srv, "POST", "/api/local/open-folder", map[string]any{
				"path": tc.path,
			})
			if w.Code != tc.want {
				t.Errorf("期望 %d，得到 %d body=%s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}
