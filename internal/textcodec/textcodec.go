// Package textcodec detects and preserves the encoding and line endings of
// text files edited by the Compare workbench.
package textcodec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

type Info struct {
	Encoding string `json:"encoding"`
	EOL      string `json:"eol"`
	BOM      bool   `json:"bom"`
}

func Decode(raw []byte, preferred string) (string, Info, bool, error) {
	encoding := canonicalEncoding(preferred)
	bomEncoding, hasBOM, bomLen := detectBOM(raw)
	if hasBOM {
		encoding = bomEncoding
		raw = raw[bomLen:]
	}
	if encoding == "auto" {
		if looksBinary(raw) {
			return "", Info{}, true, nil
		}
		if utf8.Valid(raw) {
			encoding = "utf-8"
		} else {
			encoding = "gb18030"
		}
	} else if encoding != "utf-16le" && encoding != "utf-16be" && looksBinary(raw) {
		return "", Info{}, true, nil
	}

	text, err := decodeBytes(raw, encoding)
	if err != nil {
		return "", Info{}, false, err
	}
	if looksBinaryText(text) {
		return "", Info{}, true, nil
	}
	info := Info{Encoding: encoding, EOL: detectEOL(text), BOM: hasBOM}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text, info, false, nil
}

func Encode(text string, info Info) ([]byte, error) {
	encoding := canonicalEncoding(info.Encoding)
	if encoding == "auto" {
		encoding = "utf-8"
	}
	switch strings.ToLower(info.EOL) {
	case "crlf":
		text = strings.ReplaceAll(text, "\n", "\r\n")
	case "cr":
		text = strings.ReplaceAll(text, "\n", "\r")
	}
	var raw []byte
	var err error
	switch encoding {
	case "utf-8":
		raw = []byte(text)
	case "gbk":
		raw, err = simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
	case "gb18030":
		raw, err = simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(text))
	case "utf-16le":
		raw = encodeUTF16(text, binary.LittleEndian)
	case "utf-16be":
		raw = encodeUTF16(text, binary.BigEndian)
	default:
		return nil, fmt.Errorf("unsupported encoding %q", info.Encoding)
	}
	if err != nil {
		return nil, err
	}
	if info.BOM {
		switch encoding {
		case "utf-8":
			raw = append([]byte{0xef, 0xbb, 0xbf}, raw...)
		case "utf-16le":
			raw = append([]byte{0xff, 0xfe}, raw...)
		case "utf-16be":
			raw = append([]byte{0xfe, 0xff}, raw...)
		}
	}
	return raw, nil
}

func canonicalEncoding(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto", "detected":
		return "auto"
	case "utf8", "utf-8":
		return "utf-8"
	case "gbk", "gb2312":
		return "gbk"
	case "gb18030":
		return "gb18030"
	case "utf16le", "utf-16le":
		return "utf-16le"
	case "utf16be", "utf-16be":
		return "utf-16be"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func detectBOM(raw []byte) (string, bool, int) {
	switch {
	case bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}):
		return "utf-8", true, 3
	case bytes.HasPrefix(raw, []byte{0xff, 0xfe}):
		return "utf-16le", true, 2
	case bytes.HasPrefix(raw, []byte{0xfe, 0xff}):
		return "utf-16be", true, 2
	default:
		return "", false, 0
	}
}

func decodeBytes(raw []byte, encoding string) (string, error) {
	switch encoding {
	case "utf-8":
		if !utf8.Valid(raw) {
			return "", errors.New("invalid UTF-8 text")
		}
		return string(raw), nil
	case "gbk":
		decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(raw)
		return string(decoded), err
	case "gb18030":
		decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
		return string(decoded), err
	case "utf-16le":
		return decodeUTF16(raw, binary.LittleEndian)
	case "utf-16be":
		return decodeUTF16(raw, binary.BigEndian)
	default:
		return "", fmt.Errorf("unsupported encoding %q", encoding)
	}
}

func decodeUTF16(raw []byte, order binary.ByteOrder) (string, error) {
	if len(raw)%2 != 0 {
		return "", errors.New("invalid UTF-16 byte length")
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = order.Uint16(raw[i*2 : i*2+2])
	}
	return string(utf16.Decode(units)), nil
}

func encodeUTF16(text string, order binary.ByteOrder) []byte {
	units := utf16.Encode([]rune(text))
	raw := make([]byte, len(units)*2)
	for i, unit := range units {
		order.PutUint16(raw[i*2:i*2+2], unit)
	}
	return raw
}

func detectEOL(text string) string {
	crlf := strings.Count(text, "\r\n")
	withoutCRLF := strings.ReplaceAll(text, "\r\n", "")
	lf := strings.Count(withoutCRLF, "\n")
	cr := strings.Count(withoutCRLF, "\r")
	if crlf >= lf && crlf >= cr && crlf > 0 {
		return "crlf"
	}
	if cr > lf && cr > 0 {
		return "cr"
	}
	return "lf"
}

func looksBinary(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	controls := 0
	for _, b := range raw {
		if b == 0 {
			return true
		}
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' && b != '\f' {
			controls++
		}
	}
	return controls*100 > len(raw)*2
}

func looksBinaryText(text string) bool {
	if text == "" {
		return false
	}
	controls := 0
	for _, r := range text {
		if r == 0 {
			return true
		}
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' && r != '\f' {
			controls++
		}
	}
	return controls*100 > utf8.RuneCountInString(text)*2
}
