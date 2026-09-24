package dbconsole

// Batch C / DB-06 失败优先回归：LOB 改写后的 scanner 必须和普通 scanner 返回
// 完全相同的行数组协议 —— 在行尾追加真实 ROWID，使摘要广告的 hidden_rowid_index
// 等于最终序列化后的下标。
//
// 修复前实测（评审文档记录）：business_columns=2; advertised_hidden_index=2;
// actual_row_length=2。后端宣称 oracle_rowid 可编辑，但行数组里没有定位值。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- 只回答数据查询的假 driver（元数据全部来自缓存） ----------

// gridCLOBCase 描述一次假查询的结果形状。
type gridCLOBCase struct {
	cols    []string
	dbTypes []string
	rows    [][]driver.Value
}

type gridCLOBDriver struct {
	mu      sync.Mutex
	queries []string
	// rowsFunc 按查询文本决定返回的结果形状, 便于分别覆盖
	// LOB 改写分支（normal）与 Fast 懒加载分支。
	rowsFunc func(query string) gridCLOBCase
}

func (d *gridCLOBDriver) record(query string) {
	d.mu.Lock()
	d.queries = append(d.queries, gridCFirstLine(query))
	d.mu.Unlock()
}

func (d *gridCLOBDriver) Connect(context.Context) (driver.Conn, error) {
	return &gridCLOBConn{d: d}, nil
}
func (d *gridCLOBDriver) Driver() driver.Driver { return d }
func (d *gridCLOBDriver) Open(string) (driver.Conn, error) {
	return &gridCLOBConn{d: d}, nil
}

type gridCLOBConn struct{ d *gridCLOBDriver }

func (c *gridCLOBConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("gridCLOBDriver: 未预期的 Prepare")
}
func (c *gridCLOBConn) Close() error              { return nil }
func (c *gridCLOBConn) Begin() (driver.Tx, error) { return gridCLOBTx{}, nil }
func (c *gridCLOBConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return gridCLOBTx{}, nil
}
func (c *gridCLOBConn) Ping(context.Context) error { return nil }
func (c *gridCLOBConn) CheckNamedValue(*driver.NamedValue) error {
	return nil
}
func (c *gridCLOBConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (c *gridCLOBConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.d.record(query)
	shape := c.d.rowsFunc(query)
	if len(shape.cols) == 0 {
		return nil, fmt.Errorf("gridCLOBDriver: 未预期的查询（元数据必须来自缓存）: %s", gridCFirstLine(query))
	}
	return &gridCLOBRows{cols: shape.cols, dbTypes: shape.dbTypes, rows: shape.rows}, nil
}

type gridCLOBTx struct{}

func (gridCLOBTx) Commit() error   { return nil }
func (gridCLOBTx) Rollback() error { return nil }

type gridCLOBRows struct {
	cols    []string
	dbTypes []string
	rows    [][]driver.Value
	idx     int
}

func (r *gridCLOBRows) Columns() []string { return r.cols }
func (r *gridCLOBRows) Close() error      { return nil }
func (r *gridCLOBRows) ColumnTypeDatabaseTypeName(index int) string {
	if index >= 0 && index < len(r.dbTypes) {
		return r.dbTypes[index]
	}
	return ""
}
func (r *gridCLOBRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.idx])
	r.idx++
	return nil
}

func gridCFirstLine(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	if len(text) > 120 {
		text = text[:120]
	}
	return text
}

// gridCNewLOBPool 建出真实的 database/sql 池, 并把 DB-06 需要的元数据全部预置到缓存,
// 这样假 driver 只会看到数据查询（与 DB-03 的“游标打开后不再申请连接”约束一致）。
func gridCNewLOBPool(t *testing.T, rowsFunc func(string) gridCLOBCase) (*Manager, Source, *gridCLOBDriver) {
	t.Helper()
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	driverImpl := &gridCLOBDriver{rowsFunc: rowsFunc}
	db := sql.OpenDB(driverImpl)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	source := Source{
		ID:                  "src-c-lob",
		Kind:                KindOracle,
		Username:            "SCOTT",
		OracleDriver:        "go-ora",
		MaxOpenConnections:  1,
		MaxIdleConnections:  1,
		QueryTimeoutSeconds: 5,
		MaxRows:             50,
		MaxResultBytes:      16 << 20,
	}
	m.pools[source.ID] = &poolEntry{fingerprint: sourceFingerprint(source), sql: db}
	if got, poolErr := m.sqlDB(source); poolErr != nil || got != db {
		t.Fatalf("gridCNewLOBPool: 注入的池未被复用 (err=%v)", poolErr)
	}

	fields := []Field{
		{Name: "TAG", DataType: "VARCHAR2(64)", Nullable: true},
		{Name: "BODY", DataType: "CLOB", Nullable: true},
	}
	m.SetCachedHeapTable(source.ID, "SCOTT", "HTTP_REQ_LOG", true)
	// DB-08: 字段与索引分成两个缓存键；这里把两者都预置，保证假 driver 只看到数据查询。
	metadataCacheSet(m, gridFieldsCacheKey(source.ID, sourceFingerprint(source), "SCOTT", "HTTP_REQ_LOG"), fields)
	metadataCacheSet(m, gridIndexesCacheKey(source.ID, sourceFingerprint(source), "SCOTT", "HTTP_REQ_LOG"), []IndexInfo{})
	metadataCacheSet(m, fmt.Sprintf("%s\x00lob_proj_fields\x00%s\x00%s", source.ID, "SCOTT", "HTTP_REQ_LOG"), fields)
	t.Cleanup(func() { _ = m.Close() })
	return m, source, driverImpl
}

