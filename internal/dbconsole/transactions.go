package dbconsole

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

func transactionKey(sourceID, sessionID string) string {
	return sourceID + "\x00" + sessionID
}

func (m *Manager) transactionFor(source Source, sessionID string, create bool) (*transactionEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), source.Timeout())
	defer cancel()
	return m.transactionForContext(ctx, source, sessionID, create)
}

func (m *Manager) transactionForContext(ctx context.Context, source Source, sessionID string, create bool) (*transactionEntry, error) {
	if m.closed.Load() {
		return nil, errors.New("dbconsole manager is closed")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("事务会话 id 不能为空")
	}
	key := transactionKey(source.ID, sessionID)
	fingerprint := sourceFingerprint(source)
	m.mu.Lock()
	if m.transactions == nil {
		m.transactions = make(map[string]*transactionEntry)
	}
	_, maxTransactions := m.transactionPolicyLocked()
	maxSourceTransactions := defaultTransactionMaxSource
	if source.MaxOpenConnections == 1 {
		maxSourceTransactions = 1
	}
	if source.MaxOpenConnections > 1 && source.MaxOpenConnections-1 < maxSourceTransactions {
		// Reserve a connection for metadata and reads while tabs hold transactions.
		maxSourceTransactions = source.MaxOpenConnections - 1
	}
	entry := m.transactions[key]
	if entry != nil && entry.fingerprint == fingerprint {
		m.mu.Unlock()
		return entry, nil
	}
	if entry != nil {
		delete(m.transactions, key)
	}
	m.mu.Unlock()
	if entry != nil {
		entry.mu.Lock()
		if entry.tx != nil {
			_ = entry.tx.Rollback()
		}
		if entry.cancel != nil {
			entry.cancel()
		}
		entry.mu.Unlock()
	}
	if !create {
		return nil, nil
	}
	m.mu.Lock()
	if current := m.transactions[key]; current != nil && current.fingerprint == fingerprint {
		m.mu.Unlock()
		return current, nil
	}
	if len(m.transactions) >= maxTransactions {
		m.mu.Unlock()
		return nil, fmt.Errorf("事务会话数量已达到上限（最多 %d 个）", maxTransactions)
	}
	perSource := 0
	for _, tx := range m.transactions {
		if tx != nil && tx.sourceID == source.ID {
			perSource++
		}
	}
	if perSource >= maxSourceTransactions {
		m.mu.Unlock()
		return nil, fmt.Errorf("数据源事务会话数量已达到上限（最多 %d 个）", maxSourceTransactions)
	}
	m.mu.Unlock()
	db, err := m.sqlDB(source)
	if err != nil {
		return nil, err
	}
	txCtx, cancel := context.WithCancel(context.Background())
	// Bound pool acquisition/BeginTx by the request, then detach the successful
	// transaction so subsequent requests can commit or roll it back.
	stop := context.AfterFunc(ctx, cancel)
	tx, err := db.BeginTx(txCtx, &sql.TxOptions{})
	stop()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = tx.Rollback()
		cancel()
		return nil, err
	}
	now := time.Now()
	created := &transactionEntry{tx: tx, cancel: cancel, sourceID: source.ID, sessionID: sessionID, fingerprint: fingerprint, createdAt: now, updatedAt: now}
	m.mu.Lock()
	if m.closed.Load() {
		m.mu.Unlock()
		_ = tx.Rollback()
		cancel()
		return nil, errors.New("dbconsole manager is closed")
	}
	if m.transactions == nil {
		m.transactions = make(map[string]*transactionEntry)
	}
	if existing := m.transactions[key]; existing != nil && existing.fingerprint == fingerprint {
		m.mu.Unlock()
		_ = tx.Rollback()
		cancel()
		return existing, nil
	}
	perSource = 0
	for _, current := range m.transactions {
		if current != nil && current.sourceID == source.ID {
			perSource++
		}
	}
	if len(m.transactions) >= maxTransactions || perSource >= maxSourceTransactions {
		m.mu.Unlock()
		_ = tx.Rollback()
		cancel()
		return nil, fmt.Errorf("事务会话数量已达到上限（最多 %d 个）", maxTransactions)
	}
	m.transactions[key] = created
	m.mu.Unlock()
	return created, nil
}

