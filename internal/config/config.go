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

	// EnableFreeFileBrowser 控制 v0.3 文件浏览器（任意路径下载）是否可用。
	// true / 未设置 = 启用；false = 拒绝所有 /api/files/* 请求。
	// 关闭原因：任意路径下载 = 按 SSH 账号实际权限放行，对内网自用是好事，
	// 但若工具分发给"未严格管控的同事"，可能因安全审计被卡。
	EnableFreeFileBrowser *bool `yaml:"enable_free_file_browser,omitempty" json:"enable_free_file_browser,omitempty"`

	// FreeFileRoots v0.4 起可用：白名单"任意路径下载"允许访问的远端根路径前缀。
	// 为空 = 行为同 v0.3（按 SSH 账号实际权限放行）；
	// 非空 = 只允许访问以列表中某项为前缀的远端路径。
	// Agent 2 在前端做启动警告；这里只做字段存读。
	FreeFileRoots []string `yaml:"free_file_roots,omitempty" json:"free_file_roots,omitempty"`

	// CredentialStore v0.4 起配置化：keyring / file / disabled。
	// 空字符串 / "keyring" = 走 keyring（macOS Keychain / Windows DPAPI / Linux Secret Service）；
	// "file" = 走文件密文（AES-GCM 加密，存 data/credentials.json）；
	// "disabled" / "off" / "none" = 不保存密码，每次都需要用户手动输入。
	CredentialStore string `yaml:"credential_store,omitempty" json:"credential_store,omitempty"`

	// CredentialKey v0.4 file 模式使用的 AES-256 密钥（hex 编码，64 个十六进制字符）。
	// 留空时自动生成并保存到 data/.credkey（自动模式）。
	CredentialKey string `yaml:"credential_key,omitempty" json:"credential_key,omitempty"`

	// SSHDebug / SSHTrafficDump / SSHLogMaxMB / SSHLogKeep 控制 SSH debug/traffic 日志。
	// 默认全关（false / 20 / 3）。
	// 详见 internal/sshclient 的注释。
	SSHDebug       bool `yaml:"ssh_debug" json:"ssh_debug"`
	SSHTrafficDump bool `yaml:"ssh_traffic_dump" json:"ssh_traffic_dump"`
	SSHLogMaxMB    int  `yaml:"ssh_log_max_mb" json:"ssh_log_max_mb"`
	SSHLogKeep     int  `yaml:"ssh_log_keep" json:"ssh_log_keep"`

	// SSHCompatProfile v0.4 起配置化（modern / compat / no-ecdh / legacy / auto）。
	// "" / "auto" = 走默认重试链（保持向后兼容）。
	SSHCompatProfile string `yaml:"ssh_compat_profile,omitempty" json:"ssh_compat_profile,omitempty"`

	// AllowCustomDownloadDir v0.5-G 起：是否允许用户在下载请求里指定 target_dir
	// （v0.5 项 18「FTP 下载到指定目录」）。
	//   - true / 未设置 = 允许（默认；向后兼容）
	//   - false         = 一律拒绝带 target_dir 的下载请求（强制走 download_dir）
	// 默认开启原因：内网自用工具，下载到指定目录是高频需求。
	AllowCustomDownloadDir *bool `yaml:"allow_custom_download_dir,omitempty" json:"allow_custom_download_dir,omitempty"`

	// AllowedDownloadRoots v0.5-G 起：target_dir 白名单（绝对路径前缀）。
	// 为空 = 任何绝对路径都允许；非空 = 只允许落在列表中某项前缀下的目录。
	// 用途：限制"下载到指定目录"能写到哪些盘符/根目录，
	// 例如 ["D:/logs", "E:/downloads"] 防止误下到 C:\Windows 等敏感位置。
	// 匹配按平台路径分隔符边界（Windows 盘符也算边界）。
	AllowedDownloadRoots []string `yaml:"allowed_download_roots,omitempty" json:"allowed_download_roots,omitempty"`

	// ExternalOpeners v0.8 起：用户自定义的"用外部程序打开下载文件"列表。
	// 每个元素 {Name, Path, Icon}，Name 在 openers 间唯一，作为前端按钮标识符。
	// Icon 是 emoji（如 "📝" / "💡"），前端拿来当按钮图标 —— 不去解析 .exe/.app 真实图标。
	// Path 是可执行文件的绝对路径（Windows .exe / macOS .app 的可执行 / Linux ELF）。
	// 调 /api/local/open-with 时按 Name 查表，再用 Path + 文件绝对路径 exec.Command 启动。
	// 留空 = 不启用该功能（下载历史不会显示额外按钮）。
	ExternalOpeners []ExternalOpener `yaml:"external_openers,omitempty" json:"external_openers,omitempty"`

	// DownloadRetentionDays 下载文件/记录保留天数。
	// 默认 7 天；0 = 不自动按时间清理。
	DownloadRetentionDays *int `yaml:"download_retention_days,omitempty" json:"download_retention_days,omitempty"`

	// DownloadMaxCount 最大下载记录数。
	// 默认 1000 条；超过则删除最旧的记录和对应文件；0 = 不限制数量。
	DownloadMaxCount *int `yaml:"download_max_count,omitempty" json:"download_max_count,omitempty"`

	// 解析后的绝对路径
	downloadDirAbs string
	logDirAbs      string
	dataDirAbs     string
}

