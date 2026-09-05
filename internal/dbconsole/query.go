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
	return m.StreamSessionQueryPage(ctx, source, query, page, pageSize, "", emit)
}

// StreamSessionQueryPage binds DML and transaction control to one browser tab.
// An empty sessionID preserves the legacy stateless behavior for internal callers.
func (m *Manager) StreamSessionQueryPage(ctx context.Context, source Source, query string, page, pageSize int, sessionID string, emit EmitFunc) (QuerySummary, error) {
	return m.StreamSessionQueryPageWithParams(ctx, source, query, page, pageSize, sessionID, nil, emit)
}

// StreamSessionQueryPageWithParams is the parameterized counterpart used by
// the HTTP query endpoint.  Binding happens before any database call and the
// rewritten SQL is still passed through the same classifier/paginator.
func (m *Manager) StreamSessionQueryPageWithParams(ctx context.Context, source Source, query string, page, pageSize int, sessionID string, params []BindParameter, emit EmitFunc) (QuerySummary, error) {
	if source.Kind == KindRedis {
		return QuerySummary{}, fmt.Errorf("Redis 不支持 SQL 查询")
	}
	boundQuery, boundArgs, bindErr := BindSQLParameters(source.Kind, query, params)
	if bindErr != nil {
		return QuerySummary{}, bindErr
	}
	info, err := ValidateSQL(source.Kind, boundQuery)
	if err != nil {
		return QuerySummary{}, err
	}
	if !info.IsQuery {
		if sessionID != "" {
			return m.executeSessionStatement(ctx, source, boundQuery, boundArgs, info, sessionID, emit)
		}
		return m.executeStatementWithArgs(ctx, source, boundQuery, boundArgs, info, emit)
	}
	p := normalizeQueryPage(source, page, pageSize)
	hadSessionTransaction := sessionID != "" && m.SessionTransactionPending(source, sessionID)
	var last QuerySummary
	for attempt := 0; attempt < 2; attempt++ {
		emittedRows := false
		safeEmit := func(event StreamEvent) error {
			// Oracle may expose column metadata before ORA-01466 is raised while
			// fetching the first row after a concurrent DDL.  Retrying is still
			// safe until an actual data row has reached the client; a repeated
			// metadata event is idempotent for the streaming consumers.
			if len(event.Rows) > 0 {
				emittedRows = true
			}
			if emit != nil {
				return emit(event)
			}
			return nil
		}
		summary, err := m.streamQueryAttempt(ctx, source, boundQuery, boundArgs, p, info, sessionID, safeEmit)
		summary.RetryCount = attempt
		last = summary
		if err == nil {
			return summary, nil
		}
		// 重试仅在尚未向客户端输出任何数据行且属于可恢复故障时允许。
		if attempt == 0 && !hadSessionTransaction && !emittedRows && isRetryableQueryFailure(err) {
			m.invalidatePool(source.ID)
			continue
		}
		return summary, err
	}
	return last, fmt.Errorf("连接重试失败")
}

// Oracle can return ORA-01466 for the first read-only transaction opened on
// a pooled connection immediately after this workbench changes an object's
// definition. Reopening the pool gives the retry a fresh snapshot while still
// preserving the read-only transaction guard.
func isRetryableQueryFailure(err error) bool {
	if isConnectionFailure(err) {
		return true
	}
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "ORA-01466")
}

func (m *Manager) ExecuteSessionStatement(ctx context.Context, source Source, query string, info SQLStatementInfo, sessionID string, emit EmitFunc) (QuerySummary, error) {
	return m.executeSessionStatement(ctx, source, query, nil, info, sessionID, emit)
}

