package dbconsole

import (
	"context"
	"strings"
	"testing"
)

func TestParseSafeSingleTableQuery(t *testing.T) {
	cases := []struct {
		sql        string
		expectSafe bool
		schema     string
		table      string
		alias      string
		where      string
		orderBy    string
	}{
		{
			sql:        "SELECT * FROM HR.DOCUMENTS",
			expectSafe: true,
			schema:     "HR",
			table:      "DOCUMENTS",
		},
		{
			sql:        `SELECT * FROM "HR"."DOCUMENTS"`,
			expectSafe: true,
			schema:     "HR",
			table:      "DOCUMENTS",
		},
		{
			sql:        "SELECT d.* FROM HR.DOCUMENTS d",
			expectSafe: true,
			schema:     "HR",
			table:      "DOCUMENTS",
			alias:      "d",
		},
		{
			sql:        "SELECT d.* FROM HR.DOCUMENTS AS d WHERE id = 1",
			expectSafe: false,
			schema:     "HR",
			table:      "DOCUMENTS",
			alias:      "d",
			where:      "id = 1",
		},
		{
			sql:        "SELECT * FROM DOCUMENTS WHERE status = 'OK' ORDER BY created_at DESC",
			expectSafe: false,
			schema:     "",
			table:      "DOCUMENTS",
			where:      "status = 'OK'",
			orderBy:    "created_at DESC",
		},
		// 复杂/危险语句必须拒绝改写
		{sql: "SELECT * FROM t1 JOIN t2 ON t1.id = t2.id", expectSafe: false},
		{sql: "SELECT * FROM t1 LEFT JOIN t2 ON t1.id = t2.id", expectSafe: false},
		{sql: "SELECT * FROM (SELECT 1 FROM dual)", expectSafe: false},
		{sql: "SELECT DISTINCT * FROM t", expectSafe: false},
		{sql: "SELECT * FROM t GROUP BY id", expectSafe: false},
		{sql: "SELECT * FROM t UNION SELECT * FROM t2", expectSafe: false},
		{sql: "SELECT * FROM t FOR UPDATE", expectSafe: false},
		{sql: "WITH cte AS (SELECT 1 FROM dual) SELECT * FROM cte", expectSafe: false},
		{sql: "SELECT a, b, * FROM t", expectSafe: false},
	}

	for _, tc := range cases {
		info, ok := parseSafeSingleTableQuery(tc.sql)
		if ok != tc.expectSafe {
			t.Fatalf("query: %s, expected safe=%v, got safe=%v", tc.sql, tc.expectSafe, ok)
		}
		if ok {
			if !strings.EqualFold(info.Schema, tc.schema) || !strings.EqualFold(info.Table, tc.table) {
				t.Fatalf("query %s mismatch: schema=%s table=%s", tc.sql, info.Schema, info.Table)
			}
			if tc.alias != "" && !strings.EqualFold(info.Alias, tc.alias) {
				t.Fatalf("query %s alias mismatch: expected %s, got %s", tc.sql, tc.alias, info.Alias)
			}
		}
	}
}

type dummyMetadataManager struct {
	fields []Field
}

func (d *dummyMetadataManager) Fields(ctx context.Context, source Source, schema, object string) ([]Field, error) {
	return d.fields, nil
}

func TestBuildLOBProjectedQuery(t *testing.T) {
	// 1. Table with NO LOB columns
	noLOBFields := []Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "NAME", DataType: "VARCHAR2(100)"},
	}
	info, ok := parseSafeSingleTableQuery("SELECT * FROM HR.EMPLOYEES")
	if !ok {
		t.Fatal("expected safe parse")
	}
	rwNoLOB, err := compileLOBProjectedQuery(info, noLOBFields)
	if err != nil {
		t.Fatalf("compileLOBProjectedQuery failed: %v", err)
	}
	if rwNoLOB != nil {
		t.Fatal("table without LOB should not be rewritten")
	}

	// 2. Table with LOB columns: ID (PK), TITLE, BODY (CLOB), ATTACHMENT (BLOB)
	tableFields := []Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "TITLE", DataType: "VARCHAR2(100)"},
		{Name: "BODY", DataType: "CLOB"},
		{Name: "ATTACHMENT", DataType: "BLOB"},
	}

	infoLOB, ok := parseSafeSingleTableQuery("SELECT d.* FROM HR.DOCUMENTS d")
	if !ok {
		t.Fatal("expected safe parse")
	}

	rw, err := compileLOBProjectedQuery(infoLOB, tableFields)
	if err != nil {
		t.Fatalf("compileLOBProjectedQuery failed: %v", err)
	}
	if rw == nil {
		t.Fatal("expected rewritten query")
	}

	sql := rw.SQL
	t.Logf("Rewritten SQL:\n%s", sql)

	// Verify rewritten SQL structure (Section 2.2 / 2.5)
	if !strings.Contains(sql, `DBMS_LOB.GETLENGTH("D"."BODY")`) {
		t.Errorf("missing DBMS_LOB.GETLENGTH for BODY: %s", sql)
	}
	if !strings.Contains(sql, `DBMS_LOB.GETLENGTH("D"."ATTACHMENT")`) {
		t.Errorf("missing DBMS_LOB.GETLENGTH for ATTACHMENT: %s", sql)
	}
	if !strings.Contains(sql, `ROWIDTOCHAR("D".ROWID) AS "__KAIRO_ROWID__"`) {
		t.Errorf("missing ROWIDTOCHAR: %s", sql)
	}
	if !strings.Contains(sql, `ORDER BY "D"."ID"`) {
		t.Errorf("missing deterministic order by with primary key: %s", sql)
	}
	if len(rw.Columns) != 4 {
		t.Errorf("expected 4 user columns, got %d", len(rw.Columns))
	}
}
