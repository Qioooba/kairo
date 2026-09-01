package waspack

import (
	"strings"
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
