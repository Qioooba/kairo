package diagnostics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kairo/internal/config"
)

// makeCfg 构造一个最小可用的 config（用 t.TempDir 兜底所有 dir）
func makeCfg(t *testing.T) *config.Config {
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
						Name:     "ok-loopback",
						Host:     "127.0.0.1",
						Port:     1, // closed port → TCP fail 预期
						Username: "ops",
						LogDirs: []config.LogDirEntry{
							{Name: "L", Path: "/tmp", Patterns: []string{"*.log"}, Encoding: "utf-8"},
						},
					},
				},
			},
		},
		Search: config.SearchConfig{},
	}
	cfg.Defaults()
	if err := cfg.ResolvePaths(tmp); err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestCollect_Basic(t *testing.T) {
	cfg := makeCfg(t)
	rep := Collect(cfg, "/tmp/config.yaml", Options{CheckServers: false})

	if rep.App.Name != "TestBox" {
		t.Errorf("App.Name=%q", rep.App.Name)
	}
	if rep.App.ListenAddr != "127.0.0.1:18080" {
		t.Errorf("ListenAddr=%q", rep.App.ListenAddr)
	}
	if !rep.Runtime.DataDirWritable || !rep.Runtime.DownloadDirWritable || !rep.Runtime.LogDirWritable {
		t.Errorf("dirs should be writable: %+v", rep.Runtime)
	}
	if rep.Build.GoVersion == "" {
		t.Errorf("GoVersion empty")
	}
	// find / grep / tail 通常 CI 也有，但 unzip / ssh 不一定有 — 不要强求
	if !rep.Tools.Find.Found {
		t.Log("find 未找到（CI 环境可能没装）")
	}
}

func TestCollect_RWCheck(t *testing.T) {
	cfg := makeCfg(t)
	// 把 data 改成不可写（指向一个文件而不是目录）
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "iam-a-file")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.App.DataDir = filePath
	if err := cfg.ResolvePaths(tmp); err != nil {
		t.Fatal(err)
	}
	rep := Collect(cfg, "/tmp/config.yaml", Options{CheckServers: false})
	if rep.Runtime.DataDirWritable {
		t.Errorf("指向文件的 data_dir 应判为不可写")
	}
	// Issues 应该有告警
	found := false
	for _, issue := range rep.Issues {
		if strings.Contains(issue, "数据目录不可写") {
			found = true
		}
	}
	if !found {
		t.Errorf("Issues 应包含「数据目录不可写」告警: %v", rep.Issues)
	}
}

// TestCollect_ServerCheck_FakePort TCP 端口不可达 → 失败但不应 panic。
func TestCollect_ServerCheck_FakePort(t *testing.T) {
	cfg := makeCfg(t)
	// 用一个不会有人监听的端口（TEST-NET + reserved）
	cfg.Systems[0].Servers[0].Host = "127.0.0.1"
	cfg.Systems[0].Servers[0].Port = 1 // 1/tcp 通常不监听
	rep := Collect(cfg, "/tmp/config.yaml", Options{
		CheckServers:     true,
		PerServerTimeout: 500 * time.Millisecond,
	})
	if len(rep.Servers) != 1 {
		t.Fatalf("期望 1 个 server 检查，实际 %d", len(rep.Servers))
	}
	if rep.Servers[0].TCP.OK {
		t.Errorf("端口 1 不应可达")
	}
	if rep.Servers[0].Category == "" {
		t.Errorf("失败时 Category 应有值: %+v", rep.Servers[0])
	}
}

// TestCollect_ServerCheck_BadDNS DNS 失败不 panic。
func TestCollect_ServerCheck_BadDNS(t *testing.T) {
	cfg := makeCfg(t)
	cfg.Systems[0].Servers[0].Host = "no-such-host.invalid.example-xyz.test"
	rep := Collect(cfg, "/tmp/config.yaml", Options{
		CheckServers:     true,
		PerServerTimeout: 500 * time.Millisecond,
		LookupHost: func(context.Context, string) ([]string, error) {
			return nil, errors.New("deterministic NXDOMAIN")
		},
	})
	if len(rep.Servers) != 1 {
		t.Fatalf("期望 1 个 server 检查，实际 %d", len(rep.Servers))
	}
	if rep.Servers[0].DNS.OK {
		t.Errorf("无效域名不应解析成功")
	}
	if rep.Servers[0].Category != "dns" {
		t.Errorf("Category 应是 dns，实际 %q", rep.Servers[0].Category)
	}
}

