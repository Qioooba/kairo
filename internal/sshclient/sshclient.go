// Package sshclient 提供受控的 SSH 连接与命令执行能力。
//
// 核心安全设计：
//   1. 不允许用户输入任意命令；
//   2. 所有命令由后端固定模板生成；
//   3. 远程目录/文件名只能来自配置白名单或前一步 ls 的结果；
//   4. 搜索关键词做严格转义，禁止 shell 元字符；
//   5. 每次执行带超时，防止长时间挂起；
//   6. 超时由客户端 ctx + 内部 timer 控制，不依赖服务器端 `timeout` 命令；
//   7. 支持 UTF-8 / GBK 编码（老 WebSphere / Oracle 常见 GBK）。
package sshclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
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
}

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
func Dial(ctx context.Context, srv Server, cred Credentials, timeout time.Duration) (*Client, error) {
	if strings.TrimSpace(srv.Host) == "" {
		return nil, errors.New("host 不能为空")
	}
	if srv.Port == 0 {
		srv.Port = 22
	}
	addr := net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))
	cfg := &ssh.ClientConfig{
		User:            srv.Username,
		Auth:            []ssh.AuthMethod{ssh.Password(cred.Password)},
		Timeout:         timeout,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 内网工具 + 不在配置里管 known_hosts，第一版可接受
	}

	dialDone := make(chan struct {
		c   *ssh.Client
		err error
	}, 1)
	go func() {
		// ssh.Dial 不支持 ctx，这里在另一个 goroutine 做，外部用 ctx 控制
		c, err := ssh.Dial("tcp", addr, cfg)
		dialDone <- struct {
			c   *ssh.Client
			err error
		}{c, err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("SSH 连接 %s 超时: %s", addr, SanitizeError(ctx.Err().Error()))
	case r := <-dialDone:
		if r.err != nil {
			return nil, fmt.Errorf("SSH 连接 %s 失败: %s", addr, SanitizeError(r.err.Error()))
		}
		return &Client{srv: srv, cre: cred, conn: r.c}, nil
	}
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
//   "ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain"
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
type safeWriter struct {
	w    *strings.Builder
	max  int
	wrot int
}

func (s *safeWriter) Write(p []byte) (int, error) {
	const cap = 8 * 1024 * 1024 // 8MB 上限，防止炸内存
	if s.wrot+len(p) > cap {
		remain := cap - s.wrot
		if remain > 0 {
			_, _ = s.w.Write(p[:remain])
			s.wrot += remain
		}
		_, _ = s.w.Write([]byte("\n...[truncated]..."))
		return len(p), nil
	}
	n, err := s.w.Write(p)
	s.wrot += n
	return n, err
}
