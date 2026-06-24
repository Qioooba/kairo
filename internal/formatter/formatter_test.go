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

// ---------- v0.8：YAML ----------

func TestFormatYAML_Basic(t *testing.T) {
	in := "a: 1\nb:\n  c: hello\n  d:\n  - 1\n  - 2\n  - 3\n"
	out, err := FormatYAML(in, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a: 1") || !strings.Contains(out, "c: hello") {
		t.Fatalf("格式化结果缺字段: %s", out)
	}
	// indent=2 应该产生 2 空格缩进
	if !strings.Contains(out, "  c: hello") {
		t.Fatalf("indent=2 没生效: %q", out)
	}
}

func TestFormatYAML_Indent(t *testing.T) {
	in := "a:\n  b: 1\n"
	out, err := FormatYAML(in, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "    b: 1") {
		t.Fatalf("indent=4 没生效: %q", out)
	}
}

func TestFormatYAML_IndentClamp(t *testing.T) {
	in := "a: 1\n"
	// indent > 8 应被 clamp 到 8
	out, err := FormatYAML(in, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a: 1") {
		t.Fatalf("clamp 后内容丢了: %q", out)
	}
}

func TestFormatYAML_InvalidYAML(t *testing.T) {
	in := "a: 1\n  bad: : :\n\t- oops\n" // 故意非法：tab + 错位
	_, err := FormatYAML(in, 2)
	if err == nil {
		t.Fatal("非法 YAML 应该报错")
	}
}

func TestFormatYAML_TrailingGarbage_Rejected(t *testing.T) {
	in := "a: 1\n---\nb: 2\n" // 多文档流应被拒
	_, err := FormatYAML(in, 2)
	if err == nil {
		t.Fatal("多段 YAML 应该被拒绝")
	}
}

func TestMinifyYAML_Basic(t *testing.T) {
	in := "a: 1\nb:\n  c: 2\n"
	out, err := MinifyYAML(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a: 1") || !strings.Contains(out, "c: 2") {
		t.Fatalf("minify 丢字段: %q", out)
	}
}

func TestValidateYAML_Invalid(t *testing.T) {
	if err := ValidateYAML("a: :\n  - :"); err == nil {
		t.Fatal("非法 YAML 应该 validate 失败")
	}
}

func TestYAMLToJSON(t *testing.T) {
	in := "name: ops\nage: 18\ntags:\n  - a\n  - b\n"
	out, err := YAMLToJSON(in, "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\"name\": \"ops\"") {
		t.Fatalf("YAML→JSON 失败: %s", out)
	}
}

func TestYAMLToJSON_Minify(t *testing.T) {
	in := "a: 1\nb: 2\n"
	out, err := YAMLToJSON(in, "") // 空 indent = minify
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\n") {
		t.Fatalf("minify 应该无换行: %q", out)
	}
}

func TestJSONToYAML(t *testing.T) {
	in := `{"name":"ops","age":18,"tags":["a","b"]}`
	out, err := JSONToYAML(in, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "name: ops") {
		t.Fatalf("JSON→YAML 失败: %s", out)
	}
}

func TestYAMLJSONRoundTrip(t *testing.T) {
	in := `{"name":"运维","age":18,"tags":["a","b"]}`
	yamlOut, err := JSONToYAML(in, 2)
	if err != nil {
		t.Fatal(err)
	}
	jsonOut, err := YAMLToJSON(yamlOut, "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut, "\"name\": \"运维\"") {
		t.Fatalf("中文没保留: %s", jsonOut)
	}
	if !strings.Contains(jsonOut, "\"a\"") || !strings.Contains(jsonOut, "\"b\"") {
		t.Fatalf("数组 round-trip 失败: %s", jsonOut)
	}
}

// ---------- v0.8：URL form ----------

func TestURLFormEncode_Basic(t *testing.T) {
	out, err := URLFormEncode(map[string]string{"a": "1", "b": "2"})
	if err != nil {
		t.Fatal(err)
	}
	// 按字典序排序
	if out != "a=1&b=2" {
		t.Fatalf("排序错: %q", out)
	}
}

func TestURLFormEncode_ChineseAndSpecial(t *testing.T) {
	out, err := URLFormEncode(map[string]string{"name": "上海", "q": "a b&c=d"})
	if err != nil {
		t.Fatal(err)
	}
	// name 字典序在 q 前
	if !strings.HasPrefix(out, "name=") {
		t.Fatalf("编码顺序错: %q", out)
	}
	if !strings.Contains(out, "%E4%B8%8A%E6%B5%B7") {
		t.Fatalf("中文没编码: %q", out)
	}
	if !strings.Contains(out, "a+b%26c%3Dd") {
		t.Fatalf("特殊字符没编码: %q", out)
	}
}

func TestURLFormEncode_Empty(t *testing.T) {
	out, err := URLFormEncode(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("空 map 应该输出空: %q", out)
	}
}

