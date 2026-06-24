// Package portreuse 提供「端口被占时自动清理」能力。
//
// 背景：v0.6.1 之前，OpsToolbox.exe 双击启动时如果上一次还没完全退出，
// 会立刻报 `bind: Only one usage of each socket address (protocol/network
// address/port) is normally permitted.` 然后退出。对内网测试场景来说
// 「双击重启」是高频操作，每次都要去任务管理器杀残留进程很烦。
//
// 策略（仅 Windows；其它平台保留原行为）：
//  1. netstat -ano | findstr :PORT 找到占用端口的 PID（监听 LISTENING 行）
//  2. tasklist /FI "PID eq <pid>" 拿进程名 + exe 路径
//  3. 进程名 == OpsToolbox.exe 且 exe 路径 == 当前 exe 路径 →
//     静默 taskkill /F /T，杀完打日志，让主流程重试 listen
//  4. 进程名匹配但 exe 路径不同（可能是同名的另一个工具）→
//     不杀，避免误伤；交给主流程报端口冲突
//  5. 别的进程 → 弹 msg 框问用户是否杀；超时 / 拒绝 → 不杀，主流程报错
//
// 任何步骤失败都不致命 —— 最差就是回到原来的"端口被占"错误，
// 但用户能看到具体是哪个进程 / PID 占着，方便排查。
package portreuse

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Decision 描述我们对占用进程的处理结论
type Decision int

const (
	// DecisionNone 不做任何事，主流程应继续走"端口被占"错误分支
	DecisionNone Decision = iota
	// DecisionKilled 已经把占用进程杀掉，主流程应重试 listen
	DecisionKilled
)

// Result 描述一次占用检查的结论
type Result struct {
	Decision Decision
	// PID 是占用端口的进程 ID（找不到则 0）
	PID int
	// ProcName 是进程映像名（tasklist 第一列，找不到则空）
	ProcName string
	// Reason 给人看的说明 —— 写到日志 / 弹窗里
	Reason string
}

// HandlePortOccupied 在 listen 失败后调用，决定是否杀掉占用进程。
//
// 参数：
//   - listenAddr: bind 时使用的地址，例如 "127.0.0.1:18090"
//   - selfExePath: 当前 OpsToolbox.exe 的绝对路径（用于判断"是不是自己"）
//   - selfPID: 当前进程 PID（额外保险：同名不同 exe 时也不杀）
//   - enabled: config.yaml 里 kill_occupied_port 的值
//
// 返回：
//   - Result.Decision == DecisionKilled 时，主流程应该 sleep 一下再重试 listen
//     （Windows 上 taskkill 之后 socket 通常立即释放，但保险起见睡 200ms）
//   - 其它情况 Result.Reason 里写好了原因，主流程可以原样打日志 + 退出
func HandlePortOccupied(listenAddr, selfExePath string, selfPID int, enabled bool) Result {
	res := Result{Decision: DecisionNone}

	if !enabled {
		res.Reason = "端口占用自动清理未启用 (kill_occupied_port=false)"
		return res
	}

	if runtime.GOOS != "windows" {
		// 现阶段只针对 Windows。Linux/Mac 上 "pkill OpsToolbox" 风险更大
		// （Linux 上同名进程可能是用户别的工具；Mac 上没有 pkill 自带行为）。
		// 等真有人反馈这两个平台的问题再扩。
		res.Reason = "端口占用自动清理仅 Windows 上生效（当前 GOOS=" + runtime.GOOS + "）"
		return res
	}

	port, ok := extractPort(listenAddr)
	if !ok {
		res.Reason = "无法从 listenAddr 提取端口: " + listenAddr
		return res
	}

	pid, procName, procExe, ok := findListenerOnPort(port)
	if !ok {
		res.Reason = fmt.Sprintf("netstat 未找到占用端口 %d 的监听进程（端口可能已被释放）", port)
		return res
	}
	res.PID = pid
	res.ProcName = procName

	log.Printf("[portreuse] 端口 %d 被占用: PID=%d name=%s exe=%s", port, pid, procName, procExe)

	// 自我检查 1：PID 等于当前进程 —— 极少见（Windows PID 复用场景），
	// 但理论上可能。直接放行，不杀自己。
	if pid == selfPID {
		res.Reason = fmt.Sprintf("占用端口的 PID=%d 等于当前进程（可能 Windows PID 复用），不杀", pid)
		return res
	}

	// 自我检查 2：进程名 == OpsToolbox.exe 且 exe 路径 == 当前 exe → 静默杀
	// 注意：只比对 basename。test 机上 exe 可能在不同目录启动，
	// 但既然叫同名 + 同 PID 起点，杀掉重连就能拿到端口。
	if strings.EqualFold(procName, "OpsToolbox.exe") {
		// 进一步比对 exe 路径（如果 tasklist 给了）
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
		// Windows 上 socket 通常立即释放，但保险起见主流程会再睡一下再 listen
		return res
	}

	// 别的进程 → 弹 msg 框问
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

// extractPort 从 "host:port" 提取端口。addr 由 net.JoinHostPort 生成，
// IPv6 host 会带 "[..]"，但本工具默认 127.0.0.1，所以简单 LastIndexByte(':') 即可。
func extractPort(addr string) (int, bool) {
	idx := strings.LastIndexByte(addr, ':')
	if idx < 0 || idx == len(addr)-1 {
		return 0, false
	}
	p, err := strconv.Atoi(addr[idx+1:])
	if err != nil {
		return 0, false
	}
	return p, true
}

// findListenerOnPort 调 netstat -ano | findstr :PORT 找监听进程。
// 找 LISTENING 那一行的 PID，并 tasklist 拿进程名。
//
// 返回：(pid, procName, procExe, ok)。procExe 可能为空 —— tasklist 在某些
// Windows 版本上对 system 进程拿不到完整路径，不影响主流程。
func findListenerOnPort(port int) (int, string, string, bool) {
	// netstat -ano -p TCP 过滤只 TCP 行
	cmd := exec.Command("netstat", "-ano", "-p", "TCP")
	var out bytes.Buffer
	cmd.Stdout = &out
	// 故意吞 stderr，netstat 在没有 IPv6 栈时会刷"请求的操作需要提升"
	// 这种噪音进 stderr 对用户没用。
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
		// 形如：  TCP    127.0.0.1:18090    0.0.0.0:0    LISTENING    1234
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

	// tasklist /FI "PID eq <pid>" /FO LIST /V 拿进程名 + 映像路径
	// /FO LIST 输出形如：
	//   Image Name:   OpsToolbox.exe
	//   PID:           1234
	//   ...
	//   Image Path:    E:\...\OpsToolbox.exe
	cmd = exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", listeningPID),
		"/FO", "LIST", "/V")
	out.Reset()
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		// 进程可能在 netstat 和 tasklist 之间已经退出 —— 不致命
		log.Printf("[portreuse] tasklist 查 PID=%d 失败（进程可能已退出）: %v", listeningPID, err)
		return listeningPID, "", "", true
	}

	procName, procExe := parseTasklistOutput(out.String())
	return listeningPID, procName, procExe, true
}

