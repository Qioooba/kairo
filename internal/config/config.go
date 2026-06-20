// Package config 负责加载和校验 config.yaml。
//
// 第一版只支持用户名密码登录，密码不在配置文件中出现，
// 每次由页面传入。所有可访问的远程目录都来自白名单。
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 整个配置文件结构
type Config struct {
	App     AppConfig      `yaml:"app" json:"app"`
	Systems []SystemConfig `yaml:"systems" json:"systems"`
	Search  SearchConfig   `yaml:"search" json:"search"`
}

// AppConfig 应用自身配置
type AppConfig struct {
	Name            string `yaml:"name" json:"name"`
	Host            string `yaml:"host" json:"host"` // 默认 127.0.0.1
	Port            int    `yaml:"port" json:"port"` // 默认 18080
	AutoOpenBrowser bool   `yaml:"auto_open_browser" json:"auto_open_browser"`
	DownloadDir     string `yaml:"download_dir" json:"download_dir"`
	LogDir          string `yaml:"log_dir" json:"log_dir"`
	DataDir         string `yaml:"data_dir" json:"data_dir"`
	// 解析后的绝对路径
	downloadDirAbs string
	logDirAbs      string
	dataDirAbs     string
}

// ListenAddr 返回绑定地址，例如 127.0.0.1:18080
func (a *AppConfig) ListenAddr() string {
	host := strings.TrimSpace(a.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	if a.Port <= 0 || a.Port > 65535 {
		a.Port = 18080
	}
	return net.JoinHostPort(host, strconv.Itoa(a.Port))
}

// SystemConfig 一个逻辑系统（业务系统）
type SystemConfig struct {
	Name        string         `yaml:"name" json:"name"`
	Description string         `yaml:"description" json:"description"`
	Servers     []ServerConfig `yaml:"servers" json:"servers"`
}

// ServerConfig 一台目标服务器
type ServerConfig struct {
	Name     string        `yaml:"name" json:"name"`
	Host     string        `yaml:"host" json:"host"`
	Port     int           `yaml:"port" json:"port"`
	Username string        `yaml:"username" json:"username"`
	AuthType string        `yaml:"auth_type" json:"auth_type"`
	LogDirs  []LogDirEntry `yaml:"log_dirs" json:"log_dirs"`
}

// LogDirEntry 一个允许访问的日志目录
type LogDirEntry struct {
	Name     string   `yaml:"name" json:"name"`
	Path     string   `yaml:"path" json:"path"`
	Patterns []string `yaml:"patterns" json:"patterns"`
	Encoding string   `yaml:"encoding" json:"encoding"` // utf-8 (默认) / gbk
}

// SearchConfig 搜索相关默认值
type SearchConfig struct {
	DefaultLatestFiles  int `yaml:"default_latest_files" json:"default_latest_files"`
	MaxMatches          int `yaml:"max_matches" json:"max_matches"`
	DefaultContextLines int `yaml:"default_context_lines" json:"default_context_lines"`
	TimeoutSeconds      int `yaml:"timeout_seconds" json:"timeout_seconds"`
	MaxConcurrency      int `yaml:"max_concurrency" json:"max_concurrency"`
}

// Defaults 把缺失值填上合理默认
func (c *Config) Defaults() {
	if c.App.Name == "" {
		c.App.Name = "内网运维工具箱"
	}
	if c.App.Host == "" {
		c.App.Host = "127.0.0.1"
	}
	if c.App.Port == 0 {
		c.App.Port = 18080
	}
	if c.App.DownloadDir == "" {
		c.App.DownloadDir = "./downloads"
	}
	if c.App.LogDir == "" {
		c.App.LogDir = "./logs"
	}
	if c.App.DataDir == "" {
		c.App.DataDir = "./data"
	}
	if c.Search.DefaultLatestFiles == 0 {
		c.Search.DefaultLatestFiles = 3
	}
	if c.Search.MaxMatches == 0 {
		c.Search.MaxMatches = 200
	}
	if c.Search.DefaultContextLines == 0 {
		c.Search.DefaultContextLines = 30
	}
	if c.Search.TimeoutSeconds == 0 {
		c.Search.TimeoutSeconds = 30
	}
	if c.Search.MaxConcurrency == 0 {
		c.Search.MaxConcurrency = 2
	}
	for i := range c.Systems {
		sys := &c.Systems[i]
		for j := range sys.Servers {
			srv := &sys.Servers[j]
			if srv.Port == 0 {
				srv.Port = 22
			}
			if srv.AuthType == "" {
				srv.AuthType = "password"
			}
			if srv.Name == "" {
				srv.Name = srv.Host
			}
			for k := range srv.LogDirs {
				ld := &srv.LogDirs[k]
				if len(ld.Patterns) == 0 {
					ld.Patterns = []string{"*.log"}
				}
				if ld.Name == "" {
					ld.Name = ld.Path
				}
				if strings.EqualFold(ld.Encoding, "gbk") || strings.EqualFold(ld.Encoding, "gb18030") {
					ld.Encoding = "gbk"
				} else {
					ld.Encoding = "utf-8"
				}
			}
		}
	}
}

// Load 加载并校验 config.yaml
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析 yaml 失败: %w", err)
	}
	cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate 校验关键字段