func (m *Manager) executeSessionStatement(ctx context.Context, source Source, query string, args []any, info SQLStatementInfo, sessionID string, emit EmitFunc) (QuerySummary, error) {
	if info.Type == "TRANSACTION" {
		summary, err := m.ControlSessionTransaction(ctx, source, sessionID, info.Action)
		if err == nil && emit != nil {
			_ = emit(StreamEvent{Type: "mutation", Summary: &summary, Message: summary.Message, StatementType: summary.StatementType})
		}
		return summary, err
	}
	if info.Type == "DDL" {
		if m.SessionTransactionPending(source, sessionID) {
			return QuerySummary{}, errors.New("当前页签有未提交 DML；请先提交或回滚，再执行会被数据库隐式提交的 DDL")
		}
		summary, err := m.executeStatementWithArgs(ctx, source, query, args, info, nil)
		if err != nil {
			return summary, err
		}
		summary.Message = "DDL 已执行；该数据库会隐式提交 DDL，无法纳入手动事务"
		summary.StatementType = "DDL_AUTOCOMMIT"
		if emit != nil {
			_ = emit(StreamEvent{Type: "mutation", Summary: &summary, Message: summary.Message, StatementType: summary.StatementType})
		}
		return summary, nil
	}
	if info.Type != "DML" {
		return QuerySummary{}, fmt.Errorf("不支持在事务会话中执行 %s", info.Action)
	}
	queryCtx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(queryCtx); err != nil {
		return QuerySummary{}, err
	}
	defer m.release()
	entry, err := m.transactionForContext(queryCtx, source, sessionID, true)
	if err != nil {
		return QuerySummary{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	started := time.Now()
	cleanQuery := strings.TrimRight(strings.TrimSpace(query), "; \t\r\n")
	result, err := entry.tx.ExecContext(queryCtx, cleanQuery, args...)
	if err != nil {
		if queryCtx.Err() != nil || isConnectionFailure(err) {
			m.rollbackEntryLocked(source.ID, sessionID, entry)
		}
		return QuerySummary{}, err
	}
	rowsAffected := int64(-1)
	if affected, affectedErr := result.RowsAffected(); affectedErr == nil {
		rowsAffected = affected
	}
	entry.updatedAt = time.Now()
	message := fmt.Sprintf("已执行 %s，等待提交", info.Action)
	if rowsAffected >= 0 {
		message = fmt.Sprintf("已执行 %s，影响 %d 行，等待提交", info.Action, rowsAffected)
	}
	summary := QuerySummary{ElapsedMS: time.Since(started).Milliseconds(), Rows: int(rowsAffected), RowsAffected: rowsAffected, StatementType: info.Type, Message: message, TransactionPending: true}
	if emit != nil {
		_ = emit(StreamEvent{Type: "mutation", Summary: &summary, Message: message, StatementType: info.Type, RowsAffected: rowsAffected})
	}
	return summary, nil
}

// ExecuteStatement executes DML (UPDATE, INSERT, DELETE, MERGE) or DDL (CREATE, ALTER, DROP, TRUNCATE) statements.
func (m *Manager) ExecuteStatement(ctx context.Context, source Source, query string, info SQLStatementInfo, emit EmitFunc) (QuerySummary, error) {
	return m.executeStatementWithArgs(ctx, source, query, nil, info, emit)
}

func (m *Manager) executeStatementWithArgs(ctx context.Context, source Source, query string, args []any, info SQLStatementInfo, emit EmitFunc) (QuerySummary, error) {
	if info.Type == "TRANSACTION" {
		return QuerySummary{}, fmt.Errorf("独立的 %s 不受支持：SQL 编辑器使用无状态请求，DML/DDL 每条自动提交；结果网格修改请使用批量提交或放弃", info.Action)
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
	cleanQuery := strings.TrimRight(strings.TrimSpace(query), "; \t\r\n")
	res, err := db.ExecContext(queryCtx, cleanQuery, args...)
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

func (m *Manager) streamQueryAttempt(ctx context.Context, source Source, query string, args []any, page QueryPage, info SQLStatementInfo, sessionID string, emit EmitFunc) (QuerySummary, error) {
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
	var queryTx *sql.Tx
	var sessionTx *transactionEntry
	if sessionID != "" {
		sessionTx, err = m.transactionFor(source, sessionID, false)
		if err != nil {
			return QuerySummary{}, err
		}
	}
	if sessionTx != nil {
		sessionTx.mu.Lock()
		defer sessionTx.mu.Unlock()
		sessionTx.updatedAt = time.Now()
		queryTx = sessionTx.tx
	} else {
		// 无待提交 DML 时使用请求级只读事务，查询结束立即释放连接。
		txOptions := &sql.TxOptions{ReadOnly: !info.HasForUpdate && source.Kind == KindMySQL}
		queryTx, err = db.BeginTx(queryCtx, txOptions)
		if err != nil {
			return QuerySummary{}, err
		}
		defer queryTx.Rollback()
		// Do not issue Oracle SET TRANSACTION READ ONLY here. Oracle can keep a
		// stale read-only snapshot immediately after this workbench executes DDL
		// and then fail the first fetch with ORA-01466. The query has already
		// passed ValidateSQL as a SELECT, remains inside this request-scoped
		// transaction, and is always rolled back below, so the extra session
		// command adds no write protection but does make metadata work unreliable.
	}
	limitedQuery, err := serverPagedQuery(source.Kind, query, page)
	if err != nil {
		return QuerySummary{}, err
	}
	rows, err := queryTx.QueryContext(queryCtx, limitedQuery, args...)
	if err != nil {
		if sessionTx != nil && (queryCtx.Err() != nil || isConnectionFailure(err)) {
			m.rollbackEntryLocked(source.ID, sessionID, sessionTx)
		}
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
		TransactionPending: sessionTx != nil,
		Offset:             page.Offset(), HasPrev: page.Page > 1, PaginationMode: "page",
		Ordered: queryHasOrderBy(query),
	}

	batch := make([][]any, 0, rowBatchSize)
	scanner := newRowScanner(columns, aliasIdx)

	for rows.Next() {
		row, rowBytes, scanErr := scanner.Scan(rows)
		if scanErr != nil {
			if sessionTx != nil && (queryCtx.Err() != nil || isConnectionFailure(scanErr)) {
				m.rollbackEntryLocked(source.ID, sessionID, sessionTx)
			}
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
		if sessionTx != nil && (queryCtx.Err() != nil || isConnectionFailure(err)) {
			m.rollbackEntryLocked(source.ID, sessionID, sessionTx)
		}
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
		if sessionTx != nil {
			summary.Message = "FOR UPDATE 行锁已保留在当前页签事务中，提交或回滚后释放"
			summary.TransactionPending = true
		} else {
			summary.Message = "FOR UPDATE 查询已执行；当前没有待提交事务，行锁随请求结束已释放"
		}
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