func (m *Manager) removeTransaction(sourceID, sessionID string, expected *transactionEntry) {
	key := transactionKey(sourceID, sessionID)
	m.mu.Lock()
	if current := m.transactions[key]; current == expected {
		delete(m.transactions, key)
	}
	m.mu.Unlock()
}

// CommitUncertainError indicates that a commit request encountered an error
// (such as a lost connection, driver error, or network timeout) where the server
// may or may not have committed the transaction.
type CommitUncertainError struct {
	Err     error
	Message string
}

func (e *CommitUncertainError) Error() string {
	if e.Message != "" {
		if e.Err != nil {
			return e.Message + " (" + e.Err.Error() + ")"
		}
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "commit outcome unknown"
}

func (e *CommitUncertainError) Unwrap() error {
	return e.Err
}

func isCommitUncertainError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	text := strings.ToUpper(err.Error())
	for _, marker := range []string{
		"MYSQL SERVER HAS GONE AWAY", "ERROR 2006", "ERROR 2013", "BROKEN PIPE",
		"CONNECTION RESET", "CONNECTION IS CLOSED", "USE OF CLOSED NETWORK CONNECTION",
		"ORA-03113", "ORA-03114", "ORA-01012", "ORA-12537", "TTC ERROR",
		"EOF", "I/O TIMEOUT", "CLIENT.TIMEOUT", "CONNECTION LOST", "UNEXPECTED EOF",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// rollbackEntryLocked is used while the caller already owns entry.mu.  It is
// deliberately idempotent and removes the map entry immediately so a dropped
// HTTP request cannot leave a connection/lock behind until the next sweep.
func (m *Manager) rollbackEntryLocked(sourceID, sessionID string, entry *transactionEntry) {
	if entry == nil {
		return
	}
	if entry.tx != nil {
		_ = entry.tx.Rollback()
	}
	if entry.cancel != nil {
		entry.cancel()
	}
	m.removeTransaction(sourceID, sessionID, entry)
	m.recordTerminalState(sourceID, sessionID, entry.fingerprint, OutcomeRolledBack, "事务已回滚")
}

// Commit always ends the driver transaction, including when the server's
// acknowledgement is lost. Release its context and registry entry on both paths.
func (m *Manager) commitEntryLocked(sourceID, sessionID string, entry *transactionEntry) error {
	defer m.removeTransaction(sourceID, sessionID, entry)
	if entry.cancel != nil {
		defer entry.cancel()
	}
	if entry.tx == nil {
		return sql.ErrTxDone
	}
	err := entry.tx.Commit()
	if err == nil {
		m.recordTerminalState(sourceID, sessionID, entry.fingerprint, OutcomeCommitted, "事务已提交")
		return nil
	}
	if isCommitUncertainError(err) {
		msg := "提交确认丢失或连接中断，事务最终状态未知，请核对目标数据，切勿盲目重试写入"
		m.recordTerminalState(sourceID, sessionID, entry.fingerprint, OutcomeUnknown, msg)
		return &CommitUncertainError{Err: err, Message: msg}
	}
	m.recordTerminalState(sourceID, sessionID, entry.fingerprint, OutcomeRolledBack, "事务提交失败已回滚: "+err.Error())
	return err
}

func (m *Manager) SessionTransactionPending(source Source, sessionID string) bool {
	entry, _ := m.transactionFor(source, sessionID, false)
	return entry != nil
}

func (m *Manager) ControlSessionTransaction(ctx context.Context, source Source, sessionID, action string) (QuerySummary, error) {
	action = strings.ToUpper(strings.TrimSpace(action))
	if action != "COMMIT" && action != "ROLLBACK" {
		return QuerySummary{}, fmt.Errorf("不支持的事务操作 %q", action)
	}
	entry, err := m.transactionFor(source, sessionID, false)
	if err != nil {
		return QuerySummary{}, err
	}
	started := time.Now()
	if entry == nil {
		if rec, found := m.getTerminalRecord(source.ID, sessionID); found && rec.Fingerprint == sourceFingerprint(source) {
			switch rec.Outcome {
			case OutcomeUnknown:
				if action == "COMMIT" {
					return QuerySummary{}, &CommitUncertainError{
						Err:     errors.New("transaction outcome unknown"),
						Message: rec.Message,
					}
				}
				return QuerySummary{
					StatementType: "TRANSACTION",
					Message:       "事务已在服务端结束（最终状态未知），本地已解除锁定",
				}, nil
			case OutcomeCommitted:
				if action == "COMMIT" {
					return QuerySummary{StatementType: "TRANSACTION", Message: "事务已提交（重复操作已忽略）"}, nil
				}
				return QuerySummary{}, errors.New("事务已提交，无法回滚")
			case OutcomeRolledBack:
				if action == "ROLLBACK" {
					return QuerySummary{StatementType: "TRANSACTION", Message: "事务已回滚（重复操作已忽略）"}, nil
				}
				return QuerySummary{}, errors.New("事务已回滚，无法提交")
			case OutcomeExpired:
				if action == "ROLLBACK" {
					return QuerySummary{StatementType: "TRANSACTION", Message: "事务已过期回滚（重复操作已忽略）"}, nil
				}
				return QuerySummary{}, errors.New("事务已过期回滚，无法提交")
			}
		}
		message := "当前页签没有待提交事务"
		return QuerySummary{StatementType: "TRANSACTION", Message: message}, nil
	}
	queryCtx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(queryCtx); err != nil {
		return QuerySummary{}, err
	}
	defer m.release()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	var controlErr error
	if action == "COMMIT" {
		controlErr = m.commitEntryLocked(source.ID, sessionID, entry)
	} else {
		if entry.tx != nil {
			controlErr = entry.tx.Rollback()
		}
		if entry.cancel != nil {
			entry.cancel()
		}
		m.removeTransaction(source.ID, sessionID, entry)
		m.recordTerminalState(source.ID, sessionID, entry.fingerprint, OutcomeRolledBack, "事务已回滚")
	}
	if controlErr != nil && (action == "COMMIT" || !errors.Is(controlErr, sql.ErrTxDone)) {
		return QuerySummary{}, controlErr
	}
	message := "事务已提交"
	if action == "ROLLBACK" {
		message = "事务已回滚"
	}
	return QuerySummary{ElapsedMS: time.Since(started).Milliseconds(), StatementType: "TRANSACTION", Message: message}, nil
}

// ExecuteSessionBatch stages grid UPDATE statements in the same tab transaction.
// The caller must explicitly COMMIT or ROLLBACK afterwards.
func (m *Manager) ExecuteSessionBatch(ctx context.Context, source Source, sessionID string, statements []string) (QuerySummary, error) {
	if !source.MutationAllowed() {
		return QuerySummary{}, errors.New("该数据源处于只读锁定状态")
	}
	if len(statements) == 0 {
		return QuerySummary{}, errors.New("批量语句不能为空")
	}
	if len(statements) > 50 {
		return QuerySummary{}, errors.New("批量语句数量超出限制 (最多 50 条)")
	}
	for _, statement := range statements {
		info, err := ValidateSQL(source.Kind, statement)
		if err != nil {
			return QuerySummary{}, err
		}
		if info.IsQuery || info.Type != "DML" {
			return QuerySummary{}, fmt.Errorf("网格批量修改仅支持 DML: %s", info.Action)
		}
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
	var totalAffected int64
	for _, statement := range statements {
		clean := strings.TrimRight(strings.TrimSpace(statement), "; \t\r\n")
		result, execErr := entry.tx.ExecContext(queryCtx, clean)
		if execErr != nil {
			m.rollbackEntryLocked(source.ID, sessionID, entry)
			m.recordTerminalState(source.ID, sessionID, entry.fingerprint, OutcomeRolledBack, "批量修改执行失败已回滚: "+execErr.Error())
			return QuerySummary{}, execErr
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr == nil && affected >= 0 {
			totalAffected += affected
		}
	}
	entry.updatedAt = time.Now()
	message := fmt.Sprintf("已暂存 %d 条网格修改，共影响 %d 行，等待提交", len(statements), totalAffected)
	return QuerySummary{
		ElapsedMS:          time.Since(started).Milliseconds(),
		Rows:               int(totalAffected),
		RowsAffected:       totalAffected,
		StatementType:      "DML_BATCH",
		Message:            message,
		TransactionPending: true,
	}, nil
}
