package httpserver

// sftp_f_identity_handlers_test.go — Batch F（OTH-05）HTTP 身份契约失败回归。
//
// 审核文档指出的三层断点之一：HTTP 层只认"/"开头的展示路径，
// 前端即使把后端发回来的 `kairo-raw:...` 身份塞进 path 字段也会被 400 拒绝。
// 本文件的断言全部用"后端真正收到的路径字节"作为依据：
//   - 列目录条目必须对**所有**条目（含 ASCII）发 path_id；
//   - 预览/下载必须能只用 path_id 定位原始物理条目；
//   - 非法身份（换行、非绝对、非 token 的身份字段）必须继续 400，不能放宽校验。

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"kairo/internal/sftpclient"
	"kairo/internal/sshclient"
)

// sftpFEntry 是 sftpFClient 的目录条目。
type sftpFEntry struct {
	name  string
	size  int64
	isDir bool
}

// sftpFClient 是 sftpClientLike 的测试替身，记录后端收到的路径。
type sftpFClient struct {
	files             map[string][]byte
	dirs              map[string][]sftpFEntry
	openedPaths       []string
	stattedPaths      []string
	downloadedRemotes []string
}

func sftpFNewBasic() *sftpFClient {
	return &sftpFClient{files: make(map[string][]byte), dirs: make(map[string][]sftpFEntry)}
}

func (f *sftpFClient) Close() error { return nil }

func (f *sftpFClient) ReadDir(p string) ([]os.FileInfo, error) {
	entries, ok := f.dirs[p]
	if !ok {
		return nil, &os.PathError{Op: "readdir", Path: p, Err: os.ErrNotExist}
	}
	out := make([]os.FileInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, fakeFileInfo{name: e.name, size: e.size, isDir: e.isDir, mode: 0o644})
	}
	return out, nil
}

func (f *sftpFClient) ListLimited(p string, max int) ([]os.FileInfo, bool, error) {
	entries, err := f.ReadDir(p)
	if err != nil {
		return nil, false, err
	}
	if max > 0 && len(entries) > max {
		return entries[:max], true, nil
	}
	return entries, false, nil
}

func (f *sftpFClient) Stat(p string) (os.FileInfo, error) {
	f.stattedPaths = append(f.stattedPaths, p)
	if _, ok := f.dirs[p]; ok {
		return fakeFileInfo{name: filepath.Base(p), isDir: true, mode: os.ModeDir | 0o755}, nil
	}
	if content, ok := f.files[p]; ok {
		return fakeFileInfo{name: filepath.Base(p), size: int64(len(content)), mode: 0o644}, nil
	}
	return nil, &os.PathError{Op: "stat", Path: p, Err: os.ErrNotExist}
}

func (f *sftpFClient) Open(p string) (sftpclient.SftpFile, error) {
	f.openedPaths = append(f.openedPaths, p)
	content, ok := f.files[p]
	if !ok {
		return nil, &os.PathError{Op: "open", Path: p, Err: os.ErrNotExist}
	}
	return &fakeSftpFile{r: strings.NewReader(string(content)), size: int64(len(content)), name: filepath.Base(p)}, nil
}

func (f *sftpFClient) DownloadFile(remote, local string) (int64, error) {
	return f.downloadWith(remote, local, nil)
}

func (f *sftpFClient) DownloadFileWithProgress(remote, local string, progress func(int64, int64)) (int64, error) {
	return f.downloadWith(remote, local, progress)
}

func (f *sftpFClient) downloadWith(remote, local string, progress func(int64, int64)) (int64, error) {
	f.downloadedRemotes = append(f.downloadedRemotes, remote)
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
	return n, nil
}

// sftpFWithFake 启一个假 SSH server + 把 sftpDialer 换成自定义替身。
// 为什么不用 withFakeSFTP：那个 helper 的形参是 *fakeSftpClient（本文件要记录
// 原始字节路径，需要自己的替身）。
func sftpFWithFake(t *testing.T, cli sftpClientLike) *Server {
	t.Helper()
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)
	orig := sftpDialer
	sftpDialer = func(_ *sshclient.Client) (sftpClientLike, error) { return cli, nil }
	t.Cleanup(func() { sftpDialer = orig })
	return srv
}

// sftpFCreds 是假 SSH server 的固定凭据。
func sftpFCreds(body map[string]any) map[string]any {
	body["system"] = "信贷生产"
	body["server"] = "mock-1"
	body["username"] = "ops"
	body["password"] = "testpw"
	return body
}

