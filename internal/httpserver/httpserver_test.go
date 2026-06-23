package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"ops-toolbox/internal/audit"
	"ops-toolbox/internal/config"
	"ops-toolbox/internal/logquery"
	"ops-toolbox/internal/tailmgr"
)

// ---------- 测试辅助 ----------

// newTestServer 构造一个最小可用的 Server，配置 + 1 个系统 / 1 台服务器 / 1 个 log_dir。
// downloadDir / logDir 落在 t.TempDir() 下；audit 也在那里写。
func newTestServer(t *testing.T) (*Server, *config.Manager, *audit.Logger, string) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		App: config.AppConfig{
			Name:        "TestBox",
			Host:        "127.0.0.1",
			Port:        18080,
			DownloadDir: "downloads",
			LogDir:      "logs",
			DataDir:     "data",
		},
		Systems: []config.SystemConfig{
			{
				Name: "信贷生产",
				Servers: []config.ServerConfig{
					{
						Name:     "mock-1",
						Host:     "10.0.0.1",
						Port:     22,
						Username: "ops",
						AuthType: "password",
						LogDirs: []config.LogDirEntry{
							{
								Name:     "SystemOut",
								Path:     "/opt/logs/SystemOut",
								Patterns: []string{"*.log"},
								Encoding: "utf-8",
							},
						},
					},
				},
			},
		},
		Search: config.SearchConfig{
			DefaultLatestFiles:  3,
			MaxMatches:          200,
			DefaultContextLines: 30,
			TimeoutSeconds:      30,
			MaxConcurrency:      2,
		},
	}
	cfg.Defaults()
	if err := cfg.ResolvePaths(tmp); err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(tmp, "config.yaml")
	mgr := config.NewManager(cfg, cfgPath)

	al, err := audit.New(cfg.LogDir(), "audit.log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = al.Close() })

	// 嵌入 fs 用真实 web/ 目录（测试用真实 index.html）
	webFS := os.DirFS(filepath.Join("..", "..", "web"))
	srv := New(mgr, al, webFS, tailmgr.NewManager())
	return srv, mgr, al, cfg.DownloadDir()
}

func doRequest(srv *Server, method, path string, body any) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rdr)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

// ---------- 通用辅助函数 ----------

func TestTrim(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel..."},
		{"你好世界", 3, "你好世..."}, // rune-based
		{"   spaced   ", 20, "spaced"},
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := trim(c.in, c.n); got != c.want {
			t.Errorf("trim(%q,%d)=%q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024*1024 + 512*1024, "1.5 MB"},
		{1024 * 1024 * 1024, "1.00 GB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.n); got != c.want {
			t.Errorf("humanBytes(%d)=%q, want %q", c.n, got, c.want)
		}
	}
}

func TestSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{"", "x"},
		{"a/b\\c:d*e?f\"g<h>i|j k", "a_b_c_d_e_f_g_h_i_j_k"},
		{"中文/路径\\分隔", "中文_路径_分隔"},
	}
	for _, c := range cases {
		if got := sanitize(c.in); got != c.want {
			t.Errorf("sanitize(%q)=%q, want %q", c.in, got, c.want)
		}
	}
	// 超长
	long := strings.Repeat("a", 200)
	got := sanitize(long)
	if len(got) > 80 {
		t.Errorf("len=%d, want <=80", len(got))
	}
}

func TestFindLogDir(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	_, server, _ := srv.cur().FindServer("信贷生产", "mock-1")

	if ld, ok := findLogDir(server, "SystemOut"); !ok || ld.Path != "/opt/logs/SystemOut" {
		t.Errorf("by name: %+v %v", ld, ok)
	}
	if ld, ok := findLogDir(server, "/opt/logs/SystemOut"); !ok || ld.Name != "SystemOut" {
		t.Errorf("by path: %+v %v", ld, ok)
	}
	// 空 key + 有 log_dirs → 默认第一个
	if ld, ok := findLogDir(server, ""); !ok || ld.Name != "SystemOut" {
		t.Errorf("default: %+v %v", ld, ok)
	}
	// 空 key + 没 log_dirs → false
	emptySrv := &config.ServerConfig{Name: "x", LogDirs: nil}
	if _, ok := findLogDir(emptySrv, ""); ok {
		t.Error("empty log_dirs + empty key should fail")
	}
	// 找不到
	if _, ok := findLogDir(server, "nonexistent"); ok {
		t.Error("nonexistent should fail")
	}
}

