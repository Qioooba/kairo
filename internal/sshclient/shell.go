// Package sshclient — shell.go 提供交互式 shell 能力。
//
// 与 Run / Stream 的区别：
//   - Run / Stream：一次性命令（受控白名单），命令由后端模板生成
//   - Shell：开 PTY + 交互式 shell，stdin/stdout 直接转发给前端 xterm.js
//
// 这是 v0.10 新增能力（设计见 docs/SSH-TERMINAL-DESIGN.md），用于「SSH 终端」菜单。
// 与 Run/Stream 共用同一套 3 套 compat profile（Dial 已自动 fallback），不重复握手。
//
// 安全：
//   - 用户主动连自己的机器（不是工具代为下发命令），不违反 README「禁止任意命令执行」约束；
//   - 审计只记 start/end，不记命令内容（输入流太密，无意义）；
//   - 凭据走现有 resolveCreds，不接受前端 ws upgrade 帧里传 password。
package sshclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// ShellSession 封装一个交互式 shell 会话。
//
// 字段语义：
//   - Ssh：底层 *ssh.Session（用于 WindowChange / Signal / Close）
//   - Stdin：写到远端 shell stdin 的管道（前端按键经 ws binary 写到这里）
//   - Stdout：远端 shell stdout + stderr 合并的读取端（合并是惯例，xterm 不区分）
//   - Rows / Cols：当前 PTY 尺寸（resize 时更新）
//
// 生命周期由调用方管理：Close 会关闭底层 session + stdin pipe。
// 不要假设 Ssh.Close() 后 Stdin/Stdout 仍可读写。
type ShellSession struct {
	Ssh     *ssh.Session
	Stdin   io.WriteCloser
	Stdout  io.Reader
	Rows    int
	Cols    int
	encoding string // "utf-8" 或 "gbk"/"gb18030"
}

// gbkDecoder 包装一个 io.Reader，对 GBK 字节流做实时解码。
// 它内部缓冲未完成的字节（GBK 字符最多 2 字节），避免跨 Read() 边界截断字符。
type gbkDecoder struct {
	src   io.Reader
	buf   []byte // 缓冲未完成的字节
	enc   transform.Transformer
}

func newGBKDecoder(src io.Reader) *gbkDecoder {
	return &gbkDecoder{
		src: src,
		buf: make([]byte, 0, 4), // 最多缓冲 2 个未完成字节
		enc: simplifiedchinese.GBK.NewDecoder(),
	}
}

// Read 从 GBK 字节流读取，解码后返回 UTF-8 字节。
func (d *gbkDecoder) Read(p []byte) (int, error) {
	// 如果有残留字节，先消费
	if len(d.buf) > 0 {
		n := copy(p, d.buf)
		d.buf = d.buf[n:]
		if len(d.buf) == 0 {
			return n, nil
		}
	}
	// 从 src 读取更多字节
	tmp := make([]byte, 4096)
	for {
		nr, err := d.src.Read(tmp)
		if nr == 0 {
			if err == nil {
				continue
			}
			// src EOF 或错误，flush 缓冲
			if len(d.buf) > 0 {
				n := copy(p, d.buf)
				d.buf = d.buf[:0]
				if n > 0 {
					return n, err
				}
			}
			return 0, err
		}
		// 把新字节追加到缓冲
		d.buf = append(d.buf, tmp[:nr]...)
		// 尝试把整个缓冲转换（buf 里可能有未完成字符）
		r := transform.NewReader(bytes.NewReader(d.buf), d.enc)
		n, err := r.Read(p)
		if err == transform.ErrShortDst {
			// 输出缓冲区不够，这意味着 p 太小或 buf 有完整转换但输出没读完
			// 保留一个字节（可能是不完整字符）继续
			if len(d.buf) > 0 {
				d.buf = d.buf[len(d.buf)-1:]
			}
			return n, nil
		}
		if err == io.EOF {
			// 全部转换完成，可能还有 1 字节残留（半个 GBK 字符）
			// EOF 表示转换器已处理完所有输入
			// 检查 buf 是否还有未处理的字节
			if len(d.buf) > 0 {
				// buf 里可能有 1 字节残留
				if len(d.buf) == 1 && n > 0 {
					return n, nil
				}
			}
			return n, nil
		}
		if n > 0 {
			// 成功转换了一些字节，可能还有残留字符
			if len(d.buf) > 0 && n < len(p) {
				// 还有空间，尝试继续处理残留
				continue
			}
			return n, nil
		}
		// n == 0 且无特殊错误，循环继续
		if err != nil && err != io.EOF {
			break
		}
	}
	// 错误时尽量 flush 缓冲
	if len(d.buf) > 0 {
		n := copy(p, d.buf)
		d.buf = d.buf[:0]
		return n, nil
	}
	return 0, nil
}

// 默认 TERM 类型。xterm-256color 是事实标准，所有现代 sshd 都支持；
// 老 WebSphere / AIX 默认 xterm 也支持，256color 在老 terminfo 缺失时退化为 16 色，
// 不会握手失败。
const DefaultTERM = "xterm-256color"

// 默认环境变量。LANG/LC_ALL 设为 C.UTF-8（兼容性最好），让大多数发行版的 shell 输出 UTF-8。
// 老 AIX / Solaris 可能不识别 C.UTF-8，那时 Setenv 会失败 —— 失败就失败，不阻塞 shell 启动。
var defaultEnv = [][2]string{
	{"LANG", "C.UTF-8"},
	{"LC_ALL", "C.UTF-8"},
}

