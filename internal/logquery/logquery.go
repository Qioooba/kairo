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

	// P0-3 修复：OR 搜索时每段都要独立读文件列表，不能把第一段结果通过管道传给子 shell。
	// 原 bug：`grep A -- files | (grep B -- files; grep C -- files)` 里，
	// 子 shell 的 stdin 指向管道，但 grep B/C 不读 stdin，各自重新读 files，
	// 导致 grep A 的结果实际上被丢弃。
	//
	// 正确结构：所有段（包括第一段 AND 链）全部放进统一子 shell，
	// 每个分支独立用 grep 读 files，再 sort -u 合并。
	// 例 A || B → `sh -c 'grep A -- files; grep B -- files' | sort -u | head -n N`
	// 多段时：
	//   ( grep A -- files | grep B -- files; grep C -- files ) | sort -u | head -n N
	//
	// buildBranch 是同一个 group 内的 AND 链构建（grep 串联 + neg 过滤），
	// buildOrBranch 把 group 变成一个完整分支字符串（含括号外的 grep 读文件）。
	// 我们把所有 groups（包括第一个）都作为分支，统一子 shell 合并。
	var allBranches []string
	for i, g := range groups {
		var branch string
		if len(g.pos) > 0 {
			// AND 链：第一个 grep 读文件并加 filename:lineno: 前缀，后续 grep 串联过滤
			pat0, err := quoteForGrep(g.pos[0])
			if err != nil {
				return "", err
			}
			// 关键：即使只有一个文件，grep -H 也要加，让输出有 filename: 前缀
			// parseSearchOutput 按 filename: 分割，没有前缀会解析失败。
			// 不能用 cat | grep：grep 看到 cat 的单流会去掉文件名，导致解析错位。
			branch = fmt.Sprintf("grep -HnE %s -- %s", pat0, fileList)
			for _, term := range g.pos[1:] {
				pat, err := quoteForGrep(term)
				if err != nil {
					return "", err
				}
				branch += " | grep -E " + pat
			}
		} else {
			// 纯 neg（例 "!DEBUG"）：用 grep -HnE "^." -- files 给所有行加前缀，再排除
			branch = fmt.Sprintf("grep -HnE %q -- %s", "^.", fileList)
		}
		// neg 过滤（每组都适用，包括纯 neg 组）
		for _, p := range g.neg {
			pat, err := quoteForGrep(p)
			if err != nil {
				return "", err
			}
			branch += " | grep -vE " + pat
		}
		// 第一段（i==0）已经在 branch 里，不需要额外处理
		_ = i
		allBranches = append(allBranches, branch)
	}

	var cmdBody string
	if len(allBranches) == 1 {
		// 单一分支：不需要子 shell，直接 pipe 到 sort + head
		cmdBody = allBranches[0] + " | LC_ALL=C sort -u | head -n " + strconv.Itoa(max)
	} else {
		// 多分支 OR：所有分支放进统一子 shell（每个分支独立读 files），再 sort -u + head
		// 结构：( branch1; branch2; ... ) | LC_ALL=C sort -u | head -n N
		cmdBody = "(" + strings.Join(allBranches, "; ") + ") | LC_ALL=C sort -u | head -n " + strconv.Itoa(max)
	}

	// 超时由 Go 客户端 ctx + 内部 timer 控制，这里不再依赖 Linux `timeout` 命令，
	// 老 Linux / Alpine / 精简镜像也能跑。
	cmd := fmt.Sprintf(`sh -c 'cd %q && %s'`, dir, cmdBody)
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
		a = strings.ReplaceAll(a, "'", "")
		out = append(out, "'"+a+"'")
	}
	return out
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

	cleanFile := strings.ReplaceAll(file, "'", "")
	cleanHitsStr := strings.ReplaceAll(hitsStr, "'", "")
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

		// 找第一个分隔符：: 表示匹配行，- 表示上下文行
		// 格式：filename X lineno : content，其中 X 是 : 或 -
		idx1 := strings.IndexAny(line, ":-")
		if idx1 < 0 {
			continue
		}
		sep := line[idx1]
		rest := line[idx1+1:]

		// 第二个分隔符一定是 :（lineno 和 content 之间）
		idx2 := strings.Index(rest, ":")
		if idx2 < 0 {
			continue
		}

		file := line[:idx1]
		if useWhitelist {
			if _, ok := byName[file]; !ok {
				continue
			}
		}

		lineNoStr := rest[:idx2]
		content := rest[idx2+1:]

		ln, err := strconv.Atoi(strings.TrimSpace(lineNoStr))
		if err != nil {
			continue
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

	// 按行号升序、文件名排序（保持输出有序）
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].File != hits[j].File {
			return hits[i].File < hits[j].File
		}
		return hits[i].LineNo < hits[j].LineNo
	})

	return hits
}
