package dbconsole

import (
	"debug/pe"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// OracleCandidateClient 记录探测到的单个候选客户端详情
type OracleCandidateClient struct {
	Path          string `json:"path"`
	OciDllPath    string `json:"oci_dll_path"`
	Bitness       string `json:"bitness"`              // "64-bit" / "32-bit" / "unknown"
	Compatible    bool   `json:"compatible"`           // 是否与当前进程架构匹配
	Source        string `json:"source"`               // 来源说明 (PL/SQL Developer / Oracle Registry / ORACLE_HOME 等)
	ConfigDir     string `json:"config_dir,omitempty"` // tnsnames.ora 目录
	HasTNS        bool   `json:"has_tns"`
	HasClientDLLs bool   `json:"has_client_dlls"`
	Version       string `json:"version,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

// InspectPEArchitecture 使用标准库 debug/pe 读取 DLL / EXE 的 PE 机器架构。
// 支持判断 32 位 (i386) 与 64 位 (x64 / ARM64)。
func InspectPEArchitecture(path string) (bitness string, isCompatible bool, err error) {
	f, err := pe.Open(path)
	if err != nil {
		return "unknown", false, err
	}
	defer f.Close()

	curArch := runtime.GOARCH
	switch f.FileHeader.Machine {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		return "64-bit", curArch == "amd64", nil
	case pe.IMAGE_FILE_MACHINE_ARM64:
		return "64-bit", curArch == "arm64", nil
	case pe.IMAGE_FILE_MACHINE_I386:
		return "32-bit", curArch == "386", nil
	default:
		return "unknown", false, nil
	}
}

// FindTNSAdmin 在给定客户端目录及上下级标准位置查找 tnsnames.ora / sqlnet.ora 目录。
func FindTNSAdmin(clientDir string) (string, bool) {
	if clientDir == "" {
		return "", false
	}
	// 环境变量 TNS_ADMIN 优先
	if tnsAdmin := strings.TrimSpace(os.Getenv("TNS_ADMIN")); tnsAdmin != "" {
		if hasTNSFiles(tnsAdmin) {
			return tnsAdmin, true
		}
	}

	candidates := []string{
		filepath.Join(clientDir, "network", "admin"),
		filepath.Join(filepath.Dir(clientDir), "network", "admin"),
		clientDir,
	}
	for _, c := range candidates {
		if hasTNSFiles(c) {
			return c, true
		}
	}
	return "", false
}

func hasTNSFiles(dir string) bool {
	if dir == "" {
		return false
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	for _, name := range []string{"tnsnames.ora", "sqlnet.ora", "cwallet.sso", "ewallet.p12"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// CheckClientCompanionFiles 检查该目录下除 oci.dll 外是否包含基本的配套客户端文件。
// 避免仅单独拷贝一个孤立 oci.dll 导致缺少依赖运行时崩溃。
func CheckClientCompanionFiles(clientDir string) bool {
	if clientDir == "" {
		return false
	}
	if runtime.GOOS != "windows" {
		return true
	}
	entries, err := os.ReadDir(clientDir)
	if err != nil {
		return false
	}
	hasOCI := false
	hasCompanion := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if name == "oci.dll" {
			hasOCI = true
		}
		// Instant Client 或 Full Client 常见依赖
		if strings.HasPrefix(name, "oraociei") || strings.HasPrefix(name, "orannzsbb") ||
			strings.HasPrefix(name, "oraociicus") || strings.HasPrefix(name, "oran") ||
			strings.HasPrefix(name, "oramts") {
			hasCompanion = true
		}
	}
	return hasOCI && hasCompanion
}

// EvaluateCandidate 验证并填充一个候选路径的信息。
func EvaluateCandidate(path, sourceLabel string) *OracleCandidateClient {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}

	clientDir := path
	ociPath := ""
	if !fi.IsDir() {
		// 传进来的是文件（如 oci.dll）
		ociPath = path
		clientDir = filepath.Dir(path)
	} else {
		// 传进来的是目录
		cand := filepath.Join(clientDir, "oci.dll")
		if runtime.GOOS != "windows" {
			cand = filepath.Join(clientDir, "libclntsh.so")
		}
		if _, err := os.Stat(cand); err == nil {
			ociPath = cand
		} else {
			// 尝试子目录 bin
			subBin := filepath.Join(clientDir, "bin", "oci.dll")
			if _, err := os.Stat(subBin); err == nil {
				ociPath = subBin
				clientDir = filepath.Join(clientDir, "bin")
			}
		}
	}

	if ociPath == "" {
		return nil
	}

	bitness := "unknown"
	compatible := false
	var peErr error
	if runtime.GOOS == "windows" {
		bitness, compatible, peErr = InspectPEArchitecture(ociPath)
		_ = peErr
	} else {
		bitness = "64-bit"
		compatible = true
	}

	configDir, hasTns := FindTNSAdmin(clientDir)
	hasCompanion := CheckClientCompanionFiles(clientDir)

	detail := ""
	clientVer := ""
	if !compatible {
		detail = "位数不匹配：此客户端为 " + bitness + "，当前应用程序为 " + runtime.GOARCH + "（无法跨位数动态加载 DLL）"
	} else if !hasCompanion {
		detail = "缺少配套运行时 DLL（可能为孤立 oci.dll 文件，需完整 Instant Client 目录）"
	} else {
		// 校验 OCIClientVersion 符号与可用性，防范 DPI-1072 致命报错
		if runtime.GOOS == "windows" {
			ver, ok, vErr := InspectOCIClientVersion(ociPath)
			if !ok {
				compatible = false
				detail = fmt.Sprintf("Oracle Client 库不受支持（%v），已自动标记为不可用以防 DPI-1072", vErr)
			} else {
				clientVer = ver
				detail = fmt.Sprintf("可用并兼容（%s，客户端版本 %s）", bitness, ver)
			}
		} else {
			detail = "可用并兼容（" + bitness + "）"
		}
	}

	return &OracleCandidateClient{
		Path:          clientDir,
		OciDllPath:    ociPath,
		Bitness:       bitness,
		Compatible:    compatible,
		Source:        sourceLabel,
		ConfigDir:     configDir,
		HasTNS:        hasTns,
		HasClientDLLs: hasCompanion,
		Version:       clientVer,
		Detail:        detail,
	}
}

// SelectBestCandidate 从候选列表中挑选出最优且兼容的 Oracle Client。
// 优先条件：
//  1. 与当前进程架构匹配 (Compatible == true)
//  2. 含有完整配套 DLL (HasClientDLLs == true)
//  3. 含有 TNS 配置目录 (HasTNS == true)
//  4. 来源优先级 (显式配置 > 注册表标准安装/Instant Client > PL/SQL Developer > PATH)
func SelectBestCandidate(candidates []OracleCandidateClient, customLibDir string) *OracleCandidateClient {
	if customLibDir != "" {
		for i := range candidates {
			if strings.EqualFold(filepath.Clean(candidates[i].Path), filepath.Clean(customLibDir)) {
				return &candidates[i]
			}
		}
		// 若 custom 没在候选里，独立尝试评估
		if eval := EvaluateCandidate(customLibDir, "数据源显式配置 libDir"); eval != nil {
			return eval
		}
		return nil
	}

	var best *OracleCandidateClient
	bestScore := -1

	for i := range candidates {
		c := &candidates[i]
		if !c.Compatible {
			continue
		}
		score := 0
		if c.HasClientDLLs {
			score += 20
		}
		if c.HasTNS {
			score += 10
		}
		if strings.Contains(c.Source, "ORACLE_HOME") {
			score += 5
		}
		if strings.Contains(c.Source, "Registry") {
			score += 4
		}
		if strings.Contains(c.Source, "PL/SQL") {
			score += 3
		}

		if score > bestScore {
			bestScore = score
			best = c
		}
	}
	return best
}
