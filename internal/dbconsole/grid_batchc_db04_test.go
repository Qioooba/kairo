package dbconsole

// Batch C / DB-04 失败优先回归：参数绑定必须依据“声明的物理字段类型”，
// 不允许再按字符串外形猜测日期。
//
// 修复前实测：向声明为 VARCHAR2 的 MESSAGE 写入字面文本 `2026-09-22 12:34:56`，
// 得到 sql.NamedArg.Value 的类型是 time.Time；`2026/09/22 12:34:56` 会被替换成
// `-` 之后解析，文本字段的值与类型都被擅自改动。

import (
	"encoding/json"
	"testing"
	"time"
)

// gridCDb04TextPlan 构造一个 MESSAGE VARCHAR2(200) + CREATED_AT DATE + AMOUNT NUMBER 的可写计划。
func gridCDb04TextPlan(t *testing.T) *ResultEditContext {
	t.Helper()
	fields := []Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "MESSAGE", DataType: "VARCHAR2(200)", Nullable: true},
		{Name: "CREATED_AT", DataType: "DATE", Nullable: true},
		{Name: "AMOUNT", DataType: "NUMBER(20,6)", Nullable: true},
	}
	columns := []Column{
		{Name: "ID", Database: "NUMBER"},
		{Name: "MESSAGE", Database: "VARCHAR2"},
		{Name: "CREATED_AT", Database: "DATE"},
		{Name: "AMOUNT", Database: "NUMBER"},
	}
	return gridCPlanFor(t, KindMySQL, "SELECT ID, MESSAGE, CREATED_AT, AMOUNT FROM APP.LOGS", "app", "logs", fields, columns, false)
}

// TestGridCBDb04Varchar2KeepsLiteralText 文本列的日期样式字面量必须原样绑定。
func TestGridCBDb04Varchar2KeepsLiteralText(t *testing.T) {
	plan := gridCDb04TextPlan(t)
	cases := []string{
		"2026-09-22 12:34:56",
		"2026/09/22 12:34:56",
		"2026-09-22T12:34:56+08:00",
		"2026-09-22",
	}
	for _, literal := range cases {
		mutation := gridCMutationFromJSON(t, `{"action":"update","values":{"MESSAGE":`+jsonQuote(literal)+`},"original":{"ID":1,"MESSAGE":"old"}}`)
		_, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
		if err != nil {
			t.Fatalf("字面量 %q 更新失败: %v", literal, err)
		}
		if len(args) == 0 {
			t.Fatalf("字面量 %q 未生成绑定参数", literal)
		}
		got, ok := args[0].(string)
		if !ok {
			t.Fatalf("VARCHAR2 文本列 %q 被改成了 %T (%v)，必须以字符串原样绑定", literal, args[0], args[0])
		}
		if got != literal {
			t.Fatalf("VARCHAR2 文本列 %q 被改写为 %q，用户输入不得被静默调整", literal, got)
		}
	}
}

// TestGridCBDb04DateColumnParsesWithoutMachineTimezone 只有 DATE/TIMESTAMP 才解析日期，
// 且“无时区”字面量必须与机器默认时区无关（这里用固定 UTC 承载，墙钟时间不变）。
func TestGridCBDb04DateColumnParsesWithoutMachineTimezone(t *testing.T) {
	plan := gridCDb04TextPlan(t)
	for _, literal := range []string{"2026-09-22 12:34:56", "2026/09/22 12:34:56", "2026-09-22 12:34:56.123456"} {
		mutation := gridCMutationFromJSON(t, `{"action":"update","values":{"CREATED_AT":`+jsonQuote(literal)+`},"original":{"ID":1,"CREATED_AT":"2000-01-01 00:00:00"}}`)
		_, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
		if err != nil {
			t.Fatalf("DATE 列 %q 更新失败: %v", literal, err)
		}
		got, ok := args[0].(time.Time)
		if !ok {
			t.Fatalf("DATE 列 %q 必须解析为 time.Time，实际 %T (%v)", literal, args[0], args[0])
		}
		if got.Year() != 2026 || got.Month() != time.September || got.Day() != 22 ||
			got.Hour() != 12 || got.Minute() != 34 || got.Second() != 56 {
			t.Fatalf("DATE 列 %q 被机器默认时区改写: %v (location=%v)", literal, got, got.Location())
		}
	}

	// 带 offset 的值必须保留 offset 表达的时刻。
	mutation := gridCMutationFromJSON(t, `{"action":"update","values":{"CREATED_AT":"2026-09-22T12:34:56+08:00"},"original":{"ID":1,"CREATED_AT":"2000-01-01 00:00:00"}}`)
	_, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
	if err != nil {
		t.Fatalf("带 offset 的 DATE 值更新失败: %v", err)
	}
	got, ok := args[0].(time.Time)
	if !ok {
		t.Fatalf("带 offset 的值必须解析为 time.Time，实际 %T", args[0])
	}
	want, _ := time.Parse(time.RFC3339, "2026-09-22T12:34:56+08:00")
	if !got.Equal(want) {
		t.Fatalf("带 offset 的时刻被改写: got=%v want=%v", got, want)
	}
}

