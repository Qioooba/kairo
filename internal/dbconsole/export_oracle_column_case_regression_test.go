package dbconsole

import (
	"bytes"
	"strings"
	"testing"
)

// P2（审核第 8 项）：Oracle 中通过双引号创建的小写列名（如 "note"）必须保持原样。
// 旧实现按"未加引号标识符折叠成大写"处理结果集元数据里的列名，会生成 SET "NOTE" = ...
// 并在执行时报 ORA-00904（标识符无效）。
func TestBuildExportTargetPlan_OracleQuotedLowerCaseColumnPreserved(t *testing.T) {
	cols := []Column{{Name: "ID"}, {Name: "note"}}
	plan, err := BuildExportTargetPlan(KindOracle, "T", "SELECT * FROM T", cols, []string{"ID"}, false)
	if err != nil {
		t.Fatalf("BuildExportTargetPlan failed: %v", err)
	}
	if len(plan.SetPhysicalNames) != 1 || plan.SetPhysicalNames[0] != "note" {
		t.Fatalf("SET 物理列名必须是结果集元数据里的 note，got %#v", plan.SetPhysicalNames)
	}

	table := ExportTable{
		Columns: cols,
		Rows:    [][]any{{int64(7), "hello"}},
	}
	var buf bytes.Buffer
	if err := WriteUPDATEWithPlan(&buf, table, plan); err != nil {
		t.Fatalf("WriteUPDATEWithPlan failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `SET "note" = 'hello'`) {
		t.Fatalf("应生成 SET \"note\" = 'hello'，实际: %s", out)
	}
	if strings.Contains(out, `"NOTE"`) {
		t.Fatalf("不能把 \"note\" 折叠成 \"NOTE\": %s", out)
	}
}

// P2（审核第 8 项）：没有查询文本（直接导出表格）时同样保留元数据里的原始大小写。
func TestBuildExportTargetPlan_OracleColumnCaseWithoutQueryText(t *testing.T) {
	cols := []Column{{Name: "ID"}, {Name: "note"}}
	plan, err := BuildExportTargetPlan(KindOracle, "T", "", cols, []string{"ID"}, false)
	if err != nil {
		t.Fatalf("BuildExportTargetPlan failed: %v", err)
	}
	if len(plan.SetPhysicalNames) != 1 || plan.SetPhysicalNames[0] != "note" {
		t.Fatalf("无查询文本时也必须保留 note，got %#v", plan.SetPhysicalNames)
	}
}
