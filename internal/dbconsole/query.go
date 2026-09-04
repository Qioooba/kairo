package dbconsole

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
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
	Type          string        `json:"type"`
	Columns       []Column      `json:"columns,omitempty"`
	Rows          [][]any       `json:"rows,omitempty"`
	Summary       *QuerySummary `json:"summary,omitempty"`
	Error         string        `json:"error,omitempty"`
	Message       string        `json:"message,omitempty"`
	StatementType string        `json:"statement_type,omitempty"`
	RowsAffected  int64         `json:"rows_affected,omitempty"`
}

type EmitFunc func(StreamEvent) error

func (m *Manager) StreamQuery(ctx context.Context, source Source, query string, requestedMaxRows int, emit EmitFunc) (QuerySummary, error) {
	return m.StreamQueryPage(ctx, source, query, 1, requestedMaxRows, emit)
}

// StreamQueryPage executes one bounded page. Events are emitted in a true streaming
// fashion as batches are read from the database, preventing resident memory accumulation.
// Idle-timeout connection rebuilding and retrying is performed safely before any data is emitted.
func (m *Manager) StreamQueryPage(ctx context.Context, source Source, query string, page, pageSize int, emit EmitFunc) (QuerySummary, error) {
	if source.Kind == KindRedis {
		return QuerySummary{}, fmt.Errorf("Redis 不支持 SQL 查询")
	}
	info, err := ValidateSQL(source.Kind, query)
	if err != nil {
		return QuerySummary{}, err
	}
	if !info.IsQuery {
		return m.ExecuteStatement(ctx, source, query, info, emit)
	}
	p := normalizeQueryPage(source, page, pageSize)
	var last QuerySummary
	for attempt := 0; attempt < 2; attempt++ {
		startedEmit := false
		safeEmit := func(event StreamEvent) error {
			startedEmit = true
			if emit != nil {
				return emit(event)
			}
			return nil
		}
		summary, err := m.streamQueryAttempt(ctx, source, query, p, info, safeEmit)
		summary.RetryCount = attempt
		last = summary
		if err == nil {
			return summary, nil
		}
		// 重试仅在尚未向客户端输出任何数据且属于连接故障时允许
		if attempt == 0 && !startedEmit && isConnectionFailure(err) {
			m.Invalidate(source.ID)
			continue
		}
		return summary, err
	}
	return last, fmt.Errorf("连接重试失败")
}

// ExecuteStatement executes DML (UPDATE, INSERT, DELETE, MERGE) or DDL (CREATE, ALTER, DROP, TRUNCATE) statements.
func (m *Manager) ExecuteStatement(ctx context.Context, source Source, query string, info SQLStatementInfo, emit EmitFunc) (QuerySummary, error) {
	queryCtx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(queryCtx); err != nil {
		return QuerySummary{}, err
	}
	defer m.release()
	db, err := m.sqlDB(source)
	if err != nil {
		return QuerySummary{}, err
	}
	started := time.Now()
	cleanQuery := strings.TrimRight(strings.TrimSpace(query), "; \t\r\n")
	res, err := db.ExecContext(queryCtx, cleanQuery)
	if err != nil {
		return QuerySummary{}, err
	}
	var rowsAffected int64 = -1
	if ra, raErr := res.RowsAffected(); raErr == nil {
		rowsAffected = ra
	}
	elapsed := time.Since(started).Milliseconds()
	msg := fmt.Sprintf("执行成功: %s", info.Action)
	if rowsAffected >= 0 {
		msg = fmt.Sprintf("执行成功: %s 影响 %d 行", info.Action, rowsAffected)
	}
	summary := QuerySummary{
		ElapsedMS:     elapsed,
		Rows:          int(rowsAffected),
		RowsAffected:  rowsAffected,
		StatementType: info.Type,
		Message:       msg,
	}
	event := StreamEvent{
		Type:          "mutation",
		Summary:       &summary,
		Message:       msg,
		StatementType: info.Type,
		RowsAffected:  rowsAffected,
	}
	if emit != nil {
		_ = emit(event)
	}
	return summary, nil
}

