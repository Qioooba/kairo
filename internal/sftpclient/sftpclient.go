// Package sftpclient 在已建立的 SSH 连接上做受控的文件读取。
//
// v0.4 起抽象出 RemoteFS 接口，client 可以由两种 backend 驱动：
//   - SFTPBackend：标准 SFTP 子系统（Linux / macOS 默认）
//   - ShellBackend：纯 SSH shell + cat/dd/ls（老 AIX / 银行前置机 / 精简镜像
//     只开 SSH 不开 SFTP 时的兜底）
//
// 不允许写远程文件、不允许删远程文件、不允许改权限。
//
// 模型：
//   - Client 持有一个 RemoteFS 实现；
//   - New(conn) 走 SFTP（默认行为，向后兼容）；
//   - NewAuto(conn, runFunc) 先试 SFTP，失败自动降级到 ShellBackend；
//     runFunc 是回调，签名同 sshclient.Client.Run —— 这样 sftpclient 不直接
//     依赖 sshclient 包（避免循环依赖），调用方在 handler 里桥接。
package sftpclient

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// sftpFile 是对 *sftp.File 的最小抽象（io.Reader + io.Closer + Stat），
// 方便测试时注入假实现。Stat 用于在下载开始时拿到文件总大小，便于上报进度。
type sftpFile interface {
	io.Reader
	io.Closer
	Stat() (os.FileInfo, error)
}

// RemoteFS 是 sftpclient 包对外暴露的"远端文件系统"抽象（项 8 新增）。
//
// 两种实现：
//   - sftpBackend：标准 SFTP 子系统；
//   - shellBackend：SSH shell + 命令（cat / dd / ls）。
//
// 加新 backend 只要再写一个 struct 实现这 4 个方法。
type RemoteFS interface {
	Open(path string) (sftpFile, error)
	ReadDir(path string) ([]os.FileInfo, error)
	Stat(path string) (os.FileInfo, error)
	Close() error
}

// sftpBackend 是对 *sftp.Client 的最小抽象，
// 方便测试时注入假实现。
//
// 命名上虽然保持 "sftpFile"，但 v0.3 起 ReadDir / Stat 也走这个抽象，
// 这样 mock 时可以用 map/file 假数据完整覆盖 list / stat 路径。
type sftpBackend interface {
	Open(path string) (sftpFile, error)
	ReadDir(path string) ([]os.FileInfo, error)
	Stat(path string) (os.FileInfo, error)
	Close() error
}

// Client 包装一个远端文件系统客户端（SFTP 或 ShellBackend）
type Client struct {
	b RemoteFS
}

// New 在已有 SSH 连接上创建 SFTP 客户端。
//
// 如果 SFTP 子系统不可用（老 sshd / 安全加固环境），返回 error；
// 调用方可以用 NewAuto 自动 fallback 到 ShellBackend。
func New(conn *ssh.Client) (*Client, error) {
	sc, err := sftp.NewClient(conn)
	if err != nil {
		return nil, fmt.Errorf("创建 sftp 客户端失败: %w", err)
	}
	return &Client{b: &realSftpBackend{c: sc}}, nil
}

// NewAuto 优先 SFTP；SFTP 不可用时自动 fallback 到 ShellBackend（项 8 新增）。
//
// runFn 是 shell 命令执行回调，签名同 sshclient.Client.Run：
//
//	func(ctx context.Context, command string, timeout time.Duration, encoding string) (stdout, stderr string, code int, err error)
//
// 调用方在 handler 里写一个 wrapper 把 sshclient.Client.Run 包进去即可。
// 为什么要回调而不是直接传 *sshclient.Client：避免 sftpclient 反向依赖 sshclient。
//
// 行为：
//   - SFTP OK → 返回 SFTP 客户端（同 New）；
//   - SFTP 失败 → 试 ShellBackend（构造时不会真发命令，Open/Stat/ReadDir 时才发）；
//   - 两者都失败 → 报错（一般是网络问题，不是 backend 选择问题）。
func NewAuto(conn *ssh.Client, runFn func(ctx context.Context, command string, timeout time.Duration, encoding string) (string, string, int, error)) (*Client, error) {
	if sc, err := sftp.NewClient(conn); err == nil {
		return &Client{b: &realSftpBackend{c: sc}}, nil
	}
	// SFTP 不可用 → fallback 到 ShellBackend
	if runFn == nil {
		return nil, fmt.Errorf("SFTP 不可用且未提供 shell fallback 执行器")
	}
	return &Client{b: newShellBackend(conn, runFn)}, nil
}

