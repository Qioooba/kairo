//go:build windows

// Package sysutil / autostart_windows.go — Windows 平台的"开机自启"实现。
//
// 方案：用户级 Run 键（HKCU\...\Run），Win7/10/11 三代行为一致：
//
//	HKEY_CURRENT_USER
//	  Software\Microsoft\Windows\CurrentVersion\Run
//	    <valueName> = "<exePath>"
//
// 选择理由：
//
//   - 不需要管理员权限（HKLM\...\Run 需要 UAC），Kairo 是自用工具，默认 user 跑。
//   - Run 键值在所有 Windows 桌面版本（Win7 起）都受支持，无 API 差异。
//   - 值就是 exe 完整路径，简单清晰；不带参数（auto_open_browser 等参数由配置
//     决定，不需要从命令行覆盖）。
//   - 用户级 HKCU 删除/重装 Kairo 时不会留垃圾（HKLM 会）。
//
// 已知 Win7 上的"差异"已经被 Run 键实现吸收：Run 键 API 在 Win7/10/11 完全一样。
//
// 注册表 API 用 syscall 直接调 advapi32.dll，避免依赖 golang.org/x/sys/windows/registry
// （项目 vendor 里没有这个子包，go.mod 锁在 Go 1.20，加新依赖成本高）。
package sysutil

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	// autoStartRunKey 是要打开/创建的注册表子键。
	// 注意：HKCU\Software\... 路径下不需要管理员权限。
	autoStartRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

	// autoStartValueName 是 Run 键下面的值名。
	// 用「产品名+工具名+功能」的复合命名（KairoOpsToolboxAutoStart），
	// 跟外部程序撞名的概率几乎为 0 —— 这样升级场景下"看注册表里有没有这个值"
	// 的判断可以完全信赖，不存在误接管。
	autoStartValueName = "KairoOpsToolboxAutoStart"
)

// advapi32.dll 内的注册表 API 常量 + syscall 绑定。
//
// HKEY 类型：HKEY 在 Windows API 是 HANDLE 的特殊形式（最高位 bit 区分 predefined/user-defined）。
// 下面 HKEY_CURRENT_USER = 0x80000001 是 predefined handle。
//
// RegOpenKeyExW / RegSetValueExW / RegDeleteValueW / RegQueryValueExW / RegCloseKey
// 都是 Unicode（W 结尾）版本。Win7 起默认走 Unicode，没有 ANSI 兼容问题。
const (
	hkeyCurrentUser uintptr = 0x80000001

	keyRead  uint32 = 0x20019 // KEY_READ = STANDARD_RIGHTS_READ | KEY_QUERY_VALUE | KEY_ENUMERATE_SUB_KEYS | KEY_NOTIFY
	keyWrite uint32 = 0x20006 // KEY_WRITE = STANDARD_RIGHTS_WRITE | KEY_SET_VALUE | KEY_CREATE_SUB_KEY

	regSz uint32 = 1 // REG_SZ 类型：以 NUL 结尾的 Unicode 字符串
)

// 注册表 API 返回值常量（必须是 uintptr —— Go 的 syscall.Syscall 把所有返回值塞进 uintptr）。
const (
	regSuccess      uintptr = 0 // ERROR_SUCCESS
	regFileNotFound uintptr = 2 // ERROR_FILE_NOT_FOUND（值 / 键 不存在）
)

var (
	modAdvapi32 = syscall.NewLazyDLL("advapi32.dll")

	procRegOpenKeyExW    = modAdvapi32.NewProc("RegOpenKeyExW")
	procRegSetValueExW   = modAdvapi32.NewProc("RegSetValueExW")
	procRegDeleteValueW  = modAdvapi32.NewProc("RegDeleteValueW")
	procRegQueryValueExW = modAdvapi32.NewProc("RegQueryValueExW")
	procRegCloseKey      = modAdvapi32.NewProc("RegCloseKey")
)

// utf16Ptr 把 Go string 转成 *uint16（Windows Unicode API 入参）。
//
// 用 syscall.UTF16PtrFromString 而不是 unsafe.Pointer(&[]uint16(str)[0])：
//   - UTF16PtrFromString 内部已经加了 NUL 终止符；
//   - 出错会返回 error，调用方更好处理。
func utf16Ptr(s string) (*uint16, error) {
	return syscall.UTF16PtrFromString(s)
}

