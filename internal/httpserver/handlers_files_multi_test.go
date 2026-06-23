package httpserver

// handlers_files_multi_test.go — v0.5 项 7（多 server 并列目录）的测试
//
// 覆盖：
//   - 多 server 并列目录：3 台 server / 同一 path / 部分失败 / 全部成功 / 全失败
//   - 向后兼容：老调用（单 server）仍走单 server 模式，响应 shape 不变
//
// 设计要点：
//   - 复用 newTestServer 的 fake SSH + fake SFTP；
//   - 用 Manager.Replace 把 config 改成 3 台 server（同 fake SSH 端口，
//     但 name 不同）；fakeSFTP 按 path 共享桶 → 不同 server 拿到同一份目录。
//
// API 契约（跟 handler_files.go:62-69 一致）：
//   - 单服务器模式：{system, server, path, ...}
//   - 多服务器模式：{system, servers:[...], path, ...}
//   - 多 server 共用同一个 path（项 7 的设计：让用户先勾选 servers，再选 path）
//
// 注意：TargetDir 相关测试见 handlers_files_test.go（项 18）。

import (
	"encoding/json"
	"net"
	"strconv"
	"testing"

	"ops-toolbox/internal/config"
)

// newTestServerMultiServers 构造一个配置了 3 台 server 的测试环境：
//   - mock-1 / mock-2 / mock-3 都指向同一 fake SSH 端口；
//   - fakeSFTP 共用一份 dirs 表（多 server 模式共享 path）。
//   - 审计 + web 跟 newTestServer 一样用 tmpDir。
func newTestServerMultiServers(t *testing.T, f *fakeSftpClient) (*Server, *config.Manager) {
	t.Helper()
	srv, mgr, _, _ := newTestServer(t)
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	cfg := mgr.Get()
	for si := range cfg.Systems {
		if cfg.Systems[si].Name != "信贷生产" {
			continue
		}
		// 改 mock-1 的 host/port → fake SSH
		if len(cfg.Systems[si].Servers) > 0 {
			srvRef := &cfg.Systems[si].Servers[0]
			srvRef.Host = "127.0.0.1"
			srvRef.Port = port
		}
		// 追加 mock-2 / mock-3
		cfg.Systems[si].Servers = append(cfg.Systems[si].Servers,
			config.ServerConfig{
				Name:     "mock-2",
				Host:     "127.0.0.1",
				Port:     port,
				Username: "ops",
				AuthType: "password",
				LogDirs: []config.LogDirEntry{
					{Name: "any", Path: "/data", Patterns: []string{"*"}, Encoding: "utf-8"},
				},
			},
			config.ServerConfig{
				Name:     "mock-3",
				Host:     "127.0.0.1",
				Port:     port,
				Username: "ops",
				AuthType: "password",
				LogDirs: []config.LogDirEntry{
					{Name: "any", Path: "/data", Patterns: []string{"*"}, Encoding: "utf-8"},
				},
			},
		)
		break
	}
	if err := mgr.Replace(cfg); err != nil {
		t.Fatalf("替换 cfg 失败: %v", err)
	}
	withFakeSFTP(t, f)
	return srv, mgr
}

