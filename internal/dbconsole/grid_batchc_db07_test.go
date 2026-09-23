package dbconsole

// Batch C / DB-07 失败优先回归：无主键表的“行特征回查”不得把相似日志当成同一行。
//
// 修复前实测：`handlers_database_lob.go` 在无 PK/UK 时直接采用 ResolveRowIDByKeys 的结果；
// 该函数只留前 6 个候选字段、强制 `ROWNUM <= 1`、用 QueryRow 取第一行，
// 完全不检查候选匹配是否唯一。两行 TAG 都是 `same` 时点击第二行会拿到第一行的内容。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// ---------- 记录 SQL/参数并可按查询文本决定返回行的假 driver ----------

type gridCLocatorDriver struct {
	mu       sync.Mutex
	queries  []string
	args     [][]driver.NamedValue
	rowsFunc func(query string, inTx bool) [][]driver.Value
}

func (d *gridCLocatorDriver) record(query string, args []driver.NamedValue) {
	d.mu.Lock()
	d.queries = append(d.queries, query)
	d.args = append(d.args, append([]driver.NamedValue(nil), args...))
	d.mu.Unlock()
}

func (d *gridCLocatorDriver) lastQuery() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.queries) == 0 {
		return ""
	}
	return d.queries[len(d.queries)-1]
}

func (d *gridCLocatorDriver) Connect(context.Context) (driver.Conn, error) {
	return &gridCLocatorConn{d: d}, nil
}
func (d *gridCLocatorDriver) Driver() driver.Driver { return d }
func (d *gridCLocatorDriver) Open(string) (driver.Conn, error) {
	return &gridCLocatorConn{d: d}, nil
}

type gridCLocatorConn struct {
	d    *gridCLocatorDriver
	inTx bool
}

