package textcodec

import (
	"bytes"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestDecodeAndEncodeGB18030CRLF(t *testing.T) {
	raw, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("环境=生产\r\n端口=8080\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	text, info, binary, err := Decode(raw, "auto")
	if err != nil || binary || text != "环境=生产\n端口=8080\n" {
		t.Fatalf("text=%q info=%+v binary=%v err=%v", text, info, binary, err)
	}
	if info.Encoding != "gb18030" || info.EOL != "crlf" || info.BOM {
		t.Fatalf("unexpected info: %+v", info)
	}
	reencoded, err := Encode(text, info)
	if err != nil || !bytes.Equal(reencoded, raw) {
		t.Fatalf("round trip mismatch: %x/%x err=%v", reencoded, raw, err)
	}
}

func TestDecodeAndEncodeUTF16LEBOM(t *testing.T) {
	raw, err := Encode("alpha\nbeta\n", Info{Encoding: "utf-16le", EOL: "crlf", BOM: true})
	if err != nil {
		t.Fatal(err)
	}
	text, info, binary, err := Decode(raw, "auto")
	if err != nil || binary || text != "alpha\nbeta\n" {
		t.Fatalf("text=%q info=%+v binary=%v err=%v", text, info, binary, err)
	}
	if info.Encoding != "utf-16le" || info.EOL != "crlf" || !info.BOM {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestDecodeBinary(t *testing.T) {
	_, _, binary, err := Decode([]byte{0x89, 0x50, 0x4e, 0x47, 0, 1, 2, 3}, "auto")
	if err != nil || !binary {
		t.Fatalf("binary=%v err=%v", binary, err)
	}
}

func TestT068_TextEncodingBOM_CRLF_Unencodable(t *testing.T) {
	// 1. GBK encoding and CRLF line ending preservation
	rawGBK, err := Encode("测试=中文\n参数=123\n", Info{Encoding: "gbk", EOL: "crlf"})
	if err != nil {
		t.Fatalf("Encode GBK CRLF failed: %v", err)
	}
	if !bytes.Contains(rawGBK, []byte("\r\n")) {
		t.Fatalf("expected CRLF in raw bytes")
	}

	decoded, info, isBin, err := Decode(rawGBK, "gbk")
	if err != nil || isBin {
		t.Fatalf("Decode GBK failed: err=%v isBin=%v", err, isBin)
	}
	if info.Encoding != "gbk" || info.EOL != "crlf" || info.BOM {
		t.Fatalf("unexpected decoded info: %+v", info)
	}
	if decoded != "测试=中文\n参数=123\n" {
		t.Fatalf("decoded text mismatch: %q", decoded)
	}

	// 2. Unencodable character in GBK: must error and reject silent corruption
	_, errUnencodable := Encode("测试=中文 🌟 不可编码字符", Info{Encoding: "gbk", EOL: "crlf"})
	if errUnencodable == nil {
		t.Fatalf("expected error when encoding emoji in strict GBK, got nil")
	}

	// 3. UTF-8 with BOM preservation
	rawUTF8BOM, err := Encode("hello\nworld\n", Info{Encoding: "utf-8", EOL: "lf", BOM: true})
	if err != nil {
		t.Fatalf("Encode UTF-8 BOM failed: %v", err)
	}
	if !bytes.HasPrefix(rawUTF8BOM, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatalf("expected UTF-8 BOM prefix")
	}

	decodedBOM, infoBOM, _, err := Decode(rawUTF8BOM, "auto")
	if err != nil || !infoBOM.BOM || infoBOM.Encoding != "utf-8" || infoBOM.EOL != "lf" {
		t.Fatalf("unexpected BOM decode: text=%q, info=%+v, err=%v", decodedBOM, infoBOM, err)
	}
}

