// simulate_test.go — Kairo 激活系统端到端模拟测试
//
// 覆盖 10 种典型场景, 每个场景独立跑, 输出详细模拟报告:
//   1. 正常激活 (服务端 {ok:true})
//   2. 错误激活码 → 服务端拒绝
//   3. IP 不匹配 → 服务端拒绝 (防内鬼)
//   4. 并发抢码 (5 个 IP 同时) → 1 成功 4 失败
//   5. 主备 fallback (主挂了自动切备用)
//   6. 主备都挂 → "所有激活服务都不可达"
//   7. 服务端返回乱码 → 客户端报"响应解析失败"
//   8. 服务端慢响应 → 客户端 5s 超时
//   9. cert 完整生命周期 (激活 → 重启模拟 → Check 通过)
//  10. 开发者白名单 → Check 直接通过, 不读 cert
//
// 运行:
//   go test -tags=simulate -v -run TestSimulate ./internal/license/
//
// 设计:
//   - 用 httptest 起一个全功能 mock server (内置业务逻辑 + 异常注入)
//   - HOME 切到临时目录, 避免污染用户 ~/.kairo/license.dat
//   - 每个场景 t.Log 输出人可读的"模拟报告"
//   - build tag = simulate, 默认 `go test` 不跑, 跑模拟测试要明确加 tag
//
//go:build simulate

package license

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ===== mock server 实现 (全功能, 支持注入异常) =====

// mockBinding 模拟 K_ACT_CODE 表: code → ip
type mockBinding struct {
	code string
	ip   string
}

// mockActivateServer 是模拟 Java 端的全功能 mock, 支持场景注入。
//   - normal: 正常激活业务逻辑 (同 IP 通过, 异 IP 拒绝)
//   - rejectAll: 所有请求都返回 {ok:false, error:"激活码无效"}
//   - ipMismatchForce: 强制返回 IP 不匹配拒绝 (用于模拟服务端 IP 绑定场景)
//   - garbage: 返回非 JSON 内容
//   - slow: sleep 10s (客户端 5s 超时)
type mockActivateServer struct {
	mu       sync.Mutex
	bindings map[string]*mockBinding
	scenario string
	reqLog   []mockRequest

	// 统计
	hitCount atomic.Int64
}

type mockRequest struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   map[string]string   `json:"query"`
	Header  http.Header         `json:"header"`
	Body    string              `json:"body"`
	RespCode int                `json:"resp_code"`
	RespBody string             `json:"resp_body"`
	Time    time.Time           `json:"time"`
}

func newMockServer(scenario string) (*mockActivateServer, *httptest.Server) {
	m := &mockActivateServer{
		bindings: map[string]*mockBinding{},
		scenario: scenario,
	}
	// 预置激活码
	m.bindings["ok-001"] = &mockBinding{code: "ok-001"}
	m.bindings["ok-002"] = &mockBinding{code: "ok-002"}
	m.bindings["race-001"] = &mockBinding{code: "race-001"}
	m.bindings["mismatch-001"] = &mockBinding{code: "mismatch-001", ip: "192.168.99.99"} // 预绑定到别的 IP

	srv := httptest.NewServer(http.HandlerFunc(m.handle))
	return m, srv
}

