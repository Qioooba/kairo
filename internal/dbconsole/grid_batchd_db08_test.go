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

// DB-08 回归：查询前的元数据规划。
//
// 这一组用例在真实 KAIRO_LAB Oracle 上暴露出来的问题是：
//  1. `Indexes` 首次执行的冷启动开销可达 ~1.9s，超过旧的单段 1500ms 预算，
//     于是"本来能成功"的读取被判成超时，网格静默降级为只读；
//  2. 堆表确认与字段/索引各自开预算、串行执行，最坏叠加；
//  3. 读取失败不写任何缓存，同一张表每次查询都重新等满一次预算。
//
// 这里用确定性假 driver 把"到底发出了哪些查询、等了多久"钉死，
// 真实库只用于功能与量级验证（见 *_integration_test.go）。

type gridD08Driver struct {
	mu sync.Mutex
	// queries 记录收到的每条查询文本，用来断言"到底发了哪几次字典查询"。
	queries []string
	// latency 注入固定延迟，用来验证预算边界。
	latency time.Duration
	// fail 让所有查询失败。
	fail bool
	// failContains 只让包含该子串的查询失败，用来构造"只有索引读不到"的场景。
	failContains string
}

type gridD08Conn struct{ d *gridD08Driver }

type gridD08Rows struct{ cols []string }

func (d *gridD08Driver) Open(string) (driver.Conn, error) { return &gridD08Conn{d}, nil }

// Connect / Driver 让 gridD08Driver 同时满足 driver.Connector，可被 sql.OpenDB 直接使用。
func (d *gridD08Driver) Connect(context.Context) (driver.Conn, error) { return &gridD08Conn{d}, nil }
func (d *gridD08Driver) Driver() driver.Driver                        { return d }

func (c *gridD08Conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("gridD08Driver: prepare unsupported")
}

func (c *gridD08Conn) Close() error { return nil }
func (c *gridD08Conn) Begin() (driver.Tx, error) {
	return nil, errors.New("gridD08Driver: tx unsupported")
}

