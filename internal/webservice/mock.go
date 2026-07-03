package webservice

// Mock WebService 服务端：注册动态路由，返回固定响应 XML。
//
// 设计：
//   - MockRegistry 持有一个 *Store，启动时加载所有 enabled 的 mock 配置；
//   - ServeHTTP 处理 /mock/ 前缀的请求：按 path 匹配 mock，应用延迟后返回固定 body；
//   - 同时把收到的请求记录到 Store（headers + body）。
//   - 配置保存后通过 Reload 重新生效（动态生效）。

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// mockRecordQueueSize 请求记录异步队列容量。满时丢弃记录，绝不阻塞 mock 响应。
const mockRecordQueueSize = 256

// MockRegistry 管理 mock 路由的动态注册。
type MockRegistry struct {
	store *Store

	mu     sync.RWMutex
	byPath map[string]MockConfig // path -> config（仅 enabled）

	// recordQueue 把请求记录的落盘异步化。同步 AppendMockRecord 内部走
	// saveJSONAtomic（temp + rename + fsync），是毫秒级磁盘 IO；
	// 高频调用 mock 时会阻塞 ServeHTTP，导致 mock 响应延迟抖动。
	// 改成单后台 goroutine 串行消费，热路径只做非阻塞投递。
	recordQueue chan MockRequestRecord

	// flushMu 保护 pendingWg 的 Add/Wait 互斥。
	// sync.WaitGroup 的 Add 和 Wait 不能并发调用，否则 panic。
	// 用 flushMu 保证 Close/Flush 期间不会有新的 Add 进来。
	flushMu   sync.Mutex
	pendingWg sync.WaitGroup

	closed bool
}

// NewMockRegistry 构造一个 mock 注册表。
func NewMockRegistry(store *Store) *MockRegistry {
	r := &MockRegistry{
		store:       store,
		byPath:      map[string]MockConfig{},
		recordQueue: make(chan MockRequestRecord, mockRecordQueueSize),
	}
	go r.drainRecordQueue()
	return r
}

// drainRecordQueue 后台串行消费 recordQueue，逐条落盘。
// 串行避免多 goroutine 同时 saveJSONAtomic 抢 store 锁和文件锁。
// 进程退出时未落盘的记录会丢失 —— mock 请求记录是诊断数据，非关键，
// 可接受（关键数据是 mock 配置本身，仍走同步保存）。
func (r *MockRegistry) drainRecordQueue() {
	for rec := range r.recordQueue {
		_ = r.store.AppendMockRecord(rec)
		r.pendingWg.Done()
	}
}

// FlushRecords 阻塞等待所有已投递的请求记录落盘。用于测试和优雅关闭。
// 调用期间新的 enqueueRecord 会被互斥等待，保证 WaitGroup 安全。
func (r *MockRegistry) FlushRecords() {
	r.flushMu.Lock()
	r.pendingWg.Wait()
	r.flushMu.Unlock()
}

// Close 优雅关闭：停止接收新记录，排空队列，等待所有落盘完成。
func (r *MockRegistry) Close() {
	r.flushMu.Lock()
	if r.closed {
		r.flushMu.Unlock()
		return
	}
	r.closed = true
	close(r.recordQueue)
	r.pendingWg.Wait()
	r.flushMu.Unlock()
}

// enqueueRecord 非阻塞投递请求记录。队列满时丢弃。已关闭则直接丢弃。
// 必须在 flushMu 内完成 select：否则 Close() 可能在 Add(1) 之后、send 之前
// close(channel)，导致 "send on closed channel" panic。
// select 带 default 不阻塞，持锁期间不会死锁。
func (r *MockRegistry) enqueueRecord(rec MockRequestRecord) {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	if r.closed {
		return
	}
	r.pendingWg.Add(1)
	select {
	case r.recordQueue <- rec:
	default:
		r.pendingWg.Done() // 队列满，丢弃，撤销 Add
	}
}

// Reload 重新加载所有 enabled mock 配置到路由表。配置保存后调用以动态生效。
func (r *MockRegistry) Reload() error {
	mocks, err := r.store.ListMocks()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byPath = map[string]MockConfig{}
	for _, m := range mocks {
		if !m.Enabled {
			continue
		}
		p := normalizeMockPath(m.Path)
		if p == "" {
			continue
		}
		r.byPath[p] = m
	}
	return nil
}

// normalizeMockPath 规范化 path：必须以 /mock/ 开头，去掉尾斜杠。
func normalizeMockPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.TrimRight(p, "/")
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/mock/") && p != "/mock" {
		if strings.HasPrefix(p, "/") {
			p = "/mock" + p
		} else {
			p = "/mock/" + p
		}
	}
	return p
}

// Lookup 按 path 查找启用的 mock 配置。
func (r *MockRegistry) Lookup(path string) (MockConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.byPath[normalizeMockPath(path)]
	return m, ok
}

// ServeHTTP 实现 http.Handler，处理 /mock/ 前缀请求。
// 路由注册在 httpserver.go 里把 /mock/ 前缀转发到此。
func (r *MockRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// 读取请求体（限 2MB）
	var bodyStr string
	if req.Body != nil {
		b, err := io.ReadAll(io.LimitReader(req.Body, 2*1024*1024))
		if err == nil {
			bodyStr = string(b)
		}
	}

	// 记录请求（headers 取关键几个，避免过大）
	headers := map[string]string{}
	for _, k := range []string{"Content-Type", "SOAPAction", "User-Agent", "Host", "Content-Length"} {
		if v := req.Header.Get(k); v != "" {
			headers[k] = v
		}
	}
	r.enqueueRecord(MockRequestRecord{
		Path:    req.URL.Path,
		Method:  req.Method,
		Headers: headers,
		Body:    bodyStr,
	})

	m, ok := r.Lookup(req.URL.Path)
	if !ok {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		// path 里可能含 & 等 XML 特殊字符，需转义避免破坏 XML 结构
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><error>未找到 mock: ` + escapeXMLText(req.URL.Path) + `</error>`))
		return
	}

	// 延迟
	if m.DelayMs > 0 {
		// 用 NewTimer 而非 time.After：After 在 select 走 Done 分支时
		// 不会立即回收 timer，得跑完整个时长才被 GC。慢 mock + 客户端
		// 提前取消时会堆积挂着 timer 的对象。NewTimer + defer Stop 干净。
		timer := time.NewTimer(time.Duration(m.DelayMs) * time.Millisecond)
		select {
		case <-timer.C:
		case <-req.Context().Done():
			timer.Stop()
			return
		}
	}

	status := m.StatusCode
	if status <= 0 {
		status = 200
	}
	ct := "text/xml; charset=utf-8"
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(m.Body))
}

// AllMockPaths 返回当前已注册的 mock path 列表（用于路由诊断/启动日志）。
func (r *MockRegistry) AllMockPaths() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byPath))
	for p := range r.byPath {
		out = append(out, p)
	}
	return out
}

// escapeXMLText 转义 XML 文本节点里的特殊字符（& < >）。
func escapeXMLText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
