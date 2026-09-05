package dbconsole

import (
	"strings"
	"testing"
)

func studioSource(kind string) Source {
	return Source{ID: "studio-test", Kind: kind, AllowDDL: true, Environment: "development"}
}

func TestObjectStudioCapabilities(t *testing.T) {
	t.Parallel()
	oracle := ObjectStudioCapabilities(KindOracle)
	if !oracle["table"] || !oracle["sequence"] || !oracle["function_compile"] {
		t.Fatalf("Oracle capability map: %#v", oracle)
	}
	mysql := ObjectStudioCapabilities(KindMySQL)
	if !mysql["table"] || mysql["sequence"] || mysql["function_compile"] {
		t.Fatalf("MySQL capability map: %#v", mysql)
	}
}

func TestObjectStudioRejectsUnsafeValues(t *testing.T) {
	t.Parallel()
	source := studioSource(KindOracle)
	unsafe := []ObjectStudioChange{
		{Action: "create", ObjectType: "table", Schema: "APP", Name: "T;DROP TABLE X", Columns: []ObjectStudioColumn{{Name: "ID", DataType: "NUMBER"}}},
		{Action: "create", ObjectType: "table", Schema: "APP", Name: "T", Columns: []ObjectStudioColumn{{Name: "ID", DataType: "NUMBER", Default: "1); DROP TABLE X"}}},
		{Action: "create", ObjectType: "view", Schema: "APP", Name: "V", Definition: "SELECT 1; DROP TABLE X"},
		{Action: "drop", ObjectType: "table", Schema: "APP", Name: "T"},
	}
	for i, change := range unsafe {
		if err := ValidateObjectStudioChange(source, change); err == nil {
			t.Errorf("unsafe request %d was accepted", i)
		}
	}
	if err := ValidateObjectStudioChange(studioSource(KindMySQL), ObjectStudioChange{
		Action: "create", ObjectType: "table", Schema: "app", Name: "t", Columns: []ObjectStudioColumn{{Name: "id", DataType: "VARCHAR2(20)"}},
	}); err == nil || !strings.Contains(err.Error(), "MySQL") {
		t.Fatalf("vendor-specific type should be rejected: %v", err)
	}
}

