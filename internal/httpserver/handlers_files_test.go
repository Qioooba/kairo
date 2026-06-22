package httpserver

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ops-toolbox/internal/config"
	"ops-toolbox/internal/sftpclient"
	"ops-toolbox/internal/sshclient"
)

// fakeSftpClient 实现 sftpClientLike 接口，给测试用。
//
// 行为：
//   - ReadDir/Stat：走 fs + dirs（路径 → 内容/条目）
//   - DownloadFile/DownloadFileWithProgress：把 fs[path] 写到 localPath，
//     写完后回调 progress(written, total) 一次保证 SSE 拿到 100%
type fakeSftpClient struct {
	files      map[string][]byte
	dirs       map[string][]fakeDirEntry
	downloadCb func(remotePath string, bytes int64)
}

type fakeDirEntry struct {
	name  string
	size  int64
	isDir bool
}

func (f *fakeSftpClient) Close() error { return nil }

func (f *fakeSftpClient) ReadDir(path string) ([]os.FileInfo, error) {
	entries, ok := f.dirs[path]
	if !ok {
		return nil, &os.PathError{Op: "readdir", Path: path, Err: os.ErrNotExist}
	}
	out := make([]os.FileInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, fakeFileInfo{
			name:  e.name,
			size:  e.size,
			isDir: e.isDir,
			mode:  0o644,
		})
	}
	return out, nil
}

func (f *fakeSftpClient) Stat(path string) (os.FileInfo, error) {
	if entries, ok := f.dirs[path]; ok {
		if len(entries) == 0 {
			return fakeFileInfo{name: filepath.Base(path), isDir: true, mode: os.ModeDir | 0o755}, nil
		}
		return fakeFileInfo{name: filepath.Base(path), isDir: true, mode: os.ModeDir | 0o755}, nil
	}
	if content, ok := f.files[path]; ok {
		return fakeFileInfo{name: filepath.Base(path), size: int64(len(content)), mode: 0o644}, nil
	}
	return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
}

func (f *fakeSftpClient) DownloadFile(remote, local string) (int64, error) {
	return f.downloadWith(remote, local, nil)
}

func (f *fakeSftpClient) DownloadFileWithProgress(remote, local string, progress func(int64, int64)) (int64, error) {
	return f.downloadWith(remote, local, progress)
}

func (f *fakeSftpClient) downloadWith(remote, local string, progress func(int64, int64)) (int64, error) {
	content, ok := f.files[remote]
	if !ok {
		return 0, &os.PathError{Op: "open", Path: remote, Err: os.ErrNotExist}
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(local, content, 0o644); err != nil {
		return 0, err
	}
	n := int64(len(content))
	if progress != nil {
		progress(n, n)
	}
	if f.downloadCb != nil {
		f.downloadCb(remote, n)
	}
	return n, nil
}

// fakeFileInfo 是 io/fs.FileInfo 的最小实现。
type fakeFileInfo struct {
	name  string
	size  int64
	isDir bool
	mode  os.FileMode
}

func (f fakeFileInfo) Name() string { return f.name }
func (f fakeFileInfo) Size() int64  { return f.size }
func (f fakeFileInfo) Mode() os.FileMode {
	if f.isDir {
		return os.ModeDir | 0o755
	}
	return f.mode
}
func (f fakeFileInfo) ModTime() (mtime time.Time) { return }
func (f fakeFileInfo) IsDir() bool                { return f.isDir }
func (f fakeFileInfo) Sys() any                   { return nil }

// ---- 测试辅助 ----

// withFakeSFTP 替换全局 sftpDialer 为返回 fakeSftpClient 的工厂，
// t.Cleanup 自动还原。
func withFakeSFTP(t *testing.T, f *fakeSftpClient) {
	t.Helper()
	orig := sftpDialer
	sftpDialer = func(_ *sshclient.Client) (sftpClientLike, error) {
		return f, nil
	}
	t.Cleanup(func() { sftpDialer = orig })
}

// newServerWithFakeSSHAndSFTP 同时启用 fake SSH + fake SFTP dialer：
//   - 启一个 in-process SSH server（host=127.0.0.1，端口动态）
//   - 把配置里的 mock-1 server 指向这个端口
//   - 替换 sftpDialer 让 handler 拿到测试用 fake SFTP 客户端
//
// 这样测的是 handlers_files.go 全链路（auth + sftpDialer + sftp 操作 + 进度广播）
// 但不需要真的起 SFTP 子系统。
func newServerWithFakeSSHAndSFTP(t *testing.T, f *fakeSftpClient) *Server {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)
	withFakeSFTP(t, f)
	return srv
}

