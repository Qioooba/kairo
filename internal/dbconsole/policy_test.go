package dbconsole

import "testing"

func TestValidateReadOnlySQL(t *testing.T) {
	good := []string{
		"select * from users",
		"/* report */ WITH x AS (SELECT 1 a FROM dual) SELECT * FROM x;",
		"select 'delete from x' as text from dual",
	}
	for _, q := range good {
		if err := ValidateReadOnlySQL(KindOracle, q); err != nil {
			t.Errorf("expected valid %q: %v", q, err)
		}
	}
	bad := []string{
		"update users set name='x'",
		"with x as (select 1) update users set name='x'",
		"with x as (select 1) delete from users",
		"select analyze from t",
		"select 1 /*!50000 ; DELETE FROM users */",
		"select load_file('/etc/passwd')",
		"select * from t for update",
		"select 1 from dual; delete from t",
		"select * from t into outfile '/tmp/x'",
	}
	for _, q := range bad {
		if err := ValidateReadOnlySQL(KindMySQL, q); err == nil {
			t.Errorf("expected rejection: %q", q)
		}
	}
}

func TestRejectLargeObjectColumns(t *testing.T) {
	if err := rejectLargeObjectColumns(KindOracle, []Column{{Name: "PAYLOAD", Database: "CLOB"}}); err == nil {
		t.Fatal("Oracle CLOB should require an explicit bounded preview")
	}
	if err := rejectLargeObjectColumns(KindMySQL, []Column{{Name: "payload", Database: "LONGTEXT"}}); err == nil {
		t.Fatal("MySQL LONGTEXT should require an explicit bounded preview")
	}
	if err := rejectLargeObjectColumns(KindOracle, []Column{{Name: "NAME", Database: "VARCHAR2"}}); err != nil {
		t.Fatalf("ordinary columns should pass: %v", err)
	}
}
