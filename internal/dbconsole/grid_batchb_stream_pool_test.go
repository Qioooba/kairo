package dbconsole

// Batch B / DB-03（并覆盖 DB-02 的“改写前确认堆表”）回归测试。
//
// 用一个进程内的假 driver 建出真实的 database/sql 单连接池
// （MaxOpenConnections=1），冷元数据缓存，断言：
//  1. 查询首包仍然能发出（不会因为元数据链路再次申请同一个连接而自等待）；
//  2. 池 InUse 最终归零；
//  3. 未确认是普通堆表时不得改写 SQL 追加 ROWID。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- 假 driver ----------

type gridBFakeDriver struct {
	mu            sync.Mutex
	heapCount     int64
	dataQueries   []string
	allQueries    []string
	opened        int
	fieldsQueries int
	fieldsGate    chan struct{}
	fieldsStarted chan struct{}
	fieldsOnce    sync.Once
}

func (d *gridBFakeDriver) countFieldsQuery() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fieldsQueries
}

// enterFieldsQuery 让并发回归可以用屏障卡住第一次元数据查询。
func (d *gridBFakeDriver) enterFieldsQuery() {
	if d.fieldsGate == nil {
		return
	}
	if d.fieldsStarted != nil {
		d.fieldsOnce.Do(func() { close(d.fieldsStarted) })
	}
	<-d.fieldsGate
}

func (d *gridBFakeDriver) recordQuery(query string) {
	d.mu.Lock()
	d.allQueries = append(d.allQueries, gridBFirstLine(query))
	d.mu.Unlock()
}

func (d *gridBFakeDriver) queryLog() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.allQueries...)
}

func (d *gridBFakeDriver) recordDataQuery(query string) {
	d.mu.Lock()
	d.dataQueries = append(d.dataQueries, query)
	d.mu.Unlock()
}

func (d *gridBFakeDriver) dataQueryCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.dataQueries)
}

func (d *gridBFakeDriver) lastDataQuery() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.dataQueries) == 0 {
		return ""
	}
	return d.dataQueries[len(d.dataQueries)-1]
}

type gridBFakeConnector struct{ driver *gridBFakeDriver }

func (c gridBFakeConnector) Connect(context.Context) (driver.Conn, error) {
	c.driver.mu.Lock()
	c.driver.opened++
	c.driver.mu.Unlock()
	return &gridBFakeConn{driver: c.driver}, nil
}

func (c gridBFakeConnector) Driver() driver.Driver { return gridBFakeDriverObj{c.driver} }

type gridBFakeDriverObj struct{ driver *gridBFakeDriver }

func (d gridBFakeDriverObj) Open(string) (driver.Conn, error) {
	d.driver.mu.Lock()
	d.driver.opened++
	d.driver.mu.Unlock()
	return &gridBFakeConn{driver: d.driver}, nil
}

type gridBFakeConn struct{ driver *gridBFakeDriver }

func (c *gridBFakeConn) Prepare(query string) (driver.Stmt, error) {
	return &gridBFakeStmt{conn: c, query: query}, nil
}

func (c *gridBFakeConn) Close() error { return nil }

func (c *gridBFakeConn) Begin() (driver.Tx, error) { return gridBFakeTx{}, nil }

func (c *gridBFakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return gridBFakeTx{}, nil
}

func (c *gridBFakeConn) Ping(context.Context) error { return nil }

