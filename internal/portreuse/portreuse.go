// Package portreuse 提供「端口被占时自动清理」能力。
//
// 背景：v0.6.1 之前，DoubaoToolbox.exe 双击启动时如果上一次还没完全退出，
// 会立刻报 `bind: Only one usage of each socket address (protocol/network
// address/port) is normally permitted.` 然后退出。对内网测试场景来说
// 「双击重启」是高频操作，每次都要去任务管理器杀残留进程很烦。
//
// 策略：
//   - Windows：自动检测残留 DoubaoToolbox.exe 并静默 taskkill；其它进程弹框询问
//   - macOS/Linux：使用 lsof 检测占用进程，给出友好提示让用户手动处理，不强杀
//
// 任何步骤失败都不致命 —— 最差就是回到原来的"端口被占"错误，
// 但用户能看到具体是哪个进程 / PID 占着，方便排查。
package portreuse

import (
	"strconv"
	"strings"
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
	// ProcName 是进程映像名（找不到则空）
	ProcName string
	// Reason 给人看的说明 —— 写到日志 / 弹窗里
	Reason string
}

// HandlePortOccupied 在 listen 失败后调用，决定是否杀掉占用进程。
//
// 参数：
//   - listenAddr: bind 时使用的地址，例如 "127.0.0.1:18090"
//   - selfExePath: 当前 DoubaoToolbox.exe 的绝对路径（用于判断"是不是自己"）
//   - selfPID: 当前进程 PID（额外保险：同名不同 exe 时也不杀）
//   - enabled: config.yaml 里 kill_occupied_port 的值
//
// 返回：
//   - Result.Decision == DecisionKilled 时，主流程应该 sleep 一下再重试 listen
//   - 其它情况 Result.Reason 里写好了原因，主流程可以原样打日志 + 退出
func HandlePortOccupied(listenAddr, selfExePath string, selfPID int, enabled bool) Result {
	res := Result{Decision: DecisionNone}

	if !enabled {
		res.Reason = "端口占用自动清理未启用 (kill_occupied_port=false)"
		return res
	}

	port, ok := extractPort(listenAddr)
	if !ok {
		res.Reason = "无法从 listenAddr 提取端口: " + listenAddr
		return res
	}

	return handlePortOccupied(port, selfExePath, selfPID)
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
