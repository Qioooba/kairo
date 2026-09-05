//go:build windows

// Package sysutil / browser_locate_windows.go — Windows 平台现代浏览器探测。
//
// 支持探测：
//  1. Google Chrome (chrome.exe)
//  2. 360 极速浏览器 / 360 极速浏览器 X (360chrome.exe)
//  3. Microsoft Edge (msedge.exe)
//  4. 360 安全浏览器 (360se.exe)
//
// 探测策略：
//   - 优先查各浏览器的常见安装路径（os.Stat，< 1ms，命中 95%+ 用户）
//   - 次查注册表 App Paths 与软件安装路径（< 5ms，处理自定义安装目录）
//   - 注册表 API 直绑 advapi32.dll，不引入额外三方依赖，保持 Win7/10/11 兼容性。
package sysutil

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"kairo/internal/browserpref"
)

// BrowserCandidate 描述探测到的现代浏览器信息。
type BrowserCandidate struct {
	Kind browserpref.Kind
	Name string
	Path string
}

// candidateEnvDirs 返回常见环境变量对应的系统应用根目录列表。
func candidateEnvDirs() []string {
	var dirs []string
	for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)", "LOCALAPPDATA", "APPDATA"} {
		if d := os.Getenv(env); d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// 常见 Chrome 安装路径。
func chromeCandidatePaths() []string {
	var paths []string
	for _, env := range []string{"ProgramFiles", "ProgramW6432"} {
		if dir := os.Getenv(env); dir != "" {
			paths = append(paths, filepath.Join(dir, "Google", "Chrome", "Application", "chrome.exe"))
		}
	}
	if dir := os.Getenv("ProgramFiles(x86)"); dir != "" {
		paths = append(paths, filepath.Join(dir, "Google", "Chrome", "Application", "chrome.exe"))
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		paths = append(paths, filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"))
	}
	return paths
}

// 常见 360 极速浏览器 / 360 极速浏览器 X 安装路径。
func chrome360CandidatePaths() []string {
	var paths []string
	for _, dir := range candidateEnvDirs() {
		// 360 极速浏览器常规版本
		paths = append(paths, filepath.Join(dir, "360", "360Chrome", "Chrome", "Application", "360chrome.exe"))
		paths = append(paths, filepath.Join(dir, "360Chrome", "Chrome", "Application", "360chrome.exe"))
		// 360 极速浏览器 X（64 位 Chromium 新架构版）
		paths = append(paths, filepath.Join(dir, "360", "360ChromeX", "Chrome", "Application", "360chrome.exe"))
		paths = append(paths, filepath.Join(dir, "360ChromeX", "Chrome", "Application", "360chrome.exe"))
	}
	return paths
}

// 常见 Microsoft Edge (Chromium 内核) 安装路径。
func edgeCandidatePaths() []string {
	var paths []string
	for _, dir := range candidateEnvDirs() {
		paths = append(paths, filepath.Join(dir, "Microsoft", "Edge", "Application", "msedge.exe"))
	}
	return paths
}

// 常见 360 安全浏览器安装路径。
func se360CandidatePaths() []string {
	var paths []string
	for _, dir := range candidateEnvDirs() {
		paths = append(paths, filepath.Join(dir, "360", "360se6", "Application", "360se.exe"))
		paths = append(paths, filepath.Join(dir, "360se6", "Application", "360se.exe"))
	}
	return paths
}

type regQuery struct {
	root      uintptr
	subkey    string
	valueName string
}

func chromeRegistryKeys() []regQuery {
	return []regQuery{
		{hkeyLocalMachine, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, ""},
		{hkeyCurrentUser, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, ""},
		{hkeyLocalMachine, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, ""},
		{hkeyLocalMachine, `SOFTWARE\Google\Chrome\Application`, "path"},
		{hkeyLocalMachine, `SOFTWARE\WOW6432Node\Google\Chrome\Application`, "path"},
	}
}

func chrome360RegistryKeys() []regQuery {
	return []regQuery{
		{hkeyLocalMachine, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\360chrome.exe`, ""},
		{hkeyCurrentUser, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\360chrome.exe`, ""},
		{hkeyLocalMachine, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\360chrome.exe`, ""},
		{hkeyLocalMachine, `SOFTWARE\360\360Chrome`, "path"},
		{hkeyLocalMachine, `SOFTWARE\WOW6432Node\360\360Chrome`, "path"},
	}
}

func edgeRegistryKeys() []regQuery {
	return []regQuery{
		{hkeyLocalMachine, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, ""},
		{hkeyCurrentUser, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, ""},
		{hkeyLocalMachine, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, ""},
	}
}

func se360RegistryKeys() []regQuery {
	return []regQuery{
		{hkeyLocalMachine, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\360se.exe`, ""},
		{hkeyCurrentUser, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\360se.exe`, ""},
		{hkeyLocalMachine, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\360se.exe`, ""},
	}
}

var disableRegistryForTest = false

// findPathWithCandidates 优先检测指定文件路径，若未命中再遍历注册表项。
func findPathWithCandidates(filePaths []string, regQueries []regQuery) (string, bool) {
	for _, p := range filePaths {
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				return cleanPath(p), true
			}
		}
	}
	if disableRegistryForTest {
		return "", false
	}
	for _, q := range regQueries {
		if p := readPathFromRegistry(q.root, q.subkey, q.valueName); p != "" {
			return cleanPath(p), true
		}
	}
	return "", false
}

