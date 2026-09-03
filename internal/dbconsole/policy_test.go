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

func TestClassifySQL(t *testing.T) {
	// Good queries and classifications
	tests := []struct {
		sql      string
		wantType string
		isQuery  bool
	}{
		{"SELECT * FROM users", "SELECT", true},
		{"WITH cte AS (SELECT 1 FROM dual) SELECT * FROM cte", "SELECT", true},
		{"SELECT * FROM users FOR UPDATE", "FOR_UPDATE", true},
		{"UPDATE users SET name='bob' WHERE id=1", "DML", false},
		{"INSERT INTO users (id, name) VALUES (1, 'alice')", "DML", false},
		{"DELETE FROM users WHERE id=1", "DML", false},
		{"CREATE TABLE t (id INT)", "DDL", false},
		{"ALTER TABLE t ADD col VARCHAR(20)", "DDL", false},
		{"DROP TABLE t", "DDL", false},
		{"COMMIT", "TRANSACTION", false},
		{"ROLLBACK", "TRANSACTION", false},
		{"SHOW TABLES", "COMMAND", true},
		{"EXPLAIN SELECT 1", "COMMAND", true},
	}
	for _, tc := range tests {
		info, err := ClassifySQL(KindOracle, tc.sql)
		if err != nil {
			t.Errorf("ClassifySQL(%q) unexpected error: %v", tc.sql, err)
			continue
		}
		if info.Type != tc.wantType {
			t.Errorf("ClassifySQL(%q) type = %q, want %q", tc.sql, info.Type, tc.wantType)
		}
		if info.IsQuery != tc.isQuery {
			t.Errorf("ClassifySQL(%q) isQuery = %v, want %v", tc.sql, info.IsQuery, tc.isQuery)
		}
	}

	// Semicolon injection, dangerous functions, unsupported commands
	rejected := []string{
		"SELECT 1; DROP TABLE t",
		"SELECT 1; SELECT 2",
		"SELECT SLEEP(5)",
		"SELECT BENCHMARK(1000000, MD5(1))",
		"SELECT GET_LOCK('test', 10)",
		"SELECT RELEASE_LOCK('test')",
		"SELECT LOAD_FILE('/etc/passwd')",
		"SELECT * FROM t INTO OUTFILE '/tmp/t.csv'",
		"GRANT ALL PRIVILEGES ON *.* TO 'user'@'%'",
		"REVOKE ALL PRIVILEGES ON *.* FROM 'user'@'%'",
		"CALL my_procedure()",
		"EXEC sp_executesql",
		"LOCK TABLES t WRITE",
	}
	for _, q := range rejected {
		if _, err := ClassifySQL(KindMySQL, q); err == nil {
			t.Errorf("ClassifySQL(%q) expected error, but got nil", q)
		}
	}
}
