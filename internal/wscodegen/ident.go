package wscodegen

import (
	"regexp"
	"strings"
	"unicode"
)

var javaReserved = map[string]bool{
	"abstract": true, "assert": true, "boolean": true, "break": true, "byte": true,
	"case": true, "catch": true, "char": true, "class": true, "const": true,
	"continue": true, "default": true, "do": true, "double": true, "else": true,
	"enum": true, "extends": true, "final": true, "finally": true, "float": true,
	"for": true, "goto": true, "if": true, "implements": true, "import": true,
	"instanceof": true, "int": true, "interface": true, "long": true, "native": true,
	"new": true, "package": true, "private": true, "protected": true, "public": true,
	"return": true, "short": true, "static": true, "strictfp": true, "super": true,
	"switch": true, "synchronized": true, "this": true, "throw": true, "throws": true,
	"transient": true, "try": true, "void": true, "volatile": true, "while": true,
	"true": true, "false": true, "null": true,
}

var nonIdent = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// JavaIdent 把任意名字收成合法 Java 标识符（字段 / 方法，首字母小写）。
func JavaIdent(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return "value"
	}
	s = nonIdent.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "value"
	}
	runes := []rune(s)
	if unicode.IsUpper(runes[0]) {
		runes[0] = unicode.ToLower(runes[0])
	}
	if unicode.IsDigit(runes[0]) {
		s = "n" + string(runes)
	} else {
		s = string(runes)
	}
	if javaReserved[s] {
		s = s + "_"
	}
	return s
}

// JavaClassName 把任意名字收成 PascalCase 类名。
func JavaClassName(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return "GeneratedType"
	}
	parts := nonIdent.Split(s, -1)
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		runes := []rune(p)
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	out := b.String()
	if out == "" {
		return "GeneratedType"
	}
	if unicode.IsDigit(rune(out[0])) {
		out = "T" + out
	}
	if javaReserved[strings.ToLower(out)] {
		out = out + "Type"
	}
	return out
}

// JavaPackage 把用户输入或 namespace 收成合法包名。
func JavaPackage(name string) string {
	s := strings.TrimSpace(strings.ToLower(name))
	s = strings.ReplaceAll(s, "\\", ".")
	s = strings.ReplaceAll(s, "/", ".")
	if s == "" {
		return "com.example.ws"
	}
	parts := strings.Split(s, ".")
	var out []string
	for _, p := range parts {
		p = nonIdent.ReplaceAllString(p, "")
		if p == "" {
			continue
		}
		if unicode.IsDigit(rune(p[0])) {
			p = "p" + p
		}
		if javaReserved[p] {
			p = p + "x"
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return "com.example.ws"
	}
	return strings.Join(out, ".")
}

// PackageFromNamespace 从 WSDL targetNamespace 推导包名。
func PackageFromNamespace(ns string) string {
	ns = strings.TrimSpace(ns)
	if ns == "" {
		return "com.example.ws"
	}
	ns = strings.TrimPrefix(ns, "http://")
	ns = strings.TrimPrefix(ns, "https://")
	ns = strings.TrimPrefix(ns, "urn:")
	ns = strings.TrimRight(ns, "/")
	ns = strings.ReplaceAll(ns, ":", ".")
	ns = strings.ReplaceAll(ns, "-", ".")
	parts := strings.Split(ns, "/")
	host := parts[0]
	hostParts := strings.Split(host, ".")
	// reverse host: cust.example.com -> com.example.cust
	if len(hostParts) >= 2 {
		for i, j := 0, len(hostParts)-1; i < j; i, j = i+1, j-1 {
			hostParts[i], hostParts[j] = hostParts[j], hostParts[i]
		}
	}
	var segs []string
	segs = append(segs, hostParts...)
	if len(parts) > 1 {
		segs = append(segs, parts[1:]...)
	}
	return JavaPackage(strings.Join(segs, "."))
}

// JavaString 把内容编成 Java 双引号字符串字面量。
func JavaString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 32 {
				b.WriteString(`\u00`)
				b.WriteString("0123456789abcdef"[r>>4 : (r>>4)+1])
				b.WriteString("0123456789abcdef"[r&15 : (r&15)+1])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func packageToPath(pkg string) string {
	return strings.ReplaceAll(JavaPackage(pkg), ".", "/")
}
