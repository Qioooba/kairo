package dbconsole

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxImportBytes = 8 << 20
	maxImportRows  = 10000
	maxImportCols  = 256
)

type ImportMapping struct {
	SourceIndex int    `json:"source_index"`
	SourceName  string `json:"source_name,omitempty"`
	Target      string `json:"target"`
	Type        string `json:"type,omitempty"`
	Nullable    bool   `json:"nullable,omitempty"`
}

type ImportTable struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
	Format  string     `json:"format"`
}

type ImportPreview struct {
	Table        ImportTable     `json:"table"`
	Fields       []Field         `json:"fields,omitempty"`
	SuggestedMap []ImportMapping `json:"suggested_mapping,omitempty"`
	TotalRows    int             `json:"total_rows"`
	PreviewRows  int             `json:"preview_rows"`
	Truncated    bool            `json:"truncated"`
}

type ImportApplyRequest struct {
	Schema    string          `json:"schema"`
	Table     string          `json:"table"`
	Mappings  []ImportMapping `json:"mappings"`
	Rows      [][]string      `json:"rows"`
	SessionID string          `json:"session_id"`
	Commit    bool            `json:"commit,omitempty"`
	Confirm   bool            `json:"confirm,omitempty"`
}

type ImportRowResult struct {
	Row          int    `json:"row"`
	Status       string `json:"status"`
	RowsAffected int64  `json:"rows_affected,omitempty"`
	Error        string `json:"error,omitempty"`
}

type ImportApplyResult struct {
	RowsAffected       int64             `json:"rows_affected"`
	Processed          int               `json:"processed"`
	Committed          bool              `json:"committed"`
	RolledBack         bool              `json:"rolled_back"`
	TransactionPending bool              `json:"transaction_pending"`
	Rows               []ImportRowResult `json:"rows"`
	ElapsedMS          int64             `json:"elapsed_ms"`
}

// ParseImportData parses CSV or a minimal standards-compliant XLSX workbook
// using only the Go standard library.  The reader intentionally accepts the
// first worksheet and shared strings, which covers exports produced by Kairo,
// Excel and LibreOffice without adding a heavy spreadsheet dependency.
func ParseImportData(format string, data []byte) (ImportTable, error) {
	format = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(format), "."))
	if len(data) == 0 || len(data) > maxImportBytes {
		return ImportTable{}, fmt.Errorf("导入文件大小必须在 1..%d 字节之间", maxImportBytes)
	}
	switch format {
	case "csv", "txt":
		return parseCSVImport(data)
	case "xlsx", "excel":
		return parseXLSXImport(data)
	default:
		return ImportTable{}, errors.New("导入格式仅支持 CSV 或 XLSX")
	}
}

func parseCSVImport(data []byte) (ImportTable, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = false
	reader.LazyQuotes = false
	var rows [][]string
	for len(rows) <= maxImportRows+1 {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ImportTable{}, fmt.Errorf("CSV 解析失败: %w", err)
		}
		if len(record) > maxImportCols {
			return ImportTable{}, fmt.Errorf("CSV 列数超过 %d", maxImportCols)
		}
		for i := range record {
			if len(record[i]) > maxCellBytes {
				return ImportTable{}, fmt.Errorf("CSV 第 %d 列单元格过大", i+1)
			}
		}
		rows = append(rows, record)
	}
	if len(rows) == 0 {
		return ImportTable{}, errors.New("CSV 没有数据")
	}
	if len(rows) > maxImportRows+1 {
		return ImportTable{}, fmt.Errorf("CSV 行数超过 %d", maxImportRows)
	}
	return ImportTable{Headers: rows[0], Rows: rows[1:], Format: "csv"}, nil
}

type xlsxCell struct {
	Ref  string   `xml:"r,attr"`
	Type string   `xml:"t,attr"`
	V    string   `xml:"v"`
	IS   xlsxText `xml:"is"`
}

type xlsxText struct {
	Text string `xml:"t"`
	Runs []struct {
		Text string `xml:"t"`
	} `xml:"r"`
}

func (s xlsxText) value() string {
	var out strings.Builder
	out.WriteString(s.Text)
	for _, run := range s.Runs {
		out.WriteString(run.Text)
	}
	return out.String()
}

type xlsxRow struct {
	Cells []xlsxCell `xml:"c"`
}

type xlsxSheet struct {
	Rows []xlsxRow `xml:"sheetData>row"`
}

