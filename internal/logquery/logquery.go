// Package logquery 把"列文件、搜索、上下文"包装成受控的命令模板。
//
// 重要：所有传给 SSH 的命令由本包内的固定模板生成；
// 目录和文件名经过白名单校验；关键词经过严格转义。
package logquery

import (
	"bytes"
	"fmt"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"path/filepath"
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

// ModTimeParsed 把 ModTime 字符串解析回 time.Time（B1 用）。
//
// 解析失败返回零值；FilterHitsByTimeWindow 会通过 SearchTimeWindow.Contains
// 看到零值 time 落不进任何有限窗口（Start 非零 + t 零值 < Start），导致被滤掉。
// 但因为 handler 总是先按窗口判断，没有窗口时全保留，所以解析失败只在
// 有窗口且该文件 mtime 不可解析时才有影响——直接被滤掉就行，不会 panic。
func (f FileEntry) ModTimeParsed() time.Time {
	if f.ModTime == "" {
		return time.Time{}
	}
	// 远端 gnu_find 模式给的是浮点时间戳（"1700000000.123"），
	// handler 那边 ParseListOutput 已经转成 RFC3339，所以这里只尝试 RFC3339。
	if t, err := time.Parse(time.RFC3339, f.ModTime); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, f.ModTime); err == nil {
		return t
	}
	return time.Time{}
}

// SearchHit 一次搜索命中
type SearchHit struct {
	Server    string `json:"server"`
	Dir       string `json:"dir"`
	File      string `json:"file"`
	FullPath  string `json:"full_path"`
	LineNo    int    `json:"line_no"`
	Content   string `json:"content"`
	IsContext bool   `json:"is_context"`
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
	Negate bool // true 表示这是 !term 形式，最终用 grep -v
}

// SearchTimeWindow 是"按文件 mtime 过滤搜索命中"的时间窗口（B1 新功能）。
//
// 语义：
//   - Start 零值 = 不限起点；End 零值 = 不限终点；
//   - 包含两端（>= Start && <= End）；
//   - 时区是 UTC；前端传 RFC3339（如 "2026-06-23T00:00:00Z"），handler 解析后
//     转 UTC 再喂给本结构。
//
// 设计动机：
//   - 用户搜"6 月 22 号 14 点 - 16 点的异常"，但远端 grep 只能对"整个文件"做匹配，
//     不可能给每条 hit 单独知道时间；
//   - 我们用"文件 mtime"做粗过滤：保留 mtime 在窗口内的文件的所有命中。
//   - 这是 v0.4 P3 体验项里最简单的"时间筛选"实现方式，足够应对"看某天日志"场景。
type SearchTimeWindow struct {
	Start time.Time
	End   time.Time
}

// IsZero 返回窗口是否"两个端点都没设"——handler 用这个判断要不要走全量。
func (w SearchTimeWindow) IsZero() bool {
	return w.Start.IsZero() && w.End.IsZero()
}

// Contains 判断 t 是否落在窗口内。
//
// 边界：Start 零值 = 不限起点；End 零值 = 不限终点；都零值 = 全包含。
// 都设置时使用闭区间 [Start, End]。
func (w SearchTimeWindow) Contains(t time.Time) bool {
	if w.IsZero() {
		return true
	}
	if !w.Start.IsZero() && t.Before(w.Start) {
		return false
	}
	if !w.End.IsZero() && t.After(w.End) {
		return false
	}
	return true
}

// FilterHitsByTimeWindow 按"文件 mtime"过滤 hits（B1）。
//
// files 是 ListCommand 解析出的文件列表（handler 调用前已拿到）。
// 实现：建 name → FileEntry 索引 → 命中行的 file 必须存在 → 看 mtime 是否在窗口内。
// 不在窗口内的 hit 整条跳过，不返回给前端。
//
// 边界：
//   - files 为 nil / 空：保留所有 hit（向后兼容，没有 mtime 信息时不过滤）；
//   - window.IsZero()：保留所有 hit（同上）；
//   - hit 里的 file 在 files 里找不到：保留（防御性，避免漏数据）。
func FilterHitsByTimeWindow(hits []SearchHit, files []FileEntry, window SearchTimeWindow) []SearchHit {
	if len(hits) == 0 {
		return hits
	}
	if len(files) == 0 || window.IsZero() {
		return hits
	}
	idx := make(map[string]FileEntry, len(files))
	for _, f := range files {
		idx[f.Name] = f
	}
	out := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		f, ok := idx[h.File]
		if !ok {
			// 远端 grep 给出的 file 不在 ListCommand 返回的列表里 —— 兜底保留
			// （理论上不应该发生，但解析层防御性写一下）。
			out = append(out, h)
			continue
		}
		if window.Contains(f.ModTimeParsed()) {
			out = append(out, h)
		}
	}
	return out
}

