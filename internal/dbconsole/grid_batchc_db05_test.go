package dbconsole

// Batch C / DB-05 失败优先回归：只有真正写进 WHERE 的物理定位列才可以跳过重复的
// original 原值比较；其他唯一键列、被 ROWID 定位覆盖的主键列都必须继续比较原值。
//
// 修复前实测：identity_policy=pk、真实主键 id、另有非空唯一列 email 时，
// 更新 email 生成 `UPDATE `app`.`accounts` SET `email` = ? WHERE `id` = ?`
// args: [new@example.test 1] —— 缺少 `AND email = old@example.test`，
// 两个客户端读到相同旧值时后提交者会静默覆盖先提交者的新值。

import (
	"strings"
	"testing"
)

// TestGridCBDb05UniqueColumnNotUsedAsLocatorKeepsOriginalCheck 唯一键列未被用作定位时必须比较原值。
func TestGridCBDb05UniqueColumnNotUsedAsLocatorKeepsOriginalCheck(t *testing.T) {
	fields := []Field{
		{Name: "ID", DataType: "bigint", PrimaryKey: true},
		{Name: "EMAIL", DataType: "varchar(120)"},
		{Name: "NICKNAME", DataType: "varchar(50)"},
	}
	columns := []Column{{Name: "ID", Database: "bigint"}, {Name: "EMAIL", Database: "varchar"}, {Name: "NICKNAME", Database: "varchar"}}
	plan := gridCPlanFor(t, KindMySQL, "SELECT ID, EMAIL, NICKNAME FROM APP.ACCOUNTS", "app", "accounts", fields, columns, false)
	plan.UniqueKeys = []string{"EMAIL"}
	plan.IdentityPolicy = "pk"
	plan.CanUpdate, plan.CanDelete = true, true

	mutation := gridCMutationFromJSON(t, `{
		"action": "update",
		"values": {"EMAIL": "new@example.test"},
		"original": {"ID": 1, "EMAIL": "old@example.test"},
		"key": {"ID": 1},
		"primary_key": ["ID"]
	}`)
	sqlText, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if !strings.Contains(sqlText, "`EMAIL` = ?") {
		t.Fatalf("未被用作定位的唯一列必须继续比较原值，实际 SQL=%s (args=%v)", sqlText, args)
	}
	wantSQL := "UPDATE `app`.`accounts` SET `EMAIL` = ? WHERE `ID` = ? AND `EMAIL` = ?"
	if sqlText != wantSQL {
		t.Fatalf("生成的 SQL 不符: got=%s want=%s", sqlText, wantSQL)
	}
	if !gridCArgsEqual(args, []any{"new@example.test", int64(1), "old@example.test"}) {
		t.Fatalf("参数不符: got=%v want=[new@example.test 1 old@example.test]", args)
	}
}

// TestGridCBDb05RowIDLocatorStillComparesModifiedPrimaryKey 采用 ROWID 定位时，
// locatorColumns 不含任何业务列，被修改的主键列也必须比较原值。
func TestGridCBDb05RowIDLocatorStillComparesModifiedPrimaryKey(t *testing.T) {
	plan := &ResultEditContext{
		Dialect:        KindOracle,
		Schema:         "SCOTT",
		Table:          "LOG_TABLE",
		IdentityPolicy: "oracle_rowid",
		PrimaryKeys:    []string{"ID"},
		CanUpdate:      true,
		CanDelete:      true,
		Columns: []GridColumnBinding{
			{Index: 0, ResultName: "ID", PhysicalName: "ID", DataType: "NUMBER", IsPrimaryKey: true, Writable: true},
			{Index: 1, ResultName: "MESSAGE", PhysicalName: "MESSAGE", DataType: "VARCHAR2(200)", Writable: true},
		},
	}
	mutation := gridCMutationFromJSON(t, `{
		"action": "update",
		"rowid": "AAASDMAABAAAL9DAAA",
		"use_rowid": true,
		"values": {"ID": 5},
		"original": {"ID": 1, "__KAIRO_EDIT_RID__": "AAASDMAABAAAL9DAAA"}
	}`)
	sqlText, args, err := BuildGridMutationSQLWithPlan(KindOracle, plan, mutation)
	if err != nil {
		t.Fatalf("ROWID 定位更新主键列失败: %v", err)
	}
	if strings.Count(sqlText, `"ID" =`) != 2 {
		t.Fatalf("ROWID 定位下被修改的主键列必须同时出现在 SET 与 WHERE 原值比较中，实际 SQL=%s (args=%v)", sqlText, args)
	}
	if !strings.Contains(sqlText, "ROWID = :kairo_p") {
		t.Fatalf("ROWID 必须仍是定位条件，实际 SQL=%s", sqlText)
	}
}

