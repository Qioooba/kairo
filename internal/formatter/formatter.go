// Package formatter 提供 JSON / XML 格式化与压缩。
//
// 纯前端也可以做，这里同时提供后端版本，方便后续扩展。
//
// 严格性约束：JSON / XML 都禁止 trailing garbage（多段 / 末尾多余内容）。
// 用 encoding/json.Decoder 读一段后必须再 Decode 一次确认是 io.EOF，
// 否则 '{"a":1} {"b":2}' 会被当成只有前一段合法的 JSON 而误判通过。
package formatter

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
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
