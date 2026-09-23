package dbconsole

// Batch B / DB-01 + DB-02 回归测试：
//   DB-01 Oracle ROWID 改写必须保留标识符的引号语义（未加引号统一大写，显式加引号原样保留），
//         且 plan.Schema/plan.Table 必须使用同一套规范化结果，否则写入仍会指向 "emp"。
//   DB-02 只有可证明安全的单表查询才允许追加逐行 ROWID；聚合/表达式/不可确认别名一律保留原 SQL，
//         并且普通堆表事实未知时不得默认按堆表开放 ROWID 编辑。
//
// 这些断言只覆盖纯函数与纯规划（不依赖真实 Oracle）。

import (
	"context"
	"strings"
	"testing"
	"time"
)

// gridBPlanTestSource 返回一个无法建立真实连接的 Oracle 数据源，
// 保证测试只走缓存/纯函数路径，冷缓存时任何数据库尝试都会被短预算截断。
func gridBPlanTestSource(id string) Source {
	return Source{
		ID:                  id,
		Kind:                KindOracle,
		Username:            "scott",
		MaxOpenConnections:  1,
		MaxIdleConnections:  1,
		QueryTimeoutSeconds: 1,
	}
}

// TestGridBDB01RewriteKeepsIdentifierSemantics 覆盖未加引号/加引号标识符的引用规则。
func TestGridBDB01RewriteKeepsIdentifierSemantics(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string // 必须出现在改写结果中的片段
	}{
		{
			name: "unquoted lowercase table is uppercased before quoting",
			sql:  "select * from emp",
			want: `ROWIDTOCHAR("EMP".ROWID) AS "__KAIRO_EDIT_RID__"`,
		},
		{
			name: "unquoted lowercase alias is uppercased before quoting",
			sql:  "SELECT t.* FROM SCOTT.EMP t",
			want: `ROWIDTOCHAR("T".ROWID) AS "__KAIRO_EDIT_RID__"`,
		},
		{
			name: "explicitly quoted alias stays verbatim",
			sql:  `SELECT "t".* FROM SCOTT.EMP "t"`,
			want: `ROWIDTOCHAR("t".ROWID) AS "__KAIRO_EDIT_RID__"`,
		},
		{
			name: "explicitly quoted lowercase table stays verbatim",
			sql:  `select * from "emp"`,
			want: `ROWIDTOCHAR("emp".ROWID) AS "__KAIRO_EDIT_RID__"`,
		},
		{
			name: "explicit columns with lowercase unquoted table",
			sql:  "select empno, ename from emp where empno = 7369",
			want: `ROWIDTOCHAR("EMP".ROWID) AS "__KAIRO_EDIT_RID__"`,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RewriteOracleQueryForHiddenRowID(tt.sql)
			if err != nil {
				t.Fatalf("RewriteOracleQueryForHiddenRowID(%q) error = %v", tt.sql, err)
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("DB-01: rewritten SQL must contain %s\ngot: %s", tt.want, got)
			}
		})
	}
}

// TestGridBDB01ParseNormalizesOracleIdentifiers 断言语法解析结果本身已完成规范化。
func TestGridBDB01ParseNormalizesOracleIdentifiers(t *testing.T) {
	parsed, err := parseGridQuerySyntax(KindOracle, "select empno, ename from emp")
	if err != nil {
		t.Fatalf("parseGridQuerySyntax error = %v", err)
	}
	if parsed.Table != "EMP" {
		t.Errorf("DB-01: unquoted Oracle table must normalize to EMP, got %q", parsed.Table)
	}
	if parsed.TableAlias != "" {
		t.Errorf("DB-01: unexpected alias %q", parsed.TableAlias)
	}

	quoted, err := parseGridQuerySyntax(KindOracle, `select * from "emp"`)
	if err != nil {
		t.Fatalf("parseGridQuerySyntax quoted error = %v", err)
	}
	if quoted.Table != "emp" {
		t.Errorf("DB-01: quoted Oracle table must keep exact case, got %q", quoted.Table)
	}

	aliasParsed, err := parseGridQuerySyntax(KindOracle, "select t.* from scott.emp t")
	if err != nil {
		t.Fatalf("parseGridQuerySyntax alias error = %v", err)
	}
	if aliasParsed.Schema != "SCOTT" || aliasParsed.Table != "EMP" || aliasParsed.TableAlias != "T" {
		t.Errorf("DB-01: want SCOTT.EMP alias T, got %q.%q alias %q",
			aliasParsed.Schema, aliasParsed.Table, aliasParsed.TableAlias)
	}

	quotedAlias, err := parseGridQuerySyntax(KindOracle, `select "t".* from scott.emp "t"`)
	if err != nil {
		t.Fatalf("parseGridQuerySyntax quoted alias error = %v", err)
	}
	if quotedAlias.TableAlias != "t" {
		t.Errorf("DB-01: quoted alias must keep exact case, got %q", quotedAlias.TableAlias)
	}
}

