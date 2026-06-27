//go:build !windows

package portreuse

import (
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

// handlePortOccupied macOS/Linux 平台：使用 lsof 检测端口占用，给出友好提示，
// 不自动强杀进程（避免误杀用户的其他程序）。
func handlePortOccupied(port int, selfExePath string, selfPID int) Result {
	res := Result{Decision: DecisionNone}

	pid, procName, ok := findListenerOnPort(port)
	if !ok {
		res.Reason = fmt.Sprintf("未找到占用端口 %d 的监听进程（端口可能已被释放）", port)
		return res
	}
	res.PID = pid
	res.ProcName = procName

	log.Printf("[portreuse] 端口 %d 被占用: PID=%d name=%s", port, pid, procName)

	selfExeBase := ""
	if selfExePath != "" {
		if idx := strings.LastIndex(selfExePath, "/"); idx >= 0 {
			selfExeBase = selfExePath[idx+1:]
		} else {
			selfExeBase = selfExePath
		}
	}

	if pid == selfPID {
		res.Reason = fmt.Sprintf("占用端口 %d 的 PID=%d 是当前进程自身（可能是文件描述符未释放）", port, pid)
		return res
	}

	if selfExeBase != "" && procName == selfExeBase {
		res.Reason = fmt.Sprintf(
			"端口 %d 被残留的 %s 进程占用 (PID=%d)。\n"+
				"请手动执行以下命令释放端口：\n"+
				"  kill %d\n"+
				"如进程未响应，可强制终止：\n"+
				"  kill -9 %d",
			port, procName, pid, pid, pid)
		return res
	}

	res.Reason = fmt.Sprintf(
		"端口 %d 被其它进程占用 (PID=%d name=%s)。\n"+
			"请手动释放该端口后重试，可执行：\n"+
			"  lsof -i :%d       # 查看占用详情\n"+
			"  kill %d           # 终止进程",
		port, pid, procName, port, pid)
	return res
}

// findListenerOnPort 使用 lsof 命令查找监听指定端口的进程。
// macOS/Linux 通用：lsof -nP -iTCP:PORT -sTCP:LISTEN -F pc
// 使用 -F 选项获取机器可读格式：
//
//	p<PID>   进程 ID
//	c<COMM>  进程命令名（完整，不截断）
//
// lsof 退出码：0=找到，1=未找到（端口空闲），其它=真正错误。
func findListenerOnPort(port int) (int, string, bool) {
	cmd := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-F", "pc")
	var out bytes.Buffer
	cmd.Stdout = &out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return 0, "", false
		}
		log.Printf("[portreuse] lsof 执行失败: %v (stderr: %s)", err, strings.TrimSpace(stderr.String()))
		return 0, "", false
	}

	lines := strings.Split(out.String(), "\n")
	var pid int
	var procName string
	for _, line := range lines {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "p"):
			p, err := strconv.Atoi(line[1:])
			if err == nil {
				pid = p
			}
		case strings.HasPrefix(line, "c"):
			procName = line[1:]
		}
		if pid != 0 && procName != "" {
			return pid, procName, true
		}
	}

	return 0, "", false
}
