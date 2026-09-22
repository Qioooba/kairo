package dbconsole

import (
	"context"
	"strings"
	"testing"
)

func TestAnalyzeGridQuery_SingleTableNoOrderBy(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	source := Source{ID: "src-1", Kind: KindMySQL, Database: "testdb"}

	// 缓存基表元数据字段：id 为主键，name, age 为普通列
	m.SetCachedFields("src-1", "testdb", "users", []Field{
		{Name: "id", DataType: "int", PrimaryKey: true},
		{Name: "name", DataType: "varchar(50)"},
		{Name: "age", DataType: "int"},
	})

	cols := []Column{
		{Name: "id", Database: "int"},
		{Name: "name", Database: "varchar"},
	}

	// 1. 无 ORDER BY 单表查询
	plan, err := m.AnalyzeGridQuery(context.Background(), source, "tab-1", "SELECT id, name FROM users WHERE id = 1", cols)
	if err != nil {
		t.Fatalf("AnalyzeGridQuery failed: %v", err)
	}
	if !plan.CanUpdate || !plan.CanDelete || !plan.CanInsert {
		t.Fatalf("expected fully editable plan without ORDER BY, got: %+v", plan)
	}
	if plan.HasTopLevelOrder {
		t.Fatal("expected HasTopLevelOrder == false")
	}
	if plan.IdentityPolicy != "pk" {
		t.Fatalf("expected IdentityPolicy == 'pk', got %s", plan.IdentityPolicy)
	}
	if plan.Schema != "testdb" || plan.Table != "users" {
		t.Fatalf("expected testdb.users, got %s.%s", plan.Schema, plan.Table)
	}

	// 2. 带 ORDER BY 单表查询
	planOrder, err := m.AnalyzeGridQuery(context.Background(), source, "tab-1", "SELECT id, name FROM users WHERE id = 1 ORDER BY id DESC", cols)
	if err != nil {
		t.Fatalf("AnalyzeGridQuery failed: %v", err)
	}
	if !planOrder.HasTopLevelOrder {
		t.Fatal("expected HasTopLevelOrder == true")
	}
	if !planOrder.CanUpdate {
		t.Fatalf("expected planOrder.CanUpdate == true, got false (%s)", planOrder.Reason)
	}
}

func TestAnalyzeGridQuery_RejectsComplexQueries(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	source := Source{ID: "src-1", Kind: KindMySQL, Database: "testdb"}
	cols := []Column{{Name: "id"}}

	cases := []struct {
		sql    string
		reason string
	}{
		{"SELECT u.id FROM users u JOIN roles r ON u.id = r.uid", "JOIN"},
		{"SELECT id FROM users UNION SELECT id FROM archived_users", "UNION"},
		{"SELECT DISTINCT id FROM users", "DISTINCT"},
		{"SELECT id FROM users GROUP BY id", "GROUP BY"},
		{"SELECT id FROM (SELECT id FROM users) t", "派生表"},
		{"WITH cte AS (SELECT id FROM users) SELECT * FROM cte", "CTE"},
		{"SELECT u.id FROM users u, roles r WHERE u.id = r.uid", "隐式连接"},
	}

	for _, tc := range cases {
		plan, err := m.AnalyzeGridQuery(context.Background(), source, "tab-1", tc.sql, cols)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", tc.sql, err)
		}
		if plan.CanUpdate || plan.CanDelete {
			t.Fatalf("complex query %q must not be updateable/deleteable: %+v", tc.sql, plan)
		}
	}
}

