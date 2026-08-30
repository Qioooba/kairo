package dbconsole

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestServerLimitedQueryOracle11g(t *testing.T) {
	got, err := serverLimitedQuery(KindOracle, " SELECT id FROM orders ORDER BY id; ", 101)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT * FROM (\nSELECT id FROM orders ORDER BY id\n) WHERE ROWNUM <= 101"
	if got != want {
		t.Fatalf("unexpected Oracle query:\n%s", got)
	}

	withQuery, err := serverLimitedQuery(KindOracle, "WITH x AS (SELECT 1 n FROM dual) SELECT n FROM x", 11)
	if err != nil || !strings.Contains(withQuery, "ROWNUM <= 11") {
		t.Fatalf("Oracle WITH was not limited: query=%q err=%v", withQuery, err)
	}
}

func TestServerLimitedQueryMySQL(t *testing.T) {
	got, err := serverLimitedQuery(KindMySQL, "SELECT id FROM orders", 51)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "AS kairo_limited_query LIMIT 51") {
		t.Fatalf("MySQL query was not server limited: %s", got)
	}
	show, err := serverLimitedQuery(KindMySQL, "SHOW TABLES", 51)
	if err != nil || show != "SHOW TABLES" {
		t.Fatalf("SHOW should remain executable: query=%q err=%v", show, err)
	}
}

func TestTruncateTextKeepsUTF8Boundary(t *testing.T) {
	value := strings.Repeat("a", maxCellBytes-1) + "中文"
	got, ok := truncateText(value).(map[string]any)
	if !ok {
		t.Fatalf("expected truncated value, got %T", truncateText(value))
	}
	preview, _ := got["preview"].(string)
	if !utf8.ValidString(preview) || !strings.HasSuffix(preview, "a") {
		t.Fatalf("invalid UTF-8 preview ending: %q", preview[len(preview)-8:])
	}
}

func TestNormalizeInvalidUTF8StringAsBinary(t *testing.T) {
	raw := []byte{'a', 0xff, 'b'}
	got, ok := normalizeValue(string(raw)).(map[string]any)
	if !ok || got["kind"] != "binary" {
		t.Fatalf("invalid UTF-8 was not encoded as binary: %#v", got)
	}
	if got["preview_base64"] != base64.StdEncoding.EncodeToString(raw) {
		t.Fatalf("binary preview changed bytes: %#v", got)
	}
}

func TestMetadataCacheInvalidation(t *testing.T) {
	m := &Manager{metadataCache: make(map[string]metadataCacheEntry)}
	metadataCacheSet(m, "source-a\x00schemas", []Schema{{Name: "A"}})
	metadataCacheSet(m, "source-b\x00schemas", []Schema{{Name: "B"}})
	m.InvalidateMetadata("source-a")
	if _, ok := metadataCacheGet[[]Schema](m, "source-a\x00schemas"); ok {
		t.Fatal("source-a metadata was not invalidated")
	}
	if got, ok := metadataCacheGet[[]Schema](m, "source-b\x00schemas"); !ok || len(got) != 1 {
		t.Fatal("unrelated source metadata was invalidated")
	}
}
