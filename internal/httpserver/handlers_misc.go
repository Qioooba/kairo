package httpserver

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ---------- /downloads/<file> ----------

func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/downloads/")
	if name == "" ||
		strings.Contains(name, "..") ||
		strings.Contains(name, "\\") ||
		strings.Contains(name, "/") ||
		containsControlChar(name) {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.cur().DownloadDir(), filepath.FromSlash(name))
	// 必须在 DownloadDir 下
	abs, err := filepath.Abs(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	base, err := filepath.Abs(s.cur().DownloadDir())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !strings.HasPrefix(abs, base+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	lst, err := os.Lstat(abs)
	if err != nil || lst.Mode()&os.ModeSymlink != 0 {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", contentDispositionFilename(name))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	_, _ = io.Copy(w, f)
}

// containsControlChar 检查 s 是否含 ASCII 控制字符（含 \r \n \t）。
// 路径里出现这些字符除了注入 HTTP 头外没别的合法用途，一律拒绝。
func containsControlChar(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 || b == 0x7f {
			return true
		}
	}
	return false
}

// contentDispositionFilename 构造 Content-Disposition 响应头。
//
// 规则：
//   - ASCII 可见字符（0x21..0x7e 除 " \）：直接放进 filename="..."，并把 "
//     转义为 \"；
//   - 含其它字节（中文等非 ASCII）：追加 RFC 5987 filename*=UTF-8”<percent-encoded>，
//     老旧客户端拿 filename=，新客户端拿 filename*=。
//
// 这样既不会被引号 / 反斜杠 / 控制字符破坏头，又能正确显示中文文件名。
func contentDispositionFilename(name string) string {
	ascii, isPureASCII := sanitizeForHeaderASCII(name)
	if isPureASCII {
		return `attachment; filename="` + ascii + `"`
	}
	// 非 ASCII：给一个 fallback filename（去扩展名）+ RFC 5987 filename*
	fallback := sanitizeForHeaderASCIIKeepASCII(name)
	return `attachment; filename="` + fallback + `"; filename*=UTF-8''` + urlEncodePath(name)
}

// sanitizeForHeaderASCII 把 s 里的 " 和 \ 转义，返回值只含 ASCII。
// 返回值与 isPureASCII 表示原串是否就是纯 ASCII。
func sanitizeForHeaderASCII(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	isPure := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			isPure = false
			continue
		}
		if c > 0x7e {
			isPure = false
			continue
		}
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), isPure
}

// sanitizeForHeaderASCIIKeepASCII 把非 ASCII 字节替换成 _，保留 ASCII 字符和
// 已有转义行为（" → \"，\ → \\）。用于给非 ASCII 文件名一个 fallback。
func sanitizeForHeaderASCIIKeepASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c < 0x20 || c == 0x7f || c > 0x7e:
			b.WriteByte('_')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// urlEncodePath 按 RFC 5987 percent-encode：unreserved 字符原样，SPACE → %20，
// 其余按字节 %XX。调用方已经过滤控制字符，所以单字节都是合法 UTF-8 子节。
func urlEncodePath(s string) string {
	const upperHex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s) * 3)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(upperHex[c>>4])
			b.WriteByte(upperHex[c&0x0F])
		}
	}
	return b.String()
}
