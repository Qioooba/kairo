package webservice

// 出站 SSRF 防护（SOAP 发送 / WSDL URL 导入共用）。
//
// 与 /api/http/request 调试器不同，本工具核心场景就是调试内网 WebSphere / XFire /
// 老 Java WebService，endpoint 几乎都是内网地址，因此**放行私有网段**。
// 只拒绝三类绝对不可能作为 WebService 目标、却常被 SSRF 利用的地址：
//   - link-local（169.254.0.0/16，含云元数据 169.254.169.254；fe80::/10）
//   - link-local 组播（224.0.0.0/24、ff02::/16）
//   - unspecified（0.0.0.0、::）
//
// 另在拨号阶段对本次真正用于建连的 IP 再做一次校验，防 DNS rebinding
// （先返合法 IP 通过校验、再返危险 IP 触发 SSRF 的 TOCTOU）。
//
// loopback（127.0.0.1）被有意放行：本工具自带 /mock/ 服务跑在 127.0.0.1，
// 用户需要用 SOAP 请求调试自己本机的 mock；且服务本身只监听 127.0.0.1，
// 攻击者必须先触达本机 HTTP 才能触发 SSRF，回环自指不会带来额外权限提升。

import (
	"context"
	"errors"
	"fmt"
	"net"
	urlpkg "net/url"
	"time"
)

// ValidateEndpointURL 校验出站 SOAP/WSDL endpoint URL。
// 仅允许 http/https，且目标主机解析出的所有 IP 都必须通过 isDangerousEndpointIP。
func ValidateEndpointURL(raw string) error {
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
	if err := rejectDangerousEndpointHost(ctx, host); err != nil {
		return err
	}
	return nil
}

// SafeDialContext 是经 SSRF 校验的拨号函数，供 http.Transport.DialContext 使用。
// 对本次真正用于建连的 IP 再做一次校验（DNS rebinding 双重校验），
// 由于 Transport 对重定向的每一跳都走同一个 DialContext，redirect 也一并被覆盖。
//
// 与普通 net.Dial 的关键差异：这里会尝试解析出的**所有** IP（IPv4 优先），
// 而不是只拨第一个。内网主机名常同时解析出 IPv6 + 多个 IPv4，或首个 IPv4
// 不通（双网卡 / 漂移 IP），只拨第一个会导致"文件/URL 导入正常，但发请求失败"。
func SafeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := resolveCheckedHostIPs(ctx, host)
	if err != nil {
		return nil, err
	}

	// IPv4 优先：内网老 Java WebService 几乎只监听 IPv4，IPv6 常解析出来却
	// 不可达；若先死等 IPv6，会把整个请求超时耗尽，IPv4 根本没机会被尝试。
	var v4, v6 []net.IP
	for _, ip := range ips {
		if ip.To4() != nil {
			v4 = append(v4, ip)
		} else {
			v6 = append(v6, ip)
		}
	}
	candidates := append(v4, v6...)
	if len(candidates) == 0 {
		return nil, errors.New("无可用 IP 可连接")
	}

	dialer := &net.Dialer{}
	var lastErr error
	for _, ip := range candidates {
		conn, derr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
		// 超时 / 取消就别再试下一个了，立即返回
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("无可用 IP 可连接")
	}
	return nil, lastErr
}

// resolveCheckedHostIPs 解析 host 并返回通过危险地址校验的 IP 列表。
// host 为 IP 字面量时直接返回该 IP（校验后）。
func resolveCheckedHostIPs(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if isDangerousEndpointIP(ip) {
			return nil, fmt.Errorf("拒绝访问危险地址: %s", host)
		}
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS 解析失败: %w", err)
	}
	if len(addrs) == 0 {
		return nil, errors.New("DNS 解析结果为空")
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if isDangerousEndpointIP(addr.IP) {
			return nil, fmt.Errorf("拒绝访问危险地址: %s", host)
		}
		ips = append(ips, addr.IP)
	}
	return ips, nil
}

func rejectDangerousEndpointHost(ctx context.Context, host string) error {
	_, err := resolveCheckedHostIPs(ctx, host)
	return err
}

func isDangerousEndpointIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
