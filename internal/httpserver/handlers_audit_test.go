package httpserver

import (
	"bytes"
	"encoding/csv"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAuditExportCSV_Basic 验证 /api/audit/export.csv 返回合法 CSV 附件。
func TestAuditExportCSV_Basic(t *testing.T) {
	srv, _, al, _ := newTestServer(t)
	_ = al
	// 写几条记录（走 JSONL，VerifyItem 的 format）
	al.Write("ssh.test", "system", "信贷生产", "server", "mock-1", "result", "ok")
	al.Write("logs.search", "system", "信贷生产", "server", "mock-1", "query", "Exception", "result", "fail", "err", "boom")

	w := doRequest(srv, "GET", "/api/audit/export.csv", nil)
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type 应是 text/csv，实际 %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition 应含 attachment，实际 %q", cd)
	}

	// 跳过 BOM 后用 csv.Reader 解
	body := w.Body.Bytes()
	if len(body) < 3 || body[0] != 0xEF || body[1] != 0xBB || body[2] != 0xBF {
		t.Error("应有 UTF-8 BOM 头（Excel 友好）")
	}
	r := csv.NewReader(bytes.NewReader(body[3:]))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("CSV 解析失败: %v", err)
	}
	if len(records) < 1 {
		t.Fatal("至少应有 header 行")
	}
	// header 包含 ts/op/system/server/result...
	header := records[0]
	wantHeaders := []string{"ts", "op", "system", "server", "result"}
	for _, w := range wantHeaders {
		found := false
		for _, h := range header {
			if h == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("header 应含 %q，实际: %v", w, header)
		}
	}
	// 至少 1 行数据
	if len(records) < 2 {
		t.Errorf("期望至少 1 行数据，实际 %d", len(records)-1)
	}
}

// TestAuditExportCSV_Filtered 验证 op= 过滤生效。
func TestAuditExportCSV_Filtered(t *testing.T) {
	srv, _, al, _ := newTestServer(t)
	al.Write("ssh.test", "result", "ok")
	al.Write("logs.list", "result", "ok")
	al.Write("ssh.test", "result", "fail")

	w := doRequest(srv, "GET", "/api/audit/export.csv?op=ssh.test", nil)
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d", w.Code)
	}
	r := csv.NewReader(bytes.NewReader(w.Body.Bytes()[3:]))
	records, _ := r.ReadAll()
	if len(records) != 3 { // header + 2 行 ssh.test
		t.Errorf("应有 3 行（1 header + 2 ssh.test），实际 %d", len(records))
	}
}

// TestAuditExportCSV_Empty 验证没有审计记录时也能正常返回（只有 header）。
func TestAuditExportCSV_Empty(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	// 不写任何记录
	w := doRequest(srv, "GET", "/api/audit/export.csv", nil)
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d", w.Code)
	}
	r := csv.NewReader(bytes.NewReader(w.Body.Bytes()[3:]))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Errorf("空记录应只返 header，实际 %d 行", len(records))
	}
}

// TestAuditExportCSV_WrongMethod 验证 405。
func TestAuditExportCSV_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/audit/export.csv", nil)
	if w.Code != 405 {
		t.Errorf("期望 405，得到 %d", w.Code)
	}
	// 兜底：httptest.NewRecorder 默认 Code=200
	_ = httptest.NewRecorder()
}
