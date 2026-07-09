package httpserver

// handlers_ssh_sftp_upload_test.go — 测 /api/ssh/sftp/upload/* 的纯函数逻辑 +
// init 端点参数校验（不依赖 SSH，避免测试基础设施过重）。
//
// 覆盖：
//   - validateUploadPath 路径安全校验
//   - AppConfig.UploadMaxSizeBytes 默认值 / 配置值 / 0
//   - init 端点参数校验（缺字段 / 大小超限 / overwrite 非法）
//   - cancel / data 不存在的 id 返回 404
//   - pickRenameCandidate 重命名候选生成（通过 statChecker 接口注入 mock statFn，
//     覆盖：不冲突 / _1 冲突 / 全冲突 / dotfile / 多扩展名 / 权限错误）

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"kairo/internal/config"
)

// ============================================================================
// validateUploadPath 纯函数测试
// ============================================================================

func TestUpload_ValidatePath(t *testing.T) {
	cases := []struct {
		name       string
		targetDir  string
		filename   string
		wantErr    bool
		errContain string
	}{
		{"happy", "/tmp", "test.log", false, ""},
		{"relative_dir", "tmp", "test.log", true, "绝对路径"},
		{"dotdot_in_dir", "/tmp/../etc", "test.log", true, "\"..\""}, // 含 .. 直接拒绝，不静默归一
		{"filename_with_slash", "/tmp", "sub/test.log", true, "路径分隔符"},
		{"filename_dotdot", "/tmp", "..", true, "filename"},
		{"filename_dot", "/tmp", ".", true, "filename"},
		{"control_char_dir", "/tmp\n", "test.log", true, "非法字符"},
		{"control_char_name", "/tmp", "test\x00.log", true, "非法字符"},
		{"empty_dir", "", "test.log", true, "绝对路径"}, // 空串不是绝对路径
		{"empty_name", "/tmp", "", true, "不能为空"},
		{"trailing_slash_ok", "/tmp/", "test.log", false, ""}, // path.Clean 会归一
		{"double_slash_ok", "/tmp//sub", "test.log", false, ""},
		{"backslash_in_name", "/tmp", "test\\.log", true, "路径分隔符"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			td, fn, err := validateUploadPath(c.targetDir, c.filename)
			if c.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (td=%q fn=%q)", td, fn)
				} else if c.errContain != "" && !strings.Contains(err.Error(), c.errContain) {
					t.Errorf("err=%q, want contains %q", err.Error(), c.errContain)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected err: %v", err)
				}
				if td == "" || fn == "" {
					t.Errorf("empty result: td=%q fn=%q", td, fn)
				}
			}
		})
	}
}

// TestUpload_ValidatePath_DotDotDir 确保 /a/../b 被直接拒绝（不静默归一化）
func TestUpload_ValidatePath_DotDotDir(t *testing.T) {
	_, _, err := validateUploadPath("/tmp/../etc", "x.log")
	if err == nil {
		t.Error("expected error for /tmp/../etc, got nil")
	}
	if !strings.Contains(err.Error(), "\"..\"") {
		t.Errorf("err=%q, want contains '\"..\"'", err.Error())
	}
}

// ============================================================================
// AppConfig.UploadMaxSizeBytes
// ============================================================================

func TestUpload_ConfigUploadMaxSize(t *testing.T) {
	// 默认 2GB
	a := &config.AppConfig{}
	if got := a.UploadMaxSizeBytes(); got != 2*1024*1024*1024 {
		t.Errorf("default: %d, want 2GB", got)
	}
	// 显式配 100MB
	hundredMB := int64(100 * 1024 * 1024)
	a = &config.AppConfig{UploadMaxSize: &hundredMB}
	if got := a.UploadMaxSizeBytes(); got != hundredMB {
		t.Errorf("configured: %d, want %d", got, hundredMB)
	}
	// 显式配 0（不限制）
	zero := int64(0)
	a = &config.AppConfig{UploadMaxSize: &zero}
	if got := a.UploadMaxSizeBytes(); got != 0 {
		t.Errorf("zero: %d, want 0", got)
	}
}

// ============================================================================
// init 端点参数校验
// ============================================================================

