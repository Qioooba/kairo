package dbconsole

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	rowBatchSize = 100
	maxCellBytes = 256 << 10
)

type StreamEvent struct {
	Type    string        `json:"type"`
	Columns []Column      `json:"columns,omitempty"`
	Rows    [][]any       `json:"rows,omitempty"`
	Summary *QuerySummary `json:"summary,omitempty"`
	Error   string        `json:"error,omitempty"`
}

type EmitFunc func(StreamEvent) error

func (m *Manager) StreamQuery(ctx context.Context, source Source, query string, requestedMaxRows int, emit EmitFunc) (QuerySummary, error) {
	return m.StreamQueryPage(ctx, source, query, 1, requestedMaxRows, emit)
}

// StreamQueryPage executes one bounded page. Events are collected before they
// are emitted so an idle-timeout connection can be rebuilt and retried once
// without duplicating already-visible rows in the browser.
func (m *Manager) StreamQueryPage(ctx context.Context, source Source, query string, page, pageSize int, emit EmitFunc) (QuerySummary, error) {
	if source.Kind == KindRedis {
		return QuerySummary{}, fmt.Errorf("Redis 不支持 SQL 查询")
	}
	if err := ValidateReadOnlySQL(source.Kind, query); err != nil {
		return QuerySummary{}, err
	}
	p := normalizeQueryPage(source, page, pageSize)
	var last QuerySummary
	for attempt := 0; attempt < 2; attempt++ {
		events, summary, err := m.streamQueryAttempt(ctx, source, query, p)
		summary.RetryCount = attempt
		last = summary
		if err == nil {
			for _, event := range events {
				if emit != nil {
					if emitErr := emit(event); emitErr != nil {
						return summary, emitErr
					}
				}
			}
			return summary, nil
		}
		if attempt == 0 && isConnectionFailure(err) {
			m.Invalidate(source.ID)
			continue
		}
		return summary, err
	}
	return last, fmt.Errorf("连接重试失败")
}

func (m *Manager) streamQueryAttempt(ctx context.Context, source Source, query string, page QueryPage) ([]StreamEvent, QuerySummary, error) {
	queryCtx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(queryCtx); err != nil {
		return nil, QuerySummary{}, err
	}
	defer m.release()
	db, err := m.sqlDB(source)
	if err != nil {
		return nil, QuerySummary{}, err
	}
	started := time.Now()
	txOptions := &sql.TxOptions{ReadOnly: source.Kind == KindMySQL}
	tx, err := db.BeginTx(queryCtx, txOptions)
	if err != nil {
		return nil, QuerySummary{}, err
	}
	defer tx.Rollback()
	if source.Kind == KindOracle {
		if _, err := tx.ExecContext(queryCtx, "SET TRANSACTION READ ONLY"); err != nil {
			return nil, QuerySummary{}, fmt.Errorf("启用 Oracle 只读事务失败: %w", err)
		}
	}
	limitedQuery, err := serverPagedQuery(source.Kind, query, page)
	if err != nil {
		return nil, QuerySummary{}, err
	}
	rows, err := tx.QueryContext(queryCtx, limitedQuery)
	if err != nil {
		return nil, QuerySummary{}, err
	}
	defer rows.Close()
	columns, err := resultColumns(rows)
	if err != nil {
		return nil, QuerySummary{}, err
	}
	aliasIdx := -1
	for i, c := range columns {
		if strings.HasPrefix(strings.ToUpper(c.Name), "__KAIRO_RN_") {
			aliasIdx = i
			break
		}
	}
	if aliasIdx >= 0 {
		columns = append(columns[:aliasIdx], columns[aliasIdx+1:]...)
	}
	if err := rejectLargeObjectColumns(source.Kind, columns); err != nil {
		return nil, QuerySummary{}, err
	}
	summary := QuerySummary{
		QueryLimit: page.PageSize, Page: page.Page, PageSize: page.PageSize,
		Offset: page.Offset(), HasPrev: page.Page > 1, PaginationMode: "page",
		Ordered: queryHasOrderBy(query),
	}
	events := []StreamEvent{{Type: "meta", Columns: columns}}
	batch := make([][]any, 0, rowBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		events = append(events, StreamEvent{Type: "rows", Rows: batch})
		batch = make([][]any, 0, rowBatchSize)
	}
	for rows.Next() {
		scanCount := len(columns)
		if aliasIdx >= 0 {
			scanCount = len(columns) + 1
		}
		row, rowBytes, scanErr := scanRow(rows, scanCount)
		if scanErr != nil {
			return nil, summary, scanErr
		}
		if aliasIdx >= 0 && aliasIdx < len(row) {
			row = append(row[:aliasIdx], row[aliasIdx+1:]...)
			if raw, err := json.Marshal(row); err == nil {
				rowBytes = int64(len(raw))
			}
		}
		if summary.Rows >= page.PageSize {
			summary.HasNext = true
			break
		}
		if summary.Bytes+rowBytes > source.MaxResultBytes {
			summary.Truncated = true
			break
		}
		summary.Rows++
		summary.Bytes += rowBytes
		batch = append(batch, row)
		if len(batch) == rowBatchSize {
			flush()
		}
	}
	if err := rows.Err(); err != nil {
		return nil, summary, err
	}
	flush()
	summary.ElapsedMS = time.Since(started).Milliseconds()
	events = append(events, StreamEvent{Type: "summary", Summary: &summary})
	return events, summary, nil
}

