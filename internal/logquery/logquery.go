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
	Negate bool // true 表示这是 !term 形式，最终用 grep -v
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
			if state == stateOp && negateNext {
				// 上一个 token 是操作符 + ! + term，term 是 term；这其实是合法：
				//   "A && !B" 拆出来是 ['A','&&','!','B']，
				//   处理 'A'（state→term）→ '&&'（state→op）→ '!'（state=op, negate=true）
				//   → 'B'（到这里 state=op,negate=true，仍 OK，走 default 走 append）
				//
				// 但要注意：上面 default 分支必须先把 term append 进去，再清 negate。
				// 所以这里不需要特殊分支，让 default 自然处理。
			}
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
			// 简单去掉 shell 注入风险字符；只保留 * ? [ ] 和普通字符。
			cleaned := make([]rune, 0, len(p))
			for _, r := range p {
				if r == '*' || r == '?' || r == '[' || r == ']' || r == '.' || r == '-' || r == '_' ||
					(r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
					cleaned = append(cleaned, r)
				}
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
			// 把 glob * 转成正则 .*，其他字符保持字面
			re := strings.ReplaceAll(p, "*", ".*")
			re = strings.ReplaceAll(re, "?", ".")
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
	// 关键：不管有没有正 term，第一步都要用 `grep -HnE` 直接读文件列表，让 grep
	// 给每个文件加 `filename:lineno:` 前缀。即使只有一个文件，`-H` 也能保证
	// 输出 filename:lineno:content 而不是只有 lineno:content，
	// 否则 parseSearchOutput 会把 lineno 当成 filename 解析失败。
	// 如果用 `cat -- file1 file2 file3` 拼成单流再喂给下游 grep，
	// grep 看到单流就不再加前缀，前端解析就会错位。
	if len(groups[0].pos) > 0 {
		pat0, err := quoteForGrep(groups[0].pos[0])
		if err != nil {
			return "", err
		}
		pipes = append(pipes, fmt.Sprintf("grep -HnE %s -- %s", pat0, fileList))
		for _, term := range groups[0].pos[1:] {
			pat, err := quoteForGrep(term)
			if err != nil {
				return "", err
			}
			pipes = append(pipes, fmt.Sprintf("grep -E %s", pat))
		}
	} else {
		// 第一段只有 neg（例: "!DEBUG"）。直接 `grep -HnE "^." -- file1 file2 file3`
		// 让 grep 给每行加 `file:lineno:` 前缀，再串联 grep -vE。
		pipes = append(pipes, fmt.Sprintf("grep -HnE %q -- %s", "^.", fileList))
	}
	for _, p := range groups[0].neg {
		pat, err := quoteForGrep(p)
		if err != nil {
			return "", err
		}
		pipes = append(pipes, fmt.Sprintf("grep -vE %s", pat))
	}
	// 后续 OR 段：每段从 files 独立 grep，再用 sort -u 合并去重
	//
	// v0.4 修复：原版本对 g.pos == [] 的分支直接 continue，纯 neg OR 段（"A || !B"）
	// 会被整段丢掉。修正：纯 neg 段也要生成独立分支（先 `grep -HnE "^." -- files` 拿全部行，
	// 再 `grep -vE B`），逻辑跟"纯 NOT"那个第一段对称。
	if len(groups) > 1 {
		var branches []string
		for _, g := range groups[1:] {
			branch, err := buildOrBranch(g, fileList, quoteForGrep)
			if err != nil {
				return "", err
			}
			if branch != "" {
				branches = append(branches, branch)
			}
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

// orGroup 是 SearchCommand 把 kw 切分成"OR 段"时用的内部容器：
//   - pos: 当前段里的"正"term（被 grep -E / grep -HnE 命中的）
//   - neg: 当前段里的"负"term（被 grep -vE 排除的）
//
// 提升到包级类型而不是 SearchCommand 内的局部类型，是为了让 buildOrBranch 能直接复用，
// 避免重复声明 struct shape。
type orGroup struct {
	pos []string
	neg []string
}

// buildOrBranch 构造一个 OR 分支的命令片段（不含前后括号）。
//
// g 是经 ParseQuery 拆分后、去掉"或"操作符得到的 pos/neg 列表：
//   - pos 非空：从 grep -HnE pos[0] 开始，正 term 串联（AND），再 neg 串联（NOT）；
//   - pos 为空：纯 neg 分支，先 `grep -HnE "^." -- files` 拿全部行（保留 filename:lineno: 前缀），
//     再 grep -vE neg。
//
// 返回空字符串说明该 group 完全是空的（不应该发生 —— ParseQuery 已经挡了，
// 这里再做防御性检查）。
func buildOrBranch(g orGroup, fileList string, quoteForGrep func(string) (string, error)) (string, error) {
	var branch string
	if len(g.pos) > 0 {
		pat0, err := quoteForGrep(g.pos[0])
		if err != nil {
			return "", err
		}
		branch = fmt.Sprintf("grep -HnE %s -- %s", pat0, fileList)
		for _, term := range g.pos[1:] {
			pat, err := quoteForGrep(term)
			if err != nil {
				return "", err
			}
			branch += " | grep -E " + pat
		}
	} else if len(g.neg) > 0 {
		// 纯 neg 分支：用 `grep -HnE "^." -- files` 给每行打前缀，
		// 前端 parseSearchOutput 仍能正确解析 file:lineno:content。
		branch = fmt.Sprintf(`grep -HnE %q -- %s`, "^.", fileList)
	}
	for _, p := range g.neg {
		pat, err := quoteForGrep(p)
		if err != nil {
			return "", err
		}
		branch += " | grep -vE " + pat
	}
	return branch, nil
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
	cleanFile := strings.ReplaceAll(file, "'", "")
	cmd := fmt.Sprintf(`sh -c 'cd %q && tail -n %d -F %q 2>/dev/null'`,
		dir, lines, cleanFile)
	return cmd, nil
}
