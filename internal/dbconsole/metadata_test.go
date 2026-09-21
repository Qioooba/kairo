package dbconsole

import (
	"context"
	"strings"
	"testing"
)

func TestObjectCategory(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"TABLE":             "tables",
		"base table":        "tables",
		"VIEW":              "views",
		"MATERIALIZED VIEW": "views",
		"FUNCTION":          "functions",
		"PROCEDURE":         "procedures",
		"PACKAGE":           "procedures",
		"TRIGGER":           "triggers",
		"SEQUENCE":          "other",
	}
	for objectType, want := range tests {
		objectType, want := objectType, want
		t.Run(objectType, func(t *testing.T) {
			t.Parallel()
			if got := objectCategory(objectType); got != want {
				t.Fatalf("objectCategory(%q) = %q, want %q", objectType, got, want)
			}
		})
	}
}

func TestObjectsValidationAndCategory(t *testing.T) {
	m := &Manager{metadataCache: make(map[string]metadataCacheEntry)}
	ctx := context.Background()
	source := Source{Kind: KindRedis}
	_, err := m.Objects(ctx, source, "public", "", "tables")
	if err == nil || !strings.Contains(err.Error(), "Redis 没有表对象") {
		t.Fatalf("expected Redis error, got %v", err)
	}

	sourceSQL := Source{Kind: KindMySQL, Host: "invalid.local", Port: 3306}
	_, err = m.Objects(ctx, sourceSQL, "", "", "tables")
	if err == nil || !strings.Contains(err.Error(), "schema 不能为空") {
		t.Fatalf("expected schema required error, got %v", err)
	}

	longName := strings.Repeat("a", 300)
	_, err = m.Objects(ctx, sourceSQL, longName, "", "tables")
	if err == nil || !strings.Contains(err.Error(), "不能超过 256 字节") {
		t.Fatalf("expected length error, got %v", err)
	}
}

func TestObjectsCategoryCacheIsolation(t *testing.T) {
	t.Parallel()
	m := &Manager{metadataCache: make(map[string]metadataCacheEntry)}
	ctx := context.Background()
	source := Source{ID: "cache-cat-src", Kind: KindMySQL, Host: "invalid.local", Port: 3306}
	tables := []Object{{Schema: "HR", Name: "EMP", Type: "TABLE", Category: "tables"}}
	views := []Object{{Schema: "HR", Name: "EMP_V", Type: "VIEW", Category: "views"}}
	metadataCacheSet(m, "cache-cat-src\x00objects\x00HR\x00TABLES\x00", append([]Object(nil), tables...))
	metadataCacheSet(m, "cache-cat-src\x00objects\x00HR\x00VIEWS\x00", append([]Object(nil), views...))

	gotTables, err := m.Objects(ctx, source, "HR", "", "tables")
	if err != nil {
		t.Fatalf("cached tables: %v", err)
	}
	if len(gotTables) != 1 || gotTables[0].Name != "EMP" {
		t.Fatalf("cached tables mismatch: %+v", gotTables)
	}
	// 大小写归一：Tables 与 tables 命中同一缓存维度。
	gotTablesUpper, err := m.Objects(ctx, source, "hr", "", "Tables")
	if err != nil || len(gotTablesUpper) != 1 || gotTablesUpper[0].Name != "EMP" {
		t.Fatalf("category case-insensitive cache failed: %+v %v", gotTablesUpper, err)
	}
	gotViews, err := m.Objects(ctx, source, "HR", "", "views")
	if err != nil || len(gotViews) != 1 || gotViews[0].Name != "EMP_V" {
		t.Fatalf("cached views mismatch: %+v %v", gotViews, err)
	}
	// 空 category 与 all 向后兼容：互不污染，各自缓存维度独立。
	all := []Object{{Schema: "HR", Name: "EMP", Type: "TABLE", Category: "tables"}, {Schema: "HR", Name: "EMP_V", Type: "VIEW", Category: "views"}}
	metadataCacheSet(m, "cache-cat-src\x00objects\x00HR\x00\x00", append([]Object(nil), all...))
	metadataCacheSet(m, "cache-cat-src\x00objects\x00HR\x00ALL\x00", append([]Object(nil), all...))
	gotAll, err := m.Objects(ctx, source, "HR", "", "")
	if err != nil || len(gotAll) != 2 {
		t.Fatalf("empty category compat failed: %+v %v", gotAll, err)
	}
	gotAllWord, err := m.Objects(ctx, source, "HR", "", "all")
	if err != nil || len(gotAllWord) != 2 {
		t.Fatalf("all category compat failed: %+v %v", gotAllWord, err)
	}
}

func TestResolveSchema(t *testing.T) {
	oracleSource := Source{Kind: KindOracle, Username: "kairo_admin"}
	mysqlSource := Source{Kind: KindMySQL, Database: "kairo_db"}

	// Explicit schema preserved
	if got := ResolveSchema(oracleSource, "CUSTOM_SCHEMA"); got != "CUSTOM_SCHEMA" {
		t.Fatalf("expected CUSTOM_SCHEMA, got %q", got)
	}
	// Placeholders and blanks fallback to source defaults
	for _, placeholder := range []string{"", "  ", "加载中…", "加载失败"} {
		if got := ResolveSchema(oracleSource, placeholder); got != "KAIRO_ADMIN" {
			t.Fatalf("oracle placeholder %q fallback expected KAIRO_ADMIN, got %q", placeholder, got)
		}
		if got := ResolveSchema(mysqlSource, placeholder); got != "kairo_db" {
			t.Fatalf("mysql placeholder %q fallback expected kairo_db, got %q", placeholder, got)
		}
	}
}
