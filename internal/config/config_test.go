package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// validYAML 给"最小可用配置"用：1 个系统 / 1 台服务器 / 1 个 log_dir
const validYAML = `
app:
  name: "TestBox"
  host: 127.0.0.1
  port: 18080
  auto_open_browser: false
  download_dir: ./downloads
  log_dir: ./logs
  data_dir: ./data
systems:
  - name: "信贷生产"
    description: "测试"
    servers:
      - name: "mock-1"
        host: "10.0.0.1"
        port: 22
        username: "ops"
        auth_type: "password"
        log_dirs:
          - name: "SystemOut"
            path: "/opt/logs/SystemOut"
            patterns: ["*.log"]
            encoding: "utf-8"
search:
  default_latest_files: 3
  max_matches: 200
  default_context_lines: 30
  timeout_seconds: 30
  max_concurrency: 2
`

func TestLoad_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o640); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.Name != "TestBox" {
		t.Errorf("App.Name=%q", cfg.App.Name)
	}
	if len(cfg.Systems) != 1 {
		t.Fatalf("Systems: %d", len(cfg.Systems))
	}
	if cfg.Systems[0].Servers[0].Name != "mock-1" {
		t.Errorf("server name: %q", cfg.Systems[0].Servers[0].Name)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/xyz.yaml")
	if err == nil {
		t.Fatal("missing file should fail")
	}
}

func TestLoad_BadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(path, []byte("not: valid: yaml: ::::"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("bad yaml should fail")
	}
}

func TestDefaults_AppliesMissing(t *testing.T) {
	c := &Config{}
	c.Defaults()
	if c.App.Name != "豆包工具箱" {
		t.Errorf("App.Name default: %q", c.App.Name)
	}
	if c.App.Host != "127.0.0.1" {
		t.Errorf("App.Host default: %q", c.App.Host)
	}
	if c.App.Port != 18080 {
		t.Errorf("App.Port default: %d", c.App.Port)
	}
	if c.App.DownloadDir != "./downloads" {
		t.Errorf("App.DownloadDir default: %q", c.App.DownloadDir)
	}
	if c.Search.DefaultLatestFiles != 3 {
		t.Errorf("Search.DefaultLatestFiles default: %d", c.Search.DefaultLatestFiles)
	}
	if c.Search.MaxConcurrency != 2 {
		t.Errorf("Search.MaxConcurrency default: %d", c.Search.MaxConcurrency)
	}
}

func TestDefaults_NormalizesLogDirEncoding(t *testing.T) {
	c := &Config{
		Systems: []SystemConfig{
			{
				Name: "s",
				Servers: []ServerConfig{
					{
						Name: "sv",
						Host: "1.2.3.4",
						LogDirs: []LogDirEntry{
							{Path: "/a", Encoding: "GBK"},
							{Path: "/b", Encoding: "gb18030"},
							{Path: "/c", Encoding: "UTF-8"},
							{Path: "/d", Encoding: "utf8"},
							{Path: "/e", Encoding: ""}, // 空串 → 默认 utf-8
						},
					},
				},
			},
		},
	}
	c.Defaults()
	encs := []string{
		c.Systems[0].Servers[0].LogDirs[0].Encoding,
		c.Systems[0].Servers[0].LogDirs[1].Encoding,
		c.Systems[0].Servers[0].LogDirs[2].Encoding,
		c.Systems[0].Servers[0].LogDirs[3].Encoding,
		c.Systems[0].Servers[0].LogDirs[4].Encoding,
	}
	want := []string{"gbk", "gbk", "utf-8", "utf-8", "utf-8"}
	for i := range encs {
		if encs[i] != want[i] {
			t.Errorf("enc[%d]=%q, want %q", i, encs[i], want[i])
		}
	}
}

// TestDefaults_DoesNotSwallowInvalidEncoding 回归（P2-编码）：
// 旧 Defaults 会把未知 encoding 静默改成 utf-8，掩盖用户配错的事实。
// 新版 Defaults 必须保留原值，让 Validate() 显式报错。
func TestDefaults_DoesNotSwallowInvalidEncoding(t *testing.T) {
	c := &Config{
		Systems: []SystemConfig{
			{
				Name: "s",
				Servers: []ServerConfig{
					{
						Name: "sv",
						Host: "1.2.3.4",
						LogDirs: []LogDirEntry{
							{Path: "/jp", Encoding: "shift-jis"},
							{Path: "/jp2", Encoding: "EUC-JP"},
						},
					},
				},
			},
		},
	}
	c.Defaults()
	if c.Systems[0].Servers[0].LogDirs[0].Encoding != "shift-jis" {
		t.Errorf("shift-jis 应当原样保留，得到: %q",
			c.Systems[0].Servers[0].LogDirs[0].Encoding)
	}
	if c.Systems[0].Servers[0].LogDirs[1].Encoding != "EUC-JP" {
		t.Errorf("EUC-JP 应当原样保留，得到: %q",
			c.Systems[0].Servers[0].LogDirs[1].Encoding)
	}
}

