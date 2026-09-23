package dbconsole

import (
	"strings"
	"testing"
)

// P1（审核第 6 项）：Oracle 的未加引号标识符允许包含 `#`（如 ORDERS#ARCHIVE）。
// 去注释函数不能无条件把 `#` 当作行注释，否则表名会被截断，自动推断的导出目标
// 会指向另一张表（ORDERS）。
func TestInferExportTable_OracleHashIdentifierNotTruncated(t *testing.T) {
	if got := InferExportTable(KindOracle, "SELECT * FROM ORDERS#ARCHIVE"); got != "ORDERS#ARCHIVE" {
		t.Fatalf("Oracle 表名里的 # 必须保留，got %q", got)
	}
	if got := InferExportTable(KindOracle, "SELECT * FROM SCOTT.ORDERS#ARCHIVE WHERE ID = 7"); got != "SCOTT.ORDERS#ARCHIVE" {
		t.Fatalf("schema.表名里的 # 必须保留，got %q", got)
	}
	// MySQL 下 # 仍是行注释。
	if got := InferExportTable(KindMySQL, "SELECT * FROM T # FROM OTHER"); got != "T" {
		t.Fatalf("MySQL 的 # 注释必须继续被识别，got %q", got)
	}
}

// P1（审核第 6 项）：自动推断的导出目标必须与查询来源一致，不能被投影里的子查询带走。
func TestInferExportTable_UsesOutermostFrom(t *testing.T) {
	sqlText := "SELECT ID, (SELECT COUNT(*) FROM ARCHIVE) AS N FROM LIVE"
	if got := InferExportTable(KindOracle, sqlText); got != "LIVE" {
		t.Fatalf("导出目标必须是最外层 FROM 的 LIVE，got %q", got)
	}
}

// P1（审核第 6 项）：端到端确认生成的 UPDATE 脚本指向 ORDERS#ARCHIVE。
func TestBuildExportTargetPlan_OracleHashTableTarget(t *testing.T) {
	cols := []Column{{Name: "ID"}, {Name: "AMOUNT"}}
	plan, err := BuildExportTargetPlan(KindOracle, "", "SELECT * FROM ORDERS#ARCHIVE", cols, []string{"ID"}, false)
	if err != nil {
		t.Fatalf("BuildExportTargetPlan failed: %v", err)
	}
	if plan.Table != "ORDERS#ARCHIVE" {
		t.Fatalf("导出目标表必须是 ORDERS#ARCHIVE，got %q", plan.Table)
	}
	if !strings.Contains(plan.FullTarget, "ORDERS#ARCHIVE") {
		t.Fatalf("FullTarget 必须保留完整表名，got %q", plan.FullTarget)
	}
	if strings.Contains(plan.FullTarget, `"ORDERS"`) {
		t.Fatalf("FullTarget 不能退化成 ORDERS: %q", plan.FullTarget)
	}
}