// writeRegSz 把 string 当作 REG_SZ 写到已打开的注册表键值。
//
// 关键点：cbData 必须是 UTF-16 编码后的字节数（含 NUL 终止符）——
// 调 RegSzCbData 计算（不依赖注册表 API 的纯函数，可跨平台单测）。
// 不能用 len(s)*2 替代：s 是 UTF-8 字节数，跟 UTF-16 code unit 数在
// 非 ASCII 路径下不等（BMP 内 CJK 3 字节 vs 1 unit；BMP 外字符 4 字节 vs 2 units）。
// 旧实现用 (len(s)+1)*2 在纯 ASCII 路径下巧合正确，中文/emoji 路径下 cbData 偏大
// 4n 字节导致 RegSetValueExW 越界读 buffer 外的内存。
func writeRegSz(hKey uintptr, valueName *uint16, s string) error {
	cbData, err := RegSzCbData(s)
	if err != nil {
		return err
	}
	utf16, err := syscall.UTF16FromString(s)
	if err != nil {
		return fmt.Errorf("转 UTF16 失败: %w", err)
	}
	ret, _, _ := procRegSetValueExW.Call(
		hKey,
		uintptr(unsafe.Pointer(valueName)),
		0,
		uintptr(regSz),
		uintptr(unsafe.Pointer(&utf16[0])),
		uintptr(cbData),
	)
	if ret != regSuccess {
		return fmt.Errorf("RegSetValueExW 失败 (ret=%d)", ret)
	}
	return nil
}

// SetAutoStart 启用或禁用 Kairo 的开机自启。
//
// enabled = true  → 在 HKCU\...\Run\<valueName> 写 exePath（REG_SZ）
// enabled = false → 删除该值（存在则删，不存在视作成功）
// exePath 在 enabled=true 时必填；空字符串会返回 error，避免写一个空路径到注册表。
//
// 返回值：
//   - nil: 操作成功（即使 enabled=false 时值本来就不存在，也算成功）
//   - non-nil: 系统级失败（权限不够、注册表被锁、exePath 为空等）
//
// 调用方约定：失败要让前端看到，不要吞掉。
func SetAutoStart(enabled bool, exePath string) error {
	if !enabled {
		return deleteAutoStartValue()
	}
	if exePath == "" {
		return fmt.Errorf("启用自启需要 exePath，不能为空")
	}
	return setAutoStartValue(exePath)
}