// ExecuteBatch atomically executes multiple DML statements in a single transaction.
// All statements are validated via ValidateSQL and must be DML/TRANSACTION; DDL is rejected for batch atomicity.
// If any statement fails the whole batch is rolled back.
func (m *Manager) ExecuteBatch(ctx context.Context, source Source, statements []string) (QuerySummary, error) {
	if len(statements) == 0 {
		return QuerySummary{}, errors.New("批量语句不能为空")
	}
	if len(statements) > 50 {
		return QuerySummary{}, errors.New("批量语句数量超出限制 (最多 50 条)")
	}
	// Pre-validate all statements
	for _, stmt := range statements {
		info, err := ValidateSQL(source.Kind, stmt)
		if err != nil {
			return QuerySummary{}, err
		}
		if info.Type == "DDL" {
			return QuerySummary{}, fmt.Errorf("批量执行不支持 DDL 语句: %s", info.Action)
		}
		if info.IsQuery {
			return QuerySummary{}, fmt.Errorf("批量执行仅支持 DML/事务语句，禁止查询: %s", stmt)
		}
	}
	queryCtx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(queryCtx); err != nil {
		return QuerySummary{}, err
	}
	defer m.release()
	db, err := m.sqlDB(source)
	if err != nil {
		return QuerySummary{}, err
	}
	started := time.Now()
	tx, err := db.BeginTx(queryCtx, nil)
	if err != nil {
		return QuerySummary{}, err
	}
	defer tx.Rollback()
	var totalAffected int64
	for _, stmt := range statements {
		trimmed := strings.TrimSpace(stmt)
		upper := strings.ToUpper(trimmed)
		if upper == "COMMIT" || upper == "ROLLBACK" {
			continue
		}
		clean := strings.TrimRight(strings.TrimSpace(stmt), "; \t\r\n")
		res, err := tx.ExecContext(queryCtx, clean)
		if err != nil {
			return QuerySummary{}, err
		}
		if ra, raErr := res.RowsAffected(); raErr == nil && ra >= 0 {
			totalAffected += ra
		}
	}
	if err := tx.Commit(); err != nil {
		return QuerySummary{}, err
	}
	elapsed := time.Since(started).Milliseconds()
	msg := fmt.Sprintf("批量执行成功: %d 条语句，共影响 %d 行", len(statements), totalAffected)
	summary := QuerySummary{
		ElapsedMS:     elapsed,
		Rows:          int(totalAffected),
		RowsAffected:  totalAffected,
		StatementType: "DML_BATCH",
		Message:       msg,
	}
	return summary, nil
}

