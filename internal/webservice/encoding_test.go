package webservice

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestDecodeXMLBytes_CommonWSDLFormats(t *testing.T) {
	utf8XML := `<?xml version="1.0" encoding="UTF-8"?><definitions name="中文服务"/>`

	t.Run("utf8-bom", func(t *testing.T) {
		got, err := DecodeXMLBytes(append([]byte{0xEF, 0xBB, 0xBF}, []byte(utf8XML)...), "")
		if err != nil || !strings.Contains(got, "中文服务") || strings.HasPrefix(got, "\uFEFF") {
			t.Fatalf("got=%q err=%v", got, err)
		}
	})

	for _, tc := range []struct {
		name  string
		order binary.ByteOrder
		bom   []byte
	}{
		{"utf16le", binary.LittleEndian, []byte{0xFF, 0xFE}},
		{"utf16be", binary.BigEndian, []byte{0xFE, 0xFF}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u16 := utf16.Encode([]rune(strings.Replace(utf8XML, "UTF-8", "UTF-16", 1)))
			raw := append([]byte{}, tc.bom...)
			for _, v := range u16 {
				var pair [2]byte
				tc.order.PutUint16(pair[:], v)
				raw = append(raw, pair[:]...)
			}
			got, err := DecodeXMLBytes(raw, "")
			if err != nil || !strings.Contains(got, "中文服务") || !strings.Contains(got, `encoding="UTF-8"`) {
				t.Fatalf("got=%q err=%v", got, err)
			}
		})
	}

	for _, enc := range []struct {
		name string
		enc  func([]byte) ([]byte, error)
	}{
		{"GBK", func(in []byte) ([]byte, error) { return simplifiedchinese.GBK.NewEncoder().Bytes(in) }},
		{"GB2312", func(in []byte) ([]byte, error) { return simplifiedchinese.GBK.NewEncoder().Bytes(in) }},
		{"GB18030", func(in []byte) ([]byte, error) { return simplifiedchinese.GB18030.NewEncoder().Bytes(in) }},
	} {
		t.Run(enc.name, func(t *testing.T) {
			src := strings.Replace(utf8XML, "UTF-8", enc.name, 1)
			raw, err := enc.enc([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeXMLBytes(raw, "text/xml")
			if err != nil || !strings.Contains(got, "中文服务") || !strings.Contains(got, `encoding="UTF-8"`) {
				t.Fatalf("got=%q err=%v raw=%x", got, err, bytes.TrimSpace(raw))
			}
		})
	}
}

func TestParseWSDL_NormalizesDecodedLegacyDeclaration(t *testing.T) {
	raw := strings.Replace(sampleWSDL, "UTF-8", "GBK", 1)
	p := ParseWSDL(raw)
	if p.ParseError != "" || len(p.Operations) != 1 {
		t.Fatalf("已解码的 JSON 字符串即使 declaration=GBK 也应可解析: error=%q ops=%d", p.ParseError, len(p.Operations))
	}
}