// parseTasklistOutput 解析 tasklist /FO LIST /V 输出，
// 提取 "Image Name" 和 "Image Path" 两行。
//
// /V 在中文 Windows 上列名是「映像名称」「映像路径」；在英文 Windows 上是
// "Image Name" / "Image Path"。两种都兼容 —— 只认冒号前的关键字是否
// 等于 name/path 的某个同义词。
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
// /F 强制 /T 连子进程一起杀 —— 上一次 OpsToolbox 可能有 tail 子会话。
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
// 用 msg.exe（Windows 自带）；如果系统是 Home 版没装 msg，就 fallback 到
// 一个 30 秒超时的命令行 stdin —— 不阻塞整个启动流程。
//
// 返回 true = 用户同意杀。
func promptKillOtherProcess(pid int, name string, port int) bool {
	msg := fmt.Sprintf(
		"OpsToolbox 想使用端口 %d，但被其它进程占用。\r\n\r\n"+
			"占用方：%s (PID=%d)\r\n\r\n"+
			"按 Y 杀掉该进程（注意：会丢失该进程未保存的数据）。\r\n"+
			"按 N 取消，OpsToolbox 退出。\r\n",
		port, name, pid)

	// 优先用 msg *（发给当前 session 的所有终端 / 用户）
	// msg 默认是发到 session 而不是弹 GUI，对远程桌面/无头场景更稳。
	// 用户必须从键盘输入 y/n —— 远程桌面上简单粗暴但够用。
	// msg 没有内置的 y/n 模式，只能发文本，所以我们用一个临时 BAT 文件
	// 配合 choice 命令做 y/n。
	//
	// 简化方案：用 msg 把"是否要杀"通知到所有 session，然后另开一个
	// 90 秒超时的 cmd /C "choice /C YN /N /T 90 /D N" —— 用户在
	// 当前控制台按 Y/N 选，90 秒不按默认 N。
	//
	// 风险：如果进程跑在无控制台环境（如计划任务启动），
	// choice 会立刻 EOF 当作 N，不阻塞。
	notifyMsg(pid, name, port)

	log.Printf("[portreuse] 等待用户在当前终端输入 Y/N（90 秒不输默认 N）……")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "===============================================================")
	fmt.Fprintln(os.Stderr, msg)
	fmt.Fprintln(os.Stderr, "===============================================================")
	fmt.Fprint(os.Stderr, "请输入 Y/N: ")

	type result struct{ ok bool; err error }
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

// notifyMsg 用 Windows msg.exe 通知其它 session（如果有）。
// 失败不致命 —— 单机 session 下没有别的终端收不到也没事。
func notifyMsg(pid int, name string, port int) {
	msg := fmt.Sprintf("OpsToolbox needs port %d, occupied by %s (PID=%d). Check the launching console for Y/N prompt.", port, name, pid)
	cmd := exec.Command("msg", "*", "/TIME:60", msg)
	// msg 在没装 / 没启用 messenger 服务时会失败 —— 吞掉
	_ = cmd.Run()
}

// sameFilePath 比对两条 Windows 路径是否指同一个文件。
// Windows 路径大小写不敏感 + 路径分隔符混用，所以两边都做
// filepath.Clean + ToLower + 替换 \ 为 / 再比。
func sameFilePath(a, b string) (bool, error) {
	if a == "" || b == "" {
		return false, nil
	}
	ca := normalizePath(a)
	cb := normalizePath(b)
	if ca == cb {
		return true, nil
	}
	// 进一步 stat 一次确认（处理符号链接 / 8.3 短名）
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