// newWithBackend 内部构造函数，允许测试注入 mock backend。
func newWithBackend(b sftpBackend) *Client {
	return &Client{b: b}
}

// NewWithBackend 导出 newWithBackend 供跨包测试使用（如 httpserver 集成测试）。
// 生产代码不要用；用 New。
func NewWithBackend(b sftpBackend) *Client {
	return newWithBackend(b)
}

// realSftpBackend 真实 *sftp.Client 的适配器
type realSftpBackend struct {
	c *sftp.Client
}

func (r *realSftpBackend) Open(path string) (sftpFile, error) {
	return r.c.Open(path)
}

func (r *realSftpBackend) ReadDir(path string) ([]os.FileInfo, error) {
	return r.c.ReadDir(path)
}

func (r *realSftpBackend) Stat(path string) (os.FileInfo, error) {
	return r.c.Stat(path)
}

func (r *realSftpBackend) Close() error {
	return r.c.Close()
}

// Close 关闭
func (c *Client) Close() error {
	if c == nil || c.b == nil {
		return nil
	}
	return c.b.Close()
}

// Backend 返回当前的 backend 实现名（"sftp" / "shell" / "mock"），便于 debug / 审计。
func (c *Client) Backend() string {
	if c == nil || c.b == nil {
		return "none"
	}
	switch c.b.(type) {
	case *realSftpBackend:
		return "sftp"
	case *shellBackend:
		return "shell"
	default:
		return "mock"
	}
}

// DownloadFile 把 remotePath 下载到 localPath
//
// remotePath 必须由调用方做过白名单校验。
//
// 下载前会先 Stat：远端是目录时直接报错（"暂不支持直接下载目录"），
// 避免不同 SFTP server 对 Open(目录) 的报错/返回不一致。
func (c *Client) DownloadFile(remotePath, localPath string) (int64, error) {
	return c.DownloadFileContext(context.Background(), remotePath, localPath, nil)
}

// DownloadFileWithProgress 同 DownloadFile，但通过 progress 回调上报下载进度。
//
// 参数 progress 可以为 nil，此时行为与 DownloadFile 完全一致。
// 回调语义：每写入 progressInterval 字节触发一次；最后完成时再补一次 (written, total) 保证前端能到 100%。
// total 来自 sftpFile.Stat()，若 Stat 失败则为 -1（前端按 indeterminate 进度条处理）。
// 回调可能在 io.Copy 路径中被并发触发，调用方需自行同步。
func (c *Client) DownloadFileWithProgress(remotePath, localPath string, progress func(written, total int64)) (int64, error) {
	return c.DownloadFileContext(context.Background(), remotePath, localPath, progress)
}

