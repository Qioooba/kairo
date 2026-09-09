//go:build windows

package dbconsole

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modadvapi32          = syscall.NewLazyDLL("advapi32.dll")
	procRegOpenKeyExW    = modadvapi32.NewProc("RegOpenKeyExW")
	procRegQueryValueExW = modadvapi32.NewProc("RegQueryValueExW")
	procRegCloseKey      = modadvapi32.NewProc("RegCloseKey")
	procRegEnumKeyExW    = modadvapi32.NewProc("RegEnumKeyExW")
)

const (
	hkeyCurrentUser  uintptr = 0x80000001
	hkeyLocalMachine uintptr = 0x80000002

	keyReadStandard uint32 = 0x20019 // STANDARD_RIGHTS_READ | KEY_QUERY_VALUE | KEY_ENUMERATE_SUB_KEYS
	keyWow6464      uint32 = 0x0100  // KEY_WOW64_64KEY
	keyWow6432      uint32 = 0x0200  // KEY_WOW64_32KEY

	regSZ       uint32  = 1
	regExpandSZ uint32  = 2
	errSuccess  uintptr = 0
	errNoMore   uintptr = 259
	errNotFound uintptr = 2
)

func winRegOpenKey(rootKey uintptr, subKey string, access uint32) (uintptr, error) {
	subPtr, err := syscall.UTF16PtrFromString(subKey)
	if err != nil {
		return 0, err
	}
	var hKey uintptr
	r0, _, _ := procRegOpenKeyExW.Call(rootKey, uintptr(unsafe.Pointer(subPtr)), 0, uintptr(access), uintptr(unsafe.Pointer(&hKey)))
	if r0 != errSuccess {
		return 0, fmt.Errorf("RegOpenKeyExW failed: error %d", r0)
	}
	return hKey, nil
}

func winRegCloseKey(hKey uintptr) {
	if hKey != 0 {
		procRegCloseKey.Call(hKey)
	}
}

func winRegGetString(hKey uintptr, valName string) (string, error) {
	var valPtr *uint16
	var err error
	if valName != "" {
		valPtr, err = syscall.UTF16PtrFromString(valName)
		if err != nil {
			return "", err
		}
	}
	var valType uint32
	var byteLen uint32 = 1024
	buf := make([]uint16, 512)
	r0, _, _ := procRegQueryValueExW.Call(
		hKey,
		uintptr(unsafe.Pointer(valPtr)),
		0,
		uintptr(unsafe.Pointer(&valType)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&byteLen)),
	)
	if r0 != errSuccess {
		return "", fmt.Errorf("RegQueryValueExW failed: %d", r0)
	}
	if valType != regSZ && valType != regExpandSZ {
		return "", fmt.Errorf("not a string type: %d", valType)
	}
	str := syscall.UTF16ToString(buf)
	if valType == regExpandSZ {
		str = os.ExpandEnv(str)
	}
	return strings.TrimSpace(str), nil
}

func winRegEnumSubKeys(hKey uintptr) []string {
	var results []string
	var index uint32 = 0
	for {
		var nameLen uint32 = 256
		buf := make([]uint16, nameLen)
		r0, _, _ := procRegEnumKeyExW.Call(
			hKey,
			uintptr(index),
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&nameLen)),
			0, 0, 0, 0,
		)
		if r0 == errNoMore {
			break
		}
		if r0 != errSuccess {
			break
		}
		results = append(results, syscall.UTF16ToString(buf[:nameLen]))
		index++
	}
	return results
}

