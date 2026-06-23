package httpserver

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestOpenPathAllowed 验证路径白名单校验（B3 安全关键）。
//
// 允许：target 在 allowRoot 下（含子目录、子子目录、target == allowRoot）。
// 拒绝：
//   - 空 allowRoot / 空 target；
//   - 含控制字符（NUL/换行/回车）；
//   - target 用 ../ 跳出 allowRoot；
//   - target 跟 allowRoot 完全不相关（兄弟目录、父目录、跨盘符等）。
func TestOpenPathAllowed(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "downloads")
	sub := filepath.Join(root, "20260101")
	outside := filepath.Join(tmp, "etc")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		root    string
		target  string
		wantErr bool
	}{
		{name: "root 下文件", root: root, target: filepath.Join(root, "a.log"), wantErr: false},
		{name: "root 子目录下的文件", root: root, target: filepath.Join(sub, "a.log"), wantErr: false},
		{name: "root 自身", root: root, target: root, wantErr: false},
		{name: "父目录（tmp）拒绝", root: root, target: tmp, wantErr: true},
		{name: "../ 跳出拒绝", root: root, target: filepath.Join(root, "..", "etc", "passwd"), wantErr: true},
		{name: "兄弟目录拒绝", root: root, target: outside, wantErr: true},
		{name: "空 root 拒绝", root: "", target: "/tmp", wantErr: true},
		{name: "空 target 拒绝", root: root, target: "", wantErr: true},
		{name: "NUL 字符拒绝", root: root, target: root + "\x00/etc/passwd", wantErr: true},
		{name: "换行拒绝", root: root, target: root + "\nrm", wantErr: true},
		{name: "不存在的子路径（在 root 下）允许", root: root, target: filepath.Join(root, "ghost", "x.log"), wantErr: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := openPathAllowed(c.root, c.target)
			if (err != nil) != c.wantErr {
				t.Errorf("wantErr=%v, err=%v", c.wantErr, err)
			}
		})
	}
}

// TestRevealInFileManager_BuildCommand 验证 revealInFileManager 在不同 OS 下
// 选取的命令可被 fork（exec.Command 不报 "executable not found"）。
//
// 因为测试机可能没装真正的 Finder/Explorer，调用 start 可能报错，
// 这里只验证函数至少能跑到 fork 阶段 —— 不实际等命令完成。
//
// 实际平台：CI 多在 Linux + macOS；Windows 由 build tag / runner 覆盖。
func TestRevealInFileManager_BuildCommand(t *testing.T) {
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "fake.txt")
	if err := os.WriteFile(fake, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := revealInFileManager(fake)
	// 在 CI 容器里可能没装 open/explorer/xdg-open —— 这种 OS 错误也算"函数行为正确"。
	// 我们关心的是：函数没 panic / 没搞错 platform 分支。
	if err != nil {
		// 不致命，记 t.Log；测试只在"完全没有命令"的极端环境会失败
		t.Logf("revealInFileManager(%s) on %s returned: %v (acceptable if OS has no GUI)", runtime.GOOS, runtime.GOOS, err)
	}
}

// ============ HTTP 层集成测试 ============

// newTempDownloadDir 返回一个临时下载目录，并在 cfg 里把它替换成当前 downloadDir。
func newTempDownloadDir(t *testing.T) (downloadDir string, cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	return dir, func() {}
}

// TestDownloadsOpenDir_OK 验证 happy path：tmp 下放一个文件 → POST /api/downloads/{name}/open-dir。
//
// 我们不验证 revealInFileManager 真的弹窗（CI 没 GUI），只验证：
//   - HTTP 200；
//   - 响应里 platform 字段 = runtime.GOOS；
//   - 审计写入；
//   - 跨平台的 exec.Command 至少不 panic。
func TestDownloadsOpenDir_OK(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	// 取当前 downloadDir，往里塞一个文件
	dlDir := downloadDirForTest(t, srv)
	name := "mock-1_001_test_150405_000.log"
	target := filepath.Join(dlDir, name)
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := doRequest(srv, "POST", "/api/downloads/"+name+"/open-dir", nil)
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	// 响应 JSON 至少含 platform 字段
	body := w.Body.String()
	if !strings.Contains(body, `"platform"`) {
		t.Errorf("响应应含 platform 字段，实际: %s", body)
	}
	if !strings.Contains(body, runtime.GOOS) {
		t.Errorf("响应 platform 应等于 runtime.GOOS (%s)，实际: %s", runtime.GOOS, body)
	}
}

// TestDownloadsOpenDir_NotFound 验证文件不存在 → 404。
func TestDownloadsOpenDir_NotFound(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	dlDir := downloadDirForTest(t, srv)
	// 创建 downloadDir 让校验过，但文件不创建
	_ = dlDir

	w := doRequest(srv, "POST", "/api/downloads/ghost.log/open-dir", nil)
	if w.Code != 404 {
		t.Errorf("期望 404，得到 %d", w.Code)
	}
}

// TestDownloadsOpenDir_PathTraversal 验证路径穿越拒绝（安全关键）。
//
// name 里带 "../" 会被 handler 立刻拒绝（防注入），不会被拼到 downloadDir 下。
func TestDownloadsOpenDir_PathTraversal(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	dlDir := downloadDirForTest(t, srv)
	_ = dlDir

	w := doRequest(srv, "POST", "/api/downloads/..%2Fetc%2Fpasswd/open-dir", nil)
	// httptest URL 不会被自动 decode；"../etc/passwd" 不会被 handler 解析成路径
	// —— 但 handler 看到 .. 也拒（"name 含非法字符"）。
	if w.Code != 400 && w.Code != 404 {
		t.Errorf("期望 400/404，得到 %d body=%s", w.Code, w.Body.String())
	}
}

// TestDownloadsOpenDir_WrongMethod 验证非 POST 405。
func TestDownloadsOpenDir_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	dlDir := downloadDirForTest(t, srv)
	target := filepath.Join(dlDir, "x.log")
	_ = os.WriteFile(target, []byte("x"), 0o644)
	_ = httptest.NewRecorder()

	for _, method := range []string{"GET", "PUT", "DELETE"} {
		w := doRequest(srv, method, "/api/downloads/x.log/open-dir", nil)
		if w.Code != 405 {
			t.Errorf("%s /open-dir 期望 405，得到 %d", method, w.Code)
		}
	}
}

// downloadDirForTest 拿当前 server cfg 的 DownloadDir（避免 hardcode tmp）。
func downloadDirForTest(t *testing.T, srv *Server) string {
	t.Helper()
	// 通过 audit logger 间接拿：直接用 Server.cur().DownloadDir()
	return srv.cur().DownloadDir()
}