// DownloadFileContext 是 DownloadFile 的 ctx 取消版本。
//
// ctx 取消时会立即关闭底层 sftp 连接，正在进行的 io.Copy 会因为底层 read
// 返回错误而退出，避免下大文件时取消要等好几秒网络读返回才生效。
// ctx 为 context.Background() 时行为与 DownloadFile 等价。
//
// progress 可以为 nil。
func (c *Client) DownloadFileContext(ctx context.Context, remotePath, localPath string, progress func(written, total int64)) (int64, error) {
	if c == nil || c.b == nil {
		return 0, fmt.Errorf("sftp 客户端未连接")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// 下载前 Stat：拿 total + 提前拒绝目录，避免 Open(目录) 在不同 server 行为不一致。
	info, statErr := c.b.Stat(remotePath)
	if statErr != nil {
		return 0, fmt.Errorf("stat 远程文件失败: %w", statErr)
	}
	if info.IsDir() {
		return 0, fmt.Errorf("暂不支持直接下载目录: %s", remotePath)
	}
	total := info.Size()

	src, err := c.b.Open(remotePath)
	if err != nil {
		return 0, fmt.Errorf("打开远程文件失败: %w", err)
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return 0, fmt.Errorf("创建本地目录失败: %w", err)
	}
	dst, err := os.OpenFile(localPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, fmt.Errorf("创建本地文件失败: %w", err)
	}
	defer dst.Close()

	// ctx 取消时主动关掉 backend：正在进行的 io.Copy 会因 read 报错而退出。
	// SFTP backend：先关 src 再关 c.b；
	// Shell backend：关 SSH session（让阻塞的 cat 退出）。
	cancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = src.Close()
			_ = c.b.Close()
		case <-cancelDone:
		}
	}()
	defer close(cancelDone)

	pw := &progressWriter{w: dst, total: total, progress: progress}
	n, err := io.Copy(pw, src)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return n, fmt.Errorf("下载被取消: %w", ctxErr)
		}
		return n, fmt.Errorf("下载过程中断: %w", err)
	}
	// 收尾回调：保证 100% 状态一定会到达（最后一节可能 < 64KB 不触发中间回调）
	if progress != nil {
		progress(n, total)
	}
	return n, nil
}

// ReadDir 列出 path 下的所有条目（文件和目录）。
//
// 不做白名单校验——调用方决定该传什么路径；
// 真实访问控制由远端 SSH 服务器的账号权限承担。
// 隐藏文件（以 . 开头）也一并返回，由调用方决定是否过滤。
func (c *Client) ReadDir(path string) ([]os.FileInfo, error) {
	if c == nil || c.b == nil {
		return nil, fmt.Errorf("sftp 客户端未连接")
	}
	infos, err := c.b.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("列出目录失败: %w", err)
	}
	return infos, nil
}

// Stat 拿到 path 对应的文件信息（大小、修改时间、是否为目录、权限位）。
//
// 用于"文件浏览器"页：先 Stat 判断是文件还是目录，再决定是进子目录还是直接下载。
func (c *Client) Stat(path string) (os.FileInfo, error) {
	if c == nil || c.b == nil {
		return nil, fmt.Errorf("sftp 客户端未连接")
	}
	info, err := c.b.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat 失败: %w", err)
	}
	return info, nil
}

// progressInterval 进度回调最小间隔（字节）。64KB 对内网 SFTP
// 来说粒度足够细，1GB 文件约 16384 次回调。配合下面的
// progressMinInterval 时间节流，最终频率 ≈ 10 Hz，避免刷爆前端。
const progressInterval = 64 * 1024

// progressMinInterval 进度回调最小时间间隔。100ms ≈ 10 Hz，
// 人眼能感觉到流畅但不会刷爆浏览器渲染。
const progressMinInterval = 100 * time.Millisecond

// progressWriter 包装 io.Writer，按"字节数 + 时间"双重节流回调 progress。
//
// 触发条件：写入了 >= progressInterval 字节 且 距上次回调 >= progressMinInterval
// 两者都满足才推一次。保证 1GB 文件下载最多 ~10 Hz 回调，前端不卡。
// 收尾（文件完成）时由 download() 主动再调一次保证 100% 状态到达。
type progressWriter struct {
	w        io.Writer
	total    int64
	written  int64
	progress func(written, total int64)
	lastCb   int64
	lastCbT  time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	if n > 0 {
		p.written += int64(n)
		if p.progress != nil && p.written-p.lastCb >= progressInterval {
			now := time.Now()
			if p.lastCb == 0 || now.Sub(p.lastCbT) >= progressMinInterval {
				p.lastCb = p.written
				p.lastCbT = now
				p.progress(p.written, p.total)
			}
		}
	}
	return n, err
}

