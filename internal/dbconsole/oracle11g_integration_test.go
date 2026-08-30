package dbconsole

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"kairo/internal/credentials"
)

// TestOracle11gIntegration is skipped in ordinary CI. It is the release-gate
// probe for the real Oracle 11g environment and deliberately uses the same
// Manager, credential backend, read-only transaction and stream path as HTTP.
func TestOracle11gIntegration(t *testing.T) {
	host := os.Getenv("KAIRO_TEST_ORACLE_HOST")
	if host == "" {
		t.Skip("set KAIRO_TEST_ORACLE_HOST and related variables for the Oracle 11g release gate")
	}
	user := os.Getenv("KAIRO_TEST_ORACLE_USER")
	password := os.Getenv("KAIRO_TEST_ORACLE_PASSWORD")
	service := os.Getenv("KAIRO_TEST_ORACLE_SERVICE")
	if user == "" || password == "" || service == "" {
		t.Fatal("KAIRO_TEST_ORACLE_USER/PASSWORD/SERVICE are required")
	}
	port := 1521
	if raw := os.Getenv("KAIRO_TEST_ORACLE_PORT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatal("KAIRO_TEST_ORACLE_PORT must be an integer")
		}
		port = parsed
	}
	connectBy := os.Getenv("KAIRO_TEST_ORACLE_CONNECT_BY")
	if connectBy == "" {
		connectBy = "service_name"
	}

	dir := t.TempDir()
	credentials.SetMode(credentials.ModeFile)
	if err := credentials.Init(dir, ""); err != nil {
		t.Fatal(redactIntegrationError(err, password))
	}
	t.Cleanup(func() { credentials.SetMode(credentials.ModeKeyring) })
	source := Source{
		ID: "oracle11g_release_gate", Name: "Oracle 11g release gate", Kind: KindOracle,
		Host: host, Port: port, Username: user, OracleConnectBy: connectBy,
		OracleService: service, OracleClientCharset: os.Getenv("KAIRO_TEST_ORACLE_CHARSET"),
		QueryTimeoutSeconds: 30, MaxRows: 20, MaxResultBytes: 2 << 20,
		MaxOpenConnections: 2, MaxIdleConnections: 1, ConnectionMaxMinutes: 5,
	}
	source.Defaults()
	if err := source.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := credentials.SaveResource(CredentialNamespace, source.ID, source.CredentialUser(), password); err != nil {
		t.Fatal(redactIntegrationError(err, password))
	}
	manager, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	if _, err := manager.Test(context.Background(), source); err != nil {
		t.Fatalf("Oracle connection failed: %s", redactIntegrationError(err, password))
	}
	rows := collectIntegrationRows(t, manager, source, `SELECT
  (SELECT version FROM product_component_version WHERE product LIKE 'Oracle Database%' AND ROWNUM = 1) AS version,
  '中文验证' AS text_value,
  CAST(1234567890.12 AS NUMBER(20,2)) AS number_value,
  SYSDATE AS date_value
FROM dual`, password)
	if len(rows) != 1 || len(rows[0]) != 4 {
		t.Fatalf("unexpected Oracle verification result shape: %#v", rows)
	}
	if !strings.Contains(strings.ToLower(toText(rows[0][0])), "11.2") {
		t.Fatalf("target is not Oracle 11g Release 2: version=%q", toText(rows[0][0]))
	}
	if toText(rows[0][1]) != "中文验证" {
		t.Fatalf("Oracle character conversion failed: %q", toText(rows[0][1]))
	}
	withRows := collectIntegrationRows(t, manager, source, `WITH sample AS (
  SELECT LEVEL AS n FROM dual CONNECT BY LEVEL <= 3
) SELECT n FROM sample ORDER BY n`, password)
	if len(withRows) != 3 {
		t.Fatalf("Oracle 11g WITH query failed through server-side limiter: %#v", withRows)
	}
	var limitedRows int
	limitedSummary, err := manager.StreamQuery(context.Background(), source,
		"SELECT LEVEL AS n FROM dual CONNECT BY LEVEL <= 100", 3, func(event StreamEvent) error {
			if event.Type == "rows" {
				limitedRows += len(event.Rows)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("Oracle server-side row limit failed: %s", redactIntegrationError(err, password))
	}
	if limitedRows != 3 || !limitedSummary.Truncated {
		t.Fatalf("Oracle limiter mismatch: rows=%d summary=%#v", limitedRows, limitedSummary)
	}

	if _, err := manager.StreamQuery(context.Background(), source, "SELECT TO_CLOB('x') AS payload FROM dual", 1, func(StreamEvent) error { return nil }); err == nil || !strings.Contains(err.Error(), "DBMS_LOB.SUBSTR") {
		t.Fatalf("direct CLOB must be rejected with bounded-preview guidance: %v", err)
	}
	preview := collectIntegrationRows(t, manager, source, "SELECT DBMS_LOB.SUBSTR(TO_CLOB('中文'), 4000, 1) AS payload FROM dual", password)
	if len(preview) != 1 || toText(preview[0][0]) != "中文" {
		t.Fatalf("bounded CLOB preview failed: %#v", preview)
	}
}

func collectIntegrationRows(t *testing.T, manager *Manager, source Source, query, password string) [][]any {
	t.Helper()
	var rows [][]any
	_, err := manager.StreamQuery(context.Background(), source, query, 20, func(event StreamEvent) error {
		if event.Type == "rows" {
			rows = append(rows, event.Rows...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Oracle query failed: %s", redactIntegrationError(err, password))
	}
	return rows
}

func redactIntegrationError(err error, secret string) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(err.Error(), secret, "[REDACTED]")
}

func toText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(stringValue(value), "\x00", ""))
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}