// gridCPagingAliasName 从分页包装 SQL 里取出真实生成的辅助列名（服务端按查询摘要生成）。
func gridCPagingAliasName(query string) string {
	idx := strings.Index(strings.ToUpper(query), "__KAIRO_RN_")
	if idx < 0 {
		return ""
	}
	end := idx
	for end < len(query) {
		c := query[end]
		isIdentChar := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
		if !isIdentChar {
			break
		}
		end++
	}
	return query[idx:end]
}

// gridCLobRewriteShape 是 LOB 改写分支的底层结果形状：
// 业务列 + LOB 的 __LP_/__LL_ + 行尾 __KAIRO_ROWID__（分页第 2 页再追加辅助列）。
func gridCLobRewriteShape(query string, rowIDs []string, withPagingAlias bool) gridCLOBCase {
	if withPagingAlias {
		return gridCLOBCase{
			cols:    []string{"TAG", "__LP_1", "__LL_1", "__KAIRO_ROWID__", gridCPagingAliasName(query)},
			dbTypes: []string{"VARCHAR2", "NUMBER", "NUMBER", "VARCHAR2", "NUMBER"},
			rows: [][]driver.Value{
				{"same", int64(1), int64(5), rowIDs[0], int64(1)},
				{"same", int64(1), int64(6), rowIDs[1], int64(2)},
			},
		}
	}
	return gridCLOBCase{
		cols:    []string{"TAG", "__LP_1", "__LL_1", "__KAIRO_ROWID__"},
		dbTypes: []string{"VARCHAR2", "NUMBER", "NUMBER", "VARCHAR2"},
		rows: [][]driver.Value{
			{"same", int64(1), int64(5), rowIDs[0]},
			{"same", int64(1), int64(6), rowIDs[1]},
		},
	}
}

// gridCFastRowIDShape 是 Fast 分支（普通 scanner + 隐藏 ROWID 追加列）的形状。
func gridCFastRowIDShape(rowIDs []string) gridCLOBCase {
	return gridCLOBCase{
		cols:    []string{"TAG", "BODY", "__KAIRO_EDIT_RID__"},
		dbTypes: []string{"VARCHAR2", "CLOB", "VARCHAR2"},
		rows: [][]driver.Value{
			{"same", "first payload", rowIDs[0]},
			{"same", "second payload", rowIDs[1]},
		},
	}
}

