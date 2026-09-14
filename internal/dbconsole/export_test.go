package dbconsole

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func testExportTable() ExportTable {
	return ExportTable{
		Columns: []Column{{Name: "ID"}, {Name: "NAME"}},
		Rows: [][]any{
			{int64(1), "alice"},
			{nil, "O'Hara"},
			{int64(3), "=cmd"},
		},
	}
}

func TestInferExportTable(t *testing.T) {
	if got := InferExportTable("SELECT * FROM credit.T_USER WHERE 1=1"); got != "credit.T_USER" {
		t.Fatalf("schema.table: %q", got)
	}
	if got := InferExportTable(`SELECT a FROM "OWNER"."TABLE_A"`); !strings.Contains(got, "TABLE_A") {
		t.Fatalf("quoted: %q", got)
	}
	if got := InferExportTable("SELECT 1 AS n FROM DUAL"); got != "DUAL" {
		t.Fatalf("dual: %q", got)
	}
	if got := InferExportTable("SELECT 1"); got != "exported_rows" {
		t.Fatalf("fallback: %q", got)
	}
}

func TestWriteJSONAndINSERT(t *testing.T) {
	table := testExportTable()
	var jsonBuf bytes.Buffer
	if err := WriteJSON(&jsonBuf, table, map[string]any{"source": "demo"}); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(jsonBuf.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["count"].(float64) != 3 || payload["source"] != "demo" {
		t.Fatalf("json payload: %#v", payload)
	}

	var sqlBuf bytes.Buffer
	if err := WriteINSERT(&sqlBuf, table, KindOracle, "CREDIT.T_USER"); err != nil {
		t.Fatal(err)
	}
	got := sqlBuf.String()
	if !strings.Contains(got, `INSERT INTO "CREDIT"."T_USER"`) {
		t.Fatalf("missing quoted table: %s", got)
	}
	if !strings.Contains(got, "NULL") || !strings.Contains(got, "'O''Hara'") {
		t.Fatalf("literals: %s", got)
	}

	validUpdateTable := ExportTable{
		Columns: []Column{{Name: "ID"}, {Name: "NAME"}},
		Rows: [][]any{
			{int64(1), "alice"},
			{int64(2), "bob"},
		},
	}
	var updateBuf bytes.Buffer
	if err := WriteUPDATE(&updateBuf, validUpdateTable, KindOracle, "CREDIT.T_USER", []string{"ID"}); err != nil {
		t.Fatal(err)
	}
	updateGot := updateBuf.String()
	if !strings.Contains(updateGot, `UPDATE "CREDIT"."T_USER" SET "NAME" = 'alice' WHERE "ID" = 1;`) {
		t.Fatalf("unexpected update output: %s", updateGot)
	}
	if !strings.Contains(updateGot, "COMMIT;") {
		t.Fatalf("missing Oracle COMMIT: %s", updateGot)
	}
}

func TestWriteUPDATE_NullPKRejects(t *testing.T) {
	table := ExportTable{
		Columns: []Column{{Name: "ID"}, {Name: "NAME"}},
		Rows: [][]any{
			{nil, "alice"},
		},
	}
	var buf bytes.Buffer
	err := WriteUPDATE(&buf, table, KindOracle, "CREDIT.T_USER", []string{"ID"})
	if err == nil || !strings.Contains(err.Error(), "NULL") {
		t.Fatalf("expected error for NULL PK, got: %v", err)
	}
}

func TestWriteUPDATE_CompositePKMissingColumnRejects(t *testing.T) {
	// Table has composite PK (TENANT_ID, ROW_ID), but query only returned TENANT_ID and NAME
	table := ExportTable{
		Columns: []Column{{Name: "TENANT_ID"}, {Name: "NAME"}},
		Rows: [][]any{
			{"T1", "alice"},
		},
	}
	var buf bytes.Buffer
	err := WriteUPDATE(&buf, table, KindOracle, "CREDIT.T_USER", []string{"TENANT_ID", "ROW_ID"})
	if err == nil || !strings.Contains(err.Error(), "缺少主键列 ROW_ID") {
		t.Fatalf("expected error for missing composite PK column, got: %v", err)
	}
}

func TestWriteUPDATE_CompositePKCompleteSucceeds(t *testing.T) {
	table := ExportTable{
		Columns: []Column{{Name: "TENANT_ID"}, {Name: "ROW_ID"}, {Name: "NAME"}},
		Rows: [][]any{
			{"T1", int64(100), "alice"},
		},
	}
	var buf bytes.Buffer
	err := WriteUPDATE(&buf, table, KindOracle, "CREDIT.T_USER", []string{"TENANT_ID", "ROW_ID"})
	if err != nil {
		t.Fatalf("expected success for complete composite PK, got: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `WHERE "TENANT_ID" = 'T1' AND "ROW_ID" = 100`) {
		t.Fatalf("unexpected where clause for composite PK: %s", got)
	}
}

func TestBuildExportTargetPlan_ForgedROWIDAliasRejects(t *testing.T) {
	// SELECT 1 AS ROWID ... should NOT be treated as trusted physical ROWID
	querySQL := `SELECT 1 AS ROWID, name FROM users`
	cols := []Column{{Name: "ROWID"}, {Name: "NAME"}}
	_, err := BuildExportTargetPlan(KindOracle, "users", querySQL, cols, nil, true)
	if err == nil {
		t.Fatal("expected forged ROWID alias to be rejected when table has no PK, but got nil")
	}
}

func TestBuildExportTargetPlan_JOINRejects(t *testing.T) {
	querySQL := `SELECT a.id, b.name FROM a JOIN b ON a.id = b.id`
	cols := []Column{{Name: "ID"}, {Name: "NAME"}}
	_, err := BuildExportTargetPlan(KindOracle, "a", querySQL, cols, []string{"ID"}, false)
	if err == nil || !strings.Contains(err.Error(), "JOIN") {
		t.Fatalf("expected JOIN query to be rejected for UPDATE plan, got: %v", err)
	}
}

func TestExportSQLLiteral_IncompleteLOBAndBinaryRejects(t *testing.T) {
	lazyLOB := map[string]any{"kind": "clob", "lazy": true, "column": "CONTENT"}
	table := ExportTable{
		Columns: []Column{{Name: "ID"}, {Name: "CONTENT"}},
		Rows: [][]any{
			{int64(1), lazyLOB},
		},
	}
	var buf bytes.Buffer
	err := WriteUPDATE(&buf, table, KindOracle, "DOCS", []string{"ID"})
	if err == nil || !strings.Contains(err.Error(), "未加载") {
		t.Fatalf("expected error for lazy LOB in WriteUPDATE, got: %v", err)
	}

	binaryVal := map[string]any{"kind": "binary", "bytes": 128}
	tableBinary := ExportTable{
		Columns: []Column{{Name: "ID"}, {Name: "AVATAR"}},
		Rows: [][]any{
			{int64(1), binaryVal},
		},
	}
	buf.Reset()
	errBinary := WriteUPDATE(&buf, tableBinary, KindOracle, "USERS", []string{"ID"})
	if errBinary == nil || !strings.Contains(errBinary.Error(), "二进制") {
		t.Fatalf("expected error for binary in WriteUPDATE, got: %v", errBinary)
	}
}

func TestWriteJSON_DuplicateColumnRejects(t *testing.T) {
	table := ExportTable{
		Columns: []Column{{Name: "ID"}, {Name: "ID"}},
		Rows: [][]any{
			{int64(1), int64(2)},
		},
	}
	var buf bytes.Buffer
	err := WriteJSON(&buf, table, nil)
	if err == nil || !strings.Contains(err.Error(), "重复列名") {
		t.Fatalf("expected error for duplicate column in WriteJSON, got: %v", err)
	}
}

func TestWriteXLSXIsZipWithSheet(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteXLSX(&buf, testExportTable(), "Query/Data"); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range zr.File {
		if file.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		found = true
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rc)
		_ = rc.Close()
		text := string(body)
		if !strings.Contains(text, "alice") || !strings.Contains(text, "'=cmd") {
			t.Fatalf("sheet missing cells: %s", text)
		}
	}
	if !found {
		t.Fatal("missing worksheet")
	}
}

func TestNormalizeExportFormat(t *testing.T) {
	got, err := NormalizeExportFormat("Excel")
	if err != nil || got != "xlsx" {
		t.Fatalf("excel alias: %q %v", got, err)
	}
	if _, err := NormalizeExportFormat("pdf"); err == nil {
		t.Fatal("pdf should be rejected")
	}
}

func TestQuoteIdentMySQL(t *testing.T) {
	if got := QuoteIdent(KindMySQL, "db.t`x"); got != "`db`.`t``x`" {
		t.Fatalf("got %s", got)
	}
	// Verify that injection strings wrapped in quotes are safely unquoted and escaped
	injected := `"t"; DROP TABLE users; -- "`
	quotedOracle := QuoteIdent(KindOracle, injected)
	if quotedOracle != `"t""; DROP TABLE users; -- "` {
		t.Fatalf("QuoteIdent Oracle expected doubled quotes for inner quote, got: %s", quotedOracle)
	}
}

func TestWriteUPDATEWithoutPKRejects(t *testing.T) {
	var buf bytes.Buffer
	table := ExportTable{
		Columns: []Column{{Name: "CITY"}, {Name: "TEMPERATURE"}},
		Rows:    [][]any{{"Beijing", 25}},
	}
	// Call WriteUPDATE on table without primary key and without pkCols
	err := WriteUPDATE(&buf, table, KindOracle, "WEATHER", nil)
	if err == nil {
		t.Fatal("expected WriteUPDATE without PK to return error, but got nil")
	}
	if !strings.Contains(err.Error(), "主键") {
		t.Fatalf("expected error mentioning primary key, got: %v", err)
	}
}

func TestQuoteIdentWithQuotedDots(t *testing.T) {
	input := `"my.schema".col`
	gotOracle := QuoteIdent(KindOracle, input)
	wantOracle := `"my.schema"."col"`
	if gotOracle != wantOracle {
		t.Fatalf("QuoteIdent(%q) = %q, want %q", input, gotOracle, wantOracle)
	}

	inputMySQL := "`my.schema`.`col`"
	gotMySQL := QuoteIdent(KindMySQL, inputMySQL)
	wantMySQL := "`my.schema`.`col`"
	if gotMySQL != wantMySQL {
		t.Fatalf("QuoteIdent(%q) = %q, want %q", inputMySQL, gotMySQL, wantMySQL)
	}
}
