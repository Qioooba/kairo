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
	"kairo/internal/sysutil"
)

// handlePortOccupied Windows 平台：检测占用端口的进程，自动杀残留 Kairo.exe，
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

	if strings.EqualFold(procName, "Kairo.exe") {
		if procExe != "" && selfExePath != "" {
			sameExe, err := sameFilePath(procExe, selfExePath)
			if err == nil && !sameExe {
				res.Reason = fmt.Sprintf(
					"进程名是 Kairo.exe 但路径不同 (占用方=%s / 当前=%s)，疑似别人的工具，不杀",
					procExe, selfExePath)
				return res
			}
		}
		log.Printf("[portreuse] 检测到残留 Kairo.exe (PID=%d)，自动 taskkill", pid)
		if err := taskKill(pid); err != nil {
			res.Reason = fmt.Sprintf("taskkill PID=%d 失败: %v", pid, err)
			return res
		}
		res.Decision = DecisionKilled
		res.Reason = fmt.Sprintf("已 taskkill 残留 Kairo.exe (PID=%d)", pid)
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
	sysutil.HideConsoleWindow(cmd) // 双击 GUI exe 启动时避免弹 cmd 黑框
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
	sysutil.HideConsoleWindow(cmd)
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
	sysutil.HideConsoleWindow(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w (output: %s)", err, strings.TrimSpace(out.String()))
	}
	log.Printf("[portreuse] taskkill /F /T /PID %d 成功: %s", pid, strings.TrimSpace(out.String()))
	return nil
}

// MessageBox 返回值常量（vendor/golang.org/x/sys/windows 未导出 IDYES/IDNO）。
const (
	idYes int32 = 6
	idNo  int32 = 7
)

// isGUIMode 检测当前进程是否在没有终端的情况下运行（GUI 双击启动场景）。
// os.Stdin 不是字符设备（终端）时，os.Stdin.Read 会立即返回 EOF，
// 没法用 stdin 收 Y/N，必须改用原生 MessageBox 弹窗。
func isGUIMode() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		// Stat 失败保守按 GUI 模式处理（弹 MessageBox 比干等 stdin 安全）
		return true
	}
	return fi.Mode()&os.ModeCharDevice == 0
}

// promptKillOtherProcess 弹一个对话框问用户是否杀进程。
//   - GUI 模式（双击启动，无 stdin）：用 windows.MessageBox 原生弹窗
//   - 终端模式：保留现有 stdin.Read Y/N 逻辑
func promptKillOtherProcess(pid int, name string, port int) bool {
	msg := fmt.Sprintf(
		"Kairo 想使用端口 %d，但被其它进程占用。\r\n\r\n"+
			"占用方：%s (PID=%d)\r\n\r\n"+
			"按 Y 杀掉该进程（注意：会丢失该进程未保存的数据）。\r\n"+
			"按 N 取消，Kairo 退出。\r\n",
		port, name, pid)

	// GUI 模式：双击启动时 stdin 是 EOF，os.Stdin.Read 立即返回，
	// 没法用 stdin 收 Y/N；notifyMsg 用 msg.exe 弹的窗也没法回传用户输入。
	// 改用 windows.MessageBox 原生对话框，阻塞等待用户点击 Yes/No。
	if isGUIMode() {
		return promptKillViaMessageBox(pid, name, port, msg)
	}

	// 终端模式：保留现有 stdin.Read 逻辑
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

// promptKillViaMessageBox 在 GUI 模式下用 windows.MessageBox 弹原生对话框。
// 阻塞等待用户点击 Yes/No，没有超时（MessageBox 默认无限等待，符合 GUI 交互习惯）。
func promptKillViaMessageBox(pid int, name string, port int, msg string) bool {
	title := fmt.Sprintf("Kairo - 端口 %d 被占用", port)
	log.Printf("[portreuse] GUI 模式：弹 MessageBox 询问用户 (PID=%d name=%s)", pid, name)

	// UTF16PtrFromString 在字符串含 NUL 字节时返回 error（比已废弃的
	// StringToUTF16Ptr 安全，后者遇 NUL 会 panic）。进程名来自 tasklist 输出，
	// 理论上可能含特殊字符，这里稳妥处理。
	textPtr, err := windows.UTF16PtrFromString(msg)
	if err != nil {
		log.Printf("[portreuse] UTF16PtrFromString(msg) 失败: %v（默认不杀）", err)
		return false
	}
	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		log.Printf("[portreuse] UTF16PtrFromString(title) 失败: %v（默认不杀）", err)
		return false
	}
	// MB_SETFOREGROUND：双击启动时进程没有可见窗口，MessageBox 默认可能不
	// 在前台，加这个 flag 确保用户能看到弹窗。
	ret, err := windows.MessageBox(0, textPtr, titlePtr,
		windows.MB_YESNO|windows.MB_ICONQUESTION|windows.MB_SETFOREGROUND)
	if err != nil {
		log.Printf("[portreuse] MessageBox 失败: %v（默认不杀）", err)
		return false
	}
	if ret == idYes {
		log.Printf("[portreuse] 用户在 MessageBox 点击了 是 (PID=%d)", pid)
		return true
	}
	log.Printf("[portreuse] 用户在 MessageBox 点击了 否 (PID=%d)", pid)
	return false
}

// notifyMsg 用 Windows msg.exe 通知其它 session。
func notifyMsg(pid int, name string, port int) {
	msg := fmt.Sprintf("Kairo needs port %d, occupied by %s (PID=%d). Check the launching console for Y/N prompt.", port, name, pid)
	cmd := exec.Command("msg", "*", "/TIME:60", msg)
	sysutil.HideConsoleWindow(cmd)
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
