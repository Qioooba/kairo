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

// ============ B2：JSON 导出集成测试 ============

// TestAuditExportJSON_Basic 验证 /api/audit/export.json 返回合法 JSON 附件。
func TestAuditExportJSON_Basic(t *testing.T) {
	srv, _, al, _ := newTestServer(t)
	al.Write("ssh.test", "system", "信贷生产", "server", "mock-1", "result", "ok")
	al.Write("logs.search", "system", "信贷生产", "server", "mock-1", "query", "Exception", "result", "fail")

	w := doRequest(srv, "GET", "/api/audit/export.json", nil)
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type 应是 application/json，实际 %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition 应含 attachment，实际 %q", cd)
	}
	// 反序列化
	var arr []map[string]any
	if err := jsonDecodeArr(w.Body.Bytes(), &arr); err != nil {
		t.Fatalf("JSON 解析失败: %v; raw=%s", err, w.Body.String())
	}
	if len(arr) < 2 {
		t.Errorf("期望至少 2 条，实际 %d", len(arr))
	}
	for _, rec := range arr {
		if rec["op"] == nil {
			t.Error("每条记录应有 op 字段")
		}
		if rec["ts"] == nil {
			t.Error("每条记录应有 ts 字段")
		}
	}
}

// TestAuditExportJSON_RedactsPassword 验证 password 字段在 JSON 导出中被脱敏（B2 安全）。
//
// 走完整 HTTP → handler → audit.WriteJSON 路径，确认脱敏在出口生效。
func TestAuditExportJSON_RedactsPassword(t *testing.T) {
	srv, _, al, _ := newTestServer(t)
	const leakMarker = "PASSWORD_SHOULD_NOT_APPEAR_IN_EXPORT_xyz"
	al.Write("ssh.test", "system", "信贷", "server", "mock-1", "result", "fail", "password", leakMarker)

	w := doRequest(srv, "GET", "/api/audit/export.json", nil)
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, leakMarker) {
		t.Fatalf("password 值 %q 出现在导出 JSON 中: %s", leakMarker, body)
	}
	var arr []map[string]any
	if err := jsonDecodeArr(w.Body.Bytes(), &arr); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if len(arr) < 1 {
		t.Fatal("应至少 1 条记录")
	}
	if _, hasPassword := arr[0]["password"]; hasPassword {
		t.Errorf("password 顶层字段不应出现，被脱敏后该键消失")
	}
	// 验证 redacted 数组列出被脱敏的 key
	redacted, ok := arr[0]["redacted"].([]any)
	if !ok {
		t.Fatalf("redacted 字段应为数组，实际 %v", arr[0]["redacted"])
	}
	foundPassword := false
	for _, k := range redacted {
		if k == "password" {
			foundPassword = true
		}
	}
	if !foundPassword {
		t.Errorf("redacted 应含 password，实际: %v", redacted)
	}
}

// TestAuditExportJSON_Filter 验证 op= 过滤生效（B2 与 CSV 行为一致）。
func TestAuditExportJSON_Filter(t *testing.T) {
	srv, _, al, _ := newTestServer(t)
	al.Write("ssh.test", "result", "ok")
	al.Write("logs.list", "result", "ok")
	al.Write("ssh.test", "result", "fail")

	w := doRequest(srv, "GET", "/api/audit/export.json?op=ssh.test", nil)
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d", w.Code)
	}
	var arr []map[string]any
	if err := jsonDecodeArr(w.Body.Bytes(), &arr); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if len(arr) != 2 {
		t.Errorf("op=ssh.test 应只返 2 条，实际 %d", len(arr))
	}
	for _, rec := range arr {
		if rec["op"] != "ssh.test" {
			t.Errorf("过滤失效: 出现 op=%v", rec["op"])
		}
	}
}

// TestAuditExportJSON_Empty 验证空审计也能正常返回 []。
func TestAuditExportJSON_Empty(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/audit/export.json", nil)
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d", w.Code)
	}
	var arr []map[string]any
	if err := jsonDecodeArr(w.Body.Bytes(), &arr); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if len(arr) != 0 {
		t.Errorf("空记录应返 []，实际 len=%d", len(arr))
	}
}

// TestAuditExportJSON_WrongMethod 验证 405。
func TestAuditExportJSON_WrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/audit/export.json", nil)
	if w.Code != 405 {
		t.Errorf("期望 405，得到 %d", w.Code)
	}
}
