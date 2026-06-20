// Package formatter 提供 JSON / XML 格式化与压缩。
//
// 纯前端也可以做，这里同时提供后端版本，方便后续扩展。
package formatter

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
)

// FormatJSON 格式化 JSON，indent 为缩进字符串（推荐 "  " 或 "\t"）
func FormatJSON(input string, indent string) (string, error) {
	var v interface{}
	dec := json.NewDecoder(bytes.NewReader([]byte(input)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
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
	var v interface{}
	dec := json.NewDecoder(bytes.NewReader([]byte(input)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("JSON 解析失败: %w", err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("JSON 序列化失败: %w", err)
	}
	return string(b), nil
}

// ValidateJSON 仅校验 JSON 合法性
func ValidateJSON(input string) error {
	var v interface{}
	dec := json.NewDecoder(bytes.NewReader([]byte(input)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
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
			if err.Error() == "EOF" {
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

// MinifyXML 压缩 XML（去掉多余空白）
func MinifyXML(input string) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader([]byte(input)))
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
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
