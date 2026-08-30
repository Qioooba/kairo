package schedtask

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf8"
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
	if out, err := decodePlatformOutput([]byte(raw)); err == nil {
		return out
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
// 超时或上层取消时会终止完整进程树。平台细节由 process_*.go 隔离：
// Windows 使用 Job Object（无法加入 Job 时退回 taskkill /T），Unix 使用进程组。
func run(ctx context.Context, t *Task) runResult {
	name, args := shellCommand(t.Command)
	tctx, cancel := context.WithTimeout(ctx, t.Timeout())
	defer cancel()
	if tctx.Err() != nil {
		return runResult{
			exitCode: -1,
			status:   StatusCanceled,
			output:   "(任务已取消，未启动进程)",
		}
	}

	// 不使用 exec.CommandContext：它只杀直接子进程，无法保证 cmd /C、sh -c
	// 派生的整棵进程树被回收。managedCommand 统一负责 start/wait/kill-tree。
	cmd := exec.Command(name, args...)
	if dir := strings.TrimSpace(t.WorkDir); dir != "" {
		cmd.Dir = dir
	}
	out := newTailBuffer(outputKeepBytes)
	cmd.Stdout = out
	cmd.Stderr = out

	proc, err := startManagedCommand(cmd)
	if err != nil {
		return runResult{
			exitCode: -1,
			status:   StatusFailed,
			output:   "(启动失败) " + err.Error(),
			err:      err,
		}
	}
	defer proc.Close()

	waitCh := make(chan error, 1)
	go func() { waitCh <- proc.Wait() }()

	var canceled bool
	select {
	case err = <-waitCh:
	case <-tctx.Done():
		canceled = true
		killErr := proc.KillTree()
		err = <-waitCh // 进程树被回收后管道关闭，Wait 应立即返回
		if killErr != nil && err == nil {
			err = killErr
		}
	}
	res := runResult{exitCode: 0, output: decodeOutput(out.String())}

	if canceled {
		res.exitCode = -1
		switch {
		case errors.Is(tctx.Err(), context.DeadlineExceeded):
			res.status = StatusTimeout
			if res.output == "" {
				res.output = "(执行超时，进程树已被终止)"
			}
		default:
			res.status = StatusCanceled
			if res.output == "" {
				res.output = "(任务已取消，进程树已被终止)"
			}
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
