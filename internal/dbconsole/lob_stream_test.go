package dbconsole

import "testing"

func TestLOBRefValidate(t *testing.T) {
	if err := (LOBRef{Owner: "HR", Table: "DOCS", Column: "BODY", UseRowID: true, RowID: "AAAB12AADAAAAwPAAA"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (LOBRef{Owner: "HR", Table: "DOCS", Column: "BODY", PrimaryKey: []string{"ID"}, Keys: map[string]any{"ID": 1}}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (LOBRef{Owner: "", Table: "DOCS", Column: "BODY"}).Validate(); err == nil {
		t.Fatal("expected missing owner")
	}
	if err := (LOBRef{Owner: "HR", Table: "DOCS", Column: "BODY", PrimaryKey: []string{"ID"}, Keys: map[string]any{}}).Validate(); err == nil {
		t.Fatal("expected missing pk")
	}
}

func TestLOBWhereClause(t *testing.T) {
	ref := LOBRef{Owner: "HR", Table: "DOCS", Column: "BODY", PrimaryKey: []string{"ID"}, Keys: map[string]any{"ID": 42}}
	sql, args, err := ref.WhereClause()
	if err != nil {
		t.Fatal(err)
	}
	if sql != `"ID" = :kairo_pk1` {
		t.Fatalf("where: %s", sql)
	}
	if len(args) != 1 {
		t.Fatalf("args: %v", args)
	}
	rowRef := LOBRef{Owner: "HR", Table: "T", Column: "C", UseRowID: true, RowID: "AAAB12AADAAAAwPAAA"}
	sql2, _, err := rowRef.WhereClause()
	if err != nil || sql2 != "ROWID = :kairo_rowid" {
		t.Fatalf("rowid where: %s err:%v", sql2, err)
	}
}

func TestIsLOBType(t *testing.T) {
	if !isLOBType("CLOB") || !isLOBType("BLOB") || !isLOBType("NCLOB") {
		t.Fatal("lob type")
	}
	if isLOBType("VARCHAR2") {
		t.Fatal("not lob")
	}
	if !isBlobType("BLOB") || isBlobType("CLOB") {
		t.Fatal("blob type")
	}
}

func TestIsTTCError(t *testing.T) {
	if !isTTCError(nil) == false {
		// nil should be false
	}
	if !isTTCError(errWith("TTC error: received code 10 during response reading")) {
		t.Fatal("ttc")
	}
	if isTTCError(errWith("ORA-00942")) {
		t.Fatal("not ttc")
	}
}

func errWith(s string) error { return &testErr{s} }

type testErr struct{ s string }

func (e *testErr) Error() string { return e.s }
