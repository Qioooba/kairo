// Package diagnostics 收集"环境自检"信息（P3-1）。
//
// 目标：在不暴露密码的前提下，回答运维三问：
//
//  1. 我这台 ops-toolbox 跑得正常吗？（Go 版本、x/crypto/ssh 版本、监听地址、磁盘可写）
//  2. 我能 SSH 上去吗？（DNS 解析 + TCP 端口连通性，按 server 逐台报）
//  3. 我有必要的工具吗？（find -printf、grep、sed、tail 等 find_list / grep 依赖）
//
// 全部信息一次性 JSON 出来，前端做展示；不修改 sshclient.go，
// 这里只复用其底层 *ssh.ClientConfig 之外的 Conn 行为（直接 net.Dial）。
package diagnostics

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"doubao-toolbox/internal/config"
)

// Report 单次自检结果。结构稳定，前端可作为表格字段映射。
//
// 字段按"大类"分块：
//   - App* / Build*：本进程 / 编译环境
//   - Runtime*：磁盘 / 监听地址 / 凭据模式
//   - Tools*：本机命令工具
//   - Servers[]：每台配置里的 server 单独一项
//   - Issues[]：汇总的"需要关注的告警"（前端可置顶展示）
type Report struct {
	GeneratedAt string        `json:"generated_at"` // RFC3339
	App         AppInfo       `json:"app"`
	Build       BuildInfo     `json:"build"`
	Runtime     RuntimeInfo   `json:"runtime"`
	Tools       ToolsInfo     `json:"tools"`
	Servers     []ServerCheck `json:"servers"`
	Issues      []string      `json:"issues"` // 文字列表，适合前端置顶红条
}

// AppInfo 应用自身信息（来自 config）
type AppInfo struct {
	Name             string   `json:"name"`
	ListenAddr       string   `json:"listen_addr"`
	Host             string   `json:"host"`
	Port             int      `json:"port"`
	CredentialStore  string   `json:"credential_store"`
	FreeFileBrowser  bool     `json:"free_file_browser"`
	FreeFileRoots    []string `json:"free_file_roots,omitempty"`
	SSHCompatProfile string   `json:"ssh_compat_profile"`
	SSHDebug         bool     `json:"ssh_debug"`
	SSHTrafficDump   bool     `json:"ssh_traffic_dump"`
	SSHLogMaxMB      int      `json:"ssh_log_max_mb"`
	SSHLogKeep       int      `json:"ssh_log_keep"`
	DownloadDir      string   `json:"download_dir"`
	LogDir           string   `json:"log_dir"`
	DataDir          string   `json:"data_dir"`
	ConfigPath       string   `json:"config_path"`
}

// BuildInfo 编译环境
type BuildInfo struct {
	GoVersion        string `json:"go_version"`
	GOOS             string `json:"goos"`
	GOARCH           string `json:"goarch"`
	CryptoSSHVersion string `json:"crypto_ssh_version"`
}

// RuntimeInfo 运行时环境
type RuntimeInfo struct {
	DataDirWritable     bool   `json:"data_dir_writable"`
	DownloadDirWritable bool   `json:"download_dir_writable"`
	LogDirWritable      bool   `json:"log_dir_writable"`
	WorkingDir          string `json:"working_dir"`
	PID                 int    `json:"pid"`
	NumGoroutine        int    `json:"num_goroutine"`
}

// ToolsInfo 本机命令工具可用性
type ToolsInfo struct {
	Find          ToolCheck `json:"find"`
	FindPrintF    bool      `json:"find_supports_printf"` // GNU find 的 -printf，老 AIX 可能没有
	Grep          ToolCheck `json:"grep"`
	Sed           ToolCheck `json:"sed"`
	Tail          ToolCheck `json:"tail"`
	Unzip         ToolCheck `json:"unzip"`
	SSH           ToolCheck `json:"ssh"`
	SCPKnownHosts string    `json:"scp_known_hosts,omitempty"` // 暂留空，避免误以为真的有 ~/.ssh/known_hosts
}

// ToolCheck 命令工具检查结果
type ToolCheck struct {
	Path    string `json:"path,omitempty"`    // 可执行文件绝对路径
	Found   bool   `json:"found"`             // 是否在 PATH 里找到
	Version string `json:"version,omitempty"` // --version 第一行（trim 后），可能为空
}