// FreeFileBrowserEnabled 返回"任意路径下载"是否启用。默认 true（向后兼容）。
// YAML 里显式写 false 才禁用；不写 / nil 走 true。
func (a *AppConfig) FreeFileBrowserEnabled() bool {
	if a.EnableFreeFileBrowser == nil {
		return true
	}
	return *a.EnableFreeFileBrowser
}

// FreeFileRootsEnabled 判断 path 是否在 free_file_roots 白名单里。
//
// roots 为空时放行（最自由模式）；非空时要求 path 以任一 root 为前缀。
// 匹配按 / 边界："/var/log" 匹配 "/var/log" 和 "/var/log/app.log"，但不匹配 "/var/logs"。
// 大小写敏感（远端 Linux 系统路径区分大小写）。
func (a *AppConfig) FreeFileRootsEnabled(path string) bool {
	if len(a.FreeFileRoots) == 0 {
		return true
	}
	if path == "" {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(path))
	for _, root := range a.FreeFileRoots {
		root = filepath.ToSlash(filepath.Clean(root))
		if cleaned == root {
			return true
		}
		// 必须按目录边界匹配：cleaned 是 root 的子路径
		if strings.HasPrefix(cleaned, root+"/") {
			return true
		}
	}
	return false
}

// FreeFileRootsConfigured 是否有 free_file_roots 配置（非空）。
// 用于前端判断"用户是否已限定根路径"，决定要不要显示警告。
func (a *AppConfig) FreeFileRootsConfigured() bool {
	return len(a.FreeFileRoots) > 0
}

// AllowCustomDownloadDirEnabled 返回是否允许用户在下载请求里指定 target_dir。
// 默认 true（向后兼容）。
func (a *AppConfig) AllowCustomDownloadDirEnabled() bool {
	if a.AllowCustomDownloadDir == nil {
		return true
	}
	return *a.AllowCustomDownloadDir
}