// ============================================================================
// ShellBackend：通过 SSH shell 命令实现 Open/ReadDir/Stat（项 8 新增）
// ============================================================================
//
// 适用场景：
//   - 老 AIX / 银行前置机 / 精简 Linux 镜像只开 SSH 不开 SFTP；
//   - SSH 服务端配置了 ForceCommand / internal-sftp 没启。
//
// 实现策略：
//   - Open(path) → 跑 "cat <path>"（小文件，几十 MB 内 OK），流式读 stdout；
//     大文件后续可改 dd + base64 分片，v0.4 先覆盖 cat。
//   - ReadDir(path) → 跑 "ls -la <path>"，按行解析。
//   - Stat(path) → 跑 "ls -ld <path>"，单行解析（不会展开目录）。
//
// 安全：
//   - path 走 shell 单引号包（跟 logquery 同样风格），不直接拼。
//   - 远端命令本身是受控的（白名单），path 只做目录/文件名校验（调用方负责）。
//   - ctx 取消时通过 SSH session.Close() 杀掉远端 cat。

// shellBackend 是 RemoteFS 的"shell 命令"实现。
//
// 持有 sshclient.Client.Run 的回调（不是直接持 ssh.Client），避免循环依赖。
type shellBackend struct {
	conn *ssh.Client // 保留 ssh conn 用于 session 级别关闭
	run  func(ctx context.Context, command string, timeout time.Duration, encoding string) (string, string, int, error)
	// sessionKill 是 ctx 取消时用来强杀 SSH session 的回调（Run 内部用 ssh.Session.Signal/Kill）。
	// 没有这个回调时，ctx 取消后 Run 的 goroutine 仍可能阻塞在读 stdout 上。
	sessionKill func()
	mu          sync.Mutex
}

func newShellBackend(conn *ssh.Client, runFn func(ctx context.Context, command string, timeout time.Duration, encoding string) (string, string, int, error)) *shellBackend {
	return &shellBackend{conn: conn, run: runFn}
}

// Close 关掉 ssh 连接。
func (s *shellBackend) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// shellQuoteArg 把单个 arg 用单引号包起来（防 shell 注入）。
// 单引号在 shell 里没有转义机制，所以 arg 里的 ' 必须先去掉。
func shellQuoteArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "") + "'"
}

// shellStatInfo 是 shellBackend Stat / ReadDir 返回的最小 os.FileInfo 实现。
//
// 实现：
//   - Name：path 的 basename；
//   - Size：bytes；
//   - IsDir：根据 mode 位判断（ls -la 第一字段）；
//   - ModTime：从 ls -la 的月份/日/时间/年解析；
//   - Mode：从 ls -la 字符串解析（drwxr-xr-x 这种）。
//
// 解析逻辑兼容 GNU ls / AIX ls / BSD ls：
//   - GNU:  "-rw-r--r-- 1 user group 12345 Jun 21 10:00 file"
//   - AIX:  "-rw-r--r--    1 user     group         12345 Jun 21 10:00 file"
//   - BSD:  "-rw-r--r--  1 user  group  12345 Jun 21 10:00 file"
//   - 字段数 ≥ 9：mode (0) / links (1) / owner (2) / group (3) / size (4) / date (5-7) / name (≥8)
type shellStatInfo struct {
	name    string
	size    int64
	isDir   bool
	mode    os.FileMode
	modTime time.Time
}

func (s shellStatInfo) Name() string       { return s.name }
func (s shellStatInfo) Size() int64        { return s.size }
func (s shellStatInfo) Mode() os.FileMode  { return s.mode }
func (s shellStatInfo) ModTime() time.Time { return s.modTime }
func (s shellStatInfo) IsDir() bool        { return s.isDir }
func (s shellStatInfo) Sys() interface{}   { return nil }

