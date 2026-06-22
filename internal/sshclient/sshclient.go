// Package sshclient 提供受控的 SSH 连接与命令执行能力。
//
// 核心安全设计：
//  1. 不允许用户输入任意命令；
//  2. 所有命令由后端固定模板生成；
//  3. 远程目录/文件名只能来自配置白名单或前一步 ls 的结果；
//  4. 搜索关键词做严格转义，禁止 shell 元字符；
//  5. 每次执行带超时，防止长时间挂起；
//  6. 超时由客户端 ctx + 内部 timer 控制，不依赖服务器端 `timeout` 命令；
//  7. 支持 UTF-8 / GBK 编码（老 WebSphere / Oracle 常见 GBK）。
package sshclient

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// Server 目标服务器配置（来自 config.yaml）
type Server struct {
	Name     string
	Host     string
	Port     int
	Username string
	// HostKeySHA256 可选：固定这台 server 的 host key 指纹（base64 SHA256）。
	// 非空时 Dial 会用 ssh.FixedHostKey 强校验，避免中间人攻击。
	// 空时仍走 InsecureIgnoreHostKey（内网工具默认）。
	HostKeySHA256 string
	// SSHProfile 可选：覆盖 app 级别的默认 profile。
	// 取值同 AppConfig.SSHCompatProfile：modern/compat/no-ecdh/legacy/auto。
	// 空时由 SetDefaultProfile 指定的全局默认决定；都没设就 compat。
	SSHProfile string
}

// ---- 日志与 profile 全局配置（项 9 + 项 22）----

var (
	pkgLogMu        sync.RWMutex
	pkgDebugEnabled bool   // 是否启用 ssh_debug.log 写入
	pkgTrafficOn    bool   // 是否启用 ssh_traffic.log 写入
	pkgLogMaxBytes  int64  // 单个日志文件上限（0 = 不限）
	pkgLogKeep      int    // 轮转保留份数（0 = 不轮转，只截断）
	pkgDefaultProf  string // 全局默认 SSH compat profile 名；空 = compat
)

// SetLogConfig 配置 SSH 日志开关（项 9）。
//
//   - debug=true 开启 ssh_debug.log
//   - traffic=true 开启 ssh_traffic.log（hex dump，会写大量数据，调试老 sshd 时再开）
//   - maxMB：单个日志文件大小上限（MB），0 = 不限
//   - keep：超过上限时保留的轮转份数（不含当前），0 = 不轮转
//
// 默认全关（debug=false, traffic=false, maxMB=0, keep=0）——
// 这是为了不在用户机器上无脑生成日志。
func SetLogConfig(debug, traffic bool, maxMB, keep int) {
	pkgLogMu.Lock()
	defer pkgLogMu.Unlock()
	pkgDebugEnabled = debug
	pkgTrafficOn = traffic
	if maxMB > 0 {
		pkgLogMaxBytes = int64(maxMB) * 1024 * 1024
	} else {
		pkgLogMaxBytes = 0
	}
	pkgLogKeep = keep
}

// SetDefaultProfile 设置全局默认 SSH compat profile（项 22）。
//
// 取值：modern / compat / no-ecdh / legacy / auto。
// "auto" 表示按 sshCompatProfiles() 的 3 段默认顺序（向后兼容旧行为）。
// 任何 srv.SSHProfile 非空的连接仍走 srv 上的 profile，不被全局默认值覆盖。
func SetDefaultProfile(name string) {
	pkgLogMu.Lock()
	defer pkgLogMu.Unlock()
	pkgDefaultProf = strings.ToLower(strings.TrimSpace(name))
}

// DefaultProfileName 返回当前全局默认 SSH compat profile 名（main 启动日志用）。
// 空 = 没显式设过，行为等同 "auto"。
func DefaultProfileName() string {
	pkgLogMu.RLock()
	defer pkgLogMu.RUnlock()
	if pkgDefaultProf == "" {
		return "auto"
	}
	return pkgDefaultProf
}