// setAutoStartValue 写一个 REG_SZ 值到 HKCU\...\Run\<valueName>。
//
// 用 KEY_SET_VALUE 打开就够了：RegSetValueExW 不需要 KEY_WRITE 全集。
// 如果父键（...Run）不存在，RegOpenKeyExW 会失败；这种情况极少（Run 键系统自带），
// 但兜底做一次：失败时尝试 RegCreateKeyExW 创建父键，再写入。
//
// 值会用双引号包住（quoteExePath），防止含空格的路径被 Windows 按
// 命令行规则切分（path planting）。
func setAutoStartValue(exePath string) error {
	valueNamePtr, err := utf16Ptr(autoStartValueName)
	if err != nil {
		return fmt.Errorf("valueName 转 UTF16 失败: %w", err)
	}
	// 1. 打开 Run 子键（HKEY_CURRENT_USER 是 predefined handle，不需要 close）。
	var hKey uintptr
	keyPathPtr, err := utf16Ptr(autoStartRunKey)
	if err != nil {
		return fmt.Errorf("keyPath 转 UTF16 失败: %w", err)
	}
	// RegOpenKeyExW(hKey, lpSubKey, ulOptions, samDesired, phkResult)
	// 第 3、4 参数：Reserved=0, sam=KEY_SET_VALUE(=0x0002，KEY_SET_VALUE 在 winnt.h 是 0x0002)
	const keySetValue uint32 = 0x0002
	ret, _, _ := procRegOpenKeyExW.Call(
		hkeyCurrentUser,
		uintptr(unsafe.Pointer(keyPathPtr)),
		0,
		uintptr(keySetValue),
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != regSuccess {
		// 打开失败：尝试 RegCreateKeyExW 创建 Run 键（极少触发；正常机器 Run 键都在）。
		if createErr := createRunKey(&hKey); createErr != nil {
			return fmt.Errorf("打开 Run 键失败 (ret=%d)，尝试创建也失败: %w", ret, createErr)
		}
	}
	defer procRegCloseKey.Call(hKey)

	// 2. 写带引号的 exe 路径。cbData 由 writeRegSz 内部按真实 UTF-16 长度算。
	return writeRegSz(hKey, valueNamePtr, QuoteExePath(exePath))
}

// createRunKey 调 RegCreateKeyExW 创建 HKCU\...\Run 子键。
//
// advapi32 的 RegCreateKeyExW 多参数，绑定 syscall 较啰嗦，这里用最简路径：
// 只在 RegOpenKeyExW 失败时调用，正常机器 Run 键都存在，不会触发。
func createRunKey(hKey *uintptr) error {
	keyPathPtr, err := utf16Ptr(autoStartRunKey)
	if err != nil {
		return fmt.Errorf("keyPath 转 UTF16 失败: %w", err)
	}
	var disposition uint32
	procRegCreateKeyExW := modAdvapi32.NewProc("RegCreateKeyExW")
	// RegCreateKeyExW(hKey, lpSubKey, Reserved, lpClass, dwOptions, samDesired,
	//                  lpSecurityAttributes, phkResult, lpdwDisposition)
	// samDesired 用 KEY_SET_VALUE(0x0002) 就够了 —— SetValueEx 不需要更多权限。
	const keySetValue uint32 = 0x0002
	ret, _, _ := procRegCreateKeyExW.Call(
		hkeyCurrentUser,
		uintptr(unsafe.Pointer(keyPathPtr)),
		0,
		0, // lpClass = NULL
		0, // dwOptions = REG_OPTION_NON_VOLATILE(=0)
		uintptr(keySetValue),
		0, // lpSecurityAttributes = NULL
		uintptr(unsafe.Pointer(hKey)),
		uintptr(unsafe.Pointer(&disposition)),
	)
	if ret != regSuccess {
		return fmt.Errorf("RegCreateKeyExW 失败 (ret=%d)", ret)
	}
	return nil
}

// deleteAutoStartValue 删除 HKCU\...\Run\<valueName>。
//
// 用 KEY_SET_VALUE 打开就够 RegDeleteValueW。值不存在时（ERROR_FILE_NOT_FOUND=2）
// 视为成功 —— 用户可能本来就没启自启，只是重复取消。
func deleteAutoStartValue() error {
	valueNamePtr, err := utf16Ptr(autoStartValueName)
	if err != nil {
		return fmt.Errorf("valueName 转 UTF16 失败: %w", err)
	}
	keyPathPtr, err := utf16Ptr(autoStartRunKey)
	if err != nil {
		return fmt.Errorf("keyPath 转 UTF16 失败: %w", err)
	}
	var hKey uintptr
	const keySetValue uint32 = 0x0002
	ret, _, _ := procRegOpenKeyExW.Call(
		hkeyCurrentUser,
		uintptr(unsafe.Pointer(keyPathPtr)),
		0,
		uintptr(keySetValue),
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != regSuccess {
		// Run 键不存在 = 自启肯定没设过，视为成功。
		if ret == regFileNotFound {
			return nil
		}
		return fmt.Errorf("打开 Run 键失败 (ret=%d)", ret)
	}
	defer procRegCloseKey.Call(hKey)

	ret, _, _ = procRegDeleteValueW.Call(
		hKey,
		uintptr(unsafe.Pointer(valueNamePtr)),
	)
	if ret == regFileNotFound {
		return nil
	}
	if ret != regSuccess {
		return fmt.Errorf("RegDeleteValueW 失败 (ret=%d)", ret)
	}
	return nil
}

// IsAutoStartEnabled 检查注册表里 Kairo 的自启值是否存在。
//
// 返回 (true, nil) 表示已启用；(false, nil) 表示未启用；(false, err) 表示查询失败。
//
// 实现：RegQueryValueExW + lpcbData 探测长度 —— 比 CreateFile+ReadFile 简单，且
// 不需要临时分配。
func IsAutoStartEnabled() (bool, error) {
	valueNamePtr, err := utf16Ptr(autoStartValueName)
	if err != nil {
		return false, fmt.Errorf("valueName 转 UTF16 失败: %w", err)
	}
	keyPathPtr, err := utf16Ptr(autoStartRunKey)
	if err != nil {
		return false, fmt.Errorf("keyPath 转 UTF16 失败: %w", err)
	}
	var hKey uintptr
	const keyQueryValue uint32 = 0x0001
	ret, _, _ := procRegOpenKeyExW.Call(
		hkeyCurrentUser,
		uintptr(unsafe.Pointer(keyPathPtr)),
		0,
		uintptr(keyQueryValue),
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret == regFileNotFound {
		return false, nil
	}
	if ret != regSuccess {
		return false, fmt.Errorf("打开 Run 键失败 (ret=%d)", ret)
	}
	defer procRegCloseKey.Call(hKey)

	// RegQueryValueExW(hKey, lpValueName, Reserved, lpType, lpData, lpcbData)
	// 第一次传 lpData=NULL + lpcbData=0，返回后会写 lpcbData 为所需大小（含 NUL）。
	// 我们只想知道"值是否存在 + 大小是否 > 0"，不需要读实际内容。
	var dataType uint32
	var dataSize uint32
	ret, _, _ = procRegQueryValueExW.Call(
		hKey,
		uintptr(unsafe.Pointer(valueNamePtr)),
		0,
		uintptr(unsafe.Pointer(&dataType)),
		0,
		uintptr(unsafe.Pointer(&dataSize)),
	)
	if ret == regFileNotFound {
		return false, nil
	}
	if ret != regSuccess {
		return false, fmt.Errorf("RegQueryValueExW 失败 (ret=%d)", ret)
	}
	// dataSize 是字节数（含 NUL 终止符 = 2 字节）。空值视作未启用。
	return dataSize > 2, nil
}

// AutoStartKeyName 返回注册表值的可读名，给 audit 日志用。
//
// 例：HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Run\Kairo
func AutoStartKeyName() string {
	return `HKEY_CURRENT_USER\` + autoStartRunKey + `\` + autoStartValueName
}

// Supported Windows 平台支持自启（写 HKCU Run 键，Win7/10/11 都通用）。
func Supported() bool {
	return true
}

// PlatformName 永远返回 "windows"，给 /api/admin/autostart 响应用。
func PlatformName() string {
	return "windows"
}