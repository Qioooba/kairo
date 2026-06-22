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

// TestSetMode 验证 SetMode 接受合法值并拒绝未知值（项 23）。
//
// 顺序：保存原 mode → 多次 SetMode → 恢复原 mode，避免影响别的 test。
func TestSetMode(t *testing.T) {
	orig := Mode()
	t.Cleanup(func() { SetMode(orig) })

	cases := []struct {
		in, want string
	}{
		{"keyring", ModeKeyring},
		{"KEYRING", ModeKeyring},
		{"  keyring  ", ModeKeyring},
		{"file", ModeFile},
		{"FILE", ModeFile},
		{"disabled", ModeDisabled},
		{"DISABLED", ModeDisabled},
		{"off", ModeDisabled},
		{"none", ModeDisabled},
		{"unknown-mode", ModeKeyring}, // 未知值兜底为 keyring
		{"", ModeKeyring},
	}
	for _, c := range cases {
		SetMode(c.in)
		if got := Mode(); got != c.want {
			t.Errorf("SetMode(%q) → Mode()=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestDefaultMode 默认 mode 应该是 keyring（向后兼容）。
func TestDefaultMode(t *testing.T) {
	// 在所有其它测试跑完之后验证默认值 — 但 Go test 顺序不固定，
	// 所以这里只断言"某个测试结束后 mode 被还原为 keyring"是脆弱的。
	// 退一步：只验证 SetMode("") 的兜底行为。
	SetMode("")
	if got := Mode(); got != ModeKeyring {
		t.Errorf("SetMode(\"\") → Mode()=%q, want %q (default)", got, ModeKeyring)
	}
}

// TestDisabledMode_BlocksAllOps mode=disabled 时所有 Save/Get/Has/Clear 都返 ErrUnavailable。
//
// 这里不需要真实 keyring（disabled 模式根本不访问 keyring），
// 所以即使在无 keyring 的 CI 容器里也能稳定 pass。
func TestDisabledMode_BlocksAllOps(t *testing.T) {
	orig := Mode()
	t.Cleanup(func() { SetMode(orig) })
	SetMode(ModeDisabled)

	if err := Save("s", "srv", "u", "p"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("disabled Save: err=%v, want ErrUnavailable", err)
	}
	if _, err := Get("s", "srv", "u"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("disabled Get: err=%v, want ErrUnavailable", err)
	}
	if _, err := Has("s", "srv", "u"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("disabled Has: err=%v, want ErrUnavailable", err)
	}
	if err := Clear("s", "srv", "u"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("disabled Clear: err=%v, want ErrUnavailable", err)
	}
}

// TestFileMode_NotImplemented mode=file 时返 ErrFileNotImplemented（暂时未实现加密文件）。
//
// 设计取舍：与其静默写一个未加密文件（泄密），不如明说"暂未实现"，
// 让运维用户知道要么回退到 keyring、要么明确禁用。
func TestFileMode_NotImplemented(t *testing.T) {
	orig := Mode()
	t.Cleanup(func() { SetMode(orig) })
	SetMode(ModeFile)

	if err := Save("s", "srv", "u", "p"); !errors.Is(err, ErrFileNotImplemented) {
		t.Errorf("file Save: err=%v, want ErrFileNotImplemented", err)
	}
	if _, err := Get("s", "srv", "u"); !errors.Is(err, ErrFileNotImplemented) {
		t.Errorf("file Get: err=%v, want ErrFileNotImplemented", err)
	}
}

// TestKeyringMode_BehavesAsBefore mode=keyring 是默认行为，不应被 SetMode 改动后阻塞。
//
// 避免误改 Save/Get 的情况下让真实 keyring round-trip 跑挂。
func TestKeyringMode_KeyringFunctionsCalled(t *testing.T) {
	orig := Mode()
	t.Cleanup(func() { SetMode(orig) })
	SetMode(ModeKeyring)
	if Mode() != ModeKeyring {
		t.Fatalf("keyring mode not active")
	}
	// 真实 round-trip 由 TestKeyringRoundTrip 覆盖；这里只断言 mode 切换不会
	// 让 guardMode 拦下请求：用一个不存在的 key 调 Get，应返 ErrNotSaved，
	// 不是 ErrUnavailable / ErrFileNotImplemented。
	_, err := Get("nosuch-system", "nosuch-srv", "nosuch-user-"+t.Name())
	if !errors.Is(err, ErrNotSaved) {
		t.Errorf("keyring mode Get(nonexistent): err=%v, want ErrNotSaved", err)
	}
}
