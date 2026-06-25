// Package audit 写本地操作审计日志。
//
// 重要：不记录密码、SSH 私钥、日志正文中的敏感信息。
// 审计文件按日滚动（audit-YYYY-MM-DD.log），方便归档。
//
// 格式（项 15 修复）：
//   - v2（默认）：每行一个 JSON 对象，字段名固定（ts/op/system/.../result）。
//     便于程序解析 / 二次处理。读取时一行 json.Unmarshal 即可。
//   - v1（兼容）："ts=... op=... k=v k=v ..." 空格分隔的 K=V 格式。
//     旧 audit.log 升级后第一次跑会按 v1 解析（看每行首字符是不是 '{'）。
package audit

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Logger 简单的线程安全审计日志器
type Logger struct {
	dir  string
	mu   sync.RWMutex
	out  io.WriteCloser
	file *os.File
	cur  string // 当前日期字符串
}

// New 在 dir 下创建/打开 audit.log（同一天复用）
func New(dir, name string) (*Logger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建审计目录失败: %w", err)
	}
	l := &Logger{dir: dir}
	if err := l.rotate(name); err != nil {
		return nil, err
	}
	return l, nil
}

// Close 关闭当前文件
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.out != nil {
		return l.out.Close()
	}
	return nil
}

// Write 写一条审计记录。
// fields 是 key/value 交替的扁平结构（必须偶数长度）。
//
// 输出格式（项 15）：每行一个 JSON 对象（JSONL 风格），
//
//	{"ts":"2026-06-23T12:00:00.000+08:00","op":"logs.search","system":"xxx",...}
//
// 比老 K=V 格式（项 15 之前的 v1）优点：
//   - 字段名 / 值含空格 / 等号 / 引号都不需要转义（标准 json 处理）；
//   - 解析端一行 json.Unmarshal，零正则；
//   - 二次处理（grep 改成 jq / awk 拆字段）更稳。
func (l *Logger) Write(op string, fields ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// 检查是否需要按日滚动
	today := time.Now().Format("2006-01-02")
	if today != l.cur {
		if err := l.rotateLocked("audit.log"); err != nil {
			return
		}
	}
	if l.out == nil {
		return
	}
	if len(fields)%2 != 0 {
		fields = append(fields, "<missing>")
	}
	// 拼成 map[string]string（值走 fmt.Sprint，复合类型也能序列化）
	rec := make(map[string]string, 2+len(fields)/2)
	rec["ts"] = time.Now().Format(time.RFC3339Nano)
	rec["op"] = op
	for i := 0; i < len(fields); i += 2 {
		k, ok := fields[i].(string)
		if !ok {
			k = fmt.Sprint(fields[i])
		}
		rec[k] = fmt.Sprint(fields[i+1])
	}
	b, err := json.Marshal(rec)
	if err != nil {
		// 序列化失败兜底：写一行"audit_format_error"标记
		_, _ = fmt.Fprintln(l.out, `{"op":"audit_format_error","err":"`+err.Error()+`"}`)
		return
	}
	_, _ = l.out.Write(b)
	_, _ = l.out.Write([]byte("\n"))
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

func (l *Logger) rotate(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rotateLocked(name)
}

func (l *Logger) rotateLocked(name string) error {
	if l.out != nil {
		_ = l.out.Close()
		l.out = nil
		l.file = nil
	}
	path := filepath.Join(l.dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("打开审计文件失败: %w", err)
	}
	l.file = f
	l.out = f
	l.cur = time.Now().Format("2006-01-02")
	return nil
}

// Record 单条审计记录（按 tab 切割回 key/value 对）
type Record struct {
	Time time.Time
	Op   string
	KV   map[string]string
	Raw  string
}

// Filter 过滤条件（可同时生效）
type Filter struct {
	Op     string // 按 op= 过滤（精确匹配），空 = 不过滤
	System string // 按 system= 过滤（包含），空 = 不过滤
	Server string // 按 server= 过滤（包含），空 = 不过滤
	Result string // "ok" / "fail" / 空（不过滤）
}

// Recent 读取最近的 N 条记录（按文件中倒序）。
//
// 设计要点：
//   - 默认日志是 append-only，文件不大，所以倒序读尾部足够；
//   - limit 上限 5000，避免 OOM；
//   - 过滤在解析后做，先 filter 再 limit 返回（保证 limit 命中的是过滤后的最新 N 条）；
//   - 文件不存在时返回空切片和 nil，不报错。
func (l *Logger) Recent(limit int, f Filter) ([]Record, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 5000 {
		limit = 5000
	}
	l.mu.RLock()
	path := filepath.Join(l.dir, "audit.log")
	l.mu.RUnlock()

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("打开审计文件失败: %w", err)
	}
	defer file.Close()

	// 先把全部行读到内存。日志增长有限（每条 ~200B，10万条约 20MB）。
	// 后续可优化成 tail -n。
	var allLines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if line != "" {
			allLines = append(allLines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读审计文件失败: %w", err)
	}

	// 倒序遍历，应用过滤
	out := make([]Record, 0, limit)
	for i := len(allLines) - 1; i >= 0 && len(out) < limit; i-- {
		rec := parseLine(allLines[i])
		if rec == nil {
			continue
		}
		if f.Op != "" && rec.Op != f.Op {
			continue
		}
		if f.Result != "" && rec.KV["result"] != f.Result {
			continue
		}
		if f.System != "" && !strings.Contains(rec.KV["system"], f.System) {
			continue
		}
		if f.Server != "" && !strings.Contains(rec.KV["server"], f.Server) {
			continue
		}
		out = append(out, *rec)
	}
	return out, nil
}

// parseLine 把一行审计日志解成 Record。
//
// 自动识别格式（项 15）：
//   - 行首是 '{' → 当 JSONL 解析（v2）；
//   - 否则按老 K=V 格式解析（v1，向后兼容）。
//
// 这样老 audit.log 文件升级后第一次跑 Recent() 也能正常返回。
func parseLine(line string) *Record {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}
	if trimmed[0] == '{' {
		return parseJSONLine(trimmed)
	}
	return parseKVLine(trimmed)
}

