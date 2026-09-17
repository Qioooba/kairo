package sftpclient

// names.go — SFTP 远端文件名的 GBK/UTF-8 自适应。
//
// 背景（点文件面板里乱码文件夹进不去，FlashFXP 却正常）：
//   - SFTP 协议规定文件名是 UTF-8，但老 AIX / 旧 OpenSSH + GBK locale 的机器
//     磁盘上是 GBK 字节，server 直接把 GBK 字节发过来；
//   - pkg/sftp 不做转码，原样当 Go string 交出来（GBK 字节 = 非法 UTF-8）；
//   - 之前 Client.ReadDir 直接把 info.Name() 塞进 JSON，encoding/json 遇到非法
//     UTF-8 会替换成 U+FFFD，原字节永久丢失；前端点那个"�"发回来，后端再拿
//     破损串去 Stat/Open，server 当然报 no such file —— 观感就是"展示乱码+进不去"。
//   - FlashFXP 正常是因为它有字符集自适应（GBK 解码展示、回写时再编回 GBK），
//     部分 Xshell/旧工具和我们之前一样按纯 UTF-8 处理，所以也乱。
//
// 策略（FlashFXP 同款思路，后端透明，不改前端/API）：
//   - 出（server→前端）：DecodeServerName，合法 UTF-8 直通；非法则试 GB18030
//    （GBK 超集）解码，成功就给前端干净 UTF-8，JSON 不再丢字节；
//   - 回（前端→server）：EncodePathCandidates，ASCII 直通；含中文则多给一个
//     "UTF-8 显示串→GBK 字节串"候选，读操作按 [UTF-8, GBK] 顺序试，
//     哪个通走哪个。纯 UTF-8 盘零额外开销（一次即中），GBK 盘多一次回退。
//   - 写操作（Mkdir/Write/Upload/Create）不回退：目标本就不存在，"试错建文件"
//     会建出错编码的重名文件，保持 UTF-8 语义，由用户显式决定。

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

var (
	// ErrAmbiguousPath 当同目录下存在同名但不同编码的文件时返回，防止非幂等操作误伤其他文件。
	ErrAmbiguousPath = errors.New("目标路径存在多个同名但不同编码的文件，无法唯一定位")
)

// PathIdentityPrefix 是强绑定文件服务端身份的前缀
const PathIdentityPrefix = "kairo-raw:"

// DecodeServerNameWithEncoding 把 server 发来的原始文件名转成前端展示的 UTF-8 并返回识别编码。
func DecodeServerNameWithEncoding(raw string) (decoded string, encoding string) {
	if raw == "" {
		return "", "utf-8"
	}
	if utf8.ValidString(raw) {
		return raw, "utf-8"
	}
	if dec, err := io.ReadAll(transform.NewReader(
		bytes.NewReader([]byte(raw)), simplifiedchinese.GB18030.NewDecoder(),
	)); err == nil && len(dec) > 0 && utf8.ValidString(string(dec)) {
		return string(dec), "gb18030"
	}
	return raw, "unknown"
}

// DecodeServerName 把 server 发来的原始文件名转成前端可展示的 UTF-8。
// 合法 UTF-8（含纯 ASCII）零成本直通；GBK 字节解码成中文；实在解不出才原样返回。
func DecodeServerName(raw string) string {
	dec, _ := DecodeServerNameWithEncoding(raw)
	return dec
}

// EncodePathIdentity 将服务端原始路径转为不透明路径标识。
func EncodePathIdentity(rawPath string) string {
	if rawPath == "" {
		return ""
	}
	return PathIdentityPrefix + hex.EncodeToString([]byte(rawPath))
}

// DecodePathIdentity 解析路径标识。如果是 kairo-raw: 开头且合法绝对路径，则还原为原始字节串。
func DecodePathIdentity(p string) (string, bool) {
	if strings.HasPrefix(p, PathIdentityPrefix) {
		hexStr := strings.TrimPrefix(p, PathIdentityPrefix)
		rawBytes, err := hex.DecodeString(hexStr)
		if err == nil {
			raw := string(rawBytes)
			if strings.HasPrefix(raw, "/") && !strings.ContainsAny(raw, "\x00\r\n") {
				return path.Clean(raw), true
			}
		}
	}
	return p, false
}