// TargetDirAllowed v0.5-G #18：检查 absolutePath 是否被 allowed_download_roots 允许。
//
// 规则：
//   - roots 为空 → 放行（最自由模式）
//   - roots 非空 → absolutePath 必须以任一 root 为前缀（按 / 或 \ 边界）
//
// 注意：
//   - absolutePath 必须是绝对路径（不在这里检查，由 validateTargetDir 保证）
//   - 跨平台：Windows 用 \，Linux/Mac 用 /；本函数把 root 和 path 都做大小写不敏感比较（Windows 不区分大小写）
//     + 双分隔符归一。
func (a *AppConfig) TargetDirAllowed(absolutePath string) bool {
	if len(a.AllowedDownloadRoots) == 0 {
		return true
	}
	if absolutePath == "" {
		return false
	}
	cleaned := filepath.Clean(absolutePath)
	for _, root := range a.AllowedDownloadRoots {
		rootCleaned := filepath.Clean(root)
		if cleaned == rootCleaned {
			return true
		}
		// 必须按目录边界匹配：cleaned 是 root 的子路径
		sep := string(filepath.Separator)
		if strings.HasPrefix(cleaned, rootCleaned+sep) {
			return true
		}
		// Windows 还要兼容跨分隔符：root 用 /，cleaned 用 \
		if strings.HasPrefix(cleaned, rootCleaned+"/") {
			return true
		}
	}
	return false
}

// CredentialStoreEnabled 解析 credential_store 配置，返回有效后端名（"keyring"/"file"/"disabled"）。
//   - 空字符串 / "keyring" → "keyring"（默认，向后兼容）
//   - "file"              → "file"
//   - "disabled"          → "disabled"（明确禁用）
//   - 其它                → 走默认 "keyring"，调用方可记 warning
func (a *AppConfig) CredentialStoreEnabled() string {
	switch strings.ToLower(strings.TrimSpace(a.CredentialStore)) {
	case "", "keyring":
		return "keyring"
	case "file":
		return "file"
	case "disabled", "off", "none":
		return "disabled"
	default:
		return "keyring"
	}
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
	Name     string `yaml:"name" json:"name"`
	Host     string `yaml:"host" json:"host"`
	Port     int    `yaml:"port" json:"port"`
	Username string `yaml:"username" json:"username"`
	AuthType string `yaml:"auth_type" json:"auth_type"`

	// SSHProfile v0.4 起可单独覆盖这台 server 的 SSH compat profile；
	// 空字符串 = 走 app.ssh_compat_profile（再空 = auto）。
	SSHProfile string `yaml:"ssh_profile,omitempty" json:"ssh_profile,omitempty"`

	// HostKeySHA256 v0.4 起可选：固定这台 server 的 host key 指纹（base64 SHA256）；
	// 设置后即使 app 级 InsecureIgnoreHostKey 仍开，本字段也会强制做指纹校验。
	// 留空 = 不做 pin。
	HostKeySHA256 string `yaml:"host_key_sha256,omitempty" json:"host_key_sha256,omitempty"`

	LogDirs []LogDirEntry `yaml:"log_dirs" json:"log_dirs"`
}

// LogDirEntry 一个允许访问的日志目录
type LogDirEntry struct {
	Name     string   `yaml:"name" json:"name"`
	Path     string   `yaml:"path" json:"path"`
	Patterns []string `yaml:"patterns" json:"patterns"`
	Encoding string   `yaml:"encoding" json:"encoding"` // utf-8 (默认) / gbk

	// ListMode 列目录命令模式：
	//   - "" / "auto"  : 默认 gnu_find（find -printf），老 AIX 不可用
	//   - "gnu_find"   : 用 find ... -printf（Linux / macOS）
	//   - "posix_ls"   : 用 ls -lt，AIX / 老 Unix / WebSphere 安全模式
	// 选 posix_ls 时 ParseListOutput 改用 ls 输出解析（牺牲精度换兼容）。
	ListMode string `yaml:"list_mode,omitempty" json:"list_mode,omitempty"`
}

// ListModeFor 把字符串 list_mode 归一化到内部枚举值。
// 未配置或显式 "auto" 都默认 "gnu_find"（向后兼容）。
// "posix" / "ls" 也归到 "posix_ls" 方便用户写短。
func (ld *LogDirEntry) ListModeFor() string {
	switch strings.ToLower(strings.TrimSpace(ld.ListMode)) {
	case "", "auto":
		return "gnu_find"
	case "gnu_find", "gnu", "find":
		return "gnu_find"
	case "posix_ls", "posix", "ls":
		return "posix_ls"
	default:
		// 未知值保持原样，让 ListCommand 报错而不是默默降级
		return ld.ListMode
	}
}

