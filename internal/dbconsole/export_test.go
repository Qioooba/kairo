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
}