// DetectSystemOracleClients 执行针对 Windows 企业环境的深度探测：
// 1. PL/SQL Developer 注册表偏好 (HKCU/HKLM)、安装目录及 plsqldev.exe 位数
// 2. Oracle 注册表标准安装 (HKLM\SOFTWARE\ORACLE 及其 32/64 位视图中的所有 KEY_*)
// 3. 环境变量 (ORACLE_HOME, TNS_ADMIN, OCI_LIB_DIR, PATH)
// 4. 标准候选安装路径
func DetectSystemOracleClients(source Source) (candidates []OracleCandidateClient, plsqlDetected bool, plsqlBitness string, plsqlDetail string) {
	seenPaths := make(map[string]struct{})
	addCandidate := func(path, label string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		norm := strings.ToLower(filepath.Clean(path))
		if _, exists := seenPaths[norm]; exists {
			return
		}
		seenPaths[norm] = struct{}{}
		if c := EvaluateCandidate(path, label); c != nil {
			candidates = append(candidates, *c)
		}
	}

	// 1. 数据源显式指定的 libDir（若未来或当前指定）
	if strings.TrimSpace(source.OracleLibDir) != "" {
		addCandidate(source.OracleLibDir, "数据源显式指定 (OracleLibDir)")
	}

	// 2. 探测 PL/SQL Developer 注册表与安装
	plsqlDetected, plsqlBitness, plsqlDetail = detectPLSQLDeveloper(addCandidate)

	// 3. 探测 Windows 注册表中 Oracle 官方 Home
	detectOracleRegistryHomes(addCandidate)

	// 4. 环境变量
	if v := strings.TrimSpace(os.Getenv("ORACLE_HOME")); v != "" {
		addCandidate(v, "环境变量 ORACLE_HOME")
		addCandidate(filepath.Join(v, "bin"), "环境变量 ORACLE_HOME/bin")
	}
	if v := strings.TrimSpace(os.Getenv("OCI_LIB_DIR")); v != "" {
		addCandidate(v, "环境变量 OCI_LIB_DIR")
	}

	// 5. PATH 环境变量
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// 快速判断是否有 oci.dll，避免全盘 stat 开销
		if _, err := os.Stat(filepath.Join(p, "oci.dll")); err == nil {
			addCandidate(p, "系统 PATH 包含目录")
		}
	}

	// 6. 常见本地路径启发式搜索（含 XE、19c、21c、Instant Client）
	standardDirs := []string{
		`D:\app\oracle\product\21.0.0\dbhomeXE\bin`,
		`C:\app\oracle\product\21.0.0\dbhomeXE\bin`,
		`C:\oracle\instantclient_21_12`,
		`C:\oracle\instantclient_21_11`,
		`C:\oracle\instantclient_21_10`,
		`C:\oracle\instantclient_19_24`,
		`C:\oracle\instantclient_19_23`,
		`C:\oracle\instantclient_19_20`,
		`C:\oracle\instantclient_19_19`,
		`C:\oracle\instantclient`,
		`D:\oracle\instantclient_19_20`,
		`D:\oracle\instantclient_21_12`,
		`D:\oracle\instantclient`,
		`C:\instantclient_21_12`,
		`C:\instantclient_19_20`,
		`C:\instantclient`,
		`D:\instantclient`,
	}
	for _, sd := range standardDirs {
		addCandidate(sd, "标准 Oracle 安装目录")
	}

	return candidates, plsqlDetected, plsqlBitness, plsqlDetail
}