// ListModeIsAuto 返回 list_mode 是否是"auto"（用户没明确指定模式）。
//
// "" 和 "auto" 都返回 true（兼容 YAML 里没写的旧配置）。
// handler 用这个字段判断要不要"先试 gnu_find，失败再降级到 posix_ls"；
// 显式 "gnu_find" / "posix_ls" 直接按用户指定跑，不做 fallback。
//
// 项 6 修复：v2 只在 ListCommand 加了 auto case 但 handler 没真的做 fallback，
// 升级后老 AIX 用户第一次列目录还是 502。这里给 handler 一个明确信号。
func (ld *LogDirEntry) ListModeIsAuto() bool {
	switch strings.ToLower(strings.TrimSpace(ld.ListMode)) {
	case "", "auto":
		return true
	}
	return false
}

// ExternalOpener 一个用户配置的"外部打开器"——可执行文件 + 显示名 + emoji 图标。
//
// 设计动机：
//   - 用户下载 .jsp / .java / .txt 等不同后缀，想用不同软件打开（Notepad++ / IDEA / VS Code）；
//   - 不绑后缀映射——用户自己挑软件、自己点按钮，避免"系统默认打开"猜错；
//   - 图标走 emoji 而不是解析 .exe/.app 二进制图标，避免依赖平台特定库。
type ExternalOpener struct {
	Name string `yaml:"name" json:"name"` // 唯一标识（前端按钮 title 也用这个），非空
	Path string `yaml:"path" json:"path"` // 可执行文件绝对路径；留空则不显示（前端会过滤）
	Icon string `yaml:"icon" json:"icon"` // emoji（可空，空字符串按钮显示 ⚙）
}

// FindOpener 按名字找 opener。找不到返回 nil。
// 用于 /api/local/open-with 校验请求里的 opener 名是否在白名单里——
// 防止恶意请求随便指定一个可执行文件路径来执行。
func (a *AppConfig) FindOpener(name string) *ExternalOpener {
	for i := range a.ExternalOpeners {
		if a.ExternalOpeners[i].Name == name {
			return &a.ExternalOpeners[i]
		}
	}
	return nil
}

// DownloadRetentionDaysEffective 返回生效的下载保留天数。
// 未配置返回默认 7 天；配置为 0 表示不按时间清理。
func (a *AppConfig) DownloadRetentionDaysEffective() int {
	if a.DownloadRetentionDays == nil {
		return 7
	}
	return *a.DownloadRetentionDays
}