// resolveProfile 根据 srv.SSHProfile 选具体 profile。
//
// 优先级：
//  1. srv.SSHProfile 非空 → 直接选这个
//  2. 否则用全局 pkgDefaultProf
//  3. 都没设或值非法 → "auto"（走 sshCompatProfiles() 3 段顺序）
func resolveProfile(srv Server, defaultName string) (name string, profs []sshCompatProfile) {
	all := sshCompatProfiles()
	wanted := strings.ToLower(strings.TrimSpace(srv.SSHProfile))
	if wanted == "" {
		wanted = defaultName
	}
	switch wanted {
	case "", "auto":
		return "auto", all
	case "modern":
		// 现代子集：仅 curve25519 + 现代 host key（避免老 sshd 不识别算法导致握手失败）
		return "modern", all[:1]
	case "compat":
		return "compat", all[:1]
	case "no-ecdh":
		// 取 no-ecdh 那段（第 2 个）
		if len(all) >= 2 {
			return "no-ecdh", all[1:2]
		}
		return "no-ecdh", all
	case "legacy":
		// legacy DH-sha1 兜底（第 3 个）
		if len(all) >= 3 {
			return "legacy", all[2:3]
		}
		return "legacy", all
	default:
		return wanted, all
	}
}

// sshDebugLog 把 ops-toolbox 自己的 SSH 调用细节写到独立文件
// （<exe 目录>/logs/ssh_debug.log），便于排查老 sshd 兼容性问题。
// 注意：Go x/crypto/ssh 内部 KEXINIT 协商没有暴露 Logf API，
// 这里只能记 ops-toolbox 自己的配置/调用/错误/耗时，
// 真正的 SSH 协议包需要 ssh -vvv 或 Wireshark 抓。
var (
	sshDebugOnce sync.Once
	sshDebugFile *os.File
)

func openSSHDebugLog() *os.File {
	sshDebugOnce.Do(func() {
		// 默认关闭（项 9）：避免无脑写日志污染用户机器。
		pkgLogMu.RLock()
		enabled := pkgDebugEnabled
		pkgLogMu.RUnlock()
		if !enabled {
			return
		}
		exe, err := os.Executable()
		if err != nil {
			return
		}
		real, err := filepath.EvalSymlinks(exe)
		if err == nil && real != "" {
			exe = real
		}
		logDir := filepath.Join(filepath.Dir(exe), "logs")
		// logs/ 目录可能不存在（开发态跑 go run），尽力创建
		_ = os.MkdirAll(logDir, 0o755)
		f, err := os.OpenFile(filepath.Join(logDir, "ssh_debug.log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		sshDebugFile = f
	})
	return sshDebugFile
}

func sshDebugLogf(format string, args ...interface{}) {
	f := openSSHDebugLog()
	if f == nil {
		return
	}
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	fmt.Fprintf(f, "[%s] %s\n", ts, fmt.Sprintf(format, args...))
}

// cryptoSSHVersion 运行时读取嵌入到二进制里的 Go module info，
// 返回实际编译进来的 golang.org/x/crypto 版本字符串。
// 用 runtime/debug.ReadBuildInfo 而不是 ldflags -X 注入，
// 是因为 ReadBuildInfo 自动从 Go 编译时嵌入的 module graph 读取，
// 不依赖编译参数，也不会因为忘加 ldflags 而拿到空字符串。
// 也支持 Replace directive（指向 fork 版本时会同时打印原版和替换版）。
func cryptoSSHVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep.Path == "golang.org/x/crypto" {
			if dep.Replace != nil {
				return dep.Replace.Path + " " + dep.Replace.Version + " (replace for " + dep.Version + ")"
			}
			return dep.Version
		}
	}
	return "not found in build info"
}

