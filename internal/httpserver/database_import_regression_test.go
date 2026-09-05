package httpserver

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"testing"
)

func TestImportUploadUsesAllDataRowsWithoutHeader(t *testing.T) {
	csv := databaseImportApplyRequest{Format: "csv", DataBase64: base64.StdEncoding.EncodeToString([]byte("id,name\n1,Alice\n2,Bob\n"))}
	rows, err := csv.importRows()
	if err != nil || len(rows) != 2 || rows[0][0] != "1" {
		t.Fatalf("CSV imported header: %v %v", rows, err)
	}
	var data bytes.Buffer
	zw := zip.NewWriter(&data)
	w, _ := zw.Create("xl/worksheets/sheet1.xml")
	fmt.Fprint(w, `<worksheet><sheetData><row><c r="A1" t="inlineStr"><is><t>id</t></is></c></row>`)
	for i := 1; i <= 150; i++ {
		fmt.Fprintf(w, `<row><c r="A%d"><v>%d</v></c></row>`, i+1, i)
	}
	fmt.Fprint(w, `</sheetData></worksheet>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	xlsx := databaseImportApplyRequest{Format: "xlsx", DataBase64: base64.StdEncoding.EncodeToString(data.Bytes())}
	rows, err = xlsx.importRows()
	if err != nil || len(rows) != 150 || rows[149][0] != "150" {
		t.Fatalf("XLSX import truncated to preview: rows=%d err=%v", len(rows), err)
	}
	csv.Rows = [][]string{{"ambiguous"}}
	if _, err := csv.importRows(); err == nil {
		t.Fatal("ambiguous row and file payload accepted")
	}
}
