package webservice

// XML/WSDL 文本编码兼容。
// 老 Java/WebSphere 项目常见 UTF-8 BOM、UTF-16、GBK/GB2312/GB18030；
// encoding/xml 只原生接受 UTF-8，因此在进入解析器前统一转成 UTF-8。

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

var xmlDeclEncodingRe = regexp.MustCompile(`(?i)(<\?xml\b[^>]*\bencoding\s*=\s*["'])[^"']+(["'])`)
var xmlDeclEncodingBytesRe = regexp.MustCompile(`(?i)<\?xml\b[^>]*\bencoding\s*=\s*["']\s*([^"']+)\s*["']`)

// DecodeXMLBytes 根据 BOM、XML declaration、HTTP Content-Type（按此优先级）
// 解码 XML 原始字节，并把 declaration 归一为 UTF-8，供 encoding/xml 安全解析。
func DecodeXMLBytes(raw []byte, contentType string) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		return normalizeXMLDeclarationUTF8(string(raw[3:])), nil
	}
	if bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) {
		s, err := decodeUTF16(raw[2:], binary.LittleEndian)
		return normalizeXMLDeclarationUTF8(s), err
	}
	if bytes.HasPrefix(raw, []byte{0xFE, 0xFF}) {
		s, err := decodeUTF16(raw[2:], binary.BigEndian)
		return normalizeXMLDeclarationUTF8(s), err
	}
	// XML 规范允许 UTF-16 无 BOM；通过开头 "<\x00?\x00" 字节形态识别。
	if len(raw) >= 4 && raw[0] == '<' && raw[1] == 0 && raw[2] == '?' && raw[3] == 0 {
		s, err := decodeUTF16(raw, binary.LittleEndian)
		return normalizeXMLDeclarationUTF8(s), err
	}
	if len(raw) >= 4 && raw[0] == 0 && raw[1] == '<' && raw[2] == 0 && raw[3] == '?' {
		s, err := decodeUTF16(raw, binary.BigEndian)
		return normalizeXMLDeclarationUTF8(s), err
	}

	label := xmlDeclaredEncodingBytes(raw)
	if label == "" {
		label = parseCharset(contentType)
	}
	canonical := canonicalEncoding(label)
	switch canonical {
	case "", "UTF-8":
		if utf8.Valid(raw) {
			return normalizeXMLDeclarationUTF8(string(raw)), nil
		}
		// 无声明的内网 WSDL 常直接是 GBK/GB18030，做一次有界兜底。
		s, err := decodeWith(raw, simplifiedchinese.GB18030.NewDecoder())
		if err != nil {
			return "", fmt.Errorf("XML 既不是有效 UTF-8，也无法按 GB18030 解码: %w", err)
		}
		return normalizeXMLDeclarationUTF8(s), nil
	case "GBK", "GB2312":
		s, err := decodeWith(raw, simplifiedchinese.GBK.NewDecoder())
		return normalizeXMLDeclarationUTF8(s), err
	case "GB18030":
		s, err := decodeWith(raw, simplifiedchinese.GB18030.NewDecoder())
		return normalizeXMLDeclarationUTF8(s), err
	default:
		return "", fmt.Errorf("暂不支持的 XML 编码: %s（支持 UTF-8 / UTF-16 / GBK / GB2312 / GB18030）", label)
	}
}

func decodeUTF16(raw []byte, order binary.ByteOrder) (string, error) {
	if len(raw)%2 != 0 {
		return "", fmt.Errorf("UTF-16 字节数不是偶数")
	}
	u16 := make([]uint16, len(raw)/2)
	for i := range u16 {
		u16[i] = order.Uint16(raw[i*2 : i*2+2])
	}
	return string(utf16.Decode(u16)), nil
}

func decodeWith(raw []byte, decoder transform.Transformer) (string, error) {
	out, err := io.ReadAll(transform.NewReader(bytes.NewReader(raw), decoder))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func xmlDeclaredEncodingBytes(raw []byte) string {
	probe := raw
	if len(probe) > 1024 {
		probe = probe[:1024]
	}
	m := xmlDeclEncodingBytesRe.FindSubmatch(probe)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(string(m[1]))
}

func canonicalEncoding(label string) string {
	v := strings.ToUpper(strings.Trim(strings.TrimSpace(label), `"'`))
	v = strings.ReplaceAll(v, "_", "-")
	switch v {
	case "", "UTF-8", "UTF8":
		if v == "" {
			return ""
		}
		return "UTF-8"
	case "GBK", "CP936", "MS936", "WINDOWS-936":
		return "GBK"
	case "GB2312", "EUC-CN":
		return "GB2312"
	case "GB18030":
		return "GB18030"
	case "UTF-16", "UTF-16LE", "UTF-16BE":
		return v
	default:
		return v
	}
}

func normalizeXMLDeclarationUTF8(raw string) string {
	return xmlDeclEncodingRe.ReplaceAllString(raw, `${1}UTF-8${2}`)
}

func syncXMLDeclarationEncoding(raw, encoding string) string {
	label := canonicalEncoding(encoding)
	if label == "" {
		label = "UTF-8"
	}
	return xmlDeclEncodingRe.ReplaceAllString(raw, `${1}`+label+`${2}`)
}
