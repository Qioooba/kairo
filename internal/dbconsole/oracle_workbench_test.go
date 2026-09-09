package dbconsole

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestOracleWorkbenchFullLifecycle(t *testing.T) {
	mgr, err := NewManager("d:\\kairo\\data")
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	sources := mgr.Store().List()
	var oracleSource *Source
	for _, s := range sources {
		if s.Kind == KindOracle {
			srcCopy := s
			srcCopy.ReadOnly = false
			srcCopy.AllowDDL = true
			oracleSource = &srcCopy
			break
		}
	}
	if oracleSource == nil {
		t.Skip("No Oracle source configured, skipping real Oracle test")
	}

	testRes, err := mgr.Test(context.Background(), *oracleSource)
	if err != nil {
		t.Skipf("Oracle connection test failed (%v), skipping live test", err)
	}
	t.Logf("Connected to Oracle: %s (Version: %s)", oracleSource.Name, testRes.Version)

	ctx := context.Background()

	// Clean up any leftover test table
	_, _ = mgr.StreamQuery(ctx, *oracleSource, "DROP TABLE kairo_wb_test CASCADE CONSTRAINTS", 1, nil)

	// 1. DDL: CREATE TABLE
	t.Run("1_CreateTable", func(t *testing.T) {
		createSQL := `CREATE TABLE kairo_wb_test (
			id NUMBER PRIMARY KEY,
			name VARCHAR2(100),
			memo CLOB,
			data_blob BLOB
		)`
		sum, err := mgr.StreamQuery(ctx, *oracleSource, createSQL, 1, nil)
		if err != nil {
			t.Fatalf("CREATE TABLE failed: %v", err)
		}
		t.Logf("CREATE TABLE success: StatementType=%s ElapsedMS=%d", sum.StatementType, sum.ElapsedMS)
	})

	// 2. DML: INSERT
	t.Run("2_InsertData", func(t *testing.T) {
		insertSQL := `INSERT INTO kairo_wb_test (id, name, memo, data_blob)
			VALUES (1, '初始记录', '这是CLOB大文本数据，用于测试工作台的大对象查看功能。', HEXTORAW('DEADBEEF42'))`
		sum, err := mgr.StreamQuery(ctx, *oracleSource, insertSQL, 1, nil)
		if err != nil {
			t.Fatalf("INSERT failed: %v", err)
		}
		t.Logf("INSERT success: RowsAffected=%d, ElapsedMS=%d", sum.RowsAffected, sum.ElapsedMS)
		if sum.RowsAffected != 1 {
			t.Errorf("Expected 1 row affected, got %d", sum.RowsAffected)
		}
	})

	// 3. DML: UPDATE
	t.Run("3_UpdateData", func(t *testing.T) {
		updateSQL := `UPDATE kairo_wb_test SET name = '修改后名称' WHERE id = 1`
		sum, err := mgr.StreamQuery(ctx, *oracleSource, updateSQL, 1, nil)
		if err != nil {
			t.Fatalf("UPDATE failed: %v", err)
		}
		t.Logf("UPDATE success: RowsAffected=%d, ElapsedMS=%d", sum.RowsAffected, sum.ElapsedMS)
		if sum.RowsAffected != 1 {
			t.Errorf("Expected 1 row affected, got %d", sum.RowsAffected)
		}
	})

	// 4. Query with FOR UPDATE & CLOB/BLOB inspection
	t.Run("4_SelectForUpdateAndLOB", func(t *testing.T) {
		selectSQL := `SELECT id, name, memo, data_blob FROM kairo_wb_test WHERE id = 1 FOR UPDATE`
		var receivedColumns []Column
		var receivedRows [][]any
		sum, err := mgr.StreamQuery(ctx, *oracleSource, selectSQL, 10, func(event StreamEvent) error {
			if event.Type == "meta" {
				receivedColumns = event.Columns
			} else if event.Type == "rows" {
				receivedRows = append(receivedRows, event.Rows...)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("SELECT FOR UPDATE failed: %v", err)
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
		if !strings.Contains(memoMap["text"].(string), "CLOB大文本数据") {
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
		if !strings.EqualFold(blobMap["hex"].(string), "DEADBEEF42") {
			t.Errorf("BLOB hex mismatch, expected DEADBEEF42, got %v", blobMap["hex"])
		}
		t.Logf("-> CLOB and BLOB successfully parsed and verified!")
	})

	// 5. DDL: ALTER TABLE
	t.Run("5_AlterTable", func(t *testing.T) {
		alterSQL := `ALTER TABLE kairo_wb_test ADD (created_time DATE)`
		sum, err := mgr.StreamQuery(ctx, *oracleSource, alterSQL, 1, nil)
		if err != nil {
			t.Fatalf("ALTER TABLE failed: %v", err)
		}
		t.Logf("ALTER TABLE success: ElapsedMS=%d", sum.ElapsedMS)
	})

	// 6. DDL: DROP TABLE
	t.Run("6_DropTable", func(t *testing.T) {
		dropSQL := `DROP TABLE kairo_wb_test CASCADE CONSTRAINTS`
		sum, err := mgr.StreamQuery(ctx, *oracleSource, dropSQL, 1, nil)
		if err != nil {
			t.Fatalf("DROP TABLE failed: %v", err)
		}
		t.Logf("DROP TABLE success: ElapsedMS=%d", sum.ElapsedMS)
	})
	_ = os.Stdout
}