// ServerCheck 单台 server 的连通性检查
type ServerCheck struct {
	System    string      `json:"system"`
	Server    string      `json:"server"`
	Host      string      `json:"host"`
	Port      int         `json:"port"`
	DNS       CheckResult `json:"dns"` // 域名解析
	TCP       CheckResult `json:"tcp"` // TCP 连通性（不发起 SSH 握手）
	ElapsedMS int64       `json:"elapsed_ms"`
	Error     string      `json:"error,omitempty"` // 顶层错误（任一项失败都填）
	Category  string      `json:"category,omitempty"`
}

// CheckResult 单项检查结果
type CheckResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"` // 失败时的原因
}

// Options 控制 Report 的检查行为
type Options struct {
	// CheckServers 是否对每台 server 跑 DNS + TCP 检查。
	// 默认 true。false 用于：测试场景跳过网络。
	CheckServers bool

	// PerServerTimeout 单台 server 检查总超时（DNS+TCP）。
	// 默认 3s。
	PerServerTimeout time.Duration
}

// defaultOptions 默认配置
func defaultOptions(o Options) Options {
	if o.PerServerTimeout <= 0 {
		o.PerServerTimeout = 3 * time.Second
	}
	return o
}

// Collect 跑一次自检，返回完整 Report。
//
// 设计要点：
//   - 不依赖 sshclient.Dial（避免 ssh 握手阶段超时干扰自检）；
//     直接 net.Dial / net.LookupHost，更可控；
//   - 全程 ctx 控制总耗时（默认 5s 内全部完成）；
//   - 即使部分 server 失败也不中断其它 server（独立超时）。
func Collect(cfg *config.Config, configPath string, opts Options) Report {
	opts = defaultOptions(opts)

	rep := Report{
		GeneratedAt: time.Now().Format(time.RFC3339),
	}
	rep.App = buildAppInfo(cfg, configPath)
	rep.Build = buildBuildInfo()
	rep.Runtime = buildRuntimeInfo(cfg)
	rep.Tools = buildToolsInfo()
	rep.Servers = []ServerCheck{}

	if opts.CheckServers {
		rep.Servers = checkServers(cfg, opts.PerServerTimeout)
	}
	rep.Issues = summarizeIssues(rep)
	return rep
}

func buildAppInfo(cfg *config.Config, configPath string) AppInfo {
	if cfg == nil {
		return AppInfo{ConfigPath: configPath}
	}
	return AppInfo{
		Name:             cfg.App.Name,
		ListenAddr:       cfg.App.ListenAddr(),
		Host:             cfg.App.Host,
		Port:             cfg.App.Port,
		CredentialStore:  cfg.App.CredentialStoreEnabled(),
		FreeFileBrowser:  cfg.App.FreeFileBrowserEnabled(),
		FreeFileRoots:    cfg.App.FreeFileRoots,
		SSHCompatProfile: cfg.App.SSHCompatProfile,
		SSHDebug:         cfg.App.SSHDebug,
		SSHTrafficDump:   cfg.App.SSHTrafficDump,
		SSHLogMaxMB:      cfg.App.SSHLogMaxMB,
		SSHLogKeep:       cfg.App.SSHLogKeep,
		DownloadDir:      cfg.DownloadDir(),
		LogDir:           cfg.LogDir(),
		DataDir:          cfg.DataDir(),
		ConfigPath:       configPath,
	}
}

func buildBuildInfo() BuildInfo {
	info := BuildInfo{
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}
	// 跟 sshclient 一样用 debug.ReadBuildInfo 拿 x/crypto 版本
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range build.Deps {
			if dep.Path == "golang.org/x/crypto" {
				if dep.Replace != nil {
					info.CryptoSSHVersion = dep.Replace.Path + " " + dep.Replace.Version + " (replace for " + dep.Version + ")"
				} else {
					info.CryptoSSHVersion = dep.Version
				}
				break
			}
		}
	}
	if info.CryptoSSHVersion == "" {
		info.CryptoSSHVersion = "unknown"
	}
	return info
}

