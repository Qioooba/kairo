package dbconsole

import (
	"database/sql"
	"strings"
	"testing"
)

func TestBuildGridMutationSQLRequiresKeyAndUsesParameters(t *testing.T) {
	if _, _, err := BuildGridMutationSQL(KindMySQL, "app", "users", GridMutation{Action: "delete"}); err == nil {
		t.Fatal("delete without PK/ROWID must be rejected")
	}
	sqlText, args, err := BuildGridMutationSQL(KindMySQL, "app", "users", GridMutation{
		Action: "update", Values: map[string]any{"name": "new"}, Original: map[string]any{"id": 7, "name": "old"},
		Key: map[string]any{"id": 7}, PrimaryKey: []string{"id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sqlText, "new") || !strings.Contains(sqlText, "`id` = ?") || len(args) != 3 {
		t.Fatalf("unsafe or unexpected grid SQL: %q %#v", sqlText, args)
	}
	oracleSQL, oracleArgs, err := BuildGridMutationSQL(KindOracle, "APP", "USERS", GridMutation{
		Action: "delete", PrimaryKey: []string{"ID"}, Key: map[string]any{"ID": int64(3)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(oracleSQL, `"APP"."USERS"`) || !strings.Contains(oracleSQL, `"ID" = :kairo_p1`) || len(oracleArgs) != 1 {
		t.Fatalf("unexpected Oracle grid SQL: %q %#v", oracleSQL, oracleArgs)
	}
}

func TestGridRowIDIncludesOriginalSnapshot(t *testing.T) {
	query, args, err := BuildGridMutationSQL(KindOracle, "APP", "USERS", GridMutation{
		Action: "update", UseRowID: true, RowID: "AAAbbb", Values: map[string]any{"NAME": "new"},
		Original: map[string]any{"NAME": "old", "NOTE": nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, `ROWID = :kairo_p2 AND "NAME" = :kairo_p3 AND "NOTE" IS NULL`) || len(args) != 3 || args[2].(sql.NamedArg).Value != "old" {
		t.Fatalf("ROWID bypassed optimistic locking: %s %#v", query, args)
	}
}

func TestBuildGridMutationSQLOracleRowID(t *testing.T) {
	sqlText, args, err := BuildGridMutationSQL(KindOracle, "APP", "USERS", GridMutation{Action: "delete", UseRowID: true, RowID: "AAAbbb"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sqlText, "ROWID = :kairo_p1") || len(args) != 1 {
		t.Fatalf("unexpected ROWID SQL: %q %#v", sqlText, args)
	}
	if _, _, err := BuildGridMutationSQL(KindMySQL, "app", "users", GridMutation{Action: "delete", UseRowID: true, RowID: "x"}); err == nil {
		t.Fatal("MySQL ROWID must be rejected")
	}
}

func TestBuildGridMutationSQLPlaceholderSchema(t *testing.T) {
	for _, schema := range []string{"加载中…", "加载失败", "", "   "} {
		sqlText, _, err := BuildGridMutationSQL(KindOracle, schema, "BUSINESS_APPLY", GridMutation{
			Action: "update", Values: map[string]any{"RELATIVESERIALNO": "123"},
			Key: map[string]any{"SERIALNO": "BA1"}, PrimaryKey: []string{"SERIALNO"},
		})
		if err != nil {
			t.Fatalf("schema %q should be normalized instead of failing: %v", schema, err)
		}
		if !strings.Contains(sqlText, `"BUSINESS_APPLY"`) || strings.Contains(sqlText, "加载") {
			t.Fatalf("unexpected SQL for schema %q: %s", schema, sqlText)
		}
	}
}
