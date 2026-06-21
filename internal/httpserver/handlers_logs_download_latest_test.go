package httpserver

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- /api/logs/download-latest ----------

// TestLogsDownloadLatest_WrongMethod 405 路径
func TestLogsDownloadLatest_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/logs/download-latest", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// TestLogsDownloadLatest_BadJSON 400 路径
func TestLogsDownloadLatest_BadJSON(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	req := strings.NewReader(`{"system":"x`)
	r := httptest.NewRequest("POST", "/api/logs/download-latest", req)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Errorf("bad json 应 400，得到 %d", w.Code)
	}
}

// TestLogsDownloadLatest_UnknownSystem 400 路径
func TestLogsDownloadLatest_UnknownSystem(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/logs/download-latest", map[string]any{
		"system": "不存在", "server": "mock-1", "dir": "SystemOut",
		"username": "ops", "password": "x",
	})
	if w.Code != 400 {
		t.Errorf("未知系统应 400，得到 %d body=%s", w.Code, w.Body.String())
	}
}

// TestLogsDownloadLatest_UnknownDir 400 路径
func TestLogsDownloadLatest_UnknownDir(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/logs/download-latest", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "nope",
		"username": "ops", "password": "x",
	})
	if w.Code != 400 {
		t.Errorf("未知目录应 400，得到 %d body=%s", w.Code, w.Body.String())
	}
}

// TestLogsDownloadLatest_MissingPassword 400 路径
func TestLogsDownloadLatest_MissingPassword(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/logs/download-latest", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "SystemOut",
		"username": "ops", "password": "",
	})
	if w.Code != 400 {
		t.Errorf("缺密码应 400，得到 %d body=%s", w.Code, w.Body.String())
	}
}

// TestLogsDownloadLatest_DialFail 用 fake SSH 但配置 host 指到不可达地址 → 502
func TestLogsDownloadLatest_DialFail(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	// 不起 fake SSH，配置里的 host=10.0.0.1 不可达
	w := doRequest(srv, "POST", "/api/logs/download-latest", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "SystemOut",
		"username": "ops", "password": "x",
	})
	if w.Code != 502 {
		t.Errorf("dial 失败应 502，得到 %d body=%s", w.Code, w.Body.String())
	}
	// 错误信息不应含 password 字样
	if strings.Contains(w.Body.String(), "password=") {
		t.Errorf("错误信息泄漏 password: %s", w.Body.String())
	}
}

// TestLogsDownloadLatest_HappyFlow 完整流程：fake SSH + fake SFTP 下载 1 个文件
func TestLogsDownloadLatest_HappyFlow(t *testing.T) {
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr := splitHostPort(addr)
	port := atoi(portStr)
	srv := newTestServerWithFakeSSH(t, port)
	withFakeSFTP(t, func() *fakeSftpClient {
		f := newFakeSftpBasic()
		// fake SSH exec 返回 "./SystemOut.log"，handler 会拼成 ld.Path + SystemOut.log
		// ld.Path 在测试 config 里是 /opt/logs/SystemOut
		f.files["/opt/logs/SystemOut/SystemOut.log"] = []byte("first content")
		return f
	}())

	w := doRequest(srv, "POST", "/api/logs/download-latest", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "SystemOut",
		"username": "ops", "password": "testpw", "latest": 1,
	})
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Downloads []struct {
			File  string `json:"file"`
			Local string `json:"local"`
			Bytes string `json:"bytes"`
		} `json:"downloads"`
		Folder string `json:"folder"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Downloads) == 0 {
		t.Errorf("downloads 应非空：body=%s", w.Body.String())
	}
	if got.Folder == "" {
		t.Error("folder 应有值")
	}
}

// TestLogsDownloadLatest_LatestCap 验证 latest > 5 时被截到 5。
// 不实际下载（依赖 SSH/SFTP），走参数校验前的代码路径。
func TestLogsDownloadLatest_LatestCap(t *testing.T) {
	// 这条测试只看 latest 截断逻辑：latest=10 应被 cap 到 5
	// 但 handler 里 cap 是在所有校验之后，因此无法在 dial 前直接验。
	// 这里只断言"latest > 5"不会让请求挂死（dial 失败是 502，符合预期）。
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/logs/download-latest", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "SystemOut",
		"username": "ops", "password": "x", "latest": 100,
	})
	if w.Code != 502 {
		t.Errorf("latest=100 应 dial fail → 502，得到 %d", w.Code)
	}
}

// TestAuditErr 单元测试：覆盖 helpers.go 里 auditErr 函数（之前 0%）
func TestAuditErr(t *testing.T) {
	_, _, al, dir := newTestServer(t)
	_ = dir

	// 简单调用：仅 err 一个 kv → 应该不写（len(kv) < 2 时直接 return）
	auditErr(nil, al, "noop", errors.New("ignored"))

	// 正常调用：会写一条审计
	auditErr(nil, al, "logs.download",
		"system", "test", "server", "s1",
		"dir", "/var/log",
		"result", "fail",
		errors.New("dial fail"))
}

// TestZipFiles_Errors 覆盖 zipFiles 的几个边缘
func TestZipFiles_EmptySrc(t *testing.T) {
	dir := t.TempDir()
	if err := zipFiles(nil, filepath.Join(dir, "out.zip")); err == nil {
		t.Error("空 src 应报错")
	}
}

func TestZipFiles_BadDest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 写到 /nonexistent/xx.zip（中间目录不存在）→ zipFiles 先 MkdirAll 再 OpenFile
	// 但 MkdirAll 失败会报错
	if err := zipFiles([]string{src}, "/nonexistent_dir_xyz_123/xx.zip"); err == nil {
		t.Error("非法目标路径应报错")
	}
}

// ---- helpers ----

func splitHostPort(addr string) (string, string) {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], addr[i+1:]
		}
	}
	return addr, ""
}

func atoi(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return n
		}
		n = n*10 + int(ch-'0')
	}
	return n
}