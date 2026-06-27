package httpserver

import "testing"

// TestPathTraversal_AllowsDoubleDotFilename 验证 hasPathTraversal 不会误拒
// 含 ".." 子串的合法文件名（如 "my..file.log"）。
//
// 回归 BE-008：旧实现 strings.Contains(name, "..") 把任何含 ".." 子串的文件名
// 都拒掉，正常日志 / 归档命名（双点分隔）会被 400/404 卡住。
func TestPathTraversal_AllowsDoubleDotFilename(t *testing.T) {
	cases := []string{
		"my..file.log",
		"report..txt",
		"a.b..c",
		"..config.ini",   // 前缀含 ".." 但不构成穿越（无分隔符）
		"config..ini",    // 双点在中间但无路径分隔符
		"日志..zip",       // 非 ASCII 双点
		"normal.log",
		"20260624/app.log",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			if hasPathTraversal(name) {
				t.Errorf("hasPathTraversal(%q) = true，期望 false（合法文件名不应被拒）", name)
			}
		})
	}
}

// TestPathTraversal_RejectsParentDir 验证真正的路径穿越片段会被拒绝。
func TestPathTraversal_RejectsParentDir(t *testing.T) {
	cases := []string{
		"../etc/passwd",
		"a/../b",
		"a/b/../../c",
		"..",
		"/../etc",
		"etc/..",
		"..\\etc\\passwd",  // Windows 形态
		"a\\..\\b",
		"etc\\..",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			if !hasPathTraversal(name) {
				t.Errorf("hasPathTraversal(%q) = false，期望 true（应被当作穿越拒绝）", name)
			}
		})
	}
}
