// guard.go - 默认单元测试的"零外部网络"护栏 (QA-01)
//
// 背景: license / sponsor / pet 三个内部网关的 primary+secondary 地址都**硬编码在源码里**
// (真实 Java httpInterface 网关)。一旦单元测试只把 primary 指向本地 mock, 却让
// secondary 保持源码默认值, mock 返 4xx/5xx 时 endpointclient 会自动切到真实备用地址,
// 于是 `go test ./...` 会真的向外部服务器发请求 (审查中已实际发生过一次, 被审批拦截)。
//
// 护栏的做法: 进程看起来是 `go test` 编译出来的测试二进制时, 只允许拨号到回环地址;
// 任何非回环目标直接返回错误, 把"静默外发"变成一条明确的测试失败。
//
// 生产二进制完全不受影响: isTestBinary() 为 false 时该检查是空操作,
// 且 http.Client 仍使用 http.DefaultTransport (代理环境变量等行为不变)。
//
// 需要真实外联时的显式出口:
//   - 单元测试: 设置环境变量 KAIRO_ALLOW_EXTERNAL_NETWORK=1
//   - 集成测试: 用 -tags=integration (配合专用 mock 服务器)
package endpointclient

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// AllowExternalNetworkEnv 显式放行外部网络的开关 (值为 "1" 时生效)。
const AllowExternalNetworkEnv = "KAIRO_ALLOW_EXTERNAL_NETWORK"

// guardTransport 在测试二进制里拒绝非回环目标, 其余情况原样透传。
type guardTransport struct {
	base http.RoundTripper
}

func (g *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := checkExternalAllowed(req.URL); err != nil {
		return nil, err
	}
	return g.base.RoundTrip(req)
}

// checkExternalAllowed 非测试二进制 / 显式放行 / 回环地址 → nil。
func checkExternalAllowed(u *url.URL) error {
	if u == nil {
		return nil
	}
	if !isTestBinary() || os.Getenv(AllowExternalNetworkEnv) == "1" {
		return nil
	}
	host := u.Hostname()
	if host == "" || isLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf(
		"endpointclient: 测试二进制拒绝访问外部地址 %q (默认单元测试不得外联; "+
			"如需真实外联请改用 -tags=integration 的集成测试, 或显式设置 %s=1): %w",
		u.Host, AllowExternalNetworkEnv, ErrExternalBlocked)
}

// ErrExternalBlocked 便于调用方/测试用 errors.Is 判定"被护栏拦下"。
var ErrExternalBlocked = errors.New("external network blocked in test binary")

// isTestBinary 判断当前进程是否由 `go test` 编译/驱动。
//
// 不依赖 testing 包的 flag 注册 (任何 import testing 的二进制都会注册),
// 只看编译产物名与 go test 注入的 -test.* 参数。
func isTestBinary() bool {
	if len(os.Args) > 0 {
		name := strings.ToLower(os.Args[0])
		if strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".test.exe") {
			return true
		}
	}
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-test.") {
			return true
		}
	}
	return false
}

// isLoopbackHost 判断主机名是否只指向本机。
func isLoopbackHost(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
