//go:build integration

// 元数据链路耗时拆解 —— 真实 Oracle 实测。
//
// 目的：把"首批结果前的等待"拆成 Fields / Indexes / 普通堆表确认三段，
// 分别测冷缓存与热缓存耗时，用真实数字决定预算取值，而不是靠推断。
//
// 运行：
//
//	KAIRO_INTEGRATION_ORACLE=1 \
//	KAIRO_TEST_ORACLE_HOST=127.0.0.1 KAIRO_TEST_ORACLE_USER=kairo_ro \
//	KAIRO_TEST_ORACLE_PASSWORD=... KAIRO_TEST_ORACLE_SERVICE=XEPDB1 \
//	go test -tags=integration -run TestOracleMetadataBreakdown -v ./internal/dbconsole/
package dbconsole

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// TestOracleMetadataBreakdown 逐个对象测量元数据三段的冷/热耗时。
func TestOracleMetadataBreakdown(t *testing.T) {
	objects := []string{"EMPLOYEES", "DEPARTMENTS", "EMPLOYEE_DIRECTORY"}

	for _, object := range objects {
		object := object
		t.Run(object, func(t *testing.T) {
			// 每次都用全新 Manager，保证元数据缓存是冷的；连接池由 harness 的连接测试预热。
			manager, source, _ := newOracleWorkbenchHarness(t, false)
			ctx := context.Background()

			type step struct {
				name string
				run  func() (string, error)
			}
			steps := []step{
				{"Fields", func() (string, error) {
					fields, err := manager.Fields(ctx, source, "KAIRO_LAB", object)
					if err != nil {
						return "", err
					}
					return strconv.Itoa(len(fields)) + " fields", nil
				}},
				{"Indexes", func() (string, error) {
					indexes, err := manager.Indexes(ctx, source, "KAIRO_LAB", object)
					if err != nil {
						return "", err
					}
					return strconv.Itoa(len(indexes)) + " indexes", nil
				}},
				{"isOracleHeapTable", func() (string, error) {
					isHeap, known := manager.isOracleHeapTable(ctx, source, "KAIRO_LAB", object)
					return "isHeap=" + strconv.FormatBool(isHeap) + " known=" + strconv.FormatBool(known), nil
				}},
			}

			for _, s := range steps {
				started := time.Now()
				detail, err := s.run()
				cold := time.Since(started)
				if err != nil {
					t.Errorf("%s 冷调用失败: %v", s.name, err)
					continue
				}
				started = time.Now()
				warmDetail, warmErr := s.run()
				warm := time.Since(started)
				if warmErr != nil {
					t.Errorf("%s 热调用失败: %v", s.name, warmErr)
					continue
				}
				t.Logf("%-18s 冷=%6dms 热=%5dms  %s / %s",
					s.name, cold.Milliseconds(), warm.Milliseconds(), detail, warmDetail)
			}
		})
	}
}

// TestOracleGridPrepColdTotal 测量 prepareGridQuery 在冷缓存、充足预算下的真实总耗时。
// 预算给到 30s，是为了看到"不加预算裁剪时到底要读多久"，从而判断现有 200~1500ms 预算
// 是否会丢弃本来能成功的读取。
func TestOracleGridPrepColdTotal(t *testing.T) {
	queries := []struct {
		name string
		sql  string
	}{
		{"单表 主键完整投影", `SELECT id, employee_no, display_name FROM ` + labSchema + `.employees`},
		{"单表 无主键投影（ROWID 路径）", `SELECT display_name, monthly_salary FROM ` + labSchema + `.employees`},
		{"关联视图", `SELECT * FROM ` + labSchema + `.employee_directory`},
		{"聚合", `SELECT COUNT(*) FROM ` + labSchema + `.employees`},
	}

	for _, c := range queries {
		c := c
		t.Run(c.name, func(t *testing.T) {
			manager, source, _ := newOracleWorkbenchHarness(t, false)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			started := time.Now()
			prep := manager.prepareGridQuery(ctx, source, c.sql, true)
			elapsed := time.Since(started)

			fields, reason, rowID := 0, "", false
			heapKnown, heapTable := false, false
			if prep != nil {
				fields, reason = len(prep.Fields), prep.Reason
				rowID = prep.RowIDSQL != ""
				heapKnown, heapTable = prep.HeapKnown, prep.HeapTable
			}
			t.Logf("冷缓存 prepareGridQuery 总耗时=%dms fields=%d heap_known=%v heap_table=%v rowid_rewrite=%v reason=%q",
				elapsed.Milliseconds(), fields, heapKnown, heapTable, rowID, reason)
		})
	}
}