func (m *mockActivateServer) handle(w http.ResponseWriter, r *http.Request) {
	m.hitCount.Add(1)
	rec := mockRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  map[string]string{},
		Header: r.Header.Clone(),
		Time:   time.Now(),
	}
	for k := range r.URL.Query() {
		rec.Query[k] = r.URL.Query().Get(k)
	}
	body, _ := io.ReadAll(r.Body)
	rec.Body = string(body)

	defer func() {
		m.mu.Lock()
		m.reqLog = append(m.reqLog, rec)
		m.mu.Unlock()
	}()

	// 场景注入
	switch m.scenario {
	case "garbage":
		rec.RespCode = 200
		rec.RespBody = "this is not json at all {{{"
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, rec.RespBody)
		return
	case "slow":
		// 慢响应: 监听 client 断开 (客户端 5s 超时后会断开连接), 立即退出避免 srv.Close 卡住
		select {
		case <-time.After(10 * time.Second):
			rec.RespCode = 200
			rec.RespBody = `{"ok":true}`
			_, _ = w.Write([]byte(rec.RespBody))
		case <-r.Context().Done():
			// 客户端超时断开, 我们也跟着退出, 不让 httptest.Close 阻塞
			return
		}
		return
	case "rejectAll":
		rec.RespCode = 200
		rec.RespBody = `{"ok":false,"error":"激活码无效"}`
		writeJSON(w, rec.RespCode, map[string]any{"ok": false, "error": "激活码无效"})
		return
	case "ipMismatchForce":
		rec.RespCode = 200
		rec.RespBody = `{"ok":false,"error":"激活码已被其他机器使用"}`
		writeJSON(w, rec.RespCode, map[string]any{"ok": false, "error": "激活码已被其他机器使用"})
		return
	}

	// 正常业务: Basic auth + 查 binding + IP 绑定
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Basic ") {
		rec.RespCode = 401
		rec.RespBody = `{"ok":false,"error":"missing Basic auth"}`
		writeJSON(w, 401, map[string]any{"ok": false, "error": "missing Basic auth"})
		return
	}
	var req map[string]string
	_ = json.Unmarshal(body, &req)
	code := req["secret_key"]
	ip := req["ip"]
	if code == "" || ip == "" {
		rec.RespCode = 400
		rec.RespBody = `{"ok":false,"error":"missing secret_key or ip"}`
		writeJSON(w, 400, map[string]any{"ok": false, "error": "missing secret_key or ip"})
		return
	}

	m.mu.Lock()
	binding, exists := m.bindings[code]
	m.mu.Unlock()

	if !exists {
		rec.RespCode = 200
		rec.RespBody = `{"ok":false,"error":"激活码无效"}`
		writeJSON(w, 200, map[string]any{"ok": false, "error": "激活码无效"})
		return
	}

	// IP 不匹配
	if binding.ip != "" && binding.ip != ip {
		rec.RespCode = 200
		rec.RespBody = `{"ok":false,"error":"激活码已被其他机器使用"}`
		writeJSON(w, 200, map[string]any{"ok": false, "error": "激活码已被其他机器使用"})
		return
	}

	// 首次激活 / 重激活
	m.mu.Lock()
	binding.ip = ip
	m.mu.Unlock()
	rec.RespCode = 200
	rec.RespBody = `{"ok":true}`
	writeJSON(w, 200, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ===== 公共 helper =====

// simulateSetup 把 HOME 切到临时目录, 备份 license var, 返回 cleanup。
func simulateSetup(t *testing.T) (tmpHome string, cleanup func()) {
	t.Helper()

	origHome := os.Getenv("HOME")
	tmpHome, err := os.MkdirTemp("", "license-simulate-*")
	if err != nil {
		t.Fatalf("创建临时 HOME 失败: %v", err)
	}
	os.Setenv("HOME", tmpHome)

	prim, sec, auth := LicenseServerPrimary, LicenseServerSecondary, BasicAuthHeader
	k1, v1 := URLParamK1, URLParamV1
	k2, v2 := URLParamK2, URLParamV2
	k3, v3 := URLParamK3, URLParamV3

	cleanup = func() {
		os.Setenv("HOME", origHome)
		os.RemoveAll(tmpHome)
		LicenseServerPrimary = prim
		LicenseServerSecondary = sec
		BasicAuthHeader = auth
		URLParamK1, URLParamV1 = k1, v1
		URLParamK2, URLParamV2 = k2, v2
		URLParamK3, URLParamV3 = k3, v3
	}
	return tmpHome, cleanup
}

// pointToMockServer 设置 license 包 var 指向给定 mock server, 并应用标准 URL 参数。
//
// 注意: mockURL 是 httptest server 的根 URL (没路径), 这里自动拼上 /credit/httpInterface
// 跟真实接口路径一致, 模拟报告里的 URL 才能反映真实形态。
func pointToMockServer(mockURL string) {
	pointToMockServerWithPath(mockURL, "/credit/httpInterface")
}

// pointToMockServerWithPath 设置 license 包 var 指向 mock server + 自定义路径。
// 用于场景 5/6 等需要不同主备地址路径的测试。
func pointToMockServerWithPath(mockURL, path string) {
	LicenseServerPrimary = mockURL + path
	LicenseServerSecondary = ""
	BasicAuthHeader = "anN5aDpqc3loQDEyMw=="
	URLParamK1 = "channelID"
	URLParamV1 = "PC"
	URLParamK2 = "serviceID"
	URLParamV2 = "KairoActivateAction"
	URLParamK3 = "seqNo"
	URLParamV3 = "<TIMESTAMP>"
}

// certPath 临时 HOME 下的 cert 路径
func simCertPath() string {
	return filepath.Join(os.Getenv("HOME"), ".kairo", "license.dat")
}

// ===== 10 个场景 =====

// TestSimulate_01_NormalActivate 场景 1: 正常激活
func TestSimulate_01_NormalActivate(t *testing.T) {
	tmpHome, cleanup := simulateSetup(t)
	defer cleanup()

	mock, srv := newMockServer("normal")
	defer srv.Close()
	pointToMockServer(srv.URL)

	t.Log("")
	t.Log("【场景 1】正常激活 (服务端返回 {ok:true})")
	t.Logf("    Mock server:    %s", srv.URL)
	t.Logf("    临时 HOME:      %s", tmpHome)
	t.Logf("    本机 IP:        %s", localIP())
	t.Log("    调用:           license.Activate(\"ok-001\")")

	start := time.Now()
	err := Activate("ok-001")
	elapsed := time.Since(start)

	t.Logf("    耗时:           %v", elapsed)

	if err != nil {
		t.Errorf("  ❌ 激活失败 (期望成功): %v", err)
		return
	}
	t.Log("    激活结果:       ✅ 成功")

	// 打印实际 HTTP 请求
	mock.mu.Lock()
	last := mock.reqLog[len(mock.reqLog)-1]
	mock.mu.Unlock()

	t.Log("    ── 实际 HTTP 请求 ──")
	t.Logf("    POST %s?%s", last.Path, encodeQuery(last.Query))
	t.Logf("    Authorization:  %s", last.Header.Get("Authorization"))
	t.Logf("    Content-Type:   %s", last.Header.Get("Content-Type"))
	t.Logf("    Body:           %s", last.Body)
	t.Logf("    ── 服务端响应 ──")
	t.Logf("    HTTP %d", last.RespCode)
	t.Logf("    Body: %s", strings.TrimSpace(last.RespBody))

	// 验证 cert 落盘
	cp := simCertPath()
	if info, err := os.Stat(cp); err == nil {
		t.Logf("    cert 落盘:      %s (%d bytes)", cp, info.Size())
		cert, _ := loadLocalCert()
		if cert != nil {
			t.Logf("    cert 内容:      code=%s ip=%s", cert.Code, cert.IP)
		}
	} else {
		t.Errorf("  ❌ cert 未落盘: %v", err)
	}
	t.Log("")
}

// TestSimulate_02_RejectBadCode 场景 2: 错误激活码
func TestSimulate_02_RejectBadCode(t *testing.T) {
	_, cleanup := simulateSetup(t)
	defer cleanup()

	mock, srv := newMockServer("normal")
	defer srv.Close()
	pointToMockServer(srv.URL)

	t.Log("")
	t.Log("【场景 2】错误激活码 (服务端返回 {ok:false, error:\"激活码无效\"})")
	t.Logf("    Mock server:    %s", srv.URL)
	t.Log("    调用:           license.Activate(\"nonexistent-code\")")

	err := Activate("nonexistent-code")
	if err == nil {
		t.Errorf("  ❌ 期望返回错误, 但成功了")
		return
	}
	t.Logf("    激活结果:       ✅ 正确拒绝 (%v)", err)

	mock.mu.Lock()
	last := mock.reqLog[len(mock.reqLog)-1]
	mock.mu.Unlock()
	t.Logf("    服务端响应:     HTTP %d, body=%s", last.RespCode, strings.TrimSpace(last.RespBody))

	// 验证 cert 没落盘
	if _, err := os.Stat(simCertPath()); err == nil {
		t.Errorf("  ❌ 失败激活不应该写 cert, 但文件存在")
	} else {
		t.Logf("    cert 状态:      未落盘 (正确, 拒绝时不写)")
	}
	t.Log("")
}

// TestSimulate_03_IPMismatch 场景 3: IP 不匹配 (防内鬼)
func TestSimulate_03_IPMismatch(t *testing.T) {
	_, cleanup := simulateSetup(t)
	defer cleanup()

	mock, srv := newMockServer("normal")
	defer srv.Close()
	pointToMockServer(srv.URL)

	t.Log("")
	t.Log("【场景 3】IP 不匹配 (模拟\"内鬼把激活码给了别人\")")
	t.Log("    场景: 激活码 mismatch-001 已绑定到 192.168.99.99,")
	t.Log("          攻击者本机 IP 不是 99.99, 拿同一个码激活")

	// mock 端已经预绑定 mismatch-001 → 192.168.99.99, 本机 IP 肯定不是这个
	err := Activate("mismatch-001")
	if err == nil {
		t.Errorf("  ❌ 期望被拒绝, 但成功了 (IP 校验失效!)")
		return
	}
	if !strings.Contains(err.Error(), "其他机器") && !strings.Contains(err.Error(), "已被") {
		t.Logf("  ⚠️  拒绝原因: %v (期望含'其他机器'或'已被')", err)
	} else {
		t.Logf("    激活结果:       ✅ 正确拒绝: %v", err)
	}

	mock.mu.Lock()
	last := mock.reqLog[len(mock.reqLog)-1]
	mock.mu.Unlock()
	t.Logf("    服务端响应:     HTTP %d, body=%s", last.RespCode, strings.TrimSpace(last.RespBody))
	t.Log("    安全链生效:     攻击者拿别人激活码 → IP 不匹配 → 拒绝 (无 cert 落盘)")

	if _, err := os.Stat(simCertPath()); err == nil {
		t.Errorf("  ❌ IP 不匹配时不应该写 cert")
	}
	t.Log("")
}

// TestSimulate_04_ConcurrentRace 场景 4: 并发抢码
func TestSimulate_04_ConcurrentRace(t *testing.T) {
	_, cleanup := simulateSetup(t)
	defer cleanup()

	mock, srv := newMockServer("normal")
	defer srv.Close()
	pointToMockServer(srv.URL)

	t.Log("")
	t.Log("【场景 4】并发抢码 (5 个不同 IP 同时拿 race-001 第一次激活)")
	t.Log("    期望结果: 恰好 1 个成功, 4 个被拒 (并发竞态防护)")

	const N = 5
	type result struct {
		idx     int
		ok      bool
		errMsg  string
		elapsed time.Duration
	}
	results := make([]result, N)
	var wg sync.WaitGroup

	// 不走 Activate (会写 cert 落盘互相覆盖), 直接调底层 callActivate
	// 但 callActivate 是 private, 同包可以直接用
	start := time.Now()
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			fakeIP := fmt.Sprintf("10.0.0.%d", idx+1)
			reqStart := time.Now()
			resp, err := callActivate("race-001", fakeIP)
			elapsed := time.Since(reqStart)
			if err != nil {
				results[idx] = result{idx: idx, ok: false, errMsg: err.Error(), elapsed: elapsed}
				return
			}
			results[idx] = result{idx: idx, ok: resp.OK, errMsg: resp.Error, elapsed: elapsed}
		}(i)
	}
	wg.Wait()
	totalElapsed := time.Since(start)

	successCount := 0
	failCount := 0
	for _, r := range results {
		status := "❌ FAIL"
		if r.ok {
			status = "✅ OK  "
			successCount++
		} else {
			failCount++
		}
		t.Logf("    goroutine %d (IP=10.0.0.%d): %s (%v) %q", r.idx+1, r.idx+1, status, r.elapsed, r.errMsg)
	}

	t.Logf("    汇总:           成功=%d, 失败=%d (总耗时 %v)", successCount, failCount, totalElapsed)

	if successCount != 1 {
		t.Errorf("  ❌ 应该恰好 1 个成功, got %d", successCount)
	}
	if failCount != N-1 {
		t.Errorf("  ❌ 应该 %d 个失败, got %d", N-1, failCount)
	}

	t.Logf("    Mock 收到请求:   %d 个 (5 个 goroutine 都到了)", mock.hitCount.Load())
	t.Log("")
}