func (m *Manager) streamQueryAttempt(ctx context.Context, source Source, query string, page QueryPage, info SQLStatementInfo, emit EmitFunc) (QuerySummary, error) {
	queryCtx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(queryCtx); err != nil {
		return QuerySummary{}, err
	}
	defer m.release()
	db, err := m.sqlDB(source)
	if err != nil {
		return QuerySummary{}, err
	}
	started := time.Now()
	// FOR UPDATE 为行级锁，HTTP 无状态不做会话级持锁：事务在请求结束时立即回滚释放，避免长持锁阻塞生产
	txOptions := &sql.TxOptions{ReadOnly: !info.HasForUpdate && source.Kind == KindMySQL}
	tx, err := db.BeginTx(queryCtx, txOptions)
	if err != nil {
		return QuerySummary{}, err
	}
	defer tx.Rollback()
	if source.Kind == KindOracle && !info.HasForUpdate {
		_, _ = tx.ExecContext(queryCtx, "SET TRANSACTION READ ONLY")
	}
	limitedQuery, err := serverPagedQuery(source.Kind, query, page)
	if err != nil {
		return QuerySummary{}, err
	}
	rows, err := tx.QueryContext(queryCtx, limitedQuery)
	if err != nil {
		return QuerySummary{}, err
	}
	defer rows.Close()
	columns, err := resultColumns(rows)
	if err != nil {
		return QuerySummary{}, err
	}
	aliasIdx := -1
	for i, c := range columns {
		colName := strings.Trim(strings.ToUpper(c.Name), "\"`[] \t")
		if strings.HasPrefix(colName, "__KAIRO_RN_") {
			aliasIdx = i
			break
		}
	}
	if aliasIdx >= 0 {
		columns = append(columns[:aliasIdx], columns[aliasIdx+1:]...)
	}

	// 此时连接与首包已就绪，立即向调用方流式发射元数据
	if emit != nil {
		if err := emit(StreamEvent{Type: "meta", Columns: columns}); err != nil {
			return QuerySummary{}, err
		}
	}

	summary := QuerySummary{
		QueryLimit: page.PageSize, Page: page.Page, PageSize: page.PageSize,
		Offset: page.Offset(), HasPrev: page.Page > 1, PaginationMode: "page",
		Ordered: queryHasOrderBy(query),
	}

	batch := make([][]any, 0, rowBatchSize)
	scanner := newRowScanner(columns, aliasIdx)

	for rows.Next() {
		row, rowBytes, scanErr := scanner.Scan(rows)
		if scanErr != nil {
			return summary, scanErr
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
			if emit != nil {
				if err := emit(StreamEvent{Type: "rows", Rows: batch}); err != nil {
					return summary, err
				}
			}
			// 立即重新分配批次，断开对旧批次行的强引用，让 Go GC 可以在查询持续进行期间平滑回收内存
			batch = make([][]any, 0, rowBatchSize)
		}
	}
	if err := rows.Err(); err != nil {
		return summary, err
	}
	if len(batch) > 0 {
		if emit != nil {
			if err := emit(StreamEvent{Type: "rows", Rows: batch}); err != nil {
				return summary, err
			}
		}
		batch = nil
	}
	summary.ElapsedMS = time.Since(started).Milliseconds()
	summary.StatementType = info.Type
	if info.HasForUpdate {
		// 告知前端：FOR UPDATE 已执行但锁已随事务回滚释放，非持久会话锁
		summary.Message = "FOR UPDATE 查询已执行；行锁随请求结束已释放（HTTP 无状态，不做会话级持锁）"
		if emit != nil {
			_ = emit(StreamEvent{Type: "notice", Message: summary.Message, StatementType: info.Type})
		}
	}
	if emit != nil {
		_ = emit(StreamEvent{Type: "summary", Summary: &summary})
	}
	return summary, nil
}

// serverLimitedQuery enforces the result cap at the database boundary. The
// extra row is used only to report that the visible result was truncated.
// Oracle deliberately uses ROWNUM rather than FETCH FIRST for 11g support.
func serverLimitedQuery(kind, query string, maxRows int) (string, error) {
	query = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), "; \t\r\n"))
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

type boundedCellScanner struct {
	dbType string
	value  any
}

func (s *boundedCellScanner) Scan(src any) error {
	s.value = normalizeColumnValue(src, s.dbType)
	return nil
}

type rowScanner struct {
	scanners []boundedCellScanner
	dest     []any
	columns  []Column
	aliasIdx int
}

func newRowScanner(columns []Column, aliasIdx int) *rowScanner {
	count := len(columns)
	if aliasIdx >= 0 {
		count = len(columns) + 1
	}
	scanners := make([]boundedCellScanner, count)
	dest := make([]any, count)
	for i := range scanners {
		dbType := ""
		if aliasIdx >= 0 {
			if i < aliasIdx && i < len(columns) {
				dbType = strings.ToUpper(strings.TrimSpace(columns[i].Database))
			} else if i > aliasIdx && i-1 < len(columns) {
				dbType = strings.ToUpper(strings.TrimSpace(columns[i-1].Database))
			}
		} else if i < len(columns) {
			dbType = strings.ToUpper(strings.TrimSpace(columns[i].Database))
		}
		scanners[i].dbType = dbType
		dest[i] = &scanners[i]
	}
	return &rowScanner{
		scanners: scanners,
		dest:     dest,
		columns:  columns,
		aliasIdx: aliasIdx,
	}
}

func (rs *rowScanner) Scan(rows *sql.Rows) ([]any, int64, error) {
	if err := rows.Scan(rs.dest...); err != nil {
		return nil, 0, err
	}
	values := make([]any, len(rs.columns))
	valIdx := 0
	for i := range rs.scanners {
		if i == rs.aliasIdx {
			continue
		}
		values[valIdx] = rs.scanners[i].value
		rs.scanners[i].value = nil // 立即解除对单格对象的引用，辅助 GC 回收
		valIdx++
	}
	return values, fastRowBytes(values), nil
}