func TestUpload_InitValidation(t *testing.T) {
	srv, _, _, _ := newTestServer(t)

	cases := []struct {
		name     string
		body     map[string]any
		wantCode int
	}{
		{
			name:     "missing_system",
			body:     map[string]any{"server": "x", "target_dir": "/tmp", "filename": "a.log", "size": 100},
			wantCode: 400,
		},
		{
			name:     "missing_target_dir",
			body:     map[string]any{"system": "信贷生产", "server": "mock-1", "filename": "a.log", "size": 100},
			wantCode: 400,
		},
		{
			name:     "missing_filename",
			body:     map[string]any{"system": "信贷生产", "server": "mock-1", "target_dir": "/tmp", "size": 100},
			wantCode: 400,
		},
		{
			name:     "zero_size",
			body:     map[string]any{"system": "信贷生产", "server": "mock-1", "target_dir": "/tmp", "filename": "a.log", "size": 0},
			wantCode: 400,
		},
		{
			name:     "negative_size",
			body:     map[string]any{"system": "信贷生产", "server": "mock-1", "target_dir": "/tmp", "filename": "a.log", "size": -1},
			wantCode: 400,
		},
		{
			name:     "bad_overwrite",
			body:     map[string]any{"system": "信贷生产", "server": "mock-1", "target_dir": "/tmp", "filename": "a.log", "size": 100, "overwrite": "force"},
			wantCode: 400,
		},
		{
			name:     "size_too_big",
			body:     map[string]any{"system": "信贷生产", "server": "mock-1", "target_dir": "/tmp", "filename": "a.log", "size": 3 * 1024 * 1024 * 1024}, // 3GB > 2GB
			wantCode: 400,
		},
		{
			name:     "relative_target_dir",
			body:     map[string]any{"system": "信贷生产", "server": "mock-1", "target_dir": "tmp", "filename": "a.log", "size": 100},
			wantCode: 400,
		},
		{
			name:     "filename_with_slash",
			body:     map[string]any{"system": "信贷生产", "server": "mock-1", "target_dir": "/tmp", "filename": "sub/a.log", "size": 100},
			wantCode: 400,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := doRequest(srv, "POST", "/api/ssh/sftp/upload/init", c.body)
			if w.Code != c.wantCode {
				t.Errorf("code=%d, want %d, body=%s", w.Code, c.wantCode, w.Body.String())
			}
		})
	}
}

// TestUpload_InitHappy 测 init 成功建会话（缺密码会失败，但用 default password）
func TestUpload_InitHappy(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)

	// 配置默认密码让 resolveCreds 通过
	cur := mgr.Get()
	cur.Systems[0].Servers[0].Password = "test-pass"
	mgr.Replace(cur)

	w := doRequest(srv, "POST", "/api/ssh/sftp/upload/init", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"target_dir": "/tmp", "filename": "test.log", "size": 100,
		"overwrite": "reject",
	})
	if w.Code != 200 {
		t.Fatalf("init failed: code=%d body=%s", w.Code, w.Body.String())
	}
	var resp sshSftpUploadInitResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == "" {
		t.Error("empty upload id")
	}
	if !strings.HasPrefix(resp.ID, "up-") {
		t.Errorf("id prefix: %q, want up-", resp.ID)
	}
	if resp.PartialPath != "/tmp/.test.log.partial" {
		t.Errorf("partial path: %q, want /tmp/.test.log.partial", resp.PartialPath)
	}
	if resp.MaxSize != 2*1024*1024*1024 {
		t.Errorf("max size: %d, want 2GB", resp.MaxSize)
	}

	// 清理
	_ = doRequest(srv, "POST", "/api/ssh/sftp/upload/cancel", map[string]any{"id": resp.ID})
}

