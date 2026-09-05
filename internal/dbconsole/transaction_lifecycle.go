package dbconsole

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	defaultTransactionIdleTTL   = 15 * time.Minute
	defaultTransactionMax       = 64
	defaultTransactionMaxSource = 16
	transactionSweepInterval    = 30 * time.Second
)

// TransactionState is the safe, driver-independent view returned to the UI.
// It deliberately contains no *sql.Tx or connection details.
type TransactionState struct {
	SourceID        string    `json:"source_id"`
	SessionID       string    `json:"session_id"`
	Active          bool      `json:"active"`
	Pending         bool      `json:"pending"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
	IdleMS          int64     `json:"idle_ms,omitempty"`
	IdleTTLMS       int64     `json:"idle_ttl_ms,omitempty"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
	MaxTransactions int       `json:"max_transactions,omitempty"`
	Reason          string    `json:"reason,omitempty"`
}

// SetTransactionPolicy configures the in-memory transaction guard.  A zero
// value keeps the production defaults.  It is primarily useful for embedding
// applications and short-lived tests which need a small TTL.
func (m *Manager) SetTransactionPolicy(idleTTL time.Duration, maxTransactions int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idleTTL > 0 {
		m.transactionTTL = idleTTL
	}
	if maxTransactions > 0 {
		m.transactionMax = maxTransactions
	}
}

func (m *Manager) transactionPolicyLocked() (time.Duration, int) {
	ttl := m.transactionTTL
	if ttl <= 0 {
		ttl = defaultTransactionIdleTTL
		m.transactionTTL = ttl
	}
	max := m.transactionMax
	if max <= 0 {
		max = defaultTransactionMax
		m.transactionMax = max
	}
	return ttl, max
}

// transactionJanitor is intentionally a single low-frequency goroutine.  It
// never touches SQL while holding Manager.mu and rolls back stale work before
// releasing the entry, so it cannot leak a connection or row lock after a
// browser crash/network loss.
func (m *Manager) transactionJanitor() {
	ticker := time.NewTicker(transactionSweepInterval)
	defer ticker.Stop()
	defer close(m.transactionDone)
	for {
		select {
		case now := <-ticker.C:
			m.cleanupExpiredTransactions(now)
		case <-m.transactionStop:
			return
		}
	}
}

func (m *Manager) stopTransactionJanitor() {
	if m.transactionStop == nil {
		return
	}
	m.transactionStopOnce.Do(func() { close(m.transactionStop) })
	if m.transactionDone != nil {
		<-m.transactionDone
	}
}

func (m *Manager) cleanupExpiredTransactions(now time.Time) int {
	m.mu.Lock()
	ttl, _ := m.transactionPolicyLocked()
	entries := make([]struct {
		key   string
		entry *transactionEntry
	}, 0)
	for key, entry := range m.transactions {
		if entry == nil {
			delete(m.transactions, key)
			continue
		}
		entries = append(entries, struct {
			key   string
			entry *transactionEntry
		}{key: key, entry: entry})
	}
	m.mu.Unlock()

	cleaned := 0
	for _, item := range entries {
		entry := item.entry
		entry.mu.Lock()
		updated := entry.updatedAt
		if updated.IsZero() {
			updated = now
		}
		if now.Sub(updated) < ttl {
			entry.mu.Unlock()
			continue
		}
		m.mu.Lock()
		if current := m.transactions[item.key]; current != entry {
			m.mu.Unlock()
			entry.mu.Unlock()
			continue
		}
		delete(m.transactions, item.key)
		m.mu.Unlock()
		if entry.tx != nil {
			_ = entry.tx.Rollback()
		}
		if entry.cancel != nil {
			entry.cancel()
		}
		entry.mu.Unlock()
		cleaned++
	}
	return cleaned
}