func TestParseSearchOutput(t *testing.T) {
	out := "a.log:10:line1\nb.log:20:line2\n\nbroken_line\na.log:bad:line3\n"
	hits := parseSearchOutput(out, "mock-1", "/opt/logs", nil)
	// 用空 files 也能解析（byName 查不到也不会被用上）
	if len(hits) != 2 {
		t.Errorf("hits: %d", len(hits))
	}
	if hits[0].File != "a.log" || hits[0].LineNo != 10 {
		t.Errorf("hit[0]: %+v", hits[0])
	}
	if hits[1].File != "b.log" || hits[1].LineNo != 20 {
		t.Errorf("hit[1]: %+v", hits[1])
	}
}

func TestParseContextOutput(t *testing.T) {
	// hit=5, before=3 → startLine=2 → 输出 3 行: 2,3,4
	out := "a\nb\nc"
	lines := parseContextOutput(out, 5, 3)
	if len(lines) != 3 {
		t.Fatalf("lines: %d", len(lines))
	}
	if lines[0].LineNo != 2 {
		t.Errorf("first line: %d", lines[0].LineNo)
	}
	// hit=5 不在 2,3,4 范围内 → 没人是 hit
	for _, l := range lines {
		if l.Hit {
			t.Errorf("line %d should not be hit (target=5 not in range)", l.LineNo)
		}
	}
	// 第二个用例：hit=4, before=2 → startLine=2 → 2,3,4 → 第三个 hit
	lines2 := parseContextOutput("a\nb\nc", 4, 2)
	if !lines2[2].Hit {
		t.Errorf("line 4 should be hit: %+v", lines2)
	}
	// 边界：startLine < 1
	lines3 := parseContextOutput("a", 1, 5)
	if lines3[0].LineNo != 1 {
		t.Errorf("startLine<1 should clamp: %d", lines3[0].LineNo)
	}
	// 空输出
	lines4 := parseContextOutput("", 10, 2)
	if len(lines4) != 0 {
		t.Errorf("empty: %d", len(lines4))
	}
}

func TestParseContextOutput_StripsCR(t *testing.T) {
	lines := parseContextOutput("a\r\nb", 2, 1)
	if lines[0].Content != "a" || lines[1].Content != "b" {
		t.Errorf("CR not stripped: %+v", lines)
	}
}

func TestParseSearchOutput_StripsCR(t *testing.T) {
	hits := parseSearchOutput("a.log:1:hi\r\n", "s", "/d", []logquery.FileEntry{})
	if len(hits) != 1 || hits[0].Content != "hi" {
		t.Errorf("CR: %+v", hits)
	}
}

// ---------- /api/config ----------

