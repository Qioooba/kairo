// guard_test.go - "默认单元测试零外部网络"护栏的回归 (QA-01)
//
// 覆盖:
//  1. 测试二进制里确实识别为测试进程
//  2. 回环地址判定 (localhost / 127.0.0.1 / ::1 / *.localhost 放行)
//  3. 非回环地址在测试二进制里被拒绝, 且错误可 errors.Is(ErrExternalBlocked)
//  4. 源码硬编码的真实 pet 备用网关地址会被拒绝 —— 这是审查中实际发生过的意外外发
//  5. 显式放行开关生效
package endpointclient

import (
	"errors"
	"net/url"
	"testing"
	"time"
)

// TestIsTestBinary 测试进程必须被识别为测试二进制。
func TestIsTestBinary(t *testing.T) {
	if !isTestBinary() {
		t.Fatal("go test 编译出的测试二进制必须被 isTestBinary() 识别; 否则护栏不会生效")
	}
}

// TestIsLoopbackHost 回环判定表驱动。
func TestIsLoopbackHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"LOCALHOST", true},
		{"localhost.", true},
		{"foo.localhost", true},
		{"127.0.0.1", true},
		{"127.1.2.3", true},
		{"::1", true},
		{"[::1]", false}, // Hostname() 已剥掉方括号, 带括号不算合法 host
		{"66.0.34.198", false},
		{"66.0.34.199", false},
		{"example.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isLoopbackHost(c.host); got != c.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// TestGuard_BlocksNonLoopbackInTestBinary 非回环地址必须被拒绝。
func TestGuard_BlocksNonLoopbackInTestBinary(t *testing.T) {
	t.Setenv(AllowExternalNetworkEnv, "")

	u, err := url.Parse("http://66.0.34.198:9080/credit/httpInterface?channelID=PC")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	gotErr := checkExternalAllowed(u)
	if gotErr == nil {
		t.Fatal("测试二进制不得放行非回环地址")
	}
	if !errors.Is(gotErr, ErrExternalBlocked) {
		t.Fatalf("错误应可用 errors.Is(ErrExternalBlocked) 判定, got: %v", gotErr)
	}
}

// TestGuard_RealHardcodedPetSecondaryIsBlocked 源码硬编码的真实备用网关在测试里必须打不出去。
//
// 这正是 QA-01 记录的意外外发路径: 只把 primary 换成本地 mock, secondary 仍是源码默认值,
// mock 返 5xx 后 endpointclient 会切到真实地址。护栏必须让这种情况以错误形式暴露,
// 而不是静默向外部服务器发请求。
func TestGuard_RealHardcodedPetSecondaryIsBlocked(t *testing.T) {
	t.Setenv(AllowExternalNetworkEnv, "")

	cfg := Config{
		Primary:   "http://66.0.34.199:9080/credit/httpInterface",
		Secondary: "http://66.0.34.198:9080/credit/httpInterface",
		Auth:      "anN5aDpqc3loQDEyMw==",
		Timeout:   500 * time.Millisecond,
	}
	_, err := Call(cfg, []byte(`{"probe":true}`))
	if err == nil {
		t.Fatal("真实硬编码网关在测试二进制里必须被拒绝 (不得真的外发)")
	}
	if !errors.Is(err, ErrExternalBlocked) {
		t.Fatalf("应以 ErrExternalBlocked 结束, got: %v", err)
	}
}

// TestGuard_LoopbackStillAllowed 回环地址不受护栏影响 (现有 mock 测试依赖此点)。
func TestGuard_LoopbackStillAllowed(t *testing.T) {
	t.Setenv(AllowExternalNetworkEnv, "")

	for _, raw := range []string{
		"http://127.0.0.1:18092/x",
		"http://localhost:18092/x",
		"http://[::1]:18092/x",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", raw, err)
		}
		if err := checkExternalAllowed(u); err != nil {
			t.Errorf("%s 应放行, got: %v", raw, err)
		}
	}
}

// TestGuard_ExplicitOptIn 显式设置开关后护栏放行 (集成/真实外联路径)。
func TestGuard_ExplicitOptIn(t *testing.T) {
	t.Setenv(AllowExternalNetworkEnv, "1")

	u, _ := url.Parse("http://66.0.34.198:9080/credit/httpInterface")
	if err := checkExternalAllowed(u); err != nil {
		t.Fatalf("显式设置 %s=1 后应放行, got: %v", AllowExternalNetworkEnv, err)
	}
}
