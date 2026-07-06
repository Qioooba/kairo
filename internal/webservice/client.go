package webservice

// SOAP 请求发送。
//
// 与 /api/http/request 不同，本工具的核心场景就是调试内网 WebSphere / XFire /
// 老 Java WebService，endpoint 几乎都是内网地址，因此不做 SSRF 私网拦截
// （工具本身仅监听 127.0.0.1，由使用者本人发起请求）。
//
// 编码处理：
//   - 请求体按 encoding 字段（UTF-8 / GBK）编码后发送；
//   - Content-Type 的 charset 自动跟随 encoding；
//   - 响应体按 Content-Type 里的 charset 解码；取不到时尝试 UTF-8，
//     失败再回退 GBK（老 WebSphere SystemOut 时常是 GBK）。

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// SendRequest 描述一次 SOAP 发送请求。
type SendRequest struct {
	Endpoint    string            `json:"endpoint"`
	SOAPAction  string            `json:"soap_action"`
	ContentType string            `json:"content_type"` // 可空，自动按版本+编码生成
	Encoding    string            `json:"encoding"`     // "UTF-8" / "GBK"，默认 UTF-8
	SOAPVersion string            `json:"soap_version"` // "1.1" / "1.2"，默认 1.1
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
	TimeoutMs   int               `json:"timeout_ms"`
}

// SendResponse 描述一次 SOAP 发送结果。
type SendResponse struct {
	OK         bool              `json:"ok"`
	Status     int               `json:"status"`
	StatusText string            `json:"status_text"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	BodyBytes  int               `json:"body_bytes"`
	ElapsedMs  int64             `json:"elapsed_ms"`
	Truncated  bool              `json:"truncated"`
	Error      string            `json:"error,omitempty"`
}

const (
	defaultSOAPTimeoutMs = 30_000
	maxSOAPBodyCapture   = 2 * 1024 * 1024 // 2MB
	hardSOAPTimeoutCap   = 5 * 60_000      // 5 分钟硬上限
)

// sharedTransport 复用连接，避免每次 Send 都新建 Transport 导致 fd 暂用涨。
// 内网自签证书常见，默认跳过校验。
var sharedTransport = &http.Transport{
	TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	MaxIdleConns:          20,
	IdleConnTimeout:       30 * time.Second,
	DisableCompression:    false,
	ResponseHeaderTimeout: 0,
}

// Send 执行 SOAP 请求。永不返回 Go error，所有错误都封装在 resp.Error 里，
// 方便 handler 直接 JSON 序列化返回前端。
func Send(req SendRequest) SendResponse {
	endpoint := strings.TrimSpace(req.Endpoint)
	if endpoint == "" {
		return SendResponse{Error: "endpoint 不能为空"}
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		return SendResponse{Error: "endpoint 必须以 http:// 或 https:// 开头"}
	}

	encoding := strings.ToUpper(strings.TrimSpace(req.Encoding))
	if encoding == "" {
		encoding = "UTF-8"
	}
	if encoding != "UTF-8" && encoding != "GBK" {
		return SendResponse{Error: "encoding 仅支持 UTF-8 / GBK"}
	}

	soapVer := strings.TrimSpace(req.SOAPVersion)
	if soapVer != "1.2" {
		soapVer = "1.1"
	}

	timeoutMs := req.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = defaultSOAPTimeoutMs
	}
	if timeoutMs > hardSOAPTimeoutCap {
		timeoutMs = hardSOAPTimeoutCap
	}

	// 请求体编码
	bodyBytes, err := encodeBody(req.Body, encoding)
	if err != nil {
		return SendResponse{Error: "请求体编码失败: " + err.Error()}
	}

	// Content-Type
	contentType := strings.TrimSpace(req.ContentType)
	if contentType == "" {
		charset := encoding
		if soapVer == "1.2" {
			// SOAP 1.2 规范要求 action 参数放在 Content-Type 里
			// 格式: application/soap+xml; charset=UTF-8; action="xxx"
			sa := req.SOAPAction
			if sa == "" {
				sa = `""`
			}
			if !strings.HasPrefix(sa, `"`) {
				sa = `"` + sa + `"`
			}
			contentType = fmt.Sprintf("%s%s; action=%s", SOAPContentType12, charset, sa)
		} else {
			contentType = SOAPContentType11 + charset
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return SendResponse{Error: "构造请求失败: " + err.Error()}
	}
	httpReq.Header.Set("Content-Type", contentType)
	// SOAP 1.1 必带 SOAPAction HTTP header（即使空也要 ""）；
	// SOAP 1.2 把 action 放在 Content-Type 参数里，不需要此 header。
	if soapVer == "1.1" {
		sa := req.SOAPAction
		if sa == "" {
			sa = `""`
		}
		if !strings.HasPrefix(sa, `"`) {
			sa = `"` + sa + `"`
		}
		httpReq.Header.Set("SOAPAction", sa)
	}
	if httpReq.Header.Get("User-Agent") == "" {
		httpReq.Header.Set("User-Agent", "kairo-soap/0.1")
	}
	// 自定义 header（允许覆盖 Content-Type / SOAPAction）
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	transport := sharedTransport
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(timeoutMs) * time.Millisecond,
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	elapsed := time.Since(start)
	if err != nil {
		// 区分超时
		msg := err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			msg = fmt.Sprintf("请求超时（%dms）: %v", timeoutMs, err)
		}
		return SendResponse{
			OK:        false,
			ElapsedMs: elapsed.Milliseconds(),
			Error:     "请求失败: " + msg,
		}
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, maxSOAPBodyCapture+1)
	rawBody, _ := io.ReadAll(limited)
	truncated := len(rawBody) > maxSOAPBodyCapture
	if truncated {
		rawBody = rawBody[:maxSOAPBodyCapture]
	}

	headers := make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		if len(v) > 0 {
			if strings.EqualFold(k, "Set-Cookie") {
				headers[k] = v[0]
			} else {
				headers[k] = strings.Join(v, ", ")
			}
		}
	}

	// 响应解码：优先用 Content-Type 里的 charset
	bodyStr := decodeResponseBody(rawBody, resp.Header.Get("Content-Type"), encoding)

	return SendResponse{
		OK:         resp.StatusCode >= 200 && resp.StatusCode < 400,
		Status:     resp.StatusCode,
		StatusText: http.StatusText(resp.StatusCode),
		Headers:    headers,
		Body:       bodyStr,
		BodyBytes:  len(rawBody),
		ElapsedMs:  elapsed.Milliseconds(),
		Truncated:  truncated,
	}
}

