package dbconsole

import "testing"

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

func TestParseOraclePlanLine(t *testing.T) {
	t.Parallel()
	row := parseOraclePlanLine("|  0 | SELECT STATEMENT |      |     |     |   2 |")
	if row.Operation == "" || row.Raw == "" {
		t.Fatalf("expected parsed operation: %#v", row)
	}
	plain := parseOraclePlanLine("Predicate Information")
	if plain.Operation != "Predicate Information" {
		t.Fatalf("plain line: %#v", plain)
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
