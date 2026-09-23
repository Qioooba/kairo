//go:build integration

// 真实 Oracle LOB 集成测试 (QA-01: 从默认单测中移出)
//
// 修复对象: 旧版本把 TestLOBRealOracle 直接放在默认测试包里, 只用环境变量
// KAIRO_LOB_REVIEW_DATA 做跳过判断。表达式虽然会 skip, 但它仍然编译进默认测试
// 二进制, 并且会 NewManager(用户提供的目录) 读取真实数据源、连接真实 Oracle 实例。
// 现在默认 `go test ./...` 根本不编译本文件。
//
// 双条件门禁:
//  1. 编译门禁: -tags=integration
//  2. 显式 DSN: KAIRO_LOB_REVIEW_DATA 指向专用 XE 实验目录 (内含 kairo_ro 只读来源)
//
// 运行示例 (只读, 不执行 DDL):
//
//	KAIRO_LOB_REVIEW_DATA=/path/to/lab/data \
//	go test -tags=integration -run TestLOBRealOracle -v ./internal/dbconsole/
package dbconsole

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestLOBRealOracle(t *testing.T) {
	dir := os.Getenv("KAIRO_LOB_REVIEW_DATA")
	if dir == "" {
		t.Skip("set KAIRO_LOB_REVIEW_DATA; seed tests/oracle-lob-review.sql in local XE")
	}
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	var src Source
	for _, s := range m.Store().List() {
		if s.Kind == KindOracle && s.Host == "127.0.0.1" && s.Username == "kairo_ro" {
			src = s
			break
		}
	}
	if src.ID == "" {
		t.Fatal("local XE lab source missing")
	}
	src.OracleDriver = os.Getenv("KAIRO_LOB_REVIEW_DRIVER")
	src.OracleLibDir = os.Getenv("KAIRO_LOB_REVIEW_LIBDIR")
	src.MaxOpenConnections = 1
	src.MaxIdleConnections = 1
	if _, err := m.Test(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	t.Logf("REAL backend=%s single-connection pool", m.OracleBackendName(src))
	query := "SELECT * FROM KAIRO_LAB.KAIRO_LOB_REVIEW_0908"
	var rows [][]any
	_, err = m.StreamQuery(context.Background(), src, query, 20, func(e StreamEvent) error {
		if e.Type == "rows" {
			rows = append(rows, e.Rows...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 9 {
		t.Fatalf("rows=%d", len(rows))
	}
	for i, row := range rows {
		for _, col := range []int{1, 3, 4} {
			if i == 0 {
				if row[col] != nil {
					t.Fatal("NULL LOB lost")
				}
				continue
			}
			cell, ok := row[col].(map[string]any)
			if !ok {
				t.Fatalf("not a LOB cell: %T", row[col])
			}
			_, ref, err := VerifySignedLOBToken(cell["token"].(string))
			if err != nil {
				t.Fatal(err)
			}
			if ref.UseRowID {
				t.Fatal("primary key must take precedence")
			}
			var got bytes.Buffer
			if err = m.StreamLOB(context.Background(), src, *ref, &got); err != nil {
				t.Fatalf("row=%d col=%d: %v", i+1, col, err)
			}
			counts := []int{0, 0, 1, 512, 4096, 8192, 16384, 65536, 131072}
			want := []byte(strings.Repeat("中文😀\nlob-", counts[i]))
			if col == 3 {
				want = bytes.Repeat([]byte{0, 10, 13, 255, 128, 65, 66, 67}, counts[i])
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("row=%d col=%d bytes=%d want=%d", i+1, col, got.Len(), len(want))
			}
		}
	}
	t.Log("all 27 cells: NULL/EMPTY/multibyte/emoji/binary and >1MB exact content verified")
	t.Run("raw SQL operators", func(t *testing.T) {
		for _, sql := range []string{"SELECT * FROM KAIRO_LAB.KAIRO_LOB_REVIEW_0908 WHERE ID>=1 AND ID<=2", "SELECT 1+2 AS N FROM dual", "SELECT * FROM KAIRO_LAB.KAIRO_LOB_REVIEW_0908 ORDER BY ID DESC"} {
			if _, err := m.StreamQuery(context.Background(), src, sql, 2, nil); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("transaction locator", func(t *testing.T) {
		sid := "lob_review_transaction"
		_, err := m.StreamSessionQueryPage(context.Background(), src, "UPDATE KAIRO_LAB.KAIRO_LOB_REVIEW_0908 SET BODY='uncommitted' WHERE ID=3", 1, 20, sid, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer m.ControlSessionTransaction(context.Background(), src, sid, "ROLLBACK")
		ref := LOBRef{Owner: "KAIRO_LAB", Table: "KAIRO_LOB_REVIEW_0908", Column: "BODY", PrimaryKey: []string{"ID"}, Keys: map[string]any{"ID": 3}, SessionID: sid}
		var b bytes.Buffer
		if err := m.StreamLOB(context.Background(), src, ref, &b); err != nil {
			t.Fatal(err)
		}
		if b.String() != "uncommitted" {
			t.Fatal("different session read")
		}
		if _, err := m.ControlSessionTransaction(context.Background(), src, sid, "ROLLBACK"); err != nil {
			t.Fatal(err)
		}
		if err := m.StreamLOB(context.Background(), src, ref, io.Discard); err == nil {
			t.Fatal("expired transaction accepted")
		}
	})
	t.Run("writer failure and cancellation", func(t *testing.T) {
		ref := LOBRef{Owner: "KAIRO_LAB", Table: "KAIRO_LOB_REVIEW_0908", Column: "BODY", PrimaryKey: []string{"ID"}, Keys: map[string]any{"ID": 9}}
		w := &failLOBWriter{}
		if err := m.StreamLOB(context.Background(), src, ref, w); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("writer error: %v", err)
		}
		if w.calls != 1 {
			t.Fatalf("stream replayed: %d writes", w.calls)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := m.StreamLOB(ctx, src, ref, io.Discard); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		ctx, cancel = context.WithCancel(context.Background())
		if err := m.StreamLOB(ctx, src, ref, cancelLOBWriter{cancel: cancel}); !errors.Is(err, context.Canceled) {
			t.Fatalf("mid-stream cancellation: %v", err)
		}
	})
}

type failLOBWriter struct{ calls int }

type cancelLOBWriter struct{ cancel context.CancelFunc }

func (w cancelLOBWriter) Write(p []byte) (int, error) { w.cancel(); return len(p), nil }

func (w *failLOBWriter) Write(p []byte) (int, error) { w.calls++; return 0, io.ErrClosedPipe }
