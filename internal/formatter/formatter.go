// Package formatter 提供 JSON / XML / YAML / URL-encoded 格式化与校验。
//
// 纯前端也可以做，这里同时提供后端版本，方便后续扩展。
//
// 严格性约束：JSON / XML / YAML 都禁止 trailing garbage（多段 / 末尾多余内容）。
// 用 encoding/json.Decoder / encoding/xml.Decoder / yaml.Decoder 读一段后必须再
// Decode 一次确认是 io.EOF，否则 '{"a":1} {"b":2}' 会被当成只有前一段合法的 JSON
// 而误判通过；YAML 同理。
package formatter

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// decodeStrictJSON 把 input 解成 v，并对剩余字节流检查 EOF。
//
// 返回：
//   - 解码后的 v
//   - trailingErr：如果 input 在第一个合法 JSON 之后还有非空白内容，返回错误；
//     否则返回 nil。
func decodeStrictJSON(input string) (v interface{}, trailingErr error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(input)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	// 再 Decode 一次：合法 JSON 输入到这里必须是 io.EOF。
	if err := dec.Decode(&struct{}{}); err != nil {
		if errors.Is(err, io.EOF) {
			return v, nil
		}
		return nil, fmt.Errorf("JSON 后面存在多余内容: %w", err)
	}
	// err == nil：竟然还能再 Decode 一段，说明 input 里有多个 JSON。
	return nil, fmt.Errorf("JSON 后面存在多余内容（包含多个 JSON 段）")
}

// FormatJSON 格式化 JSON，indent 为缩进字符串（推荐 "  " 或 "\t"）
func FormatJSON(input string, indent string) (string, error) {
	v, err := decodeStrictJSON(input)
	if err != nil {
		return "", fmt.Errorf("JSON 解析失败: %w", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("JSON 序列化失败: %w", err)
	}
	out := buf.String()
	// 去掉末尾的换行
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, nil
}

// MinifyJSON 压缩 JSON
func MinifyJSON(input string) (string, error) {
	v, err := decodeStrictJSON(input)
	if err != nil {
		return "", fmt.Errorf("JSON 解析失败: %w", err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("JSON 序列化失败: %w", err)
	}
	return string(b), nil
}

// ValidateJSON 仅校验 JSON 合法性（严格：禁止 trailing garbage）
func ValidateJSON(input string) error {
	if _, err := decodeStrictJSON(input); err != nil {
		return fmt.Errorf("JSON 非法: %w", err)
	}
	return nil
}

// FormatXML 格式化 XML，indent 为缩进字符串
func FormatXML(input string, indent string) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader([]byte(input)))
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", indent)
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("XML 解析失败: %w", err)
		}
		if ch, ok := tok.(xml.CharData); ok && strings.TrimSpace(string(ch)) == "" {
			continue
		}
		if err := enc.EncodeToken(tok); err != nil {
			return "", fmt.Errorf("XML 编码失败: %w", err)
		}
	}
	if err := enc.Flush(); err != nil {
		return "", fmt.Errorf("XML flush 失败: %w", err)
	}
	out := buf.String()
	// xml.Encoder 会自己换行，这里原样返回即可
	return out, nil
}

// MinifyXML 压缩 XML（去掉多余空白）。非法 XML 必须返回错误，
// 不能像早期版本那样把错误当 EOF 吞掉（会让坏 XML 被当成合法压缩结果）。
func MinifyXML(input string) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader([]byte(input)))
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("XML 解析失败: %w", err)
		}
		if err := enc.EncodeToken(tok); err != nil {
			return "", fmt.Errorf("XML 编码失败: %w", err)
		}
	}
	if err := enc.Flush(); err != nil {
		return "", fmt.Errorf("XML flush 失败: %w", err)
	}
	return buf.String(), nil
}

// ---------- YAML 格式化 ----------
//
// YAML 在运维场景里极常见（k8s manifest / docker-compose / ansible playbook /
// GitHub Actions）。用 yaml.v3（已在 go.mod）做往返：先 Unmarshal 到 interface{},
// 再 Encoder 输出。这样能保证"输出 = 解析结果"，而不是字符级重排（保留 key
// 顺序、注释丢掉——这是 v3 的默认行为；如需保留注释需要走 yaml.Node，运维场景
// 里注释保留不是刚需，先不增加复杂度）。
//
// 与 JSON 同样的"严格"原则：
//   - 单段 YAML：合法即通过；非法返回错误
//   - 多段 YAML：v3 不允许（一个文档流里只能有一个顶层文档），单段失败即视为非法

