package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestIsSensitiveKey 验证敏感字段判定大小写不敏感 + 空格归一。
func TestIsSensitiveKey(t *testing.T) {
	cases := []struct {
		k    string
		want bool
	}{
		{"password", true},
		{"PASSWORD", true},
		{"  Password  ", true},
		{"passwd", true},
		{"secret", true},
		{"private_key", true},
		{"host_key_sha256", true},
		{"authorization", true},
		{"credential", true},
		{"auth_token", true},
		{"api_key", true},
		// 不敏感
		{"system", false},
		{"server", false},
		{"dir", false},
		{"file", false},
		{"result", false},
		{"host", false},          // host 不是 host_key_sha256
		{"token", false},         // 单 token 不是 auth_token
		{"my_password_x", false}, // 部分匹配不算
	}
	for _, c := range cases {
		t.Run(c.k, func(t *testing.T) {
			if got := IsSensitiveKey(c.k); got != c.want {
				t.Errorf("IsSensitiveKey(%q) = %v, want %v", c.k, got, c.want)
			}
		})
	}
}

// TestWriteJSON_NoSensitiveFields 验证空 filter / 无敏感字段正常序列化（B2）。
func TestWriteJSON_NoSensitiveFields(t *testing.T) {
	recs := []Record{
		{
			Time: time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
			Op:   "logs.search",
			KV: map[string]string{
				"system": "信贷",
				"server": "srv-1",
				"result": "ok",
				"hits":   "5",
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, recs); err != nil {
		t.Fatalf("WriteJSON err: %v", err)
	}
	out := buf.String()
	// 反序列化验证
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal err: %v; raw=%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0]["op"] != "logs.search" {
		t.Errorf("op = %v", got[0]["op"])
	}
	if got[0]["system"] != "信贷" {
		t.Errorf("system = %v", got[0]["system"])
	}
	if got[0]["hits"] != "5" {
		t.Errorf("hits = %v", got[0]["hits"])
	}
	// 没敏感字段 → 没有 redacted 字段
	if _, ok := got[0]["redacted"]; ok {
		t.Errorf("should not have redacted field when no sensitive keys; got %v", got[0])
	}
}

// TestWriteJSON_RedactsSensitiveFields 验证敏感字段被脱敏（B2 安全关键）。
func TestWriteJSON_RedactsSensitiveFields(t *testing.T) {
	recs := []Record{
		{
			Time: time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
			Op:   "ssh.test",
			KV: map[string]string{
				"system":          "信贷",
				"server":          "srv-1",
				"result":          "fail",
				"password":        "mySecretPassword",
				"private_key":     "-----BEGIN RSA PRIVATE KEY-----...",
				"host_key_sha256": "AAAA...==",
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, recs); err != nil {
		t.Fatalf("WriteJSON err: %v", err)
	}
	out := buf.String()
	// 验证敏感值不在输出里
	for _, leak := range []string{"mySecretPassword", "BEGIN RSA PRIVATE KEY", "AAAA...=="} {
		if strings.Contains(out, leak) {
			t.Errorf("敏感值 %q 出现在导出 JSON 中: %s", leak, out)
		}
	}
	// 反序列化验证
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal err: %v; raw=%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	// 敏感字段被脱敏 = 不出现 password/private_key/host_key_sha256 顶层字段
	for _, k := range []string{"password", "private_key", "host_key_sha256"} {
		if _, ok := got[0][k]; ok {
			t.Errorf("敏感字段 %q 仍出现在导出对象里: %v", k, got[0])
		}
	}
	// redacted 字段列出被脱敏的 key（按字母序）
	redacted, ok := got[0]["redacted"].([]any)
	if !ok || len(redacted) != 3 {
		t.Fatalf("redacted 应是 3 项数组, got %v", got[0]["redacted"])
	}
	wantSet := map[string]bool{"password": true, "private_key": true, "host_key_sha256": true}
	for _, r := range redacted {
		k, _ := r.(string)
		if !wantSet[k] {
			t.Errorf("redacted 含未预期的 key: %q", k)
		}
	}
	// 业务字段保留
	if got[0]["system"] != "信贷" {
		t.Errorf("system = %v, want 信贷", got[0]["system"])
	}
}

// TestWriteJSON_EmptyRecords 验证空 records 输出有效 JSON（空数组）。
func TestWriteJSON_EmptyRecords(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, nil); err != nil {
		t.Fatalf("WriteJSON err: %v", err)
	}
	out := strings.TrimSpace(buf.String())
	if out != "[]" {
		t.Errorf("空 records 应输出 [], got %q", out)
	}
}

// TestWriteJSON_MultipleRecords 验证多记录按顺序输出（B2）。
func TestWriteJSON_MultipleRecords(t *testing.T) {
	recs := []Record{
		{Time: time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC), Op: "op1", KV: map[string]string{"system": "s1"}},
		{Time: time.Date(2026, 6, 23, 12, 1, 0, 0, time.UTC), Op: "op2", KV: map[string]string{"system": "s2"}},
		{Time: time.Date(2026, 6, 23, 12, 2, 0, 0, time.UTC), Op: "op3", KV: map[string]string{"system": "s3"}},
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, recs); err != nil {
		t.Fatalf("WriteJSON err: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal err: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	ops := []string{got[0]["op"].(string), got[1]["op"].(string), got[2]["op"].(string)}
	for i, want := range []string{"op1", "op2", "op3"} {
		if ops[i] != want {
			t.Errorf("[%d] op = %q, want %q", i, ops[i], want)
		}
	}
}

// TestJSONFilename 验证文件名格式。
func TestJSONFilename(t *testing.T) {
	now := time.Date(2026, 6, 23, 2, 45, 0, 0, time.UTC)
	got := JSONFilename(now)
	want := "audit-2026-06-23-024500.json"
	if got != want {
		t.Errorf("JSONFilename = %q, want %q", got, want)
	}
}

// TestRedactedValue 验证脱敏常量值不会被误改。
func TestRedactedValue(t *testing.T) {
	if JSONRedactedValue != "<redacted>" {
		t.Errorf("JSONRedactedValue 变了: %q", JSONRedactedValue)
	}
}
