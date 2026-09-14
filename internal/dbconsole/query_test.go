package dbconsole

import (
	"encoding/base64"
	"errors"
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

func TestStripRowAliasQuotes(t *testing.T) {
	columns := []Column{
		{Name: "ID"},
		{Name: `"__KAIRO_RN_e8f2"`},
		{Name: "NAME"},
	}
	aliasIdx := -1
	for i, c := range columns {
		colName := strings.Trim(strings.ToUpper(c.Name), "\"`[] \t")
		if strings.HasPrefix(colName, "__KAIRO_RN_") {
			aliasIdx = i
			break
		}
	}
	if aliasIdx != 1 {
		t.Fatalf("expected aliasIdx 1, got %d", aliasIdx)
	}
}

func TestRedisMutateTTLRejectsZeroOrNegative(t *testing.T) {
	m := &Manager{}
	source := Source{Kind: KindRedis, AllowRedisWrite: true}

	// 0 seconds should be rejected
	_, err := m.RedisMutateTTL(t.Context(), source, "mykey", "EXPIRE", 0)
	if err == nil || !strings.Contains(err.Error(), "1 秒到 365 天之间") {
		t.Fatalf("expected TTL 0 to be rejected, got err: %v", err)
	}

	// -1 seconds should be rejected
	_, err = m.RedisMutateTTL(t.Context(), source, "mykey", "EXPIRE", -1)
	if err == nil || !strings.Contains(err.Error(), "1 秒到 365 天之间") {
		t.Fatalf("expected TTL -1 to be rejected, got err: %v", err)
	}
}

func TestRedisCursorValidation(t *testing.T) {
	_, _, err := parseClusterCursor("abc")
	if err == nil {
		t.Fatal("expected invalid cursor 'abc' to return error")
	}

	_, _, err = parseClusterCursor("node:bad")
	if err == nil {
		t.Fatal("expected invalid cluster cursor 'node:bad' to return error")
	}
}

func TestFastRowBytesCalculation(t *testing.T) {
	row := []any{
		12345,
		"hello world",
		true,
		nil,
		map[string]any{"kind": "clob", "bytes": 1000, "text": "preview"},
	}
	bytesCount := fastRowBytes(row)
	if bytesCount <= 0 {
		t.Fatalf("expected positive row bytes, got %d", bytesCount)
	}
	// 验证空行
	if emptyBytes := fastRowBytes(nil); emptyBytes != 0 {
		t.Fatalf("expected 0 bytes for empty row, got %d", emptyBytes)
	}
}

func BenchmarkFastRowBytes(b *testing.B) {
	row := []any{
		12345,
		"hello world this is a test column value",
		true,
		nil,
		map[string]any{"kind": "clob", "bytes": 1000, "text": "preview content"},
		9999.88,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = fastRowBytes(row)
	}
}

func TestNormalizeColumnValueLOBTruncationMemoryIsolation(t *testing.T) {
	// 构造 500 KB 大文本
	largeStr := strings.Repeat("A", 500*1024)
	result := normalizeColumnValue(largeStr, "CLOB")
	clobMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any for CLOB, got %T", result)
	}
	if clobMap["kind"] != "clob" {
		t.Fatalf("expected kind clob, got %v", clobMap["kind"])
	}
	if clobMap["bytes"] != 500*1024 {
		t.Fatalf("expected original bytes 512000, got %v", clobMap["bytes"])
	}
	if clobMap["truncated"] != true {
		t.Fatalf("expected truncated=true, got %v", clobMap["truncated"])
	}
	previewText, ok := clobMap["text"].(string)
	if !ok || len(previewText) != maxLOBPreviewBytes {
		t.Fatalf("expected preview text length %d, got %d", maxLOBPreviewBytes, len(previewText))
	}

	// 构造 500 KB 大 BLOB
	largeBlob := make([]byte, 500*1024)
	for i := range largeBlob {
		largeBlob[i] = byte(i % 256)
	}
	blobResult := normalizeColumnValue(largeBlob, "BLOB")
	blobMap, ok := blobResult.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any for BLOB, got %T", blobResult)
	}
	if blobMap["kind"] != "blob" {
		t.Fatalf("expected kind blob, got %v", blobMap["kind"])
	}
	if blobMap["truncated"] != true {
		t.Fatalf("expected truncated=true, got %v", blobMap["truncated"])
	}

	// 验证 bytes.Clone 保证的独立内存：修改原始切片不影响已截断对象
	largeBlob[0] = 0xFF
	if hexVal, ok := blobMap["hex"].(string); ok {
		// 原切片首字节为 0，经 hex.EncodeToString 后前两位应为 "00"，而不是修改后的 "ff"
		if !strings.HasPrefix(hexVal, "00") {
			t.Fatalf("expected preview to be memory isolated, got prefix: %s", hexVal[:4])
		}
	}
}

func TestBoundedCellScanner(t *testing.T) {
	scanner := boundedCellScanner{dbType: "VARCHAR2"}
	if err := scanner.Scan("normal text"); err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if scanner.value != "normal text" {
		t.Fatalf("expected 'normal text', got %v", scanner.value)
	}

	// 测试 NULL 值
	if err := scanner.Scan(nil); err != nil {
		t.Fatalf("scan nil failed: %v", err)
	}
	if scanner.value != nil {
		t.Fatalf("expected nil value, got %v", scanner.value)
	}
}

func TestOracleTTCErrorHandling(t *testing.T) {
	ttcErr := errors.New("TTC error: received code 10 during response reading")
	if !isConnectionFailure(ttcErr) {
		t.Fatalf("expected TTC error to be recognized as connection failure, but got false")
	}
	if !isRetryableQueryFailure(ttcErr) {
		t.Fatalf("expected TTC error to be retryable query failure for pool invalidation, but got false")
	}

	normalErr := errors.New("ORA-00942: table or view does not exist")
	if isConnectionFailure(normalErr) {
		t.Fatalf("expected normal ORA error to not be connection failure, but got true")
	}
}

func TestNormalizeBytesLargeUTF8RuneBoundary(t *testing.T) {
	// Construct a large UTF-8 byte slice where index maxCellBytes cuts right in the middle of a 3-byte Chinese rune
	prefix := strings.Repeat("a", maxCellBytes-1)
	chinese := "中文测试数据"
	full := []byte(prefix + chinese)
	res := normalizeBytes(full)
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected truncated text map, got %T: %v", res, res)
	}
	if m["kind"] != "text" {
		t.Fatalf("expected text kind, got: %v (falsely classified as binary!)", m["kind"])
	}
	preview, ok := m["preview"].(string)
	if !ok || !utf8.ValidString(preview) {
		t.Fatalf("preview should be valid UTF-8 string, got: %v", preview)
	}
}
