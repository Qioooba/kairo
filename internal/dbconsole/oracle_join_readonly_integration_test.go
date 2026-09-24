//go:build integration

// 关联查询（JOIN）网格只读性 —— 真实 Oracle 实证探针。
//
// 目的：不靠阅读代码推断，而是在真实 KAIRO_LAB 数据上枚举关联/复合查询形态，
// 逐个核对服务端签发的 edit_plan 是否把所有写入能力都关掉。
//
// 复用 oracle_workbench_test.go 的四道门禁（build tag / KAIRO_INTEGRATION_ORACLE=1 /
// 专用 DSN / DDL 单独授权）。本文件只跑 SELECT，不需要 DDL 授权。
//
// 运行：
//
//	KAIRO_INTEGRATION_ORACLE=1 \
//	KAIRO_TEST_ORACLE_HOST=127.0.0.1 KAIRO_TEST_ORACLE_USER=kairo_ro \
//	KAIRO_TEST_ORACLE_PASSWORD=... KAIRO_TEST_ORACLE_SERVICE=XEPDB1 \
//	go test -tags=integration -run TestOracleJoinReadOnly -v ./internal/dbconsole/
package dbconsole

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

const labSchema = "kairo_lab"

// joinProbeExpect 是单个探针用例的期望。
type joinProbeExpect string

const (
	// expectReadOnly：结果与目标行身份无法唯一对应，三项写入能力必须全关。
	expectReadOnly joinProbeExpect = "readonly"
	// expectEditable：单基表且定位键完整，应当保持可编辑（防止修复误伤主路径）。
	expectEditable joinProbeExpect = "editable"
	// expectObserve：语义上不属于"关联查询"，只记录实际行为，不做断言。
	expectObserve joinProbeExpect = "observe"
)

type joinProbeCase struct {
	name   string
	sql    string
	expect joinProbeExpect
}

func joinProbeCorpus() []joinProbeCase {
	return []joinProbeCase{
		// ---- 显式关联 ----
		{"显式 INNER JOIN", `SELECT e.id, e.employee_no, d.name FROM ` + labSchema + `.employees e JOIN ` + labSchema + `.departments d ON d.id = e.department_id`, expectReadOnly},
		{"显式 LEFT JOIN 通配符", `SELECT e.*, d.name FROM ` + labSchema + `.employees e LEFT JOIN ` + labSchema + `.departments d ON d.id = e.department_id`, expectReadOnly},
		{"自连接", `SELECT a.id, b.display_name FROM ` + labSchema + `.employees a JOIN ` + labSchema + `.employees b ON b.id = a.id`, expectReadOnly},
		{"USING 连接", `SELECT * FROM ` + labSchema + `.employees e JOIN ` + labSchema + `.departments d USING (id)`, expectReadOnly},

		// ---- 隐式关联 / 11g 外连接 ----
		{"逗号隐式连接", `SELECT e.id, d.name FROM ` + labSchema + `.employees e, ` + labSchema + `.departments d WHERE d.id = e.department_id`, expectReadOnly},
		{"11g (+) 外连接", `SELECT e.id, d.name FROM ` + labSchema + `.employees e, ` + labSchema + `.departments d WHERE e.department_id = d.id(+)`, expectReadOnly},

		// ---- 关联视图（内部就是 JOIN，且视图没有主键元数据）----
		{"关联视图 employee_directory", `SELECT * FROM ` + labSchema + `.employee_directory`, expectReadOnly},
		{"关联视图 显式列", `SELECT id, employee_no, department_name FROM ` + labSchema + `.employee_directory`, expectReadOnly},

		// ---- 复合查询 ----
		{"派生表", `SELECT * FROM (SELECT id, employee_no FROM ` + labSchema + `.employees) t`, expectReadOnly},
		{"CTE", `WITH t AS (SELECT * FROM ` + labSchema + `.employees) SELECT * FROM t`, expectReadOnly},
		{"聚合 GROUP BY", `SELECT department_id, COUNT(*) c FROM ` + labSchema + `.employees GROUP BY department_id`, expectReadOnly},
		{"UNION ALL", `SELECT id FROM ` + labSchema + `.employees UNION ALL SELECT id FROM ` + labSchema + `.departments`, expectReadOnly},
		{"DISTINCT", `SELECT DISTINCT department_id FROM ` + labSchema + `.employees`, expectReadOnly},
		{"无 GROUP BY 聚合", `SELECT COUNT(*) FROM ` + labSchema + `.employees`, expectReadOnly},

		// ---- 单基表 + 关联子查询：目标表仍是 employees，仅记录行为 ----
		{"投影含关联子查询", `SELECT e.id, (SELECT COUNT(*) FROM ` + labSchema + `.audit_events a WHERE a.employee_id = e.id) AS cnt FROM ` + labSchema + `.employees e`, expectObserve},
		{"WHERE 含 IN 子查询", `SELECT id, display_name FROM ` + labSchema + `.employees WHERE department_id IN (SELECT id FROM ` + labSchema + `.departments)`, expectObserve},

		// ---- 控制组：单基表且定位键完整，必须保持可编辑 ----
		{"控制组 单表全字段", `SELECT id, employee_no, display_name, department_id, monthly_salary, active, tags, remark, hired_at FROM ` + labSchema + `.employees`, expectEditable},
		{"控制组 单表含主键子集", `SELECT id, display_name FROM ` + labSchema + `.employees`, expectEditable},
		// 单基表、无主键投影 → 服务端会追加隐藏 ROWID 定位列，按设计**应当可编辑**。
		// 这是 DB-01/DB-02 的旗舰场景，放在这里防止"关联查询一律只读"的修复误伤它。
		{"控制组 单表无主键投影（ROWID 定位）", `SELECT display_name, monthly_salary FROM ` + labSchema + `.employees`, expectEditable},
	}
}

