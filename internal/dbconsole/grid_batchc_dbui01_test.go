package dbconsole

// Batch C / DBUI-01 失败优先回归（纯函数 + SQL/参数构造）。
//
// 这里覆盖评审文档给的三条硬性行为：
//   1. 列别名互换 `SELECT SALARY AS ID, ID AS SALARY` 必须整体只读，
//      且绝不能生成 UPDATE（否则 id=1 的行会被当成 id=100 的行）。
//   2. `SELECT ID AS K, NAME AS LABEL` 之类的“干净别名”必须仍能正确写入物理列，
//      values / original / key 三者一起按结果列名映射。
//   3. 聚合/表达式投影不得放开插入（“允许插入”不等于“允许更新”，反之亦然）。
//
// 另外两条断言直接针对线上 JSON：能力字段必须是真正的 boolean，且摘要必须带
// 按结果索引的列绑定摘要 columns。为了在修复前也能编译，线上形状一律用
// encoding/json 编解码，而不是直接引用新增的结构体字段。

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// gridCPlanFor 用纯分析函数（不申请连接、不访问数据库）构造编辑计划。
// heap 仅在 Oracle 下有意义，用于模拟“已确认是普通堆表”。
func gridCPlanFor(t *testing.T, kind, sql, schema, table string, fields []Field, resultColumns []Column, heap bool) *ResultEditContext {
	t.Helper()
	parsed, err := parseGridQuerySyntax(kind, sql)
	if err != nil {
		t.Fatalf("parseGridQuerySyntax(%q) error = %v", sql, err)
	}
	prep := &gridQueryPreparation{Parsed: parsed, Schema: schema, Table: table, Fields: fields}
	if kind == KindOracle {
		prep.HeapKnown, prep.HeapTable = true, heap
	}
	plan := AnalyzeGridQueryWithMetadata(Source{ID: "src-c", Kind: kind, Database: schema}, "tab-c", sql, resultColumns, prep, -1)
	if plan == nil {
		t.Fatalf("AnalyzeGridQueryWithMetadata(%q) 返回 nil", sql)
	}
	return plan
}

// gridCMutationFromJSON 按服务端真实解码方式（UseNumber）解析前端提交体，
// 这样新增协议字段在修复前只会表现为“字段被忽略”，而不是编译失败。
func gridCMutationFromJSON(t *testing.T, raw string) GridMutation {
	t.Helper()
	var mutation GridMutation
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&mutation); err != nil {
		t.Fatalf("解析 mutation JSON 失败: %v (raw=%s)", err, raw)
	}
	return mutation
}

// gridCArgsEqual 逐项比较绑定参数，避免 JSON 数字与 Go 数字类型误报。
func gridCArgsEqual(got []any, want []any) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if gridCArgString(got[i]) != gridCArgString(want[i]) {
			return false
		}
	}
	return true
}

// gridCArgValue 取出绑定参数的实际值（Oracle 路径使用 sql.NamedArg 包装）。
func gridCArgValue(value any) any {
	if named, ok := value.(sql.NamedArg); ok {
		return named.Value
	}
	return value
}

func gridCArgString(value any) string {
	switch v := gridCArgValue(value).(type) {
	case nil:
		return "<nil>"
	case string:
		return v
	case json.Number:
		return string(v)
	case []byte:
		return string(v)
	default:
		return jsonScalarString(v)
	}
}

