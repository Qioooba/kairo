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