// TestUpload_InitOverwriteDefault 测 overwrite 空字符串默认为 reject
func TestUpload_InitOverwriteDefault(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)
	cur := mgr.Get()
	cur.Systems[0].Servers[0].Password = "test-pass"
	mgr.Replace(cur)

	w := doRequest(srv, "POST", "/api/ssh/sftp/upload/init", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"target_dir": "/tmp", "filename": "test.log", "size": 100,
		// overwrite 留空
	})
	if w.Code != 200 {
		t.Fatalf("init failed: code=%d body=%s", w.Code, w.Body.String())
	}
	var resp sshSftpUploadInitResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	// 验证 state 里 overwrite = "reject"
	st, ok := srv.uploadStates.Get(resp.ID)
	if !ok {
		t.Fatal("state not found")
	}
	if st.Overwrite != "reject" {
		t.Errorf("overwrite: %q, want reject", st.Overwrite)
	}
	_ = doRequest(srv, "POST", "/api/ssh/sftp/upload/cancel", map[string]any{"id": resp.ID})
}

// TestUpload_CancelNotExist cancel 不存在的 id → 幂等返回 200
func TestUpload_CancelNotExist(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/ssh/sftp/upload/cancel", map[string]any{"id": "up-nonexistent"})
	if w.Code != 200 {
		t.Errorf("code=%d, want 200 (idempotent)", w.Code)
	}
}

// TestUpload_CancelEmptyId cancel 空 id
func TestUpload_CancelEmptyId(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/ssh/sftp/upload/cancel", map[string]any{"id": ""})
	if w.Code != 400 {
		t.Errorf("code=%d, want 400", w.Code)
	}
}

// TestUpload_DataNoSession /data 传不存在的 id
func TestUpload_DataNoSession(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	r := httptest.NewRequest("POST", "/api/ssh/sftp/upload/up-nonexistent/data", bytes.NewReader([]byte("data")))
	r.Header.Set("Content-Length", "4")
	r.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Errorf("code=%d, want 404", w.Code)
	}
}

// TestUpload_DataMissingContentLength /data 缺 Content-Length
func TestUpload_DataMissingContentLength(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)
	cur := mgr.Get()
	cur.Systems[0].Servers[0].Password = "test-pass"
	mgr.Replace(cur)

	// 先 init 一个
	w := doRequest(srv, "POST", "/api/ssh/sftp/upload/init", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"target_dir": "/tmp", "filename": "test.log", "size": 100,
	})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var resp sshSftpUploadInitResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// /data 不带 Content-Length（chunked）
	// httptest.NewRequest 默认 ContentLength=-1，需要手动设置
	r := httptest.NewRequest("POST", "/api/ssh/sftp/upload/"+resp.ID+"/data", bytes.NewReader([]byte("data")))
	// 不设 Content-Length，让 r.ContentLength = -1
	r.ContentLength = -1
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, r)
	if w2.Code != 400 {
		t.Errorf("code=%d, want 400", w2.Code)
	}

	_ = doRequest(srv, "POST", "/api/ssh/sftp/upload/cancel", map[string]any{"id": resp.ID})
}

// TestUpload_DataSizeMismatch /data Content-Length 与 init 声明不一致
func TestUpload_DataSizeMismatch(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)
	cur := mgr.Get()
	cur.Systems[0].Servers[0].Password = "test-pass"
	mgr.Replace(cur)

	w := doRequest(srv, "POST", "/api/ssh/sftp/upload/init", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"target_dir": "/tmp", "filename": "test.log", "size": 100,
	})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var resp sshSftpUploadInitResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// /data Content-Length=4，但 init 声明 100
	r := httptest.NewRequest("POST", "/api/ssh/sftp/upload/"+resp.ID+"/data", bytes.NewReader([]byte("data")))
	r.Header.Set("Content-Length", "4")
	r.ContentLength = 4
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, r)
	if w2.Code != 400 {
		t.Errorf("code=%d, want 400", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "不符") {
		t.Errorf("body should mention mismatch: %s", w2.Body.String())
	}

	_ = doRequest(srv, "POST", "/api/ssh/sftp/upload/cancel", map[string]any{"id": resp.ID})
}