func gridCStreamOnce(t *testing.T, fast bool, page int) ([]Column, [][]any, *GridEditPlanSummary, *gridCLOBDriver) {
	t.Helper()
	rowIDs := []string{"AAASDMAABAAAL9DAAA", "AAASDMAABAAAL9DAAB"}
	m, source, fake := gridCNewLOBPool(t, func(query string) gridCLOBCase {
		upper := strings.ToUpper(query)
		switch {
		case strings.Contains(upper, "__KAIRO_EDIT_RID__"):
			return gridCFastRowIDShape(rowIDs)
		case strings.Contains(upper, "KAIRO_RN"):
			return gridCLobRewriteShape(query, rowIDs, true)
		case strings.Contains(upper, "ROWIDTOCHAR"):
			return gridCLobRewriteShape(query, rowIDs, false)
		default:
			return gridCLOBCase{}
		}
	})
	var columns []Column
	var rows [][]any
	var plan *GridEditPlanSummary
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := m.StreamSessionQueryPageWithParamsAndOptions(ctx, source,
		"SELECT * FROM SCOTT.HTTP_REQ_LOG", page, 50, "tab-c-lob", nil,
		QueryOptions{Fast: fast}, func(ev StreamEvent) error {
			switch ev.Type {
			case "meta":
				columns = ev.Columns
				plan = ev.EditPlan
			case "rows":
				rows = append(rows, ev.Rows...)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("查询失败: %v (queries=%v)", err, fake.queries)
	}
	return columns, rows, plan, fake
}

// TestGridCBDb06LOBScannerReturnsAdvertisedHiddenRowID 断言 LOB 改写分支与普通分支协议一致：
// 业务列名保持纯净、行尾追加真实 ROWID、摘要广告的下标可直接取到定位值。
func TestGridCBDb06LOBScannerReturnsAdvertisedHiddenRowID(t *testing.T) {
	rowIDs := []string{"AAASDMAABAAAL9DAAA", "AAASDMAABAAAL9DAAB"}
	for _, tc := range []struct {
		name string
		page int
	}{{"explicit_normal_page1", 1}, {"page2_with_paging_alias", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			columns, rows, plan, fake := gridCStreamOnce(t, false, tc.page)
			if len(columns) != 2 {
				t.Fatalf("对外列名必须保持纯业务列(2 列)，实际 %d 列: %+v", len(columns), columns)
			}
			for _, col := range columns {
				if strings.HasPrefix(col.Name, "__") {
					t.Fatalf("辅助列不得泄露到列名: %+v", col)
				}
			}
			if plan == nil {
				t.Fatal("LOB 改写分支必须返回编辑计划摘要")
			}
			if plan.IdentityPolicy != "oracle_rowid" {
				t.Fatalf("普通堆表无主键应以 oracle_rowid 定位，实际 policy=%s reason=%q", plan.IdentityPolicy, plan.Reason)
			}
			if plan.HiddenRowIDIndex != len(columns) {
				t.Fatalf("hidden_rowid_index 必须等于行尾追加列下标 %d，实际 %d", len(columns), plan.HiddenRowIDIndex)
			}
			summaryRaw, mErr := json.Marshal(plan)
			if mErr != nil {
				t.Fatalf("序列化摘要失败: %v", mErr)
			}
			var summaryMap map[string]any
			if uErr := json.Unmarshal(summaryRaw, &summaryMap); uErr != nil {
				t.Fatalf("反序列化摘要失败: %v", uErr)
			}
			if len(rows) != 2 {
				t.Fatalf("必须返回 2 行，实际 %d (queries=%v)", len(rows), fake.queries)
			}
			for i, row := range rows {
				if len(row) <= plan.HiddenRowIDIndex {
					t.Fatalf("business_columns=%d; advertised_hidden_index=%d; actual_row_length=%d; "+
						"行数组中没有定位值(DB-06)", len(columns), plan.HiddenRowIDIndex, len(row))
				}
				got, ok := row[plan.HiddenRowIDIndex].(string)
				if !ok || got != rowIDs[i] {
					t.Fatalf("row[%d] 的定位值错误: got=%v (%T) want=%s", i, row[plan.HiddenRowIDIndex], row[plan.HiddenRowIDIndex], rowIDs[i])
				}
				if row[0] != "same" {
					t.Fatalf("业务列内容被破坏: row[%d][0]=%v", i, row[0])
				}
				lobCell, ok := row[1].(map[string]any)
				if !ok || lobCell["kind"] != "clob" {
					t.Fatalf("LOB 单元格协议被破坏: %v", row[1])
				}
				if tok, _ := lobCell["token"].(string); tok == "" {
					t.Fatalf("LOB 单元格必须带签名 token: %v", lobCell)
				}
			}
			if _, ok := summaryMap["columns"]; !ok {
				t.Fatalf("摘要必须携带 columns 列绑定（含行尾身份列），实际 keys=%v", summaryMap)
			}
		})
	}
}

// TestGridCBDb06FastModeLazyLOBKeepsProtocolAndCarriesRowID Fast 分支（普通 scanner）同样必须
// 保持“业务列 + 行尾 ROWID”，并且懒加载 LOB 单元格要带上本次查询的真实行身份，
// 否则按需 LOB 只能回退到会猜行的特征回查（DB-07）。
func TestGridCBDb06FastModeLazyLOBKeepsProtocolAndCarriesRowID(t *testing.T) {
	rowIDs := []string{"AAASDMAABAAAL9DAAA", "AAASDMAABAAAL9DAAB"}
	columns, rows, plan, _ := gridCStreamOnce(t, true, 1)
	if len(columns) != 2 {
		t.Fatalf("Fast 分支对外列名必须保持纯业务列，实际 %+v", columns)
	}
	if plan == nil || plan.IdentityPolicy != "oracle_rowid" || plan.HiddenRowIDIndex != len(columns) {
		t.Fatalf("Fast 分支应广告 oracle_rowid 且下标等于行尾列，实际 %+v", plan)
	}
	if len(rows) != 2 {
		t.Fatalf("必须返回 2 行，实际 %d", len(rows))
	}
	for i, row := range rows {
		if len(row) <= plan.HiddenRowIDIndex {
			t.Fatalf("Fast 分支行数组缺少行尾定位值: business_columns=%d advertised=%d actual=%d",
				len(columns), plan.HiddenRowIDIndex, len(row))
		}
		if row[plan.HiddenRowIDIndex] != rowIDs[i] {
			t.Fatalf("Fast 分支行尾定位值错误: got=%v want=%s", row[plan.HiddenRowIDIndex], rowIDs[i])
		}
		lobCell, ok := row[1].(map[string]any)
		if !ok || lobCell["lazy"] != true {
			t.Fatalf("Fast 分支的 LOB 单元格应是懒加载对象: %v", row[1])
		}
		if lobCell["rowid"] != rowIDs[i] {
			t.Fatalf("懒加载 LOB 必须携带本次查询的真实行身份（DB-07），实际 rowid=%v want=%s", lobCell["rowid"], rowIDs[i])
		}
	}
}