func newFakeSftpBasic() *fakeSftpClient {
	return &fakeSftpClient{
		files: make(map[string][]byte),
		dirs:  make(map[string][]fakeDirEntry),
	}
}

// ---- 实际测试 ----

// TestFilesList_Happy_FakeSFTP 完整路径：list /data 拿到 a.log / b.log / sub。
func TestFilesList_Happy_FakeSFTP(t *testing.T) {
	f := newFakeSftpBasic()
	f.dirs["/data"] = []fakeDirEntry{
		{name: "a.log", size: 100, isDir: false},
		{name: "b.log", size: 200, isDir: false},
		{name: "sub", size: 0, isDir: true},
	}
	srv := newServerWithFakeSSHAndSFTP(t, f)

	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"path": "/data",
	})
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Entries []struct {
			Name  string `json:"name"`
			Size  int64  `json:"size"`
			IsDir bool   `json:"isDir"`
		} `json:"entries"`
		Parent string `json:"parent"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Entries) != 3 {
		t.Errorf("期望 3 条，得到 %d", len(got.Entries))
	}
	if got.Parent != "/" {
		t.Errorf("parent 应为 /，得到 %s", got.Parent)
	}
}

func TestFilesList_NotFound_FakeSFTP(t *testing.T) {
	srv := newServerWithFakeSSHAndSFTP(t, newFakeSftpBasic())

	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"path": "/missing",
	})
	if w.Code != 502 {
		t.Errorf("期望 502，得到 %d body=%s", w.Code, w.Body.String())
	}
}

func TestFilesList_RelativePath_FakeSFTP(t *testing.T) {
	srv := newServerWithFakeSSHAndSFTP(t, newFakeSftpBasic())

	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"path": "data/x", // 相对路径，应在到达 SFTP 前被拒
	})
	if w.Code != 400 {
		t.Errorf("相对路径应被拒，期望 400，得到 %d", w.Code)
	}
}

// TestFilesDownload_EndToEnd_FakeSFTP 完整链路：
// 启动下载 → 拿到 id → 订阅 events → 拿到 file_start / file_done / done
func TestFilesDownload_EndToEnd_FakeSFTP(t *testing.T) {
	srv, _, _, dir := newTestServer(t)
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv = newTestServerWithFakeSSH(t, port)
	withFakeSFTP(t, func() *fakeSftpClient {
		f := newFakeSftpBasic()
		f.files["/data/a.log"] = []byte("content of a")
		f.files["/data/b.log"] = []byte("content of b")
		return f
	}())
	_ = dir

	// 1) 启动下载
	w := doRequest(srv, "POST", "/api/files/download", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"paths": []string{"/data/a.log", "/data/b.log"},
	})
	if w.Code != 200 {
		t.Fatalf("启动下载失败: %d body=%s", w.Code, w.Body.String())
	}
	var dl struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &dl); err != nil {
		t.Fatal(err)
	}
	if dl.ID == "" {
		t.Fatal("id 为空")
	}

	// 2) 订阅 SSE events
	eventsW := doRequest(srv, "GET", "/api/files/download/"+dl.ID+"/events", nil)
	if eventsW.Code != 200 {
		t.Fatalf("events 应 200，得到 %d", eventsW.Code)
	}
	body := eventsW.Body.String()
	for _, w := range []string{`"kind":"file_start"`, `"kind":"file_done"`, `"kind":"done"`} {
		if !strings.Contains(body, w) {
			t.Errorf("events 应包含 %s: %s", w, body)
		}
	}
	if strings.Count(body, `"kind":"file_start"`) < 2 {
		t.Errorf("应有 ≥2 次 file_start: %s", body)
	}
	if strings.Count(body, `"kind":"file_done"`) < 2 {
		t.Errorf("应有 ≥2 次 file_done: %s", body)
	}
	if strings.Count(body, `"kind":"done"`) != 1 {
		t.Errorf("应只有 1 次 done: %s", body)
	}
}

// TestFilesDownload_Cancel_BeforeStart 测试对一个不存在 task 调 cancel（idempotent）。
func TestFilesDownload_Cancel_NotFound(t *testing.T) {
	srv := newServerWithFakeSSHAndSFTP(t, newFakeSftpBasic())

	w := doRequest(srv, "POST", "/api/files/download/dl-doesnotexist/cancel", nil)
	if w.Code != 404 {
		t.Errorf("不存在 id 应 404，得到 %d", w.Code)
	}
}

// TestFilesDownload_Events_AlreadyFinished 测试"订阅时已结束"的早期返回分支。
// 流程：启动 → 等 done → 订阅 events → 拿 done 行（不带 file_start 等）。
func TestFilesDownload_Events_AlreadyFinished(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)
	withFakeSFTP(t, func() *fakeSftpClient {
		f := newFakeSftpBasic()
		f.files["/data/a.log"] = []byte("hi")
		return f
	}())

	w := doRequest(srv, "POST", "/api/files/download", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"paths": []string{"/data/a.log"},
	})
	var dl struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &dl)

	// 先订阅"普通"events，等它结束
	first := doRequest(srv, "GET", "/api/files/download/"+dl.ID+"/events", nil)
	if first.Code != 200 {
		t.Fatalf("first events 失败: %d", first.Code)
	}
	_ = first.Body.String()

	// 再订阅一次（已结束路径）
	second := doRequest(srv, "GET", "/api/files/download/"+dl.ID+"/events", nil)
	if second.Code != 200 {
		t.Errorf("二次订阅应 200，得到 %d", second.Code)
	}
	body := second.Body.String()
	if !strings.Contains(body, `"kind":"done"`) {
		t.Errorf("二次订阅应立即拿到 done: %s", body)
	}
}

// TestFilesDownload_ZipFlow 启动一个 ≥2 文件 + zip=true 的下载，确认 zip 出现。
func TestFilesDownload_ZipFlow(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv, _, _, dir := newTestServer(t)
	srv = newTestServerWithFakeSSH(t, port)
	withFakeSFTP(t, func() *fakeSftpClient {
		f := newFakeSftpBasic()
		f.files["/data/a.log"] = []byte(strings.Repeat("a", 50))
		f.files["/data/b.log"] = []byte(strings.Repeat("b", 50))
		return f
	}())

	w := doRequest(srv, "POST", "/api/files/download", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"paths": []string{"/data/a.log", "/data/b.log"},
		"zip":   true,
	})
	if w.Code != 200 {
		t.Fatalf("启动失败: %d body=%s", w.Code, w.Body.String())
	}
	var dl struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &dl)

	eventsW := doRequest(srv, "GET", "/api/files/download/"+dl.ID+"/events", nil)
	body := eventsW.Body.String()

	// 找下载目录里 .zip：用 downloads list 接口拿 dir
	listW := doRequest(srv, "GET", "/api/downloads/list", nil)
	var list struct {
		Files []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"files"`
		Folder string `json:"folder"`
	}
	_ = json.Unmarshal(listW.Body.Bytes(), &list)
	foundZip := false
	for _, f := range list.Files {
		if f.Kind == "zip" || strings.HasSuffix(f.Name, ".zip") {
			foundZip = true
			break
		}
	}
	// 兜底：Walk 整个下载目录（dir 是初次 newTestServer 的 dir，
	// 但 newTestServerWithFakeSSH 又开了一个 tmpDir；两者不同；用 list.Folder 才对）
	_ = dir
	if !foundZip && list.Folder != "" {
		filepath.Walk(list.Folder, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(p, ".zip") {
				foundZip = true
			}
			return nil
		})
	}
	if !foundZip {
		t.Errorf("下载后应有 .zip 文件，body=%s", body)
	}
}