func (c *gridD08Conn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.d.mu.Lock()
	c.d.queries = append(c.d.queries, query)
	latency, fail, failContains := c.d.latency, c.d.fail, c.d.failContains
	c.d.mu.Unlock()

	if latency > 0 {
		select {
		case <-time.After(latency):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if fail || (failContains != "" && strings.Contains(query, failContains)) {
		return nil, errors.New("synthetic metadata read failure")
	}
	cols := []string{"value"}
	if strings.Contains(strings.ToLower(query), "all_ind_columns") {
		cols = []string{"index_name", "index_type", "uniqueness", "column_name"}
	}
	return &gridD08Rows{cols: cols}, nil
}

func (c *gridD08Conn) recorded() []string {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	return append([]string(nil), c.d.queries...)
}

func (r *gridD08Rows) Columns() []string         { return r.cols }
func (r *gridD08Rows) Close() error              { return nil }
func (r *gridD08Rows) Next([]driver.Value) error { return io.EOF }

// gridD08Manager 建一个只连着假 driver 的 Manager，不读任何真实数据源配置。
func gridD08Manager(t *testing.T, d *gridD08Driver) (*Manager, Source) {
	t.Helper()
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	db := sql.OpenDB(d)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	source := Source{
		ID:                  "src-d08",
		Kind:                KindOracle,
		Username:            "SCOTT",
		QueryTimeoutSeconds: 1,
		MaxRows:             50,
		MaxResultBytes:      4 << 20,
	}
	m.pools[source.ID] = &poolEntry{fingerprint: sourceFingerprint(source), sql: db}
	t.Cleanup(func() { _ = m.Close() })
	return m, source
}

func gridD08SeedFields(t *testing.T, m *Manager, source Source, schema, table string, fields []Field) {
	t.Helper()
	metadataCacheSet(m, gridFieldsCacheKey(source.ID, sourceFingerprint(source), schema, table), fields)
}

// gridD08SeedBaseTable 预置"目标对象是真实基表"的事实，
// 让 T1 的查询计数只反映"是否还去取索引"，不被基表确认这一次廉价查询干扰。
func gridD08SeedBaseTable(t *testing.T, m *Manager, source Source, schema, table string, isBase bool) {
	t.Helper()
	m.SetCachedBaseTable(source, schema, table, isBase)
}

// TestD08PrimaryKeyProjectionSkipsIndexesAndHeapProbe 钉住"主键已完整投影时不做额外查询"。
//
// 旧实现对每个 Oracle 单表查询都会先查 all_tables 再查字段与索引；
// 现在主键已覆盖时既不需要 ROWID 定位列，也就不需要索引与堆表确认。
func TestD08PrimaryKeyProjectionSkipsIndexesAndHeapProbe(t *testing.T) {
	fields := []Field{
		{Name: "EMPNO", DataType: "NUMBER", PrimaryKey: true},
		{Name: "ENAME", DataType: "VARCHAR2(20)"},
	}

	cases := []struct {
		name        string
		sql         string
		wantQueries int
		wantRowID   bool
	}{
		{"通配符投影覆盖主键", "SELECT * FROM scott.emp", 0, false},
		{"裸主键列", "SELECT empno, ename FROM scott.emp", 0, false},
		{"带表别名的主键列", "SELECT e.empno, e.ename FROM scott.emp e", 0, false},
		{"主键被别名遮蔽仍走 ROWID", "SELECT empno AS no, ename FROM scott.emp", 1, true},
		{"未投影主键", "SELECT ename FROM scott.emp", 1, true},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			d := &gridD08Driver{}
			m, source := gridD08Manager(t, d)
			// 堆表事实预置，把"是否还会发索引查询"单独隔离出来。
			m.SetCachedHeapTable(source.ID, "SCOTT", "EMP", true)
			gridD08SeedFields(t, m, source, "SCOTT", "EMP", fields)
			gridD08SeedBaseTable(t, m, source, "SCOTT", "EMP", true)

			prep := m.prepareGridQuery(withGridConcurrencyToken(context.Background()), source, c.sql, true)
			if prep == nil {
				t.Fatal("prepareGridQuery 返回 nil")
			}
			queries := dQueries(d)
			if len(queries) != c.wantQueries {
				t.Fatalf("发出了 %d 条元数据查询，期望 %d 条: %v", len(queries), c.wantQueries, queries)
			}
			if c.wantQueries == 1 && !strings.Contains(strings.ToLower(queries[0]), "all_ind_columns") {
				t.Fatalf("唯一发出的查询应当是索引查询，实际=%q", queries[0])
			}
			if gotRowID := prep.RowIDSQL != ""; gotRowID != c.wantRowID {
				t.Fatalf("RowIDSQL 改写=%v，期望 %v（reason=%q）", gotRowID, c.wantRowID, prep.Reason)
			}
		})
	}
}

// dQueries 读取假 driver 记录到的查询文本。
func dQueries(d *gridD08Driver) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.queries...)
}

// TestD08MetadataNegativeCacheSuppressesRepeatedProbe 钉住"失败不会让每次查询都重新等满预算"。
func TestD08MetadataNegativeCacheSuppressesRepeatedProbe(t *testing.T) {
	d := &gridD08Driver{fail: true}
	m, source := gridD08Manager(t, d)
	ctx := withGridConcurrencyToken(context.Background())
	const sqlText = "SELECT ename FROM scott.emp"

	first := m.prepareGridQuery(ctx, source, sqlText, true)
	if first == nil || first.Reason == "" {
		t.Fatalf("元数据读取失败时必须降级为只读并给出原因: %+v", first)
	}
	firstCount := len(dQueries(d))
	if firstCount == 0 {
		t.Fatal("首次调用应当真的尝试读元数据")
	}

	second := m.prepareGridQuery(ctx, source, sqlText, true)
	if second == nil || second.Reason == "" {
		t.Fatalf("负缓存命中时仍必须保持只读降级: %+v", second)
	}
	if got := len(dQueries(d)); got != firstCount {
		t.Fatalf("负缓存未生效：第二次调用又发出了 %d 条查询（首次 %d 条）", got-firstCount, firstCount)
	}
	if second.RowIDSQL != "" {
		t.Fatal("确认失败时不得产出 ROWID 改写")
	}
}