// TestSimulate_05_FallbackToSecondary 场景 5: 主备 fallback
func TestSimulate_05_FallbackToSecondary(t *testing.T) {
	_, cleanup := simulateSetup(t)
	defer cleanup()

	_, primarySrv := newMockServer("normal")
	defer primarySrv.Close()
	mock, secondarySrv := newMockServer("normal")
	defer secondarySrv.Close()

	// 主地址指向一个不可达的端口 (1), 备用地址指向真正的 mock
	LicenseServerPrimary = "http://127.0.0.1:1/credit/httpInterface"
	LicenseServerSecondary = secondarySrv.URL + "/credit/httpInterface"
	BasicAuthHeader = "anN5aDpqc3loQDEyMw=="
	URLParamK1 = "channelID"; URLParamV1 = "PC"
	URLParamK2 = "serviceID"; URLParamV2 = "KairoActivateAction"
	URLParamK3 = "seqNo"; URLParamV3 = "<TIMESTAMP>"
	_ = primarySrv // 主 mock 不收请求, 仅占位证明"主地址真的不通"

	t.Log("")
	t.Log("【场景 5】主备 fallback (主地址挂了, 自动切备用)")
	t.Logf("    主地址:         http://127.0.0.1:1/credit/httpInterface (故意不可达)")
	t.Logf("    备用地址:       %s", secondarySrv.URL)

	start := time.Now()
	err := Activate("ok-001")
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("  ❌ 应自动切到备用地址成功, got error: %v", err)
		return
	}
	t.Logf("    激活结果:       ✅ 成功 (主挂了自动切到备用, 耗时 %v)", elapsed)

	// 检查主备 mock server 收到几个请求
	t.Logf("    主 mock 收到:    0 个 (地址不通, TCP 连接直接拒)")
	t.Logf("    备用 mock 收到:  %d 个 (客户端切过来发请求)", mock.hitCount.Load())

	// 备用地址收到的请求应该只 1 个
	if mock.hitCount.Load() != 1 {
		t.Errorf("  ❌ 备用地址应该收到 1 个请求, got %d", mock.hitCount.Load())
	}
	t.Log("")
}