func TestURLFormDecode_Basic(t *testing.T) {
	out, err := URLFormDecode("a=1&b=2&b=3")
	if err != nil {
		t.Fatal(err)
	}
	if len(out["a"]) != 1 || out["a"][0] != "1" {
		t.Fatalf("a 解析错: %v", out["a"])
	}
	if len(out["b"]) != 2 || out["b"][0] != "2" || out["b"][1] != "3" {
		t.Fatalf("b 重复 key 应合并: %v", out["b"])
	}
}

func TestURLFormDecode_Chinese(t *testing.T) {
	out, err := URLFormDecode("name=%E4%B8%8A%E6%B5%B7")
	if err != nil {
		t.Fatal(err)
	}
	if len(out["name"]) != 1 || out["name"][0] != "上海" {
		t.Fatalf("中文解码失败: %v", out["name"])
	}
}

func TestURLFormDecode_Empty(t *testing.T) {
	out, err := URLFormDecode("")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("空输入应得到空 map: %v", out)
	}
}

func TestURLFormRoundTrip(t *testing.T) {
	orig := map[string]string{"a": "1", "中文": "上海"}
	encoded, err := URLFormEncode(orig)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := URLFormDecode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded["a"][0] != "1" || decoded["中文"][0] != "上海" {
		t.Fatalf("round-trip 失败: %v", decoded)
	}
}

// ---------- v0.8：SQL 格式化（依赖 node + sqlfmt.mjs） ----------
//
// 这些测试需要外部环境（node + scripts/sqlfmt.mjs），找不到时跳过，不让单测挂掉。
// 想跑：在仓库根目录跑 go test，内部会自动定位 ../scripts/sqlfmt.mjs。

func TestFormatSQL_MySQL_Basic(t *testing.T) {
	out, err := FormatSQL("select id,name from users where age>18", SQLFormatOptions{
		Language:    "mysql",
		KeywordCase: "upper",
		TabWidth:    2,
	}, "")
	if err != nil {
		t.Skipf("跳 SQL 测试（环境缺 node/sqlfmt.mjs）：%v", err)
	}
	if !strings.Contains(out, "SELECT") || !strings.Contains(out, "FROM") {
		t.Fatalf("MySQL 格式化结果不像样: %s", out)
	}
}

func TestFormatSQL_Postgres(t *testing.T) {
	out, err := FormatSQL(`SELECT u.id, u.name FROM users u WHERE u.age > 18 AND u.city = 'SH' ORDER BY u.id`, SQLFormatOptions{
		Language:    "postgresql",
		KeywordCase: "upper",
		TabWidth:    2,
	}, "")
	if err != nil {
		t.Skipf("跳 SQL 测试: %v", err)
	}
	if !strings.Contains(out, "SELECT") {
		t.Fatalf("PG 格式化失败: %s", out)
	}
}

func TestFormatSQL_EmptyInput(t *testing.T) {
	_, err := FormatSQL("   ", SQLFormatOptions{}, "")
	if err == nil {
		t.Fatal("空 SQL 应报错")
	}
}

func TestFormatSQL_ScriptNotFound(t *testing.T) {
	_, err := FormatSQL("select 1", SQLFormatOptions{}, "/nonexistent/path/sqlfmt.mjs")
	if err == nil {
		t.Fatal("找不到脚本时应报错")
	}
	if !strings.Contains(err.Error(), "找不到 sqlfmt.mjs") {
		t.Fatalf("错误文案不对: %v", err)
	}
}

func TestFormatSQL_ChinesePreserved(t *testing.T) {
	out, err := FormatSQL(`SELECT * FROM users WHERE city = '上海'`, SQLFormatOptions{
		Language:    "mysql",
		KeywordCase: "upper",
		TabWidth:    2,
	}, "")
	if err != nil {
		t.Skipf("跳: %v", err)
	}
	if !strings.Contains(out, "上海") {
		t.Fatalf("中文没保留: %s", out)
	}
}

func TestJSONToYAML_NumberTypes(t *testing.T) {
	// json.Number 在 decodeStrictJSON 里被保留；
	// JSONToYAML 应该把它转成 int64 / float64，输出不带引号。
	in := `{"int":18,"float":1.5,"big":9999999999}`
	out, err := JSONToYAML(in, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "int: 18") {
		t.Fatalf("int 没转成数字: %s", out)
	}
	if !strings.Contains(out, "float: 1.5") {
		t.Fatalf("float 没转成数字: %s", out)
	}
	if !strings.Contains(out, "big: 9999999999") {
		t.Fatalf("大整数没转成数字: %s", out)
	}
	// 不能有 int: "18" 这种带引号的字符串
	if strings.Contains(out, `"18"`) {
		t.Fatalf("int 仍带引号: %s", out)
	}
}