// FormatYAML 格式化 YAML，indent 是每级缩进的空格数（推荐 2 或 4）
func FormatYAML(input string, indent int) (string, error) {
	if indent <= 0 {
		indent = 2
	}
	if indent > 8 {
		indent = 8
	}
	v, err := decodeStrictYAML(input)
	if err != nil {
		return "", fmt.Errorf("YAML 解析失败: %w", err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("YAML 序列化失败: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("YAML 序列化失败: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// MinifyYAML 压缩 YAML（最小化输出）。语义参考 JSON Minify：把对象序列化到紧凑形式。
// 注意：YAML 的"压缩"语义不像 JSON 那么严格——多行字符串/多行 scalar 会保留自己的换行。
func MinifyYAML(input string) (string, error) {
	v, err := decodeStrictYAML(input)
	if err != nil {
		return "", fmt.Errorf("YAML 解析失败: %w", err)
	}
	out, err := yaml.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("YAML 序列化失败: %w", err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// ValidateYAML 仅校验 YAML 合法性（不允许多文档流）
func ValidateYAML(input string) error {
	if _, err := decodeStrictYAML(input); err != nil {
		return fmt.Errorf("YAML 非法: %w", err)
	}
	return nil
}

// YAMLToJSON 把 YAML 转成 JSON（indent 仅控制 JSON 输出缩进，正整数；0 = 紧凑）
func YAMLToJSON(input string, indent string) (string, error) {
	v, err := decodeStrictYAML(input)
	if err != nil {
		return "", fmt.Errorf("YAML 解析失败: %w", err)
	}
	if indent == "" {
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("JSON 序列化失败: %w", err)
		}
		return string(b), nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("JSON 序列化失败: %w", err)
	}
	out := buf.String()
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, nil
}

// JSONToYAML 把 JSON 转成 YAML（indent 是 YAML 缩进空格数）
func JSONToYAML(input string, indent int) (string, error) {
	v, err := decodeStrictJSON(input)
	if err != nil {
		return "", fmt.Errorf("JSON 解析失败: %w", err)
	}
	// decodeStrictJSON 用 UseNumber，json.Number 不能直接 marshal 成 YAML 数字。
	// 在序列化前把 json.Number 转成 int64/float64，让 YAML 输出更符合直觉：
	//   {"age": 18} → age: 18
	//   {"price": 1.5} → price: 1.5
	//   {"big": 9999999999999999999} → big: 9999999999999999999 （超 int64 范围走字符串）
	normalizeJSONNumbers(v)
	if indent <= 0 {
		indent = 2
	}
	if indent > 8 {
		indent = 8
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("YAML 序列化失败: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("YAML 序列化失败: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// normalizeJSONNumbers 递归把 map/slice 里的 json.Number 转成 int64/float64。
// 走 string() 再 ParseFloat 再判有无小数点的策略：避免 18.0 被错当成整数，也避免
// 9999999999999999999 这种超 int64 范围的数字被强转。
func normalizeJSONNumbers(v interface{}) {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, val := range x {
			x[k] = normalizeJSONNumberValue(val)
			normalizeJSONNumbers(val)
		}
	case []interface{}:
		for i, val := range x {
			x[i] = normalizeJSONNumberValue(val)
			normalizeJSONNumbers(val)
		}
	}
}

func normalizeJSONNumberValue(val interface{}) interface{} {
	n, ok := val.(json.Number)
	if !ok {
		return val
	}
	s := n.String()
	// 优先 int64
	if i, err := strconv.ParseInt(s, 10, 64); err == nil && strconv.FormatInt(i, 10) == s {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// decodeStrictYAML 解析 YAML，禁止多文档流。
//
// yaml.v3 的 Decoder 默认每读一次就解析一个文档；我们读两次，
// 第二次必须是 io.EOF 才算"只有一段"。
func decodeStrictYAML(input string) (interface{}, error) {
	dec := yaml.NewDecoder(strings.NewReader(input))
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("YAML 非法: 空输入")
		}
		return nil, err
	}
	// 再 Decode 一次：必须是 io.EOF
	var dummy interface{}
	if err := dec.Decode(&dummy); err != nil {
		if errors.Is(err, io.EOF) {
			return v, nil
		}
		return nil, fmt.Errorf("YAML 后面存在多余内容（包含多段 YAML 文档）")
	}
	return nil, fmt.Errorf("YAML 后面存在多余内容（包含多段 YAML 文档）")
}

// ---------- URL-encoded / form-data 编解码 ----------
//
// 场景：调试 Web 接口、抓包粘贴 query string、构造 application/x-www-form-urlencoded
// body、调试 multipart/form-data（这里只做 key=value&... 的简单形式；multipart
// 涉及 boundary/二进制，前端通常用 FormData 直接构造，不需要服务端格式化）。
//
// 行为约定：
//   - encode：map[string]string → "a=1&b=2"；值自动 URL-encode；key 按字典序排序，便于 diff
//   - decode：把 "a=1&b=2" 解成 map[string][]string；同名 key 重复时合并到数组

// URLFormEncode 把 map 编码成 application/x-www-form-urlencoded。
//
// 入参：key/value 都是字符串；空字符串视作有效 key。
// 输出：按 key 字典序排序。
func URLFormEncode(kv map[string]string) (string, error) {
	if kv == nil {
		return "", nil
	}
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(kv[k]))
	}
	return strings.Join(parts, "&"), nil
}

// URLFormDecode 把 application/x-www-form-urlencoded 解码成 map[string][]string。
//
// 重复 key 自动合并成数组；空字符串值保留为 ""。
func URLFormDecode(input string) (map[string][]string, error) {
	out := make(map[string][]string)
	if input == "" {
		return out, nil
	}
	values, err := url.ParseQuery(input)
	if err != nil {
		return nil, fmt.Errorf("URL 解析失败: %w", err)
	}
	for k, v := range values {
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out, nil
}