func (c *gridCLocatorConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("gridCLocatorDriver: 未预期的 Prepare")
}
func (c *gridCLocatorConn) Close() error { return nil }
func (c *gridCLocatorConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *gridCLocatorConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.inTx = true
	return &gridCLocatorTx{conn: c}, nil
}
func (c *gridCLocatorConn) Ping(context.Context) error { return nil }
func (c *gridCLocatorConn) CheckNamedValue(*driver.NamedValue) error {
	return nil
}
func (c *gridCLocatorConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (c *gridCLocatorConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.d.record(query, args)
	if !strings.Contains(strings.ToUpper(query), "ROWIDTOCHAR") {
		return nil, fmt.Errorf("gridCLocatorDriver: 未预期的查询: %s", gridCFirstLine(query))
	}
	return &gridCLOBRows{cols: []string{"ROWIDTOCHAR(ROWID)"}, rows: c.d.rowsFunc(query, c.inTx)}, nil
}

type gridCLocatorTx struct{ conn *gridCLocatorConn }

func (tx *gridCLocatorTx) Commit() error   { tx.conn.inTx = false; return nil }
func (tx *gridCLocatorTx) Rollback() error { tx.conn.inTx = false; return nil }

// gridCNewLocatorPool 建出真实池并把假 driver 注入 Manager。
func gridCNewLocatorPool(t *testing.T, rowsFunc func(string, bool) [][]driver.Value) (*Manager, Source, *gridCLocatorDriver) {
	t.Helper()
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	driverImpl := &gridCLocatorDriver{rowsFunc: rowsFunc}
	db := sql.OpenDB(driverImpl)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	source := Source{
		ID:                  "src-c-loc",
		Kind:                KindOracle,
		Username:            "SCOTT",
		OracleDriver:        "go-ora",
		MaxOpenConnections:  2,
		MaxIdleConnections:  2,
		QueryTimeoutSeconds: 5,
		MaxRows:             50,
		MaxResultBytes:      16 << 20,
	}
	m.pools[source.ID] = &poolEntry{fingerprint: sourceFingerprint(source), sql: db}
	t.Cleanup(func() { _ = m.Close() })
	return m, source, driverImpl
}

func gridCDb07Fields() []Field {
	return []Field{
		{Name: "TAG", DataType: "VARCHAR2(64)", Nullable: true},
		{Name: "SEQ", DataType: "NUMBER", Nullable: true},
		{Name: "BODY", DataType: "CLOB", Nullable: true},
	}
}

// TestGridCBDb07AmbiguousFeatureMatchMustFail 匹配到多行时必须明确失败，
// 不得静默返回第一行（否则用户点击第二行会看到第一行的内容）。
func TestGridCBDb07AmbiguousFeatureMatchMustFail(t *testing.T) {
	m, source, fake := gridCNewLocatorPool(t, func(string, bool) [][]driver.Value {
		return [][]driver.Value{{"AAA-FIRST"}, {"AAA-SECOND"}}
	})
	rowID, err := m.ResolveRowIDByKeys(context.Background(), source, "SCOTT", "HTTP_REQ_LOG",
		map[string]any{"TAG": "same", "SEQ": 7}, gridCDb07Fields(), "")
	if err == nil {
		t.Fatalf("匹配到多行必须返回“无法唯一定位”，实际静默返回 rowID=%q (SQL=%s)", rowID, fake.lastQuery())
	}
	if rowID != "" {
		t.Fatalf("不可唯一定位时不得返回 ROWID，实际 %q", rowID)
	}
	if !strings.Contains(err.Error(), "唯一") {
		t.Fatalf("错误信息应说明无法唯一定位并提示重新查询，实际: %v", err)
	}
}

// TestGridCBDb07LocatorComparesEveryColumn 不得只比较“前六个非 NULL 列”。
// 这里第 7 列不同 —— 若被截断，假库会返回 2 行，从而暴露“取第一行”的缺陷。
func TestGridCBDb07LocatorComparesEveryColumn(t *testing.T) {
	fields := []Field{
		{Name: "C1", DataType: "VARCHAR2(20)"},
		{Name: "C2", DataType: "VARCHAR2(20)"},
		{Name: "C3", DataType: "VARCHAR2(20)"},
		{Name: "C4", DataType: "VARCHAR2(20)"},
		{Name: "C5", DataType: "VARCHAR2(20)"},
		{Name: "C6", DataType: "VARCHAR2(20)"},
		{Name: "C7", DataType: "VARCHAR2(20)"},
	}
	m, source, fake := gridCNewLocatorPool(t, func(query string, _ bool) [][]driver.Value {
		if !strings.Contains(query, `"C7"`) {
			// 少了第 7 列的比较, 这两行在特征上是“同一行”
			return [][]driver.Value{{"AAA-FIRST"}, {"AAA-SECOND"}}
		}
		return [][]driver.Value{{"AAA-UNIQUE"}}
	})
	keys := map[string]any{"C1": "a", "C2": "b", "C3": "c", "C4": "d", "C5": "e", "C6": "f", "C7": "g"}
	rowID, err := m.ResolveRowIDByKeys(context.Background(), source, "SCOTT", "LOG7", keys, fields, "")
	if err != nil {
		t.Fatalf("完整快照应能唯一定位: %v (SQL=%s)", err, fake.lastQuery())
	}
	if rowID != "AAA-UNIQUE" {
		t.Fatalf("第 7 列必须参与比较，实际返回 rowID=%q (SQL=%s)", rowID, fake.lastQuery())
	}
	// 同时断言生成的 SQL：必须比较全部 7 列，且最多取 2 行做唯一性判定。
	if got := strings.Count(fake.lastQuery(), " = :kairo_rid"); got != 7 {
		t.Fatalf("必须比较全部 7 列，实际绑定列 %d 个: %s", got, fake.lastQuery())
	}
	if !strings.Contains(fake.lastQuery(), "ROWNUM <= 2") {
		t.Fatalf("必须最多取 2 行以分辨 0/1/多行，实际 SQL=%s", fake.lastQuery())
	}
	if strings.Contains(fake.lastQuery(), "ROWNUM <= 1") {
		t.Fatalf("ROWNUM <= 1 会掩盖多行匹配，实际 SQL=%s", fake.lastQuery())
	}
}

// TestGridCBDb07NullParticipatesAsIsNull NULL 必须以 IS NULL 参与比较。
func TestGridCBDb07NullParticipatesAsIsNull(t *testing.T) {
	m, source, fake := gridCNewLocatorPool(t, func(query string, _ bool) [][]driver.Value {
		if !strings.Contains(strings.ToUpper(query), "IS NULL") {
			return [][]driver.Value{{"AAA-FIRST"}, {"AAA-SECOND"}}
		}
		return [][]driver.Value{{"AAA-NULLMATCH"}}
	})
	rowID, err := m.ResolveRowIDByKeys(context.Background(), source, "SCOTT", "HTTP_REQ_LOG",
		map[string]any{"TAG": nil, "SEQ": 7}, gridCDb07Fields(), "")
	if err != nil {
		t.Fatalf("含 NULL 的完整快照应能唯一定位: %v (SQL=%s)", err, fake.lastQuery())
	}
	if rowID != "AAA-NULLMATCH" {
		t.Fatalf("NULL 必须以 IS NULL 参与比较，实际 rowID=%q (SQL=%s)", rowID, fake.lastQuery())
	}
	if !strings.Contains(strings.ToUpper(fake.lastQuery()), `"TAG" IS NULL`) {
		t.Fatalf("NULL 必须以 IS NULL 参与比较，实际 SQL=%s", fake.lastQuery())
	}
}

// TestGridCBDb07IncompleteSnapshotMustRefuse 快照不完整（缺少可比较列）时必须拒绝，
// 不能因为“剩下的列凑巧唯一”就签发 token。
func TestGridCBDb07IncompleteSnapshotMustRefuse(t *testing.T) {
	m, source, _ := gridCNewLocatorPool(t, func(string, bool) [][]driver.Value {
		return [][]driver.Value{{"AAA-FIRST"}}
	})
	rowID, err := m.ResolveRowIDByKeys(context.Background(), source, "SCOTT", "HTTP_REQ_LOG",
		map[string]any{"TAG": "same"}, gridCDb07Fields(), "")
	if err == nil {
		t.Fatalf("缺少可比较列 SEQ 时必须拒绝，实际 rowID=%q", rowID)
	}
	if !strings.Contains(err.Error(), "SEQ") {
		t.Fatalf("错误信息应指出缺失的可比较列，实际: %v", err)
	}
}

// TestGridCBDb07EmptyMetadataMustRefuse 没有元数据确认时必须拒绝（不能只凭客户端键猜）。
func TestGridCBDb07EmptyMetadataMustRefuse(t *testing.T) {
	m, source, _ := gridCNewLocatorPool(t, func(string, bool) [][]driver.Value {
		return [][]driver.Value{{"AAA-FIRST"}}
	})
	rowID, err := m.ResolveRowIDByKeys(context.Background(), source, "SCOTT", "HTTP_REQ_LOG",
		map[string]any{"TAG": "same"}, nil, "")
	if err == nil {
		t.Fatalf("没有元数据确认时必须拒绝，实际 rowID=%q", rowID)
	}
}

// TestGridCBDb07NoMatchReportsNotFound 0 行必须明确报告“未找到”。
func TestGridCBDb07NoMatchReportsNotFound(t *testing.T) {
	m, source, _ := gridCNewLocatorPool(t, func(string, bool) [][]driver.Value { return nil })
	rowID, err := m.ResolveRowIDByKeys(context.Background(), source, "SCOTT", "HTTP_REQ_LOG",
		map[string]any{"TAG": "same", "SEQ": 7}, gridCDb07Fields(), "")
	if err == nil {
		t.Fatalf("0 行必须报告未找到，实际 rowID=%q", rowID)
	}
	if !strings.Contains(err.Error(), "未找到") {
		t.Fatalf("错误信息应说明未找到匹配行，实际: %v", err)
	}
}

// TestGridCBDb07LocatorUsesSessionTransactionSnapshot 未提交事务中的行必须在同一事务快照里定位，
// 否则定位走独立池连接，看不到本会话新插入/修改的日志（DB-07 第 4 条）。
// 这里让池连接看到 2 行（会判为无法唯一定位），会话事务连接只看到 1 行；
// 只有真正切进事务快照才能拿到唯一定位结果。
func TestGridCBDb07LocatorUsesSessionTransactionSnapshot(t *testing.T) {
	m, source, fake := gridCNewLocatorPool(t, func(_ string, inTx bool) [][]driver.Value {
		if inTx {
			return [][]driver.Value{{"AAA-SESSION"}}
		}
		return [][]driver.Value{{"AAA-COMMITTED-1"}, {"AAA-COMMITTED-2"}}
	})
	if _, err := m.transactionFor(source, "tab-tx", true); err != nil {
		t.Fatalf("建立会话事务失败: %v", err)
	}
	defer func() {
		if _, err := m.ControlSessionTransaction(context.Background(), source, "tab-tx", "ROLLBACK"); err != nil {
			t.Logf("回滚测试事务失败: %v", err)
		}
	}()

	rowID, err := m.ResolveRowIDByKeys(context.Background(), source, "SCOTT", "HTTP_REQ_LOG",
		map[string]any{"TAG": "same", "SEQ": 7}, gridCDb07Fields(), "tab-tx")
	if err != nil {
		t.Fatalf("同一事务快照内应能唯一定位: %v (SQL=%s)", err, fake.lastQuery())
	}
	if rowID != "AAA-SESSION" {
		t.Fatalf("定位必须使用会话事务快照，实际 rowID=%q（说明走了独立池连接）", rowID)
	}
}

// TestGridCBDb07MissingSessionTransactionMustRefuse 会话事务已结束时不得偷偷改用池连接
// （那会看到另一个快照，正是 DB-07 要修的问题）。
func TestGridCBDb07MissingSessionTransactionMustRefuse(t *testing.T) {
	m, source, _ := gridCNewLocatorPool(t, func(string, bool) [][]driver.Value {
		return [][]driver.Value{{"AAA-COMMITTED"}}
	})
	rowID, err := m.ResolveRowIDByKeys(context.Background(), source, "SCOTT", "HTTP_REQ_LOG",
		map[string]any{"TAG": "same", "SEQ": 7}, gridCDb07Fields(), "tab-missing")
	if err == nil {
		t.Fatalf("会话事务不存在时必须拒绝，实际 rowID=%q", rowID)
	}
	if !strings.Contains(err.Error(), "事务已结束") {
		t.Fatalf("错误信息应说明原事务已结束，实际: %v", err)
	}
}