// sshTrafficLog 把 ops-toolbox 跟远端 sshd 之间的 TCP 字节流镜像到
// <exe 目录>/logs/ssh_traffic.log（hex dump 格式）。
// 既然不能装 ssh 客户端跑 ssh -vvv 抓真实 KEXINIT，
// 就让 ops-toolbox 自己抓，这样能直接看到 client 发了什么 KEXINIT、
// server 回了什么 KEXINIT、协商到哪一步 close 的。
var (
	sshTrafficOnce     sync.Once
	sshTrafficFile     *os.File
	sshTrafficDisabled bool
)

func openSSHTrafficLog() *os.File {
	sshTrafficOnce.Do(func() {
		// 默认关闭（项 9）：traffic 日志会写大量 hex dump，没开 SSH 兼容问题时别开。
		pkgLogMu.RLock()
		enabled := pkgTrafficOn
		pkgLogMu.RUnlock()
		if !enabled {
			sshTrafficDisabled = true
			return
		}
		exe, err := os.Executable()
		if err != nil {
			sshTrafficDisabled = true
			return
		}
		real, err := filepath.EvalSymlinks(exe)
		if err == nil && real != "" {
			exe = real
		}
		logDir := filepath.Join(filepath.Dir(exe), "logs")
		_ = os.MkdirAll(logDir, 0o755)
		f, err := os.OpenFile(filepath.Join(logDir, "ssh_traffic.log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			sshTrafficDisabled = true
			return
		}
		sshTrafficFile = f
	})
	return sshTrafficFile
}

// teedConn 包装一个 net.Conn，把所有 Read/Write 字节镜像到 hex 文件。
// 用于在不装 Wireshark/ssh 客户端的情况下抓 SSH 协议包。
//
// readDir 和 writeDir 分别记录"从网络读到的字节方向"和"写到网络去的字节方向"，
// 避免用同一个 dir 字段误导日志阅读者：
//   - Read() 读的是 server→client 字节，所以用 readDir = "S->C"
//   - Write() 写的是 client→server 字节，所以用 writeDir = "C->S"
type teedConn struct {
	conn     net.Conn
	file     *os.File
	mu       sync.Mutex
	readDir  string // 通常为 "S->C"
	writeDir string // 通常为 "C->S"
}

func (t *teedConn) Read(p []byte) (int, error) {
	n, err := t.conn.Read(p)
	if n > 0 && t.file != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
		fmt.Fprintf(t.file, "=== %s Read %d bytes @ %s ===\n",
			t.readDir, n, time.Now().Format("15:04:05.000"))
		t.file.Write([]byte(hex.Dump(p[:n])))
	}
	return n, err
}

func (t *teedConn) Write(p []byte) (int, error) {
	if t.file != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
		fmt.Fprintf(t.file, "=== %s Write %d bytes @ %s ===\n",
			t.writeDir, len(p), time.Now().Format("15:04:05.000"))
		t.file.Write([]byte(hex.Dump(p)))
	}
	return t.conn.Write(p)
}

func (t *teedConn) Close() error                       { return t.conn.Close() }
func (t *teedConn) LocalAddr() net.Addr                { return t.conn.LocalAddr() }
func (t *teedConn) RemoteAddr() net.Addr               { return t.conn.RemoteAddr() }
func (t *teedConn) SetDeadline(d time.Time) error      { return t.conn.SetDeadline(d) }
func (t *teedConn) SetReadDeadline(d time.Time) error  { return t.conn.SetReadDeadline(d) }
func (t *teedConn) SetWriteDeadline(d time.Time) error { return t.conn.SetWriteDeadline(d) }

// Credentials 登录凭据（密码，每次由调用方传入）
type Credentials struct {
	Password string
}

// Client 一个可复用的 SSH 客户端封装
type Client struct {
	srv  Server
	cre  Credentials
	conn *ssh.Client
}

// Dial 连接并认证
type sshCompatProfile struct {
	Name              string
	Description       string
	KeyExchanges      []string
	HostKeyAlgorithms []string
	Ciphers           []string
	MACs              []string
}