// CheckNamedValue 允许 Oracle 风格的 sql.Named 参数直接透传。
func (c *gridBFakeConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (c *gridBFakeConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return driver.RowsAffected(0), nil
}

func (c *gridBFakeConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.driver.recordQuery(query)
	upper := strings.ToUpper(query)
	switch {
	case strings.Contains(upper, "ALL_TABLES"):
		return &gridBFakeRows{cols: []string{"COUNT(*)"}, rows: [][]driver.Value{{c.driver.heapCount}}}, nil
	case strings.Contains(upper, "ALL_TAB_COLUMNS"):
		c.driver.enterFieldsQuery()
		c.driver.mu.Lock()
		c.driver.fieldsQueries++
		c.driver.mu.Unlock()
		return &gridBFakeRows{
			cols: []string{"COLUMN_NAME", "DATA_TYPE", "NULLABLE", "COLUMN_ID", "DEFINITION", "DATA_DEFAULT", "COMMENTS"},
			rows: [][]driver.Value{
				{"ID", "NUMBER", "N", int64(1), "NUMBER", nil, ""},
				{"MESSAGE", "VARCHAR2", "Y", int64(2), "VARCHAR2(200)", nil, ""},
			},
		}, nil
	case strings.Contains(upper, "ALL_INDEXES"):
		return &gridBFakeRows{cols: []string{"INDEX_NAME", "INDEX_TYPE", "UNIQUENESS", "COLUMN_NAME"}}, nil
	case strings.Contains(upper, "ALL_CONSTRAINTS"), strings.Contains(upper, "ALL_CONS_COLUMNS"):
		return &gridBFakeRows{cols: []string{"COLUMN_NAME"}}, nil
	case strings.Contains(upper, "SCOTT.LOG_TABLE"), strings.Contains(upper, "SCOTT.IOT_TABLE"):
		c.driver.recordDataQuery(query)
		if strings.Contains(upper, "__KAIRO_EDIT_RID__") {
			return &gridBFakeRows{
				cols: []string{"ID", "MESSAGE", "__KAIRO_EDIT_RID__"},
				rows: [][]driver.Value{
					{int64(1), "hello", "AAASDMAABAAAL9DAAA"},
					{int64(2), "world", "AAASDMAABAAAL9DAAB"},
				},
			}, nil
		}
		return &gridBFakeRows{
			cols: []string{"ID", "MESSAGE"},
			rows: [][]driver.Value{
				{int64(1), "hello"},
				{int64(2), "world"},
			},
		}, nil
	default:
		return nil, fmt.Errorf("gridBFakeDriver: 未预期的查询: %s", gridBFirstLine(query))
	}
}

func gridBFirstLine(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	if len(text) > 120 {
		text = text[:120]
	}
	return text
}

type gridBFakeStmt struct {
	conn  *gridBFakeConn
	query string
}

func (s *gridBFakeStmt) Close() error  { return nil }
func (s *gridBFakeStmt) NumInput() int { return -1 }

func (s *gridBFakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

func (s *gridBFakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.conn.QueryContext(context.Background(), s.query, nil)
}

type gridBFakeTx struct{}

func (gridBFakeTx) Commit() error   { return nil }
func (gridBFakeTx) Rollback() error { return nil }

type gridBFakeRows struct {
	cols []string
	rows [][]driver.Value
	idx  int
}

func (r *gridBFakeRows) Columns() []string { return r.cols }
func (r *gridBFakeRows) Close() error      { return nil }

func (r *gridBFakeRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.idx])
	r.idx++
	return nil
}

// gridBNewFakePool 建出一个真实的单连接池并注入 Manager，绕开凭据与网络。
func gridBNewFakePool(t *testing.T, id string, heapCount int64) (*Manager, Source, *gridBFakeDriver, *sql.DB) {
	t.Helper()
	return gridBNewFakePoolSized(t, id, heapCount, 1)
}

func gridBNewFakePoolSized(t *testing.T, id string, heapCount int64, poolSize int) (*Manager, Source, *gridBFakeDriver, *sql.DB) {
	t.Helper()
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	fake := &gridBFakeDriver{heapCount: heapCount}
	db := sql.OpenDB(gridBFakeConnector{driver: fake})
	db.SetMaxOpenConns(poolSize)
	db.SetMaxIdleConns(poolSize)
	source := Source{
		ID:                  id,
		Kind:                KindOracle,
		Username:            "SCOTT",
		OracleDriver:        "go-ora", // 固定后端，避免依赖本机 OCI 探测
		MaxOpenConnections:  poolSize,
		MaxIdleConnections:  poolSize,
		QueryTimeoutSeconds: 5,
		MaxRows:             50,
		MaxResultBytes:      16 << 20,
	}
	m.pools[source.ID] = &poolEntry{fingerprint: sourceFingerprint(source), sql: db}
	got, poolErr := m.sqlDB(source)
	if poolErr != nil || got != db {
		t.Fatalf("gridBNewFakePool: 注入的池未被复用 (err=%v, same=%v)", poolErr, got == db)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, source, fake, db
}

// ---------- DB-03 ----------

func TestGridBDB03SingleConnectionPoolMustNotWaitOnItself(t *testing.T) {
	m, source, fake, db := gridBNewFakePool(t, "src-db03-pool", 1)

	var events []StreamEvent
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	started := time.Now()
	summary, err := m.StreamSessionQueryPageWithParamsAndOptions(ctx, source,
		"SELECT ID, MESSAGE FROM SCOTT.LOG_TABLE", 1, 50, "tab-db03", nil,
		QueryOptions{Fast: true}, func(ev StreamEvent) error {
			events = append(events, ev)
			return nil
		})
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("DB-03: 单连接池查询必须返回首包而不是自等待超时，err = %v (elapsed=%v, events=%d)",
			err, elapsed, len(events))
	}
	if len(events) == 0 {
		t.Fatalf("DB-03: 必须至少发出一个流事件")
	}
	if events[0].Type != "meta" {
		t.Fatalf("DB-03: 首个事件必须是 meta，实际 %q", events[0].Type)
	}
	if summary.Rows == 0 {
		t.Fatalf("DB-03: 必须返回数据行，summary=%+v", summary)
	}
	if fake.dataQueryCount() == 0 {
		t.Fatalf("DB-03: 假 driver 未观察到数据查询")
	}
	if stats := db.Stats(); stats.InUse != 0 {
		t.Fatalf("DB-03: 查询结束后池 InUse 必须归零，实际 %d", stats.InUse)
	}
	if elapsed > 900*time.Millisecond {
		t.Fatalf("DB-03: 元数据规划不得拖住首包，elapsed=%v", elapsed)
	}
}

