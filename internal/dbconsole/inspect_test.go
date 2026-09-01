package dbconsole

import (
	"strings"
	"testing"
)

func TestConstraintLabelAndDDLType(t *testing.T) {
	t.Parallel()
	if constraintLabel("P") != "PRIMARY KEY" {
		t.Fatal(constraintLabel("P"))
	}
	if constraintLabel("R") != "FOREIGN KEY" {
		t.Fatal(constraintLabel("R"))
	}
	if ddlObjectType("MATERIALIZED VIEW") != "MATERIALIZED_VIEW" {
		t.Fatal(ddlObjectType("MATERIALIZED VIEW"))
	}
	if ddlObjectType("TABLE") != "TABLE" {
		t.Fatal(ddlObjectType("TABLE"))
	}
	if quoteIdent(KindOracle, `A"B`) != `"A""B"` {
		t.Fatal(quoteIdent(KindOracle, `A"B`))
	}
	if quoteIdent(KindMySQL, "a`b") != "`a``b`" {
		t.Fatal(quoteIdent(KindMySQL, "a`b"))
	}
}

func TestMySQLExplainRowKeepsColumnOrder(t *testing.T) {
	t.Parallel()
	row := mysqlExplainRow(
		[]string{"id", "select_type", "table", "type", "possible_keys", "key", "rows", "Extra"},
		[]string{"1", "SIMPLE", "emp", "ALL", "PRIMARY", "", "8", "Using where"},
	)
	if row.ID != "1" || row.Object != "emp" || row.Operation != "SIMPLE" || row.Options != "ALL" {
		t.Fatalf("structured fields: %#v", row)
	}
	if row.Cells["table"] != "emp" || row.Cells["Extra"] != "Using where" {
		t.Fatalf("cells: %#v", row.Cells)
	}
	if len(row.ColumnOrder) != 8 || row.ColumnOrder[2] != "table" {
		t.Fatalf("column order: %#v", row.ColumnOrder)
	}
}

func TestParseOraclePlanLine(t *testing.T) {
	t.Parallel()
	row := parseOraclePlanLine("|   0 | SELECT STATEMENT            |      |     1 |       |     2  (0)| 00:00:01 |")
	if row.ID != "0" || row.Operation != "SELECT STATEMENT" || row.Cardinality != "1" || !strings.Contains(row.Cost, "2") {
		t.Fatalf("typical xplan columns: %#v", row)
	}
	access := parseOraclePlanLine("|   1 |  TABLE ACCESS FULL          | EMP  |     8 |    96 |     2  (0)| 00:00:01 |")
	if access.Object != "EMP" || access.Operation != "TABLE ACCESS FULL" {
		t.Fatalf("table access: %#v", access)
	}
	plain := parseOraclePlanLine("Predicate Information (identified by operation id):")
	if plain.Operation != "" || plain.Raw == "" {
		t.Fatalf("heading should stay raw-only: %#v", plain)
	}
}

func TestExplainRejectsMutation(t *testing.T) {
	t.Parallel()
	m := &Manager{}
	_, err := m.Explain(t.Context(), Source{Kind: KindOracle, QueryTimeoutSeconds: 5, MaxOpenConnections: 1, MaxIdleConnections: 1}, "DELETE FROM t")
	if err == nil {
		t.Fatal("expected read-only rejection")
	}
}
