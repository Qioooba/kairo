package portreuse

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestExtractPort 验证 host:port 解析（含 IPv6 [::1]:port 形式）
func TestExtractPort(t *testing.T) {
	cases := []struct {
		addr string
		want int
		ok   bool
	}{
		{"127.0.0.1:18090", 18090, true},
		{"[::1]:18090", 18090, true},
		{"0.0.0.0:8080", 8080, true},
		{"no-colon", 0, false},
		{"127.0.0.1:", 0, false},
		{"127.0.0.1:abc", 0, false},
		{"127.0.0.1:65535", 65535, true},
	}
	for _, c := range cases {
		got, ok := extractPort(c.addr)
		if ok != c.ok || got != c.want {
			t.Errorf("extractPort(%q) = (%d, %v), want (%d, %v)", c.addr, got, ok, c.want, c.ok)
		}
	}
}

// TestHandlePortOccupied_Disabled 验证 enabled=false 直接走 DecisionNone，
// 不调 netstat/lsof，不弹窗，不杀进程。
func TestHandlePortOccupied_Disabled(t *testing.T) {
	res := HandlePortOccupied("127.0.0.1:18090", "/some/path/Kairo.exe", 1234, false)
	if res.Decision != DecisionNone {
		t.Errorf("enabled=false 期望 DecisionNone,实际 %v", res.Decision)
	}
	if !strings.Contains(res.Reason, "kill_occupied_port=false") {
		t.Errorf("Reason 应说明未启用,实际: %q", res.Reason)
	}
}

// TestHandlePortOccupied_BadAddr 验证 listenAddr 解析失败走 DecisionNone，
// 不 panic、不乱杀。
func TestHandlePortOccupied_BadAddr(t *testing.T) {
	res := HandlePortOccupied("not-a-valid-addr", "/some/path", 1234, true)
	if res.Decision != DecisionNone {
		t.Errorf("坏地址期望 DecisionNone,实际 %v", res.Decision)
	}
	if !strings.Contains(res.Reason, "无法从 listenAddr 提取端口") {
		t.Errorf("Reason 应说明地址解析失败,实际: %q", res.Reason)
	}
}

// TestHandlePortOccupied_FreePort 验证端口未被占用时返回正确提示。
func TestHandlePortOccupied_FreePort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法监听临时端口: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	res := HandlePortOccupied(addr, "/some/path/Kairo", os.Getpid(), true)
	if res.Decision != DecisionNone {
		t.Errorf("空闲端口期望 DecisionNone,实际 %v", res.Decision)
	}
	if !strings.Contains(res.Reason, "未找到占用端口") && !strings.Contains(res.Reason, "端口可能已被释放") {
		t.Errorf("空闲端口应提示未找到占用进程,实际: %q", res.Reason)
	}
}

// TestHandlePortOccupied_DetectsOccupied 验证能检测到实际占用端口的进程。
// macOS/Linux：用 lsof 检测；Windows：用 netstat 检测。
// 非 Windows 平台不自动杀进程，只返回提示信息；Windows 平台如果是自身进程也不杀。
func TestHandlePortOccupied_DetectsOccupied(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法监听临时端口: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	selfPID := os.Getpid()

	selfExePath, err := os.Executable()
	if err != nil {
		selfExePath = "/path/to/Kairo"
	}

	res := HandlePortOccupied(addr, selfExePath, selfPID, true)
	if res.Decision != DecisionNone {
		t.Errorf("当前进程占用端口时不应杀掉自己,期望 DecisionNone,实际 %v", res.Decision)
	}
	if res.PID != selfPID {
		t.Errorf("应检测到当前进程 PID=%d 占用端口,实际检测到 PID=%d", selfPID, res.PID)
	}
	if !strings.Contains(res.Reason, fmt.Sprintf("PID=%d", selfPID)) {
		t.Errorf("Reason 应包含被占用进程的 PID 信息,实际: %q", res.Reason)
	}
	t.Logf("端口检测结果: %s", res.Reason)
}

// TestHandlePortOccupied_NonWindows_Hint 验证非 Windows 平台返回手动释放提示。
func TestHandlePortOccupied_NonWindows_Hint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("此测试只在非 Windows 平台运行")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法监听临时端口: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	port, _ := extractPort(addr)
	selfPID := os.Getpid()

	selfExePath, _ := os.Executable()

	res := HandlePortOccupied(addr, selfExePath, selfPID, true)
	if res.Decision != DecisionNone {
		t.Errorf("非 Windows 平台不自动杀进程,期望 DecisionNone,实际 %v", res.Decision)
	}

	if strings.Contains(res.Reason, "仅 Windows") {
		t.Errorf("非 Windows 平台现在应使用 lsof 检测,不应再返回'仅 Windows'提示,实际: %q", res.Reason)
	}

	if !strings.Contains(res.Reason, "kill") && !strings.Contains(res.Reason, "手动") {
		t.Logf("提示信息包含手动释放建议: %q", res.Reason)
	}

	t.Logf("端口 %d 被 PID=%d 占用，提示信息:\n%s", port, selfPID, res.Reason)
}

func BenchmarkExtractPort(b *testing.B) {
	addr := "127.0.0.1:" + strconv.Itoa(18090)
	for i := 0; i < b.N; i++ {
		extractPort(addr)
	}
}