func parseXLSXImport(data []byte) (ImportTable, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ImportTable{}, fmt.Errorf("XLSX 压缩包无效: %w", err)
	}
	shared := make([]string, 0)
	if file := findZipFile(archive.File, "xl/sharedStrings.xml"); file != nil {
		reader, openErr := file.Open()
		if openErr != nil {
			return ImportTable{}, openErr
		}
		var doc struct {
			Items []xlsxText `xml:"si"`
		}
		err = xml.NewDecoder(io.LimitReader(reader, maxImportBytes)).Decode(&doc)
		_ = reader.Close()
		if err != nil {
			return ImportTable{}, fmt.Errorf("XLSX sharedStrings 解析失败: %w", err)
		}
		for _, item := range doc.Items {
			shared = append(shared, item.value())
		}
	}
	file := findZipFile(archive.File, "xl/worksheets/sheet1.xml")
	if file == nil {
		return ImportTable{}, errors.New("XLSX 缺少第一个工作表")
	}
	reader, err := file.Open()
	if err != nil {
		return ImportTable{}, err
	}
	var sheet xlsxSheet
	err = xml.NewDecoder(io.LimitReader(reader, maxImportBytes)).Decode(&sheet)
	_ = reader.Close()
	if err != nil {
		return ImportTable{}, fmt.Errorf("XLSX 工作表解析失败: %w", err)
	}
	rows := make([][]string, 0, len(sheet.Rows))
	for _, row := range sheet.Rows {
		if len(rows) >= maxImportRows+1 {
			return ImportTable{}, fmt.Errorf("XLSX 行数超过 %d", maxImportRows)
		}
		rowWidth := 0
		for i, cell := range row.Cells {
			col := i
			if cell.Ref != "" {
				parsedCol, colErr := xlsxColumnIndex(cell.Ref)
				if colErr != nil {
					return ImportTable{}, colErr
				}
				col = parsedCol
			}
			if col >= maxImportCols {
				return ImportTable{}, fmt.Errorf("XLSX 列数超过 %d", maxImportCols)
			}
			if col+1 > rowWidth {
				rowWidth = col + 1
			}
		}
		values := make([]string, rowWidth)
		for i, cell := range row.Cells {
			col := i
			if cell.Ref != "" {
				if parsedCol, colErr := xlsxColumnIndex(cell.Ref); colErr == nil {
					col = parsedCol
				}
			}
			value := cell.V
			if cell.Type == "s" {
				idx, parseErr := strconv.Atoi(strings.TrimSpace(cell.V))
				if parseErr != nil || idx < 0 || idx >= len(shared) {
					return ImportTable{}, errors.New("XLSX shared string 索引无效")
				}
				value = shared[idx]
			} else if cell.Type == "inlineStr" {
				value = cell.IS.value()
			} else if cell.Type == "b" {
				if value == "1" {
					value = "true"
				} else {
					value = "false"
				}
			}
			if len(value) > maxCellBytes {
				return ImportTable{}, errors.New("XLSX 单元格过大")
			}
			values[col] = value
		}
		rows = append(rows, values)
	}
	if len(rows) == 0 {
		return ImportTable{}, errors.New("XLSX 没有数据")
	}
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	if maxCols > maxImportCols {
		return ImportTable{}, fmt.Errorf("XLSX 列数超过 %d", maxImportCols)
	}
	for i := range rows {
		for len(rows[i]) < maxCols {
			rows[i] = append(rows[i], "")
		}
	}
	return ImportTable{Headers: rows[0], Rows: rows[1:], Format: "xlsx"}, nil
}

func xlsxColumnIndex(ref string) (int, error) {
	ref = strings.ToUpper(strings.TrimSpace(ref))
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		i++
	}
	if i == 0 {
		return 0, errors.New("XLSX 单元格引用无效")
	}
	column := 0
	for _, r := range ref[:i] {
		column = column*26 + int(r-'A'+1)
		if column > maxImportCols {
			return 0, errors.New("XLSX 列号过大")
		}
	}
	return column - 1, nil
}

func findZipFile(files []*zip.File, name string) *zip.File {
	for _, file := range files {
		if file.Name == name {
			return file
		}
	}
	return nil
}

func (m *Manager) PreviewImport(ctx context.Context, source Source, schema, table, format string, data []byte) (ImportPreview, error) {
	parsed, err := ParseImportData(format, data)
	if err != nil {
		return ImportPreview{}, err
	}
	fields, err := m.Fields(ctx, source, schema, table)
	if err != nil {
		return ImportPreview{}, err
	}
	fieldByName := make(map[string]Field, len(fields))
	for _, field := range fields {
		fieldByName[strings.ToUpper(field.Name)] = field
	}
	mapping := make([]ImportMapping, 0, len(parsed.Headers))
	for i, header := range parsed.Headers {
		field, ok := fieldByName[strings.ToUpper(strings.TrimSpace(header))]
		item := ImportMapping{SourceIndex: i, SourceName: header}
		if ok {
			item.Target, item.Type, item.Nullable = field.Name, NormalizeImportType(source.Kind, field.DataType), field.Nullable
		}
		mapping = append(mapping, item)
	}
	previewRows := len(parsed.Rows)
	if previewRows > 100 {
		previewRows = 100
	}
	return ImportPreview{Table: ImportTable{Headers: parsed.Headers, Rows: parsed.Rows[:previewRows], Format: parsed.Format}, Fields: fields, SuggestedMap: mapping, TotalRows: len(parsed.Rows), PreviewRows: previewRows, Truncated: len(parsed.Rows) > previewRows}, nil
}