// illegalKeyKey 决定一个 token 是否被整体拒绝。
//
// 早期版本禁了一大堆 shell / grep 元字符（`< > ( ) [ ] { } * ? & ; | ! ~ ` $ \` 等），
// 结果把日志里极其常见的字符（HTML 标签、URL fragment、堆栈里的 `( )`、SQL 里的 `<>`）
// 一并挡在门外 —— 用户报障搜索 `Exception at com.example.Foo.bar(Foo.java:123)`
// 都搜不到，因为 `(` `)` 被拒。
//
// 现在的设计：
//   - grep -E 元字符 `(` `)` `[` `]` `{` `}` `*` `?` `.` `+` `^` `|` `\` 全部放行，
//     因为 `quoteForGrep` 用 `regexp.QuoteMeta` 把它们转成 `\( \)` 等字面匹配。
//   - shell 元字符 `<` `>` `&` `;` `|` `!` `~` `` ` `` `$` `"` 全部放行：
//     keyword 经过 `quoteForGrep` 之后，要么包在 `$(printf %b '...')` 的单引号里
//     （GBK），要么包在 Go `%q` 的双引号里（UTF-8）；再被外层 `sh -c '...'` 单引号
//     包裹一层，shell 永远不会展开它们。
//   - 真危险的只剩这几个，必须禁：
//       `'`  —— 唯一会破坏外层 sh -c '...' / 内部 $(printf %b '...') 单引号包裹的字符
//       `/`  —— 路径分隔符；保留它会让用户误以为可以搜路径
//       `\n` `\r` `\t` —— 控制字符；单引号包裹虽字面保留但会让 grep 解析乱
//       `\x00` —— NUL；远程 grep / ssh 通道都可能截断
var illegalKeyKey = regexp.MustCompile(`['/\x00\n\r\t]`)

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
	var buf bytes.Buffer
	w := transform.NewWriter(&buf, enc.NewEncoder())
	if _, err := w.Write([]byte(s)); err != nil {
		return "", fmt.Errorf("按 %q 编码失败: %w", encoding, err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("按 %q 编码失败: %w", encoding, err)
	}
	var sb strings.Builder
	for _, b := range buf.Bytes() {
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

// describeIllegalRune 在错误信息里给用户描述"第一个非法的字符是什么"。
// 例：'foo/bar' → "斜杠 /"；'a\nb' → "换行符"；"O'Brien" → "单引号 '"。
func describeIllegalRune(s string) string {
	for _, r := range s {
		switch r {
		case '\'':
			return "单引号 '"
		case '/':
			return "斜杠 /"
		case '\n':
			return "换行符"
		case '\r':
			return "回车符"
		case '\t':
			return "制表符"
		case 0:
			return "NUL 字符"
		}
	}
	return "非法字符"
}

// EscapeKeyword 对一个 term token 做"保留字面"的处理：仅 trim 首尾空白后返回。
//
// 早期版本这里用 `illegalKey.ReplaceAllString(s, "")` 静默删除"非法字符"——
// 这是 bug：用户输入 `123>` 会被悄悄删成 `123`，远程 grep 搜 `123` 当然查不到
// `123>`，而且不会报错，调试时极难发现"为啥我搜的关键词好像没生效"。
//
// 现在的设计：
//   - 真危险的字符在 ParseQuery 阶段（illegalKeyKey.MatchString）直接整体拒收，
//     抛错让前端 toast 提示用户。
//   - EscapeKeyword 拿到的是已经通过 ParseQuery 校验的 token，**不要再删任何字符**，
//     也不需要 QuoteMeta（那是 quoteForGrep 在构造 shell 管道时做的）。
func EscapeKeyword(s string) string {
	return strings.TrimSpace(s)
}

// ParseQuery 解析搜索表达式，支持 && || !
//
// 状态机校验（v0.4）：明确拒收"语法不完整"或"两个操作符黏在一起"的输入，
// 不再等到 SearchCommand 阶段才"用 grep 怪招兜底"。
//
// 例: "Exception && userinfo" => [{term Exception}, {and}, {term userinfo}]
// 例: "Exception || Timeout"  => [{term Exception}, {or},  {term Timeout}]
// 例: "!DEBUG"                => [{term DEBUG, Negate: true}]
// 例: "Exception && !DEBUG"   => [{term Exception}, {and}, {term DEBUG, Negate: true}]
// 例: "!A || !B"              => [{term A, Negate: true}, {or}, {term B, Negate: true}]
//
// 拒绝的形态（之前会被静默接受 → grep 返回空结果，调试极痛苦）：
//   - "A &&"        （尾随 && — 缺后半 term）
//   - "A ||"        （尾随 ||）
//   - "!DEBUG &&"   （尾随 && 在 !term 之后）
//   - "A && && B"   （连续 &&）
//   - "A && || B"   （&& 后面接 ||）
//   - "&& B"        （开头 &&）
//   - "|| B"        （开头 ||）
//   - "A && !"      （&& 后立即 ! — 没有 term 可 negate）
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

	// 三状态机：
	//   stateStart : 还没看到任何 term（或刚吃完 !）
	//   stateTerm  : 刚看到一个 term
	//   stateOp    : 刚看到一个 && 或 ||
	//
	// 转换规则：
	//   start + term         → term, append term
	//   start + (&& / ||)    → error "表达式必须以关键词开头"
	//   start + !            → stay start, 标记 negate
	//   term  + term         → error "缺少操作符"
	//   term  + (&& / ||)    → op,   append op
	//   term  + !            → error "缺少操作符 before !"
	//   op    + term         → term, append term
	//   op    + (&& / ||)    → error "连续操作符"
	//   op    + !            → stay op, 标记 negate
	// 终结：
	//   stateOp             → error "表达式以操作符结尾"
	const (
		stateStart = "start"
		stateTerm  = "term"
		stateOp    = "op"
	)

	var out []SearchKeyword
	negateNext := false
	state := stateStart
	tokens := strings.Fields(q)
	for i, tok := range tokens {
		switch tok {
		case "&&", "||":
			opName := tok
			if state == stateStart {
				return nil, fmt.Errorf("搜索表达式必须以关键词开头（位置 %d 出现 %q）", i, tok)
			}
			if state == stateOp {
				return nil, fmt.Errorf("搜索表达式出现连续操作符（位置 %d 出现第二个 %q）", i, tok)
			}
			// state == stateTerm
			if opName == "&&" {
				out = append(out, SearchKeyword{Op: "and"})
			} else {
				out = append(out, SearchKeyword{Op: "or"})
			}
			state = stateOp
		case "!":
			if state == stateTerm {
				return nil, fmt.Errorf("搜索表达式在关键词后再出现 '!' 却缺少操作符（位置 %d）", i)
			}
			// stateStart 或 stateOp 都允许 ! —— 它修饰紧跟的 term
			negateNext = true
			// state 不变
		default:
			// 拒绝对搜索无意义或危险的字符（v0.14：黑名单缩窄到 6 个真危险的；
			// 见 illegalKeyKey 注释）。错误信息告诉用户具体哪个字符有问题，
			// 而不是笼统的"非法字符"。
			if illegalKey.MatchString(tok) {
				return nil, fmt.Errorf("关键词不能含 %q（单引号 / 斜杠 / 控制字符；其他特殊字符如 ( ) [ ] { } * ? < > & ; | ! ~ 都可以搜）: %q", describeIllegalRune(tok), tok)
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
			state = stateTerm
		}
	}
	if state == stateOp {
		return nil, fmt.Errorf("搜索表达式以操作符结尾（缺后半关键词）")
	}
	if state == stateStart {
		// 仅由 ! 组成（"!" / "! !"）的情况
		return nil, fmt.Errorf("搜索表达式必须以关键词开头（不能只有 '!'）")
	}
	return out, nil
}

// ListCommand 构造"列出指定目录下匹配 patterns 的文件"命令
//
// 默认用 find + -printf（GNU/Linux/macOS）。
// listMode = "posix_ls" 时改用 ls -lt（AIX / 老 Unix / WebSphere 安全模式），
// 牺牲一些精度（mtime 精确到分钟）换兼容性。
//
// dir 和 pattern 都不在 shell 层做 glob 展开（用 -name 透传给 find）。
// 整体用单引号包，避免内嵌引号转义问题。
// 超时由 Go 客户端 ctx 控制，不依赖 Linux `timeout` 命令。
func ListCommand(dir string, patterns []string, max int, listMode string) (string, error) {
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

	switch strings.ToLower(strings.TrimSpace(listMode)) {
	case "", "auto", "gnu_find":
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
	case "posix_ls":
		// AIX / 老 Unix 没有 -printf。改用 ls -lt：
		//   -l  : 长格式（含 size）
		//   -t  : 按 mtime 倒序
		//   -1  : 一行一个文件
		//   -d  : 目录自身不展开
		// 多个 pattern 用 shell case 或 awk 过滤；为了兼容最差环境，
		// 把 pattern 透传给 grep -E（每个 pattern 转义成固定字符串）。
		//
		// 注意：AIX 上 ls 可能用 %y / %Y 等差异格式；这里只依赖最稳的
		// "-l" 列：mode links owner group size month day time/year name。
		// 解析时按"倒数第一段是 name，前面有 size"这种稳定结构切。
		if len(patterns) == 0 {
			return "", fmt.Errorf("posix_ls 模式需要至少一个 pattern")
		}
		// 把 patterns 拼成 find ... -name ... OR -name ... 的轻量过滤。
		// 这里实际是 pipe ls 到 awk，awk 按 name 列做 glob 匹配：
		//   { name = $NF; if (name ~ pattern) print ... }
		// 但 awk 不直接支持多个 pattern OR；改用 grep -E 多 pattern 串。
		// pattern 转义：只允许 glob 字符 * ? []，去掉其他特殊字符。
		cleanPatterns := make([]string, 0, len(patterns))
		for _, p := range patterns {
			// REVIEW-rc5 #5：原白名单只允许 glob 元字符 + ASCII 字母数字 + . - _，
			// 中文 / Unicode 后缀（如「日志.2026.gz」「Système.1.log」）会被静默丢弃。
			// 改成"排除法"——只去掉真正危险的 shell 元字符，其他字符（含中文 / Unicode）全保留。
			cleaned := make([]rune, 0, len(p))
			for _, r := range p {
				if isShellUnsafePatternRune(r) {
					continue
				}
				cleaned = append(cleaned, r)
			}
			if len(cleaned) == 0 {
				continue
			}
			cleanPatterns = append(cleanPatterns, string(cleaned))
		}
		if len(cleanPatterns) == 0 {
			return "", fmt.Errorf("posix_ls 模式所有 pattern 都非法")
		}
		// 用 grep -E 在 ls 输出里按 basename 过滤名字，保留 ls 整行
		// 给 ParseListOutputPOSIX 解析（size/mtime/name）。
		//
		// 顺序：ls -lt | grep -E ... | head -n max
		// （不是 head 在前！）v0.4 修复：head 在前的话会把"还没过滤到匹配"的行
		// 截掉，结果少几条日志。grep 在前意味着过滤后 head 按匹配行数截断，
		// 文件再多也只用 max 行 ls 输出（小 ls 在 grep 之前已经过滤了大部分），
		// 不会出现"head 截掉想看的行"。
		grepExprs := make([]string, 0, len(cleanPatterns))
		for _, p := range cleanPatterns {
			// 把 glob 转成正则：普通字符必须转义，避免 "." 等正则元字符误匹配任意字符。
			re := globPatternToRegexp(p)
			grepExprs = append(grepExprs, fmt.Sprintf("-e %q", "^.*"+re+"$"))
		}
		grepExpr := strings.Join(grepExprs, " ")
		cmd := fmt.Sprintf(
			`sh -c 'cd %q && ls -lt 2>/dev/null | grep -E %s | head -n %d'`,
			dir, grepExpr, max,
		)
		return cmd, nil
	default:
		return "", fmt.Errorf("不支持的 list_mode: %q（仅支持 gnu_find / posix_ls）", listMode)
	}
}

func globPatternToRegexp(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String()
}

// ParseListOutput 解析远端列目录输出，自动识别格式：
//   - gnu_find 模式（默认）：每行 <size>\t<mtime_unix>\t<name>
//   - posix_ls 模式：ls -lt 的长格式行，由 ParseListOutputPOSIX 解析
func ParseListOutput(out string) ([]FileEntry, error) {
	if looksLikePOSIXLSOutput(out) {
		return ParseListOutputPOSIX(out)
	}
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

// looksLikePOSIXLSOutput 简单判断是否是 ls -l 输出。
// 启发式：第一行是 "total N"（除非 -A 之类），每行起始是类似 -rw-r--r-- / drwxr-xr-x。
func looksLikePOSIXLSOutput(out string) bool {
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		return false
	}
	// 第一行是 "total N"
	if strings.HasPrefix(strings.TrimSpace(lines[0]), "total ") {
		return true
	}
	// 或者任何一行起始是 -rw / drw / lrw 等 mode 串
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if len(ln) < 10 {
			continue
		}
		if ln[0] == '-' || ln[0] == 'd' || ln[0] == 'l' || ln[0] == 'c' || ln[0] == 'b' || ln[0] == 'p' || ln[0] == 's' {
			// 形如 "-rw-r--r--"
			if ln[1] == 'r' || ln[1] == 'w' || ln[1] == '-' || ln[1] == 'x' {
				return true
			}
		}
	}
	return false
}

// ParseListOutputPOSIX 解析 ls -lt 长格式输出。
//
// 形如：
//
//	-rw-r--r-- 1 user group 12345 Jun 21 10:00 SystemOut.log
//	-rw-r--r-- 1 user group 67890 Jun 21 09:30 SystemOut_20260619.log
//
// 取 size（第 5 字段）、mtime（第 6/7/8 字段）、name（最后一字段）。
// mtime 不能精确到秒（ls 默认不显示），所以 ModTime 用 RFC3339 字符串，
// 排序时按字典序倒序——文件越多越不准，但能区分"今天的"和"昨天的"。
func ParseListOutputPOSIX(out string) ([]FileEntry, error) {
	var list []FileEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// 跳过 "total N" 头
		if strings.HasPrefix(strings.TrimSpace(line), "total ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		// 第 5 字段是 size（mode links owner group size ...）
		size, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil {
			continue
		}
		// mtime 在第 6/7/8 字段（month day time 或 month day year）
		mtimeStr := fields[5] + " " + fields[6] + " " + fields[7]
		// name 是最后一字段
		name := fields[len(fields)-1]
		// 把 mtimeStr 尽力解析成 RFC3339；解析失败就原样塞字符串（让排序按字典）
		modTime := mtimeStr
		for _, layout := range []string{
			"Jan 2 15:04 2006",
			"Jan 2 2006",
			"Jan _2 15:04 2006",
			"Jan _2 2006",
		} {
			if t, err := time.Parse(layout, mtimeStr); err == nil {
				modTime = t.UTC().Format(time.RFC3339)
				break
			}
		}
		list = append(list, FileEntry{
			Name:       name,
			FullPath:   name,
			Size:       size,
			ModTime:    modTime,
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
//
// ignoreCase（v0.13）：
//   - true → 所有 grep 加 -i 标志（pattern 与文件内容都转小写匹配）。
//   - false（默认）→ 大小写敏感，保持原行为。
//   - 影响所有 grep 调用（首段 AND、OR 分支、neg 的 grep -vE、纯 neg 的 "^"）。
func SearchCommand(dir string, files []string, kw []SearchKeyword, max, timeoutSec int, encoding string, ignoreCase bool) (string, error) {
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
	var groups []orGroup
	cur := orGroup{}
	flush := func() {
		if len(cur.pos) > 0 || len(cur.neg) > 0 {
			groups = append(groups, cur)
		}
		cur = orGroup{}
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

	enc := strings.ToLower(strings.TrimSpace(encoding))
	// v0.13：ignoreCase 标志，统一附加到所有 grep 命令上（首段 AND / OR 分支 / neg 过滤 / 纯 neg 的 "^"）。
	// grep 的 -i 放最前面，配合原有的 -HnE 形成 "grep -iHnE" 形式，对所有中间 grep 同样保持 -i。
	caseFlag := ""
	if ignoreCase {
		caseFlag = "i"
	}
	// P1-bugfix：每个文件单独 grep -m 限制单文件最大匹配数，确保所有文件都被搜索到。
	// 原问题：grep file1 file2 file3 | head -n 200 时，如果 file1 匹配了 200+ 行，
	// head 读完就关管道，grep 收 SIGPIPE 退出，file2/file3 根本没被搜。
	// 修复策略：
	//   1. 用 for 循环逐个文件 grep；
	//   2. 每个 grep 加 -m perFileMax，限制单文件输出上限；
	//   3. 所有文件结果合并后，再全局 sort -u | head -n max 截断总数。
	// perFileMax 计算：平均分配 + 缓冲，保证每文件至少 30 行（如果有的话）。
	perFileMax := max / len(files)
	if perFileMax < 30 {
		perFileMax = 30
	}
	perFileMax += 20 // 加缓冲，抵消 AND 链后续 grep 过滤掉的行
	// 单文件场景：不需要循环，直接 grep 单文件（保持原有行为，只是加 -m 保险）
	singleFile := len(files) == 1
	// 构造 for 循环的文件列表：用单引号包裹每个文件名，安全拼接
	quotedFiles := quoteArgs(files)
	// quoteForGrep 根据目标编码生成 grep 模式部分的 shell token：
	//   - utf-8：直接用 Go 的 %q 双引号包裹（UTF-8 字节安全）。
	//   - gbk：用 $(printf %b '\xHH...') 展开 GBK 字节。
	quoteForGrep := func(term string) (string, error) {
		pattern := regexp.QuoteMeta(term)
		if enc == "gbk" || enc == "gb18030" {
			esc, err := ToEncodingEscaped(pattern, encoding)
			if err != nil {
				return "", err
			}
			// 单引号包整个 printf 表达式，printf 的格式串再单引号包字节转义。
			// 整个 token 是纯 ASCII（0-9 a-f \ x ' $ ( ) ），不依赖任何外部变量。
			return fmt.Sprintf(`$(printf %%b '%s')`, esc), nil
		}
		return fmt.Sprintf("%q", pattern), nil
	}

	// buildPerFileBranch 构建"对单个文件 $f"执行的 grep 管道（一个 OR 分支）。
	// fileArg 是 grep 读文件的参数——单文件直接传文件名，多文件传循环变量 "$f"。
	// 第一个 grep 加 -m perFileMax 限制单文件输出，防止单个文件吃光所有配额。
	buildPerFileBranch := func(g orGroup, fileArg string) (string, error) {
		var branch string
		if len(g.pos) > 0 {
			pat0, err := quoteForGrep(g.pos[0])
			if err != nil {
				return "", err
			}
			// 关键：grep -H 必须加（即使单文件），保证输出有 filename: 前缀；
			// grep -m N 限制单文件最大匹配行数，确保后续文件能被搜到。
			branch = fmt.Sprintf("LC_ALL=C grep -HnE%s -m %d %s -- %s", caseFlag, perFileMax, pat0, fileArg)
			for _, term := range g.pos[1:] {
				pat, err := quoteForGrep(term)
				if err != nil {
					return "", err
				}
				branch += " | LC_ALL=C grep -" + caseFlag + "E " + pat
			}
		} else {
			// 纯 neg（例 "!DEBUG"）：用 "^" 匹配所有行，同样加 -m 限制。
			branch = fmt.Sprintf("LC_ALL=C grep -HnE%s -m %d %q -- %s", caseFlag, perFileMax, "^", fileArg)
		}
		for _, p := range g.neg {
			pat, err := quoteForGrep(p)
			if err != nil {
				return "", err
			}
			branch += " | LC_ALL=C grep -v" + caseFlag + "E " + pat
		}
		return branch, nil
	}

	var cmdBody string
	if singleFile {
		// 单文件：不需要 for 循环，直接 grep 该文件
		var allBranches []string
		for _, g := range groups {
			b, err := buildPerFileBranch(g, quotedFiles[0])
			if err != nil {
				return "", err
			}
			allBranches = append(allBranches, b)
		}
		if len(allBranches) == 1 {
			cmdBody = allBranches[0] + " | LC_ALL=C sort -u | head -n " + strconv.Itoa(max)
		} else {
			cmdBody = "(" + strings.Join(allBranches, "; ") + ") | LC_ALL=C sort -u | head -n " + strconv.Itoa(max)
		}
	} else {
		// 多文件：for f in ...; do ...; done 循环逐个 grep，每文件用 -m 限制
		// 结构：
		//   (for f in 'f1' 'f2' 'f3'; do
		//     [OR 多分支：( branch1_for_f; branch2_for_f )]
		//     [单分支：branch1_for_f]
		//   done) | sort -u | head -n max
		var perFileParts []string
		for _, g := range groups {
			b, err := buildPerFileBranch(g, `"$f"`)
			if err != nil {
				return "", err
			}
			perFileParts = append(perFileParts, b)
		}
		var loopBody string
		if len(perFileParts) == 1 {
			loopBody = perFileParts[0]
		} else {
			// OR：多分支放进子 shell，保证分支间互不干扰（P0-3 修复语义）
			loopBody = "(" + strings.Join(perFileParts, "; ") + ")"
		}
		fileList := strings.Join(quotedFiles, " ")
		cmdBody = "(for f in " + fileList + "; do " + loopBody + "; done) | LC_ALL=C sort -u | head -n " + strconv.Itoa(max)
	}

	// 超时由 Go 客户端 ctx + 内部 timer 控制，这里不再依赖 Linux `timeout` 命令，
	// 老 Linux / Alpine / 精简镜像也能跑。
	cmd := fmt.Sprintf("sh -c %s", shellQuote("cd "+shellQuote(dir)+" && "+cmdBody))
	return cmd, nil
}

// orGroup 是 SearchCommand 把 kw 切分成"OR 段"时用的内部容器：
//   - pos: 当前段里的"正"term（被 grep -E / grep -HnE 命中的）
//   - neg: 当前段里的"负"term（被 grep -vE 排除的）
//
// 每个 group 在 SearchCommand 里会被构造成一个独立分支，
// 所有分支放进统一子 shell 用 sort -u 合并（P0-3 修复 OR 语义）。
type orGroup struct {
	pos []string
	neg []string
}

// quoteArgs 把每个 arg 包成单引号字符串
func quoteArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, shellQuote(a))
	}
	return out
}

// shellQuote 用 POSIX 单引号形式安全引用一个 shell token。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isShellUnsafePatternRune REVIEW-rc5 #5：判断一个 rune 在 posix_ls pattern 中
// 是否属于 shell 危险字符。返回 true 表示需要从 pattern 中过滤掉。
//
// 危险字符清单（POSIX shell 元字符，会被 ls / grep 当成语法 / 注入载体）：
//   - ' " ` $ \ ; & | < > ( ) { } —— 注入 / 重定向 / 子 shell
//   - ! —— bash 历史展开 + 部分 shell 操作符
//   - \n \r \t \x00 —— 控制字符 / NUL（截断）
//   - ~ —— 用户主目录展开
//   - = —— 部分 shell 当作赋值
//
// glob 元字符 * ? [ ] 以及 ASCII 字母数字、中文 / Unicode 全部允许。
// pattern 最终会被包在 grep -E 里再用 shellQuote 二次转义，所以即使 pattern 含
// 看起来"危险"的字符（比如空格）也不会触发 shell 注入——这一层是纵深防御。
func isShellUnsafePatternRune(r rune) bool {
	switch r {
	case '\'', '"', '`', '$', '\\', ';', '&', '|', '<', '>',
		'(', ')', '{', '}', '!', '~', '=', '\n', '\r', '\t', 0:
		return true
	}
	return false
}

// ContextCommand 构造 "sed -n 'a,bp' file" 上下文查看命令
// 超时由 Go 客户端 ctx 控制，不依赖 Linux `timeout` 命令。
// 注意：sed 不接受 `--` 终止符；文件名来自上一步 ls（白名单内），不需要终止符。
//
// 安全要点：file 必须是没有目录分隔符的纯文件名（不含 /、\、..）。
// 防止类似 "../etc/passwd" 越过 log_dir 白名单读父目录文件。
// 实际生产环境还是建议 handler 端再次校验 file 必须在 ListCommand 返回的 files 列表里。
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
	if before > 5000 {
		before = 5000
	}
	if after > 5000 {
		after = 5000
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
	// 路径穿越防护：file 必须是 basename，不允许任何目录分隔符或 .. 跳出。
	if file == "." || file == ".." {
		return "", fmt.Errorf("file 不允许为 '.' 或 '..'")
	}
	if strings.ContainsAny(file, "/\\") {
		return "", fmt.Errorf("file 不允许包含路径分隔符: %q", file)
	}
	if strings.Contains(file, "..") {
		return "", fmt.Errorf("file 不允许包含 '..'")
	}
	start := line - before
	if start < 1 {
		start = 1
	}
	end := line + after
	// REVIEW-rc5 #3：上游已经校验 file 不含 '，这里不再做 ReplaceAll 静默删 —
	// 万一上游重构漏校验，O'Brien.log 会变成 OBrien.log 然后读到错的文件还不报错。
	// 纵深防御：再 assert 一次，含 ' 直接报错，绝不静默删。
	if strings.Contains(file, "'") {
		return "", fmt.Errorf("file 含非法字符 '（纵深防御）")
	}
	// 不在 sed 前面加 `--` 终止符（sed 不支持）；文件名来自 ls 白名单。
	cmd := fmt.Sprintf(`sh -c 'cd %q && sed -n "%d,%dp" %q'`,
		dir, start, end, file)
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
	// 路径穿越防护：file 必须是 basename。
	if file == "." || file == ".." {
		return "", fmt.Errorf("file 不允许为 '.' 或 '..'")
	}
	if strings.ContainsAny(file, "/\\") {
		return "", fmt.Errorf("file 不允许包含路径分隔符: %q", file)
	}
	if strings.Contains(file, "..") {
		return "", fmt.Errorf("file 不允许包含 '..'")
	}
	// REVIEW-rc5 #3：纵深防御，再 assert 一次，绝不静默 ReplaceAll。
	if strings.Contains(file, "'") {
		return "", fmt.Errorf("file 含非法字符 '（纵深防御）")
	}
	cmd := fmt.Sprintf(`sh -c 'cd %q && tail -n %d -F %q 2>/dev/null'`,
		dir, lines, file)
	return cmd, nil
}

// LineCountCommand 构造"统计 file 当前行数"的命令模板。
//
// 用 awk 'END{print NR}' 而不是 wc -l：
//
//   - wc -l 数 `\n` 数量，最后一行没 `\n` 会少算 1；
//   - awk 'END{print NR}' 按"awk 读到的最终记录数"算，兼容末尾无换行；
//   - awk 在 GNU/Linux/macOS/AIX/精简镜像都自带，比 wc 更稳定；
//   - 空文件输出 "0"。
//
// 安全要点：
//   - file 必须是没有目录分隔符的纯文件名；
//   - 走和 TailCommand 一样的白名单规则，不允许出现路径穿越字符；
//   - 2>/dev/null 兜底：文件不存在 / 不可读时 awk 退出码非 0，返回 stderr，我们忽略
//     不出 stdout，让 baseline 拿不到（兜底路径"前端不显示行号"）。
//
// 与 TailCommand 的语义区别：这是"一次性命令"，由 Go 端同步调一次拿到 baseline，
// 不是流式。
func LineCountCommand(dir, file string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("dir 不能为空")
	}
	if strings.TrimSpace(file) == "" {
		return "", fmt.Errorf("file 不能为空")
	}
	if strings.ContainsAny(dir, "'`$\\;") {
		return "", fmt.Errorf("dir 含非法字符")
	}
	if strings.ContainsAny(file, "'`$\\;&|><\n\r*?") {
		return "", fmt.Errorf("file 含非法字符")
	}
	// 路径穿越防护：file 必须是 basename。
	if file == "." || file == ".." {
		return "", fmt.Errorf("file 不允许为 '.' 或 '..'")
	}
	if strings.ContainsAny(file, "/\\") {
		return "", fmt.Errorf("file 不允许包含路径分隔符: %q", file)
	}
	if strings.Contains(file, "..") {
		return "", fmt.Errorf("file 不允许包含 '..'")
	}
	// REVIEW-rc5 #3：纵深防御，再 assert 一次，绝不静默 ReplaceAll。
	if strings.Contains(file, "'") {
		return "", fmt.Errorf("file 含非法字符 '（纵深防御）")
	}
	// awk 脚本作为 shell token 直接拼（不是用户输入），固定字符串安全。
	cmd := fmt.Sprintf(`sh -c 'cd %q && awk "END{print NR}" %q 2>/dev/null'`,
		dir, file)
	return cmd, nil
}

// ContextLinesForHitsCommand 构建一个 awk 命令，一次性输出多个命中行及其前后 N 行上下文。
//
// 输入：
//   - dir: 日志目录
//   - file: 文件名（纯 basename，无路径分隔符）
//   - hitLines: 匹配行号集合（已去重）
//   - ctx: 每个匹配行前后取 N 行上下文（0 表示仅返回匹配行本身）
//
// 输出格式（每行）：
//   - 匹配行：filename:lineno:content  （第一个分隔符是 :）
//   - 上下文行：filename-lineno:content （第一个分隔符是 -）
//
// 输出不包含重复行（同一行既是匹配又是上下文时，优先标记为匹配行）。
// 行按行号升序输出。
func ContextLinesForHitsCommand(dir, file string, hitLines []int, ctx int) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("dir 不能为空")
	}
	if strings.TrimSpace(file) == "" {
		return "", fmt.Errorf("file 不能为空")
	}
	if ctx < 0 {
		ctx = 0
	}
	if ctx > 50 {
		ctx = 50
	}
	if strings.ContainsAny(dir, "'`$\\;") {
		return "", fmt.Errorf("dir 含非法字符")
	}
	if strings.ContainsAny(file, "'`$\\;&|><\n\r*?/\\") {
		return "", fmt.Errorf("file 含非法字符: %q", file)
	}
	if file == "." || file == ".." {
		return "", fmt.Errorf("file 不允许为 '.' 或 '..'")
	}
	if strings.Contains(file, "..") {
		return "", fmt.Errorf("file 不允许包含 '..'")
	}

	// 去重 + 构建 hits 行号集合
	hitsSet := make(map[int]bool)
	for _, ln := range hitLines {
		if ln > 0 {
			hitsSet[ln] = true
		}
	}
	if len(hitsSet) == 0 {
		return "", fmt.Errorf("没有有效的命中行")
	}

	// 构建 awk 脚本
	// 策略：
	//   1. BEGIN 块：把 hitLines 列表填进 is_hit map
	//   2. 逐行扫描：先判断 FNR 是否在任意 [hit-ctx, hit+ctx] 窗口内（needed map）
	//   3. 对 needed 行：按 is_hit 选择前缀分隔符（: 或 -），输出 filename SEP FNR : content
	//
	// 注意：awk 命令通过 -v 传入参数，hit 列表用逗号分隔字符串传入再 split。
	hitsList := make([]string, 0, len(hitsSet))
	for h := range hitsSet {
		hitsList = append(hitsList, strconv.Itoa(h))
	}
	hitsStr := strings.Join(hitsList, ",")

	// REVIEW-rc5 #3：纵深防御，上游已经校验不含 '，这里再 assert 一次，绝不静默 ReplaceAll。
	// hitsStr 是 Go strconv.Itoa 出来的纯数字，理论上不可能含 '，但还是 assert 一下。
	if strings.Contains(file, "'") {
		return "", fmt.Errorf("file 含非法字符 '（纵深防御）")
	}
	if strings.Contains(hitsStr, "'") {
		return "", fmt.Errorf("hit 行号非法（不应出现 '）")
	}
	cleanFile := file
	cleanHitsStr := hitsStr
	cleanCtx := strconv.Itoa(ctx)

	// awk 脚本：
	// -v hits="10,25,30" -v ctx=N -v fname="file.log"
	// 先 split hits 到 h_arr，再标记 is_hit 和 needed
	// 主循环只处理 needed 行，输出 fname SEP FNR : content
	awkScript := `
BEGIN {
	split(hits, h_arr, ",")
	for (i in h_arr) {
		h = int(h_arr[i])
		if (h <= 0) continue
		is_hit[h] = 1
		s = h - ctx; if (s < 1) s = 1
		e = h + ctx
		for (j = s; j <= e; j++) needed[j] = 1
	}
}
FNR in needed {
	sep = (FNR in is_hit) ? ":" : "-"
	printf "%s%s%d:", fname, sep, FNR
	print $0
}
`

	// 压缩空白（避免多行脚本被 shell 解释问题）—— 简单替换换行和多空格
	awkScript = strings.TrimSpace(awkScript)
	awkScript = strings.ReplaceAll(awkScript, "\n", " ")
	awkScript = strings.ReplaceAll(awkScript, "\t", " ")

	cmd := fmt.Sprintf(
		`sh -c 'cd %q && awk -v hits=%q -v ctx=%s -v fname=%q '"'"'%s'"'"' %q'`,
		dir, cleanHitsStr, cleanCtx, cleanFile, awkScript, cleanFile,
	)
	return cmd, nil
}