// TestLoadRejectsInvalidEncoding 验证 Load() 整条链：未知 encoding → Validate → error。
//
// 旧 Defaults 吞掉非法值时，Load 会"成功"但用户看到的是 utf-8 行为。
// 新版要让 Load 明确报错，告诉用户哪个 encoding 写错了。
func TestLoadRejectsInvalidEncoding(t *testing.T) {
	yamlText := `
app:
  host: 127.0.0.1
systems:
  - name: sys
    servers:
      - name: sv
        host: 1.1.1.1
        auth_type: password
        log_dirs:
          - path: /jp
            encoding: shift-jis
`
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(path, []byte(yamlText), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load 应该拒绝非法 encoding shift-jis")
	}
	if !strings.Contains(err.Error(), "encoding") {
		t.Errorf("错误信息应提到 encoding，得到: %v", err)
	}
}

func TestDefaults_AppliesServerDefaults(t *testing.T) {
	c := &Config{
		Systems: []SystemConfig{
			{
				Name: "s",
				Servers: []ServerConfig{
					{Host: "1.2.3.4", LogDirs: []LogDirEntry{{Path: "/a"}}},
				},
			},
		},
	}
	c.Defaults()
	srv := &c.Systems[0].Servers[0]
	if srv.Port != 22 {
		t.Errorf("Port default: %d", srv.Port)
	}
	if srv.AuthType != "password" {
		t.Errorf("AuthType default: %q", srv.AuthType)
	}
	if srv.Name != "1.2.3.4" {
		t.Errorf("Name fallback to host: %q", srv.Name)
	}
	if srv.LogDirs[0].Name != "/a" {
		t.Errorf("LogDir.Name fallback to path: %q", srv.LogDirs[0].Name)
	}
	if len(srv.LogDirs[0].Patterns) != 1 || srv.LogDirs[0].Patterns[0] != "*.log" {
		t.Errorf("Patterns default: %v", srv.LogDirs[0].Patterns)
	}
}

func TestValidate_RejectsNonLocalhost(t *testing.T) {
	c := &Config{App: AppConfig{Host: "0.0.0.0"}}
	c.Defaults()
	if err := c.Validate(); err == nil {
		t.Fatal("expected validation failure for 0.0.0.0")
	}
}

// TestValidate_BE006_RejectsWildcardHost 验证 BE-006：0.0.0.0 / :: / [::]
// 一律拒绝，即使 auth.enabled=true。
func TestValidate_BE006_RejectsWildcardHost(t *testing.T) {
	cases := []struct {
		name string
		host string
	}{
		{"0.0.0.0", "0.0.0.0"},
		{"::", "::"},
		{"[::]", "[::]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{App: AppConfig{Host: c.host}}
			cfg.Defaults()
			// auth 未启用也应拒绝
			if err := cfg.Validate(); err == nil {
				t.Fatalf("auth 未启用时 %q 应被拒绝", c.host)
			}
			// auth 启用也应拒绝（BE-006）
			cfg.Auth = AuthConfig{Enabled: true}
			cfg.Auth.Tokens = []AuthToken{{Name: "admin", Token: "abcdefghijklmnopqrstuvwx"}}
			if err := cfg.Validate(); err == nil {
				t.Fatalf("auth 启用时 %q 也应被拒绝（BE-006）", c.host)
			}
		})
	}
}

func TestValidate_RequiresSystems(t *testing.T) {
	c := &Config{App: AppConfig{Host: "127.0.0.1"}}
	c.Defaults()
	if err := c.Validate(); err == nil {
		t.Fatal("empty systems should fail")
	}
}

func TestValidate_RequiresServerFields(t *testing.T) {
	c := &Config{
		App: AppConfig{Host: "127.0.0.1"},
		Systems: []SystemConfig{
			{
				Name:    "sys",
				Servers: []ServerConfig{{Name: "sv", Host: "", LogDirs: []LogDirEntry{{Path: "/a"}}}},
			},
		},
	}
	c.Defaults()
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("empty host should fail with 'host' error, got: %v", err)
	}
}

