package dbconsole

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type ExportTable struct {
	Columns []Column
	Rows    [][]any
}

func (m *Manager) CollectQuery(ctx context.Context, source Source, query string, maxRows int) (ExportTable, QuerySummary, error) {
	return m.CollectQueryPage(ctx, source, query, 1, maxRows)
}

func (m *Manager) CollectQueryPage(ctx context.Context, source Source, query string, page, pageSize int) (ExportTable, QuerySummary, error) {
	var table ExportTable
	summary, err := m.StreamQueryPage(ctx, source, query, page, pageSize, func(event StreamEvent) error {
		switch event.Type {
		case "meta":
			table.Columns = event.Columns
		case "rows":
			table.Rows = append(table.Rows, event.Rows...)
		}
		return nil
	})
	return table, summary, err
}

func NormalizeExportFormat(format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "csv":
		return "csv", nil
	case "json":
		return "json", nil
	case "xlsx", "excel":
		return "xlsx", nil
	case "insert", "sql":
		return "insert", nil
	case "update":
		return "update", nil
	default:
		return "", fmt.Errorf("不支持的导出格式 %q，可选 csv / json / xlsx / insert / update", format)
	}
}

func ExportExtension(format string) string {
	switch format {
	case "json":
		return ".json"
	case "xlsx":
		return ".xlsx"
	case "insert", "update":
		return ".sql"
	default:
		return ".csv"
	}
}

func ExportContentType(format string) string {
	switch format {
	case "json":
		return "application/json; charset=utf-8"
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "insert", "update":
		return "text/plain; charset=utf-8"
	default:
		return "text/csv; charset=utf-8"
	}
}

var fromTableRe = regexp.MustCompile(`(?is)\bFROM\s+((?:"[^"]+"|` + "`[^`]+`" + `|[A-Za-z0-9_$#]+)(?:\s*\.\s*(?:"[^"]+"|` + "`[^`]+`" + `|[A-Za-z0-9_$#]+))?)`)

func InferExportTable(sql string) string {
	match := fromTableRe.FindStringSubmatch(sql)
	if len(match) < 2 {
		return "exported_rows"
	}
	name := strings.Join(strings.Fields(match[1]), "")
	if name == "" {
		return "exported_rows"
	}
	return name
}

func QuoteIdent(kind, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return quoteIdentPart(kind, "exported_rows")
	}
	parts := strings.Split(name, ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if isQuotedIdent(part) {
			out = append(out, part)
			continue
		}
		out = append(out, quoteIdentPart(kind, part))
	}
	if len(out) == 0 {
		return quoteIdentPart(kind, "exported_rows")
	}
	return strings.Join(out, ".")
}

func isQuotedIdent(name string) bool {
	return len(name) >= 2 && ((name[0] == '"' && name[len(name)-1] == '"') || (name[0] == '`' && name[len(name)-1] == '`'))
}