// TestUpload_StateCtxCancel 验证 cancel 端点能触发 state.ctx.Done
func TestUpload_StateCtxCancel(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)
	cur := mgr.Get()
	cur.Systems[0].Servers[0].Password = "test-pass"
	mgr.Replace(cur)

	w := doRequest(srv, "POST", "/api/ssh/sftp/upload/init", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"target_dir": "/tmp", "filename": "test.log", "size": 100,
	})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var resp sshSftpUploadInitResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	st, ok := srv.uploadStates.Get(resp.ID)
	if !ok {
		t.Fatal("state not found")
	}

	// 监听 state.ctx.Done
	done := make(chan struct{})
	go func() {
		<-st.ctx.Done()
		close(done)
	}()

	// cancel
	_ = doRequest(srv, "POST", "/api/ssh/sftp/upload/cancel", map[string]any{"id": resp.ID})

	// 等 1 秒应该收到
	select {
	case <-done:
		// ok
	case <-time.After(time.Second):
		t.Error("state.ctx not canceled after cancel endpoint")
	}
}

// ============================================================================
// pickRenameCandidate 单元测试
// ============================================================================

// mockStatChecker 实现 statChecker 接口，用 map 模拟远端文件存在性。
// existent 里的 key 是"已存在"的完整路径；Stat 命中返回 fakeFileInfo，
// 未命中返回 fs.ErrNotExist；如果路径在 permDenied 里，返回 permission denied。
type mockStatChecker struct {
	existent     map[string]bool
	permDenied   map[string]bool
	statCallCount int
}

func (m *mockStatChecker) Stat(p string) (os.FileInfo, error) {
	m.statCallCount++
	if m.permDenied[p] {
		return nil, errors.New("permission denied (sftp: \"Failure\")")
	}
	if m.existent[p] {
		return fakeFileInfo{name: p}, nil
	}
	return nil, fs.ErrNotExist
}

func TestPickRenameCandidate(t *testing.T) {
	cases := []struct {
		name        string
		existent    map[string]bool // 已存在的完整路径
		permDenied  map[string]bool // 权限拒绝的完整路径
		original    string
		wantName    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "no_conflict",
			existent: map[string]bool{},
			original: "test.log",
			wantName: "test_1.log",
		},
		{
			name: "first_conflict",
			existent: map[string]bool{
				"/tmp/test_1.log": true,
			},
			original: "test.log",
			wantName: "test_2.log",
		},
		{
			name: "two_conflicts",
			existent: map[string]bool{
				"/tmp/test_1.log": true,
				"/tmp/test_2.log": true,
			},
			original: "test.log",
			wantName: "test_3.log",
		},
		{
			name: "dotfile_bashrc",
			existent: map[string]bool{
				"/tmp/.bashrc_1": true,
			},
			original: ".bashrc",
			wantName: ".bashrc_2",
		},
		{
			name:     "dotfile_no_conflict",
			existent: map[string]bool{},
			original: ".bashrc",
			wantName: ".bashrc_1",
		},
		{
			name:     "multi_ext_tar_gz",
			existent: map[string]bool{},
			original: "file.tar.gz",
			wantName: "file.tar_1.gz", // 只拆最后一段 .gz
		},
		{
			name:     "no_ext",
			existent: map[string]bool{},
			original: "Makefile",
			wantName: "Makefile_1",
		},
		{
			name: "all_conflict",
			existent: func() map[string]bool {
				m := map[string]bool{}
				for i := 1; i <= 99; i++ {
					m["/tmp/test_"+itoa(i)+".log"] = true
				}
				return m
			}(),
			original:    "test.log",
			wantErr:     true,
			errContains: "找不到可用重命名候选",
		},
		{
			name: "perm_denided_on_first_candidate",
			permDenied: map[string]bool{
				"/tmp/test_1.log": true,
			},
			original:    "test.log",
			wantErr:     true,
			errContains: "检查候选名", // 权限错误不应被当作"可用"
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mock := &mockStatChecker{
				existent:   c.existent,
				permDenied: c.permDenied,
			}
			got, err := pickRenameCandidate(mock, "/tmp", c.original)
			if c.wantErr {
				if err == nil {
					t.Errorf("expected error, got name=%q", got)
				} else if c.errContains != "" && !strings.Contains(err.Error(), c.errContains) {
					t.Errorf("err=%q, want contains %q", err.Error(), c.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			wantPath := "/tmp/" + c.wantName
			if got != c.wantName {
				t.Errorf("got name=%q, want %q (full path would be %s)", got, c.wantName, wantPath)
			}
		})
	}
}