func sshCompatProfiles() []sshCompatProfile {
	modernHostKeys := []string{
		"rsa-sha2-512",
		"rsa-sha2-256",
		"ssh-rsa",
		"ecdsa-sha2-nistp256",
		"ecdsa-sha2-nistp384",
		"ecdsa-sha2-nistp521",
		"ssh-ed25519",
	}
	legacyHostKeys := append(append([]string{}, modernHostKeys...), "ssh-dss") // 只在 legacy 兜底里启用

	wideCiphers := []string{
		"aes128-gcm@openssh.com",
		"aes256-gcm@openssh.com",
		"chacha20-poly1305@openssh.com",
		"aes128-ctr",
		"aes192-ctr",
		"aes256-ctr",
		"aes128-cbc",
		"aes192-cbc",
		"aes256-cbc",
		"3des-cbc",
	}
	wideMACs := []string{
		"hmac-sha2-256-etm@openssh.com",
		"hmac-sha2-512-etm@openssh.com",
		"hmac-sha2-256",
		"hmac-sha2-512",
		"hmac-sha1",
		"hmac-sha1-96",
		"hmac-md5",
	}

	return []sshCompatProfile{
		{
			Name:              "compat-dh-before-ecdh",
			Description:       "默认兼容模式：现代 server 仍优先 curve25519；老 6.2p2 优先 DH，绕开 ECDH P-256 路径",
			HostKeyAlgorithms: modernHostKeys,
			KeyExchanges: []string{
				"curve25519-sha256",
				"curve25519-sha256@libssh.org",
				"diffie-hellman-group14-sha256",
				"diffie-hellman-group-exchange-sha256",
				"diffie-hellman-group14-sha1",
				"diffie-hellman-group-exchange-sha1",
				"ecdh-sha2-nistp256",
				"ecdh-sha2-nistp384",
				"ecdh-sha2-nistp521",
				"diffie-hellman-group1-sha1",
			},
			Ciphers: wideCiphers,
			MACs:    wideMACs,
		},
		{
			Name:              "no-ecdh",
			Description:       "自动回退 1：完全移除 ECDH，避免老 OpenSSL / 厂商补丁 sshd 在 ECDH_INIT 后 RST",
			HostKeyAlgorithms: modernHostKeys,
			KeyExchanges: []string{
				"curve25519-sha256",
				"curve25519-sha256@libssh.org",
				"diffie-hellman-group14-sha256",
				"diffie-hellman-group-exchange-sha256",
				"diffie-hellman-group14-sha1",
				"diffie-hellman-group-exchange-sha1",
				"diffie-hellman-group1-sha1",
			},
			Ciphers: wideCiphers,
			MACs:    wideMACs,
		},
		{
			Name:              "legacy-dh-sha1-first",
			Description:       "自动回退 2：面向 OpenSSH 5.x/6.0/6.2、AIX/老 WebSphere/老堡垒机；优先 group14-sha1，再到 group1",
			HostKeyAlgorithms: legacyHostKeys,
			KeyExchanges: []string{
				"diffie-hellman-group14-sha1",
				"diffie-hellman-group-exchange-sha1",
				"diffie-hellman-group14-sha256",
				"diffie-hellman-group-exchange-sha256",
				"diffie-hellman-group1-sha1",
			},
			Ciphers: []string{
				"aes128-ctr",
				"aes192-ctr",
				"aes256-ctr",
				"aes128-cbc",
				"aes192-cbc",
				"aes256-cbc",
				"3des-cbc",
			},
			MACs: []string{
				"hmac-sha1",
				"hmac-sha1-96",
				"hmac-sha2-256",
				"hmac-sha2-512",
				"hmac-md5",
			},
		},
	}
}

func passwordKeyboardInteractive(password string) ssh.KeyboardInteractiveChallenge {
	return func(user, instruction string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i := range questions {
			// 老 sshd / PAM 有时只开放 keyboard-interactive，问题通常是 Password:。
			// 对 echo=false 的问题返回同一个密码；echo=true 的交互问题不回显密码，避免误把密码写入日志/提示。
			if i < len(echos) && !echos[i] {
				answers[i] = password
			}
		}
		return answers, nil
	}
}

