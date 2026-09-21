package dbconsole

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// CollectSessionQueryPageWithParams executes a query page with session and parameters, collecting results for export.
func (m *Manager) CollectSessionQueryPageWithParams(ctx context.Context, source Source, query string, page, pageSize int, sessionID string, params []BindParameter, opts QueryOptions) (ExportTable, QuerySummary, error) {
	var table ExportTable
	summary, err := m.StreamSessionQueryPageWithParamsAndOptions(ctx, source, query, page, pageSize, sessionID, params, opts, func(event StreamEvent) error {
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

func (m *Manager) CollectQueryPage(ctx context.Context, source Source, query string, page, pageSize int) (ExportTable, QuerySummary, error) {
	return m.CollectSessionQueryPageWithParams(ctx, source, query, page, pageSize, "", nil, QueryOptions{Fast: page <= 1})
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
	noComments := stripSQLComments(sql)
	if regexp.MustCompile(`(?is)^\s*WITH\b|\bFROM\s*\(`).MatchString(noComments) {
		return "exported_rows"
	}
	match := fromTableRe.FindStringSubmatch(noComments)
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
	parts := splitIdentParts(name)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if isQuotedIdent(part) {
			part = part[1 : len(part)-1]
			part = strings.ReplaceAll(part, `""`, `"`)
			part = strings.ReplaceAll(part, "``", "`")
		}
		out = append(out, quoteIdentPart(kind, part))
	}
	if len(out) == 0 {
		return quoteIdentPart(kind, "exported_rows")
	}
	return strings.Join(out, ".")
}

func splitIdentParts(name string) []string {
	var parts []string
	var current strings.Builder
	inDouble := false
	inBacktick := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch c {
		case '"':
			if !inBacktick {
				if inDouble && i+1 < len(name) && name[i+1] == '"' {
					current.WriteByte('"')
					current.WriteByte('"')
					i++
					continue
				}
				inDouble = !inDouble
			}
			current.WriteByte(c)
		case '`':
			if !inDouble {
				if inBacktick && i+1 < len(name) && name[i+1] == '`' {
					current.WriteByte('`')
					current.WriteByte('`')
					i++
					continue
				}
				inBacktick = !inBacktick
			}
			current.WriteByte(c)
		case '.':
			if inDouble || inBacktick {
				current.WriteByte('.')
			} else {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	parts = append(parts, current.String())
	return parts
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
		if _, ok := v["token"]; ok {
			copy := make(map[string]any, len(v))
			for key, value := range v {
				if key != "token" {
					copy[key] = value
				}
			}
			return ExportCellText(copy)
		}
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
		if lazy, _ := v["lazy"].(bool); lazy {
			return "NULL"
		}
		if kind, _ := v["kind"].(string); kind == "binary" {
			return "NULL"
		}
		return quoteSQLString(ExportCellText(v))
	}
	return quoteSQLString(ExportCellText(value))
}

func exportSQLLiteralStrict(kind string, value any, colName string, rowIdx int) (string, error) {
	if value == nil {
		return "NULL", nil
	}
	if obj, ok := value.(map[string]any); ok {
		if lazy, _ := obj["lazy"].(bool); lazy {
			return "", fmt.Errorf("第 %d 行的列 %s 包含未加载的 LOB 内容，无法生成完整 SQL 字面量，请先加载或选用 CSV 格式", rowIdx+1, colName)
		}
		if kindStr, _ := obj["kind"].(string); kindStr == "clob" || kindStr == "blob" {
			return "", fmt.Errorf("第 %d 行的列 %s 包含 LOB 描述符，无法生成完整 SQL 字面量", rowIdx+1, colName)
		}
		if trunc, _ := obj["truncated"].(bool); trunc {
			return "", fmt.Errorf("第 %d 行的列 %s 文本已被截断，无法作为完整 SQL 字面量导出", rowIdx+1, colName)
		}
		if kindStr, _ := obj["kind"].(string); kindStr == "binary" {
			return "", fmt.Errorf("第 %d 行的列 %s 包含二进制内容，无法作为 SQL 字面量无损导出，请选用文件导出或 CSV", rowIdx+1, colName)
		}
	}
	return exportSQLLiteralForKind(kind, value), nil
}

func quoteSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// Hex-encoded UTF-8 avoids dependence on MySQL NO_BACKSLASH_ESCAPES when
// exporting paths, control characters, or quote/backslash combinations.
func exportSQLLiteralForKind(kind string, value any) string {
	literal := ExportSQLLiteral(value)
	if kind == KindMySQL && strings.HasPrefix(literal, "'") {
		text := ExportCellText(value)
		if strings.ContainsAny(text, "\\\x00\r\n\x1a") {
			return fmt.Sprintf("CONVERT(X'%x' USING utf8mb4)", []byte(text))
		}
	}
	return literal
}

func WriteJSON(w io.Writer, table ExportTable, extra map[string]any) error {
	colSeen := make(map[string]struct{}, len(table.Columns))
	for _, col := range table.Columns {
		if _, exists := colSeen[col.Name]; exists {
			return fmt.Errorf("结果集包含重复列名 %q，以 JSON 对象格式导出将丢失数据；请为重复列指定不同别名", col.Name)
		}
		colSeen[col.Name] = struct{}{}
	}
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
	for rIdx, row := range table.Rows {
		values := make([]string, len(table.Columns))
		for i := range table.Columns {
			var value any
			if i < len(row) {
				value = row[i]
			}
			lit, err := exportSQLLiteralStrict(kind, value, table.Columns[i].Name, rIdx)
			if err != nil {
				return err
			}
			values[i] = lit
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
	plan, err := BuildExportTargetPlan(kind, tableName, "", table.Columns, pkCols, false)
	if err != nil {
		return err
	}
	return WriteUPDATEWithPlan(w, table, plan)
}

// WriteUPDATEWithPlan exports rows using a validated ExportTargetPlan.
func WriteUPDATEWithPlan(w io.Writer, table ExportTable, plan ExportTargetPlan) error {
	whereIndices := plan.KeyIndices
	if plan.UseRowID && plan.RowIDIndex >= 0 {
		whereIndices = []int{plan.RowIDIndex}
	}
	if len(whereIndices) == 0 {
		return errors.New("导出 UPDATE 语句需要目标表定义主键，或请选用 INSERT/CSV 格式")
	}

	for rIdx, row := range table.Rows {
		if err := plan.ValidateRowValues(table.Columns, row, rIdx); err != nil {
			return err
		}
		var setParts []string
		for i, idx := range plan.SetIndices {
			var value any
			if idx < len(row) {
				value = row[idx]
			}
			lit, err := exportSQLLiteralStrict(plan.Kind, value, table.Columns[idx].Name, rIdx)
			if err != nil {
				return err
			}
			physCol := table.Columns[idx].Name
			if i < len(plan.SetPhysicalNames) && plan.SetPhysicalNames[i] != "" {
				physCol = plan.SetPhysicalNames[i]
			}
			setParts = append(setParts, QuoteIdent(plan.Kind, physCol)+" = "+lit)
		}
		var whereParts []string
		for i, idx := range whereIndices {
			var value any
			if idx < len(row) {
				value = row[idx]
			}
			lit, err := exportSQLLiteralStrict(plan.Kind, value, table.Columns[idx].Name, rIdx)
			if err != nil {
				return err
			}
			physCol := table.Columns[idx].Name
			if !plan.UseRowID && i < len(plan.KeyPhysicalNames) && plan.KeyPhysicalNames[i] != "" {
				physCol = plan.KeyPhysicalNames[i]
			}
			whereParts = append(whereParts, QuoteIdent(plan.Kind, physCol)+" = "+lit)
		}
		stmt := "UPDATE " + plan.FullTarget + " SET " + strings.Join(setParts, ", ") + " WHERE " + strings.Join(whereParts, " AND ") + ";\n"
		if _, err := io.WriteString(w, stmt); err != nil {
			return err
		}
	}
	if plan.Kind == KindOracle {
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
	case '=', '+', '@', '\t', '\r':
		return "'" + cleaned
	case '-':
		if _, err := strconv.ParseFloat(cleaned, 64); err != nil {
			return "'" + cleaned
		}
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
