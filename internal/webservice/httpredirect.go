package webservice

// WSDL 根文档与外部 XSD 共用的重定向凭据隔离策略（OTH-02）。
//
// 审计指出的两条缺口：
//  1. `makeSafeHTTPClient(authHeaders)` 根本没使用 authHeaders，只剥离
//     Authorization / Proxy-Authorization / Cookie 三个固定头，用户配置的
//     X-API-Key 等自定义认证头会跟到跨源目标；
//  2. 判定基准是 `via[len(via)-1]`（上一跳）。Go 的 net/http 在每次重定向前
//     都会从**初始请求**重新复制 Header，所以 A→B→B 这种"同主机不同端口"的
//     第二跳会被当成同源，把 A 的凭据又带上去。
//
// 这里只保留一份实现，根 WSDL 下载器与 schema resolver 共用，避免两处策略漂移。
// 关键设计：
//   - 可信基准是"首次携带凭据的那个 origin"（trustedOrigin），不是上一跳；
//   - 每一跳都用 isSameOrigin（协议 + 标准化主机 + 有效端口）相对可信基准重新判定；
//   - 跨源时剥离**全部用户传入头名**加固定敏感头；程序自己设置的
//     User-Agent / Accept 等非敏感头不在用户头名集合里，因此不会被误删；
//   - URL/DNS 校验与最多 10 次跳转的限制保持不变。

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxCredentialSafeRedirects 是允许的最大重定向跳数。
const maxCredentialSafeRedirects = 10

// fixedSensitiveHeaders 是不依赖用户配置、跨源时必须剥离的固定敏感头。
var fixedSensitiveHeaders = []string{
	"Authorization",
	"Proxy-Authorization",
	"Cookie",
	"Www-Authenticate",
}

// NewCredentialSafeHTTPClient 构造 WSDL 根文档与外部 XSD 拉取共用的 HTTP 客户端：
// 传输层参数（TLS、超时、拨号期 SSRF 二次校验）与重定向凭据隔离策略都只有这一份实现。
//
// trustedOrigin 是首次携带凭据的源。对根 WSDL 而言就是用户显式填写的那个 URL 的源；
// 对被引用的外部 XSD 而言是与 BaseURI 同源的源（跨源直接 import 本来就不带凭据）。
// userHeaders 是用户配置的头（跨源时全部按名字剥离）。
func NewCredentialSafeHTTPClient(trustedOrigin string, userHeaders map[string]string) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			ResponseHeaderTimeout: 25 * time.Second,
			DialContext:           SafeDialContext,
		},
		CheckRedirect: newCredentialRedirectGuard(trustedOrigin, userHeaders),
	}
}

// newCredentialRedirectGuard 返回共用的 CheckRedirect 策略。
func newCredentialRedirectGuard(trustedOrigin string, userHeaders map[string]string) func(*http.Request, []*http.Request) error {
	credentialHeaders := make([]string, 0, len(userHeaders))
	for name := range userHeaders {
		if strings.TrimSpace(name) == "" {
			continue
		}
		credentialHeaders = append(credentialHeaders, name)
	}
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxCredentialSafeRedirects {
			return errors.New("重定向次数过多（最多允许 10 次）")
		}
		if err := ValidateEndpointURL(req.URL.String()); err != nil {
			return fmt.Errorf("重定向目标 URL 不受信任: %w", err)
		}
		if shouldStripCredentialsOnRedirect(trustedOrigin, req.URL.String()) {
			for _, name := range credentialHeaders {
				req.Header.Del(name)
			}
			for _, name := range fixedSensitiveHeaders {
				req.Header.Del(name)
			}
		}
		return nil
	}
}

// shouldStripCredentialsOnRedirect 判断某一跳是否必须剥离凭据。
//
// 基准是"首次携带凭据的源"而不是上一跳：Go 的 net/http 每跳都会从初始请求
// 重新复制 Header，只有相对可信源重新判定才能阻止 A→B→B、A→子域→子域
// 这类"跨源之后继续在别处跳转"重新带上 A 的凭据。
// 回到最初的可信源时允许继续携带凭据——那是凭据本来就该去的源。
func shouldStripCredentialsOnRedirect(trustedOrigin, nextURL string) bool {
	if strings.TrimSpace(trustedOrigin) == "" {
		// 没有可信源（例如本地文件模式）：任何出站跳转都不携带凭据。
		return true
	}
	return !isSameOrigin(nextURL, trustedOrigin)
}

// OriginOf 归一化出 URL 的源（scheme://host:port），用作可信基准；无法解析时返回空串。
func OriginOf(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}