// observation 是一次探针执行的观测结果。
type observation struct {
	plan      *GridEditPlanSummary
	columns   []Column
	elapsedMS int64
	prepMS    int64
	wall      time.Duration
	err       error
}

// probeGridQuery 用真实会话 id 走服务端流式查询路径，抓取签发的 edit_plan。
func probeGridQuery(t *testing.T, manager *Manager, source Source, sqlText, sessionID string) observation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var obs observation
	started := time.Now()
	sum, err := manager.StreamSessionQueryPageWithParamsAndOptions(
		ctx, source, sqlText, 1, 50, sessionID, nil, QueryOptions{Fast: true},
		func(event StreamEvent) error {
			switch event.Type {
			case "meta":
				obs.plan = event.EditPlan
				obs.columns = event.Columns
			}
			return nil
		})
	obs.wall = time.Since(started)
	obs.elapsedMS = sum.ElapsedMS
	obs.prepMS = sum.PrepMS
	obs.err = err
	return obs
}

// TestOracleJoinReadOnlyProbe 在真实库上枚举关联查询，核对只读强制是否闭合。
func TestOracleJoinReadOnlyProbe(t *testing.T) {
	manager, source, password := newOracleWorkbenchHarness(t, false)

	failures := 0
	for i, c := range joinProbeCorpus() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			sessionID := fmt.Sprintf("joinprobe-%04d", i)
			obs := probeGridQuery(t, manager, source, c.sql, sessionID)
			if obs.err != nil {
				t.Fatalf("查询失败: %s", redactIntegrationError(obs.err, password))
			}
			if obs.plan == nil {
				t.Fatalf("服务端未签发 edit_plan（会话查询本应总是签发）")
			}

			writable := 0
			for _, col := range obs.plan.Columns {
				if col.Writable {
					writable++
				}
			}
			t.Logf("expect=%s policy=%s can_insert=%v can_update=%v can_delete=%v writable_cols=%d/%d table=%s elapsed_ms=%d prep_ms=%d reason=%q",
				c.expect, obs.plan.IdentityPolicy, obs.plan.CanInsert, obs.plan.CanUpdate, obs.plan.CanDelete,
				writable, len(obs.plan.Columns), obs.plan.Table, obs.elapsedMS, obs.prepMS, obs.plan.Reason)

			switch c.expect {
			case expectReadOnly:
				if obs.plan.CanInsert || obs.plan.CanUpdate || obs.plan.CanDelete {
					failures++
					t.Errorf("关联/复合查询仍开放写入能力: can_insert=%v can_update=%v can_delete=%v policy=%s reason=%q",
						obs.plan.CanInsert, obs.plan.CanUpdate, obs.plan.CanDelete, obs.plan.IdentityPolicy, obs.plan.Reason)
				}
				if writable > 0 {
					failures++
					t.Errorf("关联/复合查询仍有 %d 个可写列", writable)
				}
			case expectEditable:
				if !obs.plan.CanUpdate {
					failures++
					t.Errorf("单表完整定位键被误伤为只读: policy=%s reason=%q", obs.plan.IdentityPolicy, obs.plan.Reason)
				}
				if writable == 0 {
					failures++
					t.Errorf("单表可编辑但没有任何可写列: policy=%s", obs.plan.IdentityPolicy)
				}
			}
		})
	}
	if failures > 0 {
		t.Errorf("关联查询只读性存在 %d 处缺口", failures)
	}
}