// TestD08MetadataBudgetIsSingleSharedBudget 钉住"整段元数据规划共用一个总预算"。
//
// 查询超时 1s ⇒ 预算被夹到下限 800ms；每次字典查询注入 900ms 延迟。
// 字段查询会耗尽整个预算，后续阶段只能拿到已经被取消的上下文，
// 因此总等待不会翻倍。若有人重新引入"每段各开一个预算"，这里会立刻变红。
func TestD08MetadataBudgetIsSingleSharedBudget(t *testing.T) {
	d := &gridD08Driver{latency: 900 * time.Millisecond}
	m, source := gridD08Manager(t, d)
	gridD08SeedFields(t, m, source, "SCOTT", "EMP", []Field{{Name: "ENAME", DataType: "VARCHAR2(20)"}})

	started := time.Now()
	prep := m.prepareGridQuery(withGridConcurrencyToken(context.Background()), source, "SELECT ename FROM scott.emp", true)
	elapsed := time.Since(started)

	if prep == nil {
		t.Fatal("prepareGridQuery 返回 nil")
	}
	if elapsed > 1200*time.Millisecond {
		t.Fatalf("元数据等待 %v 超过单一总预算：说明预算又被拆成了多段", elapsed)
	}
	if prep.RowIDSQL != "" {
		t.Fatal("预算耗尽后不得确认 ROWID 改写")
	}
	// 字段是从缓存命中取得的，因此这里没有降级原因；后续身份判定会因为它给出只读原因。
	// 本用例要钉住的是"堆表事实没有被确认"以及"没有产出改写"。
	if prep.HeapKnown {
		t.Fatal("预算耗尽后不应声称已确认普通堆表事实")
	}
}

// TestD08JoinFormsWithoutJoinKeywordRejected 覆盖不含 JOIN 字样的连接写法（DB-08）。
//
// 旧实现的复杂查询正则只认 JOIN/UNION/GROUP BY 等字面量，
// MySQL 的 STRAIGHT_JOIN、以及落在 FROM 头部的 LEFT/ON/USING/PIVOT 等会漏过去，
// 使多表结果被当成单表目标来编辑。
func TestD08JoinFormsWithoutJoinKeywordRejected(t *testing.T) {
	rejected := []struct {
		name string
		sql  string
	}{
		{"MySQL STRAIGHT_JOIN", "SELECT * FROM t1 STRAIGHT_JOIN t2 ON t1.id = t2.id"},
		{"显式 INNER JOIN", "SELECT * FROM t1 INNER JOIN t2 ON t1.id = t2.id"},
		{"LEFT JOIN", "SELECT * FROM t1 LEFT JOIN t2 ON t1.id = t2.id"},
		{"NATURAL JOIN", "SELECT * FROM t1 NATURAL JOIN t2"},
		{"逗号隐式连接", "SELECT * FROM t1, t2 WHERE t1.id = t2.id"},
		{"11g (+) 外连接", "SELECT * FROM t1, t2 WHERE t1.id = t2.id(+)"},
		{"USING 连接", "SELECT * FROM t1 JOIN t2 USING (id)"},
		{"派生表", "SELECT * FROM (SELECT * FROM t1) x"},
		{"CTE", "WITH x AS (SELECT * FROM t1) SELECT * FROM x"},
		{"UNION ALL", "SELECT id FROM t1 UNION ALL SELECT id FROM t2"},
		{"GROUP BY 聚合", "SELECT id, COUNT(*) FROM t1 GROUP BY id"},
		{"DISTINCT", "SELECT DISTINCT id FROM t1"},
		{"Oracle PIVOT", "SELECT * FROM t1 PIVOT (COUNT(*) FOR id IN (1))"},
	}
	for _, c := range rejected {
		c := c
		t.Run("拒绝/"+c.name, func(t *testing.T) {
			parsed, err := parseGridQuerySyntax(KindMySQL, c.sql)
			if err == nil {
				t.Fatalf("应当被拒绝，却解析出了目标表 %q（alias=%q）", parsed.Table, parsed.TableAlias)
			}
		})
	}

	allowed := []struct {
		name  string
		sql   string
		table string
	}{
		{"裸表名", "SELECT id FROM emp", "emp"},
		{"带 schema", "SELECT id FROM scott.emp", "emp"},
		{"带别名", "SELECT e.id FROM emp e", "emp"},
		{"带 WHERE", "SELECT id FROM emp WHERE id > 1", "emp"},
		{"带 ORDER BY", "SELECT id FROM emp ORDER BY id", "emp"},
		{"投影含子查询", "SELECT id, (SELECT COUNT(*) FROM dept) AS n FROM emp", "emp"},
	}
	for _, c := range allowed {
		c := c
		t.Run("允许/"+c.name, func(t *testing.T) {
			parsed, err := parseGridQuerySyntax(KindMySQL, c.sql)
			if err != nil {
				t.Fatalf("单基表查询不应被拒绝: %v", err)
			}
			if !strings.EqualFold(parsed.Table, c.table) {
				t.Fatalf("解析出的目标表 = %q，期望 %q", parsed.Table, c.table)
			}
		})
	}
}