// 抑制 unused
var _ = sftpclient.New

// TestFilesList_FreeBrowserDisabled 验证 app.enable_free_file_browser=false 时
// /api/files/* 全部返回 403（P2-11 修复）。
//
// 流程：
//   - 用基线 fake SFTP（有 /data 目录 + a.log），正常应能 list；
//   - 通过 Manager.Replace 切换 cfg.App.EnableFreeFileBrowser = false；
//   - 再次 list 应拿到 403，body 含"已在配置中关闭"。
func TestFilesList_FreeBrowserDisabled(t *testing.T) {
	f := newFakeSftpBasic()
	f.dirs["/data"] = []fakeDirEntry{{name: "a.log", size: 1, isDir: false}}
	srv, mgr, _, _ := newTestServer(t)

	// 把基线 cfg 的指针拿到，构造一份新的 cfg 关掉文件浏览器
	cur := mgr.Get()
	disabled := false
	newCfg := *cur // 浅拷贝
	newCfg.App = cur.App
	newCfg.App.EnableFreeFileBrowser = &disabled

	if err := mgr.Replace(&newCfg); err != nil {
		t.Fatalf("替换 cfg 失败: %v", err)
	}

	// /api/files/list 应 403
	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "x",
		"path": "/data",
	})
	if w.Code != 403 {
		t.Fatalf("文件浏览器关闭时 list 应 403，实际 %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "已在配置中关闭") {
		t.Errorf("错误信息应说明开关已关闭: %s", w.Body.String())
	}
	// _ = f / _ = srv（fakeSftp 没启用，但 403 路径根本不会走到 SFTP 层）
	_ = f
}