func newSSHClientConfig(srv Server, cred Credentials, timeout time.Duration, p sshCompatProfile) *ssh.ClientConfig {
	hostKeyCb := ssh.InsecureIgnoreHostKey() // 内网工具默认：信任 host key
	// 项 10：如果用户在 server 上配了 host_key_sha256，启用强校验。
	// 这里用 base64 SHA256 比对，远比 known_hosts 文件管理轻量，适合内网固定机器场景。
	if hk := strings.TrimSpace(srv.HostKeySHA256); hk != "" {
		if fp, err := base64.StdEncoding.DecodeString(hk); err == nil && len(fp) == 32 {
			want := fp
			hostKeyCb = func(_ string, _ net.Addr, key ssh.PublicKey) error {
				got := key.Marshal()
				h := sha256.Sum256(got)
				if !bytes.Equal(h[:], want) {
					return fmt.Errorf("host key fingerprint 不匹配: want sha256:%s, got sha256:%s",
						base64.StdEncoding.EncodeToString(want),
						base64.StdEncoding.EncodeToString(h[:]))
				}
				return nil
			}
		} else {
			sshDebugLogf("HostKeySHA256=%q 不是合法的 base64 SHA256（需 32 字节），按 insecure 处理", hk)
		}
	}
	return &ssh.ClientConfig{
		User: srv.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(cred.Password),
			// 兼容只开 keyboard-interactive/PAM 的老 Linux、AIX、堡垒机。
			ssh.KeyboardInteractive(passwordKeyboardInteractive(cred.Password)),
		},
		Timeout:           timeout,
		HostKeyCallback:   hostKeyCb,
		HostKeyAlgorithms: p.HostKeyAlgorithms,
		Config: ssh.Config{
			KeyExchanges: p.KeyExchanges,
			Ciphers:      p.Ciphers,
			MACs:         p.MACs,
		},
	}
}

func isNonRetryableSSHErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	// 认证失败不要反复重试，避免账号锁定/审计噪音。
	for _, marker := range []string{
		"unable to authenticate",
		"no supported methods remain",
		"permission denied",
		"authentication failed",
		"too many authentication failures",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func dialSSHOnce(ctx context.Context, addr string, srv Server, cred Credentials, timeout time.Duration, p sshCompatProfile, attempt int) (*ssh.Client, error) {
	cfg := newSSHClientConfig(srv, cred, timeout, p)
	sshDebugLogf("  attempt #%d profile : %s", attempt, p.Name)
	sshDebugLogf("  profile desc       : %s", p.Description)
	sshDebugLogf("  client kex         : %v", cfg.KeyExchanges)
	sshDebugLogf("  client host_key    : %v", cfg.HostKeyAlgorithms)
	sshDebugLogf("  client cipher      : %v", cfg.Ciphers)
	sshDebugLogf("  client mac         : %v", cfg.MACs)

	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	rawConn, dialErr := dialer.DialContext(ctx, "tcp", addr)
	if dialErr != nil {
		return nil, dialErr
	}
	// NewClientConn 没有 ctx 参数；设置握手 deadline，避免卡死。
	_ = rawConn.SetDeadline(time.Now().Add(timeout))

	trafficFile := openSSHTrafficLog()
	if trafficFile != nil {
		fmt.Fprintf(trafficFile, "\n\n========== new dial to %s @ %s | attempt=%d profile=%s ==========\n",
			addr, time.Now().Format("2006-01-02 15:04:05.000"), attempt, p.Name)
	}
	teed := &teedConn{
		conn:     rawConn,
		file:     trafficFile,
		readDir:  "S->C",
		writeDir: "C->S",
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(teed, addr, cfg)
	if err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	// 握手完成后清掉 deadline，避免长时间下载/实时 tail 被握手 deadline 误杀。
	_ = rawConn.SetDeadline(time.Time{})
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// Dial 连接并认证。
//
// 兼容性策略：先走默认兼容算法；如果握手阶段 EOF/RST/算法无交集，则自动换无 ECDH、legacy DH 再试。
// 认证失败不重试，避免账号锁定。这样可以减少内网反复发版成本。
func Dial(ctx context.Context, srv Server, cred Credentials, timeout time.Duration) (*Client, error) {
	if strings.TrimSpace(srv.Host) == "" {
		return nil, errors.New("host 不能为空")
	}
	if srv.Port == 0 {
		srv.Port = 22
	}
	addr := net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))
	sshDebugLogf("==== Dial 开始 ====")
	sshDebugLogf("  target       : %s (user=%s, timeout=%s)", addr, srv.Username, timeout)
	sshDebugLogf("  x/crypto/ssh : %s", cryptoSSHVersion())

	dialStart := time.Now()
	var lastErr error
	pkgLogMu.RLock()
	defaultProf := pkgDefaultProf
	pkgLogMu.RUnlock()
	_, profiles := resolveProfile(srv, defaultProf)
	for i, p := range profiles {
		if err := ctx.Err(); err != nil {
			sshDebugLogf("Dial 超时/取消: %s (耗时 %s)", addr, time.Since(dialStart))
			sshDebugLogf("  ctx err: %s", err.Error())
			return nil, fmt.Errorf("SSH 连接 %s 超时: %s", addr, SanitizeError(err.Error()))
		}
		attemptStart := time.Now()
		c, err := dialSSHOnce(ctx, addr, srv, cred, timeout, p, i+1)
		if err == nil {
			sshDebugLogf("Dial 成功: %s (耗时 %s, attempt=%d, profile=%s)", addr, time.Since(dialStart), i+1, p.Name)
			sshDebugLogf("  client version: %s", string(c.Conn.ClientVersion()))
			sshDebugLogf("  server version: %s", string(c.Conn.ServerVersion()))
			return &Client{srv: srv, cre: cred, conn: c}, nil
		}
		lastErr = err
		sshDebugLogf("Dial attempt 失败: %s (attempt=%d, profile=%s, 耗时 %s)", addr, i+1, p.Name, time.Since(attemptStart))
		sshDebugLogf("  raw err: %s", SanitizeError(err.Error()))
		if isNonRetryableSSHErr(err) {
			sshDebugLogf("  不再重试：该错误看起来是认证失败/账号策略问题，不是算法兼容问题。")
			break
		}
		if i < len(profiles)-1 {
			sshDebugLogf("  准备自动切换 SSH 兼容 profile 重试。")
		}
	}
	if lastErr == nil {
		lastErr = errors.New("未知 SSH 连接错误")
	}
	sshDebugLogf("Dial 最终失败: %s (总耗时 %s)", addr, time.Since(dialStart))
	sshDebugLogf("  提示: 查看 logs/ssh_traffic.log 中最后一次 attempt 的 KEXINIT/断开位置。")
	return nil, fmt.Errorf("SSH 连接 %s 失败: %s", addr, SanitizeError(lastErr.Error()))
}

// Close 关闭底层连接
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// RawConn 返回底层 *ssh.Client，用于 sftp.NewClient 等
func (c *Client) RawConn() *ssh.Client {
	if c == nil {
		return nil
	}
	return c.conn
}

// SanitizeError 把远程错误信息里的敏感词脱敏，避免返回给前端的 err 中出现 "password"。
// Go 的 x/crypto/ssh 在认证失败时会返回形如：
//
//	"ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain"
//
// 这里把 "password" 这种字面量替换为 "***"，避免审计日志或错误响应里残留。
func SanitizeError(s string) string {
	if s == "" {
		return s
	}
	for _, w := range []string{"password", "Password", "passwd", "PASSWORD", "PASSWD", "secret", "Secret", "privateKey", "private_key", "authorization", "Authorization"} {
		s = strings.ReplaceAll(s, w, "***")
	}
	return s
}

// Run 在远程执行一条受控命令（已构造好的命令字符串）
// 调用方需保证命令来自后端模板，且参数已经过转义。
//
// 超时控制：完全由 Go 客户端负责（context + 内部 timer）。
// 不依赖服务器端的 timeout 命令，老 Linux / 精简镜像也能跑。
// 超时触发后会先发 SIGTERM，等 1 秒；不退就 SIGKILL + 关闭 session。
//
// encoding 决定 stdout/stderr 的解码方式："utf-8"（默认）或 "gbk"。
func (c *Client) Run(ctx context.Context, command string, timeout time.Duration, encoding string) (stdout, stderr string, exitCode int, err error) {
	if c == nil || c.conn == nil {
		return "", "", -1, errors.New("ssh 客户端未连接")
	}
	type result struct {
		stdout string
		stderr string
		code   int
		err    error
	}
	done := make(chan result, 1)
	var sess *ssh.Session
	go func() {
		var e error
		sess, e = c.conn.NewSession()
		if e != nil {
			done <- result{err: fmt.Errorf("创建 session 失败: %w", e)}
			return
		}
		// 显式关闭 session，减少老 sshd / 堡垒机上的 channel 泄漏。
		// Run() 会等待命令结束；本 defer 只在命令自然/异常结束后清理。
		defer func() { _ = sess.Close() }()
		var outBuf, errBuf strings.Builder
		sess.Stdout = &safeWriter{w: &outBuf}
		sess.Stderr = &safeWriter{w: &errBuf}
		e = sess.Run(command)
		code := 0
		var exitErr *ssh.ExitError
		if errors.As(e, &exitErr) {
			code = exitErr.ExitStatus()
			e = nil // 命令本身跑完了，只是退出码非 0
		}
		// 注意：sshchannel 返回的 stdout/stderr 实际是 byte；
		// safeWriter 是按字节写入的，这里 builder 拿到的 string 在 Go 内部是 UTF-8。
		// 对于 GBK 服务器，原始字节是 GBK 编码，需要在拿到后做一次转换。
		// 但因为 builder 是按字节追加的，GBK 多字节字符被切到边界时会乱码。
		// 所以下面我们在 outBuf 外面再加一层"按字节再转换"路径：
		done <- result{stdout: outBuf.String(), stderr: errBuf.String(), code: code, err: e}
	}()

	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-ctx.Done():
		c.killSession(sess)
		return "", "", -1, ctx.Err()
	case <-t.C:
		c.killSession(sess)
		return "", "", -1, fmt.Errorf("远程命令超时（%s）", timeout)
	case r := <-done:
		// 解码
		stdout = decodeBytes([]byte(r.stdout), encoding)
		stderr = decodeBytes([]byte(r.stderr), encoding)
		return stdout, stderr, r.code, r.err
	}
}

