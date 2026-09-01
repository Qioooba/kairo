// 集成测试 - 真实调 mock server, 验证 Go 端 license.Activate() 全流程
//
// 运行方法:
//   1. 启动 mock server:  go run ./cmd/mock-license-server -addr :18091
//   2. 运行本测试:        go test -tags=integration -v ./internal/license/
//      (默认跳过, 避免 CI 环境跑挂)
//
//go:build integration

package license

import (
	"fmt"
	"sync"
	"testing"
)

func TestIntegration_Activate_Success(t *testing.T) {
	// 指向 mock server
	LicenseServerPrimary = "http://localhost:18091/kairo/auth/activate"
	LicenseServerSecondary = ""
	BasicAuthHeader = "TEST_TOKEN_123"
	URLParamK1 = "k1"; URLParamV1 = "v1"
	URLParamK2 = "k2"; URLParamV2 = "v2"
	URLParamK3 = "k3"; URLParamV3 = "v3"

	// 清本地证书
	withCleanCert(t)

	// 调 mock 的 admin/reset 把 test-code-001 重置
	// (本地没法调 mock, 假设 mock 刚启动状态)

	// 调 Activate
	err := Activate("test-code-001")
	if err != nil {
		t.Fatalf("Activate 应该成功: %v", err)
	}

	// 验证证书写到了
	cert, err := loadLocalCert()
	if err != nil {
		t.Fatalf("loadLocalCert 应该成功: %v", err)
	}
	if cert.Code != "test-code-001" {
		t.Errorf("Code 不一致: got=%q", cert.Code)
	}
	t.Logf("本机 IP: %s, 证书 IP: %s", localIP(), cert.IP)
}

func TestIntegration_Activate_IPMismatch(t *testing.T) {
	LicenseServerPrimary = "http://localhost:18091/kairo/auth/activate"
	LicenseServerSecondary = ""
	BasicAuthHeader = "TEST_TOKEN_123"
	URLParamK1 = "k1"; URLParamV1 = "v1"
	URLParamK2 = "k2"; URLParamV2 = "v2"
	URLParamK3 = "k3"; URLParamV3 = "v3"

	withCleanCert(t)

	// 场景模拟: A 用 test-code-001 激活成功, mock 端记录 IP=192.168.1.148 (本机)
	// 然后 B (IP=10.0.0.99) 想用同一个码激活 → 应该被 mock 拒绝

	// A 激活
	if err := Activate("test-code-001"); err != nil {
		t.Fatalf("A 首次激活失败: %v", err)
	}
	t.Logf("A 激活成功, mock 端 IP 已绑本机")

	// 现在模拟"用不同 IP 重发请求" - 直接调 callActivate 验证 mock 行为
	// (绕过本机 IP, 强制传 B 的 IP, 验证 mock 会拒绝)
	resp, err := callActivate("test-code-001", "10.0.0.99")
	if err != nil {
		t.Fatalf("callActivate 网络错误: %v", err)
	}
	if resp.OK {
		t.Errorf("用不同 IP 重激活应该被 mock 拒绝, 但成功了")
	}
	if !contains(resp.Error, "其他机器") {
		t.Errorf("错误信息应该包含'其他机器', got: %s", resp.Error)
	}
	t.Logf("不同 IP 重激活被拒绝 (符合预期): %s", resp.Error)
}

func TestIntegration_Activate_BadCode(t *testing.T) {
	LicenseServerPrimary = "http://localhost:18091/kairo/auth/activate"
	LicenseServerSecondary = ""
	BasicAuthHeader = "TEST_TOKEN_123"
	URLParamK1 = "k1"; URLParamV1 = "v1"
	URLParamK2 = "k2"; URLParamV2 = "v2"
	URLParamK3 = "k3"; URLParamV3 = "v3"

	withCleanCert(t)

	err := Activate("nonexistent-code-xyz")
	if err == nil {
		t.Errorf("无效激活码应该返回错误")
	}
	t.Logf("Activate 返回错误 (符合预期): %v", err)
}

// TestIntegration_ConcurrentActivate 验证: 多个 IP 同时激活同一个 code, 最终只有一个能成功
//
// 这是漏洞 #9 (并发竞态) 的回归测试
// 真实场景: 内鬼拿了 A 的激活码, 同一时间多台机器都去激活, 应该只有 1 台成功
func TestIntegration_ConcurrentActivate(t *testing.T) {
	LicenseServerPrimary = "http://localhost:18091/kairo/auth/activate"
	LicenseServerSecondary = ""
	BasicAuthHeader = "TEST_TOKEN_123"
	URLParamK1 = ""; URLParamV1 = ""
	URLParamK2 = ""; URLParamV2 = ""
	URLParamK3 = ""; URLParamV3 = ""

	// 重置 mock (需要外部调用, 这里用 withCleanCert 假装)
	withCleanCert(t)

	// 模拟 5 个不同 IP 同时拿同一个 code 激活
	const N = 5
	type result struct {
		idx     int
		ok      bool
		errMsg  string
	}
	results := make([]result, N)
	var wg sync.WaitGroup

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			fakeIP := fmt.Sprintf("10.0.0.%d", idx+1)
			resp, err := callActivate("test-code-002", fakeIP)
			if err != nil {
				results[idx] = result{idx: idx, ok: false, errMsg: err.Error()}
				return
			}
			results[idx] = result{idx: idx, ok: resp.OK, errMsg: resp.Error}
		}(i)
	}

	wg.Wait()

	// 统计: 应该只有 1 个 ok=true (第一个到达的), 其他都是 IP 不匹配错误
	successCount := 0
	failCount := 0
	var winnerIP string
	for _, r := range results {
		if r.ok {
			successCount++
			winnerIP = fmt.Sprintf("10.0.0.%d", r.idx+1)
		} else {
			failCount++
			t.Logf("  goroutine %d (IP=10.0.0.%d): 拒绝 (符合预期): %s",
				r.idx, r.idx+1, r.errMsg)
		}
	}

	t.Logf("汇总: 成功=%d (winner IP=%s), 失败=%d", successCount, winnerIP, failCount)

	// mock server 用 mutex 串行化, 应该恰好 1 个成功, N-1 个失败
	if successCount != 1 {
		t.Errorf("应该恰好 1 个成功, got %d", successCount)
	}
	if failCount != N-1 {
		t.Errorf("应该 %d 个失败, got %d", N-1, failCount)
	}
}
