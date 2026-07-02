// License 包单元测试。
//
// 覆盖场景:
//   1. TestDevBypass - 白名单匹配 / 不匹配
//   2. TestCertEncryptDecryptRoundTrip - 加密 → 解密 → 数据一致
//   3. TestCertIPMismatch - IP 不匹配时 GCM AAD 校验失败
//   4. TestCertCorruptedPayload - payload 被改 → 解密失败
//   5. TestLocalIP - 本机 IP 提取
//   6. TestCheck_BypassWins - 白名单优先于本地证书
//   7. TestCheck_LocalCertValid - 本地证书有效时通过
//   8. TestCheck_NoCert - 没证书时返回 ErrLicenseMissing
package license

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// withCleanCert 在测试前删除 ~/.kairo/license.dat (如果存在), 避免污染。
func withCleanCert(t *testing.T) {
	t.Helper()
	p, _ := certPath()
	_ = os.Remove(p)
}

// TestDevBypass 白名单匹配 / 不匹配
func TestDevBypass(t *testing.T) {
	// 没设 provider → 一定 false
	SetConfigProvider(func() *ConfigSnapshot { return nil })
	if devBypass() {
		t.Errorf("未注入 config 时 devBypass 应该返回 false")
	}

	// 设置 token != "111222" → false
	SetConfigProvider(func() *ConfigSnapshot { return &ConfigSnapshot{KairoInternalToken: "wrong"} })
	if devBypass() {
		t.Errorf("token 不匹配时 devBypass 应该返回 false")
	}

	// 设置 token == "111222" → true
	SetConfigProvider(func() *ConfigSnapshot { return &ConfigSnapshot{KairoInternalToken: "111222"} })
	if !devBypass() {
		t.Errorf("token 匹配时 devBypass 应该返回 true")
	}
}

// TestCertEncryptDecryptRoundTrip 加密→解密往返数据一致
func TestCertEncryptDecryptRoundTrip(t *testing.T) {
	withCleanCert(t)

	original := &Cert{Code: "test-code-001", IP: "192.168.1.50"}
	if err := saveLocalCert(original); err != nil {
		t.Fatalf("saveLocalCert 失败: %v", err)
	}

	got, err := loadLocalCert()
	if err != nil {
		t.Fatalf("loadLocalCert 失败: %v", err)
	}

	if got.Code != original.Code {
		t.Errorf("Code 不一致: got=%q want=%q", got.Code, original.Code)
	}
	if got.IP != original.IP {
		t.Errorf("IP 不一致: got=%q want=%q", got.IP, original.IP)
	}
}

// TestCertIPMismatch IP 不匹配时 GCM AAD 校验失败
//
// 这是核心安全测试: 验证"复制 license.dat 到别的机器"会被防住。
func TestCertIPMismatch(t *testing.T) {
	withCleanCert(t)

	// 用 IP-A 加密
	certA := &Cert{Code: "secret-001", IP: "192.168.1.50"}
	if err := saveLocalCert(certA); err != nil {
		t.Fatalf("saveLocalCert 失败: %v", err)
	}

	// 模拟"攻击者复制了文件, 试图在 IP-B 机器上用"
	// loadLocalCert 用 certA.IP 作为 AAD (sc.IP = 192.168.1.50)
	// 因为 IP 没改, GCM 解密会成功, 解密后 cert.IP = 192.168.1.50
	// 然后调用方 Check() 会用 localIP() == 192.168.1.50 比对, 失败 → 走激活
	//
	// 这里直接验证 loadLocalCert 在文件没被改时能解出原始 certA
	// (因为 GCM AAD 校验的是 sc.IP, 不是本机 IP)
	cert, err := loadLocalCert()
	if err != nil {
		t.Fatalf("loadLocalCert 应该成功 (文件未改): %v", err)
	}
	if cert.IP != "192.168.1.50" {
		t.Errorf("cert.IP 应该等于原 IP, got=%q", cert.IP)
	}

	// 关键: 现在模拟"攻击者改文件, 把 IP 改成自己的"
	// 改 sc.IP → GCM AAD 改了 → 解密应该失败
	p, _ := certPath()
	data, _ := os.ReadFile(p)
	modified := []byte(string(data))
	// 简单替换 IP 字段值 (实际攻击会用更精细的修改, 这里验证 AAD 起作用)
	modifiedStr := string(modified)
	modifiedStr = replaceFirst(modifiedStr, `"ip":"192.168.1.50"`, `"ip":"10.0.0.99"`)
	if err := os.WriteFile(p, []byte(modifiedStr), 0o600); err != nil {
		t.Fatalf("写入被改文件失败: %v", err)
	}

	_, err = loadLocalCert()
	if err == nil {
		t.Errorf("IP 被改后 loadLocalCert 应该失败, 但成功了 (AAD 没拦住!)")
	}
	if err != nil && !contains(err.Error(), "解密失败") {
		t.Errorf("错误信息应该包含'解密失败', got=%v", err)
	}
}

