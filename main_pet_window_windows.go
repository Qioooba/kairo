//go:build windows

package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"kairo/internal/sysutil"
)

type osVersionInfoExW struct {
	osVersionInfoSize uint32
	majorVersion      uint32
	minorVersion      uint32
	buildNumber       uint32
	platformId        uint32
	csdVersion        [128]uint16
	servicePackMajor  uint16
	servicePackMinor  uint16
	suiteMask         uint16
	productType       byte
	reserved          [3]byte
}

var (
	modNtdll   = windows.NewLazySystemDLL("ntdll.dll")
	procRtlVer = modNtdll.NewProc("RtlGetVersion")

	floatingWarn = false
)

func openFloatingPetWindow(url string) {
	if launchFloatingWindow(url) {
		return
	}

	if !floatingWarn {
		log.Printf("未检测到可用独立窗口引擎（桌面宿主 / Chrome / 360 / IE / Edge），回退系统默认浏览器")
		floatingWarn = true
	}
	openBrowser(url)
}

func isWin10OrNewer1709() bool {
	var vi osVersionInfoExW
	vi.osVersionInfoSize = uint32(unsafe.Sizeof(vi))
	ret, _, _ := procRtlVer.Call(uintptr(unsafe.Pointer(&vi)))
	if ret != 0 {
		return false
	}
	if vi.majorVersion > 10 {
		return true
	}
	if vi.majorVersion < 10 {
		return false
	}
	return vi.buildNumber >= 16299
}

func launchFloatingWindow(url string) bool {
	if isWin10OrNewer1709() {
		if launchWindowsDesktopPetHost(url) {
			log.Printf("打开桌面级原生宿主窗口")
			return true
		}
	}

	exes := preferredFloatingBrowsers()
	if len(exes) == 0 {
		return false
	}

	for _, exe := range exes {
		if exe == "" {
			continue
		}
		exe = filepath.Clean(exe)
		if runAsFloatingWindow(exe, url) {
			log.Printf("打开桌面悬浮窗口: %s", exe)
			return true
		}
	}
	return false
}

func preferredFloatingBrowsers() []string {
	var exes []string
	if isWin10OrNewer1709() {
		exes = []string{
			findEdgeExecutable(),
			findChromeExecutable(),
			find360Executable(),
			findIEExecutable(),
		}
		return dedupePaths(exes)
	}

	// Win7/Win8 或更早版本：优先 Chrome 级别体验，再到 360，最后 IE 兜底。
	exes = []string{
		findChromeExecutable(),
		find360Executable(),
		findIEExecutable(),
	}
	return dedupePaths(exes)
}

func dedupePaths(exes []string) []string {
	seen := make(map[string]struct{}, len(exes))
	out := make([]string, 0, len(exes))
	for _, exe := range exes {
		exe = filepath.Clean(strings.TrimSpace(exe))
		if exe == "" {
			continue
		}
		if _, ok := seen[exe]; ok {
			continue
		}
		seen[exe] = struct{}{}
		out = append(out, exe)
	}
	return out
}

func launchWindowsDesktopPetHost(url string) bool {
	exe := findPowerShellExecutable()
	if exe == "" {
		log.Printf("未检测到可用的 PowerShell，跳过桌面级原生宿主")
		return false
	}

	legacyURL := buildLegacyPetURL(url)
	tmp, err := os.CreateTemp("", "kairo-pet-host-*.ps1")
	if err != nil {
		log.Printf("创建桌面宠物宿主脚本失败: %v", err)
		return false
	}
	scriptPath := tmp.Name()
	encodedURL := base64.StdEncoding.EncodeToString([]byte(legacyURL))
	script := desktopPetHostScript(encodedURL)
	if _, err := tmp.WriteString(script); err != nil {
		_ = tmp.Close()
		_ = os.Remove(scriptPath)
		log.Printf("写入桌面宠物宿主脚本失败: %v", err)
		return false
	}
	_ = tmp.Close()

	cmd := exec.Command(
		exe,
		"-NoProfile",
		"-STA",
		"-WindowStyle", "Hidden",
		"-ExecutionPolicy", "Bypass",
		"-File", scriptPath,
	)
	sysutil.HideConsoleWindow(cmd)

	if err := cmd.Start(); err != nil {
		_ = os.Remove(scriptPath)
		log.Printf("启动桌面级原生宿主失败: %s (%v)", exe, err)
		return false
	}
	go func() {
		_ = cmd.Wait()
		_ = os.Remove(scriptPath)
	}()
	return true
}

func buildLegacyPetURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		if strings.HasSuffix(rawURL, "/pet-float.html") {
			return strings.TrimSuffix(rawURL, "pet-float.html") + "pet-float-legacy.html"
		}
		if strings.HasSuffix(rawURL, "/pet-float") {
			return rawURL + "-legacy"
		}
		return rawURL
	}
	if strings.HasSuffix(u.Path, "/pet-float.html") {
		u.Path = strings.TrimSuffix(u.Path, "/pet-float.html") + "/pet-float-legacy.html"
		return u.String()
	}
	if strings.HasSuffix(u.Path, "/pet-float-legacy") {
		return u.String()
	}
	if strings.HasSuffix(u.Path, "/pet-float") {
		u.Path += "-legacy"
		return u.String()
	}
	// 找不到预期路径时，在路径尾补充兼容页面。
	u.Path = strings.TrimSuffix(u.Path, "/") + "/pet-float-legacy.html"
	return u.String()
}

func runAsFloatingWindow(exe, url string) bool {
	args := floatingWindowArgs(exe, url)
	if len(args) == 0 {
		return false
	}
	cmd := exec.Command(exe, args...)
	sysutil.HideConsoleWindow(cmd)
	if err := cmd.Start(); err != nil {
		log.Printf("启动悬浮窗口失败: %s (%v)", exe, err)
		return false
	}
	go func() { _ = cmd.Wait() }()
	return true
}

func floatingWindowArgs(exe, url string) []string {
	base := strings.ToLower(filepath.Base(exe))
	switch {
	case strings.Contains(base, "msedge"):
		return []string{"--app=" + url, "--new-window", "--disable-sync"}
	case strings.Contains(base, "chrome"):
		return []string{"--app=" + url}
	case strings.Contains(base, "360"):
		return []string{"--app=" + url}
	case strings.Contains(base, "iexplore"):
		return []string{"/new", "/embed", url}
	default:
		return nil
	}
}

func findChromeExecutable() string {
	if p, ok := sysutil.FindChrome(); ok {
		return p
	}
	return ""
}

func findEdgeExecutable() string {
	cands := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "Application", "msedge.exe"),
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func find360Executable() string {
	cands := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "360", "极速浏览器", "Application", "360chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "360", "极速浏览器", "Application", "360chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "360", "安全浏览器", "Application", "360se.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "360", "安全浏览器", "Application", "360se.exe"),
		filepath.Join(os.Getenv("ProgramW6432"), "360", "Chrome", "Application", "360chrome.exe"),
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func findIEExecutable() string {
	cands := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Internet Explorer", "iexplore.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Internet Explorer", "iexplore.exe"),
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func findPowerShellExecutable() string {
	if p, err := exec.LookPath("powershell"); err == nil {
		return p
	}
	if p, err := exec.LookPath("pwsh"); err == nil {
		return p
	}
	return ""
}

func desktopPetHostScript(encodedURL string) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'SilentlyContinue'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$url = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))

$form = New-Object System.Windows.Forms.Form
$form.Text = 'Kairo 宠物'
$form.StartPosition = 'Manual'
$screen = [System.Windows.Forms.Screen]::PrimaryScreen.WorkingArea
$form.Top = 60
$form.Left = [Math]::Max(12, $screen.Width - 340)
$form.Width = 320
$form.Height = 340
$form.MinimumSize = New-Object System.Drawing.Size(280, 320)
$form.MaximizeBox = $false
$form.MinimizeBox = $false
$form.ShowInTaskbar = $false
$form.TopMost = $true
$form.FormBorderStyle = [System.Windows.Forms.FormBorderStyle]::FixedToolWindow
$form.BackColor = [System.Drawing.Color]::FromArgb(19, 24, 38)

$hostPanel = New-Object System.Windows.Forms.Panel
$hostPanel.Dock = [System.Windows.Forms.DockStyle]::Fill
$hostPanel.BackColor = [System.Drawing.Color]::FromArgb(19, 24, 38)

$browser = New-Object System.Windows.Forms.WebBrowser
$browser.Dock = [System.Windows.Forms.DockStyle]::Fill
$browser.IsWebBrowserContextMenuEnabled = $false
$browser.WebBrowserShortcutsEnabled = $false
$browser.ScrollBarsEnabled = $false
$browser.ScriptErrorsSuppressed = $true
$browser.Navigate($url)
$hostPanel.Controls.Add($browser)
$form.Controls.Add($hostPanel)

[System.Windows.Forms.Application]::EnableVisualStyles()
[System.Windows.Forms.Application]::Run($form)
`, encodedURL)
}
