package dbconsole

import (
	"context"
	"fmt"
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
	// 按照性能规范：用户未显式指定 ORDER BY 时，不再强制全表排序
	if strings.Contains(sql, `ORDER BY`) {
		t.Errorf("unexpected ORDER BY when not specified: %s", sql)
	}
	// 用户指定 ORDER BY 时，应保留并补充主键
	infoWithOrder := *infoLOB
	infoWithOrder.OrderByClause = `"D"."ID" ASC`
	rwOrder, err := compileLOBProjectedQuery(&infoWithOrder, tableFields)
	if err != nil {
		t.Fatalf("compileLOBProjectedQuery with order failed: %v", err)
	}
	if !strings.Contains(rwOrder.SQL, `ORDER BY "D"."ID" ASC, "D"."ID"`) {
		t.Errorf("missing deterministic order by with primary key: %s", rwOrder.SQL)
	}
	if len(rw.Columns) != 4 {
		t.Errorf("expected 4 user columns, got %d", len(rw.Columns))
	}
}

func TestLOBProjection_MetadataCacheIdentityAndNegativeCacheIsolation(t *testing.T) {
	// 1. Unquoted identifiers are normalized to uppercase, hitting the same logical object
	info1, ok1 := parseSafeSingleTableQuery("SELECT * FROM hr.foo")
	info2, ok2 := parseSafeSingleTableQuery("SELECT * FROM HR.FOO")
	if !ok1 || !ok2 {
		t.Fatal("expected unquoted queries to parse safely")
	}
	if info1.Table != "FOO" || info2.Table != "FOO" {
		t.Fatalf("unquoted identifiers should normalize to uppercase, got %s and %s", info1.Table, info2.Table)
	}

	// 2. Quoted identifiers preserve exact case and do not collide
	infoQuotedFoo, okFoo := parseSafeSingleTableQuery(`SELECT * FROM "HR"."Foo"`)
	infoQuotedFOO, okFOO := parseSafeSingleTableQuery(`SELECT * FROM "HR"."FOO"`)
	if !okFoo || !okFOO {
		t.Fatal("expected quoted queries to parse safely")
	}
	if infoQuotedFoo.Table != "Foo" {
		t.Fatalf(`expected table "Foo", got %s`, infoQuotedFoo.Table)
	}
	if infoQuotedFOO.Table != "FOO" {
		t.Fatalf(`expected table "FOO", got %s`, infoQuotedFOO.Table)
	}

	// 3. Cache isolation: negative cache on "Foo" must NOT contaminate "FOO"
	m := &Manager{metadataCache: make(map[string]metadataCacheEntry)}
	srcID := "test-oracle-src"

	keyFoo := fmt.Sprintf("%s\x00lob_proj_fields\x00%s\x00%s", srcID, infoQuotedFoo.Schema, infoQuotedFoo.Table)
	keyFOO := fmt.Sprintf("%s\x00lob_proj_fields\x00%s\x00%s", srcID, infoQuotedFOO.Schema, infoQuotedFOO.Table)

	if keyFoo == keyFOO {
		t.Fatalf("cache keys for Foo and FOO must not collide: %s vs %s", keyFoo, keyFOO)
	}

	// Negative cache on "Foo"
	metadataCacheSet(m, keyFoo, []Field{})
	// Valid LOB fields on "FOO"
	validFOOFields := []Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "DATA", DataType: "CLOB"},
	}
	metadataCacheSet(m, keyFOO, validFOOFields)

	// Verify "Foo" returns negative cache (empty slice)
	cachedFoo, ok := metadataCacheGet[[]Field](m, keyFoo)
	if !ok || len(cachedFoo) != 0 {
		t.Fatalf("expected negative cache hit for Foo, got ok=%v, len=%d", ok, len(cachedFoo))
	}

	// Verify "FOO" returns valid fields (not contaminated)
	cachedFOO, ok := metadataCacheGet[[]Field](m, keyFOO)
	if !ok || len(cachedFOO) != 2 {
		t.Fatalf("expected valid cache hit for FOO, got ok=%v, len=%d", ok, len(cachedFOO))
	}

	// 4. Cache invalidation clears entries
	m.InvalidateMetadata(srcID)
	if _, ok := metadataCacheGet[[]Field](m, keyFoo); ok {
		t.Fatal("expected keyFoo to be invalidated")
	}
	if _, ok := metadataCacheGet[[]Field](m, keyFOO); ok {
		t.Fatal("expected keyFOO to be invalidated")
	}
}