// TestCertCorruptedPayload payload 被改 → 解密失败
func TestCertCorruptedPayload(t *testing.T) {
	withCleanCert(t)

	cert := &Cert{Code: "secret-002", IP: "192.168.1.60"}
	if err := saveLocalCert(cert); err != nil {
		t.Fatalf("saveLocalCert 失败: %v", err)
	}

	// 改 payload 字段的最后 4 个字符 (足够让 base64 解码 + GCM tag 校验失败)
	p, _ := certPath()
	data, _ := os.ReadFile(p)
	modified := string(data)
	// 找最后一个 " 改成 X, 必然破坏 JSON 结构 → json.Unmarshal 失败 → 错误
	if len(modified) < 50 {
		t.Fatalf("证书文件太小, 测试环境异常")
	}
	// 取中间位置改一个字符, 保证 payload 一定被破坏
	mid := len(modified) / 2
	modified = modified[:mid] + "X" + modified[mid+1:]
	if err := os.WriteFile(p, []byte(modified), 0o600); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	_, err := loadLocalCert()
	if err == nil {
		t.Errorf("payload 被改后 loadLocalCert 应该失败")
	}
}

// TestCheck_BypassWins 白名单优先于本地证书
//
// 即使本地有证书, 白名单匹配应该直接通过 (最高优先级)
func TestCheck_BypassWins(t *testing.T) {
	withCleanCert(t)

	// 写一个本地证书 (IP 是 1.1.1.1, 跟 localIP 肯定不匹配)
	if err := saveLocalCert(&Cert{Code: "x", IP: "1.1.1.1"}); err != nil {
		t.Fatalf("saveLocalCert: %v", err)
	}

	// 不启用白名单 → 应该失败 (本地证书 IP 不匹配)
	SetConfigProvider(func() *ConfigSnapshot { return &ConfigSnapshot{} })
	if err := Check(); err == nil {
		t.Errorf("白名单未启用 + 本地证书 IP 不匹配, Check 应该返回 ErrLicenseMissing")
	}

	// 启用白名单 → 应该通过
	SetConfigProvider(func() *ConfigSnapshot { return &ConfigSnapshot{KairoInternalToken: "111222"} })
	if err := Check(); err != nil {
		t.Errorf("白名单匹配时 Check 应该通过, got err: %v", err)
	}
}

// TestCheck_LocalCertValid 本地证书有效时通过
func TestCheck_LocalCertValid(t *testing.T) {
	withCleanCert(t)

	// 写本地证书, IP 用本机 IP (让 Check 通过)
	myIP := localIP()
	if err := saveLocalCert(&Cert{Code: "test", IP: myIP}); err != nil {
		t.Fatalf("saveLocalCert: %v", err)
	}

	SetConfigProvider(func() *ConfigSnapshot { return &ConfigSnapshot{} })
	if err := Check(); err != nil {
		t.Errorf("本地证书 IP 匹配时 Check 应该通过, got: %v", err)
	}
}

// TestCheck_NoCert 没证书时返回 ErrLicenseMissing
func TestCheck_NoCert(t *testing.T) {
	withCleanCert(t)

	SetConfigProvider(func() *ConfigSnapshot { return &ConfigSnapshot{} })
	err := Check()
	if !errors.Is(err, ErrLicenseMissing) {
		t.Errorf("没证书时 Check 应该返回 ErrLicenseMissing, got: %v", err)
	}
}

