package dbconsole

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gridContractFixtureRelPath 真实 Go 序列化契约 fixture 相对仓库根的位置。
//
// QA-02: 前端契约测试不得再手写"新旧字段都填上"的 mock。这个文件由本测试从真实的
// ResultEditContext.ToSummary() 用 encoding/json 序列化生成, 前端测试直接消费它,
// 因此 Go 侧一旦改 json tag, fixture 校验会先红, 前端也会跟着红。
const gridContractFixtureRelPath = "tests/fixtures/grid-edit-plan-summary.json"

// contractFixtureCase 单个契约样例: 名称 + 计划 + 说明。
type contractFixtureCase struct {
	Name        string
	Description string
	Plan        *ResultEditContext
}

// contractFixtureCases 覆盖三类真实网格能力组合。
//
// 刻意覆盖:
//   - primary_key: 主键定位, 三项能力全开
//   - oracle_rowid: 隐藏 ROWID 定位, hidden_rowid_index 才出现
//   - read_only: 无可用定位键, 三项能力全关 + reason
func contractFixtureCases() []contractFixtureCase {
	return []contractFixtureCase{
		{
			Name:        "primary_key",
			Description: "普通带主键表: SELECT ID, NAME FROM APP.USERS, 主键定位",
			Plan: &ResultEditContext{
				ResultID: "res-contract-pk", SessionID: "sess-1",
				SourceID: "src-1", SourceFingerprint: "fp-1", Dialect: "mysql",
				ExecutedSQL: "SELECT ID, NAME, EMAIL FROM APP.USERS",
				Schema:      "APP", Table: "USERS",
				Columns: []GridColumnBinding{
					{Index: 0, ResultName: "ID", PhysicalName: "ID", DataType: "bigint", IsPrimaryKey: true, Writable: true},
					{Index: 1, ResultName: "NAME", PhysicalName: "NAME", DataType: "varchar", IsNullable: true, Writable: true},
					{Index: 2, ResultName: "EMAIL", PhysicalName: "EMAIL", DataType: "varchar", Writable: true},
				},
				PrimaryKeys: []string{"ID"}, UniqueKeys: []string{"EMAIL"},
				IdentityPolicy: "pk",
				CanInsert:      true, CanUpdate: true, CanDelete: true,
			},
		},
		{
			Name:        "oracle_rowid",
			Description: "无主键 Oracle 堆表: 用隐藏 ROWID 定位, hidden_rowid_index 指向行尾追加列",
			Plan: &ResultEditContext{
				ResultID: "res-contract-rid", SessionID: "sess-2",
				SourceID: "src-ora", SourceFingerprint: "fp-ora", Dialect: "oracle",
				ExecutedSQL:   `SELECT ID, NAME, ROWIDTOCHAR("LOG_TABLE".ROWID) AS "__KAIRO_EDIT_RID__" FROM LOG_TABLE`,
				Schema:        "SCOTT",
				Table:         "LOG_TABLE",
				Columns: []GridColumnBinding{
					{Index: 0, ResultName: "ID", PhysicalName: "ID", DataType: "NUMBER", Writable: true},
					{Index: 1, ResultName: "NAME", PhysicalName: "NAME", DataType: "VARCHAR2", IsNullable: true, Writable: true},
					{Index: 2, ResultName: "__KAIRO_EDIT_RID__", DataType: "VARCHAR2", ReadOnlyReason: "隐藏定位列"},
				},
				IdentityPolicy:   "oracle_rowid",
				HiddenRowIDIndex: 2,
				CanInsert:        true, CanUpdate: true, CanDelete: true,
			},
		},
		{
			Name:        "read_only",
			Description: "聚合/多表/缺少定位键: 只读, 三项能力全关并给出原因",
			Plan: &ResultEditContext{
				ResultID: "res-contract-ro", SessionID: "sess-3",
				SourceID: "src-1", SourceFingerprint: "fp-1", Dialect: "mysql",
				ExecutedSQL: "SELECT COUNT(*) AS CNT FROM APP.ORDERS",
				Schema:      "APP", Table: "ORDERS",
				Columns: []GridColumnBinding{
					{Index: 0, ResultName: "CNT", DataType: "bigint", ReadOnlyReason: "聚合表达式列"},
				},
				IdentityPolicy: "none",
				Reason:         "缺少主键或存在 JOIN",
			},
		},
	}
}

