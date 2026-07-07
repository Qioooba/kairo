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

// SftpFile 是对 *sftp.File 的最小抽象（io.Reader + io.Closer + Stat），
// 方便测试时注入假实现。Stat 用于在下载开始时拿到文件总大小，便于上报进度。
//
// v0.5 起导出（项 1 文件预览用）：之前是 lowercase sftpFile，
// 但 httpserver 包要引用它作为 sftpClientLike.Open 的返回类型。
type SftpFile interface {
	io.Reader
	io.Closer
	Stat() (os.FileInfo, error)
}

// RemoteFS 是 sftpclient 包对外暴露的"远端文件系统"抽象（项 8 新增）。
//
// 两种实现：
//   - realSftpBackend：标准 SFTP 子系统；
//   - shellBackend：SSH shell + 命令（cat / dd / ls）。
//
// 加新 backend 只要再写一个 struct 实现这 6 个方法。
//
// v1.0 起加 ListLimited：列出目录时给一个 max 限制，避免 10w 文件目录把全部条目
// 拉回本地（OOM 风险）。返回 (entries, truncated, err)：
//   - entries: 最多 max 条目录项；
//   - truncated: true 表示远端实际条目数 > max，前端可以提示"目录过大，仅展示前 N 条"。
//
// v0.12 起加 WriteFile：支持文件上传（用于远程编辑场景）。
type RemoteFS interface {
	Open(path string) (SftpFile, error)
	ReadDir(path string) ([]os.FileInfo, error)
	ListLimited(path string, max int) ([]os.FileInfo, bool, error)
	Stat(path string) (os.FileInfo, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	Close() error
}

// sftpBackend 是对 *sftp.Client 的最小抽象，
// 方便测试时注入假实现。
//
// 命名上虽然保持 "sftpFile"，但 v0.3 起 ReadDir / Stat 也走这个抽象，
// 这样 mock 时可以用 map/file 假数据完整覆盖 list / stat 路径。
//
// v1.0 起加 ListLimited：见 RemoteFS 接口注释。
// v0.12 起加 WriteFile：支持文件上传。
type sftpBackend interface {
	Open(path string) (SftpFile, error)
	ReadDir(path string) ([]os.FileInfo, error)
	ListLimited(path string, max int) ([]os.FileInfo, bool, error)
	Stat(path string) (os.FileInfo, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
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

func (r *realSftpBackend) Open(path string) (SftpFile, error) {
	return r.c.Open(path)
}

func (r *realSftpBackend) ReadDir(path string) ([]os.FileInfo, error) {
	return r.c.ReadDir(path)
}

// ListLimited SFTP 实现：调 ReadDir 后本地 cap。
//
// 治标：pkg/sftp 的 ReadDir 协议层就会一次性拿所有条目，远端遍历 + 本地内存
// 都已完成。本地 cap 只能挡住后续解析 / 序列化阶段，无法阻止 SFTP 协议读所有
// 条目。对 10w 文件目录仍是慢路径，但能避免"全量拉回 + 序列化 10w JSON"把进程
// 吃爆。彻底治本的远端 head 在 shellBackend.ListLimited 里。
//
// max <= 0 时不截断（行为同 ReadDir，返回 truncated=false）。
func (r *realSftpBackend) ListLimited(path string, max int) ([]os.FileInfo, bool, error) {
	infos, err := r.c.ReadDir(path)
	if err != nil {
		return nil, false, err
	}
	if max > 0 && len(infos) > max {
		return infos[:max], true, nil
	}
	return infos, false, nil
}

func (r *realSftpBackend) Stat(path string) (os.FileInfo, error) {
	return r.c.Stat(path)
}

func (r *realSftpBackend) WriteFile(path string, data []byte, perm os.FileMode) error {
	f, err := r.c.Create(path)
	if err != nil {
		return fmt.Errorf("创建远程文件失败: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("写入远程文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭远程文件失败: %w", err)
	}
	if perm != 0 {
		if err := r.c.Chmod(path, perm); err != nil {
			return fmt.Errorf("修改权限失败: %w", err)
		}
	}
	return nil
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
// total 来自 SftpFile.Stat()，若 Stat 失败则为 -1（前端按 indeterminate 进度条处理）。
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

	// ctx 取消时只关闭当前文件句柄：正在进行的 io.Copy 会因 read 报错而退出。
	// 不要关闭整个 backend，否则取消单个下载会让同一个 Client 后续操作全部失效。
	cancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = src.Close()
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

// Open 打开远端文件，返回 io.ReadCloser + Stat（v0.5 项 1 文件预览用）。
//
// 直接转发到 backend（realSftpBackend / shellBackend），
// 调用方负责 Close + 用 io.LimitReader 限速读。
//
// 注意：path 不做白名单校验，调用方决定传什么路径；
// 真实访问控制由远端 SSH 服务器的账号权限承担。
func (c *Client) Open(path string) (SftpFile, error) {
	if c == nil || c.b == nil {
		return nil, fmt.Errorf("sftp 客户端未连接")
	}
	f, err := c.b.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开远端文件失败: %w", err)
	}
	return f, nil
}

// ReadDir 列出 path 下的所有条目（文件和目录）。
//
// 不做白名单校验——调用方决定该传什么路径；
// 真实访问控制由远端 SSH 服务器的账号权限承担。
// 隐藏文件（以 . 开头）也一并返回，由调用方决定是否过滤。
//
// v1.0 起：10w 文件目录会爆内存，调用方应该改用 ListLimited(path, max)。
// ReadDir 保留仅为向后兼容（v0.x 老代码）。
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

// ListLimited 列出 path 下最多 max 个条目（v1.0 新增）。
//
// 解决 10w 文件目录爆内存：
//   - SFTP backend：本地 cap（治标，pkg/sftp 协议层会拉回所有条目）
//   - Shell backend：远端 head（治本，远端 ls 截断后才传回）
//
// 返回：
//   - entries: 实际条目（最多 max 条）
//   - truncated: true 表示远端实际条目数 > max
//   - err: 协议 / 网络 / 权限错误
//
// max <= 0 时不截断（行为同 ReadDir，truncated=false）。
func (c *Client) ListLimited(path string, max int) ([]os.FileInfo, bool, error) {
	if c == nil || c.b == nil {
		return nil, false, fmt.Errorf("sftp 客户端未连接")
	}
	entries, truncated, err := c.b.ListLimited(path, max)
	if err != nil {
		return nil, false, fmt.Errorf("列出目录失败: %w", err)
	}
	return entries, truncated, nil
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

// UploadFile 把本地文件上传到远端路径。
//
// remotePath 必须由调用方做过白名单校验。
// perm 是远端文件权限，传 0 时使用默认权限。
func (c *Client) UploadFile(localPath, remotePath string, perm os.FileMode) error {
	if c == nil || c.b == nil {
		return fmt.Errorf("sftp 客户端未连接")
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("读取本地文件失败: %w", err)
	}
	if perm == 0 {
		perm = 0o644
	}
	if err := c.b.WriteFile(remotePath, data, perm); err != nil {
		return fmt.Errorf("上传文件失败: %w", err)
	}
	return nil
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

// Close 释放 shell backend 自身资源。
//
// 注意：shellBackend 使用调用方传入的共享 SSH 连接创建 session，不能在这里关闭
// s.conn，否则关闭一个文件客户端会连带中断同一 SSH 连接上的其它操作。
func (s *shellBackend) Close() error {
	return nil
}

// WriteFile 通过 shell 命令上传文件：用 cat > path 覆盖写入。
//
// 实现：通过 SSH session 的 stdin 写入数据，配合 cat > 命令覆盖目标文件。
// 对于大文件，建议使用 SFTP backend（shell 方式受 SSH buffer 限制）。
func (s *shellBackend) WriteFile(path string, data []byte, perm os.FileMode) error {
	cmd := "cat > " + shellQuoteArg(path)
	sess, err := s.conn.NewSession()
	if err != nil {
		return fmt.Errorf("创建 SSH session 失败: %w", err)
	}
	defer sess.Close()

	stdin, err := sess.StdinPipe()
	if err != nil {
		return fmt.Errorf("拿 stdin pipe 失败: %w", err)
	}

	if err := sess.Start(cmd); err != nil {
		return fmt.Errorf("启动 cat 失败: %w", err)
	}

	if _, err := stdin.Write(data); err != nil {
		return fmt.Errorf("写入数据失败: %w", err)
	}
	if err := stdin.Close(); err != nil {
		return fmt.Errorf("关闭 stdin 失败: %w", err)
	}

	if err := sess.Wait(); err != nil {
		return fmt.Errorf("cat 命令执行失败: %w", err)
	}

	if perm != 0 {
		chmodCmd := fmt.Sprintf("chmod %o %s", perm, shellQuoteArg(path))
		_, _, _, err = s.run(context.Background(), chmodCmd, 10*time.Second, "utf-8")
		if err != nil {
			return fmt.Errorf("chmod 失败: %w", err)
		}
	}

	return nil
}

// shellQuoteArg 把单个 arg 用单引号包起来（防 shell 注入）。
// shell 中单引号的正确转义方式是：结束单引号、插入转义单引号、重新开始单引号。
func shellQuoteArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
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
	infos, _, err := s.parseLsLa(stdout)
	return infos, err
}

// ListLimited 跑 "ls -la <path> | head -n max+10" 远端截断（治本，v1.0 新增）。
//
// 多取 10 行是给 "total N" / "." / ".." 这些非文件条目留 buffer；
// 本地解析后过滤掉这些 + 再 cap 到 max。
//
// truncated 判定：head 拿到的行数 >= headN（即 max+10）即认为远端有更多条目。
// 这是近似（如果 ls 真的只有 max+10 行就误报 truncated=true），但用户场景下
// "目录条目数 = max+10" 概率极低，不影响实际 UX。
//
// max <= 0 时退化为 ReadDir。
func (s *shellBackend) ListLimited(path string, max int) ([]os.FileInfo, bool, error) {
	if max <= 0 {
		infos, err := s.ReadDir(path)
		return infos, false, err
	}
	headN := max + 10
	cmd := fmt.Sprintf("ls -la %s 2>/dev/null | head -n %d", shellQuoteArg(path), headN)
	stdout, stderr, code, err := s.run(context.Background(), cmd, 30*time.Second, "utf-8")
	if err != nil {
		return nil, false, fmt.Errorf("ls -la 失败: %w", err)
	}
	if code != 0 {
		return nil, false, fmt.Errorf("ls -la 退出码 %d: %s", code, strings.TrimSpace(stderr))
	}
	infos, rawLineCount, err := s.parseLsLa(stdout)
	if err != nil {
		return nil, false, err
	}
	truncated := rawLineCount >= headN
	if len(infos) > max {
		infos = infos[:max]
		truncated = true
	}
	return infos, truncated, nil
}

// parseLsLa 解析 ls -la 输出，返回 (有效条目, 原始行数, err)。
//
// 共享给 ReadDir / ListLimited 复用，避免逻辑漂移。
func (s *shellBackend) parseLsLa(stdout string) ([]os.FileInfo, int, error) {
	lines := strings.Split(stdout, "\n")
	var infos []os.FileInfo
	for _, line := range lines {
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
	return infos, len(lines), nil
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
	once    sync.Once
	closeFn func()
}

func (f *shellFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return 0, io.EOF
	}
	return f.r.Read(p)
}

func (f *shellFile) Close() error {
	f.once.Do(func() {
		f.mu.Lock()
		f.closed = true
		f.mu.Unlock()
		if f.closeFn != nil {
			f.closeFn()
		}
	})
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
func (s *shellBackend) Open(path string) (SftpFile, error) {
	// 先 Stat 拿 size（cat 不会自动报大小）
	info, err := s.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("Open 前 Stat 失败: %w", err)
	}
	// 构造命令：只对 path 做一次 shell 参数转义。
	// 不能再用 sh -c 包裹整段命令，否则外层转义会破坏内层单引号并造成命令注入。
	cmd := "cat " + shellQuoteArg(path)
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
	sess.Stderr = io.Discard
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

// FormatMode 把 os.FileMode 转成 `ls -l` 风格的 10 字符字符串（"drwxr-xr-x"）。
//
// 为什么不用 (os.FileMode).String()：
// Go 1.20+ 的 FileMode.String() 对 setuid/setgid/sticky 这些"高位"位处理
// 不再走 ls 风格的 s/S/t/T 字符，例如 chmod 4755 的文件会显示成 "urwxr-xr-x"
// 或者 "-rwxr-xr-x"（完全丢失 setuid），前端拿到这种字符串就显示不出来原本
// 的安全标识位。这里我们按 ls -l 的标准格式自己拼一遍，保证前端展示稳定。
//
// 标准 10 字符串：
//   - [0]   ：类型（d 目录 / l 符号链接 / - 普通文件 / c/b/p/s 等）
//   - [1-3] ：owner  rwx
//   - [4-6] ：group  rwx
//   - [7-9] ：other  rwx
// setuid/setgid/sticky 把对应位（owner 的 x → s/S、group 的 x → s/S、
// other 的 x → t/T）的字符替换。
func FormatMode(m os.FileMode) string {
	var buf [10]byte
	switch {
	case m&os.ModeDir != 0:
		buf[0] = 'd'
	case m&os.ModeSymlink != 0:
		buf[0] = 'l'
	case m&os.ModeDevice != 0:
		// c / b 不区分，前端无对应图标，统一用 'c'
		buf[0] = 'c'
	case m&os.ModeNamedPipe != 0:
		buf[0] = 'p'
	case m&os.ModeSocket != 0:
		buf[0] = 's'
	default:
		buf[0] = '-'
	}
	// 位 8..0 映射到 "rwxrwxrwx"（位 n 决定位置 8-n 字符）
	const permChars = "rwxrwxrwx"
	for i := 0; i < 9; i++ {
		if m&(1<<uint(8-i)) != 0 {
			buf[i+1] = permChars[i]
		} else {
			buf[i+1] = '-'
		}
	}
	// setuid → owner 的 x 位变成 s（有 x）/ S（无 x），即 buf[3]
	// 注意：setuid/setgid/sticky 在 os.FileMode 里是高位的 os.ModeSetuid /
	// os.ModeSetgid / os.ModeSticky，不是 Linux perm 的 0o4000/0o2000/0o1000
	// （那俩是 SFTP 协议层的位，不要混）。
	if m&os.ModeSetuid != 0 {
		if buf[3] == 'x' {
			buf[3] = 's'
		} else {
			buf[3] = 'S'
		}
	}
	if m&os.ModeSetgid != 0 {
		if buf[6] == 'x' {
			buf[6] = 's'
		} else {
			buf[6] = 'S'
		}
	}
	if m&os.ModeSticky != 0 {
		if buf[9] == 'x' {
			buf[9] = 't'
		} else {
			buf[9] = 'T'
		}
	}
	return string(buf[:])
}
