// Package waspack 按信贷（credit）投产清单从本地 IntelliJ 工程抽取 java / jsp / class，
// 打成相对路径 tar，并生成与现网一致的 BakTT*.sh / TT*.sh。
package waspack

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

const (
	MaxManifestBytes = 1 << 20
	MaxFiles         = 3000
	MaxArchiveBytes  = int64(2 << 30)
	ListFileName     = "list.txt"
)

// FileKind 按投产清单里的后缀分类，便于预检统计。
type FileKind string

const (
	KindJava  FileKind = "java"
	KindClass FileKind = "class"
	KindJSP   FileKind = "jsp"
	KindOther FileKind = "other"
)

// Entry 是一条已规范化的相对路径（始终 ./src/... 这种 POSIX 形式）。
type Entry struct {
	Rel    string   `json:"rel"`
	Kind   FileKind `json:"kind"`
	Source string   `json:"source"` // listed / paired
}

func kindOf(rel string) FileKind {
	switch strings.ToLower(path.Ext(rel)) {
	case ".java":
		return KindJava
	case ".class":
		return KindClass
	case ".jsp", ".jspf", ".tag":
		return KindJSP
	default:
		return KindOther
	}
}

// ParseManifest 把粘贴的清单收成去重后的相对路径。
// 接受 ./src/a.java、src/a.java，以及仍带工程前缀的 D:/credit/src/a.java。
func ParseManifest(raw, projectDir string) ([]Entry, error) {
	if len(raw) > MaxManifestBytes {
		return nil, fmt.Errorf("清单超过 %d 字节", MaxManifestBytes)
	}
	projectPrefix := normalizeSlash(strings.TrimSpace(projectDir))
	projectPrefix = strings.TrimRight(projectPrefix, "/")
	projectBase := strings.ToLower(path.Base(projectPrefix))

	seen := map[string]struct{}{}
	out := make([]Entry, 0, 64)
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rel, err := normalizeRel(line, projectPrefix, projectBase)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行: %w", i+1, err)
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, Entry{Rel: rel, Kind: kindOf(rel), Source: "listed"})
		if len(out) > MaxFiles {
			return nil, fmt.Errorf("清单超过 %d 个文件", MaxFiles)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("清单为空")
	}
	return out, nil
}

func normalizeSlash(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.TrimSpace(p)
}

func normalizeRel(line, projectPrefix, projectBase string) (string, error) {
	p := normalizeSlash(line)
	if p == "" {
		return "", fmt.Errorf("空路径")
	}
	for _, r := range p {
		if r < 32 || r == 127 || r == 0 {
			return "", fmt.Errorf("含非法控制字符")
		}
	}
	if strings.Contains(p, "://") {
		return "", fmt.Errorf("不支持 URL 路径")
	}

	lower := strings.ToLower(p)
	if projectPrefix != "" {
		pref := strings.ToLower(projectPrefix)
		switch {
		case strings.HasPrefix(lower, pref+"/"):
			p = p[len(projectPrefix)+1:]
		case lower == pref:
			return "", fmt.Errorf("不能是工程根本身")
		}
	}
	if projectBase != "" {
		token := projectBase + "/"
		if strings.HasPrefix(lower, token) {
			p = p[len(token):]
		} else if strings.HasPrefix(lower, "./"+token) {
			p = p[len(token)+2:]
		}
	}

	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("相对路径为空")
	}
	if looksAbsolute(p) {
		return "", fmt.Errorf("禁止绝对路径 %q", p)
	}
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimLeft(p, "/")
	if p == "" || p == "." {
		return "", fmt.Errorf("相对路径为空")
	}
	// 清单相对 WebRoot / WAR，不带 WebRoot 前缀。
	if len(p) >= 8 && strings.EqualFold(p[:8], "webroot/") {
		p = p[8:]
	}
	clean := path.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("路径越界 %q", p)
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." || seg == "." || seg == "" {
			return "", fmt.Errorf("路径非法 %q", p)
		}
		if strings.IndexFunc(seg, unicode.IsControl) >= 0 {
			return "", fmt.Errorf("路径含控制字符")
		}
	}
	return "./" + clean, nil
}

func looksAbsolute(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	if len(p) >= 2 && unicode.IsLetter(rune(p[0])) && p[1] == ':' {
		return true
	}
	return strings.HasPrefix(p, "//")
}