func jsonScalarString(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

// TestGridCBDbui01CrossAliasIsReadOnlyAndProducesNoDangerousSQL 覆盖文档里最危险的场景。
//
// 修复前实测（本测试在修复前失败并打印真实 SQL/args）：
//
//	UPDATE `app`.`accounts` SET `salary` = ? WHERE `id` = ? AND `salary` = ?
//	-- args: [2 100 1]
//
// 原始查询行是 id=1、salary=100；生成 SQL 却指向 id=100。若表中恰好存在
// id=100、salary=1 的行，RowsAffected 仍是 1，无法靠影响行数发现错行。
func TestGridCBDbui01CrossAliasIsReadOnlyAndProducesNoDangerousSQL(t *testing.T) {
	fields := []Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "SALARY", DataType: "NUMBER"},
	}
	columns := []Column{{Name: "ID", Database: "NUMBER"}, {Name: "SALARY", Database: "NUMBER"}}
	plan := gridCPlanFor(t, KindMySQL, "SELECT SALARY AS ID, ID AS SALARY FROM ACCOUNTS", "app", "accounts", fields, columns, false)

	if plan.IdentityPolicy == "pk" && (plan.CanUpdate || plan.CanDelete) {
		t.Fatalf("交叉别名必须整体只读，实际 identity_policy=%s can_update=%v can_delete=%v",
			plan.IdentityPolicy, plan.CanUpdate, plan.CanDelete)
	}
	if plan.CanUpdate || plan.CanDelete {
		t.Fatalf("交叉别名必须整体只读，实际 can_update=%v can_delete=%v reason=%q",
			plan.CanUpdate, plan.CanDelete, plan.Reason)
	}
	if plan.Reason == "" {
		t.Fatalf("只读计划必须给出原因 (DBUI-01: 让用户知道为什么不能改)")
	}
	for _, binding := range plan.Columns {
		if binding.Writable {
			t.Fatalf("交叉别名下不得有可写列: %+v", binding)
		}
		if binding.ReadOnlyReason == "" {
			t.Fatalf("只读列必须给出 read_only_reason: %+v", binding)
		}
	}

	// 用评审文档里的真实前端负载提交，必须被拒绝，且不得生成任何 SQL/参数。
	mutation := gridCMutationFromJSON(t, `{
		"action": "update",
		"values": {"ID": 2},
		"original": {"ID": 100, "SALARY": 1},
		"key": {"ID": 100, "SALARY": 1},
		"primary_key": ["ID"]
	}`)
	sqlText, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
	if err == nil {
		t.Fatalf("交叉别名提交必须被拒绝，实际生成 SQL=%s args=%v（危险：可能更新另一行）", sqlText, args)
	}
	if sqlText != "" || len(args) != 0 {
		t.Fatalf("被拒绝的提交不得吐出 SQL/参数，实际 SQL=%q args=%v", sqlText, args)
	}
}

// TestGridCBDbui01CleanAliasMapsToPhysicalColumn 覆盖文档验收里的 `SELECT ID AS K, NAME AS LABEL`。
// values / original / key 三者必须一起按结果列名映射到物理列。
func TestGridCBDbui01CleanAliasMapsToPhysicalColumn(t *testing.T) {
	fields := []Field{
		{Name: "ID", DataType: "bigint", PrimaryKey: true},
		{Name: "NAME", DataType: "varchar(50)"},
	}
	columns := []Column{{Name: "K", Database: "bigint"}, {Name: "LABEL", Database: "varchar"}}
	plan := gridCPlanFor(t, KindMySQL, "SELECT ID AS K, NAME AS LABEL FROM APP.USERS", "app", "users", fields, columns, false)

	if !plan.CanUpdate {
		t.Fatalf("干净别名必须仍可更新，实际 can_update=false reason=%q", plan.Reason)
	}
	if plan.IdentityPolicy != "pk" {
		t.Fatalf("干净别名下主键定位应保持可用，实际 policy=%s", plan.IdentityPolicy)
	}

	var nameBinding, idBinding GridColumnBinding
	for _, binding := range plan.Columns {
		switch binding.Index {
		case 0:
			idBinding = binding
		case 1:
			nameBinding = binding
		}
	}
	if idBinding.PhysicalName != "ID" || nameBinding.PhysicalName != "NAME" {
		t.Fatalf("列绑定必须给出物理列映射，实际 index0=%+v index1=%+v", idBinding, nameBinding)
	}
	if !nameBinding.Writable {
		t.Fatalf("别名后的普通列应可写: %+v", nameBinding)
	}

	mutation := gridCMutationFromJSON(t, `{
		"action": "update",
		"values": {"LABEL": "new"},
		"original": {"K": 42, "LABEL": "old"},
		"key": {"K": 42},
		"primary_key": ["ID"]
	}`)
	sqlText, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
	if err != nil {
		t.Fatalf("干净别名提交失败: %v", err)
	}
	if !strings.Contains(sqlText, "SET `NAME` = ?") {
		t.Fatalf("SET 必须落在物理列 NAME 上，实际 SQL=%s", sqlText)
	}
	if !strings.Contains(sqlText, "WHERE `ID` = ? AND `NAME` = ?") {
		t.Fatalf("定位必须落在物理主键 ID 上并比较 NAME 原值，实际 SQL=%s", sqlText)
	}
	if !gridCArgsEqual(args, []any{"new", int64(42), "old"}) {
		t.Fatalf("参数顺序应为 [新值, 主键, 原值]，实际 args=%v", args)
	}
}