// DownloadMaxCountEffective 返回生效的最大下载记录数。
// 未配置返回默认 1000；配置为 0 表示不限制数量。
func (a *AppConfig) DownloadMaxCountEffective() int {
	if a.DownloadMaxCount == nil {
		return 1000
	}
	return *a.DownloadMaxCount
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
//
// 设计原则：
//  1. 只补"用户没填"的字段；已填的合法值原样保留；
//  2. 已知 alias 做大小写归一（gbk/gb18030 → gbk；UTF-8/utf-8 → utf-8）；
//  3. **不再吞未知 encoding**：YAML 里写了 shift-jis 之类的非法值时，
//     这里不动它，让 Validate() 明确报错"encoding 非法"。
//     旧版 Defaults 会把 shift-jis 静默改 utf-8，
//     掩盖用户配错的事实（看着像 utf-8 在跑，其实是想用 shift-jis）。
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
	// SSH 调试相关字段的默认值：全关，20MB / 3 份。
	if c.App.SSHLogMaxMB <= 0 {
		c.App.SSHLogMaxMB = 20
	}
	if c.App.SSHLogKeep <= 0 {
		c.App.SSHLogKeep = 3
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
				// 只归一已知 alias，未知值原样保留给 Validate 报错。
				switch strings.ToLower(strings.TrimSpace(ld.Encoding)) {
				case "gbk", "gb18030":
					ld.Encoding = "gbk"
				case "", "utf-8", "utf8":
					ld.Encoding = "utf-8"
				default:
					// 未知 encoding：不改原样，交给 Validate 报错。
					// 旧版会被静默改 utf-8，掩盖用户配错。
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
	// v0.8：external_openers 校验
	//   - name 必填且非空白（前端靠 name 找按钮）；
	//   - path 必填且非空白（防止有人存了个空记录却还能"打开"）；
	//   - name 唯一（重复会让 FindOpener 拿错一个，行为不可预测）；
	// 不在这里校验 path 是否真存在 / 是否可执行——那要看用户本机情况，
	// 启动时 hard-fail 太重；运行时 open-with 失败由 handler 返回 500 即可。
	if len(c.App.ExternalOpeners) > 0 {
		seen := make(map[string]struct{}, len(c.App.ExternalOpeners))
		for i, op := range c.App.ExternalOpeners {
			if strings.TrimSpace(op.Name) == "" {
				return fmt.Errorf("app.external_openers[%d].name 不能为空", i)
			}
			if strings.TrimSpace(op.Path) == "" {
				return fmt.Errorf("app.external_openers[%d].path 不能为空（name=%q）", i, op.Name)
			}
			if _, dup := seen[op.Name]; dup {
				return fmt.Errorf("app.external_openers[%d].name %q 重复", i, op.Name)
			}
			seen[op.Name] = struct{}{}
		}
	}
	if c.App.SSHLogMaxMB < 0 || c.App.SSHLogMaxMB > 1024 {
		return fmt.Errorf("app.ssh_log_max_mb 必须在 0..1024 之间，当前: %d", c.App.SSHLogMaxMB)
	}
	if c.App.SSHLogKeep < 0 || c.App.SSHLogKeep > 100 {
		return fmt.Errorf("app.ssh_log_keep 必须在 0..100 之间，当前: %d", c.App.SSHLogKeep)
	}
	if c.App.DownloadRetentionDays != nil && *c.App.DownloadRetentionDays < 0 {
		return fmt.Errorf("app.download_retention_days 不能为负数，当前: %d", *c.App.DownloadRetentionDays)
	}
	if c.App.DownloadMaxCount != nil && *c.App.DownloadMaxCount < 0 {
		return fmt.Errorf("app.download_max_count 不能为负数，当前: %d", *c.App.DownloadMaxCount)
	}
	if c.App.SSHCompatProfile != "" {
		switch strings.ToLower(strings.TrimSpace(c.App.SSHCompatProfile)) {
		case "modern", "compat", "no-ecdh", "legacy", "auto":
		default:
			return fmt.Errorf("app.ssh_compat_profile 取值非法: %q（仅支持 modern / compat / no-ecdh / legacy / auto）", c.App.SSHCompatProfile)
		}
	}
	if c.App.CredentialStore != "" {
		switch strings.ToLower(strings.TrimSpace(c.App.CredentialStore)) {
		case "keyring", "file", "disabled", "off", "none":
		default:
			return fmt.Errorf("app.credential_store 取值非法: %q（仅支持 keyring / file / disabled）", c.App.CredentialStore)
		}
	}
	if c.App.CredentialKey != "" {
		key := strings.TrimSpace(c.App.CredentialKey)
		if len(key) != 64 {
			return fmt.Errorf("app.credential_key 必须是 64 个十六进制字符（32 字节 AES-256 密钥），当前长度: %d", len(key))
		}
		for _, ch := range key {
			if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
				return fmt.Errorf("app.credential_key 含有非法十六进制字符: %q", string(ch))
			}
		}
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
			if srv.SSHProfile != "" {
				switch strings.ToLower(strings.TrimSpace(srv.SSHProfile)) {
				case "modern", "compat", "no-ecdh", "legacy", "auto":
				default:
					return fmt.Errorf("系统 %q 服务器 %q ssh_profile 取值非法: %q（仅支持 modern / compat / no-ecdh / legacy / auto）",
						sys.Name, srv.Name, srv.SSHProfile)
				}
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

// Clone 返回一份"配置树"的深拷贝，避免 Replace 时新老 Config 共享 inner slice。
//
// 为什么需要：
//   - Config 是 struct value，但内部 []SystemConfig / []ServerConfig / []LogDirEntry
//     是 slice header，简单 *cur 拷贝只会复用底层 array；
//   - handlers_admin.go 的 PUT 路径如果用浅拷贝 + newCfg.Systems = req.Systems，
//     老 cfg.Systems 不会被覆盖（OK），但如果后面 manager 内部或者别处直接改
//     newCfg.Systems[i].Servers[0].Host，就会污染其它 reader 看到的快照；
//   - 用 Clone 后，所有 inner slice 都重新分配，调用方随便改都不影响老 cfg。
//
// 注意：指针字段（如 EnableFreeFileBrowser *bool）也会被新建一份。
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	out := *c // 顶层字段值拷贝（Search / AppConfig 大部分字段）

	// AppConfig.EnableFreeFileBrowser 是 *bool，需要独立复制
	if c.App.EnableFreeFileBrowser != nil {
		b := *c.App.EnableFreeFileBrowser
		out.App.EnableFreeFileBrowser = &b
	}
	// AppConfig.FreeFileRoots 是 slice header 复用底层数组，必须新建。
	if c.App.FreeFileRoots != nil {
		out.App.FreeFileRoots = append([]string(nil), c.App.FreeFileRoots...)
	}
	// v0.8：AppConfig.ExternalOpeners 同理必须新建 slice（且每项是值拷贝 struct，无指针字段）。
	if c.App.ExternalOpeners != nil {
		out.App.ExternalOpeners = append([]ExternalOpener(nil), c.App.ExternalOpeners...)
	}
	// 下载保留策略指针字段独立复制
	if c.App.DownloadRetentionDays != nil {
		d := *c.App.DownloadRetentionDays
		out.App.DownloadRetentionDays = &d
	}
	if c.App.DownloadMaxCount != nil {
		n := *c.App.DownloadMaxCount
		out.App.DownloadMaxCount = &n
	}

	// Systems 整树深拷贝：SystemConfig / ServerConfig / LogDirEntry 都按值拷贝，
	// 它们内部的 slice（Servers / LogDirs / Patterns）也要新建。
	if c.Systems != nil {
		out.Systems = make([]SystemConfig, len(c.Systems))
		for i := range c.Systems {
			srcSys := &c.Systems[i]
			dstSys := &out.Systems[i]
			dstSys.Name = srcSys.Name
			dstSys.Description = srcSys.Description
			if srcSys.Servers != nil {
				dstSys.Servers = make([]ServerConfig, len(srcSys.Servers))
				for j := range srcSys.Servers {
					srcSrv := &srcSys.Servers[j]
					dstSrv := &dstSys.Servers[j]
					*dstSrv = *srcSrv // ServerConfig 不含指针字段，值拷贝 OK
					if srcSrv.LogDirs != nil {
						dstSrv.LogDirs = make([]LogDirEntry, len(srcSrv.LogDirs))
						for k := range srcSrv.LogDirs {
							srcLd := &srcSrv.LogDirs[k]
							dstLd := &dstSrv.LogDirs[k]
							*dstLd = *srcLd
							if srcLd.Patterns != nil {
								dstLd.Patterns = append([]string(nil), srcLd.Patterns...)
							}
						}
					}
				}
			}
		}
	}

	return &out
}
