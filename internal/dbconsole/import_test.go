package dbconsole

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestCSVImportHeaderBOMAndRowLimit(t *testing.T) {
	table, err := ParseImportData("csv", []byte("\xef\xbb\xbfid\n"+strings.Repeat("1\n", maxImportRows)))
	if err != nil || table.Headers[0] != "id" || len(table.Rows) != maxImportRows {
		t.Fatalf("rows=%d err=%v", len(table.Rows), err)
	}
	if _, err := ParseImportData("csv", []byte("id\n"+strings.Repeat("1\n", maxImportRows+1))); err == nil {
		t.Fatal("row cap bypassed")
	}
}

func TestXLSXRejectsOutOfRangeCellInsteadOfRelocating(t *testing.T) {
	var data bytes.Buffer
	zw := zip.NewWriter(&data)
	w, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`<worksheet><sheetData><row><c r="XFD1" t="inlineStr"><is><t>outside</t></is></c></row></sheetData></worksheet>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseImportData("xlsx", data.Bytes()); err == nil {
		t.Fatal("out-of-range cell silently moved to column A")
	}
}

func TestParseImportDataCSVLimitsAndHeaders(t *testing.T) {
	table, err := ParseImportData("csv", []byte("id,name\n1,alice\n2,bob\n"))
	if err != nil {
		t.Fatal(err)
	}
	if table.Format != "csv" || len(table.Headers) != 2 || len(table.Rows) != 2 || table.Rows[1][1] != "bob" {
		t.Fatalf("unexpected CSV table: %#v", table)
	}
	if _, err := ParseImportData("sql", []byte("select 1")); err == nil {
		t.Fatal("unsupported import format must be rejected")
	}
}

func TestBuildImportInsertTypedMapping(t *testing.T) {
	query, args, err := buildImportInsert(KindMySQL, "app", "users", []ImportMapping{
		{SourceIndex: 0, Target: "id", Type: "int"},
		{SourceIndex: 1, Target: "name", Type: "string"},
	}, []string{"7", "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "`app`.`users`") || !strings.Contains(query, "(`id`, `name`)") || len(args) != 2 {
		t.Fatalf("unexpected import SQL: %q %#v", query, args)
	}
	if got, ok := args[0].(int64); !ok || got != 7 {
		t.Fatalf("typed import value mismatch: %#v", args[0])
	}
}

func TestNormalizeImportType(t *testing.T) {
	for _, tc := range []struct {
		kind, dbType, want string
	}{
		{KindOracle, "VARCHAR2(80)", "string"},
		{KindOracle, "NUMBER(10,2)", "decimal"},
		{KindOracle, "NUMBER", "decimal"},
		{KindOracle, "DATE", "datetime"},
		{KindMySQL, "BIGINT", "int64"},
		{KindMySQL, "DATETIME", "datetime"},
		{KindMySQL, "BLOB", "bytes"},
	} {
		if got := NormalizeImportType(tc.kind, tc.dbType); got != tc.want {
			t.Errorf("NormalizeImportType(%q,%q)=%q, want %q", tc.kind, tc.dbType, got, tc.want)
		}
	}
}

func TestImportDecimalPreservesPrecision(t *testing.T) {
	want := "12345678901234567890.123456789"
	for _, typ := range []string{"NUMBER", "decimal", "NUMERIC(38,9)"} {
		value, err := importMappedValue(KindOracle, want, ImportMapping{Type: typ})
		if err != nil || value != want {
			t.Fatalf("%s decimal precision changed: %v %v", typ, value, err)
		}
	}
}

func TestImportOracleDatePreservesTime(t *testing.T) {
	value, err := importMappedValue(KindOracle, "2026-09-05 12:34:56", ImportMapping{Type: "DATE"})
	stamp, ok := value.(time.Time)
	if err != nil || !ok || stamp.Hour() != 12 || stamp.Minute() != 34 || stamp.Second() != 56 {
		t.Fatalf("Oracle DATE lost time: %v %v", value, err)
	}
}

func TestXLSXRichTextImport(t *testing.T) {
	var data bytes.Buffer
	zw := zip.NewWriter(&data)
	for name, body := range map[string]string{
		"xl/sharedStrings.xml":     `<sst><si><r><t>full </t></r><r><t>name</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row><c r="A1" t="s"><v>0</v></c></row><row><c r="A2" t="inlineStr"><is><r><t>Alice </t></r><r><t>Smith</t></r></is></c></row></sheetData></worksheet>`,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	table, err := ParseImportData("xlsx", data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Headers) != 1 || table.Headers[0] != "full name" || len(table.Rows) != 1 || table.Rows[0][0] != "Alice Smith" {
		t.Fatalf("rich text was lost: %#v", table)
	}
}