// TestFilesListMulti_Happy_3Servers 三台 server 都成功列出同一 path。
//
// 验证：
//   - 响应里 servers[] 有 3 段，每段对应一台 server；
//   - 每段 ok=true，count 匹配预期；
//   - ok_count=3, fail_count=0, total_count=总文件数。
func TestFilesListMulti_Happy_3Servers(t *testing.T) {
	f := newFakeSftpBasic()
	f.dirs["/data"] = []fakeDirEntry{
		{name: "a.log", size: 10, isDir: false},
		{name: "b.log", size: 20, isDir: false},
		{name: "c.log", size: 30, isDir: false},
	}
	srv, _ := newTestServerMultiServers(t, f)

	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system":   "信贷生产",
		"username": "ops", "password": "testpw",
		"servers": []string{"mock-1", "mock-2", "mock-3"},
		"path":    "/data",
	})
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Servers []struct {
			Server string `json:"server"`
			Host   string `json:"host"`
			OK     bool   `json:"ok"`
			Error  string `json:"error"`
			Count  int    `json:"count"`
		} `json:"servers"`
		OKCount    int `json:"ok_count"`
		FailCount  int `json:"fail_count"`
		TotalCount int `json:"total_count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Servers) != 3 {
		t.Fatalf("期望 3 段 server 结果，得到 %d: %s", len(got.Servers), w.Body.String())
	}
	if got.OKCount != 3 {
		t.Errorf("ok_count 期望 3，得到 %d", got.OKCount)
	}
	if got.FailCount != 0 {
		t.Errorf("fail_count 期望 0，得到 %d", got.FailCount)
	}
	if got.TotalCount != 9 {
		t.Errorf("total_count 期望 9 (3*3)，得到 %d", got.TotalCount)
	}
	for i, s := range got.Servers {
		if !s.OK {
			t.Errorf("server[%d] %q 应 ok，但 error=%q", i, s.Server, s.Error)
			continue
		}
		if s.Count != 3 {
			t.Errorf("server[%d] %q count 期望 3，得到 %d", i, s.Server, s.Count)
		}
	}
}

// TestFilesListMulti_PartialFailure 3 台里 1 台不存在 → 其他 2 台仍能列出。
func TestFilesListMulti_PartialFailure(t *testing.T) {
	f := newFakeSftpBasic()
	f.dirs["/data"] = []fakeDirEntry{{name: "a.log", size: 1, isDir: false}}
	srv, _ := newTestServerMultiServers(t, f)

	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system":   "信贷生产",
		"username": "ops", "password": "testpw",
		"servers": []string{"mock-1", "mock-2", "nonexistent"},
		"path":    "/data",
	})
	if w.Code != 200 {
		t.Fatalf("部分失败仍应 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Servers []struct {
			Server string `json:"server"`
			OK     bool   `json:"ok"`
			Error  string `json:"error"`
		} `json:"servers"`
		OKCount   int `json:"ok_count"`
		FailCount int `json:"fail_count"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.OKCount != 2 {
		t.Errorf("ok_count 期望 2，得到 %d", got.OKCount)
	}
	if got.FailCount != 1 {
		t.Errorf("fail_count 期望 1，得到 %d", got.FailCount)
	}
	found := false
	for _, s := range got.Servers {
		if s.Server == "nonexistent" {
			found = true
			if s.OK {
				t.Error("nonexistent 段应 ok=false")
			}
			if s.Error == "" {
				t.Error("nonexistent 段应有 error 字段")
			}
		}
	}
	if !found {
		t.Error("nonexistent 段缺失")
	}
}

// TestFilesListMulti_EmptyServers 没给 server/servers → 400
func TestFilesListMulti_EmptyServers(t *testing.T) {
	srv := newServerWithFakeSSHAndSFTP(t, newFakeSftpBasic())
	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system":   "信贷生产",
		"username": "ops", "password": "testpw",
		"path": "/data",
	})
	if w.Code != 400 {
		t.Errorf("空 servers 应 400，得到 %d", w.Code)
	}
}

// TestFilesListMulti_BackwardCompat 老调用（单 server 字段）仍走单 server 模式。
func TestFilesListMulti_BackwardCompat(t *testing.T) {
	f := newFakeSftpBasic()
	f.dirs["/data"] = []fakeDirEntry{
		{name: "x.log", size: 100, isDir: false},
	}
	srv := newServerWithFakeSSHAndSFTP(t, f)
	w := doRequest(srv, "POST", "/api/files/list", map[string]any{
		"system": "信贷生产", "server": "mock-1",
		"username": "ops", "password": "testpw",
		"path": "/data",
	})
	if w.Code != 200 {
		t.Fatalf("单 server 模式应仍 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var raw map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	if _, hasServers := raw["servers"]; hasServers {
		t.Errorf("单 server 模式不应有 servers 字段: %v", raw)
	}
	if _, hasEntries := raw["entries"]; !hasEntries {
		t.Errorf("单 server 模式应仍返 entries 字段: %v", raw)
	}
}
