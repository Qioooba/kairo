//go:build integration

// 真实 Oracle 网格写入回归：确认「写入必须绑定服务端编辑计划」的收紧
// 没有把合法写入一起关掉，同时确认关联视图/派生来源拿不到可写的编辑计划。
//
// 需要一个对目标 schema 有 DML 权限的账号（仓库自带的实验账号 kairo_lab）；
// 只插入并删除一条固定 ID 的临时行，无论成败都会清理。
//
// 运行：
//
//	KAIRO_INTEGRATION_ORACLE=1 \
//	KAIRO_TEST_ORACLE_HOST=127.0.0.1 KAIRO_TEST_ORACLE_USER=kairo_lab \
//	KAIRO_TEST_ORACLE_PASSWORD=... KAIRO_TEST_ORACLE_SERVICE=XEPDB1 \
//	go test -tags=integration -run TestOracleGridWriteRealRoundTrip -v ./internal/dbconsole/
package dbconsole

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

const (
	gridWriteProbeID   = 999001
	gridWriteProbeName = "KAIRO_DB08_真实写入验证"
)

// TestOracleGridWriteRealRoundTrip 在真实库上跑一次「查询 → 取计划 → 插入 → 回读 → 删除」。
func TestOracleGridWriteRealRoundTrip(t *testing.T) {
	manager, source, password := newOracleWorkbenchHarness(t, false)

	// 无论成败都清理这一条固定 ID 的行，避免污染实验库。
	t.Cleanup(func() { gridWriteProbeDelete(t, manager, source, password) })

	const sessionID = "writeprobe-0001"
	selectSQL := `SELECT id, employee_no, display_name, department_id, monthly_salary, active FROM ` + labSchema + `.employees WHERE id = 1`
	obs := probeGridQuery(t, manager, source, selectSQL, sessionID)
	if obs.err != nil {
		t.Fatalf("查询失败: %s", redactIntegrationError(obs.err, password))
	}
	if obs.plan == nil {
		t.Fatal("服务端未签发编辑计划")
	}
	t.Logf("计划: policy=%s can_insert=%v can_update=%v can_delete=%v table=%s",
		obs.plan.IdentityPolicy, obs.plan.CanInsert, obs.plan.CanUpdate, obs.plan.CanDelete, obs.plan.Table)

	// 只读账号（如 kairo_ro）跑集成本来就是合法降级场景：跳过而不是失败。
	if !obs.plan.CanInsert || !obs.plan.CanUpdate || !obs.plan.CanDelete {
		t.Skipf("当前账号/对象没有写入能力（can_insert=%v can_update=%v can_delete=%v），"+
			"写入回归需要 kairo_lab 这类有 DML 权限的账号；reason=%q",
			obs.plan.CanInsert, obs.plan.CanUpdate, obs.plan.CanDelete, obs.plan.Reason)
	}
	if obs.plan.IdentityPolicy != "pk" {
		t.Fatalf("期望主键定位，实际 %s", obs.plan.IdentityPolicy)
	}

	// 先清掉可能残留的同 ID 行，让插入是确定性的。
	gridWriteProbeDelete(t, manager, source, password)

	// 1) 插入：名称协议，服务端凭不可变 Columns 解析物理列。
	insertSum, err := manager.ApplyGridMutations(context.Background(), source, GridMutationRequest{
		ResultID:  obs.plan.ResultID,
		SessionID: sessionID,
		Commit:    true,
		Mutations: []GridMutation{{
			Action: "insert",
			Values: map[string]any{
				"ID":            gridWriteProbeID,
				"EMPLOYEE_NO":   "E999001",
				"DISPLAY_NAME":  gridWriteProbeName,
				"DEPARTMENT_ID": 1,
			},
		}},
	})
	if err != nil {
		t.Fatalf("真实插入失败: %s", redactIntegrationError(err, password))
	}
	if insertSum.RowsAffected != 1 {
		t.Fatalf("插入应当只影响 1 行，实际 %d（%+v）", insertSum.RowsAffected, insertSum.Results)
	}

	// 2) 回读确认真的落库了（不是只看返回值）。
	if got := gridWriteProbeReadName(t, manager, source, password); got != gridWriteProbeName {
		t.Fatalf("回读 display_name = %q，期望 %q", got, gridWriteProbeName)
	}

	// 3) 删除：同一个编辑计划（pk 定位），用主键原值定位。
	deleteSum, err := manager.ApplyGridMutations(context.Background(), source, GridMutationRequest{
		ResultID:  obs.plan.ResultID,
		SessionID: sessionID,
		Commit:    true,
		Mutations: []GridMutation{{
			Action: "delete",
			Key:    map[string]any{"ID": gridWriteProbeID},
		}},
	})
	if err != nil {
		t.Fatalf("真实删除失败: %s", redactIntegrationError(err, password))
	}
	if deleteSum.RowsAffected != 1 {
		t.Fatalf("删除应当只影响 1 行，实际 %d（%+v）", deleteSum.RowsAffected, deleteSum.Results)
	}
	if got := gridWriteProbeReadName(t, manager, source, password); got != "" {
		t.Fatalf("删除后仍能读到 %q", got)
	}
}

