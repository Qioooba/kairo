package dbconsole

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTransactionStateAndIdleCleanup(t *testing.T) {
	m := &Manager{transactions: map[string]*transactionEntry{}, transactionTTL: time.Second, transactionMax: 2}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().Add(-2 * time.Second)
	entry := &transactionEntry{sourceID: "s1", sessionID: "tab1", fingerprint: sourceFingerprint(Source{ID: "s1", Kind: KindMySQL}), cancel: cancel, createdAt: now, updatedAt: now}
	m.transactions[transactionKey("s1", "tab1")] = entry
	state := m.GetTransactionStatus(Source{ID: "s1", Kind: KindMySQL}, "tab1")
	if !state.Active || !state.Pending || state.ExpiresAt.IsZero() {
		t.Fatalf("unexpected active transaction state: %#v", state)
	}
	if got := m.cleanupExpiredTransactions(time.Now()); got != 1 {
		t.Fatalf("expected one expired transaction, got %d", got)
	}
	if len(m.transactions) != 0 {
		t.Fatal("expired transaction was not removed")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("expired transaction cancel function was not called")
	}
}

func TestTransactionLimitFailsClosed(t *testing.T) {
	m := &Manager{transactions: map[string]*transactionEntry{}, transactionTTL: time.Minute, transactionMax: 1}
	source := Source{ID: "s1", Kind: KindMySQL}
	m.transactions[transactionKey(source.ID, "existing")] = &transactionEntry{sourceID: source.ID, fingerprint: sourceFingerprint(source)}
	if _, err := m.transactionFor(source, "new", true); err == nil {
		t.Fatal("transaction limit must reject new session")
	}
}

func TestTransactionTerminalStateCommitSuccess(t *testing.T) {
	m, source, d := mutationManager(t)
	sid := "tab-commit-ok"

	// Stage a transaction
	summary, err := m.ExecuteSessionBatch(context.Background(), source, sid, []string{"UPDATE t SET v=1 WHERE id=1"})
	if err != nil || !summary.TransactionPending {
		t.Fatalf("unexpected batch result: summary=%+v err=%v", summary, err)
	}

	// Active status
	state := m.GetTransactionStatus(source, sid)
	if !state.Active || !state.Pending {
		t.Fatalf("expected active transaction before commit: %+v", state)
	}

	// Commit successfully
	sumCommit, err := m.ControlSessionTransaction(context.Background(), source, sid, "COMMIT")
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	if sumCommit.Message != "事务已提交" {
		t.Fatalf("unexpected commit message: %s", sumCommit.Message)
	}
	if d.commits != 1 {
		t.Fatalf("expected 1 driver commit, got %d", d.commits)
	}

	// Connection released, not active in map
	if len(m.transactions) != 0 {
		t.Fatalf("expected active transactions to be empty, got %d", len(m.transactions))
	}

	// Terminal state is recorded as committed
	stateAfter := m.GetTransactionStatus(source, sid)
	if stateAfter.Active || stateAfter.TerminalOutcome != OutcomeCommitted {
		t.Fatalf("expected terminal committed state, got %+v", stateAfter)
	}

	// Repeated COMMIT is idempotent
	sumRepeat, err := m.ControlSessionTransaction(context.Background(), source, sid, "COMMIT")
	if err != nil || !strings.Contains(sumRepeat.Message, "重复操作已忽略") {
		t.Fatalf("expected idempotent commit response, got %+v, err=%v", sumRepeat, err)
	}

	// Repeated ROLLBACK fails
	if _, err := m.ControlSessionTransaction(context.Background(), source, sid, "ROLLBACK"); err == nil {
		t.Fatal("expected error rolling back already committed transaction")
	}
}

func TestControlSessionTransaction_DuplicateCommitConcurrency(t *testing.T) {
	m, source, d := mutationManager(t)
	sid := "tab-concurrent-commit"

	summary, err := m.ExecuteSessionBatch(context.Background(), source, sid, []string{"UPDATE t SET v=1 WHERE id=1"})
	if err != nil || !summary.TransactionPending {
		t.Fatalf("unexpected batch result: summary=%+v err=%v", summary, err)
	}

	commitStarted := make(chan struct{})
	commitRelease := make(chan struct{})
	d.onCommit = func() {
		close(commitStarted)
		<-commitRelease
	}

	var err1 error
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		_, err1 = m.ControlSessionTransaction(context.Background(), source, sid, "COMMIT")
	}()

	<-commitStarted

	var sum2 QuerySummary
	var err2 error
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		sum2, err2 = m.ControlSessionTransaction(context.Background(), source, sid, "COMMIT")
	}()

	// Small pause to let goroutine 2 acquire the entry before goroutine 1 releases commit
	time.Sleep(20 * time.Millisecond)
	close(commitRelease)

	<-done1
	<-done2

	if err1 != nil {
		t.Fatalf("goroutine 1 commit failed: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("goroutine 2 commit should succeed idempotently or report already committed, got err: %v", err2)
	}
	if !strings.Contains(sum2.Message, "已忽略") && !strings.Contains(sum2.Message, "已提交") {
		t.Fatalf("unexpected goroutine 2 message: %s", sum2.Message)
	}

	// Terminal outcome MUST be committed, NOT rolled_back!
	status := m.GetTransactionStatus(source, sid)
	if status.TerminalOutcome != OutcomeCommitted {
		t.Fatalf("terminal outcome overwritten! want %s, got %s (message: %s)", OutcomeCommitted, status.TerminalOutcome, status.TerminalMessage)
	}
}