func TestValidate_RejectsNonPasswordAuth(t *testing.T) {
	c := &Config{
		App: AppConfig{Host: "127.0.0.1"},
		Systems: []SystemConfig{
			{
				Name: "sys",
				Servers: []ServerConfig{
					{Name: "sv", Host: "1.1.1.1", AuthType: "key", LogDirs: []LogDirEntry{{Path: "/a"}}},
				},
			},
		},
	}
	c.Defaults()
	if err := c.Validate(); err == nil {
		t.Fatal("auth_type=key should be rejected")
	}
}

func TestValidate_RejectsIllegalPattern(t *testing.T) {
	c := &Config{
		App: AppConfig{Host: "127.0.0.1"},
		Systems: []SystemConfig{
			{
				Name: "sys",
				Servers: []ServerConfig{
					{
						Name:     "sv",
						Host:     "1.1.1.1",
						AuthType: "password",
						LogDirs:  []LogDirEntry{{Path: "/a", Patterns: []string{"*.log; rm -rf /"}}},
					},
				},
			},
		},
	}
	c.Defaults()
	if err := c.Validate(); err == nil {
		t.Fatal("pattern with ; should be rejected")
	}
}

func TestValidate_RejectsBadEncoding(t *testing.T) {
	// Defaults() 不再吞非法 encoding，所以这里跑 Defaults 后 shift-jis 仍会原样保留。
	c := &Config{
		App: AppConfig{Host: "127.0.0.1"},
		Systems: []SystemConfig{
			{
				Name:    "sys",
				Servers: []ServerConfig{{Name: "sv", Host: "1.1.1.1", AuthType: "password"}},
			},
		},
	}
	c.Systems[0].Servers[0].LogDirs = []LogDirEntry{{Path: "/a", Encoding: "shift-jis"}}
	c.Defaults()
	if err := c.Validate(); err == nil {
		t.Fatal("encoding=shift-jis should be rejected")
	}
}

func TestResolvePaths_RelativeToBase(t *testing.T) {
	base := t.TempDir()
	c := &Config{
		App: AppConfig{
			DownloadDir: "dl",
			LogDir:      "lg",
			DataDir:     "da",
		},
	}
	if err := c.ResolvePaths(base); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(c.DownloadDir()) != "dl" {
		t.Errorf("DownloadDir abs: %s", c.DownloadDir())
	}
	if !filepath.IsAbs(c.DownloadDir()) {
		t.Errorf("DownloadDir should be abs: %s", c.DownloadDir())
	}
}

func TestResolvePaths_AbsoluteStays(t *testing.T) {
	c := &Config{
		App: AppConfig{
			DownloadDir: "/abs/dl",
			LogDir:      "/abs/lg",
			DataDir:     "/abs/da",
		},
	}
	if err := c.ResolvePaths("/whatever"); err != nil {
		t.Fatal(err)
	}
	if c.DownloadDir() != "/abs/dl" {
		t.Errorf("absolute path changed: %s", c.DownloadDir())
	}
}

func TestEnsureDirs(t *testing.T) {
	base := t.TempDir()
	c := &Config{
		App: AppConfig{
			downloadDirAbs: filepath.Join(base, "dl"),
			logDirAbs:      filepath.Join(base, "lg"),
			dataDirAbs:     filepath.Join(base, "da"),
		},
	}
	if err := c.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, p := range []string{c.App.downloadDirAbs, c.App.logDirAbs, c.App.dataDirAbs} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("dir not created: %s: %v", p, err)
		}
	}
}

func TestFindSystem(t *testing.T) {
	c := &Config{
		Systems: []SystemConfig{
			{Name: "alpha"},
			{Name: "beta"},
		},
	}
	if sys, ok := c.FindSystem("beta"); !ok || sys.Name != "beta" {
		t.Errorf("FindSystem beta: ok=%v, sys=%+v", ok, sys)
	}
	if _, ok := c.FindSystem("gamma"); ok {
		t.Error("FindSystem gamma should fail")
	}
}

func TestFindServer(t *testing.T) {
	c := &Config{
		Systems: []SystemConfig{
			{
				Name: "alpha",
				Servers: []ServerConfig{
					{Name: "s1"},
					{Name: "s2"},
				},
			},
		},
	}
	sys, srv, ok := c.FindServer("alpha", "s2")
	if !ok || srv.Name != "s2" || sys.Name != "alpha" {
		t.Errorf("FindServer: ok=%v, sys=%+v, srv=%+v", ok, sys, srv)
	}
	if _, _, ok := c.FindServer("alpha", "missing"); ok {
		t.Error("missing server should fail")
	}
	if _, _, ok := c.FindServer("missing", "x"); ok {
		t.Error("missing system should fail")
	}
}

