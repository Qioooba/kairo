package schedtask

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// outputKeepBytes 输出保留上限：只留尾部 32KB。
// git pull / svn update 输出一般几 KB；构建脚本可能几百 KB，留尾部足够排错。
const outputKeepBytes = 32 * 1024

// tailBuffer 只保留尾部 N 字节的 Writer（环形语义，写满后从头部丢弃）。
type tailBuffer struct {
	buf []byte
	cap int
}

func newTailBuffer(cap int) *tailBuffer {
	return &tailBuffer{buf: make([]byte, 0, cap), cap: cap}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n >= b.cap {
		// 单次写入就超过上限：只留尾巴
		b.buf = append(b.buf[:0], p[n-b.cap:]...)
		return n, nil
	}
	over := len(b.buf) + n - b.cap
	if over > 0 {
		b.buf = b.buf[over:]
	}
	b.buf = append(b.buf, p...)
	return n, nil
}

func (b *tailBuffer) String() string { return string(b.buf) }

// shellCommand 按平台选 shell。Windows 用 cmd /C（.bat/.cmd 也能跑），
// 其余用 sh -c（macOS / Linux 都有 POSIX sh）。
func shellCommand(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", command}
	}
	return "sh", []string{"-c", command}
}

// decodeOutput 尽量转成可读 UTF-8：
//   - 已是合法 UTF-8 → 原样返回（macOS / Linux 常态）；
//   - Windows 中文系统 cmd 输出是 GBK → 转 UTF-8；
//   - 转换失败 → 原样返回（宁可乱码不丢内容）。
func decodeOutput(raw string) string {
	if utf8.ValidString(raw) {
		return raw
	}
	if runtime.GOOS == "windows" {
		out, _, err := transform.String(simplifiedchinese.GBK.NewDecoder(), raw)
		if err == nil {
			return out
		}
	}
	return raw
}

// runResult 一次执行的原始结果（manager 负责转成 RunRecord）。
type runResult struct {
	exitCode int
	status   string // StatusSuccess / StatusFailed / StatusTimeout
	output   string // 合并输出尾部（已转码 + 截断）
	err      error  // 启动失败（非 nil 时 exitCode = -1）
}

// run 同步执行一次任务。调用方负责 goroutine 与超时以外的上下文。
//
// 超时：context.WithTimeout 到期后 CommandContext 会 kill 进程。
// 注意：cmd /C 派生的孙进程可能逃逸 kill（Windows 进程树问题），
// 对"git/svn/脚本"这类单进程场景够用，不额外引入 job object 复杂度。
func run(ctx context.Context, t *Task) runResult {
	name, args := shellCommand(t.Command)
	tctx, cancel := context.WithTimeout(ctx, t.Timeout())
	defer cancel()

	cmd := exec.CommandContext(tctx, name, args...)
	if dir := strings.TrimSpace(t.WorkDir); dir != "" {
		cmd.Dir = dir
	}
	out := newTailBuffer(outputKeepBytes)
	cmd.Stdout = out
	cmd.Stderr = out

	start := time.Now()
	err := cmd.Run()
	_ = start // 耗时由 manager 统一记
	res := runResult{exitCode: 0, output: decodeOutput(out.String())}

	if tctx.Err() == context.DeadlineExceeded {
		res.status = StatusTimeout
		res.exitCode = -1
		if res.output == "" {
			res.output = "(执行超时，进程已被终止)"
		}
		return res
	}
	if err != nil {
		res.status = StatusFailed
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.exitCode = exitErr.ExitCode()
		} else {
			// 启动失败（命令不存在 / 目录不存在等）：错误信息写进 output，
			// 避免历史里只见"failed"看不到原因。
			res.exitCode = -1
			res.err = err
			if res.output == "" {
				res.output = "(启动失败) " + err.Error()
			} else {
				res.output = "(启动失败) " + err.Error() + "\n" + res.output
			}
		}
		return res
	}
	res.status = StatusSuccess
	return res
}