// Shell 在已连接的 *ssh.Client 上开一个交互式 PTY + shell。
//
// 参数：
//   - ctx：仅用于握手前的取消检查；shell 起来后 ctx 不影响（用 Close 退出）
//   - term：终端类型，空字符串走 DefaultTERM
//   - rows, cols：初始 PTY 尺寸（前端 fitAddon 算出）
//   - env：额外的环境变量（k1,v1,k2,v2,...），长度必须为偶数；可空
//   - encoding：输出编码，"utf-8"（默认）或 "gbk"/"gb18030"（老 WebSphere/Oracle 终端）
//
// 返回的 *ShellSession 由调用方负责 Close。
//
// 实现：
//  1. NewSession
//  2. Setenv 默认环境 + 用户 env（失败不阻塞）
//  3. RequestPty(term, rows, cols, modes)
//  4. StdoutPipe + StdinPipe（合并 stderr：ssh.Stdout 设置后再 RequestPty，远端会把 stderr 重定向到 stdout）
//  5. Shell()
//  6. 若 encoding 为 GBK，用 gbkDecoder 包装 stdout（实时解码避免多字节字符被截断）
//
// 老 sshd（6.2p2 / AIX）的兼容性：
//   - RequestPty 已被 x/crypto/ssh 兼容到所有标准 sshd；
//   - 6.2p2 默认接受 xterm-256color（terminfo 在大多数发行版都有）；
//   - 极少数老 AIX 不识别 256color，shell 仍能起来，只是颜色退化为 16 色 —— 不影响功能。
func (c *Client) Shell(ctx context.Context, term string, rows, cols int, env []string, encoding string) (*ShellSession, error) {
	if c == nil || c.conn == nil {
		return nil, errors.New("ssh 客户端未连接")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if term == "" {
		term = DefaultTERM
	}
	if rows <= 0 {
		rows = 24
	}
	if cols <= 0 {
		cols = 80
	}
	if encoding == "" {
		encoding = "utf-8"
	}

	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, fmt.Errorf("创建 session 失败: %w", err)
	}

	// 设置环境变量。Setenv 在多数 sshd 上是 best-effort（默认 sshd_config AcceptEnv 不允许任意变量），
	// 失败不阻塞 shell 启动 —— 用户的 ~/.bashrc 仍会按服务器端默认 locale 跑。
	for _, kv := range defaultEnv {
		_ = sess.Setenv(kv[0], kv[1])
	}
	for i := 0; i+1 < len(env); i += 2 {
		_ = sess.Setenv(env[i], env[i+1])
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1, // 输入回显（shell 标准行为）
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty(term, rows, cols, modes); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("请求 PTY 失败（老 sshd 可能不支持 %s）: %w", term, err)
	}

	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("stdin pipe 失败: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("stdout pipe 失败: %w", err)
	}
	// 让 stderr 也走到 stdout pipe（PTY 模式下 sshd 默认就合并，这里显式设置是双保险）。
	sess.Stderr = nil // StdoutPipe 已独占 stdout；PTY 模式下 stderr 自动合并到 stdout

	if err := sess.Shell(); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("启动 shell 失败: %w", err)
	}

	// 若 encoding 为 GBK/GB18030，用解码器包装 stdout
	var stdoutReader io.Reader = stdout
	if strings.ToLower(encoding) == "gbk" || strings.ToLower(encoding) == "gb18030" {
		stdoutReader = newGBKDecoder(stdout)
	}

	return &ShellSession{
		Ssh:      sess,
		Stdin:    stdin,
		Stdout:   stdoutReader,
		Rows:     rows,
		Cols:     cols,
		encoding: encoding,
	}, nil
}

// WindowChange 调整 PTY 尺寸（前端 fitAddon 触发）。
// 老 sshd 不支持 window-change 请求时会返回 error，调用方忽略即可（不影响 shell 本身）。
func (s *ShellSession) WindowChange(rows, cols int) error {
	if s == nil || s.Ssh == nil {
		return errors.New("shell session 未初始化")
	}
	if rows <= 0 || cols <= 0 {
		return errors.New("rows/cols 必须为正数")
	}
	if err := s.Ssh.WindowChange(rows, cols); err != nil {
		return err
	}
	s.Rows = rows
	s.Cols = cols
	return nil
}

// Signal 给远端 shell 发信号（工具栏「Ctrl+C」按钮用 SIGINT）。
// 老 sshd 不支持 signal 请求时返回 error，调用方忽略即可。
func (s *ShellSession) Signal(sig ssh.Signal) error {
	if s == nil || s.Ssh == nil {
		return errors.New("shell session 未初始化")
	}
	return s.Ssh.Signal(sig)
}

// Wait 等待 shell 退出，返回退出码 + error。
// 调用方应在两个转发 goroutine 都结束后调 Wait 拿退出码。
func (s *ShellSession) Wait() (int, error) {
	if s == nil || s.Ssh == nil {
		return -1, errors.New("shell session 未初始化")
	}
	err := s.Ssh.Wait()
	code := 0
	if err != nil {
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitStatus()
			err = nil // 退出码非 0 不算 error
		}
	}
	return code, err
}

// Close 关闭 shell session。
//
// 顺序：先关 stdin（让 shell 收到 EOF）→ Wait（最多 1s）→ 强制 Close。
// 这样能尽量让 shell 优雅退出，避免在老 sshd 上留下 zombie session。
func (s *ShellSession) Close() error {
	if s == nil || s.Ssh == nil {
		return nil
	}
	if s.Stdin != nil {
		_ = s.Stdin.Close()
	}
	// 给 shell 1s 优雅退出，超时直接 Close。
	done := make(chan struct{})
	go func() {
		_, _ = s.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
	}
	return s.Ssh.Close()
}