func TestBuildObjectStudioTablePlan(t *testing.T) {
	t.Parallel()
	plan, err := BuildObjectStudioPlan(studioSource(KindOracle), ObjectStudioChange{
		Action: "create", ObjectType: "table", Schema: "APP", Name: "EMP",
		Columns: []ObjectStudioColumn{
			{Name: "ID", DataType: "NUMBER(10)", Nullable: boolPtr(false), PrimaryKey: true},
			{Name: "NAME", DataType: "VARCHAR2(80)", Default: "'unknown'", Comment: "employee name"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 2 {
		t.Fatalf("expected create + comment, got %#v", plan.Statements)
	}
	if !strings.Contains(plan.Statements[0], `CREATE TABLE "APP"."EMP"`) || !strings.Contains(plan.Statements[0], `PRIMARY KEY ("ID")`) {
		t.Fatalf("table SQL: %s", plan.Statements[0])
	}
	if !strings.Contains(plan.Statements[1], `COMMENT ON COLUMN "APP"."EMP"."NAME"`) {
		t.Fatalf("comment SQL: %s", plan.Statements[1])
	}
}

func TestObjectStudioProductionConfirmationAndDDLGate(t *testing.T) {
	t.Parallel()
	change := ObjectStudioChange{Action: "create", ObjectType: "table", Schema: "APP", Name: "EMP", Columns: []ObjectStudioColumn{{Name: "ID", DataType: "NUMBER"}}}
	if err := ValidateObjectStudioChange(studioSource(KindOracle), change); err != nil {
		t.Fatal(err)
	}
	// The gate is enforced at the manager boundary because a preview should
	// remain available to users even when execution is disabled.
	prod := studioSource(KindOracle)
	prod.Environment = "production"
	prod.AllowDDL = true
	if _, err := (&Manager{}).ApplyObjectStudio(t.Context(), prod, change); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("production apply without confirmation: %v", err)
	}
	readonly := studioSource(KindOracle)
	readonly.ReadOnly = true
	if _, err := (&Manager{}).ApplyObjectStudio(t.Context(), readonly, change); err == nil || !strings.Contains(err.Error(), "DDL") {
		t.Fatalf("read-only apply should fail before connecting: %v", err)
	}
}

func TestBuildObjectStudioAlterAndIndexPlans(t *testing.T) {
	t.Parallel()
	alter, err := BuildObjectStudioPlan(studioSource(KindOracle), ObjectStudioChange{
		Action: "alter", ObjectType: "table", Schema: "APP", Name: "EMP", Confirm: true,
		Changes: []ObjectStudioColumnChange{
			{Action: "add_column", Column: ObjectStudioColumn{Name: "CREATED_AT", DataType: "DATE"}},
			{Action: "rename_column", OldName: "NAME", Column: ObjectStudioColumn{Name: "FULL_NAME", DataType: "VARCHAR2(80)"}},
			{Action: "drop_column", OldName: "OLD_VALUE"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(alter.Statements) != 3 || !strings.Contains(alter.Statements[1], "RENAME COLUMN") || !strings.Contains(alter.Statements[2], "DROP COLUMN") {
		t.Fatalf("alter SQL: %#v", alter.Statements)
	}
	idx, err := BuildObjectStudioPlan(studioSource(KindMySQL), ObjectStudioChange{
		Action: "alter", ObjectType: "index", Schema: "app", Name: "ix_emp", Table: "emp", Confirm: true,
		Index: &ObjectStudioIndex{Name: "ix_emp", Table: "emp", Columns: []string{"id", "name"}, Unique: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Statements) != 2 || !strings.Contains(idx.Statements[0], "ALTER TABLE") || !strings.Contains(idx.Statements[1], "CREATE UNIQUE INDEX") {
		t.Fatalf("MySQL index rebuild SQL: %#v", idx.Statements)
	}
}

func TestBuildObjectStudioConstraintAndSequencePlans(t *testing.T) {
	t.Parallel()
	constraint, err := BuildObjectStudioPlan(studioSource(KindOracle), ObjectStudioChange{
		Action: "create", ObjectType: "constraint", Schema: "APP", Name: "FK_EMP_DEPT",
		Constraint: &ObjectStudioConstraint{
			Name: "FK_EMP_DEPT", Table: "EMP", Type: "foreign", Columns: []string{"DEPT_ID"},
			ReferencedSchema: "APP", ReferencedTable: "DEPT", ReferencedColumns: []string{"ID"}, OnDelete: "CASCADE",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(constraint.Statements[0], "FOREIGN KEY") || !strings.Contains(constraint.Statements[0], "ON DELETE CASCADE") {
		t.Fatalf("constraint SQL: %#v", constraint.Statements)
	}
	sequence, err := BuildObjectStudioPlan(studioSource(KindOracle), ObjectStudioChange{
		Action: "create", ObjectType: "sequence", Schema: "APP", Name: "SEQ_EMP",
		Sequence: &ObjectStudioSequence{Name: "SEQ_EMP", StartWith: 100, Increment: 5, Cache: 20, Cycle: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sequence.Statements[0], "START WITH 100") || !strings.Contains(sequence.Statements[0], "CYCLE") {
		t.Fatalf("sequence SQL: %#v", sequence.Statements)
	}
	if _, err := BuildObjectStudioPlan(studioSource(KindMySQL), ObjectStudioChange{Action: "create", ObjectType: "sequence", Schema: "app", Name: "s", Sequence: &ObjectStudioSequence{Name: "s"}}); err == nil {
		t.Fatal("MySQL sequence should be rejected")
	}
}

func TestBuildObjectStudioCheckConstraintWithoutColumns(t *testing.T) {
	t.Parallel()
	plan, err := BuildObjectStudioPlan(studioSource(KindOracle), ObjectStudioChange{
		Action: "create", ObjectType: "constraint", Schema: "APP", Name: "CK_EMP_ID",
		Constraint: &ObjectStudioConstraint{Name: "CK_EMP_ID", Table: "EMP", Type: "check", Expression: "ID > 0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 1 || !strings.Contains(plan.Statements[0], "CHECK (ID > 0)") {
		t.Fatalf("check constraint SQL: %#v", plan.Statements)
	}
}

func TestBuildObjectStudioMySQLConstraintDropSyntax(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{"primary", "DROP PRIMARY KEY"},
		{"unique", "DROP INDEX `UK_USERS_EMAIL`"},
		{"foreign", "DROP FOREIGN KEY `UK_USERS_EMAIL`"},
		{"check", "DROP CHECK `UK_USERS_EMAIL`"},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			plan, err := BuildObjectStudioPlan(studioSource(KindMySQL), ObjectStudioChange{
				Action: "drop", ObjectType: "constraint", Schema: "app", Name: "UK_USERS_EMAIL", Confirm: true,
				Constraint: &ObjectStudioConstraint{Name: "UK_USERS_EMAIL", Table: "users", Type: tt.kind, Columns: []string{"email"}, Expression: "email <> ''"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Statements) != 1 || !strings.Contains(plan.Statements[0], tt.want) {
				t.Fatalf("%s drop SQL = %#v, want %q", tt.kind, plan.Statements, tt.want)
			}
		})
	}
}

func TestBuildObjectStudioOracleDropNotNull(t *testing.T) {
	plan, err := BuildObjectStudioPlan(studioSource(KindOracle), ObjectStudioChange{
		Action: "drop", ObjectType: "constraint", Schema: "APP", Name: "NN_USERS_EMAIL", Confirm: true,
		Constraint: &ObjectStudioConstraint{Name: "NN_USERS_EMAIL", Table: "USERS", Type: "not_null", Columns: []string{"EMAIL"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 1 || !strings.Contains(plan.Statements[0], `MODIFY "EMAIL" NULL`) {
		t.Fatalf("drop NOT NULL SQL = %#v", plan.Statements)
	}
}

func TestNormalizeOracleFunctionSource(t *testing.T) {
	t.Parallel()
	change := OracleFunctionChange{
		Schema: "APP", Name: "GET_NAME", Replace: true,
		Source: "CREATE FUNCTION APP.GET_NAME(p_id NUMBER) RETURN VARCHAR2 IS\nBEGIN\n  RETURN 'ok';\nEND;\n/",
	}
	got, err := NormalizeOracleFunctionSource(change)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "CREATE OR REPLACE FUNCTION APP.GET_NAME") || strings.HasSuffix(strings.TrimSpace(got), "/") {
		t.Fatalf("normalized function source: %q", got)
	}
	quoted, err := NormalizeOracleFunctionSource(OracleFunctionChange{
		Schema: "APP", Name: "GET_NAME", Replace: true,
		Source: `CREATE OR REPLACE FUNCTION "APP"."GET_NAME"(p_id NUMBER) RETURN NUMBER IS BEGIN RETURN p_id; END;`,
	})
	if err != nil || !strings.Contains(quoted, `"APP"."GET_NAME"`) {
		t.Fatalf("quoted function identifiers should be accepted: %q, %v", quoted, err)
	}
	bad := []OracleFunctionChange{
		{Schema: "APP", Name: "GET_NAME", Source: "CREATE FUNCTION APP.OTHER RETURN NUMBER IS BEGIN RETURN 1; END;"},
		{Schema: "APP", Name: "GET_NAME", Source: "CREATE FUNCTION OTHER.GET_NAME RETURN NUMBER IS BEGIN RETURN 1; END;"},
		{Schema: "APP", Name: "GET_NAME", Source: "CREATE FUNCTION APP.GET_NAME RETURN NUMBER IS BEGIN RETURN 1; END;\n/\n/"},
		{Schema: "APP", Name: "GET_NAME", Source: "CREATE PROCEDURE APP.GET_NAME IS BEGIN NULL; END;"},
	}
	for i, item := range bad {
		if _, err := NormalizeOracleFunctionSource(item); err == nil {
			t.Errorf("invalid function source %d accepted", i)
		}
	}
}

func TestParseOracleCompileError(t *testing.T) {
	t.Parallel()
	item := parseOracleCompileError("APP", "F", "ORA-06550: line 12, column 9:\nPLS-00103: Encountered the symbol")
	if item.Line != 12 || item.Column != 9 || item.Position != 9 || item.ErrorNumber != 6550 {
		t.Fatalf("parsed diagnostic: %#v", item)
	}
}

func boolPtr(v bool) *bool { return &v }