// decodeBytes 按指定编码把字节流转成 UTF-8 字符串
func decodeBytes(b []byte, encoding string) string {
	if len(b) == 0 {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "gbk", "gb18030":
		r := transform.NewReader(bytes.NewReader(b), simplifiedchinese.GBK.NewDecoder())
		out, err := io.ReadAll(r)
		if err != nil {
			return string(b) // 转换失败时回退到原始字节
		}
		return string(out)
	default:
		// 默认当 UTF-8
		return string(b)
	}
}

// Stream 在远程执行一条长连接命令（典型用途：tail -F /path/to/log），
// 把 stdout 按行回调给 onLine。命令自然结束 / ctx 取消 / 写错误都会停。
//
// 设计要点：
//   - 一行一回调，按 \n 切（byte 0x0a），行尾的 \r 保留给前端；
//   - GBK 安全性：GBK 编码里 0x0a 不出现在多字节字符中，所以按 byte 切行不会切碎汉字；
//     切完后再整行按目标编码解到 UTF-8（前端拿到的是干净的字符串）；
//   - onLine 必须是非阻塞的：建议内部 chan 缓冲；阻塞会导致 SSH 接收阻塞、session 挂掉；
//   - 启动一个新 ssh.Session，每条流独立一个 session；session 自然退出（EOF 或 exit code）
//     或 ctx 取消，都会返回。
func (c *Client) Stream(ctx context.Context, command string, encoding string, onLine func(line string)) (exitCode int, err error) {
	if c == nil || c.conn == nil {
		return -1, errors.New("ssh 客户端未连接")
	}
	sess, e := c.conn.NewSession()
	if e != nil {
		return -1, fmt.Errorf("创建 session 失败: %w", e)
	}
	// 显式关闭 session（stream 退出时），减少老 sshd channel 泄漏。
	defer func() { _ = sess.Close() }()
	// stderr 单独读：错误时能拿到原因；正常命令 stderr 通常是空的
	stderrBuf := &safeWriter{w: &strings.Builder{}}
	sess.Stderr = stderrBuf

	pr, pw := io.Pipe()
	sess.Stdout = pw

	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)

	// 行切 + 解码协程
	go func() {
		defer pr.Close()
		defer pw.Close()
		scanner := bufio.NewScanner(pr)
		// 单行最大 1MB，避免单条超长日志爆内存
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			lineBytes := scanner.Bytes()
			// 转 UTF-8 字符串：GBK 模式下整行解码；UTF-8 模式直接当字节
			line := decodeBytes(lineBytes, encoding)
			onLine(line)
		}
	}()

	// Run 命令本身
	go func() {
		e := sess.Run(command)
		_ = pw.Close() // 让 scanner 退出
		code := 0
		var exitErr *ssh.ExitError
		if errors.As(e, &exitErr) {
			code = exitErr.ExitStatus()
			e = nil
		}
		done <- result{code: code, err: e}
	}()

	select {
	case <-ctx.Done():
		c.killSession(sess)
		return -1, ctx.Err()
	case r := <-done:
		return r.code, r.err
	}
}

