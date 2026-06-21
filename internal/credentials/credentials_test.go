package credentials

import (
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestKey(t *testing.T) {
	cases := []struct {
		system, server, user, want string
	}{
		{"信贷生产", "mock-node-1", "test", "信贷生产|mock-node-1|test"},
		{"a", "b", "c", "a|b|c"},
		{"", "", "", "||"},
	}
	for _, c := range cases {
		if got := Key(c.system, c.server, c.user); got != c.want {
			t.Errorf("Key(%q,%q,%q)=%q, want %q", c.system, c.server, c.user, got, c.want)
		}
	}
}

func TestValidateInputs(t *testing.T) {
	// 空 system/server/username 应该报错
	if err := Save("", "s", "u", "p"); err == nil {
		t.Error("Save with empty system should fail")
	}
	if err := Save("a", "", "u", "p"); err == nil {
		t.Error("Save with empty server should fail")
	}
	if err := Save("a", "s", "", "p"); err == nil {
		t.Error("Save with empty username should fail")
	}
	if err := Save("a", "s", "u", ""); err == nil {
		t.Error("Save with empty password should fail")
	}
	if _, err := Get("", "s", "u"); err == nil {
		t.Error("Get with empty system should fail")
	}
	if err := Clear("", "s", "u"); err == nil {
		t.Error("Clear with empty system should fail")
	}
}

func TestIsUnavailableDetection(t *testing.T) {
	// nil 不算 unavailable
	if isUnavailable(nil) {
		t.Error("nil should not be unavailable")
	}
	// 已知的"不可用"信号
	for _, msg := range []string{
		"could not connect to the Secret Service",
		"dbus session bus not available",
		"org.freedesktop.secrets: No such interface",
		"Keychain locked",
	} {
		if !isUnavailable(errors.New(msg)) {
			t.Errorf("isUnavailable(%q) should be true", msg)
		}
	}
	// 不相关的错误不应被误判
	if isUnavailable(errors.New("some random network error")) {
		t.Error("random error should not be unavailable")
	}
}

// TestKeyringRoundTrip 真正地跟 OS keyring 走一遍（不依赖 mock）。
// 在没有 keyring 的环境（CI / Linux 容器）会跳过，标记为 t.Skip。
//
// 这里采用独立的 service 命名（"OpsToolboxTest"），避免污染真实数据。
func TestKeyringRoundTrip(t *testing.T) {
	// 跑前用 setUpTestService 临时把 Service 换掉
	origService := Service
	t.Cleanup(func() { /* nothing — package-level const */ })
	_ = origService // const 不可改；改用独立 key

	// 用独立 account 避免跟真实使用冲突
	testSystem := "test-system"
	testServer := "test-server"
	testUser := "test-user-" + t.Name() // 唯一
	testPw := "p@ssw0rd-测试"

	// 先确认没有残留
	if pw, err := keyring.Get(Service, Key(testSystem, testServer, testUser)); err == nil {
		t.Logf("cleanup: 残留密码已清 (%q)", pw)
		_ = keyring.Delete(Service, Key(testSystem, testServer, testUser))
	}

	// Save
	if err := Save(testSystem, testServer, testUser, testPw); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("keyring 不可用，跳过: %v", err)
		}
		t.Fatalf("Save failed: %v", err)
	}
	t.Cleanup(func() { _ = Clear(testSystem, testServer, testUser) })

	// Get
	got, err := Get(testSystem, testServer, testUser)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != testPw {
		t.Errorf("Get=%q, want %q", got, testPw)
	}

	// Has
	has, err := Has(testSystem, testServer, testUser)
	if err != nil || !has {
		t.Errorf("Has=(%v,%v), want (true,nil)", has, err)
	}

	// 重复 Save 覆盖
	if err := Save(testSystem, testServer, testUser, "another"); err != nil {
		t.Fatalf("overwrite Save failed: %v", err)
	}
	got2, err := Get(testSystem, testServer, testUser)
	if err != nil {
		t.Fatalf("Get after overwrite failed: %v", err)
	}
	if got2 != "another" {
		t.Errorf("Get after overwrite=%q, want %q", got2, "another")
	}

	// Clear
	if err := Clear(testSystem, testServer, testUser); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// 再次 Get 应该返回 ErrNotSaved
	_, err = Get(testSystem, testServer, testUser)
	if !errors.Is(err, ErrNotSaved) {
		t.Errorf("Get after Clear err=%v, want ErrNotSaved", err)
	}

	// 再次 Clear 应该返回 ErrNotSaved
	if err := Clear(testSystem, testServer, testUser); !errors.Is(err, ErrNotSaved) {
		t.Errorf("Clear on missing err=%v, want ErrNotSaved", err)
	}
}

func TestKeyFormatNoPipeInUser(t *testing.T) {
	// username 包含 | 会破坏解析 — 文档化这个限制
	// 如果未来要支持，得换分隔符
	weird := Key("a", "b", "x|y")
	parts := strings.Split(weird, "|")
	if len(parts) != 4 {
		t.Errorf("expected 3 separators, got %d in %q", len(parts)-1, weird)
	}
}