// TestGridCBDbui01IndexProtocolResolvesPhysicalColumn 覆盖文档推荐的“结果列索引 + 值”协议：
// 服务器凭 result_id 的不可变 ResultEditContext.Columns 解析物理列，页面显示名不参与推断。
func TestGridCBDbui01IndexProtocolResolvesPhysicalColumn(t *testing.T) {
	fields := []Field{
		{Name: "ID", DataType: "bigint", PrimaryKey: true},
		{Name: "NAME", DataType: "varchar(50)"},
	}
	columns := []Column{{Name: "K", Database: "bigint"}, {Name: "LABEL", Database: "varchar"}}
	plan := gridCPlanFor(t, KindMySQL, "SELECT ID AS K, NAME AS LABEL FROM APP.USERS", "app", "users", fields, columns, false)

	mutation := gridCMutationFromJSON(t, `{
		"action": "update",
		"row_columns": [
			{"column_index": 0, "has_value": true, "value": 42},
			{"column_index": 1, "has_value": true, "value": "old"}
		],
		"changes": [
			{"column_index": 1, "has_value": true, "value": "new"}
		]
	}`)
	sqlText, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
	if err != nil {
		t.Fatalf("索引协议提交失败: %v", err)
	}
	if !strings.Contains(sqlText, "SET `NAME` = ?") || !strings.Contains(sqlText, "WHERE `ID` = ? AND `NAME` = ?") {
		t.Fatalf("索引协议必须解析出物理列与物理主键，实际 SQL=%s", sqlText)
	}
	if !gridCArgsEqual(args, []any{"new", int64(42), "old"}) {
		t.Fatalf("索引协议参数应为 [新值, 主键, 原值]，实际 args=%v", args)
	}
}

// TestGridCBDbui01IndexProtocolRejectsDuplicatePhysicalSnapshot 同一物理列在快照里出现两次且值不同时，
// 无法判断原值语义，必须拒绝而不是猜一个。
func TestGridCBDbui01IndexProtocolRejectsDuplicatePhysicalSnapshot(t *testing.T) {
	fields := []Field{
		{Name: "ID", DataType: "bigint", PrimaryKey: true},
		{Name: "NAME", DataType: "varchar(50)"},
	}
	columns := []Column{{Name: "ID", Database: "bigint"}, {Name: "NAME", Database: "varchar"}, {Name: "NAME", Database: "varchar"}}
	plan := gridCPlanFor(t, KindMySQL, "SELECT ID, NAME, NAME FROM APP.USERS", "app", "users", fields, columns, false)

	mutation := gridCMutationFromJSON(t, `{
		"action": "update",
		"row_columns": [
			{"column_index": 0, "has_value": true, "value": 42},
			{"column_index": 1, "has_value": true, "value": "old"},
			{"column_index": 2, "has_value": true, "value": "another"}
		],
		"changes": [
			{"column_index": 1, "has_value": true, "value": "new"}
		]
	}`)
	sqlText, _, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
	if err == nil {
		t.Fatalf("同一物理列出现两个不同快照值必须拒绝，实际 SQL=%s", sqlText)
	}
}

// TestGridCBDbui01DuplicateAliasIsReadOnly 重复结果列名无法用名字协议区分目标列，必须只读。
func TestGridCBDbui01DuplicateAliasIsReadOnly(t *testing.T) {
	fields := []Field{
		{Name: "ID", DataType: "bigint", PrimaryKey: true},
		{Name: "NAME", DataType: "varchar(50)"},
	}
	columns := []Column{{Name: "X", Database: "bigint"}, {Name: "X", Database: "varchar"}}
	plan := gridCPlanFor(t, KindMySQL, "SELECT ID AS X, NAME AS X FROM APP.USERS", "app", "users", fields, columns, false)

	if plan.CanUpdate || plan.CanDelete {
		t.Fatalf("重复别名必须只读，实际 can_update=%v can_delete=%v reason=%q", plan.CanUpdate, plan.CanDelete, plan.Reason)
	}
	for _, binding := range plan.Columns {
		if binding.Writable {
			t.Fatalf("重复别名下不得有可写列: %+v", binding)
		}
	}
}

