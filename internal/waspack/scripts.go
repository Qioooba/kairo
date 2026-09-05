package waspack

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

// BackupHomePrefix 是备份脚本里每条文件前面拼的生产 WAR 根。
// 页面不展示、也不让用户配；脚本格式必须和现有 BakTT*.sh 一致。
const backupHomeSuffix = "/credit.ear/credit.war"
const BackupHomePrefix = "$HOME" + backupHomeSuffix

func ExecuteScriptName(pkg string) string { return pkg + ".sh" }
func BackupScriptName(pkg string) string  { return "Bak" + pkg + ".sh" }
func TarFileName(pkg string) string       { return pkg + ".tar" }

func relNoDot(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	return strings.TrimPrefix(rel, "./")
}

// shellQuoteArg 把任意文件名编码成一个 POSIX shell 参数。
// 遇到单引号时关闭当前引用、插入字面单引号，再重新开启引用；返回值不依赖文件名字符集，
// 因而空格、通配符、变量、命令替换和控制运算符都不会被 shell 二次解释。
func shellQuoteArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

func backupFileArg(rel string) string {
	// HOME 必须在部署机展开，而清单路径必须保持纯字面量。相邻的双引号和
	// 单引号片段在 POSIX shell 中会合并为同一个 argv token。
	return `"$HOME"` + shellQuoteArg(backupHomeSuffix+"/"+relNoDot(rel))
}

func fileArgs(files []ResolvedFile, withDot bool) string {
	parts := make([]string, 0, len(files))
	for _, f := range files {
		rel := relNoDot(f.Rel)
		if withDot {
			parts = append(parts, shellQuoteArg("./"+rel))
		} else {
			parts = append(parts, backupFileArg(rel))
		}
	}
	return strings.Join(parts, " ")
}

// renderBackupScript 生成与现网一致的一行备份命令：
//
//	tar -cvf BakTT….tar $HOME/credit.ear/credit.war/CreditManage/… $HOME/credit.ear/credit.war/src/…
func renderBackupScript(pkgName string, files []ResolvedFile) string {
	return "tar -cvf Bak" + pkgName + ".tar " + fileArgs(files, false) + "\n"
}

// renderExecuteScript 生成与现网一致的一行执行命令：
//
//	tar -cvf TT….tar ./CreditManage/… ./src/… ./WEB-INF/classes/…
func renderExecuteScript(pkgName string, files []ResolvedFile) string {
	return "tar -cvf " + pkgName + ".tar " + fileArgs(files, true) + "\n"
}

const ChmodFileName = "chmod.txt"

func isSafeShellArg(arg string) bool {
	if arg == "" {
		return false
	}
	for _, r := range arg {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' || r == '/') {
			return false
		}
	}
	return true
}

func shellArg(arg string) string {
	if isSafeShellArg(arg) {
		return arg
	}
	return shellQuoteArg(arg)
}

func fileArgsBatch(files []ResolvedFile) string {
	parts := make([]string, 0, len(files))
	for _, f := range files {
		// 批量与应用一致：统一带 ./ 前缀（如 ./amargci/...、./AmarExtract/...），
		// 与预检清单、list.txt、tar 包四者保持完全一致。
		rel := "./" + relNoDot(f.Rel)
		parts = append(parts, shellArg(rel))
	}
	return strings.Join(parts, " ")
}

// renderBatchBackupScript 批量代码备份脚本：tar -cvf Bak<pkg>.tar ./<path1> ./<path2> ...
func renderBatchBackupScript(pkgName string, files []ResolvedFile) string {
	args := fileArgsBatch(files)
	if len(args) > 32*1024 {
		// list.txt is generated alongside the script. GNU/tar-compatible -T
		// reads paths without expanding them through argv, avoiding ARG_MAX.
		return "tar -cvf Bak" + pkgName + ".tar -T list.txt\n"
	}
	return "tar -cvf Bak" + pkgName + ".tar " + args + "\n"
}