// parseLsLine 解析 ls -la 输出的一行。
//
// returns: mode / size / mtime / name / ok
func parseLsLine(line string) (mode os.FileMode, size int64, mtime time.Time, name string, ok bool) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" {
		return
	}
	fields := strings.Fields(line)
	if len(fields) < 9 {
		return
	}
	mode = parseMode(fields[0])
	// size 是第 5 字段（0-based 第 4）
	sz, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return
	}
	size = sz
	// mtime 是 6/7/8 字段（month day time/year）
	mtime, ok2 := parseLsTime(fields[5], fields[6], fields[7])
	if !ok2 {
		return
	}
	// name 是最后一字段（ls -la 在多空格分隔下也只占一段；连空格文件名罕见，先不支持）
	name = fields[len(fields)-1]
	// 跳过 "." 和 ".."
	if name == "." || name == ".." {
		ok = true
		_ = ok
		return
	}
	ok = true
	return
}

// parseMode 把 ls 的 mode 字符串转 os.FileMode。
func parseMode(s string) os.FileMode {
	if len(s) < 10 {
		return 0
	}
	var m os.FileMode
	switch s[0] {
	case 'd':
		m |= os.ModeDir
	case 'l':
		m |= os.ModeSymlink
	case 'c', 'b', 'p', 's':
		// 不常见，老 Linux/AIX 才有；用 ModeDevice / ModeNamedPipe / ModeSocket 区分
		switch s[0] {
		case 'c', 'b':
			m |= os.ModeDevice
		case 'p':
			m |= os.ModeNamedPipe
		case 's':
			m |= os.ModeSocket
		}
	}
	for i := 1; i < 10; i++ {
		switch i {
		case 1, 4, 7: // owner / group / other read
			if s[i] == 'r' {
				m |= 0o444 >> (i / 3) * 3
			}
		case 2, 5, 8: // write
			if s[i] == 'w' {
				m |= 0o222 >> (i / 3) * 3
			}
		case 3, 6, 9: // execute
			if s[i] == 'x' || s[i] == 's' || s[i] == 't' {
				m |= 0o111 >> (i / 3) * 3
			}
		}
	}
	return m
}

// parseLsTime 解析 ls 时间字段：month / day / time-or-year。
//
// 6 种格式：
//   - "Jun 21 10:00" + 当前年（无年份，常见 ls 默认）
//   - "Jun 21 2024" + "00:00"（带年份，老文件）
//   - "Jun 21" + "10:00" + "2024"（罕见）
//   - "Jun 21 10:00:00"（GNU --time-style=full-iso）
//   - "21 Jun 2024" + "10:00"（不同 locale）
//   - AIX: "Jun 21 10:00:00 CDT 2024"
//
// 这里只覆盖最常见的 2 种 + AIX 长格式，其他按"解析失败返回 ok=false"处理。
func parseLsTime(month, day, timeOrYear string) (time.Time, bool) {
	now := time.Now()
	// 格式 1: month day time（time 是 HH:MM）
	t, err := time.Parse("Jan 2 15:04", month+" "+day+" "+timeOrYear)
	if err == nil {
		return time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.Local), true
	}
	// 格式 1b: month day time:sec
	t, err = time.Parse("Jan 2 15:04:05", month+" "+day+" "+timeOrYear)
	if err == nil {
		return time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.Local), true
	}
	// 格式 2: month day year
	t, err = time.Parse("Jan 2 2006", month+" "+day+" "+timeOrYear)
	if err == nil {
		return t, true
	}
	// 格式 3: day month year + time（GNU locale 不同）
	t, err = time.Parse("2 Jan 2006", day+" "+month+" "+timeOrYear)
	if err == nil {
		return t, true
	}
	return time.Time{}, false
}