// TransactionStatus returns the current server-side state for one tab.  The
// source fingerprint is checked so a tab restored against an edited source is
// never reported as active for the new connection settings.
func (m *Manager) GetTransactionStatus(source Source, sessionID string) TransactionState {
	state := TransactionState{SourceID: source.ID, SessionID: sessionID, Active: false}
	if strings.TrimSpace(sessionID) == "" {
		state.Reason = "session_id 为空"
		return state
	}
	key := transactionKey(source.ID, sessionID)
	m.mu.Lock()
	entry := m.transactions[key]
	ttl, max := m.transactionPolicyLocked()
	m.mu.Unlock()
	state.MaxTransactions = max
	if entry == nil {
		state.Reason = "server_transaction_absent"
		return state
	}
	if entry.fingerprint != sourceFingerprint(source) {
		state.Reason = "source_changed"
		return state
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	// An entry is created only after BeginTx succeeds; treating its presence as
	// active also makes status useful while a driver is returning sql.ErrTxDone.
	state.Active = true
	state.Pending = true
	state.CreatedAt = entry.createdAt
	state.UpdatedAt = entry.updatedAt
	state.IdleTTLMS = ttl.Milliseconds()
	if !state.UpdatedAt.IsZero() {
		idle := time.Since(state.UpdatedAt)
		if idle < 0 {
			idle = 0
		}
		state.IdleMS = idle.Milliseconds()
		state.ExpiresAt = state.UpdatedAt.Add(ttl)
	}
	return state
}

// TransactionStatus is kept as a concise alias for callers that prefer a
// noun-style method name.
func (m *Manager) TransactionStatus(source Source, sessionID string) TransactionState {
	return m.GetTransactionStatus(source, sessionID)
}

func (m *Manager) ListTransactionStatus(sourceID string) []TransactionState {
	m.mu.Lock()
	keys := make([]string, 0)
	for key, entry := range m.transactions {
		if entry != nil && (sourceID == "" || entry.sourceID == sourceID) {
			keys = append(keys, key)
		}
	}
	m.mu.Unlock()
	out := make([]TransactionState, 0, len(keys))
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		// Avoid requiring source credentials just to display state.  The source
		// fingerprint is checked by GetTransactionStatus when the caller has it.
		m.mu.Lock()
		entry := m.transactions[key]
		ttl, max := m.transactionPolicyLocked()
		m.mu.Unlock()
		if entry == nil {
			continue
		}
		entry.mu.Lock()
		state := TransactionState{SourceID: parts[0], SessionID: parts[1], Active: true, Pending: true, CreatedAt: entry.createdAt, UpdatedAt: entry.updatedAt, IdleTTLMS: ttl.Milliseconds(), MaxTransactions: max}
		if !entry.updatedAt.IsZero() {
			state.ExpiresAt = entry.updatedAt.Add(ttl)
			state.IdleMS = time.Since(entry.updatedAt).Milliseconds()
		}
		entry.mu.Unlock()
		out = append(out, state)
	}
	return out
}

// RollbackTransaction is an explicit disconnect/reconciliation primitive.
// It is idempotent and returns whether an active transaction was found.
func (m *Manager) RollbackTransaction(source Source, sessionID, reason string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return false, errors.New("事务会话 id 不能为空")
	}
	key := transactionKey(source.ID, sessionID)
	m.mu.Lock()
	entry := m.transactions[key]
	if entry != nil && entry.fingerprint != sourceFingerprint(source) {
		delete(m.transactions, key)
	} else if entry != nil {
		delete(m.transactions, key)
	}
	m.mu.Unlock()
	if entry == nil {
		return false, nil
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	var err error
	if entry.tx != nil {
		err = entry.tx.Rollback()
		if errors.Is(err, sql.ErrTxDone) {
			err = nil
		}
	}
	if entry.cancel != nil {
		entry.cancel()
	}
	if reason == "" {
		reason = "client_disconnect"
	}
	if err != nil {
		return true, fmt.Errorf("回滚事务失败（%s）: %w", reason, err)
	}
	return true, nil
}