// TestGridCBDb04NumberKeepsDecimalPrecision json.Number 不得经 float64 中转。
func TestGridCBDb04NumberKeepsDecimalPrecision(t *testing.T) {
	plan := gridCDb04TextPlan(t)
	const precise = "12345678901234567890.123456789"
	mutation := gridCMutationFromJSON(t, `{"action":"update","values":{"AMOUNT":`+precise+`},"original":{"ID":1,"AMOUNT":0}}`)
	_, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
	if err != nil {
		t.Fatalf("NUMBER 列更新失败: %v", err)
	}
	number, ok := args[0].(json.Number)
	if !ok {
		t.Fatalf("json.Number 必须原样保留（不得经 float64 中转），实际 %T (%v)", args[0], args[0])
	}
	if string(number) != precise {
		t.Fatalf("十进制精度被破坏: got=%s want=%s", number, precise)
	}
}

// TestGridCBDb04InsertBindsByFieldMetadata INSERT 也必须按服务端字段元数据绑定类型，
// 不能因为新行没有结果列绑定就退回字符串外形推断。
func TestGridCBDb04InsertBindsByFieldMetadata(t *testing.T) {
	plan := gridCDb04TextPlan(t)
	plan.CanInsert = true
	mutation := gridCMutationFromJSON(t, `{
		"action": "insert",
		"values": {
			"MESSAGE": "2026-09-22 12:34:56",
			"CREATED_AT": "2026-09-22 12:34:56"
		}
	}`)
	_, args, err := BuildGridMutationSQLWithPlan(KindMySQL, plan, mutation)
	if err != nil {
		t.Fatalf("INSERT 失败: %v", err)
	}
	if len(args) != 2 {
		t.Fatalf("INSERT 参数数量错误: %v", args)
	}
	// 列顺序按名字排序: CREATED_AT, MESSAGE
	if _, ok := args[0].(time.Time); !ok {
		t.Fatalf("DATE 字段 CREATED_AT 必须按元数据类型解析为 time.Time，实际 %T", args[0])
	}
	text, ok := args[1].(string)
	if !ok || text != "2026-09-22 12:34:56" {
		t.Fatalf("VARCHAR2 字段 MESSAGE 必须保持字符串，实际 %T (%v)", args[1], args[1])
	}
}

// TestGridCBDb04TimestampColumnParsesFractionalSeconds TIMESTAMP 也要按声明类型解析，
// 并保留秒的小数精度。
func TestGridCBDb04TimestampColumnParsesFractionalSeconds(t *testing.T) {
	fields := []Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "TS", DataType: "TIMESTAMP(6)", Nullable: true},
	}
	columns := []Column{{Name: "ID", Database: "NUMBER"}, {Name: "TS", Database: "TIMESTAMP"}}
	plan := gridCPlanFor(t, KindOracle, "SELECT ID, TS FROM SCOTT.EVENTS", "SCOTT", "EVENTS", fields, columns, true)

	for _, tc := range []struct {
		literal string
		nanos   int
	}{
		{"2026-09-22 12:34:56.123456", 123456000},
		{"2026-09-22 12:34:56", 0},
		{"2026-09-22T12:34:56.5+08:00", 500000000},
	} {
		mutation := gridCMutationFromJSON(t, `{"action":"update","values":{"TS":`+jsonQuote(tc.literal)+`},"original":{"ID":1,"TS":"2000-01-01 00:00:00"}}`)
		_, args, err := BuildGridMutationSQLWithPlan(KindOracle, plan, mutation)
		if err != nil {
			t.Fatalf("TIMESTAMP %q 更新失败: %v", tc.literal, err)
		}
		got, ok := gridCArgValue(args[0]).(time.Time)
		if !ok {
			t.Fatalf("TIMESTAMP %q 必须解析为 time.Time，实际 %T", tc.literal, gridCArgValue(args[0]))
		}
		if got.Nanosecond() != tc.nanos {
			t.Fatalf("TIMESTAMP %q 的秒小数被破坏: got=%v (%d ns) want=%d ns", tc.literal, got, got.Nanosecond(), tc.nanos)
		}
	}
}

func jsonQuote(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