func buildRuntimeInfo(cfg *config.Config) RuntimeInfo {
	wd, _ := os.Getwd()
	rep := RuntimeInfo{
		WorkingDir:   wd,
		PID:          os.Getpid(),
		NumGoroutine: runtime.NumGoroutine(),
	}
	if cfg != nil {
		rep.DataDirWritable = dirWritable(cfg.DataDir())
		rep.DownloadDirWritable = dirWritable(cfg.DownloadDir())
		rep.LogDirWritable = dirWritable(cfg.LogDir())
	}
	return rep
}

// dirWritable 检查 path 是否可写（创建+删除临时文件）。
func dirWritable(path string) bool {
	if path == "" {
		return false
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return false
	}
	tmp := filepath.Join(path, ".doubao-toolbox-write-check")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		return false
	}
	_ = os.Remove(tmp)
	return true
}

func buildToolsInfo() ToolsInfo {
	return ToolsInfo{
		Find:       checkTool("find"),
		FindPrintF: findSupportsPrintf(),
		Grep:       checkTool("grep"),
		Sed:        checkTool("sed"),
		Tail:       checkTool("tail"),
		Unzip:      checkTool("unzip"),
		SSH:        checkTool("ssh"),
	}
}

// checkTool 用 exec.LookPath + --version 探测命令存在 + 版本。
//
// 不依赖 sshclient.go，直接复用 stdlib；exec.LookPath 不存在时返 Found=false。
func checkTool(name string) ToolCheck {
	tc := ToolCheck{Found: false}
	path, err := exec.LookPath(name)
	if err != nil {
		return tc
	}
	tc.Found = true
	tc.Path = path
	// 拿版本（带 2s 兜底超时，避免 hang）
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, "--version").Output()
	if err == nil {
		first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		// 有些工具的 --version 输出多行；第一行通常是版本号
		tc.Version = truncate(first, 200)
	}
	return tc
}

// findSupportsPrintf 检测 find 是否支持 -printf（GNU find 有，AIX 老 find 没有）。
//
// 实现：跑 `find --help`（兼容 GNU + BSD），看输出里有没有 -printf。
func findSupportsPrintf() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "find", "--help").CombinedOutput()
	if err != nil {
		// 某些 BSD find 接受 -h 而不是 --help；试一下
		out2, err2 := exec.CommandContext(ctx, "find", "-h").CombinedOutput()
		if err2 != nil {
			return false
		}
		out = out2
	}
	return strings.Contains(strings.ToLower(string(out)), "-printf")
}

func checkServers(cfg *config.Config, timeout time.Duration) []ServerCheck {
	if cfg == nil {
		return nil
	}
	out := []ServerCheck{}
	for si := range cfg.Systems {
		sys := &cfg.Systems[si]
		for ssi := range sys.Servers {
			srv := &sys.Servers[ssi]
			out = append(out, checkOneServer(sys.Name, srv, timeout))
		}
	}
	return out
}

func checkOneServer(sysName string, srv *config.ServerConfig, timeout time.Duration) ServerCheck {
	start := time.Now()
	port := srv.Port
	if port == 0 {
		port = 22
	}
	host := strings.TrimSpace(srv.Host)
	sc := ServerCheck{
		System: sysName,
		Server: srv.Name,
		Host:   host,
		Port:   port,
	}

	if host == "" {
		sc.Error = "host 为空"
		sc.Category = "config"
		sc.ElapsedMS = time.Since(start).Milliseconds()
		return sc
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 1. DNS
	if hostIsIP(host) {
		sc.DNS.OK = true
		sc.DNS.Message = "是 IP，无需 DNS"
	} else {
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			sc.DNS.Message = "DNS 解析失败：" + err.Error()
			sc.Error = sc.DNS.Message
			sc.Category = "dns"
			sc.ElapsedMS = time.Since(start).Milliseconds()
			return sc
		}
		sc.DNS.OK = true
		sc.DNS.Message = "→ " + strings.Join(addrs, ", ")
	}

	// 2. TCP（不发起 SSH 握手，只验证端口可达）
	d := net.Dialer{Timeout: timeout}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		sc.TCP.Message = "TCP 失败：" + err.Error()
		sc.Error = sc.TCP.Message
		sc.Category = "tcp"
		sc.ElapsedMS = time.Since(start).Milliseconds()
		return sc
	}
	_ = conn.Close()
	sc.TCP.OK = true
	sc.TCP.Message = "端口可达"
	sc.ElapsedMS = time.Since(start).Milliseconds()
	return sc
}

