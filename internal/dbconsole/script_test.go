package dbconsole

import (
	"strings"
	"testing"
)

func TestSplitSQLScriptKeepsStringsCommentsAndPLSQL(t *testing.T) {
	script := "SELECT ';' AS v FROM dual;\n-- comment ;\nUPDATE t SET v=:v;\nBEGIN\n  UPDATE t SET v = v + 1;\n  INSERT INTO log_t(msg) VALUES ('a;b');\nEND;\n/\n"
	parts, err := SplitSQLScript(KindOracle, script)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("expected 3 statements, got %d: %#v", len(parts), parts)
	}
	if !strings.Contains(parts[0], "';'") || !strings.Contains(parts[1], "UPDATE") || !strings.Contains(parts[2], "INSERT INTO") {
		t.Fatalf("split damaged statement contents: %#v", parts)
	}
}

func TestScriptLexerCommentsQQuotesAndBlockKeywords(t *testing.T) {
	script := "-- leading comment\nBEGIN\n x := q'[it's :not_a_bind; text]';\nEND;\n/\nINSERT INTO t(v) VALUES ('c:\\'); -- trailing comment"
	parts, err := SplitSQLScript(KindOracle, script)
	if err != nil || len(parts) != 2 {
		t.Fatalf("parts=%q err=%v", parts, err)
	}
	if _, _, err := BindSQLParameters(KindOracle, script, nil); err != nil {
		t.Fatal(err)
	}
	info, err := ClassifyScriptSQL(KindOracle, "-- comment\nCREATE OR REPLACE FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END;")
	if err != nil || info.Type != "DDL" {
		t.Fatalf("comment bypassed DDL gate: %+v %v", info, err)
	}
	parts, err = SplitSQLScript(KindOracle, "CREATE TABLE t (type VARCHAR2(10)); INSERT INTO t(type) VALUES ('a');")
	if err != nil || len(parts) != 2 {
		t.Fatalf("column TYPE mistaken for stored block: %q %v", parts, err)
	}
}

func TestClassifyScriptSQLPLSQL(t *testing.T) {
	info, err := ClassifyScriptSQL(KindOracle, "BEGIN\n NULL;\nEND;")
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != "PLSQL" || info.IsQuery {
		t.Fatalf("unexpected PL/SQL classification: %#v", info)
	}
}

func TestBindSQLParametersTypedAndSafe(t *testing.T) {
	sqlText, args, err := BindSQLParameters(KindMySQL, "SELECT * FROM t WHERE id=:id AND name=:name AND note=':ignored'", []BindParameter{
		{Name: "id", Type: "int", Value: float64(7)},
		{Name: "name", Type: "string", Value: "alice"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sqlText, ":id") || len(args) != 2 || !strings.Contains(sqlText, "id=?") {
		t.Fatalf("unexpected MySQL bind rewrite: %q %#v", sqlText, args)
	}
	if got, ok := args[0].(int64); !ok || got != 7 {
		t.Fatalf("integer conversion failed: %#v", args[0])
	}
	if _, _, err := BindSQLParameters(KindOracle, "select :missing from dual", nil); err == nil {
		t.Fatal("missing bind must be rejected")
	}
	if _, _, err := BindSQLParameters(KindOracle, "select :id from dual", []BindParameter{{Name: "extra", Type: "int", Value: 1}}); err == nil {
		t.Fatal("unused bind must be rejected")
	}
	_, positional, err := BindSQLParameters(KindMySQL, "select ? + ?", []BindParameter{{Name: "1", Type: "int", Value: 1}, {Name: "2", Type: "int", Value: 2}})
	if err != nil || len(positional) != 2 {
		t.Fatalf("positional MySQL binds failed: %#v %v", positional, err)
	}
}
