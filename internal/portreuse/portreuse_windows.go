//go:build windows

package portreuse

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// handlePortOccupied Windows 平台：检测占用端口的进程，自动杀残留 OpsToolbox.exe，
// 其它进程弹框询问用户。
func handlePortOccupied(port int, selfExePath string, selfPID int) Result {
	res := Result{Decision: DecisionNone}

	pid, procName, procExe, ok := findListenerOnPort(port)
	if !ok {
		res.Reason = fmt.Sprintf("netstat 未找到占用端口 %d 的监听进程（端口可能已被释放）", port)
		return res
	}
	res.PID = pid
	res.ProcName = procName

	log.Printf("[portreuse] 端口 %d 被占用: PID=%d name=%s exe=%s", port, pid, procName, procExe)

	if pid == selfPID {
		res.Reason = fmt.Sprintf("占用端口的 PID=%d 等于当前进程（可能 Windows PID 复用），不杀", pid)
		return res
	}

	if strings.EqualFold(procName, "OpsToolbox.exe") {
		if procExe != "" && selfExePath != "" {
			sameExe, err := sameFilePath(procExe, selfExePath)
			if err == nil && !sameExe {
				res.Reason = fmt.Sprintf(
					"进程名是 OpsToolbox.exe 但路径不同 (占用方=%s / 当前=%s)，疑似别人的工具，不杀",
					procExe, selfExePath)
				return res
			}
		}
		log.Printf("[portreuse] 检测到残留 OpsToolbox.exe (PID=%d)，自动 taskkill", pid)
		if err := taskKill(pid); err != nil {
			res.Reason = fmt.Sprintf("taskkill PID=%d 失败: %v", pid, err)
			return res
		}
		res.Decision = DecisionKilled
		res.Reason = fmt.Sprintf("已 taskkill 残留 OpsToolbox.exe (PID=%d)", pid)
		return res
	}

	log.Printf("[portreuse] 端口被未知进程占用: PID=%d name=%s，弹窗询问", pid, procName)
	ok = promptKillOtherProcess(pid, procName, port)
	if !ok {
		res.Reason = fmt.Sprintf("用户拒绝杀掉占用进程 (PID=%d name=%s)", pid, procName)
		return res
	}
	if err := taskKill(pid); err != nil {
		res.Reason = fmt.Sprintf("taskkill PID=%d 失败: %v", pid, err)
		return res
	}
	res.Decision = DecisionKilled
	res.Reason = fmt.Sprintf("已 taskkill %s (PID=%d)", procName, pid)
	return res
}

// findListenerOnPort 调 netstat -ano | findstr :PORT 找监听进程。
func findListenerOnPort(port int) (int, string, string, bool) {
	cmd := exec.Command("netstat", "-ano", "-p", "TCP")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		log.Printf("[portreuse] netstat 执行失败: %v", err)
		return 0, "", "", false
	}

	portStr := ":" + strconv.Itoa(port) + " "
	listeningPID := 0
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, portStr) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		if !strings.EqualFold(fields[3], "LISTENING") {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil {
			continue
		}
		listeningPID = pid
		break
	}
	if listeningPID == 0 {
		return 0, "", "", false
	}

	cmd = exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", listeningPID),
		"/FO", "LIST", "/V")
	out.Reset()
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		log.Printf("[portreuse] tasklist 查 PID=%d 失败（进程可能已退出）: %v", listeningPID, err)
		return listeningPID, "", "", true
	}

	procName, procExe := parseTasklistOutput(out.String())
	return listeningPID, procName, procExe, true
}

// parseTasklistOutput 解析 tasklist /FO LIST /V 输出。
func parseTasklistOutput(s string) (name, exe string) {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:idx]))
		val := strings.TrimSpace(line[idx+1:])
		switch key {
		case "image name", "映像名称", "映像名":
			if name == "" {
				name = val
			}
		case "image path", "映像路径":
			if exe == "" {
				exe = val
			}
		}
	}
	return
}

// taskKill 调 taskkill /F /T /PID <pid>。
func taskKill(pid int) error {
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w (output: %s)", err, strings.TrimSpace(out.String()))
	}
	log.Printf("[portreuse] taskkill /F /T /PID %d 成功: %s", pid, strings.TrimSpace(out.String()))
	return nil
}

// promptKillOtherProcess 弹一个 Windows 消息框问用户是否杀进程。
func promptKillOtherProcess(pid int, name string, port int) bool {
	msg := fmt.Sprintf(
		"OpsToolbox 想使用端口 %d，但被其它进程占用。\r\n\r\n"+
			"占用方：%s (PID=%d)\r\n\r\n"+
			"按 Y 杀掉该进程（注意：会丢失该进程未保存的数据）。\r\n"+
			"按 N 取消，OpsToolbox 退出。\r\n",
		port, name, pid)

	notifyMsg(pid, name, port)

	log.Printf("[portreuse] 等待用户在当前终端输入 Y/N（90 秒不输默认 N）……")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "===============================================================")
	fmt.Fprintln(os.Stderr, msg)
	fmt.Fprintln(os.Stderr, "===============================================================")
	fmt.Fprint(os.Stderr, "请输入 Y/N: ")

	type result struct {
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		var b [1]byte
		n, err := os.Stdin.Read(b[:])
		if err != nil || n == 0 {
			done <- result{false, err}
			return
		}
		ch := strings.ToUpper(string(b[0]))
		done <- result{ch == "Y" || ch == "\r" || ch == "\n", nil}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			log.Printf("[portreuse] 读 stdin 失败: %v（默认不杀）", r.err)
			return false
		}
		if !r.ok {
			fmt.Fprintln(os.Stderr, "→ 已取消（不杀）")
		} else {
			fmt.Fprintln(os.Stderr, "→ 用户同意杀")
		}
		return r.ok
	case <-time.After(90 * time.Second):
		fmt.Fprintln(os.Stderr, "→ 90 秒未响应，默认不杀")
		return false
	}
}

// notifyMsg 用 Windows msg.exe 通知其它 session。
func notifyMsg(pid int, name string, port int) {
	msg := fmt.Sprintf("OpsToolbox needs port %d, occupied by %s (PID=%d). Check the launching console for Y/N prompt.", port, name, pid)
	cmd := exec.Command("msg", "*", "/TIME:60", msg)
	_ = cmd.Run()
}

// sameFilePath 比对两条 Windows 路径是否指同一个文件。
func sameFilePath(a, b string) (bool, error) {
	if a == "" || b == "" {
		return false, nil
	}
	ca := normalizePath(a)
	cb := normalizePath(b)
	if ca == cb {
		return true, nil
	}
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ai, bi), nil
}

func normalizePath(p string) string {
	p = filepath.Clean(p)
	p = strings.ReplaceAll(p, `\`, "/")
	return strings.ToLower(p)
}