// serverLimitedQuery enforces the result cap at the database boundary. The
// extra row is used only to report that the visible result was truncated.
// Oracle deliberately uses ROWNUM rather than FETCH FIRST for 11g support.
func serverLimitedQuery(kind, query string, maxRows int) (string, error) {
	query = strings.TrimSpace(query)
	query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
	if query == "" || maxRows < 1 {
		return "", fmt.Errorf("查询或行数上限无效")
	}
	tokens, _, err := sqlTokens(query)
	if err != nil {
		return "", fmt.Errorf("解析查询失败: %w", err)
	}
	if len(tokens) == 0 {
		return "", fmt.Errorf("解析查询失败: SQL 不能为空")
	}
	switch kind {
	case KindOracle:
		return fmt.Sprintf("SELECT * FROM (\n%s\n) WHERE ROWNUM <= %d", query, maxRows), nil
	case KindMySQL:
		if tokens[0] != "SELECT" && tokens[0] != "WITH" {
			// SHOW/DESC/EXPLAIN already return bounded metadata-like results and
			// cannot be placed in a derived table.
			return query, nil
		}
		return fmt.Sprintf("SELECT * FROM (\n%s\n) AS kairo_limited_query LIMIT %d", query, maxRows), nil
	default:
		return "", fmt.Errorf("%s 不是 SQL 数据源", kind)
	}
}

func isConnectionFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) {
		return true
	}
	text := strings.ToUpper(err.Error())
	for _, marker := range []string{
		"MYSQL SERVER HAS GONE AWAY", "ERROR 2006", "ERROR 2013", "BROKEN PIPE",
		"CONNECTION RESET", "CONNECTION IS CLOSED", "USE OF CLOSED NETWORK CONNECTION",
		"ORA-03113", "ORA-03114", "ORA-01012", "ORA-12537",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func rejectLargeObjectColumns(kind string, columns []Column) error {
	blocked := map[string]struct{}{
		"BLOB": {}, "CLOB": {}, "NCLOB": {}, "LONG": {}, "LONG RAW": {},
		"MEDIUMBLOB": {}, "LONGBLOB": {}, "MEDIUMTEXT": {}, "LONGTEXT": {},
		"XMLTYPE": {}, "JSON": {}, "GEOMETRY": {},
	}
	for _, column := range columns {
		if _, found := blocked[strings.ToUpper(strings.TrimSpace(column.Database))]; found {
			if kind == KindOracle {
				return fmt.Errorf("列 %s 为 %s；V1 为保护生产内存不直接读取大对象，请用 DBMS_LOB.SUBSTR(列, 4000, 1) 做受控预览", column.Name, column.Database)
			}
			return fmt.Errorf("列 %s 为 %s；V1 为保护生产内存不直接读取大对象，请用 SUBSTRING(列, 1, 65535) 做受控预览", column.Name, column.Database)
		}
	}
	return nil
}

func resultColumns(rows *sql.Rows) ([]Column, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	columns := make([]Column, len(types))
	for i, ct := range types {
		nullable, _ := ct.Nullable()
		columns[i] = Column{Name: ct.Name(), Database: ct.DatabaseTypeName(), Nullable: nullable}
	}
	return columns, nil
}

func scanRow(rows *sql.Rows, count int) ([]any, int64, error) {
	values := make([]any, count)
	dest := make([]any, count)
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, 0, err
	}
	for i := range values {
		values[i] = normalizeValue(values[i])
	}
	raw, _ := json.Marshal(values)
	return values, int64(len(raw)), nil
}

func normalizeValue(value any) any {
	switch v := value.(type) {
	case nil, bool, int64, float64:
		return v
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case string:
		return normalizeBytes([]byte(v))
	case []byte:
		return normalizeBytes(v)
	case fmt.Stringer:
		return truncateText(v.String())
	default:
		rv := reflect.ValueOf(value)
		if rv.IsValid() && rv.Kind() == reflect.Ptr && !rv.IsNil() {
			return normalizeValue(rv.Elem().Interface())
		}
		return truncateText(fmt.Sprint(value))
	}
}

func normalizeBytes(value []byte) any {
	copyValue := append([]byte(nil), value...)
	if utf8.Valid(copyValue) {
		return truncateText(string(copyValue))
	}
	preview := copyValue
	if len(preview) > 4096 {
		preview = preview[:4096]
	}
	return map[string]any{
		"kind":           "binary",
		"bytes":          len(copyValue),
		"preview_base64": base64.StdEncoding.EncodeToString(preview),
		"truncated":      len(preview) < len(copyValue),
	}
}

func truncateText(value string) any {
	if len(value) <= maxCellBytes {
		return value
	}
	cut := maxCellBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return map[string]any{
		"kind":      "text",
		"bytes":     len(value),
		"preview":   value[:cut],
		"truncated": true,
	}
}
