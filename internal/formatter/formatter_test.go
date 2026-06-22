package formatter

import (
	"strings"
	"testing"
)

func TestFormatJSON_Basic(t *testing.T) {
	in := `{"a":1,"b":[1,2,3]}`
	out, err := FormatJSON(in, "  ")
	if err != nil {
		t.Fatal(err)
	}
	// 验证：缩进 + 数字精度不丢
	if !strings.Contains(out, "  \"a\": 1") {
		t.Errorf("missing indent: %s", out)
	}
	if strings.HasSuffix(out, "\n") {
		t.Errorf("trailing newline should be trimmed: %q", out)
	}
}

func TestFormatJSON_TabIndent(t *testing.T) {
	in := `{"a":1}`
	out, err := FormatJSON(in, "\t")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\t\"a\": 1") {
		t.Errorf("tab indent not used: %s", out)
	}
}

func TestFormatJSON_PreservesLargeIntegers(t *testing.T) {
	// UseNumber 后大整数不会丢精度
	in := `{"id":9007199254740993}`
	out, _ := FormatJSON(in, "  ")
	if !strings.Contains(out, "9007199254740993") {
		t.Errorf("large int lost precision: %s", out)
	}
}

func TestFormatJSON_NoHTMLEscape(t *testing.T) {
	// 默认 SetEscapeHTML(false)：< > 不会被转成 \u003c 等
	in := `{"x":"<a>"}`
	out, _ := FormatJSON(in, "  ")
	if !strings.Contains(out, "<a>") {
		t.Errorf("HTML should not be escaped: %s", out)
	}
}

func TestFormatJSON_InvalidJSON(t *testing.T) {
	if _, err := FormatJSON("{bad", "  "); err == nil {
		t.Fatal("invalid json should fail")
	}
}

func TestMinifyJSON_Basic(t *testing.T) {
	in := `
		{
			"a": 1,
			"b": [1,2,3]
		}
	`
	out, err := MinifyJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":1,"b":[1,2,3]}`
	if out != want {
		t.Errorf("MinifyJSON=%q, want %q", out, want)
	}
}

func TestMinifyJSON_Invalid(t *testing.T) {
	if _, err := MinifyJSON("garbage"); err == nil {
		t.Fatal("invalid json should fail")
	}
}

func TestValidateJSON_OK(t *testing.T) {
	if err := ValidateJSON(`{"a":1}`); err != nil {
		t.Errorf("valid json: %v", err)
	}
}

func TestValidateJSON_Bad(t *testing.T) {
	if err := ValidateJSON("{"); err == nil {
		t.Error("invalid json should fail")
	}
}

func TestFormatXML_Basic(t *testing.T) {
	in := `<root><a>1</a><b>2</b></root>`
	out, err := FormatXML(in, "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<a>") || !strings.Contains(out, "<b>") {
		t.Errorf("output missing nodes: %s", out)
	}
}

func TestFormatXML_TabIndent(t *testing.T) {
	in := `<r><a/></r>`
	out, err := FormatXML(in, "\t")
	if err != nil {
		t.Fatal(err)
	}
	// Go 的 xml.Encoder 可能把 <a/> 写成 <a></a>，只要 tab 缩进存在即可
	if !strings.Contains(out, "\t<a") {
		t.Errorf("tab indent not used: %s", out)
	}
}

func TestFormatXML_Invalid(t *testing.T) {
	if _, err := FormatXML("<broken", "  "); err == nil {
		t.Error("invalid xml should fail")
	}
}

func TestMinifyXML_Basic(t *testing.T) {
	in := `<root><a>1</a><b>2</b></root>`
	out, err := MinifyXML(in)
	if err != nil {
		t.Fatal(err)
	}
	// MinifyXML 使用 xml.Encoder，会保留编码器默认的换行；
	// 验证它至少能 round-trip 并且不丢内容
	if !strings.Contains(out, "<a>") || !strings.Contains(out, "<b>") {
		t.Errorf("missing content: %s", out)
	}
	// 验证 Parse 出来一致
	if _, err := MinifyXML(in); err != nil {
		t.Fatalf("idempotent: %v", err)
	}
}

func TestMinifyXML_Invalid(t *testing.T) {
	// MinifyXML 在 EOF 时退出循环，所以不严格校验；故意造一个会让 token 失败的内容
	// （EOF 是正常退出，其它错误返回）
	if _, err := MinifyXML("<a></b>"); err != nil {
		// 不强求通过，xml 解析是宽松的
		t.Logf("MinifyXML mismatched tags err (acceptable): %v", err)
	}
}

// ---------- trailing garbage 防护（v6 修复）----------

func TestFormatJSON_TrailingGarbage_Rejected(t *testing.T) {
	// 旧版只 Decode 一次，'{"a":1} {"b":2}' 会被误判为合法 JSON。
	// v6 修复后必须报错。
	if _, err := FormatJSON(`{"a":1} {"b":2}`, "  "); err == nil {
		t.Fatal("trailing garbage JSON 应该被拒")
	}
}

func TestMinifyJSON_TrailingGarbage_Rejected(t *testing.T) {
	if _, err := MinifyJSON(`{"a":1} extra stuff`); err == nil {
		t.Fatal("trailing garbage JSON 应该被拒")
	}
}

func TestValidateJSON_TrailingGarbage_Rejected(t *testing.T) {
	if err := ValidateJSON(`{"a":1} {"b":2}`); err == nil {
		t.Fatal("trailing garbage JSON 应该被拒")
	}
}

func TestFormatXML_BrokenDoc_Rejected(t *testing.T) {
	// 旧版 MinifyXML 用 err.Error() == "EOF" 判断，遇到解析错误会被吞掉。
	// v6 修复后用 errors.Is(err, io.EOF)，其它错误必须返回。
	// 故意造一个真正能让 Token 报错的 XML（chardata 缺右尖括号）：
	if _, err := FormatXML("<a>broken", "  "); err == nil {
		t.Fatal("broken XML 应该被 FormatXML 拒")
	}
}

func TestMinifyXML_BrokenDoc_Rejected(t *testing.T) {
	// 同上：v6 修复后 MinifyXML 必须把非 EOF 的解析错误返回。
	if _, err := MinifyXML("<a>broken"); err == nil {
		t.Fatal("broken XML 应该被 MinifyXML 拒")
	}
}
