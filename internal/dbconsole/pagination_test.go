package dbconsole

import (
	"strings"
	"testing"
)

func TestServerPagedQueryOracle(t *testing.T) {
	page := QueryPage{Page: 2, PageSize: 50}
	got, err := serverPagedQuery(KindOracle, "SELECT id, name FROM users ORDER BY id", page)
	if err != nil {
		t.Fatalf("serverPagedQuery failed: %v", err)
	}

	// Must contain double-nested ROWNUM
	if !strings.Contains(got, "ROWNUM AS \"__KAIRO_RN_") {
		t.Errorf("Expected ROWNUM alias, got: %s", got)
	}
	if !strings.Contains(got, "WHERE ROWNUM <= 101") { // (2-1)*50 + 51 = 101
		t.Errorf("Expected upper bound 101, got: %s", got)
	}
	if !strings.Contains(got, "> 50") { // offset = 50
		t.Errorf("Expected lower bound > 50, got: %s", got)
	}
	if !strings.Contains(got, "SELECT id, name FROM users ORDER BY id") {
		t.Errorf("Original query must be preserved, got: %s", got)
	}
}

func TestServerPagedQueryMySQL(t *testing.T) {
	page := QueryPage{Page: 3, PageSize: 20}
	got, err := serverPagedQuery(KindMySQL, "SELECT * FROM orders WHERE status = 1;", page)
	if err != nil {
		t.Fatalf("serverPagedQuery failed: %v", err)
	}

	// Must contain LIMIT 21 OFFSET 40
	if !strings.Contains(got, "LIMIT 21 OFFSET 40") {
		t.Errorf("Expected LIMIT 21 OFFSET 40, got: %s", got)
	}
	if !strings.Contains(got, "AS kairo_page_q") {
		t.Errorf("Expected derived table alias, got: %s", got)
	}
}

func TestServerPagedQueryValidation(t *testing.T) {
	// Empty query
	_, err := serverPagedQuery(KindOracle, "", QueryPage{Page: 1, PageSize: 10})
	if err == nil {
		t.Errorf("Expected error for empty query")
	}

	// Invalid page
	_, err = serverPagedQuery(KindOracle, "SELECT 1", QueryPage{Page: 0, PageSize: 10})
	if err == nil {
		t.Errorf("Expected error for page < 1")
	}

	// Non-SQL source
	_, err = serverPagedQuery(KindRedis, "SELECT 1", QueryPage{Page: 1, PageSize: 10})
	if err == nil {
		t.Errorf("Expected error for Redis source")
	}
}

func TestQueryHasOrderBy(t *testing.T) {
	cases := []struct {
		sql      string
		expected bool
	}{
		{"SELECT * FROM users ORDER BY id", true},
		{"SELECT * FROM users order by name desc", true},
		{"SELECT * FROM users WHERE col = 'ORDER BY test'", false},
		{"SELECT * FROM users -- ORDER BY comment", false},
		{"SELECT * FROM users", false},
	}
	for _, tc := range cases {
		got := queryHasOrderBy(tc.sql)
		if got != tc.expected {
			t.Errorf("queryHasOrderBy(%q) = %v, want %v", tc.sql, got, tc.expected)
		}
	}
}

func TestServerPagedQueryMultipleSemicolons(t *testing.T) {
	page := QueryPage{Page: 1, PageSize: 10}
	queries := []string{
		"SELECT 1;;",
		"SELECT 1; ; ;\n;",
		"SELECT id FROM users WHERE status = 1;;;  ",
	}
	for _, q := range queries {
		gotOracle, err := serverPagedQuery(KindOracle, q, page)
		if err != nil {
			t.Fatalf("Oracle pagination failed for %q: %v", q, err)
		}
		if strings.Contains(gotOracle, ";") {
			t.Fatalf("Oracle query should not contain any semicolons inside wrapper: %s", gotOracle)
		}

		gotMySQL, err := serverPagedQuery(KindMySQL, q, page)
		if err != nil {
			t.Fatalf("MySQL pagination failed for %q: %v", q, err)
		}
		if strings.Contains(gotMySQL, ";") {
			t.Fatalf("MySQL query should not contain any semicolons inside derived table: %s", gotMySQL)
		}
	}
}

func TestPaginationPlan_ExplicitHelperColumn(t *testing.T) {
	// Oracle Page 1: offset == 0 -> HasHelperColumn should be FALSE
	p1Oracle, err := serverPagedPlan(KindOracle, "SELECT id, name FROM users", QueryPage{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if p1Oracle.HasHelperColumn {
		t.Errorf("Oracle Page 1 should not inject helper column, got %v", p1Oracle.HasHelperColumn)
	}

	// Oracle Page 2: offset > 0 -> HasHelperColumn should be TRUE and HelperColumnName set
	p2Oracle, err := serverPagedPlan(KindOracle, "SELECT id, name FROM users", QueryPage{Page: 2, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if !p2Oracle.HasHelperColumn || !strings.HasPrefix(p2Oracle.HelperColumnName, "__KAIRO_RN_") {
		t.Errorf("Oracle Page 2 must inject helper column, got: has=%v name=%s", p2Oracle.HasHelperColumn, p2Oracle.HelperColumnName)
	}

	// MySQL Page 1 and Page 2: HasHelperColumn should be FALSE (LIMIT/OFFSET without helper columns)
	p1MySQL, err := serverPagedPlan(KindMySQL, "SELECT id, name FROM users", QueryPage{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if p1MySQL.HasHelperColumn {
		t.Errorf("MySQL Page 1 should not inject helper column")
	}
	p2MySQL, err := serverPagedPlan(KindMySQL, "SELECT id, name FROM users", QueryPage{Page: 2, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if p2MySQL.HasHelperColumn {
		t.Errorf("MySQL Page 2 should not inject helper column")
	}
}

func TestQueryHasOrderBy_ParenAware(t *testing.T) {
	cases := []struct {
		name     string
		sql      string
		expected bool
	}{
		{"Top-level ORDER BY", "SELECT id FROM users ORDER BY id", true},
		{"Top-level ORDER BY desc", "SELECT id FROM users ORDER BY id DESC", true},
		{"Window function OVER (ORDER BY)", "SELECT id, ROW_NUMBER() OVER (ORDER BY id) AS rn FROM users", false},
		{"Window function with top-level", "SELECT id, ROW_NUMBER() OVER (ORDER BY id) AS rn FROM users ORDER BY id", true},
		{"CTE inner ORDER BY", "WITH cte AS (SELECT * FROM users ORDER BY id) SELECT * FROM cte", false},
		{"CTE inner with top-level", "WITH cte AS (SELECT * FROM users ORDER BY id) SELECT * FROM cte ORDER BY name", true},
		{"String literal", "SELECT id, 'ORDER BY' AS note FROM users", false},
		{"Comment", "SELECT id FROM users /* ORDER BY id */", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := QueryHasTopLevelOrderBy(KindOracle, tc.sql)
			if got != tc.expected {
				t.Errorf("QueryHasTopLevelOrderBy(%q) = %v, want %v", tc.sql, got, tc.expected)
			}
		})
	}
}