// parseJSONLine 解析 v2 JSONL 行。
func parseJSONLine(line string) *Record {
	rec := &Record{KV: make(map[string]string)}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return nil
	}
	rec.Raw = line
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			s = fmt.Sprint(v)
		}
		switch k {
		case "ts":
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				rec.Time = t
			} else if t, err := time.Parse("2006-01-02T15:04:05.000Z07:00", s); err == nil {
				rec.Time = t
			}
		case "op":
			rec.Op = s
		default:
			rec.KV[k] = s
		}
	}
	if rec.Op == "" {
		return nil
	}
	return rec
}

// parseKVLine 解析 v1 老格式 "ts=... op=... k=v k=v"
//
// 注意：ts 形如 "ts=2026-06-20 10:30:00.123 op=..."，
// 因为 fields 按空格切，"10:30:00.123" 和 "op=..." 会被切成两个 token。
// 这里特判：遇到 ts= 开头的 key，把后续 token 里第一个形如 "HH:MM:SS.mmm" 的吃进来。
func parseKVLine(line string) *Record {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return nil
	}
	rec := &Record{KV: make(map[string]string)}
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			continue
		}
		k := p[:eq]
		v := p[eq+1:]
		switch k {
		case "ts":
			// 把下一段（时间部分）拼上来
			ts := v
			if i+1 < len(parts) {
				// 简单判断下一段是否像 "HH:MM:SS[.mmm]"
				next := parts[i+1]
				if len(next) >= 8 && next[2] == ':' && next[5] == ':' {
					ts = v + " " + next
					i++
				}
			}
			if t, err := time.Parse("2006-01-02 15:04:05.000", ts); err == nil {
				rec.Time = t
			} else if t, err := time.Parse("2006-01-02 15:04:05", ts); err == nil {
				rec.Time = t
			}
		case "op":
			rec.Op = v
		default:
			rec.KV[k] = v
		}
	}
	rec.Raw = line
	if rec.Op == "" {
		return nil
	}
	return rec
}

