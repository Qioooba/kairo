package httpserver

// ---------- /api/http/request ----------
//
// 内网 HTTP 测试页：发一个 HTTP 请求，返回 status / headers / body / 耗时。
// 用标准库 net/http 实现，零额外依赖。
//
// 安全约束：
//   - Method 仅允许标准 GET/POST/PUT/DELETE/HEAD/PATCH/OPTIONS
//   - URL 必须以 http:// 或 https:// 开头
//   - Body 4MB 上限（与 formatter 一致）
//   - 总超时：客户端 timeout 字段控制，0 表示 30s 默认
//   - FollowRedirect: 默认 true，可手动 false

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	urlpkg "net/url"
	"strings"
	"time"
)

type httpRequestReq struct {
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers"`
	Body           string            `json:"body"`
	TimeoutMs      int               `json:"timeout_ms"`      // 0 = 30s
	FollowRedirect bool              `json:"follow_redirect"` // 默认 true
	InsecureTLS    bool              `json:"insecure_tls"`    // 跳过证书校验（内网自签用）
}

type httpRequestResp struct {
	Ok         bool              `json:"ok"`
	Status     int               `json:"status"`
	StatusText string            `json:"status_text"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	BodyBytes  int               `json:"body_bytes"`
	ElapsedMs  int64             `json:"elapsed_ms"`
	FinalURL   string            `json:"final_url"`
	Truncated  bool              `json:"truncated"`
	Error      string            `json:"error,omitempty"`
}

const (
	httpDefaultTimeoutMs = 30_000
	httpMaxBodyCapture   = 1 * 1024 * 1024 // 1MB
)

var allowedHTTPMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "DELETE": {},
	"HEAD": {}, "PATCH": {}, "OPTIONS": {},
}

func (s *Server) handleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req httpRequestReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	// doHTTPRequest 永不返回 error，所有错误都封装在 resp.Error 字段里
	resp, _ := doHTTPRequest(req)
	// BE-017：补审计日志（/api/http/request 之前是审计盲区）。
	s.audit.Write("http.request", "method", req.Method, "url", req.URL, "status", resp.Status)
	writeJSON(w, 200, resp)
}

func doHTTPRequest(req httpRequestReq) (httpRequestResp, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	if _, ok := allowedHTTPMethods[method]; !ok {
		return httpRequestResp{Ok: false, Error: "不支持的 method: " + method}, nil
	}
	url := strings.TrimSpace(req.URL)
	if url == "" {
		return httpRequestResp{Ok: false, Error: "URL 不能为空"}, nil
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return httpRequestResp{Ok: false, Error: "URL 必须以 http:// 或 https:// 开头"}, nil
	}
	if err := validateOutboundHTTPURL(url); err != nil {
		return httpRequestResp{Ok: false, Error: err.Error()}, nil
	}

	timeoutMs := req.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = httpDefaultTimeoutMs
	}
	if timeoutMs > 5*60_000 {
		timeoutMs = 5 * 60_000 // 5 分钟硬上限
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	transport := &http.Transport{
		TLSClientConfig:       nil,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		DisableCompression:    false,
		ResponseHeaderTimeout: 0,
		Proxy:                 nil,
		DialContext:           safeHTTPDialContext,
	}
	if req.InsecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(timeoutMs) * time.Millisecond,
	}
	if !req.FollowRedirect {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	} else {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if err := validateOutboundHTTPURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		}
	}

	var bodyReader io.Reader
	if req.Body != "" {
		bodyReader = bytes.NewReader([]byte(req.Body))
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return httpRequestResp{Ok: false, Error: "构造请求失败: " + err.Error()}, nil
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	if httpReq.Header.Get("User-Agent") == "" {
		httpReq.Header.Set("User-Agent", "ops-toolbox-http-test/0.7")
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	elapsed := time.Since(start)
	if err != nil {
		return httpRequestResp{
			Ok:        false,
			ElapsedMs: elapsed.Milliseconds(),
			Error:     fmt.Sprintf("请求失败: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, httpMaxBodyCapture+1)
	bodyBytes, _ := io.ReadAll(limited)
	truncated := false
	if len(bodyBytes) > httpMaxBodyCapture {
		bodyBytes = bodyBytes[:httpMaxBodyCapture]
		truncated = true
	}
	headers := make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		if len(v) > 0 {
			// 多值 header 用逗号拼接（RFC 7230 §3.2.2 通用规则；
			// Set-Cookie 在 RFC 6265 中不可合并，仅保留第一个值以避免误合并破坏 cookie）
			if k == "Set-Cookie" || k == "set-cookie" {
				headers[k] = v[0]
			} else {
				headers[k] = strings.Join(v, ", ")
			}
		}
	}

	return httpRequestResp{
		Ok:         true,
		Status:     resp.StatusCode,
		StatusText: http.StatusText(resp.StatusCode),
		Headers:    headers,
		Body:       string(bodyBytes),
		BodyBytes:  len(bodyBytes),
		ElapsedMs:  elapsed.Milliseconds(),
		FinalURL:   resp.Request.URL.String(),
		Truncated:  truncated,
	}, nil
}

func validateOutboundHTTPURL(raw string) error {
	u, err := urlpkg.Parse(raw)
	if err != nil {
		return fmt.Errorf("URL 解析失败: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("URL 必须以 http:// 或 https:// 开头")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("URL host 不能为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rejectPrivateHost(ctx, host); err != nil {
		return err
	}
	return nil
}

func safeHTTPDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if err := rejectPrivateHost(ctx, host); err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("DNS 解析结果为空")
	}
	// 防 DNS rebinding 的双重校验设计：
	// validateOutboundHTTPURL / rejectPrivateHost 已经做了一次 DNS 解析 + IP 校验，
	// 但在 follow_redirect 路径下，重定向 URL 的校验（CheckRedirect）与实际拨号
	// （safeHTTPDialContext）之间存在轻微 TOCTOU：两次独立的 DNS 解析之间，
	// 攻击者理论上可让权威 DNS 返回不同结果（先返公网 IP 通过校验，再返内网 IP 触发 SSRF）。
	// 因此这里对 *本次* 拨号即将使用的解析结果再次显式调用 isBlockedHTTPIP 校验，
	// 确保真正用于建连的 IP 一定是公网 IP。
	for _, addr := range ips {
		if isBlockedHTTPIP(addr.IP) {
			return nil, fmt.Errorf("拒绝访问内网或本机地址: %s (DNS rebinding 双重校验)", addr.IP)
		}
	}
	dialer := &net.Dialer{}
	return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

func rejectPrivateHost(ctx context.Context, host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedHTTPIP(ip) {
			return fmt.Errorf("拒绝访问内网或本机地址: %s", host)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("DNS 解析失败: %w", err)
	}
	if len(ips) == 0 {
		return errors.New("DNS 解析结果为空")
	}
	for _, addr := range ips {
		if isBlockedHTTPIP(addr.IP) {
			return fmt.Errorf("拒绝访问内网或本机地址: %s", host)
		}
	}
	return nil
}

func isBlockedHTTPIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	return false
}
