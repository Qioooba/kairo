package dbconsole

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"
)

func TestReviewMySQLBackslashExport(t *testing.T) {
	value := "C:\\logs\\'; DROP TABLE t; --"
	got := exportSQLLiteralForKind(KindMySQL, value)
	if strings.Contains(got, "DROP TABLE") || !strings.HasPrefix(got, "CONVERT(X'") {
		t.Fatal(got)
	}
	if exportSQLLiteralForKind(KindOracle, value) != quoteSQLString(value) {
		t.Fatal("Oracle escaping changed")
	}
}

func TestReviewOraclePositionalParameters(t *testing.T) {
	query, args, err := BindSQLParameters(KindOracle, "SELECT ? FROM dual WHERE id=?", []BindParameter{{Name: "1", Type: "string", Value: "hello"}, {Name: "2", Type: "int", Value: 2}})
	if err != nil || query != "SELECT :kairo_pos_1 FROM dual WHERE id=:kairo_pos_2" || len(args) != 2 {
		t.Fatalf("%s %v %v", query, args, err)
	}
	if arg, ok := args[0].(sql.NamedArg); !ok || arg.Name != "kairo_pos_1" || arg.Value != "hello" {
		t.Fatal(args)
	}
}

func TestReviewGridRejectsComplexSnapshots(t *testing.T) {
	_, _, err := BuildGridMutationSQL(KindOracle, "HR", "DOCS", GridMutation{Action: "update", PrimaryKey: []string{"ID"}, Key: map[string]any{"ID": 1}, Values: map[string]any{"NAME": "new"}, Original: map[string]any{"BODY": map[string]any{"kind": "clob", "preview": "part"}}})
	if err == nil {
		t.Fatal("LOB object passed to driver")
	}
	_, args, err := BuildGridMutationSQL(KindMySQL, "", "docs", GridMutation{Action: "update", PrimaryKey: []string{"ID"}, Key: map[string]any{"ID": 1}, Values: map[string]any{"NAME": nil}})
	if err != nil || args[0] != nil {
		t.Fatalf("NULL binding lost: %v %v", args, err)
	}
}

func TestReviewUnwrappedQueryPage(t *testing.T) {
	for _, query := range []string{"SELECT ID FROM t FOR UPDATE", "SHOW TABLES"} {
		t.Run(query, func(t *testing.T) {
			m, source, d := mutationManager(t)
			source.MaxRows = 2
			source.MaxResultBytes = 10000
			m.pools[source.ID].fingerprint = sourceFingerprint(source)
			d.queryRows = [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}
			info, err := ClassifySQL(source.Kind, query)
			if err != nil {
				t.Fatal(err)
			}
			var values [][]any
			summary, err := m.streamQueryAttempt(context.Background(), source, query, nil, QueryPage{Page: 2, PageSize: 2}, info, "", func(event StreamEvent) error { values = append(values, event.Rows...); return nil })
			if err != nil || len(values) != 2 || values[0][0] != int64(3) || values[1][0] != int64(4) || !summary.HasNext {
				t.Fatalf("%v %+v %v", values, summary, err)
			}
		})
	}
}

func TestReviewImportPreparesOnce(t *testing.T) {
	m, source, d := mutationManager(t)
	result, err := m.ApplyImport(context.Background(), source, ImportApplyRequest{Table: "t", SessionID: "tab", Mappings: []ImportMapping{{Target: "v", SourceIndex: 0}}, Rows: [][]string{{"one"}, {"two"}, {"three"}}})
	if err != nil || result.Processed != 3 || d.prepares != 1 || len(d.queries) != 3 {
		t.Fatalf("%+v prepares=%d queries=%d %v", result, d.prepares, len(d.queries), err)
	}
}

