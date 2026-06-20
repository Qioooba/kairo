// Package logquery 把"列文件、搜索、上下文"包装成受控的命令模板。
//
// 重要：所有传给 SSH 的命令由本包内的固定模板生成；
// 目录和文件名经过白名单校验；关键词经过严格转义。
package logquery

import (
	"fmt"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FileEntry 远程列出的日志文件
type FileEntry struct {
	Name       string `json:"name"`
	FullPath   string `json:"full_path"`
	Size       int64  `json:"size"`
	ModTime    string `json:"mod_time"` // RFC3339
	IsReadable bool   `json:"readable"`
}

// SearchHit 一次搜索命中
type SearchHit struct {
	Server   string `json:"server"`
	Dir      string `json:"dir"`
	File     string `json:"file"`
	FullPath string `json:"full_path"`
	LineNo   int    `json:"line_no"`
	Content  string `json:"content"`
}

// ContextLine 上下文中的一行
type ContextLine struct {
	LineNo  int    `json:"line_no"`
	Content string `json:"content"`
	Hit     bool   `json:"hit"`
}

// SearchKeyword 解析后的关键词单元
type SearchKeyword struct {
	Op     string // "and" / "or" / "term"
	Value  string
	Negate bool   // true 表示这是 !term 形式，最终用 grep -v
}

// illegalKeyKey 决定一个 token 是否被整体拒绝。
// 覆盖 shell 元字符、grep -E 元字符、伪 grep 选项（dash 开头）、路径式输入（/）。
var illegalKeyKey = regexp.MustCompile(`[\\\(\)\[\]\{\}\|\$\` + "`" + `&;<>!'"/\*\?\n\r\t~]`)

// ToEncodingEscaped 把 UTF-8 字符串按目标编码转成纯 ASCII 的 printf 转义序列。
//
// 设计要点：
//   - 只转"关键词 token"自己，不转 shell 结构、文件路径、&& || ! 等操作符。
//   - 远端 shell 仍按 UTF-8 解析；只有经过 $(printf %b '...') 展开后的字节流
//     才会以目标编码出现在 grep pattern 位置。
//   - 输出的全部是 \xHH 形式的 ASCII 字符，注入不到 shell。
//
// 用法：grep -nE "$(printf %b '<escaped>')" -- file1 file2 file3
// 其中 <escaped> 是本函数返回值，例："\xd0\xc5\xb4\xfb\xcf\xb5\xcd\xb3"（信贷系统 GBK）。
func ToEncodingEscaped(s string, encoding string) (string, error) {
	enc, err := pickEncoder(encoding)
	if err != nil {
		return "", err
	}
	pr, pw := io.Pipe()
	go func() {
		w := transform.NewWriter(pw, enc.NewEncoder())
		if _, err := w.Write([]byte(s)); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if err := w.Close(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()
	out, err := io.ReadAll(pr)
	if err != nil {
		return "", fmt.Errorf("按 %q 编码失败: %w", encoding, err)
	}
	var sb strings.Builder
	for _, b := range out {
		fmt.Fprintf(&sb, `\x%02x`, b)
	}
	return sb.String(), nil
}

// pickEncoder 根据配置名返回编码器。"utf-8" 等价于"啥也不做"，返回 nop 编码器。
func pickEncoder(name string) (encoding.Encoding, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "utf-8", "utf8":
		return encoding.Nop, nil
	case "gbk", "gb18030":
		return simplifiedchinese.GBK, nil
	default:
		return nil, fmt.Errorf("不支持的编码: %q", name)
	}
}

// 兼容旧调用路径的别名。
var illegalKey = illegalKeyKey

func EscapeKeyword(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return illegalKey.ReplaceAllString(s, "")
}

// ParseQuery 解析搜索表达式，支持 && || !
//
// 例: "Exception && userinfo" => [{term Exception}, {and}, {term userinfo}]
// 例: "Exception || Timeout"  => [{term Exception}, {or},  {term Timeout}]
// 例: "!DEBUG"                => [{term DEBUG, Negate: true}]
// 例: "Exception && !DEBUG"   => [{term Exception}, {and}, {term DEBUG, Negate: true}]
// 例: "!A || !B"              => [{term A, Negate: true}, {or}, {term B, Negate: true}]
func ParseQuery(q string) ([]SearchKeyword, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, fmt.Errorf("搜索关键词为空")
	}
	// 把 && || 替换成带空格的形式
	q = strings.ReplaceAll(q, "&&", " && ")
	q = strings.ReplaceAll(q, "||", " || ")
	// 把紧贴的 ! 变成 "! "，避免和后面的 term 黏在一起
	q = strings.ReplaceAll(q, "!", " ! ")
	// 折叠多余空格
	q = strings.Join(strings.Fields(q), " ")

	var out []SearchKeyword
	negateNext := false
	for _, tok := range strings.Fields(q) {
		switch tok {
		case "&&":
			out = append(out, SearchKeyword{Op: "and"})
		case "||":
			out = append(out, SearchKeyword{Op: "or"})
		case "!":
			negateNext = true
			continue
		default:
			// 拒绝对搜索无意义或危险的字符。
			if illegalKey.MatchString(tok) {
				return nil, fmt.Errorf("关键词含非法字符: %q", tok)
			}
			// 拒绝以 - 开头（看起来像 grep 的选项 flag）。
			if strings.HasPrefix(tok, "-") {
				return nil, fmt.Errorf("关键词不能以 '-' 开头: %q", tok)
			}
			cleaned := EscapeKeyword(tok)
			if cleaned == "" {
				return nil, fmt.Errorf("关键词为空: %q", tok)
			}
			out = append(out, SearchKeyword{Op: "term", Value: cleaned, Negate: negateNext})
			negateNext = false
		}
	}
	if len(out) == 0 || out[0].Op != "term" {
		return nil, fmt.Errorf("搜索表达式必须以关键词开头")
	}
	return out, nil
}

// ListCommand 构造"列出指定目录下匹配 patterns 的文件"命令
//
// 用 find + 多个 -name + -printf 拿 size/mtime/name。
// dir 和 pattern 都不在 shell 层做 glob 展开（用 -name 透传给 find）。
// 整体用单引号包，避免内嵌引号转义问题。
// 超时由 Go 客户端 ctx 控制，不依赖 Linux `timeout` 命令。
func ListCommand(dir string, patterns []string, max int) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("dir 不能为空")
	}
	if len(patterns) == 0 {
		patterns = []string{"*.log"}
	}
	if max <= 0 {
		max = 100
	}
	if strings.ContainsAny(dir, "'`$\\;") {
		return "", fmt.Errorf("dir 含非法字符: %q", dir)
	}
	for _, p := range patterns {
		if strings.ContainsAny(p, "'`$\\;") {
			return "", fmt.Errorf("pattern 含非法字符: %q", p)
		}
	}

	var patternExprs []string
	for _, p := range patterns {
		patternExprs = append(patternExprs, fmt.Sprintf(`-name %q`, p))
	}
	expr := strings.Join(patternExprs, " -o ")
	cmd := fmt.Sprintf(
		`sh -c 'cd %q && find . -maxdepth 1 -type f \( %s \) -printf "%%s\t%%T@\t%%p\n" | LC_ALL=C sort -k2,2nr | head -n %d'`,
		dir, expr, max,
	)
	return cmd, nil
}