func scanRow(rows *sql.Rows, count int, columns []Column, aliasIdx int) ([]any, int64, error) {
	scanner := newRowScanner(columns, aliasIdx)
	return scanner.Scan(rows)
}

func fastRowBytes(row []any) int64 {
	var total int64
	for _, v := range row {
		if v == nil {
			total += 4 // "null"
			continue
		}
		switch val := v.(type) {
		case string:
			total += int64(len(val)) + 2 // 包含 JSON 引号
		case []byte:
			total += int64(len(val))
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			total += 8
		case float32, float64:
			total += 16
		case bool:
			total += 5
		case time.Time:
			total += 35
		case map[string]any:
			if b, ok := val["bytes"].(int); ok {
				total += int64(b)
			} else if t, ok := val["text"].(string); ok {
				total += int64(len(t))
			} else if p, ok := val["preview"].(string); ok {
				total += int64(len(p))
			} else {
				total += 128
			}
		default:
			total += 64
		}
		total += 1 // 字段分隔符
	}
	return total
}

const maxLOBPreviewBytes = 256 * 1024 // 256 KB preview limit for CLOB/BLOB

func normalizeColumnValue(value any, dbType string) any {
	if value == nil {
		return nil
	}
	isClob := strings.Contains(dbType, "CLOB") || dbType == "LONG" || dbType == "LONGTEXT" || dbType == "MEDIUMTEXT"
	isBlob := strings.Contains(dbType, "BLOB") || dbType == "LONG RAW"

	if isClob {
		var text string
		switch v := value.(type) {
		case string:
			text = v
		case []byte:
			text = string(v)
		default:
			text = fmt.Sprint(v)
		}
		totalBytes := len(text)
		truncated := false
		if totalBytes > maxLOBPreviewBytes {
			cut := maxLOBPreviewBytes
			for cut > 0 && !utf8.RuneStart(text[cut]) {
				cut--
			}
			// 使用 strings.Clone 彻底断开与原始巨型 string/[]byte 底层数组的内存引用，
			// 确保几十甚至上百 MB 的原始大对象在截断后能够被 Go GC 立即回收。
			text = strings.Clone(text[:cut])
			truncated = true
		}
		return map[string]any{
			"kind":      "clob",
			"bytes":     totalBytes,
			"text":      text,
			"truncated": truncated,
		}
	}

	if !isBlob && (dbType == "RAW" || dbType == "BINARY" || dbType == "VARBINARY") {
		// Only treat as LOB if larger than 256 bytes; small RAW (e.g. UUID) remains normal inline value
		var rawLen int
		switch v := value.(type) {
		case []byte:
			rawLen = len(v)
		case string:
			rawLen = len(v)
		}
		if rawLen > 256 {
			isBlob = true
		}
	}

	if isBlob {
		var rawBytes []byte
		switch v := value.(type) {
		case []byte:
			rawBytes = v
		case string:
			rawBytes = []byte(v)
		default:
			rawBytes = []byte(fmt.Sprint(v))
		}
		totalBytes := len(rawBytes)
		truncated := false
		var preview []byte
		if totalBytes > maxLOBPreviewBytes {
			// 使用 bytes.Clone 断开与底层大切片的共享引用
			preview = bytes.Clone(rawBytes[:maxLOBPreviewBytes])
			truncated = true
		} else {
			preview = rawBytes
		}
		return map[string]any{
			"kind":           "blob",
			"bytes":          totalBytes,
			"hex":            hex.EncodeToString(preview),
			"preview_base64": base64.StdEncoding.EncodeToString(preview),
			"truncated":      truncated,
		}
	}

	return normalizeValue(value)
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
	copyValue := bytes.Clone(value)
	if utf8.Valid(copyValue) {
		return truncateText(string(copyValue))
	}
	preview := copyValue
	truncated := false
	if len(preview) > 4096 {
		preview = bytes.Clone(copyValue[:4096])
		truncated = true
	}
	return map[string]any{
		"kind":           "binary",
		"bytes":          len(copyValue),
		"preview_base64": base64.StdEncoding.EncodeToString(preview),
		"truncated":      truncated,
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
		"preview":   strings.Clone(value[:cut]),
		"truncated": true,
	}
}
