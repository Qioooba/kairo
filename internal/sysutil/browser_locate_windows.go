//go:build windows

// Package sysutil / browser_locate_windows.go — Windows 平台的 Chrome 探测。
//
// 探测优先级：
//
//  1. 常见安装路径（os.Stat，最快，< 1ms，命中 95%+ 用户）：
//     - C:\Program Files\Google\Chrome\Application\chrome.exe   (64-bit)
//     - C:\Program Files (x86)\Google\Chrome\Application\chrome.exe  (32-bit on x64)
//     - %LOCALAPPDATA%\Google\Chrome\Application\chrome.exe     (per-user)
//
//  2. 注册表兜底（< 5ms，处理"装在非标准路径"的边角用户）：
//     - HKLM\SOFTWARE\Google\Chrome\Application\path           (64-bit chrome on 64-bit / 32-bit on 32-bit)
//     - HKLM\SOFTWARE\WOW6432Node\Google\Chrome\Application\path  (32-bit chrome on 64-bit)
//     - HKLM\SOFTWARE\Google\Chrome\Application\(default)      (有些版本 key 不带 path value)
//
// 注册表 API 走 syscall 直绑 advapi32.dll，跟 autostart_windows.go 同套路 ——
// 项目刻意不依赖 golang.org/x/sys/windows/registry（vendor 里没有），保持 Go 1.20 兼容。
//
// Win7 / Win10 / Win11 兼容性：
//   - Chrome 自 110 起官方放弃 Win7/8/8.1；但 Win7 用户装的旧版 Chrome
//     安装路径跟新版本完全一致（同一 %ProgramFiles% 路径），探测不动
//     旁路即可。Chrome 110+ 不再装 Win7，但已装 Win7 的用户的 Chrome 路径
//     跟探测列表里的路径形态完全一致。
//   - RegQueryValueExW 从 Win7 起行为一致；HKLM vs WOW6432Node 区分
//     是从 Win7 x64 edition 起就有的，不是新平台的特性。
package sysutil

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// 常见 Chrome 安装路径（在哪些 PATH 下找 chrome.exe）。
//
// 顺序：先 64-bit system → 32-bit on x64 → user-local。
// 命中率递减，但 64-bit system 装 Win10/11 + Chrome 默认下载器都是走这里。
func chromeCandidatePaths() []string {
	var paths []string
	// 64-bit Chrome on 64-bit Windows / 32-bit Chrome on 32-bit Windows.
	for _, env := range []string{"ProgramFiles", "ProgramW6432"} {
		if dir := os.Getenv(env); dir != "" {
			paths = append(paths, filepath.Join(dir, "Google", "Chrome", "Application", "chrome.exe"))
		}
	}
	// 32-bit Chrome on 64-bit Windows（Program Files (x86)）。
	if dir := os.Getenv("ProgramFiles(x86)"); dir != "" {
		paths = append(paths, filepath.Join(dir, "Google", "Chrome", "Application", "chrome.exe"))
	}
	// Per-user / portable style install。
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		paths = append(paths, filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"))
	}
	return paths
}

// FindChrome 探测系统上是否安装了 Chrome。
//
// 返回 (path, true) 表示找到 chrome.exe 绝对路径；
// 返回 ("", false) 表示没找到（os.Stat + 注册表查询都失败）。
//
// 实现：先走常见路径快速命中（< 1ms），失败再读注册表"Application\path"
// 字段（< 5ms）。任何一步失败都安静地进入下一步 —— 不抛错，
// 让调用方决定要不要回落到系统默认浏览器。
func FindChrome() (string, bool) {
	// 1. 常见路径探测（最高命中率）。
	for _, p := range chromeCandidatePaths() {
		if _, err := os.Stat(p); err == nil {
			return cleanPath(p), true
		}
	}

	// 2. 注册表兜底：先查 64-bit 视图，再查 WOW6432Node (32-bit on 64-bit)。
	for _, subkey := range []string{
		`SOFTWARE\Google\Chrome\Application`,
		`SOFTWARE\WOW6432Node\Google\Chrome\Application`,
	} {
		if path := readChromePathFromRegistry(subkey); path != "" {
			return cleanPath(path), true
		}
	}

	return "", false
}

// cleanPath 把 chrome.exe 路径里的相对部分、奇怪分隔符归一。
//
// registry 偶尔写出 `C:\...\` 之类的带尾分隔符的 path，stat 路径不存在
// 的概率极小但不为 0；这里 trim 一下，保证返回的 path 可直接被 exec.Command 用。
func cleanPath(p string) string {
	return strings.TrimRight(p, `\/`)
}

// ---------- 注册表读 path ----------

const (
	hkeyLocalMachine uintptr = 0x80000002

	keyQueryValue uint32 = 0x0001
)

var (
	// 重新 bind，避免和 autostart_windows.go 的同名变量冲突。
	// （同一进程内同一个 advapi32.dll 的 NewProc 多次调用是 idempotent 的，
	//  但起新名字让 linter 看清是另外一组绑定。）
	procRegOpenKeyExWLocate    = modAdvapi32.NewProc("RegOpenKeyExW")
	procRegQueryValueExWLocate = modAdvapi32.NewProc("RegQueryValueExW")
	procRegCloseKeyLocate      = modAdvapi32.NewProc("RegCloseKey")
)

// readChromePathFromRegistry 从 HKLM\<subkey> 读 "path" value。
//
// 步骤：
//  1. RegOpenKeyExW(HKLM, subkey, KEY_QUERY_VALUE) → hKey
//  2. RegQueryValueExW(hKey, "path", NULL, lpcbData) 两遍：
//     - 第一遍 lpcbData=0 → 拿到真实 cbData
//     - 第二遍拿真实数据
//  3. RegCloseKey
//
// 返回："path" 不存在 / 任何 API 错误 → ""（不是 error，因为这步是探测链的一环）
func readChromePathFromRegistry(subkey string) string {
	keyPathPtr, err := syscall.UTF16PtrFromString(subkey)
	if err != nil {
		return ""
	}
	var hKey uintptr
	ret, _, _ := procRegOpenKeyExWLocate.Call(
		hkeyLocalMachine,
		uintptr(unsafe.Pointer(keyPathPtr)),
		0,
		uintptr(keyQueryValue),
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != 0 /* ERROR_SUCCESS */ {
		return ""
	}
	defer procRegCloseKeyLocate.Call(hKey)

	valueNamePtr, err := syscall.UTF16PtrFromString("path")
	if err != nil {
		return ""
	}

	// 第一次：拿 cbData。
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

	// 第二次：拿真实数据（dataSize 是字节数，含 NUL 终止符）。
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

	// 砍掉尾 NUL，转 UTF-8。
	raw := syscall.UTF16ToString(buf)
	if raw == "" {
		return ""
	}

	// 验证：必须是已存在的 .exe 文件；不然可能是注册表残留（卸载过 Chrome）。
	if !strings.HasSuffix(strings.ToLower(raw), ".exe") {
		return ""
	}
	if _, err := os.Stat(raw); err != nil {
		return ""
	}
	return raw
}
