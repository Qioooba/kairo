// Package audit 写本地操作审计日志。
//
// 重要：不记录密码、SSH 私钥、日志正文中的敏感信息。
// 审计文件按日滚动（audit-YYYY-MM-DD.log），方便归档。
package audit

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Logger 简单的线程安全审计日志器
type Logger struct {
	dir   string
	mu    sync.RWMutex
	out   io.WriteCloser
	file  *os.File
	cur   string // 当前日期字符串
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
func (l *Logger) Write(op string, fields ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// 检查是否需要按日滚动
	today := time.Now().Format("2006-01-02")
	if today != l.cur {
		_ = l.rotateLocked("audit.log")
	}
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	if len(fields)%2 != 0 {
		fields = append(fields, "<missing>")
	}
	parts := make([]string, 0, 1+2+len(fields)/2)
	parts = append(parts, fmt.Sprintf("ts=%s", ts), fmt.Sprintf("op=%s", op))
	for i := 0; i < len(fields); i += 2 {
		parts = append(parts, fmt.Sprintf("%s=%v", fields[i], fields[i+1]))
	}
	_, _ = fmt.Fprintln(l.out, joinComma(parts))
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
	today := time.Now().Format("2006-01-02")
	l.cur = today
	path := filepath.Join(l.dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("打开审计文件失败: %w", err)
	}
	l.file = f
	l.out = f
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

// parseLine 把 "ts=... op=... k=v k=v" 这种行解成 Record
//
// 注意：ts 形如 "ts=2026-06-20 10:30:00.123 op=..."，
// 因为 fields 按空格切，"10:30:00.123" 和 "op=..." 会被切成两个 token。
// 这里特判：遇到 ts= 开头的 key，把后续 token 里第一个形如 "HH:MM:SS.mmm" 的吃进来。
func parseLine(line string) *Record {
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