// ParseListOutput 解析 find -printf 输出
//
// 每行: <size>\t<mtime_unix>\t<name>
func ParseListOutput(out string) ([]FileEntry, error) {
	var list []FileEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		size, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil {
			continue
		}
		mtimeF, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			continue
		}
		sec := int64(mtimeF)
		nsec := int64((mtimeF - float64(sec)) * 1e9)
		t := time.Unix(sec, nsec).UTC()
		name := strings.TrimPrefix(parts[2], "./")
		list = append(list, FileEntry{
			Name:       name,
			FullPath:   name,
			Size:       size,
			ModTime:    t.Format(time.RFC3339),
			IsReadable: size > 0,
		})
	}
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].ModTime > list[j].ModTime
	})
	return list, nil
}

// SearchCommand 构造"在 files 上按关键词搜索"的安全管道命令
//
// 编码（encoding）：
//   - utf-8（默认）：keyword 直接作为 UTF-8 字符串传递，远程 grep 按字节匹配；
//   - gbk / gb18030：keyword 先 UTF-8 → GBK，再用 printf %b '\xHH...' 形式交给远程 sh。
//     shell 结构、文件路径、&& || ! 都保持原样；只有"关键词 token"按目标编码转字节。
//     这样 UTF-8 页面输入的中文关键词能匹配 GBK 文件里的中文。
func SearchCommand(dir string, files []string, kw []SearchKeyword, max, timeoutSec int, encoding string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("dir 不能为空")
	}
	if len(files) == 0 {
		return "", fmt.Errorf("files 不能为空")
	}
	if len(kw) == 0 {
		return "", fmt.Errorf("kw 不能为空")
	}
	if max <= 0 {
		max = 200
	}
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if strings.ContainsAny(dir, "'`$\\;") {
		return "", fmt.Errorf("dir 含非法字符: %q", dir)
	}
	for _, f := range files {
		if strings.ContainsAny(f, "'`$\\;&|><\n\r*?") {
			return "", fmt.Errorf("file 含非法字符: %q", f)
		}
	}

	// 拆成 OR 段；每段内是 AND
	type group struct {
		pos []string
		neg []string
	}
	var groups []group
	cur := group{}
	flush := func() {
		if len(cur.pos) > 0 || len(cur.neg) > 0 {
			groups = append(groups, cur)
		}
		cur = group{}
	}
	for _, k := range kw {
		switch k.Op {
		case "term":
			if k.Negate {
				cur.neg = append(cur.neg, k.Value)
			} else {
				cur.pos = append(cur.pos, k.Value)
			}
		case "or":
			flush()
		}
	}
	flush()
	if len(groups) == 0 {
		return "", fmt.Errorf("没有可用的搜索关键词")
	}

	fileList := strings.Join(quoteArgs(files), " ")
	enc := strings.ToLower(strings.TrimSpace(encoding))
	// quoteForGrep 根据目标编码生成 grep 模式部分的 shell token：
	//   - utf-8：直接用 Go 的 %q 双引号包裹（UTF-8 字节安全）。
	//   - gbk：用 $(printf %b '\xHH...') 展开 GBK 字节。
	quoteForGrep := func(term string) (string, error) {
		if enc == "gbk" || enc == "gb18030" {
			esc, err := ToEncodingEscaped(term, encoding)
			if err != nil {
				return "", err
			}
			// 单引号包整个 printf 表达式，printf 的格式串再单引号包字节转义。
			// 整个 token 是纯 ASCII（0-9 a-f \ x ' $ ( ) ），不依赖任何外部变量。
			return fmt.Sprintf(`$(printf %%b '%s')`, esc), nil
		}
		return fmt.Sprintf("%q", term), nil
	}

	var pipes []string
	// 第一段：对 file 操作。
	// 关键：不管有没有正 term，第一步都要用 `grep -nE` 直接读文件列表，让 grep
	// 给每个文件加 `filename:lineno:` 前缀。如果用 `cat -- file1 file2 file3`
	// 拼成单流再喂给下游 grep，grep 看到单流就不再加前缀，前端解析就会错位。
	if len(groups[0].pos) > 0 {
		pat0, err := quoteForGrep(groups[0].pos[0])
		if err != nil {
			return "", err
		}
		pipes = append(pipes, fmt.Sprintf("grep -nE %s -- %s", pat0, fileList))
		for _, term := range groups[0].pos[1:] {
			pat, err := quoteForGrep(term)
			if err != nil {
				return "", err
			}
			pipes = append(pipes, fmt.Sprintf("grep -E %s", pat))
		}
	} else {
		// 第一段只有 neg（例: "!DEBUG"）。直接 `grep -nE "^." -- file1 file2 file3`
		// 让 grep 给每行加 `file:lineno:` 前缀，再串联 grep -vE。
		pipes = append(pipes, fmt.Sprintf("grep -nE %q -- %s", "^.", fileList))
	}
	for _, p := range groups[0].neg {
		pat, err := quoteForGrep(p)
		if err != nil {
			return "", err
		}
		pipes = append(pipes, fmt.Sprintf("grep -vE %s", pat))
	}
	// 后续 OR 段：每段从 files 独立 grep，再用 sort -u 合并去重
	if len(groups) > 1 {
		var branches []string
		for _, g := range groups[1:] {
			if len(g.pos) == 0 {
				continue
			}
			pat0, err := quoteForGrep(g.pos[0])
			if err != nil {
				return "", err
			}
			branch := fmt.Sprintf("grep -nE %s -- %s", pat0, fileList)
			for _, term := range g.pos[1:] {
				pat, err := quoteForGrep(term)
				if err != nil {
					return "", err
				}
				branch += " | grep -E " + pat
			}
			for _, p := range g.neg {
				pat, err := quoteForGrep(p)
				if err != nil {
					return "", err
				}
				branch += " | grep -vE " + pat
			}
			branches = append(branches, branch)
		}
		if len(branches) > 0 {
			pipes = append(pipes,
				"("+strings.Join(branches, "; ")+")",
				"LC_ALL=C sort -u",
			)
		}
	}
	pipes = append(pipes, fmt.Sprintf("head -n %d", max))

	cmdBody := strings.Join(pipes, " | ")
	// 超时由 Go 客户端 ctx + 内部 timer 控制，这里不再依赖 Linux `timeout` 命令，
	// 老 Linux / Alpine / 精简镜像也能跑。
	cmd := fmt.Sprintf(`sh -c 'cd %q && %s'`, dir, cmdBody)
	return cmd, nil
}