// TestCollect_IssuesForMissingFind 如果本机没 find，应有告警。
//
// 注意：CI 通常有 find；这个 case 在本地无 find 的环境才能触发。
// 这里只测逻辑分支，不强制依赖工具存在。
func TestCollect_IssuesForFileMode(t *testing.T) {
	cfg := makeCfg(t)
	cfg.App.CredentialStore = "file"
	rep := Collect(cfg, "/tmp/config.yaml", Options{CheckServers: false})
	found := false
	for _, issue := range rep.Issues {
		if strings.Contains(issue, "credential_store=file") {
			found = true
		}
	}
	if !found {
		t.Errorf("file mode 应触发告警: %v", rep.Issues)
	}
}

// TestHostIsIP
func TestHostIsIP(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"example.com", false},
		{"localhost", false},
		{"", false},
	}
	for _, c := range cases {
		if got := hostIsIP(c.in); got != c.want {
			t.Errorf("hostIsIP(%q)=%v, want %v", c.in, got, c.want)
		}
	}
}

// TestCheckSSHHandshakeLimited 走 TCP 探测；端口 1 应该连不上。
func TestCheckSSHHandshakeLimited_PortClosed(t *testing.T) {
	r := CheckSSHHandshakeLimited("127.0.0.1", 1, 200*time.Millisecond)
	if r.OK {
		t.Errorf("端口 1 不应握手成功: %+v", r)
	}
	if !strings.Contains(r.Message, "TCP") && !strings.Contains(r.Message, "banner") {
		t.Errorf("错误信息应提到 TCP 或 banner: %q", r.Message)
	}
}

// TestCheckSSHHandshakeLimited_EmptyHost
func TestCheckSSHHandshakeLimited_EmptyHost(t *testing.T) {
	r := CheckSSHHandshakeLimited("", 22, 200*time.Millisecond)
	if r.OK || !strings.Contains(r.Message, "host 为空") {
		t.Errorf("空 host 应直接拒: %+v", r)
	}
}

// TestTruncate
func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 10, "abc"},
		{"abcdef", 3, "abc…"},
		{"中文很长", 2, "中文…"},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.n); got != c.want {
			t.Errorf("truncate(%q, %d)=%q, want %q", c.in, c.n, got, c.want)
		}
	}
}

// TestHostPort
func TestHostPort(t *testing.T) {
	sc := ServerCheck{Host: "1.2.3.4", Port: 22}
	if got := hostPort(sc); got != "1.2.3.4:22" {
		t.Errorf("hostPort()=%q", got)
	}
}

func TestCollect_DatabaseDiagnosticsAndBitnessMismatch(t *testing.T) {
	cfg := makeCfg(t)
	rep := Collect(cfg, "/tmp/config.yaml", Options{
		CheckServers: false,
		DatabaseDiag: func() DatabaseInfo {
			return DatabaseInfo{
				Backend:         "go-ora",
				AppBitness:      "amd64",
				ClientBitness:   "32-bit",
				BitnessMismatch: true,
				PLSQLDetected:   true,
				PLSQLBitness:    "32-bit",
				PLSQLDetail:     "PL/SQL Developer (32-bit Instant Client)",
				Detail:          "检测到 32 位客户端，已自动降级为 go-ora",
			}
		},
	})
	if rep.Database.Backend != "go-ora" {
		t.Fatalf("expected backend go-ora, got %s", rep.Database.Backend)
	}
	if !rep.Database.BitnessMismatch {
		t.Fatalf("expected BitnessMismatch to be true")
	}
	hasMismatchIssue := false
	for _, issue := range rep.Issues {
		if strings.Contains(issue, "架构不匹配") {
			hasMismatchIssue = true
			break
		}
	}
	if !hasMismatchIssue {
		t.Fatalf("expected Issues to mention 架构不匹配, got: %+v", rep.Issues)
	}
}

