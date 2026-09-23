//go:build integration

// 真实 Oracle 工作台全生命周期集成测试 (QA-01 修复)
//
// 修复对象: 旧版本在默认单测里从固定开发目录 d:\kairo\data 读取用户数据源,
// 复制后强制 ReadOnly=false / AllowDDL=true, 并无条件执行
// `DROP TABLE kairo_wb_test CASCADE CONSTRAINTS`。开发者 Windows 机器上一条
// `go test ./...` 就会对真实库执行删表 DDL。
//
// 现在有四道独立门禁, 缺任何一道都不执行:
//  1. 编译门禁: 独立 build tag `integration` —— 默认 `go test ./...` 根本不编译本文件
//  2. 总开关:   KAIRO_INTEGRATION_ORACLE=1
//  3. DSN:      KAIRO_TEST_ORACLE_HOST / USER / PASSWORD / SERVICE (专用测试库, 不读默认配置目录)
//  4. DDL 授权: KAIRO_INTEGRATION_ORACLE_ALLOW_DDL=1 (本文件会建表/改表/删表)
//
// 数据隔离: 临时表名带本次 run_id (KAIRO_INTEGRATION_RUN_ID 或随机生成);
// 所有本次创建的对象记入 manifest, 清理只删 manifest 内的对象, 不再无条件 DROP。
//
// 运行示例:
//
//	KAIRO_INTEGRATION_ORACLE=1 KAIRO_INTEGRATION_ORACLE_ALLOW_DDL=1 \
//	KAIRO_TEST_ORACLE_HOST=127.0.0.1 KAIRO_TEST_ORACLE_USER=kairo_test \
//	KAIRO_TEST_ORACLE_PASSWORD=... KAIRO_TEST_ORACLE_SERVICE=XE \
//	go test -tags=integration -run TestOracleWorkbenchFullLifecycle -v ./internal/dbconsole/
package dbconsole

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"kairo/internal/credentials"
)

const (
	// integrationOracleEnv 真实 Oracle 集成的总开关。
	integrationOracleEnv = "KAIRO_INTEGRATION_ORACLE"
	// integrationOracleDDLEnv DDL 单独授权开关 —— 只读 DSN 不可能被误删表。
	integrationOracleDDLEnv = "KAIRO_INTEGRATION_ORACLE_ALLOW_DDL"
	// integrationRunIDEnv 可选的 run_id 覆盖, 便于 CI 关联同一次运行。
	integrationRunIDEnv = "KAIRO_INTEGRATION_RUN_ID"
)