func TestAnalyzeGridQuery_MissingOrComputedPK(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	source := Source{ID: "src-1", Kind: KindOracle, Username: "SCOTT"}

	m.SetCachedFields("src-1", "SCOTT", "EMP", []Field{
		{Name: "EMPNO", DataType: "NUMBER", PrimaryKey: true},
		{Name: "ENAME", DataType: "VARCHAR2(50)"},
		{Name: "SAL", DataType: "NUMBER"},
	})

	// 1. 投影未包含主键 EMPNO
	colsNoPK := []Column{{Name: "ENAME"}, {Name: "SAL"}}
	planNoPK, err := m.AnalyzeGridQuery(context.Background(), source, "tab-1", "SELECT ENAME, SAL FROM EMP", colsNoPK)
	if err != nil {
		t.Fatal(err)
	}
	if planNoPK.CanUpdate || planNoPK.CanDelete {
		t.Fatalf("missing PK must be read-only: %+v", planNoPK)
	}
	if planNoPK.IdentityPolicy != "none" {
		t.Fatalf("expected IdentityPolicy 'none', got %s", planNoPK.IdentityPolicy)
	}

	// 2. 伪造主键列别名 (EMPNO + 1 AS EMPNO)
	colsAlias := []Column{{Name: "EMPNO"}, {Name: "ENAME"}}
	planAlias, err := m.AnalyzeGridQuery(context.Background(), source, "tab-1", "SELECT EMPNO + 1 AS EMPNO, ENAME FROM EMP", colsAlias)
	if err != nil {
		t.Fatal(err)
	}
	if planAlias.CanUpdate || planAlias.CanDelete {
		t.Fatalf("computed PK expression must not be treated as real physical PK: %+v", planAlias)
	}
}

func TestBuildGridMutationSQLWithPlan_OptimisticLockingAndNull(t *testing.T) {
	plan := &ResultEditContext{
		ResultID:          "res-123",
		SourceID:          "src-1",
		Dialect:           KindMySQL,
		Schema:            "app",
		Table:             "users",
		PrimaryKeys:       []string{"id"},
		IdentityPolicy:    "pk",
		CanUpdate:         true,
		CanDelete:         true,
		CanInsert:         true,
		Columns: []GridColumnBinding{
			{Index: 0, PhysicalName: "id", IsPrimaryKey: true, Writable: true},
			{Index: 1, PhysicalName: "name", Writable: true},
			{Index: 2, PhysicalName: "status", Writable: true},
		},
	}

	// 1. 正常 UPDATE 携带原值与 NULL 原值
	sqlText, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, GridMutation{
		Action: "update",
		Values: map[string]any{"name": "new_name"},
		Key:    map[string]any{"id": int64(10)},
		Original: map[string]any{
			"id":     int64(10),
			"name":   "old_name",
			"status": nil,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sqlText, "UPDATE `app`.`users` SET `name` = ? WHERE `id` = ? AND `name` = ?") {
		t.Fatalf("unexpected generated SQL: %s", sqlText)
	}
	if len(args) != 3 || args[0] != "new_name" || args[1] != int64(10) || args[2] != "old_name" {
		t.Fatalf("unexpected args: %#v", args)
	}

	// 2. 缺失修改列的原值 -> 拒绝
	_, _, errMissingOrig := BuildGridMutationSQLWithPlan(KindMySQL, plan, GridMutation{
		Action: "update",
		Values: map[string]any{"name": "new_name"},
		Key:    map[string]any{"id": int64(10)},
		Original: map[string]any{
			"id": int64(10), // 缺少 "name" 原值
		},
	})
	if errMissingOrig == nil || !strings.Contains(errMissingOrig.Error(), "original") {
		t.Fatalf("missing original value for modified column must be rejected: %v", errMissingOrig)
	}

	// 3. 主键为 NULL -> 拒绝
	_, _, errNullPK := BuildGridMutationSQLWithPlan(KindMySQL, plan, GridMutation{
		Action: "update",
		Values: map[string]any{"name": "new_name"},
		Key:    map[string]any{"id": nil},
		Original: map[string]any{
			"id":   nil,
			"name": "old_name",
		},
	})
	if errNullPK == nil || !strings.Contains(errNullPK.Error(), "NULL") {
		t.Fatalf("NULL primary key must be rejected: %v", errNullPK)
	}
}

func TestEncodeLosslessCell_Precision(t *testing.T) {
	safeInt := int64(9007199254740991)
	unsafeInt := int64(9007199254740993)

	if res := EncodeLosslessCell(safeInt); res != safeInt {
		t.Fatalf("safe int should stay int64: %v", res)
	}
	if res := EncodeLosslessCell(unsafeInt); res != "9007199254740993" {
		t.Fatalf("unsafe int must be converted to string: %v", res)
	}
}