// TestGridCBDbui01AggregateProjectionDisablesInsert 聚合/表达式投影不得放开插入能力。
// “允许插入”不能由“单表可解析”推导，否则聚合结果集会被 UI 当成可新增行的表格。
func TestGridCBDbui01AggregateProjectionDisablesInsert(t *testing.T) {
	fields := []Field{
		{Name: "ID", DataType: "bigint", PrimaryKey: true},
		{Name: "TOTAL", DataType: "decimal(18,2)"},
	}
	columns := []Column{{Name: "CNT", Database: "bigint"}}
	plan := gridCPlanFor(t, KindMySQL, "SELECT COUNT(*) AS CNT FROM APP.ORDERS", "app", "orders", fields, columns, false)

	if plan.CanInsert {
		t.Fatalf("聚合投影不得放开插入，实际 can_insert=true reason=%q", plan.Reason)
	}
	if plan.CanUpdate || plan.CanDelete {
		t.Fatalf("聚合投影不得放开更新/删除，实际 update=%v delete=%v", plan.CanUpdate, plan.CanDelete)
	}
	if len(plan.Columns) != 1 || plan.Columns[0].Writable {
		t.Fatalf("聚合表达式列必须只读并给出原因: %+v", plan.Columns)
	}
	if plan.Columns[0].ReadOnlyReason == "" {
		t.Fatalf("聚合表达式列必须给出 read_only_reason: %+v", plan.Columns[0])
	}
}