// TestGridCBDb05LocatorSnapshotMustAgree 同一请求不得携带两套互相矛盾的定位快照。
func TestGridCBDb05LocatorSnapshotMustAgree(t *testing.T) {
	fields := []Field{
		{Name: "ID", DataType: "bigint", PrimaryKey: true},
		{Name: "NAME", DataType: "varchar(50)"},
	}
	columns := []Column{{Name: "ID", Database: "bigint"}, {Name: "NAME", Database: "varchar"}}
	plan := gridCPlanFor(t, KindMySQL, "SELECT ID, NAME FROM APP.USERS", "app", "users", fields, columns, false)

	mutation := gridCMutationFromJSON(t, `{
		"action": "update",
		"values": {"NAME": "new"},
		"original": {"ID": 2, "NAME": "old"},
		"key": {"ID": 1},
		"primary_key": ["ID"]
	}`)
	sqlText, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
	if err == nil {
		t.Fatalf("定位原值与 original 快照不一致必须拒绝，实际 SQL=%s args=%v", sqlText, args)
	}
	if !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("错误信息应说明快照不一致，实际 %v", err)
	}
}

// TestGridCBDb05ActionNormalizedOnceForBothHalves action 必须在入口统一规范化，
// 否则带空格/大写的 action 会通过前半段分支却跳过后半段原值检查。
func TestGridCBDb05ActionNormalizedOnceForBothHalves(t *testing.T) {
	fields := []Field{
		{Name: "ID", DataType: "bigint", PrimaryKey: true},
		{Name: "NAME", DataType: "varchar(50)"},
	}
	columns := []Column{{Name: "ID", Database: "bigint"}, {Name: "NAME", Database: "varchar"}}
	plan := gridCPlanFor(t, KindMySQL, "SELECT ID, NAME FROM APP.USERS", "app", "users", fields, columns, false)

	mutation := gridCMutationFromJSON(t, `{
		"action": "  Update ",
		"values": {"NAME": "new"},
		"original": {"ID": 1},
		"key": {"ID": 1},
		"primary_key": ["ID"]
	}`)
	sqlText, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
	if err == nil {
		t.Fatalf("修改列缺少原值必须拒绝（action 规范化后仍应做并发检查），实际 SQL=%s args=%v", sqlText, args)
	}
	if !strings.Contains(err.Error(), "original") {
		t.Fatalf("错误信息应指向缺失原值，实际 %v", err)
	}
}

// TestGridCBDb05DeleteComparesProvidedSnapshot 删除同样比较用户提供的完整快照。
func TestGridCBDb05DeleteComparesProvidedSnapshot(t *testing.T) {
	fields := []Field{
		{Name: "ID", DataType: "bigint", PrimaryKey: true},
		{Name: "EMAIL", DataType: "varchar(120)"},
	}
	columns := []Column{{Name: "ID", Database: "bigint"}, {Name: "EMAIL", Database: "varchar"}}
	plan := gridCPlanFor(t, KindMySQL, "SELECT ID, EMAIL FROM APP.ACCOUNTS", "app", "accounts", fields, columns, false)
	plan.UniqueKeys = []string{"EMAIL"}

	mutation := gridCMutationFromJSON(t, `{
		"action": "delete",
		"original": {"ID": 1, "EMAIL": "old@example.test"},
		"key": {"ID": 1},
		"primary_key": ["ID"]
	}`)
	sqlText, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
	if err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if !strings.Contains(sqlText, "`EMAIL` = ?") {
		t.Fatalf("删除必须比较用户提供的快照，实际 SQL=%s args=%v", sqlText, args)
	}
}
