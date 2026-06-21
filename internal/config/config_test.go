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
	if c.App.Name != "内网运维工具箱" {
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
							{Path: "/d", Encoding: "weird"},
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
	}
	want := []string{"gbk", "gbk", "utf-8", "utf-8"}
	for i := range encs {
		if encs[i] != want[i] {
			t.Errorf("enc[%d]=%q, want %q", i, encs[i], want[i])
		}
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
	// 注意：Defaults() 会先把未知 encoding 归一化为 utf-8，
	// 所以这里跳过 Defaults，直接构造一个非法 encoding 来测 Validate 的字面校验。
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