// TestGridCBDbui01SummaryCarriesColumnBindingsAndStrictBooleans 直接对线上 JSON 断言：
// 能力字段必须是真正的 boolean，且摘要必须携带按结果索引的只读列绑定摘要。
func TestGridCBDbui01SummaryCarriesColumnBindingsAndStrictBooleans(t *testing.T) {
	fields := []Field{
		{Name: "ID", DataType: "bigint", PrimaryKey: true},
		{Name: "NAME", DataType: "varchar(50)"},
	}
	columns := []Column{{Name: "K", Database: "bigint"}, {Name: "LABEL", Database: "varchar"}}
	plan := gridCPlanFor(t, KindMySQL, "SELECT ID AS K, NAME AS LABEL FROM APP.USERS", "app", "users", fields, columns, false)
	plan.CanInsert = true

	raw, err := json.Marshal(plan.ToSummary())
	if err != nil {
		t.Fatalf("序列化摘要失败: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("反序列化摘要失败: %v", err)
	}
	for _, field := range []string{"can_insert", "can_update", "can_delete"} {
		if _, ok := decoded[field].(bool); !ok {
			t.Fatalf("%s 必须是 JSON boolean，实际 %T (%v)", field, decoded[field], decoded[field])
		}
	}
	rawColumns, ok := decoded["columns"]
	if !ok {
		t.Fatalf("摘要必须携带 columns 列绑定（DBUI-01），实际 keys=%v", gridCSortedKeys(decoded))
	}
	entries, ok := rawColumns.([]any)
	if !ok || len(entries) != len(columns) {
		t.Fatalf("columns 必须是长度等于结果列数的数组，实际 %v", rawColumns)
	}
	first, _ := entries[0].(map[string]any)
	if first["index"] != float64(0) || first["result_name"] != "K" || first["physical_name"] != "ID" {
		t.Fatalf("第一列绑定应为 index=0 result_name=K physical_name=ID，实际 %v", first)
	}
	if writable, ok := first["writable"].(bool); !ok || !writable {
		t.Fatalf("干净别名列必须 writable=true，实际 %v", first["writable"])
	}
}

func gridCSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// TestGridCBDbui01BuilderRejectsCrossAliasEvenIfPlanSaysWritable 纵深防御：
// 即使有人绕过了分析器（手工构造计划、旧缓存、未来改动）把交叉别名计划标成可更新，
// 写构造器本身也必须拒绝，绝不生成指向另一行的 UPDATE。
//
// 这正是评审文档给出的危险负载（`SELECT SALARY AS ID, ID AS SALARY FROM ACCOUNTS`）：
// 若按名称协议解释，会得到 `UPDATE app.accounts SET salary = ? WHERE id = ? AND salary = ?`
// 与 args [2,100,1]（原始行是 id=1、salary=100）。
func TestGridCBDbui01BuilderRejectsCrossAliasEvenIfPlanSaysWritable(t *testing.T) {
	plan := &ResultEditContext{
		Dialect:        KindMySQL,
		Schema:         "app",
		Table:          "accounts",
		IdentityPolicy: "pk",
		PrimaryKeys:    []string{"ID"},
		CanUpdate:      true,
		CanDelete:      true,
		Columns: []GridColumnBinding{
			{Index: 0, ResultName: "ID", PhysicalName: "SALARY", DataType: "NUMBER", Writable: true},
			{Index: 1, ResultName: "SALARY", PhysicalName: "ID", DataType: "NUMBER", IsPrimaryKey: true, Writable: true},
		},
	}
	mutation := gridCMutationFromJSON(t, `{
		"action": "update",
		"values": {"ID": 2},
		"original": {"ID": 100, "SALARY": 1},
		"key": {"ID": 100, "SALARY": 1},
		"primary_key": ["ID"]
	}`)
	sqlText, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
	if err == nil {
		t.Fatalf("交叉别名即使计划声称可写也必须拒绝，实际 SQL=%s args=%v（危险：可能更新另一行）", sqlText, args)
	}
	if sqlText != "" || len(args) != 0 {
		t.Fatalf("被拒绝的提交不得吐出 SQL/参数，实际 SQL=%q args=%v", sqlText, args)
	}
}

// TestGridCBDbui01ServerRejectsWritingReadOnlyColumn 服务器是可写性的最终权威：
// LOB 列、行尾身份列和未投影列都不能通过名称协议或索引协议写入。
func TestGridCBDbui01ServerRejectsWritingReadOnlyColumn(t *testing.T) {
	plan := &ResultEditContext{
		Dialect:          KindOracle,
		Schema:           "SCOTT",
		Table:            "LOG_TABLE",
		IdentityPolicy:   "oracle_rowid",
		HiddenRowIDIndex: 2,
		CanUpdate:        true,
		CanDelete:        true,
		Columns: []GridColumnBinding{
			{Index: 0, ResultName: "ID", PhysicalName: "ID", DataType: "NUMBER", Writable: true},
			{Index: 1, ResultName: "BODY", PhysicalName: "BODY", DataType: "CLOB", Writable: false, ReadOnlyReason: "LOB/长文本字段不支持直接网格内嵌编辑"},
			{Index: 2, ResultName: "__KAIRO_EDIT_RID__", PhysicalName: "ROWID", DataType: "VARCHAR2", Writable: false, ReadOnlyReason: "行尾隐藏定位列"},
		},
	}
	cases := []struct {
		name string
		raw  string
	}{
		{"lob column", `{"action":"update","values":{"BODY":"x"},"original":{"ID":1,"BODY":"y"},"key":{"ID":1}}`},
		{"hidden rowid column", `{"action":"update","values":{"__KAIRO_EDIT_RID__":"AAA"},"original":{"ID":1,"__KAIRO_EDIT_RID__":"AAA"},"key":{"ID":1}}`},
		{"unprojected column", `{"action":"update","values":{"SECRET":"x"},"original":{"ID":1},"key":{"ID":1}}`},
		{"write via index protocol", `{"action":"update","row_columns":[{"column_index":0,"has_value":true,"value":1}],"changes":[{"column_index":1,"has_value":true,"value":"x"}]}`},
	}
	for _, tc := range cases {
		sqlText, args, err := BuildGridMutationSQLWithPlan(KindOracle, plan, gridCMutationFromJSON(t, tc.raw))
		if err == nil {
			t.Fatalf("%s 必须被服务器拒绝，实际生成 SQL=%s args=%v", tc.name, sqlText, args)
		}
		if sqlText != "" || len(args) != 0 {
			t.Fatalf("%s 被拒绝时不得吐出 SQL/参数，实际 SQL=%q args=%v", tc.name, sqlText, args)
		}
	}
}