// ParseContextEnrichedOutput 解析 ContextLinesForHitsCommand 的输出，返回 SearchHit 切片。
//
// 解析规则：
//   - 匹配行格式：filename:lineno:content  → IsContext = false
//   - 上下文行格式：filename-lineno:content → IsContext = true
//   - 空行跳过
//
// server/dir 用于填充 SearchHit 的 Server/Dir/FullPath 字段。
// files 用于白名单校验（可选，为 nil/空则跳过校验）。
//
// 输出排序（v0.14）：
//   - 同一 file 内部按 LineNo **降序**（最晚的命中在前面，符合用户
//     "最新的排最上方"的直觉）。
//   - 不同 file 之间按 mtime **降序**（最近修改的文件在前面，
//     让用户能按"文件时间线"自然地看新不看旧）。
//   - mtime 解析失败的文件用 file 字典序倒序兜底（保持稳定有序）。
//   - hits 里的 file 不在 files 白名单里（防御性情况）的，排到最末。
//
// 早期版本用的是 `file asc + line asc` —— file 按字典序聚类、line 升序。
// 实际产品里 file 字典序对用户没意义（SystemErr < SystemOut 是巧合，不是时间线），
// 而且 line 升序导致"最早出现的命中在最上面"——用户要"最新"必须往下翻很长。
func ParseContextEnrichedOutput(out, server, dir string, files []FileEntry) []SearchHit {
	byName := make(map[string]FileEntry, len(files))
	for _, f := range files {
		byName[f.Name] = f
	}
	useWhitelist := len(byName) > 0

	seen := make(map[string]bool)
	var hits []SearchHit

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}

		// REVIEW-rc5 #1：filename 可能含 : 或 -（AIX 老系统尤其常见），
		// 原算法 strings.IndexAny(":-") 找第一个匹配，会把 "SystemOut:20260619.log:123:err"
		// 解析成 file="SystemOut" lineno="20260619.log"（atoi 失败 → 整行丢弃）。
		// 修复：扫描整行找第一个形如 "<sep><digits><:>" 或 "<sep><digits>$" 的子串，
		// 其中 sep 是 : 或 -，digits 是 lineno。这要求 lineno 必须是纯数字——能完全消除
		// filename 含 : 或 - 时的歧义。
		file, sep, ln, content, ok := parseSearchLine(line)
		if !ok {
			continue
		}
		if useWhitelist {
			if _, ok := byName[file]; !ok {
				continue
			}
		}

		isContext := sep == '-'
		key := fmt.Sprintf("%s:%d", file, ln)
		if seen[key] {
			// 如果已经记录过，且之前是上下文但现在是匹配行，需要更新
			// 找到已有条目修改 IsContext
			if !isContext {
				for i := range hits {
					if hits[i].File == file && hits[i].LineNo == ln {
						hits[i].IsContext = false
						break
					}
				}
			}
			continue
		}
		seen[key] = true

		hits = append(hits, SearchHit{
			Server:    server,
			Dir:       dir,
			File:      file,
			FullPath:  filepath.ToSlash(filepath.Join(dir, file)),
			LineNo:    ln,
			Content:   content,
			IsContext: isContext,
		})
	}

	// v0.14：先按 mtime 倒序给 file 排名（最新 = rank 0），解析失败或不在白名单的 file
	// 用 unknownRank 兜底（保证排到最末，不会污染时间线）。
	const unknownRank = 1 << 30
	fileRank := make(map[string]int, len(byName))
	{
		// 收集"已知的 file"列表（mtime 可解析才进排序，否则进 unknownRank）
		known := make([]FileEntry, 0, len(byName))
		for _, f := range byName {
			if !f.ModTimeParsed().IsZero() {
				known = append(known, f)
			}
		}
		// 已知 file 按 mtime 倒序
		sort.SliceStable(known, func(i, j int) bool {
			return known[i].ModTimeParsed().After(known[j].ModTimeParsed())
		})
		for i, f := range known {
			fileRank[f.Name] = i
		}
		// mtime 解析失败的 file 用字典序倒序接在已知 file 之后
		unknown := make([]string, 0)
		for _, f := range byName {
			if _, ok := fileRank[f.Name]; !ok {
				unknown = append(unknown, f.Name)
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(unknown)))
		for i, name := range unknown {
			// 已知 file 数量 + i —— 让 mtime 已知 file 永远在前
			fileRank[name] = len(known) + i
		}
	}

	// hits 排序：file 按 rank 升序（同 file 聚类），file 内 line 降序（最晚在前）
	sort.SliceStable(hits, func(i, j int) bool {
		ri, oki := fileRank[hits[i].File]
		if !oki {
			ri = unknownRank
		}
		rj, okj := fileRank[hits[j].File]
		if !okj {
			rj = unknownRank
		}
		if ri != rj {
			return ri < rj
		}
		// 同 file：line 降序
		if hits[i].LineNo != hits[j].LineNo {
			return hits[i].LineNo > hits[j].LineNo
		}
		// 同行号：命中行（IsContext=false）排在上下文行前面，让用户先看到"真命中"
		return !hits[i].IsContext && hits[j].IsContext
	})

	return hits
}