func TestSearchTimeout(t *testing.T) {
	c := &Config{Search: SearchConfig{TimeoutSeconds: 15}}
	if got := c.SearchTimeout(); got != 15*time.Second {
		t.Errorf("SearchTimeout: %v", got)
	}
}

func TestAppConfig_ListenAddr(t *testing.T) {
	cases := []struct {
		host string
		port int
		want string
	}{
		{"127.0.0.1", 18080, "127.0.0.1:18080"},
		{"", 0, "127.0.0.1:18080"},
		{"", 99999, "127.0.0.1:18080"}, // out of range → default
		{"localhost", 8080, "localhost:8080"},
	}
	for _, c := range cases {
		a := AppConfig{Host: c.host, Port: c.port}
		if got := a.ListenAddr(); got != c.want {
			t.Errorf("ListenAddr(host=%q,port=%d)=%q, want %q", c.host, c.port, got, c.want)
		}
	}
}

func TestAppConfig_ListenAddr_IPv6(t *testing.T) {
	a := AppConfig{Host: "::1", Port: 8080}
	if got := a.ListenAddr(); got != "[::1]:8080" {
		t.Errorf("ListenAddr IPv6: %q", got)
	}
}

// ---------- v0.4 新字段 + Clone 深拷贝 ----------

func TestDefaults_AppliesNewFields(t *testing.T) {
	// 不显式填新字段时，应该被 Defaults 补上合理默认。
	c := &Config{}
	c.Defaults()
	if c.App.SSHLogMaxMB != 20 {
		t.Errorf("SSHLogMaxMB default: %d, want 20", c.App.SSHLogMaxMB)
	}
	if c.App.SSHLogKeep != 3 {
		t.Errorf("SSHLogKeep default: %d, want 3", c.App.SSHLogKeep)
	}
	if c.App.SSHDebug {
		t.Error("SSHDebug default should be false")
	}
	if c.App.SSHTrafficDump {
		t.Error("SSHTrafficDump default should be false")
	}
}

func TestValidate_RejectsBadSSHCompatProfile(t *testing.T) {
	c := &Config{
		App: AppConfig{
			Host:             "127.0.0.1",
			SSHCompatProfile: "made-up-profile",
		},
		Systems: []SystemConfig{
			{Name: "s", Servers: []ServerConfig{{Name: "sv", Host: "1.1.1.1", AuthType: "password", LogDirs: []LogDirEntry{{Path: "/a"}}}}},
		},
	}
	c.Defaults()
	if err := c.Validate(); err == nil {
		t.Fatal("ssh_compat_profile=made-up-profile 应该被拒")
	}
}

func TestValidate_AcceptsKnownSSHCompatProfiles(t *testing.T) {
	for _, p := range []string{"", "modern", "compat", "no-ecdh", "legacy", "auto"} {
		c := &Config{
			App: AppConfig{Host: "127.0.0.1", SSHCompatProfile: p},
			Systems: []SystemConfig{
				{Name: "s", Servers: []ServerConfig{{Name: "sv", Host: "1.1.1.1", AuthType: "password", LogDirs: []LogDirEntry{{Path: "/a"}}}}},
			},
		}
		c.Defaults()
		if err := c.Validate(); err != nil {
			t.Errorf("ssh_compat_profile=%q 应被接受: %v", p, err)
		}
	}
}

func TestValidate_RejectsBadCredentialStore(t *testing.T) {
	c := &Config{
		App: AppConfig{Host: "127.0.0.1", CredentialStore: "vault"},
		Systems: []SystemConfig{
			{Name: "s", Servers: []ServerConfig{{Name: "sv", Host: "1.1.1.1", AuthType: "password", LogDirs: []LogDirEntry{{Path: "/a"}}}}},
		},
	}
	c.Defaults()
	if err := c.Validate(); err == nil {
		t.Fatal("credential_store=vault 应被拒")
	}
}

func TestValidate_AcceptsDisabledCredentialStore(t *testing.T) {
	for _, val := range []string{"disabled", "off", "none", "DISABLED", "  off  "} {
		c := &Config{
			App: AppConfig{Host: "127.0.0.1", CredentialStore: val},
			Systems: []SystemConfig{
				{Name: "s", Servers: []ServerConfig{{Name: "sv", Host: "1.1.1.1", AuthType: "password", LogDirs: []LogDirEntry{{Path: "/a"}}}}},
			},
		}
		c.Defaults()
		if err := c.Validate(); err != nil {
			t.Errorf("credential_store=%q 应被接受, got: %v", val, err)
		}
	}
}