func TestControlSessionTransaction_ConcurrentRollbackDoesNotOverwriteCommitted(t *testing.T) {
	m, source, d := mutationManager(t)
	sid := "tab-concurrent-rollback"

	summary, err := m.ExecuteSessionBatch(context.Background(), source, sid, []string{"UPDATE t SET v=1 WHERE id=1"})
	if err != nil || !summary.TransactionPending {
		t.Fatalf("unexpected batch result: summary=%+v err=%v", summary, err)
	}

	commitStarted := make(chan struct{})
	commitRelease := make(chan struct{})
	d.onCommit = func() {
		close(commitStarted)
		<-commitRelease
	}

	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		_, _ = m.ControlSessionTransaction(context.Background(), source, sid, "COMMIT")
	}()

	<-commitStarted

	done2 := make(chan struct{})
	var err2 error
	go func() {
		defer close(done2)
		_, err2 = m.ControlSessionTransaction(context.Background(), source, sid, "ROLLBACK")
	}()

	time.Sleep(20 * time.Millisecond)
	close(commitRelease)
	<-done1
	<-done2

	if err2 == nil {
		t.Fatal("concurrent rollback on committed transaction must return error, got nil")
	}

	status := m.GetTransactionStatus(source, sid)
	if status.TerminalOutcome != OutcomeCommitted {
		t.Fatalf("terminal outcome overwritten to %s! message: %s", status.TerminalOutcome, status.TerminalMessage)
	}
}

func TestControlSessionTransaction_ConcurrentBatchDoesNotOverwriteCommitted(t *testing.T) {
	m, source, d := mutationManager(t)
	sid := "tab-concurrent-batch"

	summary, err := m.ExecuteSessionBatch(context.Background(), source, sid, []string{"UPDATE t SET v=1 WHERE id=1"})
	if err != nil || !summary.TransactionPending {
		t.Fatalf("unexpected batch result: summary=%+v err=%v", summary, err)
	}

	commitStarted := make(chan struct{})
	commitRelease := make(chan struct{})
	d.onCommit = func() {
		close(commitStarted)
		<-commitRelease
	}

	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		_, _ = m.ControlSessionTransaction(context.Background(), source, sid, "COMMIT")
	}()

	<-commitStarted

	done2 := make(chan struct{})
	var err2 error
	go func() {
		defer close(done2)
		_, err2 = m.ExecuteSessionBatch(context.Background(), source, sid, []string{"UPDATE t SET v=2 WHERE id=1"})
	}()

	time.Sleep(20 * time.Millisecond)
	close(commitRelease)
	<-done1
	<-done2

	if err2 == nil {
		t.Fatal("concurrent batch on committed transaction must return error, got nil")
	}

	status := m.GetTransactionStatus(source, sid)
	if status.TerminalOutcome != OutcomeCommitted {
		t.Fatalf("terminal outcome overwritten to %s! message: %s", status.TerminalOutcome, status.TerminalMessage)
	}
}

