// Package sftpclient 在已建立的 SSH 连接上做受控 SFTP 文件读取。
//
// 第一版只提供"读文件到本地"和 stat，因为 WebSphere 日志助手只需要下载。
// v0.3 起扩展支持 ReadDir / Stat（用于"文件浏览器"页）。
//
// 不允许写远程文件、不允许删远程文件、不允许改权限。
package sftpclient

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// Client 包装一个 SFTP 客户端
type Client struct {
	b sftpBackend
}

// New 在已有 SSH 连接上创建 SFTP 客户端
func New(conn *ssh.Client) (*Client, error) {
	sc, err := sftp.NewClient(conn)
	if err != nil {
		return nil, fmt.Errorf("创建 sftp 客户端失败: %w", err)
	}
	return &Client{b: &realSftpBackend{c: sc}}, nil
}

// newWithBackend 内部构造函数，允许测试注入 mock backend。
func newWithBackend(b sftpBackend) *Client {
	return &Client{b: b}
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

// DownloadFile 把 remotePath 下载到 localPath
//
// remotePath 必须由调用方做过白名单校验。
func (c *Client) DownloadFile(remotePath, localPath string) (int64, error) {
	return c.download(remotePath, localPath, nil)
}

// DownloadFileWithProgress 同 DownloadFile，但通过 progress 回调上报下载进度。
//
// 参数 progress 可以为 nil，此时行为与 DownloadFile 完全一致。
// 回调语义：每写入 progressInterval 字节触发一次；最后完成时再补一次 (written, total) 保证前端能到 100%。
// total 来自 sftpFile.Stat()，若 Stat 失败则为 -1（前端按 indeterminate 进度条处理）。
// 回调可能在 io.Copy 路径中被并发触发，调用方需自行同步。
func (c *Client) DownloadFileWithProgress(remotePath, localPath string, progress func(written, total int64)) (int64, error) {
	return c.download(remotePath, localPath, progress)
}

// progressInterval 进度回调最小间隔（字节）。64KB 对内网 SFTP
// 来说粒度足够细，1GB 文件约 16384 次回调。配合下面的
// progressMinInterval 时间节流，最终频率 ≈ 10 Hz，避免刷爆前端。
const progressInterval = 64 * 1024

// progressMinInterval 进度回调最小时间间隔。100ms ≈ 10 Hz，
// 人眼能感觉到流畅但不会刷爆浏览器渲染。
const progressMinInterval = 100 * time.Millisecond

func (c *Client) download(remotePath, localPath string, progress func(written, total int64)) (int64, error) {
	if c == nil || c.b == nil {
		return 0, fmt.Errorf("sftp 客户端未连接")
	}
	src, err := c.b.Open(remotePath)
	if err != nil {
		return 0, fmt.Errorf("打开远程文件失败: %w", err)
	}
	defer src.Close()

	// 优先拿 total（让前端能算百分比）。Stat 失败不致命，按 -1 走。
	var total int64 = -1
	if info, statErr := src.Stat(); statErr == nil {
		total = info.Size()
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return 0, fmt.Errorf("创建本地目录失败: %w", err)
	}
	dst, err := os.OpenFile(localPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, fmt.Errorf("创建本地文件失败: %w", err)
	}
	defer dst.Close()

	pw := &progressWriter{w: dst, total: total, progress: progress}
	n, err := io.Copy(pw, src)
	if err != nil {
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
