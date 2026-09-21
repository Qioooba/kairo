package dbconsole

import (
	"context"
	"testing"
	"time"
)

func TestConstraintLabels(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"P":            "PRIMARY KEY",
		"p":            "PRIMARY KEY",
		"PRIMARY KEY":  "PRIMARY KEY",
		"U":            "UNIQUE",
		"u":            "UNIQUE",
		"UNIQUE":       "UNIQUE",
		"R":            "FOREIGN KEY",
		"r":            "FOREIGN KEY",
		"FOREIGN KEY":  "FOREIGN KEY",
		"C":            "CHECK",
		"c":            "CHECK",
		"CHECK":        "CHECK",
		"V":            "CHECK OPTION",
		"v":            "CHECK OPTION",
		"CHECK OPTION": "CHECK OPTION",
		"O":            "READ ONLY",
		"o":            "READ ONLY",
		"READ ONLY":    "READ ONLY",
		"CUSTOM":       "CUSTOM",
	}
	for in, want := range cases {
		if got := constraintLabel(in); got != want {
			t.Errorf("constraintLabel(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestInspectSchemaNormalizationAndCache(t *testing.T) {
	t.Parallel()
	m := &Manager{metadataCache: make(map[string]metadataCacheEntry)}
	ctx := context.Background()

	oracleSource := Source{ID: "src-oracle-1", Kind: KindOracle, Username: "kairo_admin"}

	// Pre-populate cache with normalized schema "KAIRO_ADMIN" and table "EMP"
	expectedIndexes := []IndexInfo{
		{Name: "PK_EMP", Type: "NORMAL", Uniqueness: "UNIQUE", Columns: []string{"EMPNO"}},
	}
	expectedConstraints := []ConstraintInfo{
		{Name: "PK_EMP", Type: "PRIMARY KEY", Columns: "EMPNO"},
	}
	expectedFields := []Field{
		{Name: "EMPNO", DataType: "NUMBER", Nullable: false, PrimaryKey: true},
	}

	metadataCacheSet(m, "src-oracle-1\x00indexes\x00KAIRO_ADMIN\x00EMP", expectedIndexes)
	metadataCacheSet(m, "src-oracle-1\x00constraints\x00KAIRO_ADMIN\x00EMP", expectedConstraints)
	metadataCacheSet(m, "src-oracle-1\x00fields\x00KAIRO_ADMIN\x00EMP", expectedFields)

	// Test with placeholder schema "加载中…"
	for _, placeholder := range []string{"加载中…", "加载失败", "", "  "} {
		idxs, err := m.Indexes(ctx, oracleSource, placeholder, "EMP")
		if err != nil {
			t.Fatalf("Indexes with placeholder %q failed: %v", placeholder, err)
		}
		if len(idxs) != 1 || idxs[0].Name != "PK_EMP" {
			t.Fatalf("Indexes mismatch for placeholder %q: %+v", placeholder, idxs)
		}

		cons, err := m.Constraints(ctx, oracleSource, placeholder, "EMP")
		if err != nil {
			t.Fatalf("Constraints with placeholder %q failed: %v", placeholder, err)
		}
		if len(cons) != 1 || cons[0].Name != "PK_EMP" {
			t.Fatalf("Constraints mismatch for placeholder %q: %+v", placeholder, cons)
		}
	}
}

func TestInspectObjectFallbackSlices(t *testing.T) {
	t.Parallel()
	m := &Manager{metadataCache: make(map[string]metadataCacheEntry)}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	source := Source{ID: "src-mysql-1", Kind: KindMySQL, Database: "kairo_db", Host: "127.0.0.1", Port: 9999, QueryTimeoutSeconds: 1}

	// Cache fields, indexes, constraints
	metadataCacheSet(m, "src-mysql-1\x00fields\x00kairo_db\x00users", []Field{
		{Name: "id", DataType: "bigint", Nullable: false, PrimaryKey: true},
	})
	metadataCacheSet(m, "src-mysql-1\x00indexes\x00KAIRO_DB\x00USERS", []IndexInfo{
		{Name: "PRIMARY", Uniqueness: "UNIQUE", Columns: []string{"id"}},
	})
	metadataCacheSet(m, "src-mysql-1\x00constraints\x00KAIRO_DB\x00USERS", []ConstraintInfo{
		{Name: "PRIMARY", Type: "PRIMARY KEY", Columns: "id"},
	})

	inspect, err := m.InspectObject(ctx, source, "加载中…", "users", "TABLE")
	if err != nil {
		t.Fatalf("InspectObject failed: %v", err)
	}

	if len(inspect.Fields) != 1 || inspect.Fields[0].Name != "id" {
		t.Fatalf("Fields mismatch: %+v", inspect.Fields)
	}
	if len(inspect.Indexes) != 1 || inspect.Indexes[0].Name != "PRIMARY" {
		t.Fatalf("Indexes mismatch: %+v", inspect.Indexes)
	}
	if len(inspect.Constraints) != 1 || inspect.Constraints[0].Name != "PRIMARY" {
		t.Fatalf("Constraints mismatch: %+v", inspect.Constraints)
	}
}