// TestOracleGridPrepLatency 在真实库上观测同一查询的冷/热缓存耗时，
// 用于量化"首次查询慢、紧接着再查变快"是否真实存在。
func TestOracleGridPrepLatency(t *testing.T) {
	manager, source, password := newOracleWorkbenchHarness(t, false)

	cases := []struct {
		name string
		sql  string
	}{
		{"单表全字段（可编辑，定位键完整）", `SELECT id, employee_no, display_name, department_id, monthly_salary, active, tags, remark, hired_at FROM ` + labSchema + `.employees`},
		{"单表无主键投影（触发 ROWID 路径）", `SELECT display_name, monthly_salary FROM ` + labSchema + `.employees`},
	}

	const runs = 4
	for ci, c := range cases {
		var first, warm int64
		for r := 0; r < runs; r++ {
			// 每次换新会话 id，避免事务/游标复用掩盖元数据缓存效应。
			sessionID := fmt.Sprintf("latprobe-%d-%d", ci, r)
			obs := probeGridQuery(t, manager, source, c.sql, sessionID)
			if obs.err != nil {
				t.Fatalf("%s 第 %d 次查询失败: %s", c.name, r, redactIntegrationError(obs.err, password))
			}
			if r == 0 {
				first = obs.elapsedMS
			} else {
				warm += obs.elapsedMS
			}
			t.Logf("%s run=%d elapsed_ms=%d wall_ms=%d policy=%s",
				c.name, r, obs.elapsedMS, obs.wall.Milliseconds(), obs.plan.IdentityPolicy)
		}
		if runs > 1 {
			t.Logf("%s 冷=%dms 热均值=%dms", c.name, first, warm/int64(runs-1))
		}
	}
}

// TestOracleEditPlanColumnsAlignWithResult 校验 edit_plan 的列摘要与真实结果列一一对齐。
// 这是"前端按索引协议提交变更"能否安全解析的前提。
func TestOracleEditPlanColumnsAlignWithResult(t *testing.T) {
	manager, source, password := newOracleWorkbenchHarness(t, false)
	sqlText := `SELECT id, employee_no, display_name FROM ` + labSchema + `.employees`
	obs := probeGridQuery(t, manager, source, sqlText, "alignprobe-0001")
	if obs.err != nil {
		t.Fatalf("查询失败: %s", redactIntegrationError(obs.err, password))
	}
	if obs.plan == nil {
		t.Fatal("未签发 edit_plan")
	}
	if len(obs.plan.Columns) != len(obs.columns) {
		t.Errorf("列摘要数量 %d 与结果列数量 %d 不一致", len(obs.plan.Columns), len(obs.columns))
	}
	for i, col := range obs.columns {
		if i >= len(obs.plan.Columns) {
			break
		}
		got := strings.ToUpper(obs.plan.Columns[i].ResultName)
		want := strings.ToUpper(col.Name)
		if got != want {
			t.Errorf("第 %d 列摘要名 %q 与结果列名 %q 不一致", i, got, want)
		}
	}
}