// FindModernBrowser 探测系统上安装的现代化 Chromium / WebKit 浏览器。
// 探测优先级：Chrome → 360 极速浏览器 → Microsoft Edge → 360 安全浏览器。
func FindModernBrowser() (BrowserCandidate, bool) {
	if p, ok := findPathWithCandidates(chromeCandidatePaths(), chromeRegistryKeys()); ok {
		return BrowserCandidate{Kind: browserpref.KindChrome, Name: "Google Chrome", Path: p}, true
	}
	if p, ok := findPathWithCandidates(chrome360CandidatePaths(), chrome360RegistryKeys()); ok {
		return BrowserCandidate{Kind: browserpref.Kind360Chrome, Name: "360极速浏览器", Path: p}, true
	}
	if p, ok := findPathWithCandidates(edgeCandidatePaths(), edgeRegistryKeys()); ok {
		return BrowserCandidate{Kind: browserpref.KindEdge, Name: "Microsoft Edge", Path: p}, true
	}
	if p, ok := findPathWithCandidates(se360CandidatePaths(), se360RegistryKeys()); ok {
		return BrowserCandidate{Kind: browserpref.Kind360SE, Name: "360安全浏览器", Path: p}, true
	}
	return BrowserCandidate{}, false
}

// FindChrome 探测系统上是否安装了 Chrome（保留此函数向后兼容现有调用与单测）。
func FindChrome() (string, bool) {
	return findPathWithCandidates(chromeCandidatePaths(), chromeRegistryKeys())
}

// cleanPath 规范化浏览器可执行文件路径（去除多余引号与尾斜杠）。
func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"`)
	return strings.TrimRight(p, `\/`)
}

// ---------- 注册表查询底层实现 ----------

const (
	hkeyLocalMachine uintptr = 0x80000002

	keyQueryValue uint32 = 0x0001
)

var (
	procRegOpenKeyExWLocate    = modAdvapi32.NewProc("RegOpenKeyExW")
	procRegQueryValueExWLocate = modAdvapi32.NewProc("RegQueryValueExW")
	procRegCloseKeyLocate      = modAdvapi32.NewProc("RegCloseKey")
)

// readPathFromRegistry 从注册表读取指定键路径字符串。
func readPathFromRegistry(root uintptr, subkey, valueName string) string {
	keyPathPtr, err := syscall.UTF16PtrFromString(subkey)
	if err != nil {
		return ""
	}
	var hKey uintptr
	ret, _, _ := procRegOpenKeyExWLocate.Call(
		root,
		uintptr(unsafe.Pointer(keyPathPtr)),
		0,
		uintptr(keyQueryValue),
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != 0 /* ERROR_SUCCESS */ {
		return ""
	}
	defer procRegCloseKeyLocate.Call(hKey)

	var valueNamePtr *uint16
	if valueName != "" {
		p, err := syscall.UTF16PtrFromString(valueName)
		if err != nil {
			return ""
		}
		valueNamePtr = p
	}

	var dataType uint32
	var dataSize uint32
	ret, _, _ = procRegQueryValueExWLocate.Call(
		hKey,
		uintptr(unsafe.Pointer(valueNamePtr)),
		0,
		uintptr(unsafe.Pointer(&dataType)),
		0,
		uintptr(unsafe.Pointer(&dataSize)),
	)
	if ret != 0 || dataSize == 0 {
		return ""
	}

	buf := make([]uint16, dataSize/2)
	ret, _, _ = procRegQueryValueExWLocate.Call(
		hKey,
		uintptr(unsafe.Pointer(valueNamePtr)),
		0,
		uintptr(unsafe.Pointer(&dataType)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&dataSize)),
	)
	if ret != 0 {
		return ""
	}

	raw := syscall.UTF16ToString(buf)
	raw = cleanPath(raw)
	if !strings.HasSuffix(strings.ToLower(raw), ".exe") {
		return ""
	}
	if _, err := os.Stat(raw); err != nil {
		return ""
	}
	return raw
}