func TestReviewTransactionsReserveReadConnection(t *testing.T) {
	source := Source{ID: "s", Kind: KindMySQL, MaxOpenConnections: 4}
	m := &Manager{transactions: map[string]*transactionEntry{}}
	for _, id := range []string{"a", "b", "c"} {
		m.transactions[transactionKey(source.ID, id)] = &transactionEntry{sourceID: source.ID, fingerprint: sourceFingerprint(source)}
	}
	if _, err := m.transactionForContext(context.Background(), source, "fourth", true); err == nil || !strings.Contains(err.Error(), "最多 3") {
		t.Fatalf("pool reservation failed: %v", err)
	}
}

func TestReviewSQLDialectAndCTEWrites(t *testing.T) {
	for _, query := range []string{"WITH c AS (SELECT 1) DELETE FROM t", "WITH c AS (DELETE FROM t RETURNING id) SELECT * FROM c", "WITH c AS (SELECT 1) UPDATE t SET n=1"} {
		if _, err := ClassifySQL(KindMySQL, query); err == nil {
			t.Fatalf("CTE write accepted as query: %s", query)
		}
	}
	for _, query := range []string{"SELECT q'[it's ; DELETE FROM t]' FROM dual", "SELECT '\\' FROM dual", "SELECT 中文 FROM 中文表"} {
		if err := ValidateReadOnlySQL(KindOracle, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	if err := ValidateReadOnlySQL(KindMySQL, "SELECT 1 # DELETE FROM t\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := ClassifySQL(KindOracle, "SELECT q'[unterminated' FROM dual"); err == nil {
		t.Fatal("unterminated q string accepted")
	}
	if _, err := ClassifySQL(KindOracle, "SELECT '\\'; DELETE FROM t -- '"); err == nil {
		t.Fatal("Oracle backslash hid a second statement")
	}
}

func TestReviewExportSecretsAndFormulas(t *testing.T) {
	cell := map[string]any{"kind": "clob", "token": "secret-bearer", "preview": "hello"}
	if strings.Contains(ExportCellText(cell), "secret-bearer") {
		t.Fatal("export contains token")
	}
	if cell["token"] != "secret-bearer" {
		t.Fatal("export mutated query value")
	}
	for _, value := range []string{"-2+3+cmd", "=cmd", "+cmd", "@cmd", "\t=cmd"} {
		if !strings.HasPrefix(sanitizeXLSXCell(value), "'") {
			t.Fatalf("unprotected formula %q", value)
		}
	}
	if sanitizeXLSXCell("-1.5") != "-1.5" {
		t.Fatal("negative number changed")
	}
}

func TestReviewImportCellLimitBeforeConnection(t *testing.T) {
	m := &Manager{}
	_, err := m.ApplyImport(context.Background(), Source{Kind: KindMySQL, Environment: "development"}, ImportApplyRequest{SessionID: "tab", Rows: [][]string{{strings.Repeat("x", maxCellBytes+1)}}})
	if err == nil || !strings.Contains(err.Error(), "256KB") {
		t.Fatalf("expected size rejection, got %v", err)
	}
}

func TestReviewRedisRanges(t *testing.T) {
	if redisReadOnlyCommandAllowed("HGETALL", nil) {
		t.Fatal("unbounded HGETALL accepted")
	}
	for _, args := range [][]string{{"0", "-1"}, {"0", "500"}, {"0", "9223372036854775807"}} {
		if redisReadOnlyCommandAllowed("LRANGE", args) {
			t.Fatalf("unbounded range accepted %v", args)
		}
	}
	if !redisReadOnlyCommandAllowed("LRANGE", []string{"0", "499"}) {
		t.Fatal("bounded range rejected")
	}
	if redisReadOnlyCommandAllowed("XRANGE", []string{"-", "+"}) {
		t.Fatal("unbounded stream accepted")
	}
	if !redisReadOnlyCommandAllowed("XRANGE", []string{"-", "+", "COUNT", "20"}) {
		t.Fatal("bounded stream rejected")
	}
}