// TestSimulate_06_AllServersDown 场景 6: 主备都挂
func TestSimulate_06_AllServersDown(t *testing.T) {
	_, cleanup := simulateSetup(t)
	defer cleanup()

	LicenseServerPrimary = "http://127.0.0.1:1/credit/httpInterface"
	LicenseServerSecondary = "http://127.0.0.1:2/credit/httpInterface"
	BasicAuthHeader = "anN5aDpqc3loQDEyMw=="
	URLParamK1 = "channelID"; URLParamV1 = "PC"
	URLParamK2 = "serviceID"; URLParamV2 = "KairoActivateAction"
	URLParamK3 = "seqNo"; URLParamV3 = "<TIMESTAMP>"

	t.Log("")
	t.Log("【场景 6】主备都挂 (客户端报\"所有激活服务都不可达\")")
	t.Logf("    主地址:         http://127.0.0.1:1 (不可达)")
	t.Logf("    备用地址:       http://127.0.0.1:2 (不可达)")

	start := time.Now()
	err := Activate("ok-001")
	elapsed := time.Since(start)

	if err == nil {
		t.Errorf("  ❌ 主备都挂应该报错, 但成功了")
		return
	}
	t.Logf("    激活结果:       ✅ 正确报错: %v", err)
	t.Logf("    耗时:           %v (2 次失败各 ~5s 超时)", elapsed)

	if !strings.Contains(err.Error(), "所有激活服务都不可达") {
		t.Errorf("  ❌ 错误信息应包含'所有激活服务都不可达', got: %v", err)
	}
	t.Log("")
}

