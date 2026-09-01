package waspack

import (
	"strings"
)

// BackupHomePrefix 是备份脚本里每条文件前面拼的生产 WAR 根。
// 页面不展示、也不让用户配；脚本格式必须和现有 BakTT*.sh 一致。
const BackupHomePrefix = "$HOME/credit.ear/credit.war"

func ExecuteScriptName(pkg string) string { return pkg + ".sh" }
func BackupScriptName(pkg string) string  { return "Bak" + pkg + ".sh" }
func TarFileName(pkg string) string       { return pkg + ".tar" }

func relNoDot(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	return strings.TrimPrefix(rel, "./")
}

func fileArgs(files []ResolvedFile, withDot bool) string {
	parts := make([]string, 0, len(files))
	for _, f := range files {
		rel := relNoDot(f.Rel)
		if withDot {
			parts = append(parts, "./"+rel)
		} else {
			parts = append(parts, BackupHomePrefix+"/"+rel)
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
