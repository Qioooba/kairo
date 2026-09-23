package dbconsole

import (
	"context"
	"strings"
	"testing"
)

// P2（审核第 9 项）：通配符与显式列混合投影时，投影下标与结果列下标不再一一对应。
// 旧实现按投影下标硬绑，会把结果里的 AMOUNT 绑成物理列 ID 并允许编辑，
// 提交时又因为索引对不上而失败。无法可靠展开时必须整体关闭编辑。
func TestAnalyzeGridQuery_MixedWildcardProjectionClosesEditing(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	source := Source{ID: "src-1", Kind: KindMySQL, Database: "app"}
	m.SetCachedFields("src-1", "app", "LIVE", []Field{
		{Name: "ID", DataType: "int", PrimaryKey: true},
		{Name: "AMOUNT", DataType: "int"},
	})

	sqlText := "SELECT t.*, t.ID AS EXTRA_ID FROM LIVE t"
	cols := []Column{{Name: "ID"}, {Name: "AMOUNT"}, {Name: "EXTRA_ID"}}

	plan, err := m.AnalyzeGridQuery(context.Background(), source, "tab-1", sqlText, cols)
	if err != nil {
		t.Fatalf("AnalyzeGridQuery failed: %v", err)
	}
	if plan.CanUpdate || plan.CanDelete || plan.CanInsert {
		t.Fatalf("混合通配符投影必须整体只读: %+v", plan)
	}
	if plan.Reason == "" {
		t.Fatal("必须给出只读原因")
	}
	for _, col := range plan.Columns {
		if col.Writable {
			t.Fatalf("混合通配符投影下结果列 %d 不能可写: %+v", col.Index, col)
		}
	}
	// 关键回归：结果第 2 列是 AMOUNT，绝不能被绑成物理列 ID。
	if plan.Columns[1].PhysicalName == "ID" {
		t.Fatalf("结果列 AMOUNT 被错绑为物理列 ID: %+v", plan.Columns[1])
	}
}

// P2（审核第 9 项）：投影列数与结果列数不一致时也不能按下标硬绑。
func TestAnalyzeGridQuery_ProjectionCountMismatchClosesEditing(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	source := Source{ID: "src-1", Kind: KindMySQL, Database: "app"}
	m.SetCachedFields("src-1", "app", "LIVE", []Field{
		{Name: "ID", DataType: "int", PrimaryKey: true},
		{Name: "AMOUNT", DataType: "int"},
	})

	plan, err := m.AnalyzeGridQuery(context.Background(), source, "tab-1",
		"SELECT ID FROM LIVE", []Column{{Name: "ID"}, {Name: "AMOUNT"}})
	if err != nil {
		t.Fatalf("AnalyzeGridQuery failed: %v", err)
	}
	if plan.CanUpdate || plan.CanDelete {
		t.Fatalf("投影列数与结果列数不一致时必须只读: %+v", plan)
	}
	if !strings.Contains(plan.Reason, "不一致") {
		t.Fatalf("只读原因应说明列数不一致，实际: %q", plan.Reason)
	}
}