// quoteArgs 把每个 arg 包成单引号字符串
func quoteArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		a = strings.ReplaceAll(a, "'", "")
		out = append(out, "'"+a+"'")
	}
	return out
}

// ContextCommand 构造 "sed -n 'a,bp' file" 上下文查看命令
// 超时由 Go 客户端 ctx 控制，不依赖 Linux `timeout` 命令。
// 注意：sed 不接受 `--` 终止符；文件名来自上一步 ls（白名单内），不需要终止符。
func ContextCommand(dir, file string, line, before, after, timeoutSec int) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("dir 不能为空")
	}
	if strings.TrimSpace(file) == "" {
		return "", fmt.Errorf("file 不能为空")
	}
	if line <= 0 {
		return "", fmt.Errorf("line 必须 > 0")
	}
	if before < 0 {
		before = 0
	}
	if after < 0 {
		after = 0
	}
	if before > 500 {
		before = 500
	}
	if after > 500 {
		after = 500
	}
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if strings.ContainsAny(dir, "'`$\\;") {
		return "", fmt.Errorf("dir 含非法字符")
	}
	if strings.ContainsAny(file, "'`$\\;&|><\n\r*?") {
		return "", fmt.Errorf("file 含非法字符")
	}
	start := line - before
	if start < 1 {
		start = 1
	}
	end := line + after
	cleanFile := strings.ReplaceAll(file, "'", "")
	// 不在 sed 前面加 `--` 终止符（sed 不支持）；文件名来自 ls 白名单。
	cmd := fmt.Sprintf(`sh -c 'cd %q && sed -n "%d,%dp" %q'`,
		dir, start, end, cleanFile)
	return cmd, nil
}