func TestConfig_Get(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/config", nil)
	if w.Code != 200 {
		t.Errorf("code=%d", w.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if _, ok := got["systems"]; !ok {
		t.Errorf("missing systems: %v", got)
	}
}

// TestConfig_Get_PathsAbsolute 验证 /api/config 暴露绝对路径供前端展示。
func TestConfig_Get_PathsAbsolute(t *testing.T) {
	srv, _, _, dir := newTestServer(t)
	w := doRequest(srv, "GET", "/api/config", nil)
	if w.Code != 200 {
		t.Fatalf("code=%d", w.Code)
	}
	var got struct {
		Paths struct {
			DownloadDir string `json:"download_dir"`
			LogDir      string `json:"log_dir"`
			DataDir     string `json:"data_dir"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Paths.DownloadDir == "" {
		t.Error("paths.download_dir 应非空")
	}
	if !filepath.IsAbs(got.Paths.DownloadDir) {
		t.Errorf("paths.download_dir 应为绝对路径: %s", got.Paths.DownloadDir)
	}
	if got.Paths.DownloadDir != dir {
		t.Errorf("paths.download_dir 应等于 cfg.DownloadDir(): want %s, got %s", dir, got.Paths.DownloadDir)
	}
	if got.Paths.LogDir == "" || got.Paths.DataDir == "" {
		t.Errorf("log_dir / data_dir 也应非空: log=%s data=%s", got.Paths.LogDir, got.Paths.DataDir)
	}
}

func TestConfig_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/config", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ---------- /api/credentials/* ----------

func TestCredSave_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/credentials/save", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestCredSave_BadJSON(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	r := httptest.NewRequest("POST", "/api/credentials/save", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCredSave_EmptyPassword(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/credentials/save", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "",
	})
	if w.Code != 400 {
		t.Errorf("empty password: %d %s", w.Code, w.Body.String())
	}
}

func TestCredSave_BadSystem(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/credentials/save", map[string]any{
		"system": "nonexistent", "server": "mock-1", "username": "ops", "password": "x",
	})
	if w.Code != 400 {
		t.Errorf("bad system: %d %s", w.Code, w.Body.String())
	}
}

func TestCredSave_BadServer(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/credentials/save", map[string]any{
		"system": "信贷生产", "server": "nonexistent", "username": "ops", "password": "x",
	})
	if w.Code != 400 {
		t.Errorf("bad server: %d %s", w.Code, w.Body.String())
	}
}

func TestCredSave_Happy(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/credentials/save", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "s3cret",
	})
	// 真实机器上能存，CI 上可能 keyring 不可用 → 503
	if w.Code != 200 && w.Code != 503 {
		t.Errorf("unexpected: %d %s", w.Code, w.Body.String())
	}
}

func TestCredHas_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/credentials/has", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestCredHas_MissingParams(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/credentials/has?system=&server=mock-1&username=ops", nil)
	if w.Code != 400 {
		t.Errorf("missing system: %d", w.Code)
	}
	w = doRequest(srv, "GET", "/api/credentials/has?system=信贷生产&server=&username=ops", nil)
	if w.Code != 400 {
		t.Errorf("missing server: %d", w.Code)
	}
}

func TestCredHas_BadSystem(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/credentials/has?system=nonexistent&server=mock-1&username=ops", nil)
	if w.Code != 400 {
		t.Errorf("bad system: %d", w.Code)
	}
}

func TestCredHas_Happy(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	q := url.Values{}
	q.Add("system", "信贷生产")
	q.Add("server", "mock-1")
	q.Add("username", "ops")
	w := doRequest(srv, "GET", "/api/credentials/has?"+q.Encode(), nil)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	// 200 总是有 ok 字段
	if got["ok"] != true {
		t.Errorf("ok field: %v", got)
	}
}

func TestCredClear_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/credentials/clear", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestCredClear_BadJSON(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	r := httptest.NewRequest("POST", "/api/credentials/clear", strings.NewReader("nope"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCredClear_BadSystem(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/credentials/clear", map[string]any{
		"system": "nonexistent", "server": "mock-1", "username": "ops",
	})
	if w.Code != 400 {
		t.Errorf("bad system: %d", w.Code)
	}
}

func TestCredClear_Happy(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/credentials/clear", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops",
	})
	// 200 不管存没存都给（cleared: true/false）
	if w.Code != 200 && w.Code != 503 {
		t.Errorf("unexpected: %d %s", w.Code, w.Body.String())
	}
}

// ---------- /api/downloads/* ----------

func TestDownloadsList_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/downloads/list", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestDownloadsList_Empty(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/downloads/list", nil)
	if w.Code != 200 {
		t.Fatalf("code=%d", w.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["count"].(float64) != 0 {
		t.Errorf("expected 0, got %v", got["count"])
	}
}

func TestDownloadsList_WithFiles(t *testing.T) {
	srv, _, _, dlDir := newTestServer(t)
	// 准备一个数据文件 + sidecar
	sub := filepath.Join(dlDir, time.Now().Format("20060102"))
	_ = os.MkdirAll(sub, 0o755)
	dataPath := filepath.Join(sub, "test.log")
	_ = os.WriteFile(dataPath, []byte("hello"), 0o600)
	_ = os.WriteFile(dataPath+".meta", []byte(`{
		"system":"信贷生产","server":"mock-1","host":"10.0.0.1:22",
		"dir":"/opt/logs/SystemOut","dir_alias":"SystemOut",
		"file":"test.log","encoding":"utf-8","kind":"file"
	}`), 0o600)

	w := doRequest(srv, "GET", "/api/downloads/list", nil)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	files := got["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files: %d", len(files))
	}
	f0 := files[0].(map[string]any)
	if f0["server"] != "mock-1" {
		t.Errorf("server: %v", f0["server"])
	}
	if f0["file"] != "test.log" {
		t.Errorf("file: %v", f0["file"])
	}
	if f0["size_human"] == nil {
		t.Errorf("size_human missing")
	}
}

func TestDownloadsList_FilterBySystem(t *testing.T) {
	srv, _, _, dlDir := newTestServer(t)
	sub := filepath.Join(dlDir, "x")
	_ = os.MkdirAll(sub, 0o755)
	for _, sys := range []string{"sysA", "sysB"} {
		p := filepath.Join(sub, sys+".log")
		_ = os.WriteFile(p, []byte("x"), 0o600)
		_ = os.WriteFile(p+".meta", []byte(`{"system":"`+sys+`","server":"s","file":"x.log","kind":"file"}`), 0o600)
	}
	q := url.Values{}
	q.Add("system", "sysA")
	w := doRequest(srv, "GET", "/api/downloads/list?"+q.Encode(), nil)
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["count"].(float64) != 1 {
		t.Errorf("filter: %v", got["count"])
	}
}

func TestDownloadsItem_NotFound(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	r := httptest.NewRequest("DELETE", "/api/downloads/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestDownloadsItem_DeleteMissing(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "DELETE", "/api/downloads/20260101/nonexistent.log", nil)
	// DELETE 不存在 → ok 幂等
	if w.Code != 200 {
		t.Errorf("expected 200 for missing, got %d %s", w.Code, w.Body.String())
	}
}

func TestDownloadsItem_DeleteHappy(t *testing.T) {
	srv, _, _, dlDir := newTestServer(t)
	sub := filepath.Join(dlDir, "20260101")
	_ = os.MkdirAll(sub, 0o755)
	p := filepath.Join(sub, "x.log")
	_ = os.WriteFile(p, []byte("y"), 0o600)
	_ = os.WriteFile(p+".meta", []byte("{}"), 0o600)
	rel, _ := filepath.Rel(dlDir, p)
	// URL path 用 / 分隔
	w := doRequest(srv, "DELETE", "/api/downloads/"+filepath.ToSlash(rel), nil)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("file should be gone")
	}
}

func TestDownloadsItem_DeleteAll_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/downloads/all", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestDownloadsItem_DeleteAll_Happy(t *testing.T) {
	srv, _, _, dlDir := newTestServer(t)
	for _, n := range []string{"a.log", "b.log"} {
		p := filepath.Join(dlDir, n)
		_ = os.WriteFile(p, []byte("x"), 0o600)
		_ = os.WriteFile(p+".meta", []byte("{}"), 0o600)
	}
	w := doRequest(srv, "POST", "/api/downloads/all", nil)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["deleted"].(float64) != 2 {
		t.Errorf("deleted: %v", got["deleted"])
	}
}

// ---------- /api/format/* ----------

func TestFormatJSON_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/format/json", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestFormatJSON_Format(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/format/json", map[string]any{
		"input": `{"a":1}`,
	})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !strings.Contains(got["output"].(string), `"a": 1`) {
		t.Errorf("not formatted: %v", got["output"])
	}
}

func TestFormatJSON_Minify(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/format/json", map[string]any{
		"input": `{ "a": 1 }`, "mode": "minify",
	})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["output"] != `{"a":1}` {
		t.Errorf("not minified: %v", got["output"])
	}
}

func TestFormatJSON_Validate_OK(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/format/json", map[string]any{
		"input": `{"a":1}`, "mode": "validate",
	})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["ok"] != true {
		t.Errorf("ok: %v", got)
	}
}

func TestFormatJSON_Validate_Bad(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/format/json", map[string]any{
		"input": `{`, "mode": "validate",
	})
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestFormatJSON_BadMode(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/format/json", map[string]any{
		"input": "{}", "mode": "yolo",
	})
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestFormatJSON_BadJSON(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/format/json", map[string]any{
		"input": "not json",
	})
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestFormatXML_Format(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/format/xml", map[string]any{
		"input": `<a><b>1</b></a>`,
	})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestFormatXML_Minify(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/format/xml", map[string]any{
		"input": `<a><b>1</b></a>`, "mode": "minify",
	})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestFormatXML_BadMode(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/format/xml", map[string]any{
		"input": "<a/>", "mode": "validate",
	})
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ---------- /api/ssh/test 验证路径（不能真连 SSH）----------

func TestSSHTest_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/ssh/test", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestSSHTest_BadJSON(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	r := httptest.NewRequest("POST", "/api/ssh/test", strings.NewReader("{"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSSHTest_NoSystem(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/ssh/test", map[string]any{
		"system": "nonexistent", "server": "mock-1", "username": "ops", "password": "x",
	})
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSSHTest_NoPasswordAndNoKeyring(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/ssh/test", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "",
	})
	// resolveCreds: 密码空 + keyring 没存 + 钥匙串可用 → 返 400 "缺少密码"
	// keyring 不可用 → 503 / 400 with system error
	if w.Code != 400 && w.Code != 502 {
		t.Errorf("expected 400/502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSSHTest_NoUsername(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/ssh/test", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "", "password": "x",
	})
	// server 配置里默认 username=ops，会被 resolveCreds 兜住；
	// 然后真去连 SSH 10.0.0.1 → 失败 → 502
	if w.Code != 502 && w.Code != 400 {
		t.Errorf("expected 502/400, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------- /api/logs/list / search / context / tail validation ----------

func TestLogsList_Validations(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	// wrong method
	if w := doRequest(srv, "GET", "/api/logs/list", nil); w.Code != 405 {
		t.Errorf("GET: %d", w.Code)
	}
	// bad json
	r := httptest.NewRequest("POST", "/api/logs/list", strings.NewReader("nope"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Errorf("bad json: %d", w.Code)
	}
	// bad system
	w = doRequest(srv, "POST", "/api/logs/list", map[string]any{
		"system": "x", "server": "x", "dir": "x", "username": "u", "password": "p",
	})
	if w.Code != 400 {
		t.Errorf("bad system: %d", w.Code)
	}
	// bad dir
	w = doRequest(srv, "POST", "/api/logs/list", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "nonexistent", "username": "u", "password": "p",
	})
	if w.Code != 400 {
		t.Errorf("bad dir: %d", w.Code)
	}
}

func TestLogsSearch_Validations(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	if w := doRequest(srv, "GET", "/api/logs/search", nil); w.Code != 405 {
		t.Errorf("GET: %d", w.Code)
	}
	if w := doRequest(srv, "POST", "/api/logs/search", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "SystemOut",
		"query": "你好[", "username": "u", "password": "p",
	}); w.Code != 400 {
		t.Errorf("bad query: %d body=%s", w.Code, w.Body.String())
	}
}

func TestLogsContext_Validations(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	if w := doRequest(srv, "GET", "/api/logs/context", nil); w.Code != 405 {
		t.Errorf("GET: %d", w.Code)
	}
	if w := doRequest(srv, "POST", "/api/logs/context", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "nonexistent",
		"file": "x", "line": 1, "username": "u", "password": "p",
	}); w.Code != 400 {
		t.Errorf("bad dir: %d", w.Code)
	}
}

func TestLogsSearchMulti_Validations(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	if w := doRequest(srv, "GET", "/api/logs/search/multi", nil); w.Code != 405 {
		t.Errorf("GET: %d", w.Code)
	}
	// empty system
	if w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "", "servers": []string{"s"}, "dir": "d", "query": "q",
		"username": "u", "password": "p",
	}); w.Code != 400 {
		t.Errorf("empty system: %d", w.Code)
	}
	// empty servers
	if w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "servers": []string{}, "dir": "d", "query": "q",
		"username": "u", "password": "p",
	}); w.Code != 400 {
		t.Errorf("empty servers: %d", w.Code)
	}
	// empty dir
	if w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "servers": []string{"mock-1"}, "dir": "", "query": "q",
		"username": "u", "password": "p",
	}); w.Code != 400 {
		t.Errorf("empty dir: %d", w.Code)
	}
	// bad query
	if w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "servers": []string{"mock-1"}, "dir": "SystemOut", "query": "你[",
		"username": "u", "password": "p",
	}); w.Code != 400 {
		t.Errorf("bad query: %d", w.Code)
	}
	// v0.5：新 targets[] 字段校验 — 空 targets 也要 400
	if w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "targets": []any{}, "query": "q",
		"username": "u", "password": "p",
	}); w.Code != 400 {
		t.Errorf("empty targets: %d", w.Code)
	}
	// v0.5：targets 里元素 srv 或 dir 为空也要 400
	if w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产", "targets": []map[string]string{{"server": "mock-1", "dir": ""}},
		"query": "q", "username": "u", "password": "p",
	}); w.Code != 400 {
		t.Errorf("empty dir in target: %d", w.Code)
	}
	// v0.5：targets 至少有一项合法元素，不为空 → 通过校验（mock sshd 会处理具体搜索）
	if w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产",
		"targets": []map[string]string{
			{"server": "mock-1", "dir": "SystemOut"},
		},
		"query": "Exception", "username": "u", "password": "p",
	}); w.Code != 200 {
		t.Errorf("valid targets expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// v0.5：servers + dir（旧模式）仍然兼容
	if w := doRequest(srv, "POST", "/api/logs/search/multi", map[string]any{
		"system": "信贷生产",
		"servers": []string{"mock-1"},
		"dir": "SystemOut",
		"query": "Exception", "username": "u", "password": "p",
	}); w.Code != 200 {
		t.Errorf("legacy mode expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTailStart_Validations(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	if w := doRequest(srv, "GET", "/api/logs/tail/start", nil); w.Code != 405 {
		t.Errorf("GET: %d", w.Code)
	}
	// empty file
	if w := doRequest(srv, "POST", "/api/logs/tail/start", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "SystemOut", "file": "",
		"username": "u", "password": "p",
	}); w.Code != 400 {
		t.Errorf("empty file: %d", w.Code)
	}
	// bad dir
	if w := doRequest(srv, "POST", "/api/logs/tail/start", map[string]any{
		"system": "信贷生产", "server": "mock-1", "dir": "x", "file": "y",
		"username": "u", "password": "p",
	}); w.Code != 400 {
		t.Errorf("bad dir: %d", w.Code)
	}
}

func TestTailEventsOrStop_NotFound(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	// 子路径不对
	w := doRequest(srv, "GET", "/api/logs/tail/abc", nil)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
	// 子路径不是 events/stop
	w = doRequest(srv, "GET", "/api/logs/tail/abc/weird", nil)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestTailStop_NotFound(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/logs/tail/missing-id/stop", nil)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestTailStop_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/logs/tail/any-id/stop", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ---------- /api/audit/recent ----------

func TestAuditRecent_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/audit/recent", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestAuditRecent_Happy(t *testing.T) {
	srv, _, al, _ := newTestServer(t)
	al.Write("ssh.test", "system", "信贷生产", "server", "mock-1", "result", "ok")
	w := doRequest(srv, "GET", "/api/audit/recent?limit=10", nil)
	if w.Code != 200 {
		t.Fatalf("code=%d", w.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["count"].(float64) < 1 {
		t.Errorf("count: %v", got["count"])
	}
}

// ---------- /api/admin/servers ----------

func TestAdminServers_Get(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/admin/servers", nil)
	if w.Code != 200 {
		t.Errorf("code=%d", w.Code)
	}
}

func TestAdminServers_Put_Happy(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "PUT", "/api/admin/servers", map[string]any{
		"systems": []map[string]any{
			{
				"name": "sysA",
				"servers": []map[string]any{
					{
						"name": "s1", "host": "1.1.1.1", "port": 22, "username": "u", "auth_type": "password",
						"log_dirs": []map[string]any{
							{"name": "d", "path": "/a", "patterns": []string{"*.log"}, "encoding": "utf-8"},
						},
					},
				},
			},
		},
	})
	if w.Code != 200 {
		t.Errorf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAdminServers_Put_Empty(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "PUT", "/api/admin/servers", map[string]any{
		"systems": []any{},
	})
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestAdminServers_Put_BadJSON(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	r := httptest.NewRequest("PUT", "/api/admin/servers", strings.NewReader("nope"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestAdminServers_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "DELETE", "/api/admin/servers", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ---------- 静态资源 / /downloads/* ----------

func TestServeStatic_NotFound(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	r := httptest.NewRequest("GET", "/static/nonexistent.js", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestServeStatic_PathTraversal(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	// 含 ..
	r := httptest.NewRequest("GET", "/static/..%2Fetc%2Fpasswd", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Errorf("expected 404 for traversal, got %d", w.Code)
	}
	// 含 \\
	r2 := httptest.NewRequest("GET", "/static/foo\\bar", nil)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, r2)
	if w2.Code != 404 {
		t.Errorf("expected 404 for backslash, got %d", w2.Code)
	}
}

func TestServeStatic_IndexHTML(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/", nil)
	if w.Code != 200 {
		t.Errorf("index: %d", w.Code)
	}
}

func TestServeDownload_PathTraversal(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	// ../etc/passwd
	w := doRequest(srv, "GET", "/downloads/..%2Fetc%2Fpasswd", nil)
	if w.Code != 404 {
		t.Errorf("traversal: %d", w.Code)
	}
	// 绝对路径
	w = doRequest(srv, "GET", "/downloads//etc/passwd", nil)
	if w.Code != 404 {
		t.Errorf("abs: %d", w.Code)
	}
	// 含 \\
	w = doRequest(srv, "GET", "/downloads/foo\\bar", nil)
	if w.Code != 404 {
		t.Errorf("backslash: %d", w.Code)
	}
}

func TestServeDownload_Happy(t *testing.T) {
	srv, _, _, dlDir := newTestServer(t)
	// 注意：serveDownload 不允许路径含 "/" — 只能下载 downloadDir 根目录下的文件
	p := filepath.Join(dlDir, "ok.log")
	_ = os.WriteFile(p, []byte("hi"), 0o600)
	w := doRequest(srv, "GET", "/downloads/ok.log", nil)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Disposition") == "" {
		t.Errorf("missing Content-Disposition")
	}
}

func TestServeDownload_NotFound(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/downloads/20260101/nope.log", nil)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestServeDownload_EmptyName(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/downloads/", nil)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ---------- /api/config body decode / not implemented stuff ----------

func TestNotFound(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/nonexistent", nil)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ---------- 防止编译期 unused 警告：fstest ----------
var _ fs.FS = fstest.MapFS{}

// ---------- v0.3 文件浏览器 ----------

func TestFilesList_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/files/list", nil)
	if w.Code != 405 {
		t.Errorf("expected 405 for GET, got %d", w.Code)
	}
}

func TestFilesList_BadJSON(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	r := httptest.NewRequest("POST", "/api/files/list", strings.NewReader("{garbage"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Errorf("expected 400 for bad JSON, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestFilesList_EmptyPath(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "x",
	})
	if w.Code != 400 {
		t.Errorf("expected 400 for empty path, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestFilesList_RelativePath(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "x",
		"path": "opt/logs",
	})
	if w.Code != 400 {
		t.Errorf("expected 400 for relative path, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "绝对路径") {
		t.Errorf("error msg should mention 绝对路径: %s", w.Body.String())
	}
}

func TestFilesList_BadSystem(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system": "不存在", "server": "mock-1", "username": "ops", "password": "x",
		"path": "/opt",
	})
	if w.Code != 400 {
		t.Errorf("expected 400 for bad system, got %d", w.Code)
	}
}

func TestFilesList_BadServer(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system": "信贷生产", "server": "不存在", "username": "ops", "password": "x",
		"path": "/opt",
	})
	if w.Code != 400 {
		t.Errorf("expected 400 for bad server, got %d", w.Code)
	}
}

func TestFilesList_MissingPassword(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops",
		"path": "/opt",
	})
	if w.Code != 400 {
		t.Errorf("expected 400 for missing password, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "密码") {
		t.Errorf("error msg should mention 密码: %s", w.Body.String())
	}
}

func TestFilesDownload_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/files/download", nil)
	if w.Code != 405 {
		t.Errorf("expected 405 for GET, got %d", w.Code)
	}
}

func TestFilesDownload_EmptyPaths(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/files/download", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "x",
		"paths": []string{},
	})
	if w.Code != 400 {
		t.Errorf("expected 400 for empty paths, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestFilesDownload_RelativePath(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/files/download", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "x",
		"paths": []string{"opt/a.log"},
	})
	if w.Code != 400 {
		t.Errorf("expected 400 for relative path, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestFilesDownload_PathWithControlChar(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/files/download", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "x",
		"paths": []string{"/opt/a\nb.log"},
	})
	if w.Code != 400 {
		t.Errorf("expected 400 for control char in path, got %d", w.Code)
	}
}

func TestFilesDownload_TooManyFiles(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	paths := make([]string, filesMaxFilesPerTask+1)
	for i := range paths {
		paths[i] = "/opt/file" + itoa(i) + ".log"
	}
	w := doRequest(srv, "POST", "/api/files/download", map[string]any{
		"system": "信贷生产", "server": "mock-1", "username": "ops", "password": "x",
		"paths": paths,
	})
	if w.Code != 400 {
		t.Errorf("expected 400 for too many files, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "100") {
		t.Errorf("error msg should mention 100: %s", w.Body.String())
	}
}

func TestFilesDownload_Events_BadID(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/files/download/nonexistent/events", nil)
	if w.Code != 404 {
		t.Errorf("expected 404 for unknown id, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestFilesDownload_Cancel_BadID(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/files/download/nonexistent/cancel", nil)
	if w.Code != 404 {
		t.Errorf("expected 404 for unknown id, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestFilesDownload_EventsOrCancel_BadPath(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/files/download/dl-abc/garbage", nil)
	if w.Code != 404 {
		t.Errorf("expected 404 for garbage subpath, got %d", w.Code)
	}
}

// itoa 是 fmt.Sprintf("%d", i) 的简化版，避免引入 fmt 仅测试用
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