// parseSearchLine 解析单行 grep 输出。
//
// 格式：filename SEP lineno : content
//   - SEP 是 `:`（匹配行）或 `-`（上下文行）
//   - lineno 必须是纯数字
//
// REVIEW-rc5 #1：filename 可能含 `:` 或 `-`（AIX/Linux 都允许），原算法
// strings.IndexAny(":-") 找第一个匹配会错位。本算法扫描整行找第一个
// 形如 `SEP<digits><:>` 或 `SEP<digits>$` 的子串——lineno 必须是纯数字这一约束
// 完全消除了 filename 含 `:`/`-` 时的歧义。
//
// 返回：
//   - file: filename（不含末尾 SEP）
//   - sep: SEP 字符（`:` 或 `-`），调用方用来区分匹配行/上下文行
//   - lineno: 行号
//   - content: SEP lineno 之后的全部内容
//   - ok: 是否成功解析（false 时整行丢弃）
func parseSearchLine(line string) (file string, sep byte, lineno int, content string, ok bool) {
	n := len(line)
	if n == 0 {
		return "", 0, 0, "", false
	}
	// 扫所有 i：line[i] 是 `:` 或 `-`，尝试作为 SEP
	for i := 0; i < n; i++ {
		c := line[i]
		if c != ':' && c != '-' {
			continue
		}
		// SEP 之后必须是 digits
		j := i + 1
		if j >= n || line[j] < '0' || line[j] > '9' {
			continue
		}
		for j < n && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		// digits 之后必须是 `:`（content 前缀分隔符）或行尾
		if j < n && line[j] != ':' {
			continue
		}
		// 找到合法 lineno
		ln, err := strconv.Atoi(line[i+1 : j])
		if err != nil {
			continue
		}
		file = line[:i]
		sep = c
		lineno = ln
		if j < n {
			content = line[j+1:]
		}
		return file, sep, lineno, content, true
	}
	return "", 0, 0, "", false
}

// ParseSearchHitLine 是 parseSearchLine 的导出版本，供 handler 层复用同一套
// 解析规则（避免规则漂移）。REVIEW-rc5 #1 修复后单点维护。
func ParseSearchHitLine(line string) (file string, sep byte, lineno int, content string, ok bool) {
	return parseSearchLine(line)
}