// TailCommand 构造 "tail -F file" 实时跟踪命令。
//
// 用 -F 而不是 -f：
//   - -F 等价于 --follow=name --retry，文件被 rotate（mv + create）后会继续追新文件；
//   - 适合 WebSphere / catalina.out 这类按 size / date rotate 的日志。
//
// lines 是启动时先吐的最近行数（0 表示只追新增）。受限于安全约束，line > 1000 会被压回 1000。
//
// 与 ContextCommand 不同：tail 是流式命令，没有"超时"概念（一直跑直到客户端断开）；
// Go 侧通过 ctx 取消 + ssh session kill 来停。
func TailCommand(dir, file string, lines int) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("dir 不能为空")
	}
	if strings.TrimSpace(file) == "" {
		return "", fmt.Errorf("file 不能为空")
	}
	if lines < 0 {
		lines = 0
	}
	if lines > 1000 {
		lines = 1000
	}
	if strings.ContainsAny(dir, "'`$\\;") {
		return "", fmt.Errorf("dir 含非法字符")
	}
	if strings.ContainsAny(file, "'`$\\;&|><\n\r*?") {
		return "", fmt.Errorf("file 含非法字符")
	}
	cleanFile := strings.ReplaceAll(file, "'", "")
	cmd := fmt.Sprintf(`sh -c 'cd %q && tail -n %d -F %q 2>/dev/null'`,
		dir, lines, cleanFile)
	return cmd, nil
}