// NormalizeImportType maps database-native metadata types to the small,
// explicit conversion vocabulary accepted by BindParameter.
func NormalizeImportType(kind, dataType string) string {
	base := strings.ToUpper(strings.TrimSpace(strings.SplitN(dataType, "(", 2)[0]))
	switch base {
	case "CHAR", "CHARACTER", "VARCHAR", "VARCHAR2", "NCHAR", "NVARCHAR", "NVARCHAR2", "TEXT", "TINYTEXT", "MEDIUMTEXT", "LONGTEXT", "CLOB", "NCLOB", "LONG":
		return "string"
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT":
		return "int64"
	case "NUMBER", "DECIMAL", "NUMERIC":
		return "decimal"
	case "FLOAT", "REAL", "DOUBLE", "BINARY_FLOAT", "BINARY_DOUBLE":
		return "float"
	case "BOOLEAN", "BOOL":
		return "bool"
	case "DATE":
		if kind == KindOracle {
			return "datetime"
		}
		return "date"
	case "DATETIME", "TIMESTAMP", "TIMESTAMP WITH TIME ZONE", "TIMESTAMP WITH LOCAL TIME ZONE":
		return "datetime"
	case "BINARY", "VARBINARY", "BLOB", "LONGBLOB", "MEDIUMBLOB", "TINYBLOB", "RAW", "LONG RAW":
		return "bytes"
	case "JSON":
		return "json"
	default:
		_ = kind
		return "string"
	}
}

func importMappedValue(kind, raw string, mapping ImportMapping) (any, error) {
	if strings.TrimSpace(raw) == "" && mapping.Nullable {
		return nil, nil
	}
	typ := strings.ToLower(strings.TrimSpace(mapping.Type))
	if typ == "" {
		typ = "string"
	}
	if typ == "number" || (kind == KindOracle && typ == "date") || !isSupportedImportBindType(typ) {
		typ = NormalizeImportType(kind, typ)
	}
	return convertBindValue(BindParameter{Name: "import", Type: typ, Value: raw, Null: strings.TrimSpace(raw) == "" && mapping.Nullable})
}

func isSupportedImportBindType(typ string) bool {
	switch typ {
	case "string", "text", "int", "int64", "uint", "float", "float64", "number", "decimal", "bool", "boolean", "date", "datetime", "timestamp", "bytes", "blob", "binary", "json", "null":
		return true
	default:
		return false
	}
}