// TestOracleWorkbenchFullLifecycle 工作台读写全链路 (建表/插入/更新/查询/改表/删表)。
func TestOracleWorkbenchFullLifecycle(t *testing.T) {
	manager, source, password := newOracleWorkbenchHarness(t, true)

	ctx := context.Background()
	table := integrationTempTableName(t, "WB")

	// created 是本次运行的对象 manifest: 只有这里登记过的对象才会被清理。
	created := make([]string, 0, 1)
	t.Cleanup(func() {
		cleanupOracleManifest(t, manager, source, password, created)
	})

	// 1. DDL: CREATE TABLE
	t.Run("1_CreateTable", func(t *testing.T) {
		createSQL := `CREATE TABLE ` + table + ` (
			id NUMBER PRIMARY KEY,
			name VARCHAR2(100),
			memo CLOB,
			data_blob BLOB
		)`
		sum, err := streamOracleWithDeadline(t, ctx, manager, source, password, createSQL, 1)
		if err != nil {
			t.Fatalf("CREATE TABLE failed: %s", redactIntegrationError(err, password))
		}
		created = append(created, table)
		t.Logf("CREATE TABLE success: StatementType=%s ElapsedMS=%d", sum.StatementType, sum.ElapsedMS)
	})

	// 2. DML: INSERT
	t.Run("2_InsertData", func(t *testing.T) {
		insertSQL := `INSERT INTO ` + table + ` (id, name, memo, data_blob)
			VALUES (1, '初始记录', '这是CLOB大文本数据，用于测试工作台的大对象查看功能。', HEXTORAW('DEADBEEF42'))`
		sum, err := streamOracleWithDeadline(t, ctx, manager, source, password, insertSQL, 1)
		if err != nil {
			t.Fatalf("INSERT failed: %s", redactIntegrationError(err, password))
		}
		t.Logf("INSERT success: RowsAffected=%d, ElapsedMS=%d", sum.RowsAffected, sum.ElapsedMS)
		if sum.RowsAffected != 1 {
			t.Errorf("Expected 1 row affected, got %d", sum.RowsAffected)
		}
	})

	// 3. DML: UPDATE
	t.Run("3_UpdateData", func(t *testing.T) {
		updateSQL := `UPDATE ` + table + ` SET name = '修改后名称' WHERE id = 1`
		sum, err := streamOracleWithDeadline(t, ctx, manager, source, password, updateSQL, 1)
		if err != nil {
			t.Fatalf("UPDATE failed: %s", redactIntegrationError(err, password))
		}
		t.Logf("UPDATE success: RowsAffected=%d, ElapsedMS=%d", sum.RowsAffected, sum.ElapsedMS)
		if sum.RowsAffected != 1 {
			t.Errorf("Expected 1 row affected, got %d", sum.RowsAffected)
		}
	})

	// 4. Query with FOR UPDATE & CLOB/BLOB inspection
	t.Run("4_SelectForUpdateAndLOB", func(t *testing.T) {
		selectSQL := `SELECT id, name, memo, data_blob FROM ` + table + ` WHERE id = 1 FOR UPDATE`
		var receivedColumns []Column
		var receivedRows [][]any
		sum, err := manager.StreamQuery(ctx, source, selectSQL, 10, func(event StreamEvent) error {
			if event.Type == "meta" {
				receivedColumns = event.Columns
			} else if event.Type == "rows" {
				receivedRows = append(receivedRows, event.Rows...)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("SELECT FOR UPDATE failed: %s", redactIntegrationError(err, password))
		}
		t.Logf("SELECT FOR UPDATE success: Rows=%d, Columns=%d, StatementType=%s", len(receivedRows), len(receivedColumns), sum.StatementType)
		if len(receivedRows) != 1 {
			t.Fatalf("Expected 1 row, got %d", len(receivedRows))
		}
		row := receivedRows[0]
		t.Logf("Row data: ID=%v, Name=%v, Memo=%T(%+v), DataBlob=%T(%+v)", row[0], row[1], row[2], row[2], row[3], row[3])

		// Verify CLOB structure
		memoMap, ok := row[2].(map[string]any)
		if !ok {
			t.Fatalf("Expected CLOB to be map[string]any, got %T: %v", row[2], row[2])
		}
		if memoMap["kind"] != "clob" {
			t.Errorf("Expected memo kind=clob, got %v", memoMap["kind"])
		}
		if text, _ := memoMap["text"].(string); !strings.Contains(text, "CLOB大文本数据") {
			t.Errorf("CLOB text content mismatch: %v", memoMap["text"])
		}

		// Verify BLOB structure
		blobMap, ok := row[3].(map[string]any)
		if !ok {
			t.Fatalf("Expected BLOB to be map[string]any, got %T: %v", row[3], row[3])
		}
		if blobMap["kind"] != "blob" {
			t.Errorf("Expected blob kind=blob, got %v", blobMap["kind"])
		}
		hexValue, _ := blobMap["hex"].(string)
		if !strings.EqualFold(hexValue, "DEADBEEF42") {
			t.Errorf("BLOB hex mismatch, expected DEADBEEF42, got %v", blobMap["hex"])
		}
		t.Logf("-> CLOB and BLOB successfully parsed and verified!")
	})

	// 5. DDL: ALTER TABLE
	t.Run("5_AlterTable", func(t *testing.T) {
		alterSQL := `ALTER TABLE ` + table + ` ADD (created_time DATE)`
		sum, err := streamOracleWithDeadline(t, ctx, manager, source, password, alterSQL, 1)
		if err != nil {
			t.Fatalf("ALTER TABLE failed: %s", redactIntegrationError(err, password))
		}
		t.Logf("ALTER TABLE success: ElapsedMS=%d", sum.ElapsedMS)
	})

	// 6. DDL: DROP TABLE (显式删除并同步 manifest)
	t.Run("6_DropTable", func(t *testing.T) {
		dropSQL := `DROP TABLE ` + table + ` CASCADE CONSTRAINTS`
		sum, err := streamOracleWithDeadline(t, ctx, manager, source, password, dropSQL, 1)
		if err != nil {
			t.Fatalf("DROP TABLE failed: %s", redactIntegrationError(err, password))
		}
		created = removeManifestEntry(created, table)
		t.Logf("DROP TABLE success: ElapsedMS=%d", sum.ElapsedMS)
	})
}

// newOracleWorkbenchHarness 校验四道门禁并装配"只用专用测试 DSN + 临时凭据目录"的环境。
//
// needDDL 为 true 时额外要求 KAIRO_INTEGRATION_ORACLE_ALLOW_DDL=1; 未授权则 skip
// 而不是 fail —— 只读 DSN 跑集成本来就是合法的降级场景。
func newOracleWorkbenchHarness(t *testing.T, needDDL bool) (*Manager, Source, string) {
	t.Helper()

	if os.Getenv(integrationOracleEnv) != "1" {
		t.Skipf("真实 Oracle 集成未启用 (需要 -tags=integration 且 %s=1)", integrationOracleEnv)
	}
	if needDDL && os.Getenv(integrationOracleDDLEnv) != "1" {
		t.Skipf("本用例会执行 DDL, 需要显式授权 %s=1", integrationOracleDDLEnv)
	}

	host := os.Getenv("KAIRO_TEST_ORACLE_HOST")
	user := os.Getenv("KAIRO_TEST_ORACLE_USER")
	password := os.Getenv("KAIRO_TEST_ORACLE_PASSWORD")
	service := os.Getenv("KAIRO_TEST_ORACLE_SERVICE")
	if host == "" || user == "" || password == "" || service == "" {
		t.Fatalf("需要专用测试 DSN: KAIRO_TEST_ORACLE_HOST/USER/PASSWORD/SERVICE 都必须设置 " +
			"(不会回退到开发机固定目录或用户默认配置目录)")
	}
	port := 1521
	if raw := os.Getenv("KAIRO_TEST_ORACLE_PORT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("KAIRO_TEST_ORACLE_PORT 必须是整数: %v", err)
		}
		port = parsed
	}
	connectBy := os.Getenv("KAIRO_TEST_ORACLE_CONNECT_BY")
	if connectBy == "" {
		connectBy = "service_name"
	}

	// 凭据落在测试自己的临时目录, 不读默认配置目录、AppData 或开发机固定路径。
	dir := t.TempDir()
	credentials.SetMode(credentials.ModeFile)
	if err := credentials.Init(dir, ""); err != nil {
		t.Fatal(redactIntegrationError(err, password))
	}
	t.Cleanup(func() { credentials.SetMode(credentials.ModeKeyring) })

	source := Source{
		ID:   "oracle_workbench_integration",
		Name: "Oracle workbench integration",
		Kind: KindOracle,
		Host: host, Port: port, Username: user,
		OracleConnectBy: connectBy, OracleService: service,
		OracleClientCharset: os.Getenv("KAIRO_TEST_ORACLE_CHARSET"),
		QueryTimeoutSeconds: 30, MaxRows: 50, MaxResultBytes: 4 << 20,
		MaxOpenConnections: 2, MaxIdleConnections: 1, ConnectionMaxMinutes: 5,
	}
	source.Defaults()
	// 写入能力必须显式声明 (Defaults 会把未配置的来源置为只读)。
	source.ReadOnly = false
	source.ReadOnlyConfigured = true
	source.AllowDDL = needDDL
	source.Environment = "development"
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

	connCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := manager.Test(connCtx, source); err != nil {
		t.Fatalf("Oracle 连接失败: %s", redactIntegrationError(err, password))
	}
	return manager, source, password
}

// integrationTempTableName 生成带 run_id 的临时表名, 保证不同运行/不同人互不覆盖。
//
// Oracle 未加引号标识符上限 30 字符, 前缀 KAIRO_<kind>_ 后 run_id 固定 8 位十六进制。
func integrationTempTableName(t *testing.T, kind string) string {
	t.Helper()
	runID := strings.ToUpper(strings.TrimSpace(os.Getenv(integrationRunIDEnv)))
	runID = sanitizeOracleIdentifier(runID)
	if runID == "" {
		buf := make([]byte, 4)
		if _, err := rand.Read(buf); err != nil {
			t.Fatalf("生成 run_id 失败: %v", err)
		}
		runID = strings.ToUpper(hex.EncodeToString(buf))
	}
	if len(runID) > 8 {
		runID = runID[:8]
	}
	name := "KAIRO_" + sanitizeOracleIdentifier(kind) + "_" + runID
	if len(name) > 30 {
		name = name[:30]
	}
	return name
}

// sanitizeOracleIdentifier 只保留 A-Z0-9_, 其余丢弃 (防注入 + 防超长)。
func sanitizeOracleIdentifier(raw string) string {
	var sb strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			sb.WriteRune(r)
		case r >= 'a' && r <= 'z':
			sb.WriteRune(r - 32)
		}
	}
	return sb.String()
}