func detectPLSQLDeveloper(addCandidate func(path, label string)) (detected bool, bitness string, detail string) {
	// 常见 PL/SQL 注册表基路径
	baseKeys := []string{
		`Software\Allround Automations\PL/SQL Developer`,
		`Software\Allround Automations\PL/SQL Developer 15`,
		`Software\Allround Automations\PL/SQL Developer 14`,
		`Software\Allround Automations\PL/SQL Developer 13`,
		`Software\Allround Automations\PL/SQL Developer 12`,
		`Software\Allround Automations\PL/SQL Developer 11`,
	}

	subPreferencePaths := []string{
		"",
		`\Preferences\Oracle`,
		`\PreferenceSets\Default\Oracle`,
	}

	var foundHomes []string
	var foundOCIs []string
	var installDirs []string

	// 检查 HKCU 与 HKLM（含 32/64 位视图）
	views := []struct {
		root   uintptr
		access uint32
		desc   string
	}{
		{hkeyCurrentUser, keyReadStandard, "HKCU"},
		{hkeyCurrentUser, keyReadStandard | keyWow6464, "HKCU (64-bit)"},
		{hkeyCurrentUser, keyReadStandard | keyWow6432, "HKCU (32-bit)"},
		{hkeyLocalMachine, keyReadStandard, "HKLM"},
		{hkeyLocalMachine, keyReadStandard | keyWow6464, "HKLM (64-bit)"},
		{hkeyLocalMachine, keyReadStandard | keyWow6432, "HKLM (32-bit)"},
	}

	for _, v := range views {
		for _, bk := range baseKeys {
			for _, sub := range subPreferencePaths {
				fullKey := bk + sub
				hKey, err := winRegOpenKey(v.root, fullKey, v.access)
				if err != nil {
					continue
				}
				detected = true
				// 查询可能配置的 OracleHome / OCI Library
				for _, valName := range []string{"OCI", "OCIDLL", "OCILibrary", "OCIFile"} {
					if s, err := winRegGetString(hKey, valName); err == nil && s != "" {
						foundOCIs = append(foundOCIs, s)
					}
				}
				for _, valName := range []string{"OracleHome", "ORACLE_HOME"} {
					if s, err := winRegGetString(hKey, valName); err == nil && s != "" {
						foundHomes = append(foundHomes, s)
					}
				}
				for _, valName := range []string{"Path", "InstallPath", "Install_Dir"} {
					if s, err := winRegGetString(hKey, valName); err == nil && s != "" {
						installDirs = append(installDirs, s)
					}
				}
				winRegCloseKey(hKey)
			}
		}
	}

	// 磁盘上常见 PL/SQL 安装路径
	diskInstallDirs := []string{
		`C:\Program Files\PLSQL Developer 15`,
		`C:\Program Files\PLSQL Developer 14`,
		`C:\Program Files\PLSQL Developer 13`,
		`C:\Program Files\PLSQL Developer`,
		`C:\Program Files (x86)\PLSQL Developer 15`,
		`C:\Program Files (x86)\PLSQL Developer 14`,
		`C:\Program Files (x86)\PLSQL Developer 13`,
		`C:\Program Files (x86)\PLSQL Developer 12`,
		`C:\Program Files (x86)\PLSQL Developer 11`,
		`C:\Program Files (x86)\PLSQL Developer`,
		`D:\Program Files\PLSQL Developer 15`,
		`D:\Program Files\PLSQL Developer 14`,
		`D:\Program Files\PLSQL Developer`,
		`D:\Program Files (x86)\PLSQL Developer 15`,
		`D:\Program Files (x86)\PLSQL Developer 14`,
		`D:\Program Files (x86)\PLSQL Developer 12`,
		`D:\Program Files (x86)\PLSQL Developer 11`,
		`D:\Program Files (x86)\PLSQL Developer`,
	}
	installDirs = append(installDirs, diskInstallDirs...)

	// 检测 plsqldev.exe 位数
	for _, dir := range installDirs {
		exe := filepath.Join(dir, "plsqldev.exe")
		if _, err := os.Stat(exe); err == nil {
			detected = true
			if b, _, err := InspectPEArchitecture(exe); err == nil && b != "unknown" {
				bitness = b
			}
			// PL/SQL 目录下有时直接内嵌 instantclient
			addCandidate(filepath.Join(dir, "instantclient"), "PL/SQL 内置 instantclient")
			addCandidate(filepath.Join(dir, "instantclient_19_20"), "PL/SQL 内置 instantclient")
			addCandidate(dir, "PL/SQL 安装目录")
		}
	}

	for _, oci := range foundOCIs {
		addCandidate(oci, "PL/SQL Developer 注册表 OCI 配置")
		addCandidate(filepath.Dir(oci), "PL/SQL Developer 注册表 OCI 目录")
	}
	for _, home := range foundHomes {
		addCandidate(home, "PL/SQL Developer 注册表 OracleHome")
		addCandidate(filepath.Join(home, "bin"), "PL/SQL Developer 注册表 OracleHome/bin")
	}

	if detected {
		if bitness == "" {
			bitness = "unknown"
		}
		if bitness == "32-bit" && runtime.GOARCH == "amd64" {
			detail = "检测到本机安装了 32 位 PL/SQL Developer。注意：32 位 PL/SQL 使用 32 位 Oracle Client，而 64 位 Kairo 无法跨位数加载 32 位 DLL。若未安装 64 位 Instant Client，系统将自动使用纯 Go (go-ora) 驱动保障连接。"
		} else if bitness == "64-bit" {
			detail = fmt.Sprintf("检测到 64 位 PL/SQL Developer，位数与 Kairo (%s) 一致", runtime.GOARCH)
		} else {
			detail = "检测到已安装 PL/SQL Developer"
		}
	}

	return detected, bitness, detail
}

func detectOracleRegistryHomes(addCandidate func(path, label string)) {
	views := []struct {
		root   uintptr
		access uint32
		label  string
	}{
		{hkeyLocalMachine, keyReadStandard | keyWow6464, "HKLM 64-bit"},
		{hkeyLocalMachine, keyReadStandard | keyWow6432, "HKLM 32-bit (WOW6432Node)"},
	}

	for _, v := range views {
		hOracle, err := winRegOpenKey(v.root, `Software\ORACLE`, v.access)
		if err != nil {
			continue
		}
		// 检查 Software\ORACLE 本身是否有 ORACLE_HOME
		if s, err := winRegGetString(hOracle, "ORACLE_HOME"); err == nil && s != "" {
			addCandidate(s, "Oracle 注册表主键 "+v.label)
			addCandidate(filepath.Join(s, "bin"), "Oracle 注册表主键 bin "+v.label)
		}
		// 枚举所有子项 (KEY_OraDB..., KEY_OraClient..., KEY_XE 等)
		subKeys := winRegEnumSubKeys(hOracle)
		for _, sk := range subKeys {
			if hSub, err := winRegOpenKey(hOracle, sk, v.access); err == nil {
				if home, err := winRegGetString(hSub, "ORACLE_HOME"); err == nil && home != "" {
					addCandidate(home, fmt.Sprintf("Oracle 注册表 %s (%s)", sk, v.label))
					addCandidate(filepath.Join(home, "bin"), fmt.Sprintf("Oracle 注册表 %s/bin (%s)", sk, v.label))
				}
				winRegCloseKey(hSub)
			}
		}
		winRegCloseKey(hOracle)
	}
}
