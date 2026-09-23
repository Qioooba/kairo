package dbconsole

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestOracleSQLBindPlaceholdersAreUnique 是 ORA-01008 的静态防线。
//
// 背景（2026-09-23 定位）：`(owner = :1 OR UPPER(owner) = UPPER(:1))` 这种写法把同一个
// 占位符写两次、只传一个值。OCI 对 **SQL 语句** 的绑定变量按"出现次数"计数
// （见 vendor/github.com/godror/godror/odpi/src/dpiStmt.c 的说明），而纯 Go 的 go-ora
// 按位置只发送 len(args) 个绑定，于是 Oracle 报 ORA-01008 not all variables bound，
// 连锁导致：对象详情读取失败、隐藏 ROWID 探测失败（网格编辑拿不到身份）、LOB 定位失败。
//
// 规则：非 PL/SQL 的 Oracle SQL 字面量里，每个 :N 占位符只能出现一次。
// PL/SQL 块（含 BEGIN）按唯一变量名计数，因此跳过；MySQL 的 ? 占位符不受影响。
func TestOracleSQLBindPlaceholdersAreUnique(t *testing.T) {
	literals := regexp.MustCompile("`[^`]*`")
	dml := regexp.MustCompile(`\b(SELECT|UPDATE|INSERT|DELETE|MERGE)\b`)
	plsql := regexp.MustCompile(`\bBEGIN\b`)
	bind := regexp.MustCompile(`:([0-9]+)`)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	scanned := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		for _, lit := range literals.FindAllString(string(src), -1) {
			upper := strings.ToUpper(lit)
			if plsql.MatchString(upper) || !dml.MatchString(upper) {
				continue
			}
			counts := map[string]int{}
			for _, m := range bind.FindAllStringSubmatch(lit, -1) {
				counts[m[1]]++
			}
			scanned++
			for placeholder, n := range counts {
				if n > 1 {
					t.Errorf("%s: Oracle SQL 里占位符 :%s 出现了 %d 次（go-ora 按位置绑定会 ORA-01008）；"+
						"请为每次出现使用不同的 :N，或改用 sql.Named 传参\nSQL: %s",
						name, placeholder, n, strings.Join(strings.Fields(lit), " "))
				}
			}
		}
	}
	if scanned == 0 {
		t.Fatal("没有扫描到任何 SQL 字面量，测试本身可能失效了")
	}
	t.Logf("已检查 %d 段 Oracle SQL 字面量的占位符唯一性", scanned)
}