func buildImportInsert(kind, schema, table string, mappings []ImportMapping, row []string) (string, []any, error) {
	if len(mappings) == 0 || len(mappings) > maxImportCols {
		return "", nil, errors.New("导入映射不能为空或过多")
	}
	qualified, err := gridQualifiedTable(kind, schema, table)
	if err != nil {
		return "", nil, err
	}
	cols := make([]string, 0, len(mappings))
	markers := make([]string, 0, len(mappings))
	args := make([]any, 0, len(mappings))
	seen := make(map[string]bool)
	for i, mapping := range mappings {
		if mapping.SourceIndex < 0 || mapping.SourceIndex >= len(row) {
			return "", nil, fmt.Errorf("映射 source_index %d 超出行范围", mapping.SourceIndex)
		}
		if strings.TrimSpace(mapping.Target) == "" {
			continue
		}
		column, err := quoteGridIdentifier(kind, mapping.Target, "目标列")
		if err != nil {
			return "", nil, err
		}
		key := strings.ToUpper(mapping.Target)
		if seen[key] {
			return "", nil, fmt.Errorf("目标列重复: %s", mapping.Target)
		}
		seen[key] = true
		value, err := importMappedValue(kind, row[mapping.SourceIndex], mapping)
		if err != nil {
			return "", nil, fmt.Errorf("第 %d 个映射（%s）: %w", i+1, mapping.Target, err)
		}
		cols = append(cols, column)
		if kind == KindOracle {
			name := fmt.Sprintf("kairo_import_%d", len(args)+1)
			markers = append(markers, ":"+name)
			args = append(args, sql.Named(name, value))
		} else {
			markers = append(markers, "?")
			args = append(args, value)
		}
	}
	if len(cols) == 0 {
		return "", nil, errors.New("没有可导入的目标列")
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", qualified, strings.Join(cols, ", "), strings.Join(markers, ", ")), args, nil
}

func (m *Manager) ApplyImport(ctx context.Context, source Source, req ImportApplyRequest) (ImportApplyResult, error) {
	if !source.MutationAllowed() {
		return ImportApplyResult{}, errors.New("该数据源处于只读锁定状态")
	}
	if source.IsProduction() && !req.Confirm {
		return ImportApplyResult{}, errors.New("生产数据源导入需要 confirm=true")
	}
	if strings.TrimSpace(req.SessionID) == "" || !validGridSessionID(req.SessionID) {
		return ImportApplyResult{}, errors.New("导入必须绑定有效 session_id")
	}
	if len(req.Rows) == 0 || len(req.Rows) > maxImportRows {
		return ImportApplyResult{}, fmt.Errorf("导入行数必须在 1..%d 之间", maxImportRows)
	}
	totalBytes := 0
	for _, row := range req.Rows {
		if len(row) > maxImportCols {
			return ImportApplyResult{}, errors.New("导入列数超过限制")
		}
		for _, cell := range row {
			if len(cell) > maxCellBytes {
				return ImportApplyResult{}, errors.New("导入单元格超过 256KB 限制")
			}
			totalBytes += len(cell)
			if totalBytes > maxImportBytes {
				return ImportApplyResult{}, errors.New("导入数据超过 8MB 限制")
			}
		}
	}
	queryCtx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(queryCtx); err != nil {
		return ImportApplyResult{}, err
	}
	defer m.release()
	entry, err := m.transactionForContext(queryCtx, source, req.SessionID, true)
	if err != nil {
		return ImportApplyResult{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	started := time.Now()
	result := ImportApplyResult{Rows: make([]ImportRowResult, len(req.Rows))}
	var prepared *sql.Stmt
	defer func() {
		if prepared != nil {
			_ = prepared.Close()
		}
	}()
	for i, row := range req.Rows {
		result.Rows[i] = ImportRowResult{Row: i + 1, Status: "failed"}
		statement, args, buildErr := buildImportInsert(source.Kind, req.Schema, req.Table, req.Mappings, row)
		if buildErr != nil {
			result.Rows[i].Error = buildErr.Error()
			m.rollbackEntryLocked(source.ID, req.SessionID, entry)
			result.RolledBack = true
			return result, buildErr
		}
		if prepared == nil {
			var prepareErr error
			prepared, prepareErr = entry.tx.PrepareContext(queryCtx, statement)
			if prepareErr != nil {
				result.Rows[i].Error = prepareErr.Error()
				m.rollbackEntryLocked(source.ID, req.SessionID, entry)
				result.RolledBack = true
				return result, prepareErr
			}
		}
		execResult, execErr := prepared.ExecContext(queryCtx, args...)
		if execErr != nil {
			result.Rows[i].Error = execErr.Error()
			m.rollbackEntryLocked(source.ID, req.SessionID, entry)
			result.RolledBack = true
			return result, execErr
		}
		affected, affectedErr := execResult.RowsAffected()
		if affectedErr != nil {
			result.Rows[i].Error = affectedErr.Error()
			m.rollbackEntryLocked(source.ID, req.SessionID, entry)
			result.RolledBack = true
			return result, affectedErr
		}
		if affected != 1 {
			result.Rows[i].Error = fmt.Sprintf("导入第 %d 行影响 %d 行，已回滚", i+1, affected)
			m.rollbackEntryLocked(source.ID, req.SessionID, entry)
			result.RolledBack = true
			return result, errors.New(result.Rows[i].Error)
		}
		result.Rows[i].RowsAffected = affected
		result.Rows[i].Status = "succeeded"
		result.RowsAffected += affected
		result.Processed++
	}
	entry.updatedAt = time.Now()
	result.TransactionPending = true
	if req.Commit {
		if err := m.commitEntryLocked(source.ID, req.SessionID, entry); err != nil {
			result.TransactionPending = false
			return result, err
		}
		result.Committed = true
		result.TransactionPending = false
	}
	result.ElapsedMS = time.Since(started).Milliseconds()
	return result, nil
}

// Ensure deterministic output for callers that build their own mapping from a
// map while keeping the implementation allocation-friendly on low-end hosts.
func SortImportMappings(mappings []ImportMapping) {
	sort.SliceStable(mappings, func(i, j int) bool { return mappings[i].SourceIndex < mappings[j].SourceIndex })
}
