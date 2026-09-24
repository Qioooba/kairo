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
	Type          string               `json:"type"`
	ResultID      string               `json:"result_id,omitempty"`
	EditPlan      *GridEditPlanSummary `json:"edit_plan,omitempty"`
	Columns       []Column             `json:"columns,omitempty"`
	Rows          [][]any              `json:"rows,omitempty"`
	Summary       *QuerySummary        `json:"summary,omitempty"`
	Error         string               `json:"error,omitempty"`
	Message       string               `json:"message,omitempty"`
	StatementType string               `json:"statement_type,omitempty"`
	RowsAffected  int64                `json:"rows_affected,omitempty"`
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

type QueryOptions struct {
	Fast bool
}

// StreamSessionQueryPageWithParams binds DML and transaction control to one browser tab.
func (m *Manager) StreamSessionQueryPageWithParams(ctx context.Context, source Source, query string, page, pageSize int, sessionID string, params []BindParameter, emit EmitFunc) (QuerySummary, error) {
	return m.StreamSessionQueryPageWithParamsAndOptions(ctx, source, query, page, pageSize, sessionID, params, QueryOptions{Fast: page <= 1}, emit)
}

// StreamSessionQueryPageWithParamsAndOptions is the parameterized counterpart with explicit query options.
func (m *Manager) StreamSessionQueryPageWithParamsAndOptions(ctx context.Context, source Source, query string, page, pageSize int, sessionID string, params []BindParameter, opts QueryOptions, emit EmitFunc) (qs QuerySummary, qerr error) {
	defer func() {
		if r := recover(); r != nil {
			qerr = fmt.Errorf("%w: 查询 panic 已捕获（可能为 TTC 解析错位，连接已销毁）: %v", driver.ErrBadConn, r)
			if source.ID != "" {
				m.invalidatePool(source.ID)
			}
		} else if qerr != nil && isTTCError(qerr) {
			qerr = mapTTCError(qerr)
			m.invalidatePool(source.ID)
		} else if qerr != nil && isConnectionFailure(qerr) {
			m.invalidatePool(source.ID)
		}
	}()
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
	if !info.IsQuery || info.HasForUpdate || info.RequiresMutation {
		if info.Type == "DDL" {
			if !source.DDLAllowed() {
				return QuerySummary{}, errors.New("该数据源未开启 DDL 能力（请在数据源配置中开启“支持 DDL”）")
			}
		} else {
			if !source.MutationAllowed() {
				return QuerySummary{}, errors.New("该数据源处于只读锁定状态")
			}
		}
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
		summary, err := m.streamQueryAttempt(ctx, source, boundQuery, boundArgs, p, info, sessionID, opts, safeEmit)
		summary.RetryCount = attempt
		last = summary
		if err == nil {
			return summary, nil
		}
		// 重试仅在尚未向客户端输出任何数据行且属于可恢复故障时允许。
		if attempt == 0 && !hadSessionTransaction && !emittedRows && isRetryableQueryFailure(err) {
			if source.Kind == KindOracle && IsOracleDPIError(err) && strings.ToLower(strings.TrimSpace(source.OracleDriver)) != "godror" && source.ID != "" {
				m.dpiFailedSources.Store(source.ID, true)
			}
			m.invalidatePool(source.ID)
			continue
		}
		if isConnectionFailure(err) {
			m.invalidatePool(source.ID)
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
	if IsOracleDPIError(err) {
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
	if !source.MutationAllowed() {
		return QuerySummary{}, errors.New("该数据源处于只读锁定状态")
	}
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
		upper := strings.ToUpper(strings.TrimRight(trimmed, "; \t\r\n"))
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

func (m *Manager) streamQueryAttempt(ctx context.Context, source Source, query string, args []any, page QueryPage, info SQLStatementInfo, sessionID string, opts QueryOptions, emit EmitFunc) (QuerySummary, error) {
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

	// DB-03: 必须在占用流式查询连接（会话事务或请求级事务）之前完成受限元数据规划，
	// 否则元数据链路会等待同一个连接直到超时。元数据链路共享本请求已持有的全局并发令牌，
	// 并使用独立短预算；拿不到元数据只降级为只读，不阻塞查询首包。
	planCtx := withGridConcurrencyToken(queryCtx)
	needEditable := sessionID != "" && info.IsSelect
	var prep *gridQueryPreparation
	var prepMS int64
	if needEditable || (source.Kind == KindOracle && !info.HasForUpdate && info.IsSelect) {
		// DB-08: 元数据准备耗时单独计量，便于把"查询慢"拆成元数据准备与 SQL 执行。
		prepStarted := time.Now()
		prep = m.prepareGridQuery(planCtx, source, query, needEditable)
		prepMS = time.Since(prepStarted).Milliseconds()
	}

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
	actualQuery := query
	var lobRewrite *LOBRewrittenQuery
	var lobSingleInfo *SingleTableQueryInfo

	isFast := false
	// Fast 极速模式仅对安全单表浏览生效；任意复杂查询（JOIN、聚合、子查询等）走正常路径
	if source.Kind == KindOracle && !info.HasForUpdate {
		if sInfo, ok := parseSafeSingleTableQuery(query); ok {
			sInfo.Schema = ResolveSchema(source, sInfo.Schema)
			lobSingleInfo = sInfo
			if opts.Fast {
				isFast = true
			} else {
				lobCtx, cancelLob := context.WithTimeout(queryCtx, 2*time.Second)
				rw, rerr := m.buildLOBProjectionOn(lobCtx, queryTx, source.ID, sInfo)
				cancelLob()
				if rerr == nil && rw != nil {
					lobRewrite = rw
					actualQuery = rw.SQL
				}
			}
		}
	}
	// DB-03: 在占用流式查询连接之前完成受限元数据规划与 ROWID 改写决策。
	// DB-01/DB-02: 只有语法允许且已确认是普通堆表时才追加 ROWID 定位列。
	if lobRewrite == nil && prep != nil && prep.RowIDSQL != "" {
		actualQuery = prep.RowIDSQL
	}

	pagedPlan, err := serverPagedPlan(source.Kind, actualQuery, page)
	if err != nil {
		return QuerySummary{}, err
	}
	limitedQuery := pagedPlan.SQL
	queryArgs := args
	cursorCtx := queryCtx
	if source.Kind == KindOracle && m.ResolveOracleBackend(source).Name() == "godror" {
		queryArgs = oracleGridArgs(args)
		cursorCtx = oracleCursorContext(queryCtx)
	}
	rows, err := queryTx.QueryContext(cursorCtx, limitedQuery, queryArgs...)
	if err != nil {
		if sessionTx != nil && (queryCtx.Err() != nil || isConnectionFailure(err)) {
			m.rollbackEntryLocked(source.ID, sessionID, sessionTx)
		}
		return QuerySummary{}, err
	}
	defer rows.Close()

	var columns []Column
	aliasIdx := -1
	hiddenRowIDIdx := -1

	if lobRewrite != nil {
		// 对外暴露的元数据列是原表的真实业务列（名称与数据类型正确），
		// 不暴露 __LP_* / __LL_* / __KAIRO_ROWID__ 辅助列
		columns = lobRewrite.Columns

		if pagedPlan.HasHelperColumn {
			rawCols, _ := rows.Columns()
			for i, c := range rawCols {
				colName := strings.Trim(strings.ToUpper(c), "\"`[] \t")
				if strings.EqualFold(colName, pagedPlan.HelperColumnName) {
					aliasIdx = i
					break
				}
			}
		}
		hiddenRowIDIdx = len(columns)
	} else {
		columns, err = resultColumns(rows)
		if err != nil {
			return QuerySummary{}, err
		}
		rawCols, _ := rows.Columns()
		for i, c := range rawCols {
			colName := strings.Trim(strings.ToUpper(c), "\"`[] \t")
			if pagedPlan.HasHelperColumn && strings.EqualFold(colName, pagedPlan.HelperColumnName) {
				aliasIdx = i
			}
			if strings.EqualFold(colName, "__KAIRO_EDIT_RID__") {
				hiddenRowIDIdx = i
			}
		}
		if aliasIdx >= 0 || hiddenRowIDIdx >= 0 {
			filtered := make([]Column, 0, len(columns))
			for i, c := range columns {
				if i == aliasIdx || i == hiddenRowIDIdx {
					continue
				}
				filtered = append(filtered, c)
			}
			columns = filtered
		}
	}

	// 此时连接与首包已就绪；DB-03: 编辑能力分析是纯函数，不再通过池请求元数据。
	var editPlan *ResultEditContext
	if sessionID != "" && info.IsSelect {
		plan := AnalyzeGridQueryWithMetadata(source, sessionID, query, columns, prep, hiddenRowIDIdx)
		if plan != nil {
			editPlan = plan
			m.RegisterResultContext(plan)
		}
	}

	var planSummary *GridEditPlanSummary
	resultID := ""
	if editPlan != nil {
		planSummary = editPlan.ToSummary()
		resultID = editPlan.ResultID
	}

	if emit != nil {
		if err := emit(StreamEvent{Type: "meta", Columns: columns, ResultID: resultID, EditPlan: planSummary}); err != nil {
			return QuerySummary{}, err
		}
	}

	summary := QuerySummary{
		QueryLimit: page.PageSize, Page: page.Page, PageSize: page.PageSize,
		TransactionPending: sessionTx != nil,
		Offset:             page.Offset(), HasPrev: page.Page > 1, PaginationMode: "page",
		Ordered:  QueryHasTopLevelOrderBy(source.Kind, actualQuery),
		ResultID: resultID,
		EditPlan: planSummary,
	}

	firstBatchCutoff := 5
	if page.PageSize > 0 && page.PageSize < firstBatchCutoff {
		firstBatchCutoff = page.PageSize
	}
	currentBatchTarget := firstBatchCutoff
	batch := make([][]any, 0, currentBatchTarget)

	type rowScannerInterface interface {
		Scan(rows *sql.Rows) ([]any, int64, error)
	}

	var scanner rowScannerInterface
	if lobRewrite != nil {
		schema := ""
		table := ""
		if lobSingleInfo != nil {
			schema = ResolveSchema(source, lobSingleInfo.Schema)
			table = lobSingleInfo.Table
		}
		lobSessionID := ""
		if sessionTx != nil {
			lobSessionID = sessionID
		}
		scanner = newLOBRowScanner(lobRewrite, source, schema, table, lobSessionID, aliasIdx)
	} else {
		schema := ""
		table := ""
		if lobSingleInfo != nil {
			schema = ResolveSchema(source, lobSingleInfo.Schema)
			table = lobSingleInfo.Table
		}
		scanner = newRowScanner(columns, aliasIdx, hiddenRowIDIdx, isFast, schema, table)
	}

	// Commands and locking queries cannot use the derived-table wrapper.
	// Apply their page offset while consuming the original cursor instead.
	var skipRows int64
	if info.HasForUpdate || (info.Action != "SELECT" && info.Action != "WITH") {
		skipRows = page.Offset()
	}
	for rows.Next() {
		if err := queryCtx.Err(); err != nil {
			return summary, err
		}
		if skipRows > 0 {
			skipRows--
			continue
		}
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
		// 渐进式流：首包 5 行（视口首眼区域），后续 min(100, pageSize) 批量发射
		if len(batch) >= currentBatchTarget {
			if emit != nil {
				if err := emit(StreamEvent{Type: "rows", Rows: batch}); err != nil {
					return summary, err
				}
			}
			currentBatchTarget = rowBatchSize
			if page.PageSize > 0 && page.PageSize < currentBatchTarget {
				currentBatchTarget = page.PageSize
			}
			// 立即重新分配批次，断开对旧批次行的强引用，让 Go GC 可以在查询持续进行期间平滑回收内存
			batch = make([][]any, 0, currentBatchTarget)
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
	summary.PrepMS = prepMS
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
		"ORA-03113", "ORA-03114", "ORA-01012", "ORA-12537", "TTC ERROR",
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
	fast   bool
	schema string
	table  string
	value  any
}

func (s *boundedCellScanner) Scan(src any) error {
	if s.fast && isLOBType(s.dbType) {
		if src == nil {
			s.value = nil
			return nil
		}
		lobKind := "clob"
		if isBlobType(s.dbType) {
			lobKind = "blob"
		}
		m := map[string]any{
			"kind":          lobKind,
			"display":       fmt.Sprintf("(%s)", strings.ToUpper(lobKind)),
			"database_type": s.dbType,
			"lazy":          true,
		}
		if s.table != "" {
			m["table"] = strings.ToUpper(s.table)
		}
		if s.schema != "" {
			m["owner"] = strings.ToUpper(s.schema)
		}
		s.value = m
		return nil
	}
	if value, handled, err := oracleLOBPreview(src, s.dbType); handled {
		s.value = value
		return err
	}
	s.value = normalizeColumnValue(src, s.dbType)
	return nil
}

type rowScanner struct {
	scanners       []boundedCellScanner
	dest           []any
	columns        []Column
	aliasIdx       int
	hiddenRowIDIdx int
}

func newRowScanner(columns []Column, aliasIdx, hiddenRowIDIdx int, fast bool, schema, table string) *rowScanner {
	count := len(columns)
	if aliasIdx >= 0 {
		count++
	}
	if hiddenRowIDIdx >= 0 {
		count++
	}
	scanners := make([]boundedCellScanner, count)
	dest := make([]any, count)
	colIdx := 0
	for i := range scanners {
		dbType := ""
		if i == aliasIdx {
			dbType = "NUMBER"
		} else if i == hiddenRowIDIdx {
			dbType = "VARCHAR2"
		} else if colIdx < len(columns) {
			dbType = strings.ToUpper(strings.TrimSpace(columns[colIdx].Database))
			colIdx++
		}
		scanners[i].dbType = dbType
		scanners[i].fast = fast
		scanners[i].schema = schema
		scanners[i].table = table
		dest[i] = &scanners[i]
	}
	return &rowScanner{
		scanners:       scanners,
		dest:           dest,
		columns:        columns,
		aliasIdx:       aliasIdx,
		hiddenRowIDIdx: hiddenRowIDIdx,
	}
}

func (rs *rowScanner) Scan(rows *sql.Rows) ([]any, int64, error) {
	if err := rows.Scan(rs.dest...); err != nil {
		return nil, 0, err
	}
	targetLen := len(rs.columns)
	if rs.hiddenRowIDIdx >= 0 {
		targetLen++
	}
	values := make([]any, targetLen)
	valIdx := 0
	for i := range rs.scanners {
		if i == rs.aliasIdx || i == rs.hiddenRowIDIdx {
			continue
		}
		values[valIdx] = rs.scanners[i].value
		rs.scanners[i].value = nil // 立即解除对单格对象的引用，辅助 GC 回收
		valIdx++
	}
	if rs.hiddenRowIDIdx >= 0 {
		values[valIdx] = rs.scanners[rs.hiddenRowIDIdx].value
		rs.scanners[rs.hiddenRowIDIdx].value = nil
		// DB-07: 懒加载 LOB 单元格必须携带本次查询的真实行身份，
		// 否则按需 LOB 只能退回会“猜行”的特征回查。
		if rowID := gridCellRowIDText(values[valIdx]); rowID != "" {
			for _, cell := range values[:valIdx] {
				lob, ok := cell.(map[string]any)
				if !ok {
					continue
				}
				lazy, _ := lob["lazy"].(bool)
				if lazy && lob["rowid"] == nil {
					lob["rowid"] = rowID
				}
			}
		}
	}
	return values, fastRowBytes(values), nil
}

// gridCellRowIDText 把隐藏 ROWID 单元格转成文本（不同驱动可能给 string 或 []byte）。
func gridCellRowIDText(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case []byte:
		return strings.TrimSpace(string(v))
	default:
		return ""
	}
}

func scanRow(rows *sql.Rows, count int, columns []Column, aliasIdx int) ([]any, int64, error) {
	scanner := newRowScanner(columns, aliasIdx, -1, false, "", "")
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
		var totalBytes int
		truncated := false
		switch v := value.(type) {
		case string:
			totalBytes = len(v)
			if totalBytes > maxLOBPreviewBytes {
				cut := maxLOBPreviewBytes
				for cut > 0 && !utf8.RuneStart(v[cut]) {
					cut--
				}
				text = strings.Clone(v[:cut])
				truncated = true
			} else {
				text = v
			}
		case []byte:
			totalBytes = len(v)
			if totalBytes > maxLOBPreviewBytes {
				cut := maxLOBPreviewBytes
				for cut > 0 && !utf8.RuneStart(v[cut]) {
					cut--
				}
				text = string(v[:cut])
				truncated = true
			} else {
				text = string(v)
			}
		default:
			s := fmt.Sprint(v)
			totalBytes = len(s)
			if totalBytes > maxLOBPreviewBytes {
				cut := maxLOBPreviewBytes
				for cut > 0 && !utf8.RuneStart(s[cut]) {
					cut--
				}
				text = strings.Clone(s[:cut])
				truncated = true
			} else {
				text = s
			}
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
		var totalBytes int
		truncated := false
		var preview []byte
		switch v := value.(type) {
		case []byte:
			totalBytes = len(v)
			if totalBytes > maxLOBPreviewBytes {
				preview = bytes.Clone(v[:maxLOBPreviewBytes])
				truncated = true
			} else {
				preview = bytes.Clone(v)
			}
		case string:
			totalBytes = len(v)
			if totalBytes > maxLOBPreviewBytes {
				preview = []byte(v[:maxLOBPreviewBytes])
				truncated = true
			} else {
				preview = []byte(v)
			}
		default:
			s := fmt.Sprint(v)
			totalBytes = len(s)
			if totalBytes > maxLOBPreviewBytes {
				preview = []byte(s[:maxLOBPreviewBytes])
				truncated = true
			} else {
				preview = []byte(s)
			}
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
	case nil, bool, float64:
		return v
	case int64:
		return EncodeLosslessCell(v)
	case uint64:
		return EncodeLosslessCell(v)
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
	if len(value) > maxCellBytes {
		cut := maxCellBytes
		for cut > 0 && !utf8.RuneStart(value[cut]) {
			cut--
		}
		if utf8.Valid(value[:cut]) {
			return map[string]any{
				"kind":      "text",
				"bytes":     len(value),
				"preview":   string(value[:cut]),
				"truncated": true,
			}
		}
		preview := bytes.Clone(value[:4096])
		return map[string]any{
			"kind":           "binary",
			"bytes":          len(value),
			"preview_base64": base64.StdEncoding.EncodeToString(preview),
			"truncated":      true,
		}
	}
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
