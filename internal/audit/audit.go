// Package audit 写本地操作审计日志。
//
// 重要：不记录密码、SSH 私钥、日志正文中的敏感信息。
// 审计文件按日滚动（audit-YYYY-MM-DD.log），方便归档。
package audit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger 简单的线程安全审计日志器
type Logger struct {
	dir   string
	mu    sync.Mutex
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
