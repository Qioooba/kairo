package dbconsole

import (
	"context"
	"strings"
	"testing"
)

func TestRewriteOracleQueryForHiddenRowID(t *testing.T) {
	tests := []struct {
		name      string
		sql       string
		wantMatch string
		wantErr   bool
	}{
		{
			name:      "Wildcard without alias",
			sql:       "SELECT * FROM EMP",
			wantMatch: `SELECT "EMP".*, ROWIDTOCHAR("EMP".ROWID) AS "__KAIRO_EDIT_RID__" FROM EMP`,
		},
		{
			name:      "Wildcard with alias",
			sql:       "SELECT t.* FROM SCOTT.EMP t WHERE DEPTNO = 10",
			wantMatch: `SELECT t.*, ROWIDTOCHAR("t".ROWID) AS "__KAIRO_EDIT_RID__" FROM SCOTT.EMP t WHERE DEPTNO = 10`,
		},
		{
			name:      "Explicit columns without alias",
			sql:       "SELECT EMPNO, ENAME FROM EMP WHERE EMPNO = 7369",
			wantMatch: `SELECT EMPNO, ENAME, ROWIDTOCHAR("EMP".ROWID) AS "__KAIRO_EDIT_RID__" FROM EMP WHERE EMPNO = 7369`,
		},
		{
			name:      "Explicit columns with alias and order by",
			sql:       "SELECT t.EMPNO, t.ENAME FROM EMP t ORDER BY t.EMPNO ASC",
			wantMatch: `SELECT t.EMPNO, t.ENAME, ROWIDTOCHAR("t".ROWID) AS "__KAIRO_EDIT_RID__" FROM EMP t ORDER BY t.EMPNO ASC`,
		},
		{
			name:      "Already rewritten",
			sql:       `SELECT "EMP".*, ROWIDTOCHAR("EMP".ROWID) AS "__KAIRO_EDIT_RID__" FROM EMP`,
			wantMatch: `__KAIRO_EDIT_RID__`,
		},
		{
			name:    "Complex query with JOIN is rejected",
			sql:     "SELECT e.EMPNO, d.DNAME FROM EMP e JOIN DEPT d ON e.DEPTNO = d.DEPTNO",
			wantErr: true,
		},
		{
			name:    "CTE query is rejected",
			sql:     "WITH cte AS (SELECT 1 FROM dual) SELECT * FROM cte",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := RewriteOracleQueryForHiddenRowID(tt.sql)
			if (err != nil) != tt.wantErr {
				t.Fatalf("RewriteOracleQueryForHiddenRowID() err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && !strings.Contains(res, tt.wantMatch) {
				t.Errorf("expected rewritten SQL to contain %q, got: %s", tt.wantMatch, res)
			}
		})
	}
}

func TestAnalyzeGridQuery_OracleHeapTableHiddenRowID(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	source := Source{ID: "src-ora", Kind: KindOracle, Database: "XE"}

	// Table without primary key
	m.SetCachedFields("src-ora", "SCOTT", "LOG_TABLE", []Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: false},
		{Name: "MESSAGE", DataType: "VARCHAR2(200)"},
		{Name: "CREATED_AT", DataType: "DATE"},
	})
	cols := []Column{
		{Name: "ID", Database: "NUMBER"},
		{Name: "MESSAGE", Database: "VARCHAR2"},
	}

	// 1. Regular heap table with hidden ROWID passed at index 2
	m.SetCachedHeapTable("src-ora", "SCOTT", "LOG_TABLE", true)
	plan, err := m.AnalyzeGridQuery(context.Background(), source, "tab-ora-1", "SELECT ID, MESSAGE FROM SCOTT.LOG_TABLE", cols, 2)
	if err != nil {
		t.Fatalf("AnalyzeGridQuery failed: %v", err)
	}
	if plan.IdentityPolicy != "oracle_rowid" {
		t.Fatalf("expected IdentityPolicy == 'oracle_rowid', got %s", plan.IdentityPolicy)
	}
	if plan.HiddenRowIDIndex != 2 {
		t.Fatalf("expected HiddenRowIDIndex == 2, got %d", plan.HiddenRowIDIndex)
	}
	if !plan.CanUpdate || !plan.CanDelete {
		t.Fatalf("expected CanUpdate && CanDelete to be true, got update=%v delete=%v reason=%s", plan.CanUpdate, plan.CanDelete, plan.Reason)
	}

	// 2. Non-heap table (e.g. IOT) without primary key should not allow ROWID editing
	m.SetCachedHeapTable("src-ora", "SCOTT", "IOT_TABLE", false)
	m.SetCachedFields("src-ora", "SCOTT", "IOT_TABLE", []Field{
		{Name: "CODE", DataType: "VARCHAR2(20)"},
		{Name: "DESCR", DataType: "VARCHAR2(100)"},
	})
	colsIOT := []Column{
		{Name: "CODE", Database: "VARCHAR2"},
		{Name: "DESCR", Database: "VARCHAR2"},
	}
	planIOT, err := m.AnalyzeGridQuery(context.Background(), source, "tab-ora-2", "SELECT CODE, DESCR FROM SCOTT.IOT_TABLE", colsIOT, 2)
	if err != nil {
		t.Fatalf("AnalyzeGridQuery failed: %v", err)
	}
	if planIOT.IdentityPolicy != "none" || planIOT.CanUpdate {
		t.Fatalf("expected IOT table without PK to be read-only, got policy=%s canUpdate=%v", planIOT.IdentityPolicy, planIOT.CanUpdate)
	}
}

