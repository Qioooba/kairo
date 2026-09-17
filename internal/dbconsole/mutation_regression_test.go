package dbconsole

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// A deterministic driver exercises database/sql transaction ownership without
// contacting a user's configured database.
type mutationDriver struct {
	mu                 sync.Mutex
	queries            []string
	args               [][]driver.NamedValue
	commitErr          error
	prepares           int
	queryRows          [][]driver.Value
	failAt             int
	commits, rollbacks int
	onCommit           func()
}
type mutationConn struct{ d *mutationDriver }
type mutationTx struct{ d *mutationDriver }
type mutationStmt struct {
	conn  *mutationConn
	query string
}

func (d *mutationDriver) Connect(context.Context) (driver.Conn, error) { return &mutationConn{d}, nil }
func (d *mutationDriver) Driver() driver.Driver                        { return d }
func (d *mutationDriver) Open(string) (driver.Conn, error)             { return &mutationConn{d}, nil }
func (c *mutationConn) Prepare(query string) (driver.Stmt, error) {
	c.d.mu.Lock()
	c.d.prepares++
	c.d.mu.Unlock()
	return &mutationStmt{conn: c, query: query}, nil
}

type mutationRows struct {
	rows  [][]driver.Value
	index int
}

func (r *mutationRows) Columns() []string { return []string{"ID"} }
func (r *mutationRows) Close() error      { return nil }
func (r *mutationRows) Next(dest []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.index])
	r.index++
	return nil
}
func (c *mutationConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &mutationRows{rows: c.d.queryRows}, nil
}
func (s *mutationStmt) Close() error  { return nil }
func (s *mutationStmt) NumInput() int { return -1 }
func (s *mutationStmt) Exec(values []driver.Value) (driver.Result, error) {
	args := make([]driver.NamedValue, len(values))
	for i, value := range values {
		args[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return s.conn.ExecContext(context.Background(), s.query, args)
}
func (s *mutationStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, errors.New("unexpected query")
}
func (s *mutationStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.conn.ExecContext(ctx, s.query, args)
}
func (c *mutationConn) Close() error              { return nil }
func (c *mutationConn) Begin() (driver.Tx, error) { return &mutationTx{c.d}, nil }
func (c *mutationConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *mutationConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	c.d.queries = append(c.d.queries, query)
	c.d.args = append(c.d.args, append([]driver.NamedValue(nil), args...))
	if c.d.failAt == len(c.d.queries) {
		return nil, errors.New("injected execution failure")
	}
	return driver.RowsAffected(1), nil
}
func (tx *mutationTx) Commit() error {
	tx.d.mu.Lock()
	fn := tx.d.onCommit
	tx.d.commits++
	err := tx.d.commitErr
	tx.d.mu.Unlock()
	if fn != nil {
		fn()
	}
	return err
}
func (tx *mutationTx) Rollback() error {
	tx.d.mu.Lock()
	defer tx.d.mu.Unlock()
	tx.d.rollbacks++
	return nil
}

func mutationManager(t *testing.T) (*Manager, Source, *mutationDriver) {
	t.Helper()
	d := &mutationDriver{}
	db := sql.OpenDB(d)
	db.SetMaxOpenConns(2)
	source := Source{ID: "test", Kind: KindMySQL, QueryTimeoutSeconds: 1}
	m := &Manager{pools: map[string]*poolEntry{source.ID: {sql: db, fingerprint: sourceFingerprint(source)}}, transactions: map[string]*transactionEntry{}, global: make(chan struct{}, 8)}
	t.Cleanup(func() { _ = m.Close() })
	return m, source, d
}

func TestScriptBindsParametersAcrossStatements(t *testing.T) {
	for _, script := range []string{"INSERT INTO t(v) VALUES (:a); INSERT INTO t(v) VALUES (:b);", "INSERT INTO t(v) VALUES (?); INSERT INTO t(v) VALUES (?);"} {
		t.Run(script, func(t *testing.T) {
			m, source, d := mutationManager(t)
			names := []string{"a", "b"}
			if strings.Contains(script, "?") {
				names = []string{"1", "2"}
			}
			result, err := m.ExecuteScript(context.Background(), source, script, "", []BindParameter{{Name: names[0], Type: "int", Value: 1}, {Name: names[1], Type: "int", Value: 2}}, ScriptOptions{})
			if err != nil || !result.Committed || len(d.args) != 2 || d.args[0][0].Value != int64(1) || d.args[1][0].Value != int64(2) {
				t.Fatalf("result=%+v args=%+v err=%v", result, d.args, err)
			}
		})
	}
}

func TestScriptRejectsTransactionControlBeforeExecution(t *testing.T) {
	m, source, d := mutationManager(t)
	_, err := m.ExecuteScript(context.Background(), source, "INSERT INTO t(v) VALUES (1); COMMIT; INSERT INTO t(v) VALUES (2)", "tab", nil, ScriptOptions{})
	if err == nil || len(d.queries) != 0 || len(m.transactions) != 0 {
		t.Fatalf("control SQL escaped transaction owner: %v", err)
	}
}

func TestScriptRejectsAnonymousPLSQLBeforeExecution(t *testing.T) {
	m, source, d := mutationManager(t)
	source.Kind = KindOracle
	_, err := m.ExecuteScript(context.Background(), source, "BEGIN DBMS_OUTPUT.PUT_LINE('x'); UPDATE accounts SET balance = 0; END;", "", nil, ScriptOptions{})
	if err == nil || !strings.Contains(err.Error(), "匿名 PL/SQL") || len(d.queries) != 0 {
		t.Fatalf("anonymous block escaped script gate: err=%v queries=%v", err, d.queries)
	}
}

func TestScriptStopKeepsPendingTransaction(t *testing.T) {
	m, source, d := mutationManager(t)
	d.failAt = 2
	result, err := m.ExecuteScript(context.Background(), source, "INSERT INTO t(v) VALUES (1); INSERT INTO t(v) VALUES (2)", "tab", nil, ScriptOptions{FailurePolicy: "stop"})
	if err == nil || !result.TransactionPending || result.RolledBack || !m.SessionTransactionPending(source, "tab") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestMutationFailureReleasesContextAndRegistry(t *testing.T) {
	for _, operation := range []string{"grid", "import"} {
		for _, commitFailure := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: "-exec", true: "-commit"}[commitFailure], func(t *testing.T) {
				m, source, d := mutationManager(t)
				if commitFailure {
					d.commitErr = errors.New("lost commit acknowledgement")
				} else {
					d.failAt = 1
				}
				entry, err := m.transactionFor(source, "tab", true)
				if err != nil {
					t.Fatal(err)
				}
				cancelled := false
				cancel := entry.cancel
				entry.cancel = func() { cancelled = true; cancel() }
				if operation == "grid" {
					result, applyErr := m.ApplyGridMutations(context.Background(), source, GridMutationRequest{Table: "t", SessionID: "tab", Commit: commitFailure, Mutations: []GridMutation{{Action: "insert", Values: map[string]any{"v": 1}}}})
					err = applyErr
					if result.TransactionPending {
						t.Fatal("completed transaction reported pending")
					}
				} else {
					result, applyErr := m.ApplyImport(context.Background(), source, ImportApplyRequest{Table: "t", SessionID: "tab", Commit: commitFailure, Mappings: []ImportMapping{{Target: "v", SourceIndex: 0}}, Rows: [][]string{{"one"}}})
					err = applyErr
					if result.TransactionPending {
						t.Fatal("completed transaction reported pending")
					}
				}
				if err == nil || !cancelled || len(m.transactions) != 0 {
					t.Fatalf("err=%v cancelled=%v transactions=%d", err, cancelled, len(m.transactions))
				}
			})
		}
	}
}

func TestTransactionAcquisitionHonorsRequestAndDetachesOnSuccess(t *testing.T) {
	m, source, _ := mutationManager(t)
	db := m.pools[source.ID].sql
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithCancel(context.Background())
	entry, err := m.transactionForContext(ctx, source, "first", true)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = entry.tx.ExecContext(context.Background(), "UPDATE t SET v=1"); err != nil {
		t.Fatalf("request ended tab transaction: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = m.transactionForContext(ctx, source, "second", true)
	if err == nil {
		t.Fatal("exhausted pool must respect acquisition deadline")
	}
	if len(m.transactions) != 1 {
		t.Fatal("failed acquisition registered a transaction")
	}
	m.invalidatePool(source.ID)
	if !m.SessionTransactionPending(source, "first") || m.pools[source.ID] == nil {
		t.Fatal("query retry destroyed another tab")
	}
}