// TestD08GridProjectionCoversPrimaryKey 单测执行前的保守判断。
func TestD08GridProjectionCoversPrimaryKey(t *testing.T) {
	fields := []Field{
		{Name: "ID", PrimaryKey: true},
		{Name: "PART", PrimaryKey: true},
		{Name: "NAME"},
	}
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{"通配符", "SELECT * FROM t", true},
		{"别名通配符", "SELECT t.* FROM t", true},
		{"复合主键齐全", "SELECT id, part, name FROM t", true},
		{"复合主键带表别名", "SELECT t.id, t.part FROM t", true},
		{"缺一个主键列", "SELECT id, name FROM t", false},
		{"主键带列别名", "SELECT id AS x, part FROM t", false},
		{"表达式投影", "SELECT COUNT(*) FROM t", false},
		{"无主键列", "SELECT name FROM t", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			parsed, err := parseGridQuerySyntax(KindOracle, c.sql)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got := gridProjectionCoversPrimaryKey(KindOracle, parsed, fields); got != c.want {
				t.Fatalf("gridProjectionCoversPrimaryKey = %v，期望 %v", got, c.want)
			}
		})
	}
	if gridProjectionCoversPrimaryKey(KindOracle, nil, fields) {
		t.Fatal("parsed 为 nil 时必须返回 false")
	}
	noPK := []Field{{Name: "NAME"}}
	parsed, _ := parseGridQuerySyntax(KindOracle, "SELECT * FROM t")
	if gridProjectionCoversPrimaryKey(KindOracle, parsed, noPK) {
		t.Fatal("表没有主键时必须返回 false，才能保留 ROWID 路径")
	}
}

// TestD08InsertIsClosedForNonBaseTableObjects 钉住"视图/同义词不开放插入"。
//
// 这条在真实 Oracle 上被实测暴露：JOIN 视图 EMPLOYEE_DIRECTORY 没有主键/唯一键，
// 却因为"单表 + 可插入投影"而拿到了 can_insert，用户新增行后必然在提交时失败。
func TestD08InsertIsClosedForNonBaseTableObjects(t *testing.T) {
	source := Source{ID: "src-d08-view", Kind: KindOracle, Username: "SCOTT", QueryTimeoutSeconds: 5}
	if !source.MutationAllowed() {
		t.Fatal("测试前提：该数据源必须允许写入，否则插入分支根本不会进入")
	}

	const sqlText = "SELECT * FROM scott.v_emp"
	parsed, err := parseGridQuerySyntax(KindOracle, sqlText)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 视图没有主键/唯一键，因此更新与删除本来就关着；这里只看插入。
	fields := []Field{{Name: "ID", DataType: "NUMBER"}, {Name: "NAME", DataType: "VARCHAR2(20)"}}
	resultColumns := []Column{{Name: "ID", Database: "NUMBER"}, {Name: "NAME", Database: "VARCHAR2(20)"}}

	prepFor := func(known, isBase bool) *gridQueryPreparation {
		return &gridQueryPreparation{
			Parsed: parsed, Schema: "SCOTT", Table: "V_EMP",
			Fields: fields, BaseKnown: known, BaseTable: isBase,
		}
	}

	view := AnalyzeGridQueryWithMetadata(source, "tab", sqlText, resultColumns, prepFor(true, false), -1)
	if view.CanInsert {
		t.Fatal("目标不是真实基表时不得开放插入")
	}
	if view.Reason == "" {
		t.Fatal("关闭插入能力时必须给出原因")
	}

	unknown := AnalyzeGridQueryWithMetadata(source, "tab", sqlText, resultColumns, prepFor(false, false), -1)
	if unknown.CanInsert {
		t.Fatal("拿不到基表事实时必须 fail-closed，不得开放插入")
	}

	table := AnalyzeGridQueryWithMetadata(source, "tab", sqlText, resultColumns, prepFor(true, true), -1)
	if !table.CanInsert {
		t.Fatalf("真实基表应当开放插入: reason=%q", table.Reason)
	}
}

