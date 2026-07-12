package webservice

// SOAP 报文生成 + XML 辅助（格式化 / 压缩 / well-formed 校验）。
//
// 报文生成策略（document/literal wrapped 为主）：
//   - Envelope 用 soapenv 前缀（SOAP 1.1 env ns = http://schemas.xmlsoap.org/soap/envelope/）；
//   - Body 下放 operation 元素，namespace 取 Operation.Namespace；
//   - 叶子参数节点默认空值（<name></name>），用户直接在编辑器里填值；复杂参数递归生成嵌套节点。
//   - 不再嵌入 ${paramName} 占位符 —— 避免"必填/非必填"判断、避免应用按钮、避免用户误以为要替换字符串。

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	soap11EnvNS = "http://schemas.xmlsoap.org/soap/envelope/"
	soap12EnvNS = "http://www.w3.org/2003/05/soap-envelope"

	// SOAPContentType11 / SOAPContentType12 是发送请求时默认 Content-Type。
	SOAPContentType11 = "text/xml; charset="
	SOAPContentType12 = "application/soap+xml; charset="
)

// GenerateEnvelope 根据 operation 生成一个可编辑的 SOAP 请求报文。
// soapVersion 为 "1.2" 时用 SOAP 1.2 envelope namespace，否则按 1.1。
func GenerateEnvelope(op Operation, soapVersion string) string {
	envNS := soap11EnvNS
	if soapVersion == "1.2" {
		envNS = soap12EnvNS
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<soapenv:Envelope xmlns:soapenv="` + envNS + `"`)
	if op.Namespace != "" {
		b.WriteString(` xmlns:web="` + escapeXMLAttr(op.Namespace) + `"`)
	}
	b.WriteString(">\n")
	b.WriteString("  <soapenv:Header/>\n")
	b.WriteString("  <soapenv:Body>\n")

	// operation 元素：document/literal 用 web 前缀；rpc 用 web 前缀同样可用。
	opTag := "web:" + op.Name
	if op.Namespace == "" {
		opTag = op.Name
	}
	b.WriteString("    <" + opTag + ">\n")
	writeParamNodes(&b, op.InputParams, "      ")
	b.WriteString("    </" + opTag + ">\n")
	b.WriteString("  </soapenv:Body>\n")
	b.WriteString("</soapenv:Envelope>\n")
	return b.String()
}

// writeParamNodes 递归写出参数节点。叶子节点默认空值，由用户在编辑器里直接填。
// 不嵌入 ${name} 占位符 —— 避免把"必填/可选"的判断责任推给前端表单，
// 也不强制用户点"应用"按钮；用户想填什么自己在请求体 XML 里写。
func writeParamNodes(b *strings.Builder, params []Param, indent string) {
	for _, p := range params {
		if len(p.Children) > 0 {
			b.WriteString(indent + "<" + p.Name + ">\n")
			writeParamNodes(b, p.Children, indent+"  ")
			b.WriteString(indent + "</" + p.Name + ">\n")
			continue
		}
		// 叶子：空标签（自闭合形式省字节，但保留成对标签方便用户点开填值）
		b.WriteString(indent + "<" + p.Name + "></" + p.Name + ">\n")
	}
}

// escapeXMLAttr 转义属性值里的特殊字符。
func escapeXMLAttr(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return r.Replace(s)
}

// SuggestLogKeywords 从请求 XML 里提取建议的日志搜索关键词。
// 覆盖 operation、SOAPAction，以及常见 trace 字段：serialNo/serialno/traceNo/requestId/transId 等。
func SuggestLogKeywords(operation, soapAction, requestBody string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(operation)
	// SOAPAction 去引号
	sa := strings.Trim(soapAction, `"`)
	add(sa)

	// 从请求体里抽取常见 trace 字段的值
	// 注意：标签名按大小写不敏感匹配（XML 标签名本应大小写敏感，但老 Java 服务时常混用），
	// 但取出的 value 必须保留原始大小写 —— 否则大写流水号（如 SN20260702001）会被小写化，
	// 搜日志时匹配不到。
	traceFields := []string{
		"serialNo", "serialno",
		"traceNo", "traceno",
		"requestId", "requestid",
		"transId", "transid",
		"tradeNo", "tradeno",
		"orderId", "orderid",
		"reqSerial", "flowNo", "flowno",
	}
	lower := strings.ToLower(requestBody)
	for _, f := range traceFields {
		val := extractTagValue(requestBody, lower, f)
		if val != "" {
			add(val)
		}
	}
	return out
}

// extractTagValue 从 XML 文本里抽取 <tag>value</tag> 的 value。
// 标签名按大小写不敏感匹配（在 lowerXML 上定位），但 value 从 origXML 取，保留原始大小写。
// 容错：跳过属性、跳过自闭合。
func extractTagValue(origXML, lowerXML, tag string) string {
	open := "<" + tag
	idx := strings.Index(lowerXML, open)
	if idx < 0 {
		return ""
	}
	// 跳过到 ">" 结束开始标签
	gt := strings.IndexByte(lowerXML[idx:], '>')
	if gt < 0 {
		return ""
	}
	start := idx + gt + 1
	// 自闭合 <tag/>
	if start-2 >= idx && lowerXML[start-2] == '/' {
		return ""
	}
	close := "</" + tag
	cidx := strings.Index(lowerXML[start:], close)
	if cidx < 0 {
		return ""
	}
	val := origXML[start : start+cidx]
	// 去首尾空白，限制长度
	val = strings.TrimSpace(val)
	if len(val) > 64 {
		val = val[:64]
	}
	return val
}

// ---------- XML 格式化 / 压缩 / 校验 ----------
//
// v0.12 修复：之前用 xml.Decoder + xml.Encoder 往返实现 FormatXML/MinifyXML，
// 踩到 Go 标准库的坑 —— xml.Encoder 在处理 StartElement 时会**自动重新生成
// namespace 声明**（因为 StartElement 只携带 Name.Space + Name.Local，丢了
// prefix 信息）。后果：用户写的 `soapenv:Envelope xmlns:soapenv="..."` 进来，
// 出来变成 `Envelope xmlns="..." xmlns:_xmlns="xmlns" _xmlns:soapenv="..."`，
// SOAP 报文语义被破坏，发送出去老 Java 服务直接拒。
//
// 修复方案：完全用字符串层面处理。
//   - Minify：用 tokenizer 逐 token 输出，text 节点 trim 首尾空白，
//     其余（tag/PI/comment/CDATA）原样保留。能剥掉 text 节点前后的缩进空白，
//     保留原始 prefix 和 namespace 声明。
//   - Format：手写 XML tokenizer（找 <tag> / </tag> / <tag/> / <?...?> / <!--...--> / <![CDATA[...]]>），
//     按当前 depth 加缩进，**原样拷贝** tag 文本（包括 prefix 和所有 attribute）。
//     标签内的文本节点按原样输出（trim 掉首尾缩进空白）。
//
// 零依赖，能 100% 保留原始 prefix 和 namespace。

// FormatXML 格式化 XML。手写 parser，原样保留所有 tag prefix。
func FormatXML(input, indent string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", fmt.Errorf("输入为空")
	}
	if indent == "" {
		indent = "  "
	}
	tokens, err := tokenizeXML(input)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	depth := 0
	for _, tk := range tokens {
		switch tk.kind {
		case xmlTkPI, xmlTkComment, xmlTkDirective, xmlTkCDATA:
			writeIndent(&buf, depth, indent)
			buf.WriteString(tk.raw)
			buf.WriteByte('\n')
		case xmlTkStartTag:
			writeIndent(&buf, depth, indent)
			buf.WriteString(tk.raw)
			if tk.selfClose {
				buf.WriteByte('\n')
			} else {
				buf.WriteByte('\n')
				depth++
			}
		case xmlTkEndTag:
			if depth > 0 {
				depth--
			}
			writeIndent(&buf, depth, indent)
			buf.WriteString(tk.raw)
			buf.WriteByte('\n')
		case xmlTkText:
			trimmed := strings.TrimSpace(tk.raw)
			if trimmed == "" {
				continue
			}
			writeIndent(&buf, depth, indent)
			buf.WriteString(trimmed)
			buf.WriteByte('\n')
		}
	}
	out := buf.String()
	out = strings.TrimRight(out, "\n")
	return out + "\n", nil
}