func quoteIdentPart(kind, name string) string {
	if kind == KindMySQL {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func ExportCellText(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	case map[string]any:
		if kind, _ := v["kind"].(string); kind == "text" {
			if preview, ok := v["preview"].(string); ok {
				return preview
			}
		}
		if kind, _ := v["kind"].(string); kind == "binary" {
			return fmt.Sprintf("[BINARY %v B]", v["bytes"])
		}
	}
	if raw, err := json.Marshal(value); err == nil {
		return string(raw)
	}
	return fmt.Sprint(value)
}

func ExportSQLLiteral(value any) string {
	if value == nil {
		return "NULL"
	}
	switch v := value.(type) {
	case bool:
		if v {
			return "1"
		}
		return "0"
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	case map[string]any:
		if kind, _ := v["kind"].(string); kind == "binary" {
			return "NULL"
		}
		return quoteSQLString(ExportCellText(v))
	}
	return quoteSQLString(ExportCellText(value))
}

func quoteSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func WriteJSON(w io.Writer, table ExportTable, extra map[string]any) error {
	rows := make([]map[string]any, 0, len(table.Rows))
	for _, row := range table.Rows {
		item := make(map[string]any, len(table.Columns))
		for i, column := range table.Columns {
			var value any
			if i < len(row) {
				value = jsonExportValue(row[i])
			}
			item[column.Name] = value
		}
		rows = append(rows, item)
	}
	payload := map[string]any{"columns": table.Columns, "rows": rows, "count": len(rows)}
	for key, value := range extra {
		payload[key] = value
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func jsonExportValue(value any) any {
	if value == nil {
		return nil
	}
	if obj, ok := value.(map[string]any); ok {
		if kind, _ := obj["kind"].(string); kind == "text" {
			return ExportCellText(obj)
		}
	}
	return value
}

func WriteINSERT(w io.Writer, table ExportTable, kind, tableName string) error {
	if len(table.Columns) == 0 {
		return fmt.Errorf("没有可导出的列")
	}
	quoted := QuoteIdent(kind, tableName)
	names := make([]string, len(table.Columns))
	for i, column := range table.Columns {
		names[i] = QuoteIdent(kind, column.Name)
	}
	prefix := "INSERT INTO " + quoted + " (" + strings.Join(names, ", ") + ") VALUES ("
	for _, row := range table.Rows {
		values := make([]string, len(table.Columns))
		for i := range table.Columns {
			var value any
			if i < len(row) {
				value = row[i]
			}
			values[i] = ExportSQLLiteral(value)
		}
		if _, err := io.WriteString(w, prefix+strings.Join(values, ", ")+");\n"); err != nil {
			return err
		}
	}
	return nil
}

// SplitSchemaObject parses "schema.table" or "table" into (schema, table).
func SplitSchemaObject(name string) (string, string) {
	name = strings.TrimSpace(name)
	if idx := strings.Index(name, "."); idx >= 0 {
		schema := strings.Trim(strings.TrimSpace(name[:idx]), `"`+"`")
		object := strings.Trim(strings.TrimSpace(name[idx+1:]), `"`+"`")
		return schema, object
	}
	return "", strings.Trim(name, `"`+"`")
}

// WriteUPDATE exports rows as SQL UPDATE statements, requiring PK in WHERE clause.
func WriteUPDATE(w io.Writer, table ExportTable, kind, tableName string, pkCols []string) error {
	if len(table.Columns) == 0 {
		return fmt.Errorf("没有可导出的列")
	}
	quotedTable := QuoteIdent(kind, tableName)
	pkSet := make(map[string]struct{})
	for _, pk := range pkCols {
		pkSet[strings.ToUpper(pk)] = struct{}{}
	}
	if len(pkSet) == 0 {
		for _, col := range table.Columns {
			upper := strings.ToUpper(col.Name)
			if upper == "ROWID" {
				pkSet["ROWID"] = struct{}{}
				break
			}
		}
	}
	if len(pkSet) == 0 {
		_, cleanObj := SplitSchemaObject(tableName)
		cleanUpper := strings.ToUpper(cleanObj)
		for _, col := range table.Columns {
			upper := strings.ToUpper(col.Name)
			if upper == "ID" || upper == cleanUpper+"_ID" {
				pkSet[upper] = struct{}{}
				break
			}
		}
	}

	var setIndices []int
	var whereIndices []int
	for i, col := range table.Columns {
		upper := strings.ToUpper(col.Name)
		if _, isPK := pkSet[upper]; isPK {
			whereIndices = append(whereIndices, i)
		} else {
			setIndices = append(setIndices, i)
		}
	}
	if len(whereIndices) == 0 {
		for i := range table.Columns {
			whereIndices = append(whereIndices, i)
			setIndices = append(setIndices, i)
		}
	} else if len(setIndices) == 0 {
		setIndices = whereIndices
	}

	for _, row := range table.Rows {
		var setParts []string
		for _, idx := range setIndices {
			var value any
			if idx < len(row) {
				value = row[idx]
			}
			setParts = append(setParts, QuoteIdent(kind, table.Columns[idx].Name)+" = "+ExportSQLLiteral(value))
		}
		var whereParts []string
		for _, idx := range whereIndices {
			var value any
			if idx < len(row) {
				value = row[idx]
			}
			if value == nil {
				whereParts = append(whereParts, QuoteIdent(kind, table.Columns[idx].Name)+" IS NULL")
			} else {
				whereParts = append(whereParts, QuoteIdent(kind, table.Columns[idx].Name)+" = "+ExportSQLLiteral(value))
			}
		}
		stmt := "UPDATE " + quotedTable + " SET " + strings.Join(setParts, ", ") + " WHERE " + strings.Join(whereParts, " AND ") + ";\n"
		if _, err := io.WriteString(w, stmt); err != nil {
			return err
		}
	}
	if kind == KindOracle {
		if _, err := io.WriteString(w, "COMMIT;\n"); err != nil {
			return err
		}
	}
	return nil
}


func WriteXLSX(w io.Writer, table ExportTable, sheetName string) error {
	sheetName = sanitizeSheetName(sheetName)
	var sheet bytes.Buffer
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sheet.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	writeXLSXRow(&sheet, 1, headerCells(table.Columns))
	for i, row := range table.Rows {
		cells := make([]string, len(table.Columns))
		for c := range table.Columns {
			var value any
			if c < len(row) {
				value = row[c]
			}
			cells[c] = ExportCellText(value)
		}
		writeXLSXRow(&sheet, i+2, cells)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	zw := zip.NewWriter(w)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
			`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
			`</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
			`</Relationships>`,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<sheets><sheet name="` + xmlEscape(sheetName) + `" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
			`</Relationships>`,
		"xl/worksheets/sheet1.xml": sheet.String(),
	}
	for name, body := range files {
		part, err := zw.Create(name)
		if err != nil {
			_ = zw.Close()
			return err
		}
		if _, err := io.WriteString(part, body); err != nil {
			_ = zw.Close()
			return err
		}
	}
	return zw.Close()
}

func headerCells(columns []Column) []string {
	out := make([]string, len(columns))
	for i, column := range columns {
		out[i] = column.Name
	}
	return out
}

func writeXLSXRow(buf *bytes.Buffer, row int, cells []string) {
	buf.WriteString(`<row r="` + strconv.Itoa(row) + `">`)
	for i, cell := range cells {
		ref := xlsxCol(i) + strconv.Itoa(row)
		text := sanitizeXLSXCell(cell)
		buf.WriteString(`<c r="` + ref + `" t="inlineStr"><is><t xml:space="preserve">` + xmlEscape(text) + `</t></is></c>`)
	}
	buf.WriteString(`</row>`)
}

func xlsxCol(index int) string {
	name := ""
	for index >= 0 {
		name = string(rune('A'+index%26)) + name
		index = index/26 - 1
	}
	return name
}

func sanitizeSheetName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Query"
	}
	replacer := strings.NewReplacer(":", " ", "\\", " ", "/", " ", "?", " ", "*", " ", "[", " ", "]", " ")
	name = replacer.Replace(name)
	if utf8.RuneCountInString(name) > 31 {
		runes := []rune(name)
		name = string(runes[:31])
	}
	return name
}

func sanitizeXLSXCell(value string) string {
	if value == "" {
		return value
	}
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return ' '
		}
		return r
	}, value)
	if utf8.RuneCountInString(cleaned) > 32767 {
		cleaned = string([]rune(cleaned)[:32767])
	}
	switch cleaned[0] {
	case '=', '+', '@':
		return "'" + cleaned
	}
	return cleaned
}

func xmlEscape(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
