package dbconsole

import (
	"context"
	"strings"
	"testing"
)

// P1（审核第 2 项）：投影里的标量子查询不能让网格编辑的目标表变成子查询里的表。
//
// 旧实现用整条 SQL 的第一个 FROM 正则匹配，`SELECT ID, (SELECT COUNT(*) FROM ARCHIVE) AS N FROM LIVE`
// 会把 ARCHIVE 当成编辑目标，用户在 LIVE 结果里的修改会被构造成对 ARCHIVE 的 UPDATE。
func TestAnalyzeGridQuery_ScalarSubqueryKeepsOuterTargetTable(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	source := Source{ID: "src-1", Kind: KindOracle, Username: "SCOTT"}

	m.SetCachedFields("src-1", "SCOTT", "LIVE", []Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "AMOUNT", DataType: "NUMBER"},
	})
	m.SetCachedFields("src-1", "SCOTT", "ARCHIVE", []Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "AMOUNT", DataType: "NUMBER"},
	})

	sqlText := "SELECT ID, (SELECT COUNT(*) FROM ARCHIVE) AS N FROM LIVE"
	cols := []Column{{Name: "ID", Database: "NUMBER"}, {Name: "N", Database: "NUMBER"}}

	plan, err := m.AnalyzeGridQuery(context.Background(), source, "tab-1", sqlText, cols)
	if err != nil {
		t.Fatalf("AnalyzeGridQuery failed: %v", err)
	}
	if plan.Table != "LIVE" {
		t.Fatalf("编辑目标表必须是外层 FROM 的 LIVE，实际为 %q (schema=%q)", plan.Table, plan.Schema)
	}
	if !plan.CanUpdate {
		t.Fatalf("外层投影含真实主键 ID，应当可更新: %+v", plan)
	}
	// 子查询投影列（N）必须只读，绝不能绑定到任何物理列。
	for _, col := range plan.Columns {
		if col.Index == 1 && col.Writable {
			t.Fatalf("子查询表达式列 N 不能可写: %+v", col)
		}
	}

	updateSQL, _, err := BuildGridMutationSQLWithPlan(KindOracle, plan, GridMutation{
		Action:   "update",
		Values:   map[string]any{"ID": 2},
		Key:      map[string]any{"ID": 1},
		Original: map[string]any{"ID": 1},
	})
	if err != nil {
		t.Fatalf("构造 UPDATE 失败: %v", err)
	}
	if strings.Contains(strings.ToUpper(updateSQL), "ARCHIVE") {
		t.Fatalf("UPDATE 不能指向子查询里的 ARCHIVE: %s", updateSQL)
	}
	if !strings.Contains(updateSQL, `"LIVE"`) {
		t.Fatalf("UPDATE 必须指向 LIVE: %s", updateSQL)
	}
}

// P1（审核第 2 项）：WHERE 里的子查询同样不能改变目标表。
func TestParseGridFromClause_IgnoresNestedSubqueries(t *testing.T) {
	cases := []struct {
		sql    string
		schema string
		table  string
		alias  string
	}{
		{"SELECT ID FROM LIVE WHERE ID IN (SELECT ID FROM ARCHIVE)", "", "LIVE", ""},
		{"SELECT ID FROM LIVE L WHERE EXISTS (SELECT 1 FROM ARCHIVE A WHERE A.ID = L.ID)", "", "LIVE", "L"},
		{"SELECT (SELECT MAX(ID) FROM ARCHIVE) AS M FROM SCOTT.LIVE", "SCOTT", "LIVE", ""},
		{"SELECT ID FROM \"SCOTT\".\"LIVE\" WHERE NAME = 'FROM ARCHIVE'", "SCOTT", "LIVE", ""},
		{"SELECT ID FROM LIVE t ORDER BY (SELECT 1 FROM ARCHIVE)", "", "LIVE", "T"},
	}

	for _, tc := range cases {
		parsed, err := parseGridQuerySyntax(KindOracle, tc.sql)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", tc.sql, err)
		}
		if parsed.Schema != tc.schema || parsed.Table != tc.table || parsed.TableAlias != tc.alias {
			t.Fatalf("%q: got schema=%q table=%q alias=%q, want %q.%q alias=%q",
				tc.sql, parsed.Schema, parsed.Table, parsed.TableAlias, tc.schema, tc.table, tc.alias)
		}
	}
}

// P1（审核第 2 项）：多表隐式连接的拒绝行为不能因为改写而回退。
func TestParseGridFromClause_StillRejectsImplicitJoin(t *testing.T) {
	if _, err := parseGridQuerySyntax(KindOracle, "SELECT A.ID FROM LIVE A, ARCHIVE B WHERE A.ID = B.ID"); err == nil {
		t.Fatal("逗号隐式连接必须继续被拒绝")
	}
}