// ---------- DB-03 并发元数据刷新 ----------

// TestGridBDB03ConcurrentMetadataRefreshIsCoalesced 用屏障卡住第一次元数据查询，
// 断言同表并发查询只产生一次元数据刷新（缓存 + 并发合并），且每个查询都发出首包。
func TestGridBDB03ConcurrentMetadataRefreshIsCoalesced(t *testing.T) {
	m, source, fake, db := gridBNewFakePoolSized(t, "src-db03-coalesce", 1, 4)
	fake.fieldsGate = make(chan struct{})
	fake.fieldsStarted = make(chan struct{})

	const workers = 4
	start := make(chan struct{})
	errs := make([]error, workers)
	metaEvents := make([]int, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var metas int
			_, err := m.StreamSessionQueryPageWithParamsAndOptions(ctx, source,
				"SELECT ID, MESSAGE FROM SCOTT.LOG_TABLE", 1, 20, fmt.Sprintf("tab-coalesce-%d", idx), nil,
				QueryOptions{Fast: true}, func(ev StreamEvent) error {
					if ev.Type == "meta" {
						metas++
					}
					return nil
				})
			errs[idx] = err
			metaEvents[idx] = metas
		}(i)
	}
	close(start)
	select {
	case <-fake.fieldsStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("DB-03: 元数据查询没有开始")
	}
	// 让其余查询进入同一个刷新等待，再放行 leader。
	time.Sleep(50 * time.Millisecond)
	close(fake.fieldsGate)
	wg.Wait()

	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("DB-03: 并发查询 %d 失败: %v", i, errs[i])
		}
		if metaEvents[i] != 1 {
			t.Fatalf("DB-03: 并发查询 %d 必须发出一个 meta 事件，实际 %d", i, metaEvents[i])
		}
	}
	if got := fake.countFieldsQuery(); got != 1 {
		t.Fatalf("DB-03: 同表并发元数据刷新必须合并为一次，实际 Fields 查询 %d 次", got)
	}
	if stats := db.Stats(); stats.InUse != 0 {
		t.Fatalf("DB-03: 并发结束后池 InUse 必须归零，实际 %d", stats.InUse)
	}
}

// ---------- DB-02（改写前确认堆表） ----------
func TestGridBDB02NonHeapTableIsQueriedWithoutRowIDRewrite(t *testing.T) {
	m, source, fake, db := gridBNewFakePool(t, "src-db02-iot", 0)

	var events []StreamEvent
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := m.StreamSessionQueryPageWithParamsAndOptions(ctx, source,
		"SELECT ID, MESSAGE FROM SCOTT.IOT_TABLE", 1, 50, "tab-db02-iot", nil,
		QueryOptions{Fast: true}, func(ev StreamEvent) error {
			events = append(events, ev)
			return nil
		})
	if err != nil {
		t.Fatalf("DB-02: 非堆表必须仍可查询（只关闭编辑能力），err = %v, queries=%v", err, fake.queryLog())
	}
	last := fake.lastDataQuery()
	if last == "" {
		t.Fatalf("DB-02: 假 driver 未观察到数据查询")
	}
	if strings.Contains(strings.ToUpper(last), "ROWIDTOCHAR") {
		t.Fatalf("DB-02: 未确认普通堆表时不得追加 ROWID 定位列，实际执行: %s", last)
	}
	for _, ev := range events {
		if ev.Type != "meta" || ev.EditPlan == nil {
			continue
		}
		if ev.EditPlan.CanUpdate || ev.EditPlan.CanDelete {
			t.Fatalf("DB-02: 非堆表必须只读，实际 EditPlan=%+v", ev.EditPlan)
		}
	}
	if stats := db.Stats(); stats.InUse != 0 {
		t.Fatalf("DB-02: 查询结束后池 InUse 必须归零，实际 %d", stats.InUse)
	}
}
