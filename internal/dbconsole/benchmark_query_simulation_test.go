package dbconsole

import (
	"strings"
	"testing"
)

// TestSimulationOldVsFastPagination 模拟复现分页包装优化：
// 旧版在 Page 1 仍使用两层嵌套和额外的别名列，新版在 Page 1 (offset=0) 简化为最简单层 ROWNUM <= N
func TestSimulationOldVsFastPagination(t *testing.T) {
	query := "SELECT * FROM BIG_TABLE"

	// 1. Page 1 (offset = 0)
	p1 := QueryPage{Page: 1, PageSize: 20}
	pagedOracleP1, err := serverPagedQuery(KindOracle, query, p1)
	if err != nil {
		t.Fatalf("serverPagedQuery Oracle P1 failed: %v", err)
	}

	t.Logf("Oracle Page 1 SQL:\n%s", pagedOracleP1)

	// 新版期望：单层 ROWNUM <= 21，不包含 __KAIRO_RN_ 别名列，不包含多余的外层 > 0
	if strings.Contains(pagedOracleP1, "__KAIRO_RN_") {
		t.Errorf("Page 1 Oracle query should not contain __KAIRO_RN_ alias column: %s", pagedOracleP1)
	}
	if strings.Contains(pagedOracleP1, "> 0") {
		t.Errorf("Page 1 Oracle query should not contain redundant '> 0' outer filter: %s", pagedOracleP1)
	}
	if !strings.Contains(pagedOracleP1, "ROWNUM <= 21") {
		t.Errorf("Page 1 Oracle query must contain ROWNUM <= 21, got: %s", pagedOracleP1)
	}
	if !strings.Contains(pagedOracleP1, "FIRST_ROWS") {
		t.Errorf("Page 1 Oracle query must contain FIRST_ROWS optimizer hint, got: %s", pagedOracleP1)
	}

	pagedMySQLP1, err := serverPagedQuery(KindMySQL, query, p1)
	if err != nil {
		t.Fatalf("serverPagedQuery MySQL P1 failed: %v", err)
	}
	t.Logf("MySQL Page 1 SQL:\n%s", pagedMySQLP1)
	if strings.Contains(pagedMySQLP1, "OFFSET 0") {
		t.Errorf("Page 1 MySQL query should not contain OFFSET 0: %s", pagedMySQLP1)
	}
	if !strings.Contains(pagedMySQLP1, "LIMIT 21") {
		t.Errorf("Page 1 MySQL query must contain LIMIT 21: %s", pagedMySQLP1)
	}

	// 2. Page 2 (offset = 20) 应保留双层嵌套
	p2 := QueryPage{Page: 2, PageSize: 20}
	pagedOracleP2, err := serverPagedQuery(KindOracle, query, p2)
	if err != nil {
		t.Fatalf("serverPagedQuery Oracle P2 failed: %v", err)
	}
	if !strings.Contains(pagedOracleP2, "__KAIRO_RN_") || !strings.Contains(pagedOracleP2, "> 20") {
		t.Errorf("Page 2 Oracle query must retain full pagination wrapper: %s", pagedOracleP2)
	}
}

// TestSimulationNoForcedOrderBy 模拟复现移除强制全表 ORDER BY：
// 当用户 SQL 没有 ORDER BY 时，不再强制追加 ORDER BY PK 或 ROWID，避免千万级大表全表排序 (SORT ORDER BY)
func TestSimulationNoForcedOrderBy(t *testing.T) {
	info, ok := parseSafeSingleTableQuery("SELECT * FROM HR.BIG_DOCS")
	if !ok {
		t.Fatal("parseSafeSingleTableQuery failed")
	}

	fields := []Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "TITLE", DataType: "VARCHAR2"},
		{Name: "CONTENT", DataType: "CLOB"},
	}

	// 1. 用户未指定 ORDER BY，重写结果不应包含 ORDER BY
	rwNoOrder, err := compileLOBProjectedQuery(info, fields)
	if err != nil {
		t.Fatalf("compileLOBProjectedQuery failed: %v", err)
	}
	if strings.Contains(rwNoOrder.SQL, "ORDER BY") {
		t.Errorf("Expected NO ORDER BY when user did not specify one, got:\n%s", rwNoOrder.SQL)
	}

	// 2. 当查询结构中带有 ORDER BY 时，则保留并补充稳定性
	infoWithOrder := *info
	infoWithOrder.OrderByClause = `"BIG_DOCS"."TITLE" ASC`
	rwWithOrder, err := compileLOBProjectedQuery(&infoWithOrder, fields)
	if err != nil {
		t.Fatalf("compileLOBProjectedQuery with order failed: %v", err)
	}
	if !strings.Contains(rwWithOrder.SQL, `ORDER BY "BIG_DOCS"."TITLE" ASC`) {
		t.Errorf("Expected user's ORDER BY to be preserved, got:\n%s", rwWithOrder.SQL)
	}
}