// MinifyXML 压缩 XML（去掉多余空白）。保留原始 prefix。
// 用 tokenizer 逐 token 输出：text 节点 trim 首尾空白，其余原样。
// 相比仅匹配 >\s+< 的 regex 方案，能剥掉 text 节点前后的缩进空白
// （FormatXML 会在 text 节点前后加缩进，regex 压不掉，导致带文本内容的报文压缩后残留空白）。
func MinifyXML(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", fmt.Errorf("输入为空")
	}
	if err := ValidateXML(input); err != nil {
		return "", err
	}
	tokens, err := tokenizeXML(input)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, tk := range tokens {
		if tk.kind == xmlTkText {
			b.WriteString(strings.TrimSpace(tk.raw))
			continue
		}
		b.WriteString(tk.raw)
	}
	return b.String(), nil
}

// ---------- 手写 XML tokenizer（保留原始 prefix）----------

type xmlTkKind int

const (
	xmlTkPI xmlTkKind = iota
	xmlTkComment
	xmlTkDirective
	xmlTkCDATA
	xmlTkStartTag
	xmlTkEndTag
	xmlTkText
)

type xmlToken struct {
	kind      xmlTkKind
	raw       string
	selfClose bool
}

// tokenizeXML 把 input 切成 token 流，保留所有原始文本。
func tokenizeXML(input string) ([]xmlToken, error) {
	var out []xmlToken
	i := 0
	n := len(input)
	for i < n {
		if isSpace(input[i]) {
			j := i
			for j < n && isSpace(input[j]) {
				j++
			}
			if j >= n {
				break
			}
			if input[j] == '<' {
				i = j
				continue
			}
			i = j
			continue
		}
		if input[i] != '<' {
			j := i
			for j < n && input[j] != '<' {
				j++
			}
			out = append(out, xmlToken{kind: xmlTkText, raw: input[i:j]})
			i = j
			continue
		}
		if i+1 < n && input[i+1] == '?' {
			end := strings.Index(input[i:], "?>")
			if end < 0 {
				return nil, fmt.Errorf("XML 解析失败: PI 未闭合 at %d", i)
			}
			raw := input[i : i+end+2]
			out = append(out, xmlToken{kind: xmlTkPI, raw: raw})
			i = i + end + 2
			continue
		}
		if i+4 <= n && input[i:i+4] == "<!--" {
			end := strings.Index(input[i:], "-->")
			if end < 0 {
				return nil, fmt.Errorf("XML 解析失败: 注释未闭合 at %d", i)
			}
			raw := input[i : i+end+3]
			out = append(out, xmlToken{kind: xmlTkComment, raw: raw})
			i = i + end + 3
			continue
		}
		if i+9 <= n && input[i:i+9] == "<![CDATA[" {
			end := strings.Index(input[i:], "]]>")
			if end < 0 {
				return nil, fmt.Errorf("XML 解析失败: CDATA 未闭合 at %d", i)
			}
			raw := input[i : i+end+3]
			out = append(out, xmlToken{kind: xmlTkCDATA, raw: raw})
			i = i + end + 3
			continue
		}
		if i+1 < n && input[i+1] == '!' {
			end := findTagEnd(input, i)
			if end < 0 {
				return nil, fmt.Errorf("XML 解析失败: directive 未闭合 at %d", i)
			}
			raw := input[i : end+1]
			out = append(out, xmlToken{kind: xmlTkDirective, raw: raw})
			i = end + 1
			continue
		}
		end := findTagEnd(input, i)
		if end < 0 {
			return nil, fmt.Errorf("XML 解析失败: tag 未闭合 at %d", i)
		}
		raw := input[i : end+1]
		selfClose := false
		if len(raw) >= 2 && raw[len(raw)-2] == '/' {
			selfClose = true
		}
		if len(raw) >= 2 && raw[1] == '/' {
			out = append(out, xmlToken{kind: xmlTkEndTag, raw: raw})
		} else {
			out = append(out, xmlToken{kind: xmlTkStartTag, raw: raw, selfClose: selfClose})
		}
		i = end + 1
	}
	return out, nil
}