// killSession 优雅地杀掉远端 session：SIGTERM -> 1s -> SIGKILL -> Close
func (c *Client) killSession(sess *ssh.Session) {
	if sess == nil {
		return
	}
	_ = sess.Signal(ssh.SIGTERM)
	// 简单 sleep：1s 内不退出就强杀
	time.Sleep(1 * time.Second)
	_ = sess.Signal(ssh.SIGKILL)
	_ = sess.Close()
}

// safeWriter 简单的写包装，避免在错误状态下无限增长
//
// 行为：
//   - 已写入字节数超过 max（或默认 8MB）时，写一次 "truncated" marker，
//     后续 Write 直接返回 len(p) 但不再追加到 builder；
//   - 用 truncated 标志保证 marker 只写一次，避免远端命令疯狂输出时
//     把 "...[truncated]..." 重复追加到 builder 里继续涨内存。
type safeWriter struct {
	w         *strings.Builder
	max       int
	wrot      int
	truncated bool
}

func (s *safeWriter) Write(p []byte) (int, error) {
	capBytes := s.max
	if capBytes <= 0 {
		capBytes = 8 * 1024 * 1024 // 8MB 上限，防止炸内存
	}
	// 已经完全截断过：直接吞掉后续字节，不动 builder
	if s.wrot >= capBytes {
		if !s.truncated {
			_, _ = s.w.Write([]byte("\n...[truncated]..."))
			s.truncated = true
		}
		return len(p), nil
	}
	// 这一次写入会越过上限：写满到上限，再写一次 marker
	if s.wrot+len(p) > capBytes {
		remain := capBytes - s.wrot
		if remain > 0 {
			_, _ = s.w.Write(p[:remain])
			s.wrot += remain
		}
		if !s.truncated {
			_, _ = s.w.Write([]byte("\n...[truncated]..."))
			s.truncated = true
		}
		return len(p), nil
	}
	// 正常写入
	n, err := s.w.Write(p)
	s.wrot += n
	return n, err
}
