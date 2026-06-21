// Package sftpclient 在已建立的 SSH 连接上做受控 SFTP 文件读取。
//
// 第一版只提供"读文件到本地"和 stat，因为 WebSphere 日志助手只需要下载。
// 不允许写远程文件、不允许删远程文件、不允许改权限。
package sftpclient

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// sftpFile 是对 *sftp.File 的最小抽象（io.Reader + io.Closer），
// 方便测试时注入假实现。
type sftpFile interface {
	io.Reader
	io.Closer
}

// sftpBackend 是对 *sftp.Client 的最小抽象，
// 方便测试时注入假实现。
type sftpBackend interface {
	Open(path string) (sftpFile, error)
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
	if c == nil || c.b == nil {
		return 0, fmt.Errorf("sftp 客户端未连接")
	}
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

	n, err := io.Copy(dst, src)
	if err != nil {
		return n, fmt.Errorf("下载过程中断: %w", err)
	}
	return n, nil
}