// TestD08IndexesFailureFallsBackToRowIDInsteadOfReadOnly 钉住"索引读不到 ≠ 不可编辑"。
//
// 这是把字段查询排到最前面的关键收益：字段（廉价）先拿到，主键缺失时才去取索引。
// 索引只是"唯一键定位"的优化，取不到时仍然可以用 ROWID 定位，
// 因此一次慢/失败的索引查询**不会**再把整个结果静默降级成只读
// （旧实现把字段和索引放进同一个预算里，超时一起丢，字段没了就整表只读）。
func TestD08IndexesFailureFallsBackToRowIDInsteadOfReadOnly(t *testing.T) {
	d := &gridD08Driver{failContains: "all_ind_columns"}
	m, source := gridD08Manager(t, d)
	m.SetCachedHeapTable(source.ID, "SCOTT", "EMP", true)
	gridD08SeedFields(t, m, source, "SCOTT", "EMP", []Field{{Name: "ENAME", DataType: "VARCHAR2(20)"}})
	gridD08SeedBaseTable(t, m, source, "SCOTT", "EMP", true)

	const sqlText = "SELECT ename FROM scott.emp"
	prep := m.prepareGridQuery(withGridConcurrencyToken(context.Background()), source, sqlText, true)
	if prep == nil {
		t.Fatal("prepareGridQuery 返回 nil")
	}
	if prep.RowIDSQL == "" {
		t.Fatal("索引不可用时必须保留 ROWID 定位改写，而不是放弃编辑能力")
	}
	if prep.Reason != "" {
		t.Fatalf("索引只是唯一键定位的优化，取不到不应当降级为只读: %q", prep.Reason)
	}
	if len(prep.Fields) == 0 {
		t.Fatal("字段元数据不应当受索引失败影响")
	}

	// 端到端：这份元数据仍然应当产出可更新的计划（ROWID 定位）。
	plan := AnalyzeGridQueryWithMetadata(source, "tab", sqlText,
		[]Column{{Name: "ENAME", Database: "VARCHAR2(20)"}}, prep, 1)
	if !plan.CanUpdate || plan.IdentityPolicy != "oracle_rowid" {
		t.Fatalf("应当回落到 ROWID 定位并保持可更新: policy=%s can_update=%v reason=%q",
			plan.IdentityPolicy, plan.CanUpdate, plan.Reason)
	}
}

// TestD08GridMutationRequiresBoundResultPlan 钉住写入路径必须绑定服务端编辑计划。
//
// 旧实现允许不带 result_id 的请求，此时 schema/table 完全来自客户端，
// 并且绕过了能力校验 —— 关联查询结果也能被"客户端自报表名"直接写库。
func TestD08GridMutationRequiresBoundResultPlan(t *testing.T) {
	m, source, _ := mutationManager(t)

	_, err := m.ApplyGridMutations(context.Background(), source, GridMutationRequest{
		Table:     "t",
		SessionID: "tab",
		Mutations: []GridMutation{{Action: "insert", Values: map[string]any{"v": 1}}},
	})
	if err == nil {
		t.Fatal("不带 result_id 的网格写入必须被拒绝")
	}
	if !strings.Contains(err.Error(), "result_id") {
		t.Fatalf("拒绝原因应当说明需要 result_id，实际=%v", err)
	}

	// 计划存在但未授予插入能力时，同样必须拒绝（能力校验不能被绕过）。
	plan := &ResultEditContext{
		ResultID:          "res-d08-readonly",
		SessionID:         "tab",
		SourceID:          source.ID,
		SourceFingerprint: sourceFingerprint(source),
		Dialect:           source.Kind,
		Table:             "t",
		IdentityPolicy:    "none",
		Reason:            "关联查询结果只读",
		Columns:           []GridColumnBinding{{Index: 0, ResultName: "v", PhysicalName: "v"}},
		CreatedAt:         time.Now(),
	}
	m.RegisterResultContext(plan)
	_, err = m.ApplyGridMutations(context.Background(), source, GridMutationRequest{
		ResultID:  plan.ResultID,
		Table:     "t",
		SessionID: "tab",
		Mutations: []GridMutation{{Action: "insert", Values: map[string]any{"v": 1}}},
	})
	if err == nil || !strings.Contains(err.Error(), "不支持插入") {
		t.Fatalf("只读计划必须拒绝插入，实际 err=%v", err)
	}
}
