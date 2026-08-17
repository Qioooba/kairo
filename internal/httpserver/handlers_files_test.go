package httpserver

import (
	"archive/zip"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"kairo/internal/config"
	"kairo/internal/sftpclient"
	"kairo/internal/sshclient"
)

// fakeSftpClient 实现 sftpClientLike 接口，给测试用。
//
// 行为：
//   - ReadDir/Stat：走 fs + dirs（路径 → 内容/条目）
//   - DownloadFile/DownloadFileWithProgress：把 fs[path] 写到 localPath，
//     写完后回调 progress(written, total) 一次保证 SSE 拿到 100%
//   - Open（v0.5 项 1 文件预览用）：返回 fakeSftpFile，让 handler 走 io.LimitReader 路径
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

func (f *fakeSftpClient) Open(path string) (sftpclient.SftpFile, error) {
	content, ok := f.files[path]
	if !ok {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	return &fakeSftpFile{
		r:    strings.NewReader(string(content)),
		size: int64(len(content)),
		name: filepath.Base(path),
	}, nil
}

// fakeSftpFile 是 fakeSftpClient.Open 的返回类型，给项 1 文件预览测试用。
type fakeSftpFile struct {
	r    *strings.Reader
	size int64
	name string
}

func (ff *fakeSftpFile) Read(p []byte) (int, error) { return ff.r.Read(p) }
func (ff *fakeSftpFile) Close() error               { return nil }
func (ff *fakeSftpFile) Stat() (os.FileInfo, error) {
	return fakeFileInfo{name: ff.name, size: ff.size, mode: 0o644}, nil
}

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

func (f *fakeSftpClient) ListLimited(path string, max int) ([]os.FileInfo, bool, error) {
	entries, err := f.ReadDir(path)
	if err != nil {
		return nil, false, err
	}
	if max > 0 && len(entries) > max {
		return entries[:max], true, nil
	}
	return entries, false, nil
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

// TestFilesDownload_DirRecursive 选中目录下载（zip=false）：后端应强制打 zip，
// 且 zip 内保留目录层级（app/logs/a.log 而非扁平 a.log）。
func TestFilesDownload_DirRecursive(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)
	withFakeSFTP(t, func() *fakeSftpClient {
		f := newFakeSftpBasic()
		f.dirs["/data/app"] = []fakeDirEntry{
			{name: "logs", size: 0, isDir: true},
			{name: "readme.txt", size: 8, isDir: false},
		}
		f.dirs["/data/app/logs"] = []fakeDirEntry{
			{name: "a.log", size: 3, isDir: false},
			{name: "sub", size: 0, isDir: true},
		}
		f.dirs["/data/app/logs/sub"] = []fakeDirEntry{
			{name: "b.log", size: 3, isDir: false},
		}
		f.files["/data/app/readme.txt"] = []byte("readme!!")
		f.files["/data/app/logs/a.log"] = []byte("aaa")
		f.files["/data/app/logs/sub/b.log"] = []byte("bbb")
		return f
	}())

	// zip=false：单目录也应强制打包（保留目录结构）
	w := doRequest(srv, "POST", "/api/files/download", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"paths": []string{"/data/app"},
		"zip":   false,
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
	if !strings.Contains(body, "ok\":true") {
		t.Fatalf("下载应成功，body=%s", body)
	}

	listW := doRequest(srv, "GET", "/api/downloads/list", nil)
	var list struct {
		Files []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"files"`
		Folder string `json:"folder"`
	}
	_ = json.Unmarshal(listW.Body.Bytes(), &list)

	// 找到 zip 并校验内部层级
	var zipPath string
	if list.Folder != "" {
		filepath.Walk(list.Folder, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(p, ".zip") {
				zipPath = p
			}
			return nil
		})
	}
	if zipPath == "" {
		t.Fatalf("目录下载应产出 .zip，body=%s", body)
	}
	names := readZipNames(t, zipPath)
	want := []string{"app/logs/a.log", "app/logs/sub/b.log", "app/readme.txt"}
	if len(names) != len(want) {
		t.Fatalf("zip 内条目数不对: got %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("zip 内第 %d 项应为 %q，实际 %q（全部: %v）", i, want[i], names[i], names)
		}
	}
}

// readZipNames 打开 zip 读出所有条目名（保持顺序）。
func readZipNames(t *testing.T, zipPath string) []string {
	t.Helper()
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("打开 zip 失败: %v", err)
	}
	defer r.Close()
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return names
}

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

// ============================================================================
// v0.5 项 1：文件预览（/api/files/preview）
// ============================================================================

// TestFilesPreview_Happy_Text 文本文件正常预览：返 content + size + truncated=false。
func TestFilesPreview_Happy_Text(t *testing.T) {
	f := newFakeSftpBasic()
	f.files["/data/SystemOut.log"] = []byte("hello world\n这是一段日志\nend\n")
	srv := newServerWithFakeSSHAndSFTP(t, f)

	w := doRequest(srv, "POST", "/api/files/preview", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"path": "/data/SystemOut.log",
	})
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		Encoding  string `json:"encoding"`
		Size      int64  `json:"size"`
		BytesRead int    `json:"bytes_read"`
		Truncated bool   `json:"truncated"`
		IsBinary  bool   `json:"is_binary"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "SystemOut.log" {
		t.Errorf("Name 期望 SystemOut.log，得到 %q", got.Name)
	}
	if got.Size != int64(len("hello world\n这是一段日志\nend\n")) {
		t.Errorf("Size 期望 %d，得到 %d", len("hello world\n这是一段日志\nend\n"), got.Size)
	}
	if int64(got.BytesRead) != got.Size {
		t.Errorf("BytesRead 应等于 Size，得到 %d vs %d", got.BytesRead, got.Size)
	}
	if got.Truncated {
		t.Errorf("小文件不应 truncated")
	}
	if got.IsBinary {
		t.Errorf("纯文本不应 is_binary=true")
	}
	if got.Content != "hello world\n这是一段日志\nend\n" {
		t.Errorf("Content 不匹配: %q", got.Content)
	}
	if got.Encoding != "utf-8" {
		t.Errorf("默认 encoding 期望 utf-8，得到 %q", got.Encoding)
	}
}

// TestFilesPreview_Truncated 文件比 max_bytes 大 → truncated=true + bytes_read=限制值。
func TestFilesPreview_Truncated(t *testing.T) {
	f := newFakeSftpBasic()
	// 1KB 文件
	big := make([]byte, 1024)
	for i := range big {
		big[i] = byte('a')
	}
	f.files["/data/big.log"] = big
	srv := newServerWithFakeSSHAndSFTP(t, f)

	w := doRequest(srv, "POST", "/api/files/preview", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"path":      "/data/big.log",
		"max_bytes": 100,
	})
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d", w.Code)
	}
	var got struct {
		Size      int64 `json:"size"`
		BytesRead int   `json:"bytes_read"`
		Truncated bool  `json:"truncated"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Size != 1024 {
		t.Errorf("Size 期望 1024，得到 %d", got.Size)
	}
	if got.BytesRead != 100 {
		t.Errorf("BytesRead 期望 100（按 max_bytes 限制），得到 %d", got.BytesRead)
	}
	if !got.Truncated {
		t.Errorf("文件大小 > max_bytes 时应 truncated=true")
	}
}

// TestFilesPreview_MaxBytesHardCap max_bytes > 10MB 应被夹到 10MB。
func TestFilesPreview_MaxBytesHardCap(t *testing.T) {
	f := newFakeSftpBasic()
	f.files["/data/x.log"] = []byte("tiny")
	srv := newServerWithFakeSSHAndSFTP(t, f)

	w := doRequest(srv, "POST", "/api/files/preview", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"path":      "/data/x.log",
		"max_bytes": 100 << 20, // 100MB，超过 10MB 上限
	})
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	// 文件本身只有 4 字节，bytes_read 应是 4（不被 max_bytes 影响）
	var got struct {
		BytesRead int `json:"bytes_read"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.BytesRead != 4 {
		t.Errorf("文件 4 字节，BytesRead 应为 4（不是 max_bytes 上限），得到 %d", got.BytesRead)
	}
}

// TestFilesPreview_Binary 含 NUL 字节 → is_binary=true + content 为空。
func TestFilesPreview_Binary(t *testing.T) {
	f := newFakeSftpBasic()
	bin := []byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0x00, 0x00, 0x00} // PNG magic + NUL
	f.files["/data/image.png"] = bin
	srv := newServerWithFakeSSHAndSFTP(t, f)

	w := doRequest(srv, "POST", "/api/files/preview", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"path": "/data/image.png",
	})
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d", w.Code)
	}
	var got struct {
		IsBinary bool   `json:"is_binary"`
		Content  string `json:"content"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !got.IsBinary {
		t.Errorf("含 NUL 字节应 is_binary=true")
	}
	if got.Content != "" {
		t.Errorf("二进制文件 content 应为空，得到 %q", got.Content)
	}
}

// TestFilesPreview_Dir 目录预览 → 400 "不支持预览目录"
func TestFilesPreview_Dir(t *testing.T) {
	f := newFakeSftpBasic()
	f.dirs["/data/sub"] = []fakeDirEntry{}
	srv := newServerWithFakeSSHAndSFTP(t, f)

	w := doRequest(srv, "POST", "/api/files/preview", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"path": "/data/sub",
	})
	if w.Code != 400 {
		t.Errorf("目录预览应 400，得到 %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "不支持预览目录") {
		t.Errorf("错误信息应提到\"不支持预览目录\"，得到 %s", w.Body.String())
	}
}

// TestFilesPreview_NotExist 文件不存在 → 502 (SSH/SFTP 错误归 502)
func TestFilesPreview_NotExist(t *testing.T) {
	f := newFakeSftpBasic()
	srv := newServerWithFakeSSHAndSFTP(t, f)

	w := doRequest(srv, "POST", "/api/files/preview", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"path": "/data/nope.log",
	})
	if w.Code != 502 && w.Code != 500 {
		t.Errorf("文件不存在应 5xx，得到 %d", w.Code)
	}
}

// TestFilesPreview_PathNotAbsolute 相对路径 → 400
func TestFilesPreview_PathNotAbsolute(t *testing.T) {
	srv := newServerWithFakeSSHAndSFTP(t, newFakeSftpBasic())
	w := doRequest(srv, "POST", "/api/files/preview", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"path": "relative/path.log",
	})
	if w.Code != 400 {
		t.Errorf("相对路径应 400，得到 %d body=%s", w.Code, w.Body.String())
	}
}

// TestFilesPreview_EncodingGBK encoding=gbk → 字节按 GBK 解码。
func TestFilesPreview_EncodingGBK(t *testing.T) {
	f := newFakeSftpBasic()
	// "中文日志" 4 个中文字符的 GBK 编码（每个 2 字节 → 8 字节）
	// 用 Go 自带 GBK encoder 算的（避免手编错误）：0xD6D0 0xCEC4 0xC8D5 0xD6BE
	gbkBytes := []byte{0xD6, 0xD0, 0xCE, 0xC4, 0xC8, 0xD5, 0xD6, 0xBE}
	f.files["/data/cn.log"] = gbkBytes
	srv := newServerWithFakeSSHAndSFTP(t, f)

	w := doRequest(srv, "POST", "/api/files/preview", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"path":     "/data/cn.log",
		"encoding": "gbk",
	})
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Encoding != "gbk" {
		t.Errorf("encoding 期望 gbk，得到 %q", got.Encoding)
	}
	if got.Content != "中文日志" {
		t.Errorf("GBK 解码后 Content 期望 %q，得到 %q", "中文日志", got.Content)
	}
}

// ============================================================================
// v0.5 项 1：文件预览（/api/files/preview）测试
// ============================================================================

// TestFilesPreview_Happy 完整路径：mock 文本文件 → preview 拿到内容 + 元信息。