// renderBatchBackupScriptAtRoot backs up the currently deployed batch files.
// It intentionally uses the deployment root (not the build/project root), so
// Bak*.sh can be run on the target host without accidentally archiving newly
// generated source files from a workstation.
func renderBatchBackupScriptAtRoot(pkgName string, files []ResolvedFile, baseDir string) (string, error) {
	base, err := validatePOSIXAbsolutePath(baseDir)
	if err != nil {
		return "", err
	}
	args := fileArgsBatch(files)
	if len(args) > 32*1024 {
		// Resolve both control files from the directory containing this script.
		// In particular, -C must not make tar look for list.txt under the
		// deployment root; list.txt is delivered beside Bak*.sh.
		const preamble = "SCRIPT_DIR=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\n"
		return preamble + "tar -C " + shellArg(base) + " -cvf \"$SCRIPT_DIR/Bak" + pkgName + ".tar\" -T \"$SCRIPT_DIR/list.txt\"\n", nil
	}
	return "tar -C " + shellArg(base) + " -cvf Bak" + pkgName + ".tar " + args + "\n", nil
}

// renderBatchExecuteScript 批量代码执行脚本：tar -cvf <pkg>.tar ./<path1> ./<path2> ...
func renderBatchExecuteScript(pkgName string, files []ResolvedFile) string {
	args := fileArgsBatch(files)
	if len(args) > 32*1024 {
		return "tar -cvf " + pkgName + ".tar -T list.txt\n"
	}
	return "tar -cvf " + pkgName + ".tar " + args + "\n"
}

// renderChmodScript 为所有 .sh 脚本生成 chmod 777 <baseDir>/<rel>
// validatePOSIXAbsolutePath validates the deployment path that is embedded in
// chmod.txt.  This value is executed on a Unix host, so accepting a Windows
// path, a relative path, or a path containing dot segments would be both
// misleading and an easy way to produce an unsafe script.
func validatePOSIXAbsolutePath(raw string) (string, error) {
	if raw == "" {
		raw = "/batch/credit"
	}
	if strings.TrimSpace(raw) != raw || strings.Contains(raw, "\\") {
		return "", fmt.Errorf("批量部署根路径必须是 POSIX 绝对路径")
	}
	for _, r := range raw {
		if r < 32 || r == 127 || unicode.IsControl(r) {
			return "", fmt.Errorf("批量部署根路径含非法控制字符")
		}
	}
	if !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("批量部署根路径必须以 / 开头")
	}
	base := strings.TrimRight(raw, "/")
	if base == "" {
		return "", fmt.Errorf("批量部署根路径不能是系统根目录")
	}
	// Reject, rather than silently normalise, dot segments and duplicate
	// separators. The generated script must refer to exactly the path the user
	// reviewed in the form.
	if path.Clean(base) != base || strings.Contains(base, "//") {
		return "", fmt.Errorf("批量部署根路径不能含 .、.. 或重复分隔符")
	}
	return base, nil
}

func validateScriptRelativePath(raw string) (string, error) {
	rel := relNoDot(raw)
	if rel == "" || strings.HasPrefix(rel, "/") || path.IsAbs(rel) {
		return "", fmt.Errorf("脚本路径必须是相对路径")
	}
	for _, r := range rel {
		if r < 32 || r == 127 || unicode.IsControl(r) {
			return "", fmt.Errorf("脚本路径含非法控制字符")
		}
	}
	clean := path.Clean(rel)
	if clean != rel || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("脚本路径越界")
	}
	return clean, nil
}

// renderChmodScriptSafe returns the chmod script and performs all validation.
// A plain wrapper is retained for package-local callers that only need a
// best-effort preview; generation paths always use the error-returning API.
func renderChmodScriptSafe(baseDir string, files []ResolvedFile, mode string) (string, error) {
	base, err := validatePOSIXAbsolutePath(baseDir)
	if err != nil {
		return "", err
	}
	if mode == "" {
		mode = "777" // existing deployment compatibility; explicit in Request/UI
	}
	if len(mode) != 3 && len(mode) != 4 {
		return "", fmt.Errorf("chmod 权限必须是 3 或 4 位八进制数字")
	}
	for _, r := range mode {
		if r < '0' || r > '7' {
			return "", fmt.Errorf("chmod 权限必须是八进制数字")
		}
	}
	var lines []string
	for _, f := range files {
		rel, err := validateScriptRelativePath(f.Rel)
		if err != nil {
			return "", err
		}
		if strings.HasSuffix(strings.ToLower(rel), ".sh") {
			arg := base + "/" + rel
			// Keep the established output readable for ordinary names. Every
			// other filename is one shell argument, so spaces/$()/operators
			// can never be interpreted by the deployment shell.
			lines = append(lines, "chmod "+mode+" "+shellArg(arg))
		}
	}
	if len(lines) == 0 {
		return "", nil
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func renderChmodScript(baseDir string, files []ResolvedFile) string {
	content, _ := renderChmodScriptSafe(baseDir, files, "777")
	return content
}