// renderGridContractFixture 生成 fixture 的确定性字节内容。
func renderGridContractFixture(t *testing.T) []byte {
	t.Helper()
	payload := struct {
		Comment     string                     `json:"_comment"`
		GeneratedBy string                     `json:"generated_by"`
		Note        string                     `json:"_note"`
		Cases       map[string]json.RawMessage `json:"cases"`
	}{
		Comment:     "真实 Go 序列化契约: 由 ResultEditContext.ToSummary() + encoding/json 生成, 请勿手工修改。",
		GeneratedBy: "internal/dbconsole/grid_contract_fixture_test.go",
		Note:        "重新生成: UPDATE_FIXTURES=1 go test ./internal/dbconsole/ -run TestGridEditPlanSummaryContractFixture",
		Cases:       map[string]json.RawMessage{},
	}
	for _, c := range contractFixtureCases() {
		raw, err := json.Marshal(c.Plan.ToSummary())
		if err != nil {
			t.Fatalf("序列化 %s 失败: %v", c.Name, err)
		}
		payload.Cases[c.Name] = raw
	}
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("生成 fixture 失败: %v", err)
	}
	return append(out, '\n')
}

// TestGridEditPlanSummaryContractFixture 校验 (必要时重新生成) 真实契约 fixture。
//
// 这是 QA-02 要求的"Go 生成真实序列化契约 fixture, 前端测试直接消费"的生成端:
// 默认模式只校验, 不一致就失败并提示重新生成的命令; 设置 UPDATE_FIXTURES=1 时写入。
func TestGridEditPlanSummaryContractFixture(t *testing.T) {
	want := renderGridContractFixture(t)
	path := filepath.Join(repoRootForFixture(t), filepath.FromSlash(gridContractFixtureRelPath))

	if os.Getenv("UPDATE_FIXTURES") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("创建 fixture 目录失败: %v", err)
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatalf("写入 fixture 失败: %v", err)
		}
		t.Logf("已重新生成 %s", gridContractFixtureRelPath)
		return
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v\n用 UPDATE_FIXTURES=1 go test ./internal/dbconsole/ -run TestGridEditPlanSummaryContractFixture 重新生成",
			gridContractFixtureRelPath, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s 与 Go 真实序列化不一致 —— 说明 Go 侧 json tag/字段变了, 前端契约必须同步。\n"+
			"重新生成: UPDATE_FIXTURES=1 go test ./internal/dbconsole/ -run TestGridEditPlanSummaryContractFixture",
			gridContractFixtureRelPath)
	}
}

// TestGridEditPlanSummaryWireShape 直接对真实序列化结果做线上形状断言。
//
// 目的: 让"前端读 camelCase"这类契约断链在 Go 侧就有一条明确的守卫 ——
// 线上字段只能是 snake_case, 且能力字段必须是真正的 JSON boolean。
func TestGridEditPlanSummaryWireShape(t *testing.T) {
	for _, c := range contractFixtureCases() {
		raw, err := json.Marshal(c.Plan.ToSummary())
		if err != nil {
			t.Fatalf("序列化 %s 失败: %v", c.Name, err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("反序列化 %s 失败: %v", c.Name, err)
		}

		for _, forbidden := range []string{"canUpdate", "canInsert", "canDelete", "resultId", "identityPolicy", "hiddenRowidIndex", "primaryKeys", "uniqueKeys"} {
			if _, ok := decoded[forbidden]; ok {
				t.Errorf("%s: 线上契约不得出现 camelCase 字段 %q (前端必须改读 snake_case)", c.Name, forbidden)
			}
		}
		for _, required := range []string{"result_id", "identity_policy", "can_update", "can_insert", "can_delete"} {
			if _, ok := decoded[required]; !ok {
				t.Errorf("%s: 线上契约缺少必需字段 %q", c.Name, required)
			}
		}
		for _, boolField := range []string{"can_update", "can_insert", "can_delete"} {
			value, ok := decoded[boolField]
			if !ok {
				continue
			}
			if _, isBool := value.(bool); !isBool {
				t.Errorf("%s: %s 必须是 JSON boolean, got %T (%v)", c.Name, boolField, value, value)
			}
		}
		if len(raw) == 0 || strings.Contains(string(raw), `"error"`) {
			t.Errorf("%s: 序列化结果异常: %s", c.Name, raw)
		}
	}
}

// repoRootForFixture 从测试工作目录向上找到仓库根 (含 go.mod)。
func repoRootForFixture(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd 失败: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("未能从 %s 向上找到含 go.mod 的仓库根", dir)
		}
		dir = parent
	}
}