// Stat 跑 ls -ld <path> 拿单条目的元信息。
func (s *shellBackend) Stat(path string) (os.FileInfo, error) {
	cmd := "ls -ld " + shellQuoteArg(path)
	stdout, stderr, code, err := s.run(context.Background(), cmd, 30*time.Second, "utf-8")
	if err != nil {
		return nil, fmt.Errorf("ls -ld 失败: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("ls -ld 退出码 %d: %s", code, strings.TrimSpace(stderr))
	}
	// 输出可能含 "ls: cannot access ..." 之类错误信息（即使 code=0 的边缘情况）
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ls:") || strings.HasPrefix(line, "cannot access") {
			return nil, fmt.Errorf("stat %s 失败: %s", path, line)
		}
		mode, size, mtime, name, ok := parseLsLine(line)
		if !ok {
			continue
		}
		return shellStatInfo{
			name:    name,
			size:    size,
			isDir:   mode&os.ModeDir != 0,
			mode:    mode,
			modTime: mtime,
		}, nil
	}
	return nil, fmt.Errorf("stat %s: 解析失败（stdout=%s）", path, stdout)
}

// ReadDir 跑 ls -la <path> 拿目录所有条目。
func (s *shellBackend) ReadDir(path string) ([]os.FileInfo, error) {
	cmd := "ls -la " + shellQuoteArg(path)
	stdout, stderr, code, err := s.run(context.Background(), cmd, 30*time.Second, "utf-8")
	if err != nil {
		return nil, fmt.Errorf("ls -la 失败: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("ls -la 退出码 %d: %s", code, strings.TrimSpace(stderr))
	}
	var infos []os.FileInfo
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "total ") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "ls:") {
			continue
		}
		mode, size, mtime, name, ok := parseLsLine(line)
		if !ok {
			continue
		}
		if name == "." || name == ".." {
			continue
		}
		infos = append(infos, shellStatInfo{
			name:    name,
			size:    size,
			isDir:   mode&os.ModeDir != 0,
			mode:    mode,
			modTime: mtime,
		})
	}
	if len(infos) == 0 {
		// 目录空 / 全是 . .. 也算 OK（返空 slice）
		return infos, nil
	}
	return infos, nil
}

// shellFile 是 shellBackend Open 返回的"虚拟文件"，包装 cat 输出流。
//
// 实现细节：
//   - 构造时同步跑一次 `wc -c <path>` 拿 total；
//   - 用 Stream 接口（调用方提供的 Run 包装）持续读 stdout；
//   - ctx 取消时关闭 stream —— 这里通过 sshconn 的 Close 间接杀掉 cat。
type shellFile struct {
	r       *bufio.Reader
	size    int64
	mu      sync.Mutex
	closed  bool
	closeFn func()
}

func (f *shellFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, io.EOF
	}
	return f.r.Read(p)
}

func (f *shellFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		if f.closeFn != nil {
			f.closeFn()
		}
	}
	return nil
}

func (f *shellFile) Stat() (os.FileInfo, error) {
	return shellStatInfo{name: "shellFile", size: f.size, mode: 0o644, modTime: time.Time{}}, nil
}

// Open 跑 `cat <path>` 流式读。
//
// 注意：本实现限制 total 必须通过 Stat 先拿到（调用方 DownloadFileContext
// 就是先 Stat 再 Open）。shellFile.Stat() 返回 0 size 是为了让 progress 回调
// 也能跑（不会 panic）。
func (s *shellBackend) Open(path string) (sftpFile, error) {
	// 先 Stat 拿 size（cat 不会自动报大小）
	info, err := s.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("Open 前 Stat 失败: %w", err)
	}
	// 构造命令：用 sh -c 让 cat 真正跑起来
	// 注意：sh -c + 单引号包整段，避免 path 里的特殊字符搞坏 shell
	cmd := "sh -c " + shellQuoteArg("cat "+shellQuoteArg(path)+" 2>/dev/null")
	// 直接调 ssh.Client 拿 session（不走 Run 回调，因为 Run 是阻塞式的，
	// 而 Open 需要"流式"读）。
	sess, err := s.conn.NewSession()
	if err != nil {
		return nil, fmt.Errorf("创建 SSH session 失败: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("拿 stdout pipe 失败: %w", err)
	}
	if err := sess.Start(cmd); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("启动 cat 失败: %w", err)
	}
	return &shellFile{
		r:    bufio.NewReader(stdout),
		size: info.Size(),
		closeFn: func() {
			_ = sess.Close()
		},
	}, nil
}