// TestSFTPF_ListEntriesAlwaysCarryPathID
// 身份必须覆盖所有条目：ASCII / UTF-8 / GBK。只给非 UTF-8 条目发 path_id
// 会让同显示名的 UTF-8 兄弟条目没有可区别于展示路径的身份。
func TestSFTPF_ListEntriesAlwaysCarryPathID(t *testing.T) {
	f := sftpFNewBasic()
	f.dirs["/data"] = []sftpFEntry{
		{name: "app.log", size: 3},
		{name: "中文.log", size: 5},
	}
	srv := sftpFWithFake(t, f)

	w := doRequest(srv, "POST", "/api/files/list", sftpFCreds(map[string]any{"path": "/data"}))
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Entries []struct {
			Name   string `json:"name"`
			PathID string `json:"path_id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("期望 2 条条目，得到 %d", len(got.Entries))
	}
	for _, e := range got.Entries {
		if e.PathID == "" {
			t.Errorf("条目 %q 没有 path_id（HTTP 层必须为所有条目发身份）", e.Name)
		}
	}
}

// TestSFTPF_PreviewAcceptsPathIdentity
// 只给 path_id（不给 path）也必须 200，并且真正打开的是身份解码后的原始字节路径。
func TestSFTPF_PreviewAcceptsPathIdentity(t *testing.T) {
	rawPath := "/data/" + sftpFGBKZhongWen + ".log"
	f := sftpFNewBasic()
	f.files[rawPath] = []byte("gbk-content")
	srv := sftpFWithFake(t, f)

	w := doRequest(srv, "POST", "/api/files/preview", sftpFCreds(map[string]any{
		"path_id": sftpclient.EncodePathIdentity(rawPath),
	}))
	if w.Code != 200 {
		t.Fatalf("只用 path_id 的预览请求被拒: %d body=%s", w.Code, w.Body.String())
	}
	if len(f.openedPaths) != 1 || f.openedPaths[0] != rawPath {
		t.Fatalf("预览实际打开的路径 = %q，期望原始字节路径 %q", f.openedPaths, rawPath)
	}
}

// TestSFTPF_MalformedPathIdentityRejected：非法身份继续 400，不能放宽。
func TestSFTPF_MalformedPathIdentityRejected(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"token 里带换行", map[string]any{"path_id": sftpclient.EncodePathIdentity("/data/x\n.log")}},
		{"token 非十六进制", map[string]any{"path_id": sftpclient.PathIdentityPrefix + "zzzz"}},
		{"身份字段是相对路径", map[string]any{"path_id": "data/x.log"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := sftpFNewBasic()
			f.files["/data/x.log"] = []byte("x")
			srv := sftpFWithFake(t, f)
			w := doRequest(srv, "POST", "/api/files/preview", sftpFCreds(tc.body))
			if w.Code != 400 {
				t.Fatalf("非法身份应 400，得到 %d body=%s", w.Code, w.Body.String())
			}
			if len(f.openedPaths) != 0 {
				t.Fatalf("非法身份却访问了后端: %q", f.openedPaths)
			}
		})
	}
}

// TestSFTPF_DownloadUsesPathIdentityForRemoteAccess
// 下载必须按身份访问原始物理条目，展示路径只用于命名/进度键。
func TestSFTPF_DownloadUsesPathIdentityForRemoteAccess(t *testing.T) {
	rawPath := "/data/" + sftpFGBKZhongWen + ".log"
	displayPath := "/data/" + sftpFUTF8ZhongWen + ".log"
	f := sftpFNewBasic()
	f.files[rawPath] = []byte("gbk-content")
	// 展示路径同名但不同物理条目的内容故意不同：下载错了内容会立刻暴露。
	f.files[displayPath] = []byte("WRONG-physical-entry")
	srv := sftpFWithFake(t, f)

	w := doRequest(srv, "POST", "/api/files/download", sftpFCreds(map[string]any{
		"paths":    []string{displayPath},
		"path_ids": []string{sftpclient.EncodePathIdentity(rawPath)},
	}))
	if w.Code != 200 {
		t.Fatalf("带身份的下载启动失败: %d body=%s", w.Code, w.Body.String())
	}
	var dl struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &dl); err != nil {
		t.Fatal(err)
	}
	if dl.ID == "" {
		t.Fatal("下载 id 为空")
	}
	// 订阅 events 会阻塞到任务结束，确保下载已完成再断言。
	eventsW := doRequest(srv, "GET", "/api/files/download/"+dl.ID+"/events", nil)
	if eventsW.Code != 200 {
		t.Fatalf("events 应 200，得到 %d", eventsW.Code)
	}
	body, _ := io.ReadAll(eventsW.Body)
	if !strings.Contains(string(body), `"kind":"done"`) {
		t.Fatalf("下载未正常结束: %s", string(body))
	}
	if len(f.downloadedRemotes) != 1 || f.downloadedRemotes[0] != rawPath {
		t.Fatalf("下载实际访问的远端路径 = %q (hex %x)，期望原始字节路径 %q (hex %x)",
			f.downloadedRemotes, []byte(strings.Join(f.downloadedRemotes, ",")), rawPath, []byte(rawPath))
	}
}

// 这两个常量与 sftpclient 测试里的 UTF-8 / GBK 字节保持一致，
// 但 httpserver 包不能引用其测试文件的私有常量，所以本地再声明一次。
const (
	sftpFUTF8ZhongWen = "中文"
	sftpFGBKZhongWen  = "\xd6\xd0\xce\xc4"
)