// hostIsIP 简单判断 host 是字面 IP（v4/v6）还是域名。
func hostIsIP(h string) bool {
	return net.ParseIP(h) != nil
}

// truncate 安全截断字符串（按 rune 计）。
func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// summarizeIssues 把 Report 转成给人看的"告警文字列表"。
//
// 规则（按重要性排序）：
//  1. 数据目录不可写
//  2. find -printf 不可用（影响 list_mode=auto 的 gnu_find 路径）
//  3. tail / grep 缺失（影响 tail / search）
//  4. 任意 server 的 DNS / TCP 失败
//  5. credential_store=file（占位模式，未实现）
func summarizeIssues(rep Report) []string {
	var issues []string
	if !rep.Runtime.DataDirWritable {
		issues = append(issues, "⚠ 数据目录不可写："+rep.App.DataDir)
	}
	if !rep.Runtime.DownloadDirWritable {
		issues = append(issues, "⚠ 下载目录不可写："+rep.App.DownloadDir)
	}
	if !rep.Runtime.LogDirWritable {
		issues = append(issues, "⚠ 日志目录不可写："+rep.App.LogDir)
	}
	if !rep.Tools.Find.Found {
		issues = append(issues, "⚠ 本机缺 find 命令")
	} else if !rep.Tools.FindPrintF {
		issues = append(issues, "⚠ find 不支持 -printf（AIX / 老 BSD），把 log_dirs[*].list_mode 显式设为 posix_ls")
	}
	if !rep.Tools.Tail.Found {
		issues = append(issues, "⚠ 本机缺 tail（实时跟踪不可用）")
	}
	if !rep.Tools.Grep.Found {
		issues = append(issues, "⚠ 本机缺 grep（搜索不可用）")
	}
	if rep.App.CredentialStore == "file" {
		issues = append(issues, "ℹ credential_store=file 暂未实现（v0.4 占位），凭据不会被持久化")
	}
	for _, s := range rep.Servers {
		if !s.DNS.OK {
			issues = append(issues, "✗ DNS 失败："+s.System+"/"+s.Server+" ("+s.Host+") — "+s.DNS.Message)
		} else if !s.TCP.OK {
			issues = append(issues, "✗ TCP 失败："+s.System+"/"+s.Server+" ("+hostPort(s)+") — "+s.TCP.Message)
		}
	}
	return issues
}

func hostPort(s ServerCheck) string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// CheckSSHHandshakeLimited 在已确认 TCP 可达的前提下，跑一次 SSH 协议版本探测。
//
// 不暴露任何凭据：连接建立后立即 read banner（"SSH-2.0-xxx"），然后关闭。
// 用于更精细的诊断（区分"TCP 通但 SSH 协议被防火墙挡"）。
//
// timeout <= 0 用 2s 兜底。
func CheckSSHHandshakeLimited(host string, port int, timeout time.Duration) CheckResult {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if host == "" {
		return CheckResult{OK: false, Message: "host 为空"}
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	d := net.Dialer{Timeout: timeout}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return CheckResult{OK: false, Message: "TCP: " + err.Error()}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	// 读 1 字节就够了 — banner 头是 "SSH-"
	buf := make([]byte, 4)
	n, err := conn.Read(buf)
	if err != nil {
		return CheckResult{OK: false, Message: "读 banner 失败: " + err.Error()}
	}
	if n < 4 || string(buf[:4]) != "SSH-" {
		return CheckResult{OK: false, Message: "非 SSH banner（被防火墙/代理拦截？）"}
	}
	return CheckResult{OK: true, Message: "SSH banner OK"}
}

// AsSSHClientConfig 把 server config 转成 *ssh.ClientConfig（仅握手用，不发起认证）。
//
// 用 ssh.InsecureIgnoreHostKey；不会真的拨号，所以密码字段无意义。
// 仅供"想拿一份握手用的 ClientConfig"的场景（当前没人用，先放着备查）。
func AsSSHClientConfig(srv *config.ServerConfig) *ssh.ClientConfig {
	if srv == nil {
		return nil
	}
	return &ssh.ClientConfig{
		User:            srv.Username,
		Auth:            []ssh.AuthMethod{ssh.Password("")},
		Timeout:         2 * time.Second,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
}
