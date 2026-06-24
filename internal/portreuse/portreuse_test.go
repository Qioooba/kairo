package portreuse

import (
	"os"
	"runtime"
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
// 不调 netstat，不弹窗，不杀进程。
func TestHandlePortOccupied_Disabled(t *testing.T) {
	res := HandlePortOccupied("127.0.0.1:18090", "/some/path/OpsToolbox.exe", 1234, false)
	if res.Decision != DecisionNone {
		t.Errorf("enabled=false 期望 DecisionNone,实际 %v", res.Decision)
	}
	if !strings.Contains(res.Reason, "kill_occupied_port=false") {
		t.Errorf("Reason 应说明未启用,实际: %q", res.Reason)
	}
}

// TestHandlePortOccupied_NonWindows 验证非 Windows 平台走 DecisionNone。
// 设计：现阶段只针对 Windows，Linux/Mac 上同名进程可能是用户别的工具，
// 杀错代价大。
func TestHandlePortOccupied_NonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("此测试只在非 Windows 平台有意义")
	}
	res := HandlePortOccupied("127.0.0.1:18090", "/some/path/OpsToolbox", 1234, true)
	if res.Decision != DecisionNone {
		t.Errorf("非 Windows 期望 DecisionNone,实际 %v", res.Decision)
	}
	if !strings.Contains(res.Reason, "仅 Windows") {
		t.Errorf("Reason 应说明仅 Windows,实际: %q", res.Reason)
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

// TestSameFilePath_Normalize 验证 Windows 上同一文件不同写法被判 same。
// macOS/Linux 跳过 — 走 filepath.Clean 即可，本测试只验 Windows API 路径归一化。
func TestSameFilePath_Normalize(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 路径归一化测试,只在 Windows 跑")
	}
	dir := t.TempDir()
	p1 := dir + "/OpsToolbox.exe"
	if err := os.WriteFile(p1, []byte(""), 0644); err != nil {
		t.Skipf("无法写测试文件: %v", err)
	}
	p2 := dir + "\\OpsToolbox.exe"
	same, err := sameFilePath(p1, p2)
	if err != nil {
		t.Skipf("sameFilePath 跳错（无 Windows API?）: %v", err)
	}
	if !same {
		t.Errorf("同一文件不同写法应判 same,实际 not same")
	}
}