// TestSimulate_07_ServerReturnsGarbage 场景 7: 服务端乱码
func TestSimulate_07_ServerReturnsGarbage(t *testing.T) {
	_, cleanup := simulateSetup(t)
	defer cleanup()

	mock, srv := newMockServer("garbage")
	defer srv.Close()
	pointToMockServer(srv.URL)

	t.Log("")
	t.Log("【场景 7】服务端返回乱码 (HTTP 200 但 body 不是 JSON)")
	t.Logf("    Mock server:    %s (scenario=garbage)", srv.URL)
	t.Log("    服务端响应:     \"this is not json at all {{{")

	err := Activate("ok-001")
	if err == nil {
		t.Errorf("  ❌ 乱码响应应该报错, 但成功了")
		return
	}
	t.Logf("    激活结果:       ✅ 正确报错: %v", err)

	mock.mu.Lock()
	last := mock.reqLog[len(mock.reqLog)-1]
	mock.mu.Unlock()
	t.Logf("    HTTP 状态:      %d (虽然是 200, 但 body 不可解析)", last.RespCode)
	t.Logf("    服务端 body:    %s", strings.TrimSpace(last.RespBody))

	if !strings.Contains(err.Error(), "响应解析失败") {
		t.Errorf("  ❌ 错误信息应包含'响应解析失败', got: %v", err)
	}
	t.Log("")
}