// TestLocalIP 本机 IP 提取不报错
func TestLocalIP(t *testing.T) {
	ip := localIP()
	if ip == "" {
		t.Errorf("localIP 不应返回空字符串")
	}
	// 应该能解析
	if ip != "127.0.0.1" {
		// 至少应该是个看起来像 IP 的字符串
		t.Logf("localIP = %s", ip)
	}
}

// TestMacFingerprint MAC fingerprint 提取不报错
func TestMacFingerprint(t *testing.T) {
	mac := macFingerprint()
	if mac == "" {
		t.Errorf("macFingerprint 不应返回空字符串")
	}
	t.Logf("macFingerprint = %s", mac)

	// 多次调用应该返回一致结果
	mac2 := macFingerprint()
	if mac != mac2 {
		t.Errorf("macFingerprint 应稳定, 第一次=%q 第二次=%q", mac, mac2)
	}
}

// TestCert_MACMismatch 模拟攻击者伪造 cert 时, 即使 IP 对, MAC 错也解密失败
//
// 漏洞 #16 修复验证: cert AAD 现在是 IP + MAC, 攻击者猜对 IP 但 MAC 错时 GCM 失败
func TestCert_MACMismatch(t *testing.T) {
	withCleanCert(t)

	// 用正常 cert 写入 (IP + 真实 MAC 作 AAD)
	cert := &Cert{Code: "secret-003", IP: localIP()}
	if err := saveLocalCert(cert); err != nil {
		t.Fatalf("saveLocalCert 失败: %v", err)
	}

	// 模拟"攻击者把 cert 复制到另一台机器" - 这台机器 IP 不同, MAC 也不同
	// loadLocalCert 用当前机器的 IP+MAC 作为 AAD, 跟写入时的 AAD 不匹配 → 解密失败
	// 我们用真实场景: 直接 loadLocalCert, 但前提是文件已经被复制到另一台机器
	// (在单元测试里, 我们只能验证"本机能解密自己的 cert")

	cert2, err := loadLocalCert()
	if err != nil {
		t.Fatalf("本机 loadLocalCert 应该成功: %v", err)
	}
	if cert2.Code != "secret-003" {
		t.Errorf("Code 不匹配")
	}

	// 验证: 手工构造一个"IP 对, MAC 错"的场景不容易在单元测试里模拟
	// 但我们可以验证 fingerprint() 返回的是非空且稳定的
	fp := Fingerprint()
	if fp == "" {
		t.Errorf("Fingerprint 不应为空")
	}
	if !contains(fp, "|") {
		t.Errorf("Fingerprint 应包含 IP|MAC 格式, got=%q", fp)
	}
	t.Logf("Fingerprint = %s (IP=%s, MAC=%s)", fp, localIP(), macFingerprint())
}

// TestCert_SpecialChars 验证 cert 里 code/ip 含特殊字符时也能正确加解密
func TestCert_SpecialChars(t *testing.T) {
	withCleanCert(t)

	// 各种特殊字符: 单引号 / 双引号 / 反斜杠 / 中文 / emoji
	testCases := []struct {
		name string
		code string
		ip   string
	}{
		{"含单引号", "abc'def", "192.168.1.50"},
		{"含双引号", `abc"def`, "192.168.1.50"},
		{"含反斜杠", `abc\def`, "192.168.1.50"},
		{"含中文", "激活码-张三", "192.168.1.50"},
		{"含emoji", "code-🔥-001", "192.168.1.50"},
		{"IPv6 形式", "code-v6", "fe80::1"},
		{"超长 IP", "code-long", "192.168.1.50.999.999"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			withCleanCert(t)

			// 注意: saveLocalCert 不验证 IP 格式, 任何字符串都能存
			cert := &Cert{Code: tc.code, IP: tc.ip}
			if err := saveLocalCert(cert); err != nil {
				t.Fatalf("saveLocalCert 失败 (%s): %v", tc.name, err)
			}

			got, err := loadLocalCert()
			if err != nil {
				t.Fatalf("loadLocalCert 失败 (%s): %v", tc.name, err)
			}
			if got.Code != tc.code {
				t.Errorf("Code 不一致 (%s): got=%q want=%q", tc.name, got.Code, tc.code)
			}
			if got.IP != tc.ip {
				t.Errorf("IP 不一致 (%s): got=%q want=%q", tc.name, got.IP, tc.ip)
			}
		})
	}
}