func TestValidate_RejectsBadCredentialKey(t *testing.T) {
	// 长度不对（63 hex 字符）
	c := &Config{
		App: AppConfig{Host: "127.0.0.1", CredentialKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde"},
		Systems: []SystemConfig{
			{Name: "s", Servers: []ServerConfig{{Name: "sv", Host: "1.1.1.1", AuthType: "password", LogDirs: []LogDirEntry{{Path: "/a"}}}}},
		},
	}
	c.Defaults()
	if err := c.Validate(); err == nil {
		t.Fatal("短 credential_key 应被拒")
	}

	// 含非 hex 字符
	c2 := &Config{
		App: AppConfig{Host: "127.0.0.1", CredentialKey: "g123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		Systems: []SystemConfig{
			{Name: "s", Servers: []ServerConfig{{Name: "sv", Host: "1.1.1.1", AuthType: "password", LogDirs: []LogDirEntry{{Path: "/a"}}}}},
		},
	}
	c2.Defaults()
	if err := c2.Validate(); err == nil {
		t.Fatal("含 g 的 credential_key 应被拒")
	}

	// 正确的 64 hex 字符密钥应通过
	goodKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c3 := &Config{
		App: AppConfig{Host: "127.0.0.1", CredentialStore: "file", CredentialKey: goodKey},
		Systems: []SystemConfig{
			{Name: "s", Servers: []ServerConfig{{Name: "sv", Host: "1.1.1.1", AuthType: "password", LogDirs: []LogDirEntry{{Path: "/a"}}}}},
		},
	}
	c3.Defaults()
	if err := c3.Validate(); err != nil {
		t.Errorf("正确 credential_key 应被接受, got: %v", err)
	}
}

func TestValidate_RejectsBadServerSSHProfile(t *testing.T) {
	c := &Config{
		App: AppConfig{Host: "127.0.0.1"},
		Systems: []SystemConfig{
			{Name: "s", Servers: []ServerConfig{
				{Name: "sv", Host: "1.1.1.1", AuthType: "password", SSHProfile: "made-up", LogDirs: []LogDirEntry{{Path: "/a"}}},
			}},
		},
	}
	c.Defaults()
	if err := c.Validate(); err == nil {
		t.Fatal("server.ssh_profile=made-up 应被拒")
	}
}

func TestValidate_RejectsBadSSHLogSettings(t *testing.T) {
	// ssh_log_max_mb 上限 1024
	c := &Config{
		App: AppConfig{Host: "127.0.0.1", SSHLogMaxMB: 9999},
		Systems: []SystemConfig{
			{Name: "s", Servers: []ServerConfig{{Name: "sv", Host: "1.1.1.1", AuthType: "password", LogDirs: []LogDirEntry{{Path: "/a"}}}}},
		},
	}
	c.Defaults()
	if err := c.Validate(); err == nil {
		t.Fatal("ssh_log_max_mb=9999 应被拒")
	}
	// ssh_log_keep 上限 100
	c2 := &Config{
		App: AppConfig{Host: "127.0.0.1", SSHLogKeep: 9999},
		Systems: []SystemConfig{
			{Name: "s", Servers: []ServerConfig{{Name: "sv", Host: "1.1.1.1", AuthType: "password", LogDirs: []LogDirEntry{{Path: "/a"}}}}},
		},
	}
	c2.Defaults()
	if err := c2.Validate(); err == nil {
		t.Fatal("ssh_log_keep=9999 应被拒")
	}
}

func TestConfig_Clone_DeepCopy(t *testing.T) {
	// 准备一份"配置 A"，跑 Clone 得到"配置 B"。
	// 修改 B 的 inner slice / pointer 字段，A 不应受影响。
	trueVal := true
	src := &Config{
		App: AppConfig{
			Name:                  "orig",
			Host:                  "127.0.0.1",
			EnableFreeFileBrowser: &trueVal,
			FreeFileRoots:         []string{"/opt", "/var"},
			SSHDebug:              true,
			SSHCompatProfile:      "compat",
		},
		Systems: []SystemConfig{
			{
				Name: "s1",
				Servers: []ServerConfig{
					{
						Name:          "srv1",
						Host:          "1.1.1.1",
						Port:          22,
						AuthType:      "password",
						SSHProfile:    "modern",
						HostKeySHA256: "abc",
						LogDirs: []LogDirEntry{
							{Name: "ld1", Path: "/a", Patterns: []string{"*.log"}, Encoding: "utf-8"},
						},
					},
				},
			},
		},
	}

	dst := src.Clone()
	if dst == src {
		t.Fatal("Clone 应该返回不同指针")
	}

	// 改 dst 的所有 inner slice
	dst.Systems[0].Name = "modified"
	dst.Systems[0].Servers[0].Host = "9.9.9.9"
	dst.Systems[0].Servers[0].LogDirs[0].Path = "/z"
	dst.Systems[0].Servers[0].LogDirs[0].Patterns[0] = "*.txt"
	dst.App.FreeFileRoots[0] = "/etc"
	*dst.App.EnableFreeFileBrowser = false

	// 验证 src 完全没被改
	if src.Systems[0].Name != "s1" {
		t.Errorf("src.Systems[0].Name 被污染: %q", src.Systems[0].Name)
	}
	if src.Systems[0].Servers[0].Host != "1.1.1.1" {
		t.Errorf("src.Servers[0].Host 被污染: %q", src.Systems[0].Servers[0].Host)
	}
	if src.Systems[0].Servers[0].LogDirs[0].Path != "/a" {
		t.Errorf("src.LogDirs[0].Path 被污染: %q", src.Systems[0].Servers[0].LogDirs[0].Path)
	}
	if src.Systems[0].Servers[0].LogDirs[0].Patterns[0] != "*.log" {
		t.Errorf("src.Patterns[0] 被污染: %q", src.Systems[0].Servers[0].LogDirs[0].Patterns[0])
	}
	if src.App.FreeFileRoots[0] != "/opt" {
		t.Errorf("src.FreeFileRoots[0] 被污染: %q", src.App.FreeFileRoots[0])
	}
	if !*src.App.EnableFreeFileBrowser {
		t.Error("src.EnableFreeFileBrowser 应该是 true，被改成了 false")
	}
}

func TestConfig_Clone_NilSafe(t *testing.T) {
	var c *Config
	if got := c.Clone(); got != nil {
		t.Errorf("nil.Clone() 应返回 nil，得到: %+v", got)
	}
}

func TestConfig_Clone_NilInnerSlices(t *testing.T) {
	// src 各 slice 都是 nil 时 Clone 不应 panic
	src := &Config{App: AppConfig{Name: "x", Host: "127.0.0.1"}}
	dst := src.Clone()
	if dst == nil {
		t.Fatal("Clone 返回 nil")
	}
	if dst.App.FreeFileRoots != nil {
		t.Errorf("空 src 的 FreeFileRoots 应是 nil，得到: %v", dst.App.FreeFileRoots)
	}
	if dst.Systems != nil {
		t.Errorf("空 src 的 Systems 应是 nil，得到: %v", dst.Systems)
	}
}

// TestFreeFileRootsEnabled 验证 free_file_roots 白名单匹配逻辑（项 14）。
//
// v0.9 起 fail-closed：空 roots → 一律拒绝（不再"最自由模式"）；
// 显式 "*" / "ANY" → 全部放行（用户明确同意承担风险）。
func TestFreeFileRootsEnabled(t *testing.T) {
	cases := []struct {
		name  string
		roots []string
		path  string
		want  bool
	}{
		// v0.9 fail-closed：空 roots = 拒绝（不再是放行）
		{"empty roots reject all (fail-closed)", nil, "/var/log", false},
		{"empty roots reject root (fail-closed)", nil, "/", false},
		{"empty roots reject etc", nil, "/etc/passwd", false},
		// 显式 "*" / "ANY" → 放行
		{"star allows all", []string{"*"}, "/etc/passwd", true},
		{"ANY allows all (case-insensitive)", []string{"ANY"}, "/var/log/x", true},
		{"any allows all (lowercase)", []string{"any"}, "/var/log/x", true},
		{"star with spaces", []string{"  *  "}, "/etc/shadow", true},
		// 单个 root
		{"exact match", []string{"/var/log"}, "/var/log", true},
		{"subpath match", []string{"/var/log"}, "/var/log/app.log", true},
		{"deeper subpath", []string{"/var/log"}, "/var/log/sub/file", true},
		// 边界：不能跨目录
		{"prefix not directory boundary", []string{"/var/log"}, "/var/logs", false},
		{"unrelated path", []string{"/var/log"}, "/etc/passwd", false},
		// 多个 root
		{"first root", []string{"/var/log", "/opt/was"}, "/var/log/x", true},
		{"second root", []string{"/var/log", "/opt/was"}, "/opt/was/SystemOut.log", true},
		{"neither", []string{"/var/log", "/opt/was"}, "/home/user", false},
		// 大小写敏感
		{"case sensitive", []string{"/var/log"}, "/VAR/log", false},
		// 路径清理
		{"trailing slash cleaned", []string{"/var/log/"}, "/var/log/x", true},
		// 空 path
		{"empty path with roots", []string{"/var/log"}, "", false},
		{"empty path no roots (fail-closed)", nil, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &AppConfig{FreeFileRoots: c.roots}
			if got := a.FreeFileRootsEnabled(c.path); got != c.want {
				t.Errorf("FreeFileRootsEnabled(%q) with roots %v = %v, want %v",
					c.path, c.roots, got, c.want)
			}
		})
	}
}