// PathIDOf 返回指定目录下 FileInfo 的唯一路径标识。
func PathIDOf(parentDir string, fi os.FileInfo) string {
	if fi == nil {
		return ""
	}
	rawName := RawNameOf(fi)
	rawPath := path.Join(parentDir, rawName)
	return EncodePathIdentity(rawPath)
}

// RawNameOf 返回 FileInfo 在服务端的原始物理名。
func RawNameOf(fi os.FileInfo) string {
	if fi == nil {
		return ""
	}
	if r, ok := fi.(interface{ RawName() string }); ok {
		return r.RawName()
	}
	return fi.Name()
}

// EncodingOf 返回 FileInfo 在服务端的文件名编码 ("utf-8", "gb18030", "unknown")。
func EncodingOf(fi os.FileInfo) string {
	if fi == nil {
		return "utf-8"
	}
	if r, ok := fi.(interface{ Encoding() string }); ok {
		return r.Encoding()
	}
	if utf8.ValidString(fi.Name()) {
		return "utf-8"
	}
	return "unknown"
}

// EncodePathCandidates 把前端发来的 UTF-8 展示路径转成候选 server 路径。
// 返回 [展示串, GBK字节串]（去重后可能只有 1 个）。调用方按序试读，首个成功即用。
// 纯 ASCII 直接返回单候选：GBK 与 UTF-8 在 ASCII 段完全重合，转了也一样。
func EncodePathCandidates(display string) []string {
	if display == "" {
		return []string{display}
	}
	isASCII := true
	for i := 0; i < len(display); i++ {
		if display[i] >= 0x80 {
			isASCII = false
			break
		}
	}
	if isASCII {
		return []string{display}
	}
	gbk, _, err := transform.Bytes(simplifiedchinese.GB18030.NewEncoder(), []byte(display))
	if err != nil || len(gbk) == 0 || string(gbk) == display {
		return []string{display}
	}
	return []string{display, string(gbk)}
}

// decodedFileInfo 包装 os.FileInfo，Name() 为展示名，同时保留 RawName() 和 Encoding()
type decodedFileInfo struct {
	inner    os.FileInfo
	name     string
	rawName  string
	encoding string
}

func (d decodedFileInfo) Name() string       { return d.name }
func (d decodedFileInfo) RawName() string    { return d.rawName }
func (d decodedFileInfo) Encoding() string   { return d.encoding }
func (d decodedFileInfo) Size() int64        { return d.inner.Size() }
func (d decodedFileInfo) Mode() os.FileMode  { return d.inner.Mode() }
func (d decodedFileInfo) ModTime() time.Time { return d.inner.ModTime() }
func (d decodedFileInfo) IsDir() bool        { return d.inner.IsDir() }
func (d decodedFileInfo) Sys() interface{}   { return d.inner.Sys() }

// wrapDecoded 批量包装 FileInfo，暴露展示名并保留原始名与编码。
func wrapDecoded(infos []os.FileInfo) []os.FileInfo {
	out := make([]os.FileInfo, len(infos))
	for i, fi := range infos {
		if fi == nil {
			out[i] = fi
			continue
		}
		raw := fi.Name()
		dec, enc := DecodeServerNameWithEncoding(raw)
		out[i] = decodedFileInfo{
			inner:    fi,
			name:     dec,
			rawName:  raw,
			encoding: enc,
		}
	}
	return out
}

// wrapDecodedOne 单条目版本（Stat 用）。
func wrapDecodedOne(fi os.FileInfo) os.FileInfo {
	if fi == nil {
		return nil
	}
	raw := fi.Name()
	dec, enc := DecodeServerNameWithEncoding(raw)
	return decodedFileInfo{
		inner:    fi,
		name:     dec,
		rawName:  raw,
		encoding: enc,
	}
}