// streamOracleWithDeadline 每个请求都带独立 deadline, 避免集成环境挂死。
func streamOracleWithDeadline(t *testing.T, parent context.Context, manager *Manager, source Source, password, query string, maxRows int) (QuerySummary, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	sum, err := manager.StreamQuery(ctx, source, query, maxRows, nil)
	if err != nil {
		return sum, err
	}
	return sum, nil
}

// cleanupOracleManifest 只清理 manifest 内登记的对象 (倒序), 绝不执行无条件 DROP。
func cleanupOracleManifest(t *testing.T, manager *Manager, source Source, password string, created []string) {
	t.Helper()
	for i := len(created) - 1; i >= 0; i-- {
		table := created[i]
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, err := manager.StreamQuery(ctx, source, `DROP TABLE `+table+` CASCADE CONSTRAINTS`, 1, nil)
		cancel()
		if err != nil {
			t.Errorf("清理本次创建的 %s 失败 (需人工处理): %s", table, redactIntegrationError(err, password))
			continue
		}
		t.Logf("cleaned up integration table %s", table)
	}
}

// removeManifestEntry 返回去掉 target 的新切片 (不复用底层数组, 避免与 cleanup 闭包别名)。
func removeManifestEntry(entries []string, target string) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e != target {
			out = append(out, e)
		}
	}
	return out
}