// TestFilesDownload_FreeBrowserDisabled 验证 download 接口在开关关闭时也返回 403。
func TestFilesDownload_FreeBrowserDisabled(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)

	cur := mgr.Get()
	disabled := false
	newCfg := *cur
	newCfg.App = cur.App
	newCfg.App.EnableFreeFileBrowser = &disabled

	if err := mgr.Replace(&newCfg); err != nil {
		t.Fatalf("替换 cfg 失败: %v", err)
	}

	w := doRequest(srv, "POST", "/api/files/download", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "x",
		"paths": []string{"/data/a.log"},
	})
	if w.Code != 403 {
		t.Fatalf("download 应 403，实际 %d body=%s", w.Code, w.Body.String())
	}
}

// TestConfig_FreeBrowserDefault 验证 config.AppConfig.FreeFileBrowserEnabled 默认 true。
func TestConfig_FreeBrowserDefault(t *testing.T) {
	// 不设 EnableFreeFileBrowser（nil）→ 应默认 true
	a := config.AppConfig{}
	if !a.FreeFileBrowserEnabled() {
		t.Fatal("默认应启用文件浏览器（向后兼容）")
	}
	// 显式设 false → 关闭
	disabled := false
	b := config.AppConfig{EnableFreeFileBrowser: &disabled}
	if b.FreeFileBrowserEnabled() {
		t.Fatal("显式 false 应禁用文件浏览器")
	}
	// 显式设 true → 启用
	enabled := true
	c := config.AppConfig{EnableFreeFileBrowser: &enabled}
	if !c.FreeFileBrowserEnabled() {
		t.Fatal("显式 true 应启用文件浏览器")
	}
}