// findTagEnd 找 input[start] 开始的 tag 的 > 位置（处理属性值内的 > 字符）。
func findTagEnd(input string, start int) int {
	inQuote := byte(0)
	for i := start + 1; i < len(input); i++ {
		c := input[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			continue
		}
		if c == '>' {
			return i
		}
	}
	return -1
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func writeIndent(buf *bytes.Buffer, depth int, indent string) {
	for i := 0; i < depth; i++ {
		buf.WriteString(indent)
	}
}

// ValidateXML 做 well-formed 校验：用元素栈检查开始/结束标签匹配。
// Strict=true 关闭非严格模式的自动闭合宽松行为，确保标签嵌套错误能被发现。
func ValidateXML(input string) error {
	if strings.TrimSpace(input) == "" {
		return fmt.Errorf("输入为空")
	}
	dec := xml.NewDecoder(strings.NewReader(input))
	dec.Strict = true
	var stack []string
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("XML 非法: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
		case xml.EndElement:
			if len(stack) == 0 {
				return fmt.Errorf("XML 非法: 多余的结束标签 </%s>", t.Name.Local)
			}
			top := stack[len(stack)-1]
			if top != t.Name.Local {
				return fmt.Errorf("XML 非法: 标签不匹配，期望 </%s> 实际 </%s>", top, t.Name.Local)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) > 0 {
		return fmt.Errorf("XML 非法: 未闭合的标签 <%s>", stack[len(stack)-1])
	}
	return nil
}
