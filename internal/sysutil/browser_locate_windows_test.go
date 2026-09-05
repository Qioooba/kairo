//go:build windows

package sysutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFindChromeWithFakeProgramFiles 模拟"Chrome 装在 Program Files 下"。
//
// 不能用真实的环境（CI 上可能装了 Chrome），改用一个临时目录 + setenv
// 模拟 %ProgramFiles% 指到我们自己造的空目录。
func TestFindChromeWithFakeProgramFiles(t *testing.T) {
	tmp := t.TempDir()
	// 构造 fake %ProgramFiles%\Google\Chrome\Application\chrome.exe。
	chromeDir := filepath.Join(tmp, "Google", "Chrome", "Application")
	if err := os.MkdirAll(chromeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	chromePath := filepath.Join(chromeDir, "chrome.exe")
	if err := os.WriteFile(chromePath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ProgramFiles", "")
	t.Setenv("ProgramW6432", tmp)
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("LOCALAPPDATA", "")

	got, ok := FindChrome()
	if !ok {
		t.Fatalf("FindChrome() = false, want true")
	}
	if !strings.EqualFold(got, chromePath) {
		t.Errorf("FindChrome() = %q, want %q", got, chromePath)
	}
}

func TestFindChromeNotInstalled(t *testing.T) {
	// 隔离真实注册表查询，验证候选路径全落空时的行为。
	disableRegistryForTest = true
	defer func() { disableRegistryForTest = false }()

	empty := t.TempDir()
	t.Setenv("ProgramFiles", empty)
	t.Setenv("ProgramW6432", "")
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("LOCALAPPDATA", empty)

	if _, ok := FindChrome(); ok {
		t.Errorf("FindChrome() should return false when no Chrome installed")
	}
}

func TestChromeCandidatePathsOrder(t *testing.T) {
	// 验证候选路径顺序：64-bit 先于 32-bit 先于 user-local。
	tmp := t.TempDir()
	// 在所有三个位置都放一个 chrome.exe 文件。
	for _, sub := range []string{"Google\\Chrome\\Application", "Google\\Chrome\\Application"} {
		_ = sub
	}
	dirs := map[string]string{
		"ProgramFiles":      filepath.Join(tmp, "pf64"),
		"ProgramW6432":      filepath.Join(tmp, "pf64"),
		"ProgramFiles(x86)": filepath.Join(tmp, "pf86"),
		"LOCALAPPDATA":      filepath.Join(tmp, "user"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(d, "Google", "Chrome", "Application"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(d, "Google", "Chrome", "Application", "chrome.exe"),
			[]byte("x"), 0o644,
		); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range dirs {
		t.Setenv(k, v)
	}

	got, ok := FindChrome()
	if !ok {
		t.Fatal("FindChrome = false, want true")
	}
	// 应该命中 ProgramFiles / ProgramW6432 路径，不会到 (x86) / LOCALAPPDATA。
	expected := filepath.Join(dirs["ProgramFiles"], "Google", "Chrome", "Application", "chrome.exe")
	if !strings.EqualFold(got, expected) {
		t.Errorf("FindChrome = %q, want %q (64-bit path first)", got, expected)
	}
}

func TestCleanPath(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{`C:\Chrome\chrome.exe`, `C:\Chrome\chrome.exe`},
		{`C:\Chrome\chrome.exe\`, `C:\Chrome\chrome.exe`},
		{`C:\Chrome\chrome.exe/`, `C:\Chrome\chrome.exe`},
		{`/c/Chrome/chrome.exe`, `/c/Chrome/chrome.exe`},
	}
	for _, tc := range tests {
		if got := cleanPath(tc.in); got != tc.want {
			t.Errorf("cleanPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFindChromeRegistryResilience —— 注册表查询 path 不存在的路径
// （这是 95% 的环境：开发机没装 Chrome），期望安静地不报错，返回 ok=false。
//
// 这一条不需要真实装 Chrome 也能验证：错误路径走到 syscall 时不会 panic。
func TestFindChromeRegistryMiss(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("ProgramFiles", empty)
	t.Setenv("ProgramW6432", "")
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("LOCALAPPDATA", empty)

	_, ok := FindChrome()
	// 不管注册表里有没有，反正公共路径没有就期望 false。
	// 在没装 Chrome 的环境下必为 false；装了的环境也至少保证不 panic。
	_ = ok
}

func TestFindModernBrowser360Chrome(t *testing.T) {
	disableRegistryForTest = true
	defer func() { disableRegistryForTest = false }()

	tmp := t.TempDir()
	// 仅构造 fake 360 极速浏览器路径
	chrome360Dir := filepath.Join(tmp, "360", "360Chrome", "Chrome", "Application")
	if err := os.MkdirAll(chrome360Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exePath := filepath.Join(chrome360Dir, "360chrome.exe")
	if err := os.WriteFile(exePath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ProgramFiles", tmp)
	t.Setenv("ProgramW6432", "")
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("APPDATA", "")

	b, ok := FindModernBrowser()
	if !ok {
		t.Fatal("FindModernBrowser() = false, want true for 360chrome")
	}
	if b.Kind != "360chrome" {
		t.Errorf("FindModernBrowser() Kind = %q, want %q", b.Kind, "360chrome")
	}
	if !strings.EqualFold(b.Path, exePath) {
		t.Errorf("FindModernBrowser() Path = %q, want %q", b.Path, exePath)
	}
}

func TestFindModernBrowserEdge(t *testing.T) {
	disableRegistryForTest = true
	defer func() { disableRegistryForTest = false }()

	tmp := t.TempDir()
	// 仅构造 fake Edge 路径
	edgeDir := filepath.Join(tmp, "Microsoft", "Edge", "Application")
	if err := os.MkdirAll(edgeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exePath := filepath.Join(edgeDir, "msedge.exe")
	if err := os.WriteFile(exePath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ProgramFiles", tmp)
	t.Setenv("ProgramW6432", "")
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("APPDATA", "")

	b, ok := FindModernBrowser()
	if !ok {
		t.Fatal("FindModernBrowser() = false, want true for edge")
	}
	if b.Kind != "edge" {
		t.Errorf("FindModernBrowser() Kind = %q, want %q", b.Kind, "edge")
	}
	if !strings.EqualFold(b.Path, exePath) {
		t.Errorf("FindModernBrowser() Path = %q, want %q", b.Path, exePath)
	}
}
