package dbconsole

import (
	"context"
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