// TestCredentialStoreEnabled 验证 credential_store 解析（项 23）。
func TestCredentialStoreEnabled(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"", "keyring"},
		{"keyring", "keyring"},
		{"KEYRING", "keyring"},
		{"  keyring  ", "keyring"},
		{"file", "file"},
		{"FILE", "file"},
		{"disabled", "disabled"},
		{"off", "disabled"},
		{"none", "disabled"},
		{"DISABLED", "disabled"},
		{"unknown", "keyring"}, // 未知值兜底为 keyring
	}
	for _, c := range cases {
		t.Run("raw="+c.raw, func(t *testing.T) {
			a := &AppConfig{CredentialStore: c.raw}
			if got := a.CredentialStoreEnabled(); got != c.want {
				t.Errorf("CredentialStoreEnabled() with raw=%q = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

// TestListModeIsAuto 验证 list_mode 是否为 "auto" 的判定逻辑（项 6 修复）。
//
// "" 和 "auto" 都算 auto（handler 走 gnu_find → posix_ls fallback）；
// 显式 "gnu_find" / "posix_ls" 不算 auto（handler 不做 fallback）。
// 大小写和空格都归一化。
func TestListModeIsAuto(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"", true},
		{"auto", true},
		{"AUTO", true},
		{"  auto  ", true},
		{"gnu_find", false},
		{"gnu", false},
		{"find", false},
		{"posix_ls", false},
		{"posix", false},
		{"ls", false},
		{"unknown", false},
	}
	for _, c := range cases {
		t.Run("raw="+c.raw, func(t *testing.T) {
			ld := &LogDirEntry{ListMode: c.raw}
			if got := ld.ListModeIsAuto(); got != c.want {
				t.Errorf("ListModeIsAuto() with raw=%q = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}

// TestListModeFor 验证 list_mode 归一化（兼容项 6 之前的语义）。
func TestListModeFor(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"", "gnu_find"},
		{"auto", "gnu_find"},
		{"gnu_find", "gnu_find"},
		{"gnu", "gnu_find"},
		{"find", "gnu_find"},
		{"posix_ls", "posix_ls"},
		{"posix", "posix_ls"},
		{"ls", "posix_ls"},
		{"GNU_FIND", "gnu_find"},
		{"  gnu_find  ", "gnu_find"},
		{"unknown", "unknown"}, // 未知值原样保留
	}
	for _, c := range cases {
		t.Run("raw="+c.raw, func(t *testing.T) {
			ld := &LogDirEntry{ListMode: c.raw}
			if got := ld.ListModeFor(); got != c.want {
				t.Errorf("ListModeFor() with raw=%q = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

// v0.5-G #18：AllowCustomDownloadDirEnabled（默认 true / 显式 false 才禁用）
func TestAllowCustomDownloadDirEnabled(t *testing.T) {
	// nil → 默认 true
	a := AppConfig{}
	if !a.AllowCustomDownloadDirEnabled() {
		t.Error("nil 应默认允许")
	}
	// 显式 true
	tr := true
	a.AllowCustomDownloadDir = &tr
	if !a.AllowCustomDownloadDirEnabled() {
		t.Error("显式 true 应允许")
	}
	// 显式 false
	fl := false
	a.AllowCustomDownloadDir = &fl
	if a.AllowCustomDownloadDirEnabled() {
		t.Error("显式 false 应拒绝")
	}
}

// v0.5-G #18：TargetDirAllowed（按 allowed_download_roots 白名单 + 目录边界）
//
// v0.9 起 fail-closed：空 roots → 一律拒绝（不再"全放行"）；
// 显式 "*" / "ANY" → 全部放行（用户明确同意承担风险）。
func TestTargetDirAllowed(t *testing.T) {
	// v0.9 fail-closed：空 roots → 全拒绝
	a := AppConfig{}
	for _, p := range []string{"/tmp/x", "D:\\Windows\\System32", "/var/log/app"} {
		if a.TargetDirAllowed(p) {
			t.Errorf("BE-007 fail-closed: 空 roots 应拒绝 %q", p)
		}
	}
	// 显式 "*" / "ANY" → 全放行
	a.AllowedDownloadRoots = []string{"*"}
	for _, p := range []string{"/tmp/x", "D:\\Windows\\System32", "/etc/passwd"} {
		if !a.TargetDirAllowed(p) {
			t.Errorf("显式 * 应放行 %q", p)
		}
	}
	a.AllowedDownloadRoots = []string{"ANY"}
	if !a.TargetDirAllowed("/var/log/app") {
		t.Error("显式 ANY 应放行")
	}
	// 非空 roots + 子路径放行
	a.AllowedDownloadRoots = []string{"/tmp", "D:/logs"}
	for _, p := range []string{"/tmp", "/tmp/x", "/tmp/sub/y", "D:/logs", "D:/logs/2026", "D:\\logs\\2026", "d:/LOGS/2026"} {
		if !a.TargetDirAllowed(p) {
			t.Errorf("子路径应放行 %q", p)
		}
	}
	// 越界拒绝
	for _, p := range []string{"/etc/passwd", "/var/log", "D:\\Windows", "D:/Windows", "E:/x"} {
		if a.TargetDirAllowed(p) {
			t.Errorf("越界应拒绝 %q", p)
		}
	}
	// 边界：恰好是 root 自身
	if !a.TargetDirAllowed("/tmp") {
		t.Error("/tmp 应放行（恰好是 root）")
	}
	// 边界：前缀相同但不是子目录（必须按目录分隔符边界）
	// "/tmpfoo" 不应通过 "/tmp" 的匹配
	if a.TargetDirAllowed("/tmpfoo") {
		t.Error("/tmpfoo 不应通过 /tmp 边界匹配")
	}

	// 空白 root 应被忽略，不能意外放行相对路径或当前目录下路径。
	a.AllowedDownloadRoots = []string{" "}
	if a.TargetDirAllowed("/tmp/x") {
		t.Error("空白 root 不应放行任何非空路径")
	}
}

// TestComparePathAllowed v0.9 BE-001：compare_allowed_roots 白名单（fail-closed）。
func TestComparePathAllowed(t *testing.T) {
	// 空 roots → 一律拒绝
	a := AppConfig{}
	if a.ComparePathAllowed("/etc/passwd") {
		t.Error("空 roots 应拒绝 /etc/passwd")
	}
	if a.ComparePathAllowed("/var/log/app") {
		t.Error("空 roots 应拒绝 /var/log/app")
	}
	// 显式 "*" → 全放行
	a.CompareAllowedRoots = []string{"*"}
	if !a.ComparePathAllowed("/etc/passwd") {
		t.Error("显式 * 应放行 /etc/passwd")
	}
	if !a.ComparePathAllowed(`C:\Windows\System32\drivers\etc\hosts`) {
		t.Error("显式 * 应放行 Windows 路径")
	}
	// ANY（大小写不敏感）
	a.CompareAllowedRoots = []string{"ANY"}
	if !a.ComparePathAllowed("/etc/shadow") {
		t.Error("ANY 应放行 /etc/shadow")
	}
	// 非空 roots + 子路径放行
	a.CompareAllowedRoots = []string{"/var/log", "/opt/was"}
	for _, p := range []string{"/var/log", "/var/log/app.log", "/opt/was/x"} {
		if !a.ComparePathAllowed(p) {
			t.Errorf("白名单内应放行 %q", p)
		}
	}
	// 越界拒绝
	for _, p := range []string{"/etc/passwd", "/var/logs", "/home/user"} {
		if a.ComparePathAllowed(p) {
			t.Errorf("越界应拒绝 %q", p)
		}
	}
	// 空 path
	a.CompareAllowedRoots = []string{"/var/log"}
	if a.ComparePathAllowed("") {
		t.Error("空 path 应拒绝")
	}
}

// TestTailIdleDuration 验证 BE-002 配置项 tail_idle_minutes 的解析。
func TestTailIdleDuration(t *testing.T) {
	cases := []struct {
		name string
		min  *int
		want time.Duration
	}{
		{"nil → 默认 30 分钟", nil, 30 * time.Minute},
		{"0 → 默认 30 分钟", intPtr(0), 30 * time.Minute},
		{"负数 → 默认 30 分钟", intPtr(-5), 30 * time.Minute},
		{"< 5 分钟 → 兜底 5 分钟", intPtr(1), 5 * time.Minute},
		{"= 5 分钟", intPtr(5), 5 * time.Minute},
		{"= 30 分钟（默认）", intPtr(30), 30 * time.Minute},
		{"= 60 分钟", intPtr(60), 60 * time.Minute},
		{"= 120 分钟", intPtr(120), 120 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := AppConfig{TailIdleMinutes: c.min}
			got := a.TailIdleDuration()
			if got != c.want {
				t.Errorf("TailIdleDuration() = %v, want %v", got, c.want)
			}
		})
	}
}

func intPtr(n int) *int { return &n }