// CSV 导出辅助（P3-3）
//
// 把一批 Record 序列化成 CSV。设计要点：
//   - 列固定，前 5 列是 ts/op/system/server/result（业务必看）；
//     其它 KV 按出现顺序拼到后面，列名就是 KV key；
//   - 第一行 header 用 \r\n 结尾，符合 RFC 4180，Excel/Numbers 都能直接打开；
//   - 数据行按 ts 升序输出（Recent 是倒序，导出时翻一下，读 CSV 更自然）；
//   - ts 用 RFC3339Nano 字符串（带时区），跨时区协作时不会被误会；
//   - 单元格里的引号 / 换行 / 逗号都让 encoding/csv 自动转义，不用我们管。
//
// Err 和 stage 字段是有时被审计加进来的额外 KV 字段；这里同样会被自动追加列。

// defaultCSVColumns 永远出现在 CSV 前几列，业务最关心的字段。
var defaultCSVColumns = []string{"ts", "op", "system", "server", "result"}

// WriteCSV 把 records 写到 w（一般是 *http.ResponseWriter / *os.File）。
//   - 列顺序：defaultCSVColumns 在前，其它 KV key 按字母序追加在后面；
//   - 空记录返空 CSV（只有 header）；
//   - 不会修改传入的 records。
func WriteCSV(w io.Writer, records []Record) error {
	cw := csv.NewWriter(w)
	// RFC 4180 推荐 CRLF，Excel / Numbers / WPS 都更兼容
	cw.UseCRLF = true
	// 收集所有 KV key（去重 + 排序）
	keySet := map[string]bool{}
	for _, r := range records {
		for k := range r.KV {
			keySet[k] = true
		}
	}
	extraKeys := make([]string, 0, len(keySet))
	for k := range keySet {
		// 跳过已经在 defaultCSVColumns 里的，避免重复列
		already := false
		for _, c := range defaultCSVColumns {
			if c == k {
				already = true
				break
			}
		}
		if already {
			continue
		}
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)

	header := append([]string{}, defaultCSVColumns...)
	header = append(header, extraKeys...)
	if err := cw.Write(header); err != nil {
		return fmt.Errorf("写 csv header 失败: %w", err)
	}

	// 按 ts 升序：Recent() 是倒序，翻一下。
	sorted := make([]Record, len(records))
	copy(sorted, records)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Time.Before(sorted[j].Time)
	})

	for _, r := range sorted {
		row := make([]string, len(header))
		// 前 5 列固定
		row[0] = r.Time.Format(time.RFC3339Nano)
		row[1] = r.Op
		// 系统/服务器/结果可能为空（早期记录 / 自定义 op），空字符串 OK
		// 不过 defaultCSVColumns 没把 system/server/result 单独存到 KV，
		// 而 audit.Write 写时这些是 top-level KV key，所以要从 KV 取。
		row[2] = r.KV["system"]
		row[3] = r.KV["server"]
		row[4] = r.KV["result"]
		// extra KV
		for i, k := range extraKeys {
			row[len(defaultCSVColumns)+i] = r.KV[k]
		}
		if err := cw.Write(row); err != nil {
			return fmt.Errorf("写 csv 行失败: %w", err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("flush csv 失败: %w", err)
	}
	return nil
}

// CSVFilename 构造导出文件名（含时间戳）。
// 例：audit-2026-06-23-024500.csv
func CSVFilename(now time.Time) string {
	return "audit-" + now.Format("2006-01-02-150405") + ".csv"
}

// ---------- JSON 导出（B2）----------

// sensitiveKeys 包含的 key 在导出 JSON 时会被替换为 "<redacted>"，
// 避免前端 / 二方系统拿到含 password / 私钥 / 密钥指纹的审计日志。
//
// 大小写不敏感（比较时统一 ToLower）。
//
// 为什么单独处理：
//   - audit.Write 已经过滤密码字段（设计上不写密码），但万一未来某次
//     handler 误把 password 拼到 fields 里，要靠这里兜住；
//   - "host_key_sha256" / "private_key" 等是加密相关字段，通常也不应该
//     跨系统传；统一按"敏感"处理。
var sensitiveKeys = map[string]bool{
	"password":        true,
	"passwd":          true,
	"secret":          true,
	"private_key":     true,
	"host_key_sha256": true,
	"authorization":   true,
	"credential":      true,
	"auth_token":      true,
	"api_key":         true,
}

// JSONRedactedValue 脱敏值常量
const JSONRedactedValue = "<redacted>"

// isSensitiveKey 大小写不敏感检查
func isSensitiveKey(k string) bool {
	return sensitiveKeys[strings.ToLower(strings.TrimSpace(k))]
}

// IsSensitiveKey 是 isSensitiveKey 的导出版本（handler 层用）。
func IsSensitiveKey(k string) bool { return isSensitiveKey(k) }

// jsonRecord 是导出 JSON 的行结构（给前端 / 二次处理用）。
//
// 设计要点：
//   - 时间用 RFC3339Nano 字符串（带时区，跨时区协作不被误会）；
//   - KV 字段统一压平到顶层（方便 jq 一把梭，不嵌套）；
//   - 敏感字段被替换为 "<redacted>"（同时记到 redacted 字段让前端能看出）。
//
// 命名故意对齐 v2 JSONL 单行格式，方便用户拿到 JSON 后用 jq '.ts' 直接查。
type jsonRecord struct {
	Time     time.Time         `json:"ts"`
	Op       string            `json:"op"`
	KV       map[string]string `json:"-"`
	Raw      string            `json:"-"`
	Redacted []string          `json:"redacted,omitempty"`
}

// MarshalJSON 序列化 jsonRecord 时压平 KV 到顶层，敏感字段替换。
//
// 顺序：ts / op 在前（业务最关心），KV 在后按 key 字母序。
func (r jsonRecord) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, 3+len(r.KV))
	out["ts"] = r.Time.Format(time.RFC3339Nano)
	out["op"] = r.Op
	redacted := make([]string, 0)
	for k, v := range r.KV {
		if isSensitiveKey(k) {
			redacted = append(redacted, k)
			continue
		}
		out[k] = v
	}
	if len(redacted) > 0 {
		sort.Strings(redacted)
		out["redacted"] = redacted
	}
	return json.Marshal(out)
}

// WriteJSON 把 records 序列化成 JSON 数组写到 w（B2）。
//
// 格式：顶层是 JSON 数组，每条 record 是一个对象（ts / op / 字段）。
// 不是 JSONL（每行一个）—— 前端 fetch 后 JSON.parse 一次就能用，
// 跟 CSV 那种"流式追加"场景不一样。
//
// 不修改 records；敏感字段按 key 名替换为 "<redacted>"，
// 不在原 Record 上做破坏性改动。
func WriteJSON(w io.Writer, records []Record) error {
	out := make([]jsonRecord, 0, len(records))
	for _, r := range records {
		out = append(out, jsonRecord{Time: r.Time, Op: r.Op, KV: r.KV, Raw: r.Raw})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("编码 JSON 失败: %w", err)
	}
	return nil
}

// JSONFilename 构造导出文件名（含时间戳）。
// 例：audit-2026-06-23-024500.json
func JSONFilename(now time.Time) string {
	return "audit-" + now.Format("2006-01-02-150405") + ".json"
}