// encodeBody 把请求体字符串按编码转成字节。
func encodeBody(body, encoding string) ([]byte, error) {
	if encoding == "GBK" {
		enc := simplifiedchinese.GBK.NewEncoder()
		return io.ReadAll(transform.NewReader(strings.NewReader(body), enc))
	}
	return []byte(body), nil
}

// decodeResponseBody 把响应字节解码成字符串。
// charset 优先取 Content-Type；取不到则用请求编码；再失败回退 GBK。
func decodeResponseBody(raw []byte, contentType, reqEncoding string) string {
	cs := parseCharset(contentType)
	if cs == "" {
		cs = reqEncoding
	}
	upper := strings.ToUpper(cs)
	switch upper {
	case "UTF-8", "UTF8", "":
		return string(raw)
	case "GBK", "GB2312", "GB18030":
		dec := simplifiedchinese.GBK.NewDecoder()
		out, err := io.ReadAll(transform.NewReader(bytes.NewReader(raw), dec))
		if err != nil {
			return string(raw) // 解码失败返回原始字节字符串
		}
		return string(out)
	default:
		return string(raw)
	}
}

// parseCharset 从 Content-Type 里抽取 charset=xxx。
func parseCharset(contentType string) string {
	parts := strings.Split(contentType, ";")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(strings.ToLower(p), "charset=") {
			return strings.Trim(p[len("charset="):], `"'`)
		}
	}
	return ""
}
