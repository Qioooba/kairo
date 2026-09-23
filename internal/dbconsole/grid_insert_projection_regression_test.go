package dbconsole

import "testing"

// TestGridProjectionAllowsInsert_HiddenLocatorDoesNotBlockInsert 是 DBUI-01 插入能力的回归：
// plan.Columns 末尾会追加"行尾隐藏定位列"（oracle_rowid 的 ROWID，Writable=false）。
// 旧实现把这个定位列也算进投影计数，于是无主键堆表上的显式投影列表
// （SELECT id, name FROM t）永远得到 CanInsert=false —— 插入能力被静默关掉。
func TestGridProjectionAllowsInsert_HiddenLocatorDoesNotBlockInsert(t *testing.T) {
	parsed := &ParsedGridQuery{Projections: []string{"ID", "NAME"}}
	bindings := []GridColumnBinding{
		{Index: 0, ResultName: "ID", PhysicalName: "ID", Writable: true},
		{Index: 1, ResultName: "NAME", PhysicalName: "NAME", Writable: true},
		{Index: 2, ResultName: "__KAIRO_EDIT_RID__", PhysicalName: "ROWID", Writable: false, ReadOnlyReason: "行尾隐藏定位列"},
	}
	if !gridProjectionAllowsInsert(parsed, bindings) {
		t.Fatal("显式投影 + 追加的隐藏定位列必须仍然允许插入")
	}
}

// TestGridProjectionAllowsInsert_RejectsExpressionAndShortBindings 守住拒绝分支：
// 表达式/常量列（无物理列名）与"绑定数少于投影数"都必须拒绝插入。
func TestGridProjectionAllowsInsert_RejectsExpressionAndShortBindings(t *testing.T) {
	parsed := &ParsedGridQuery{Projections: []string{"ID", "NAME"}}
	if gridProjectionAllowsInsert(parsed, []GridColumnBinding{
		{Index: 0, ResultName: "ID", PhysicalName: "ID", Writable: true},
		{Index: 1, ResultName: "CNT", PhysicalName: "", Writable: false},
	}) {
		t.Fatal("表达式列（无物理列名）必须拒绝插入")
	}
	if gridProjectionAllowsInsert(parsed, []GridColumnBinding{
		{Index: 0, ResultName: "ID", PhysicalName: "ID", Writable: true},
	}) {
		t.Fatal("绑定数少于投影数时必须拒绝插入")
	}
	if gridProjectionAllowsInsert(&ParsedGridQuery{Projections: nil}, nil) {
		t.Fatal("无投影且非通配符必须拒绝插入")
	}
	if !gridProjectionAllowsInsert(&ParsedGridQuery{IsWildcard: true}, nil) {
		t.Fatal("通配符查询天然允许插入")
	}
	if gridProjectionAllowsInsert(nil, nil) {
		t.Fatal("nil 计划必须拒绝插入")
	}
}