func TestTransactionTerminalStateCommitUnknown(t *testing.T) {
	m, source, d := mutationManager(t)
	sid := "tab-commit-unknown"

	// Inject driver error simulating network break / bad connection during commit
	d.commitErr = driver.ErrBadConn

	// Stage a transaction
	summary, err := m.ExecuteSessionBatch(context.Background(), source, sid, []string{"UPDATE t SET v=1 WHERE id=1"})
	if err != nil || !summary.TransactionPending {
		t.Fatalf("unexpected batch result: summary=%+v err=%v", summary, err)
	}

	// Commit encounters connection break
	_, commitErr := m.ControlSessionTransaction(context.Background(), source, sid, "COMMIT")
	if commitErr == nil {
		t.Fatal("expected commit error due to bad connection")
	}

	var uncertainErr *CommitUncertainError
	if !errors.As(commitErr, &uncertainErr) {
		t.Fatalf("expected CommitUncertainError, got %T: %v", commitErr, commitErr)
	}
	if !strings.Contains(uncertainErr.Message, "最终状态未知") {
		t.Fatalf("expected unknown outcome guidance message, got %s", uncertainErr.Message)
	}

	// Connection released, not active in map
	if len(m.transactions) != 0 {
		t.Fatalf("expected active transactions to be empty, got %d", len(m.transactions))
	}

	// Terminal state is recorded as outcome_unknown
	stateAfter := m.GetTransactionStatus(source, sid)
	if stateAfter.Active || stateAfter.TerminalOutcome != OutcomeUnknown {
		t.Fatalf("expected outcome_unknown terminal state, got %+v", stateAfter)
	}

	// Repeated COMMIT is blocked and returns unknown error instead of blind retry
	_, repeatErr := m.ControlSessionTransaction(context.Background(), source, sid, "COMMIT")
	if repeatErr == nil || !errors.As(repeatErr, &uncertainErr) {
		t.Fatalf("expected repeated commit to be blocked with CommitUncertainError, got %v", repeatErr)
	}

	// ROLLBACK on outcome_unknown cleans up local tab state idempotently
	sumRollback, err := m.ControlSessionTransaction(context.Background(), source, sid, "ROLLBACK")
	if err != nil || !strings.Contains(sumRollback.Message, "最终状态未知") {
		t.Fatalf("expected graceful dismissal on rollback, got %+v, err=%v", sumRollback, err)
	}
}

func TestTransactionTerminalStateRollback(t *testing.T) {
	m, source, _ := mutationManager(t)
	sid := "tab-rollback"

	summary, err := m.ExecuteSessionBatch(context.Background(), source, sid, []string{"UPDATE t SET v=2 WHERE id=1"})
	if err != nil || !summary.TransactionPending {
		t.Fatalf("unexpected batch result: summary=%+v err=%v", summary, err)
	}

	sumRb, err := m.ControlSessionTransaction(context.Background(), source, sid, "ROLLBACK")
	if err != nil || sumRb.Message != "事务已回滚" {
		t.Fatalf("rollback failed: summary=%+v err=%v", sumRb, err)
	}

	state := m.GetTransactionStatus(source, sid)
	if state.Active || state.TerminalOutcome != OutcomeRolledBack {
		t.Fatalf("expected rolled_back terminal state, got %+v", state)
	}

	// Repeated ROLLBACK is idempotent
	sumRepeat, err := m.ControlSessionTransaction(context.Background(), source, sid, "ROLLBACK")
	if err != nil || !strings.Contains(sumRepeat.Message, "重复操作已忽略") {
		t.Fatalf("expected idempotent rollback response, got %+v, err=%v", sumRepeat, err)
	}

	// Repeated COMMIT fails
	if _, err := m.ControlSessionTransaction(context.Background(), source, sid, "COMMIT"); err == nil {
		t.Fatal("expected error committing already rolled back transaction")
	}
}

func TestTransactionTerminalStateExpiryAndTTL(t *testing.T) {
	m := &Manager{
		transactions:    map[string]*transactionEntry{},
		terminalRecords: map[string]TransactionTerminalRecord{},
		transactionTTL:  time.Second,
		terminalTTL:     time.Second,
		transactionMax:  10,
		terminalMax:     20,
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := Source{ID: "s1", Kind: KindMySQL}
	now := time.Now().Add(-2 * time.Second)
	entry := &transactionEntry{
		sourceID:    "s1",
		sessionID:   "tab-expire",
		fingerprint: sourceFingerprint(src),
		cancel:      cancel,
		createdAt:   now,
		updatedAt:   now,
	}
	m.transactions[transactionKey("s1", "tab-expire")] = entry

	// Expire the transaction
	cleaned := m.cleanupExpiredTransactions(time.Now())
	if cleaned != 1 {
		t.Fatalf("expected 1 cleaned transaction, got %d", cleaned)
	}

	// Should be recorded as expired
	state := m.GetTransactionStatus(src, "tab-expire")
	if state.Active || state.TerminalOutcome != OutcomeExpired {
		t.Fatalf("expected expired terminal state, got %+v", state)
	}

	// Fast forward time past terminal TTL
	cleanedPast := m.cleanupExpiredTransactions(time.Now().Add(5 * time.Second))
	if cleanedPast != 0 {
		t.Fatalf("expected 0 cleaned transactions, got %d", cleanedPast)
	}

	// Now terminal record is gone from status
	statePast := m.GetTransactionStatus(src, "tab-expire")
	if statePast.TerminalOutcome != "" || statePast.Reason != "server_transaction_absent" {
		t.Fatalf("expected server_transaction_absent after terminal TTL expiry, got %+v", statePast)
	}
}