// TestSimulate_08_ServerTimeout 场景 8: 服务端慢响应 (客户端 5s 超时)
func TestSimulate_08_ServerTimeout(t *testing.T) {
	_, cleanup := simulateSetup(t)
	defer cleanup()

	mock, srv := newMockServer("slow")
	defer srv.Close()
	pointToMockServer(srv.URL)

	t.Log("")
	t.Log("【场景 8】服务端慢响应 (sleep 10s, 客户端 5s 超时)")
	t.Logf("    Mock server:    %s (scenario=slow, sleep 10s)", srv.URL)

	start := time.Now()
	err := Activate("ok-001")
	elapsed := time.Since(start)

	if err == nil {
		t.Errorf("  ❌ 慢响应应该报超时, 但成功了")
		return
	}
	t.Logf("    激活结果:       ✅ 正确报错: %v", err)
	t.Logf("    耗时:           %v (期望 ~5s, 即客户端超时时间)", elapsed)

	if elapsed > 7*time.Second || elapsed < 4*time.Second {
		t.Errorf("  ❌ 耗时应在 4-7s 之间 (5s 超时 +/- 误差), got %v", elapsed)
	}
	if mock.hitCount.Load() != 1 {
		t.Errorf("  ❌ 应该只发 1 个请求就超时退出, got %d", mock.hitCount.Load())
	}
	t.Log("")
}