func TestAnalyzeGridQuery_VerifiedUniqueKey(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	source := Source{ID: "src-mysql", Kind: KindMySQL, Database: "testdb"}

	// Table without PK, but with unique index on non-null email
	m.SetCachedFields("src-mysql", "testdb", "accounts", []Field{
		{Name: "email", DataType: "varchar(100)", Nullable: false},
		{Name: "nickname", DataType: "varchar(50)", Nullable: true},
		{Name: "points", DataType: "int", Nullable: true},
	})
	m.SetCachedIndexes("src-mysql", "testdb", "accounts", []IndexInfo{
		{Name: "uk_email", Uniqueness: "UNIQUE", Columns: []string{"email"}},
	})

	cols := []Column{
		{Name: "email", Database: "varchar"},
		{Name: "nickname", Database: "varchar"},
	}

	// 1. Query projects the full non-null unique key
	plan, err := m.AnalyzeGridQuery(context.Background(), source, "tab-uniq-1", "SELECT email, nickname FROM accounts", cols)
	if err != nil {
		t.Fatalf("AnalyzeGridQuery failed: %v", err)
	}
	if plan.IdentityPolicy != "unique" {
		t.Fatalf("expected IdentityPolicy == 'unique', got %s", plan.IdentityPolicy)
	}
	if !plan.CanUpdate || !plan.CanDelete {
		t.Fatalf("expected CanUpdate && CanDelete to be true for verified unique key")
	}

	// 2. Query fails to project the unique key
	colsMissing := []Column{
		{Name: "nickname", Database: "varchar"},
	}
	planMissing, err := m.AnalyzeGridQuery(context.Background(), source, "tab-uniq-2", "SELECT nickname FROM accounts", colsMissing)
	if err != nil {
		t.Fatalf("AnalyzeGridQuery failed: %v", err)
	}
	if planMissing.IdentityPolicy != "none" || planMissing.CanUpdate {
		t.Fatalf("expected non-projected unique key to yield read-only plan, got: policy=%s", planMissing.IdentityPolicy)
	}
}

func TestBuildGridMutationSQLWithPlan_OracleRowIDAndUnique(t *testing.T) {
	// 1. Oracle ROWID UPDATE & DELETE
	oraPlan := &ResultEditContext{
		Dialect:        KindOracle,
		Schema:         "SCOTT",
		Table:          "LOG_TABLE",
		IdentityPolicy: "oracle_rowid",
		CanUpdate:      true,
		CanDelete:      true,
		Columns: []GridColumnBinding{
			{Index: 0, PhysicalName: "STATUS", Writable: true},
			{Index: 1, PhysicalName: "MESSAGE", Writable: true},
		},
	}

	updateMutation := GridMutation{
		Action:   "update",
		RowID:    "AAASDMAABAAAL9DAAA",
		UseRowID: true,
		Values:   map[string]any{"STATUS": "DONE"},
		Original: map[string]any{"STATUS": "PENDING"},
	}
	sqlOraUp, argsOraUp, err := BuildGridMutationSQLWithPlan(KindOracle, oraPlan, updateMutation)
	if err != nil {
		t.Fatalf("BuildGridMutationSQLWithPlan failed for Oracle ROWID update: %v", err)
	}
	if !strings.Contains(sqlOraUp, `ROWID = :kairo_p2`) || !strings.Contains(sqlOraUp, `"STATUS" = :kairo_p3`) {
		t.Fatalf("unexpected SQL for Oracle ROWID update: %s", sqlOraUp)
	}
	if len(argsOraUp) != 3 {
		t.Fatalf("expected 3 named args, got %d", len(argsOraUp))
	}

	deleteMutation := GridMutation{
		Action:   "delete",
		RowID:    "AAASDMAABAAAL9DAAA",
		UseRowID: true,
		Original: map[string]any{"STATUS": "PENDING"},
	}
	sqlOraDel, _, err := BuildGridMutationSQLWithPlan(KindOracle, oraPlan, deleteMutation)
	if err != nil {
		t.Fatalf("BuildGridMutationSQLWithPlan failed for Oracle ROWID delete: %v", err)
	}
	if !strings.Contains(sqlOraDel, `DELETE FROM "SCOTT"."LOG_TABLE" WHERE ROWID = :kairo_p1`) {
		t.Fatalf("unexpected SQL for Oracle ROWID delete: %s", sqlOraDel)
	}

	// 2. MySQL Unique Key UPDATE
	mysqlPlan := &ResultEditContext{
		Dialect:        KindMySQL,
		Schema:         "testdb",
		Table:          "accounts",
		IdentityPolicy: "unique",
		UniqueKeys:     []string{"email"},
		CanUpdate:      true,
		CanDelete:      true,
		Columns: []GridColumnBinding{
			{Index: 0, PhysicalName: "email", Writable: false},
			{Index: 1, PhysicalName: "nickname", Writable: true},
		},
	}
	upUniq := GridMutation{
		Action:   "update",
		Key:      map[string]any{"email": "user@example.com"},
		Values:   map[string]any{"nickname": "Bob"},
		Original: map[string]any{"email": "user@example.com", "nickname": "Alice"},
	}
	sqlUniqUp, argsUniqUp, err := BuildGridMutationSQLWithPlan(KindMySQL, mysqlPlan, upUniq)
	if err != nil {
		t.Fatalf("BuildGridMutationSQLWithPlan failed for Unique Key update: %v", err)
	}
	if !strings.Contains(sqlUniqUp, "WHERE `email` = ? AND `nickname` = ?") {
		t.Fatalf("unexpected SQL for Unique Key update: %s", sqlUniqUp)
	}
	if len(argsUniqUp) != 3 {
		t.Fatalf("expected 3 args, got %d (%v)", len(argsUniqUp), argsUniqUp)
	}
}