// TestGridBDB01PlanAndMutationUseNormalizedTarget 断言编辑计划与写入 SQL 指向真实表名。
func TestGridBDB01PlanAndMutationUseNormalizedTarget(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	source := gridBPlanTestSource("src-db01-plan")
	m.SetCachedFields("src-db01-plan", "SCOTT", "EMP", []Field{
		{Name: "EMPNO", DataType: "NUMBER", PrimaryKey: true},
		{Name: "ENAME", DataType: "VARCHAR2(50)"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	plan, err := m.AnalyzeGridQuery(ctx, source, "tab-db01", "select empno, ename from emp",
		[]Column{{Name: "EMPNO", Database: "NUMBER"}, {Name: "ENAME", Database: "VARCHAR2"}})
	if err != nil {
		t.Fatalf("AnalyzeGridQuery error = %v", err)
	}
	if plan.Schema != "SCOTT" || plan.Table != "EMP" {
		t.Fatalf("DB-01: plan target must be SCOTT.EMP, got %q.%q (reason=%s)", plan.Schema, plan.Table, plan.Reason)
	}
	if plan.IdentityPolicy != "pk" || !plan.CanDelete {
		t.Fatalf("DB-01: projected PK must stay editable, got policy=%s canDelete=%v reason=%s",
			plan.IdentityPolicy, plan.CanDelete, plan.Reason)
	}

	sqlText, args, err := BuildGridMutationSQLWithPlan(KindOracle, plan, GridMutation{
		Action: "delete",
		Key:    map[string]any{"EMPNO": 7369},
	})
	if err != nil {
		t.Fatalf("BuildGridMutationSQLWithPlan error = %v", err)
	}
	if !strings.Contains(sqlText, `DELETE FROM "SCOTT"."EMP"`) {
		t.Errorf("DB-01: mutation SQL must target \"SCOTT\".\"EMP\", got: %s", sqlText)
	}
	if len(args) != 1 {
		t.Errorf("DB-01: unexpected args %#v", args)
	}
}

// TestGridBDB02RewriteAllowList 断言只有可证明安全的单表物理列投影才允许追加 ROWID。
func TestGridBDB02RewriteAllowList(t *testing.T) {
	// 现状是这些查询都会被追加 ROWID，其中聚合会直接触发 ORA-00937。
	notProvable := []struct {
		name string
		sql  string
	}{
		{"aggregate count without group by", "SELECT COUNT(*) FROM EMP"},
		{"aggregate max", "SELECT MAX(SAL) FROM EMP"},
		{"aggregate sum with alias", "SELECT SUM(SAL) AS TOTAL FROM EMP"},
		{"aggregate min and max mixed with column", "SELECT DEPTNO, MIN(SAL), MAX(SAL) FROM EMP"},
		{"aggregate with where clause", "SELECT COUNT(*) FROM EMP WHERE DEPTNO = 10"},
		{"arithmetic expression projection", "SELECT SAL + 1 FROM EMP"},
		{"string literal projection", "SELECT 'x' FROM EMP"},
		{"numeric literal projection", "SELECT 1 FROM EMP"},
		{"function over physical column", "SELECT UPPER(ENAME) FROM EMP"},
		{"unverifiable wildcard qualifier", "SELECT x.* FROM EMP"},
		{"qualifier does not match the single table", "SELECT e.EMPNO FROM EMP d"},
		{"hierarchical query connect by", "SELECT * FROM EMP CONNECT BY PRIOR EMPNO = MGR"},
		{"hierarchical query start with", "SELECT * FROM EMP START WITH MGR IS NULL CONNECT BY PRIOR EMPNO = MGR"},
		{"pseudo column rownum", "SELECT ROWNUM, EMPNO FROM EMP"},
	}

	for _, tt := range notProvable {
		t.Run("reject/"+tt.name, func(t *testing.T) {
			got, err := RewriteOracleQueryForHiddenRowID(tt.sql)
			if err == nil {
				t.Errorf("DB-02: %q must not be rewritten, got: %s", tt.sql, got)
			}
			if got != tt.sql {
				t.Errorf("DB-02: rejected rewrite must return the original SQL\nwant: %s\ngot:  %s", tt.sql, got)
			}
		})
	}

	provable := []struct {
		name string
		sql  string
		want string
	}{
		{"wildcard without alias", "SELECT * FROM EMP", `ROWIDTOCHAR("EMP".ROWID) AS "__KAIRO_EDIT_RID__"`},
		{"wildcard with matching alias", "SELECT e.* FROM EMP e", `ROWIDTOCHAR("E".ROWID) AS "__KAIRO_EDIT_RID__"`},
		{"physical columns", "SELECT EMPNO, ENAME FROM EMP", `ROWIDTOCHAR("EMP".ROWID) AS "__KAIRO_EDIT_RID__"`},
		{"qualified physical column", "SELECT E.EMPNO FROM EMP E", `ROWIDTOCHAR("E".ROWID) AS "__KAIRO_EDIT_RID__"`},
		{"schema qualified table", "SELECT EMPNO FROM SCOTT.EMP WHERE DEPTNO = 10", `ROWIDTOCHAR("EMP".ROWID) AS "__KAIRO_EDIT_RID__"`},
		{"where clause subquery keeps single base table", "SELECT EMPNO FROM EMP WHERE DEPTNO IN (SELECT DEPTNO FROM DEPT)", `ROWIDTOCHAR("EMP".ROWID) AS "__KAIRO_EDIT_RID__"`},
	}

	for _, tt := range provable {
		t.Run("allow/"+tt.name, func(t *testing.T) {
			got, err := RewriteOracleQueryForHiddenRowID(tt.sql)
			if err != nil {
				t.Fatalf("DB-02: %q must stay editable, error = %v", tt.sql, err)
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("DB-02: rewritten SQL must contain %s\ngot: %s", tt.want, got)
			}
		})
	}
}

// TestGridBDB02UnknownHeapFactMustNotEnableRowIDEditing 断言堆表事实未知（查询失败/未缓存）时不得默认按堆表处理。
func TestGridBDB02UnknownHeapFactMustNotEnableRowIDEditing(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	source := gridBPlanTestSource("src-db02-heap")
	m.SetCachedFields("src-db02-heap", "SCOTT", "LOG_TABLE", []Field{
		{Name: "ID", DataType: "NUMBER"},
		{Name: "MESSAGE", DataType: "VARCHAR2(200)"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// 没有缓存堆表事实，且当前环境无法连接 Oracle => 事实未知。
	plan, err := m.AnalyzeGridQuery(ctx, source, "tab-db02", "SELECT ID, MESSAGE FROM SCOTT.LOG_TABLE",
		[]Column{{Name: "ID", Database: "NUMBER"}, {Name: "MESSAGE", Database: "VARCHAR2"}}, 2)
	if err != nil {
		t.Fatalf("AnalyzeGridQuery error = %v", err)
	}
	if plan.IdentityPolicy == "oracle_rowid" || plan.CanUpdate || plan.CanDelete {
		t.Fatalf("DB-02: unknown heap fact must degrade to read-only, got policy=%s canUpdate=%v canDelete=%v",
			plan.IdentityPolicy, plan.CanUpdate, plan.CanDelete)
	}

	// 明确确认是普通堆表后才允许 ROWID 编辑。
	m.SetCachedHeapTable("src-db02-heap", "SCOTT", "LOG_TABLE", true)
	heapPlan, err := m.AnalyzeGridQuery(ctx, source, "tab-db02b", "SELECT ID, MESSAGE FROM SCOTT.LOG_TABLE",
		[]Column{{Name: "ID", Database: "NUMBER"}, {Name: "MESSAGE", Database: "VARCHAR2"}}, 2)
	if err != nil {
		t.Fatalf("AnalyzeGridQuery(heap) error = %v", err)
	}
	if heapPlan.IdentityPolicy != "oracle_rowid" || !heapPlan.CanUpdate {
		t.Fatalf("DB-02: confirmed heap table must allow ROWID editing, got policy=%s reason=%s",
			heapPlan.IdentityPolicy, heapPlan.Reason)
	}
}