func (c *Config) Validate() error {
	if c.App.Host != "127.0.0.1" && c.App.Host != "localhost" {
		// 安全要求：只允许本地监听
		return fmt.Errorf("app.host 必须为 127.0.0.1 或 localhost，当前: %q", c.App.Host)
	}
	if len(c.Systems) == 0 {
		return fmt.Errorf("配置中没有 systems，至少需要一个")
	}
	for si, sys := range c.Systems {
		if strings.TrimSpace(sys.Name) == "" {
			return fmt.Errorf("systems[%d].name 不能为空", si)
		}
		if len(sys.Servers) == 0 {
			return fmt.Errorf("系统 %q 没有 servers", sys.Name)
		}
		for ssi, srv := range sys.Servers {
			if strings.TrimSpace(srv.Host) == "" {
				return fmt.Errorf("系统 %q 第 %d 台服务器 host 不能为空", sys.Name, ssi+1)
			}
			if srv.AuthType != "password" {
				return fmt.Errorf("系统 %q 服务器 %q auth_type 仅支持 password", sys.Name, srv.Name)
			}
			if len(srv.LogDirs) == 0 {
				return fmt.Errorf("系统 %q 服务器 %q 没有 log_dirs", sys.Name, srv.Name)
			}
			for ldi, ld := range srv.LogDirs {
				if strings.TrimSpace(ld.Path) == "" {
					return fmt.Errorf("系统 %q 服务器 %q 第 %d 个 log_dir.path 不能为空",
						sys.Name, srv.Name, ldi+1)
				}
				enc := strings.ToLower(strings.TrimSpace(ld.Encoding))
				if enc != "" && enc != "utf-8" && enc != "gbk" && enc != "gb18030" {
					return fmt.Errorf("系统 %q 服务器 %q log_dir %q 的 encoding 非法: %q（仅支持 utf-8 / gbk）",
						sys.Name, srv.Name, ld.Path, ld.Encoding)
				}
				for pi, p := range ld.Patterns {
					if strings.ContainsAny(p, ";|&`$(){}[]<>\\\"' \n\r\t") {
						return fmt.Errorf("系统 %q 服务器 %q log_dir %q 的 pattern[%d] 含有非法字符: %q",
							sys.Name, srv.Name, ld.Path, pi, p)
					}
				}
			}
		}
	}
	return nil
}

// ResolvePaths 把相对路径解析为基于 baseDir 的绝对路径
func (c *Config) ResolvePaths(baseDir string) error {
	abs := func(rel string) (string, error) {
		if rel == "" {
			return "", nil
		}
		if filepath.IsAbs(rel) {
			return rel, nil
		}
		return filepath.Abs(filepath.Join(baseDir, rel))
	}
	var err error
	if c.App.downloadDirAbs, err = abs(c.App.DownloadDir); err != nil {
		return err
	}
	if c.App.logDirAbs, err = abs(c.App.LogDir); err != nil {
		return err
	}
	if c.App.dataDirAbs, err = abs(c.App.DataDir); err != nil {
		return err
	}
	return nil
}

// EnsureDirs 创建运行时需要的目录
func (c *Config) EnsureDirs() error {
	for _, p := range []string{c.App.downloadDirAbs, c.App.logDirAbs, c.App.dataDirAbs} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", p, err)
		}
	}
	return nil
}

// DownloadDir 绝对路径
func (c *Config) DownloadDir() string { return c.App.downloadDirAbs }

// LogDir 绝对路径
func (c *Config) LogDir() string { return c.App.logDirAbs }

// DataDir 绝对路径
func (c *Config) DataDir() string { return c.App.dataDirAbs }

// FindSystem 通过名字定位系统
func (c *Config) FindSystem(name string) (*SystemConfig, bool) {
	for i := range c.Systems {
		if c.Systems[i].Name == name {
			return &c.Systems[i], true
		}
	}
	return nil, false
}

// FindServer 在指定系统中找服务器
func (c *Config) FindServer(sysName, serverName string) (*SystemConfig, *ServerConfig, bool) {
	sys, ok := c.FindSystem(sysName)
	if !ok {
		return nil, nil, false
	}
	for i := range sys.Servers {
		if sys.Servers[i].Name == serverName {
			return sys, &sys.Servers[i], true
		}
	}
	return nil, nil, false
}

// SearchTimeout 返回带单位的搜索超时
func (c *Config) SearchTimeout() time.Duration {
	return time.Duration(c.Search.TimeoutSeconds) * time.Second
}