// TestOracleJoinViewHasNoWritablePlan 真实库上确认关联视图拿不到任何写入能力。
func TestOracleJoinViewHasNoWritablePlan(t *testing.T) {
	manager, source, password := newOracleWorkbenchHarness(t, false)

	const sessionID = "writeprobe-view"
	obs := probeGridQuery(t, manager, source, `SELECT * FROM `+labSchema+`.employee_directory`, sessionID)
	if obs.err != nil {
		t.Fatalf("查询失败: %s", redactIntegrationError(obs.err, password))
	}
	if obs.plan == nil {
		t.Fatal("服务端未签发编辑计划")
	}
	t.Logf("关联视图计划: policy=%s can_insert=%v can_update=%v can_delete=%v reason=%q",
		obs.plan.IdentityPolicy, obs.plan.CanInsert, obs.plan.CanUpdate, obs.plan.CanDelete, obs.plan.Reason)

	if obs.plan.CanInsert || obs.plan.CanUpdate || obs.plan.CanDelete {
		t.Fatalf("关联视图不得开放任何写入能力: can_insert=%v can_update=%v can_delete=%v",
			obs.plan.CanInsert, obs.plan.CanUpdate, obs.plan.CanDelete)
	}
	for _, col := range obs.plan.Columns {
		if col.Writable {
			t.Fatalf("关联视图不得存在可写列: %+v", col)
		}
	}
}

// gridWriteProbeDelete 直接清理固定 ID 的探针行，不依赖编辑计划（清理必须总能执行）。
func gridWriteProbeDelete(t *testing.T, manager *Manager, source Source, password string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := manager.withSQL(ctx, source, func(ctx context.Context, db *sql.DB) error {
		_, execErr := db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s.employees WHERE id = %d`, labSchema, gridWriteProbeID))
		return execErr
	})
	if err != nil {
		t.Logf("清理探针行失败（可能账号无 DML 权限）: %s", redactIntegrationError(err, password))
	}
}

// gridWriteProbeReadName 回读探针行的 display_name；不存在时返回空串。
func gridWriteProbeReadName(t *testing.T, manager *Manager, source Source, password string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got := ""
	_, err := manager.StreamQuery(ctx, source, fmt.Sprintf(`SELECT display_name FROM %s.employees WHERE id = %d`, labSchema, gridWriteProbeID), 5, func(event StreamEvent) error {
		if event.Type == "rows" && len(event.Rows) > 0 && len(event.Rows[0]) > 0 && event.Rows[0][0] != nil {
			got = fmt.Sprint(event.Rows[0][0])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("回读失败: %s", redactIntegrationError(err, password))
	}
	return got
}