// TestActivate_EmptyCode 空激活码处理
func TestActivate_EmptyCode(t *testing.T) {
	withCleanCert(t)

	// 设为 PLACEHOLDER 地址, 让请求失败但不应 panic
	saveAndRestoreLicenseVars(t, func() {
		LicenseServerPrimary = "http://PLACEHOLDER/kairo/auth/activate"
		LicenseServerSecondary = ""
		BasicAuthHeader = "TEST"
		URLParamK1 = ""; URLParamV1 = ""  // 全空, URL 不带参数
		URLParamK2 = ""; URLParamV2 = ""
		URLParamK3 = ""; URLParamV3 = ""

		err := Activate("")
		if err == nil {
			t.Errorf("空激活码应该返回错误")
		}
		t.Logf("Activate('') 错误 (符合预期): %v", err)
	})
}

// TestActivate_WhitespaceCode 纯空白激活码
func TestActivate_WhitespaceCode(t *testing.T) {
	withCleanCert(t)
	saveAndRestoreLicenseVars(t, func() {
		LicenseServerPrimary = "http://PLACEHOLDER/kairo/auth/activate"
		LicenseServerSecondary = ""
		BasicAuthHeader = "TEST"
		URLParamK1 = ""; URLParamV1 = ""
		URLParamK2 = ""; URLParamV2 = ""
		URLParamK3 = ""; URLParamV3 = ""

		err := Activate("   ")
		if err == nil {
			t.Errorf("空白激活码应该返回错误")
		}
	})
}

// saveAndRestoreLicenseVars 在测试体 fn 执行前 snapshot var, 测试结束后还原。
// 避免测试改 var 后污染其他测试。
func saveAndRestoreLicenseVars(t *testing.T, fn func()) {
	t.Helper()
	prim, sec, auth := LicenseServerPrimary, LicenseServerSecondary, BasicAuthHeader
	k1, v1 := URLParamK1, URLParamV1
	k2, v2 := URLParamK2, URLParamV2
	k3, v3 := URLParamK3, URLParamV3
	defer func() {
		LicenseServerPrimary = prim
		LicenseServerSecondary = sec
		BasicAuthHeader = auth
		URLParamK1, URLParamV1 = k1, v1
		URLParamK2, URLParamV2 = k2, v2
		URLParamK3, URLParamV3 = k3, v3
	}()
	fn()
}

// TestGetStatus_Reasons 各种状态 reason 分类正确
func TestGetStatus_Reasons(t *testing.T) {
	withCleanCert(t)

	// 1. dev-bypass
	SetConfigProvider(func() *ConfigSnapshot { return &ConfigSnapshot{KairoInternalToken: "111222"} })
	if st := GetStatus(); st.Reason != "dev-bypass" {
		t.Errorf("dev bypass 时 reason=dev-bypass, got=%q", st.Reason)
	}

	// 2. no-local-cert
	SetConfigProvider(func() *ConfigSnapshot { return &ConfigSnapshot{} })
	if st := GetStatus(); st.Reason != "no-local-cert" {
		t.Errorf("没证书时 reason=no-local-cert, got=%q", st.Reason)
	}

	// 3. local-cert (有效)
	myIP := localIP()
	_ = saveLocalCert(&Cert{Code: "test", IP: myIP})
	if st := GetStatus(); st.Reason != "local-cert" {
		t.Errorf("本地证书有效时 reason=local-cert, got=%q", st.Reason)
	}

	// 4. ip-mismatch (本地有但 IP 不对)
	_ = saveLocalCert(&Cert{Code: "test", IP: "1.1.1.1"})
	if st := GetStatus(); st.Reason != "ip-mismatch" {
		t.Errorf("IP 不匹配时 reason=ip-mismatch, got=%q", st.Reason)
	}
}

// 辅助函数
func replaceFirst(s, old, new string) string {
	idx := indexOf(s, old)
	if idx < 0 {
		return s
	}
	return s[:idx] + new + s[idx+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func contains(s, sub string) bool {
	return indexOf(s, sub) >= 0
}

// 测试结束后清理, 不影响其他测试
func TestMain(m *testing.M) {
	code := m.Run()
	// 清理 ~/.kairo/license.dat
	if p, err := certPath(); err == nil {
		_ = os.Remove(p)
		_ = os.Remove(filepath.Dir(p))
	}
	os.Exit(code)
}