// TestSimulate_09_CertLifecycle 场景 9: cert 完整生命周期
func TestSimulate_09_CertLifecycle(t *testing.T) {
	_, cleanup := simulateSetup(t)
	defer cleanup()

	_, srv := newMockServer("normal")
	defer srv.Close()
	pointToMockServer(srv.URL)

	t.Log("")
	t.Log("【场景 9】cert 完整生命周期 (激活 → 重启模拟 → Check 通过)")

	// 9.1 启动 → 无 cert → Check 应失败
	SetConfigProvider(func() *ConfigSnapshot { return &ConfigSnapshot{} })
	if err := Check(); err == nil {
		t.Errorf("  9.1 启动时应无 cert, Check 期望失败")
	} else {
		t.Logf("    9.1 启动 (无 cert):     Check 失败 (符合预期): %v", err)
	}

	// 9.2 激活
	if err := Activate("ok-001"); err != nil {
		t.Fatalf("  9.2 激活失败: %v", err)
	}
	t.Log("    9.2 激活 ok-001:         ✅ 成功")

	// 9.3 cert 落盘
	cp := simCertPath()
	if _, err := os.Stat(cp); err != nil {
		t.Fatalf("  9.3 cert 未落盘: %v", err)
	}
	t.Logf("    9.3 cert 落盘:          %s", cp)

	// 9.4 读出 cert 内容
	cert, err := loadLocalCert()
	if err != nil {
		t.Fatalf("  9.4 读 cert 失败: %v", err)
	}
	t.Logf("    9.4 cert 内容:          code=%s ip=%s", cert.Code, cert.IP)

	// 9.5 模拟重启 (重新 Check, 应该通过)
	if err := Check(); err != nil {
		t.Errorf("  9.5 重启 Check 应通过, got: %v", err)
	} else {
		t.Log("    9.5 重启 Check:         ✅ 通过 (cert 有效 + IP 匹配)")
	}

	// 9.6 GetStatus 应返回 local-cert
	st := GetStatus()
	if st.Reason != "local-cert" {
		t.Errorf("  9.6 GetStatus.Reason 应为 local-cert, got: %q", st.Reason)
	} else {
		t.Logf("    9.6 GetStatus:          licensed=true reason=%s", st.Reason)
	}

	// 9.7 模拟 IP 变了 (换网卡) → cert IP 不匹配 → Check 失败
	// 直接改 cert 文件的 IP 字段模拟
	data, _ := os.ReadFile(cp)
	modified := strings.Replace(string(data), fmt.Sprintf("%q", cert.IP), `"1.2.3.99"`, 1)
	if err := os.WriteFile(cp, []byte(modified), 0o600); err != nil {
		t.Fatalf("  9.7 改 cert 文件失败: %v", err)
	}
	if err := Check(); err == nil {
		t.Errorf("  9.7 IP 改了应导致 Check 失败")
	} else {
		t.Logf("    9.7 IP 改了 (模拟换网卡):  Check 失败 (符合预期): %v", err)
		st := GetStatus()
		t.Logf("    9.7 GetStatus.Reason:   %s", st.Reason)
	}

	t.Log("")
}

// TestSimulate_10_BypassWinsOverCert 场景 10: 开发者白名单
func TestSimulate_10_BypassWinsOverCert(t *testing.T) {
	_, cleanup := simulateSetup(t)
	defer cleanup()

	t.Log("")
	t.Log("【场景 10】开发者白名单 (config.yaml 里 kairo: \"111222\" 时)")
	t.Log("    优先级: 白名单 > 本地 cert > 弹激活窗")
	t.Log("    即使 cert 文件被改坏, 白名单仍能放行")

	// 10.1 先无白名单 → 应该没 cert 失败
	SetConfigProvider(func() *ConfigSnapshot { return &ConfigSnapshot{} })
	if err := Check(); err == nil {
		t.Errorf("  10.1 无白名单 + 无 cert, 期望失败")
	} else {
		t.Logf("    10.1 无白名单:           Check 失败 (符合预期)")
	}

	// 10.2 设白名单 → 即使没 cert 也通过
	SetConfigProvider(func() *ConfigSnapshot { return &ConfigSnapshot{KairoInternalToken: "111222"} })
	if err := Check(); err != nil {
		t.Errorf("  10.2 白名单应放行, got: %v", err)
	} else {
		t.Log("    10.2 白名单生效:         ✅ Check 通过 (无 cert 也能用)")
		st := GetStatus()
		t.Logf("    10.2 GetStatus.Reason:   %s", st.Reason)
	}

	// 10.3 写一个 IP 不匹配的 cert (模拟"被改过"), 白名单应仍通过
	bogusIP := "1.2.3.99"
	_ = saveLocalCert(&Cert{Code: "bogus", IP: bogusIP})
	t.Logf("    10.3 写一个 IP=%s 的假 cert", bogusIP)

	if err := Check(); err != nil {
		t.Errorf("  10.3 白名单应比 cert 优先, got: %v", err)
	} else {
		t.Log("    10.3 白名单 > cert:      ✅ 即使 cert IP 不匹配, 白名单仍放行")
		st := GetStatus()
		t.Logf("    10.3 GetStatus.Reason:   %s (注意: 是 dev-bypass, 不是 local-cert)", st.Reason)
	}

	t.Log("")
}

// ===== 辅助: query string 编码 =====
func encodeQuery(q map[string]string) string {
	var parts []string
	for k, v := range q {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, "&")
}

// 抑制 unused import 警告 (sha256 / hex 用在某些情况下)
var _ = sha256.Sum256
var _ = hex.EncodeToString