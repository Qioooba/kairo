package dbconsole

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"
)

const maxLOBStreamBytes int64 = 64 << 20

type lobQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type oracleCursorLifetime struct {
	context.Context
	deadline    time.Time
	hasDeadline bool
}

func (c oracleCursorLifetime) Deadline() (time.Time, bool) { return c.deadline, c.hasDeadline }

// OCI locators are borrowed from the cursor. Preserve the driver's deadline,
// but keep database/sql's asynchronous cancellation from destroying the cursor
// during a native read. The owner checks the original context between reads.
func oracleCursorContext(ctx context.Context) context.Context {
	d, ok := ctx.Deadline()
	return oracleCursorLifetime{Context: context.WithoutCancel(ctx), deadline: d, hasDeadline: ok}
}

// Every LOB read owns one connection for its entire lifetime. A transaction
// reference must fail when that transaction has ended, never change sessions.
func (m *Manager) withLOBQuery(ctx context.Context, source Source, ref LOBRef, fn func(context.Context, lobQueryer) error) error {
	if source.Kind != KindOracle {
		return errors.New("LOB 流仅支持 Oracle")
	}
	if err := ref.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	if ref.SessionID != "" {
		entry, err := m.transactionForContext(ctx, source, ref.SessionID, false)
		if err != nil {
			return err
		}
		if entry == nil {
			return errors.New("原查询事务已结束，请重新查询后加载 LOB")
		}
		entry.mu.Lock()
		defer entry.mu.Unlock()
		entry.updatedAt = time.Now()
		return fn(ctx, entry.tx)
	}
	db, err := m.sqlDB(source)
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Consistent snapshot for standalone read:
	// Establish a read transaction on this connection to ensure multi-chunk consistency.
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err == nil {
		defer tx.Rollback()
		return fn(ctx, tx)
	}
	tx, err = conn.BeginTx(ctx, nil)
	if err == nil {
		defer tx.Rollback()
		return fn(ctx, tx)
	}
	return fn(ctx, conn)
}

func validateLOBColumn(ctx context.Context, q lobQueryer, ref LOBRef) (string, error) {
	var typ string
	err := q.QueryRowContext(ctx, `SELECT data_type FROM all_tab_columns WHERE owner = :1 AND table_name = :2 AND column_name = :3`, ref.Owner, ref.Table, ref.Column).Scan(&typ)
	if err != nil {
		return "", fmt.Errorf("校验 LOB 列失败: %w", err)
	}
	if !isLOBType(typ) {
		return "", fmt.Errorf("列 %s 的类型 %s 不支持 LOB 读取", ref.Column, typ)
	}
	return typ, nil
}

type lobLimitWriter struct {
	ctx context.Context
	dst io.Writer
	n   int64
}

// Cancellation is checked before each native read. The query cursor itself is
// kept alive until the copy returns; sql.Rows must not close it concurrently.
type lobContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r lobContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (w *lobLimitWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > maxLOBStreamBytes-w.n {
		return 0, errors.New("LOB 超过 64 MB 下载上限")
	}
	n, err := w.dst.Write(p)
	w.n += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}
