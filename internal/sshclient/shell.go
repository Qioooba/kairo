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
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
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
//
// 早期自写 buffer 的版本有严重 bug：每次 Read 都新建 transform.Reader 会丢 transformer
// 内部状态；EOF 后的「d.buf 残留」判断让循环一直打转 src.Read，导致要么没有数据送达
// 前端，要么（src 真正 EOF 后）把 EOF 错误一并返回给消费者 —— 用户端表现就是
// 「切到 GBK 就重连不上」。
//
// v0.10 修复：直接复用 golang.org/x/text/transform 包提供的 NewReader。它内部已经按 GBK
// 字符宽度（双字节）正确维护 incomplete 序列缓冲；不再需要手写额外缓冲层。
type gbkDecoder struct {
	r io.Reader // = transform.NewReader(src, decoder)
}

func newGBKDecoder(src io.Reader) *gbkDecoder {
	return &gbkDecoder{r: transform.NewReader(src, simplifiedchinese.GBK.NewDecoder())}
}

// Read 把内部 transform.Reader 当成普通 io.Reader 透传 ——
// 它自己负责 multi-byte GBK 字符跨 Read 边界的拼接 + 非法字节的 replacement 字符替换。
func (d *gbkDecoder) Read(p []byte) (int, error) { return d.r.Read(p) }

// gbkStdinEncoder 把前端发来的 UTF-8 字节流实时转成 GBK 再写到远端 shell stdin。
//
// 背景（乱码文件夹进不去）：
//   - 老 WebSphere / AIX 中文目录名在磁盘上是 GBK 字节，shell 在 zh_CN.GBK locale 下
//     期望收到 GBK 字节；
//   - 前端 xterm.js 经 TextEncoder 发的永远是 UTF-8；
//   - 之前 Shell() 只对 stdout 做了 GBK→UTF-8（能看），没对 stdin 做 UTF-8→GBK（不能输），
//     导致 GBK 模式下 `ls` 看着正常，但 `cd 中文目录` 发的是 UTF-8 字节，远端报
//     "No such file or directory"，用户观感就是"乱码文件夹进不去"。
//
// 实现：transform.NewWriter 内部维护不完整 UTF-8 序列缓冲，跨 Write 边界不会截断汉字。
// Write 返回的是消费掉的源字节数（符合 io.Writer 语义）；不可编码字符（如 emoji）
// 按 x/text 默认替换为 '?'，不阻断终端流。并发安全：wsReader 与 cwd 重试注入会并发
// Write，用 mutex 串行化（底层 ssh stdin pipe 本身也不保证并发写安全）。
type gbkStdinEncoder struct {
	mu  sync.Mutex
	tw  *transform.Writer
	raw io.WriteCloser
}

func newGBKStdinEncoder(dst io.WriteCloser) *gbkStdinEncoder {
	return &gbkStdinEncoder{
		tw:  transform.NewWriter(dst, simplifiedchinese.GBK.NewEncoder()),
		raw: dst,
	}
}

// Write 把 UTF-8 源字节转 GBK 后写到底层。ASCII（含 \r \n ESC 序列）逐字节直通，
// 与 GBK 完全兼容，所以 cwd 查询注入的纯 ASCII 命令不受影响。
func (e *gbkStdinEncoder) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	n, err := e.tw.Write(p)
	if err != nil {
		// 编码器出错（如非法 UTF-8）时不阻断终端：已转换部分已写入，
		// 这里按"全部消费"返回，避免上层 wsReader 误判 stdin 断开而关会话。
		return len(p), nil
	}
	// transform.Writer 正常时 n == len(p)；防御性兜底。
	if n != len(p) {
		return len(p), nil
	}
	return n, nil
}

// Close 关闭底层 stdin（让远端 shell 收到 EOF），不额外 flush ——
// transform.Writer 无缓冲残留（不完整序列只留在内存，下次 Write 会续上），
// 关会话时残留半个汉字直接丢弃是正确行为。
func (e *gbkStdinEncoder) Close() error { return e.raw.Close() }

// isGBKEncoding 判断是否为 GBK 系编码（大小写/空格不敏感，gbk/gb18030 等价）。
func isGBKEncoding(enc string) bool {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "gbk", "gb18030":
		return true
	default:
		return false
	}
}

// encodeUTF8ToGBK 一次性把 UTF-8 字节转 GBK（单测/工具用，流式路径走 gbkStdinEncoder）。
// 不可编码字符替换为 '?'，永不返回 error（终端场景不断流优先）。
func encodeUTF8ToGBK(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), b)
	if err != nil {
		return b
	}
	return out
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

// gbkEnv 是 GBK 模式下的 locale：远端 shell 按 GBK 解释输入字节、按 GBK 输出，
// 与 stdin 编码器（UTF-8→GBK）+ stdout 解码器（GBK→UTF-8）配对，避免两边各说各话。
// zh_CN.GBK 在老 WebSphere / AIX 上最通用；不识别时 Setenv 失败也不阻塞（best-effort）。
var gbkEnv = [][2]string{
	{"LANG", "zh_CN.GBK"},
	{"LC_ALL", "zh_CN.GBK"},
}

// Shell 在已连接的 *ssh.Client 上开一个交互式 PTY + shell。
//
// 参数：
//   - ctx：仅用于握手前的取消检查；shell 起来后 ctx 不影响（用 Close 退出）
//   - term：终端类型，空字符串走 DefaultTERM
//   - rows, cols：初始 PTY 尺寸（前端 fitAddon 算出）
//   - env：额外的环境变量（k1,v1,k2,v2,...），长度必须为偶数；可空
//   - encoding：双向编码，"utf-8"（默认）或 "gbk"/"gb18030"（老 WebSphere/Oracle 终端）。
//     gbk 时：stdin 做 UTF-8→GBK（能输中文），stdout 做 GBK→UTF-8（能看中文），
//     LANG/LC_ALL 切 zh_CN.GBK 与远端对齐。
//
// 返回的 *ShellSession 由调用方负责 Close。
//
// 实现：
//  1. NewSession
//  2. Setenv 默认环境 + 用户 env（失败不阻塞）
//  3. RequestPty(term, rows, cols, modes)
//  4. StdoutPipe + StdinPipe（合并 stderr：ssh.Stdout 设置后再 RequestPty，远端会把 stderr 重定向到 stdout）
//  5. Shell()
//  6. 若 encoding 为 GBK：stdout 用 gbkDecoder 包装（GBK→UTF-8），
//     stdin 用 gbkStdinEncoder 包装（UTF-8→GBK），双向打通中文目录名
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
	// GBK 模式用 zh_CN.GBK（与 stdin 编码器/stdout 解码器配对），UTF-8 模式用 C.UTF-8。
	useGBK := isGBKEncoding(encoding)
	localeEnv := defaultEnv
	if useGBK {
		localeEnv = gbkEnv
	}
	for _, kv := range localeEnv {
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

	// 若 encoding 为 GBK/GB18030：stdout 做 GBK→UTF-8（能看），stdin 做 UTF-8→GBK（能输）。
	// 两边必须配对，否则 `ls` 看着正常但 `cd 中文目录` 发的字节对不上，表现就是"乱码文件夹进不去"。
	var stdoutReader io.Reader = stdout
	var stdinWriter io.WriteCloser = stdin
	if useGBK {
		stdoutReader = newGBKDecoder(stdout)
		stdinWriter = newGBKStdinEncoder(stdin)
	}

	return &ShellSession{
		Ssh:      sess,
		Stdin:    stdinWriter,
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
