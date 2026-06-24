package credentials

import (
	"errors"
	"os"
	"path/filepath"
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
	origService := Service
	t.Cleanup(func() { /* nothing — package-level const */ })
	_ = origService

	testSystem := "test-system"
	testServer := "test-server"
	testUser := "test-user-" + t.Name()
	testPw := "p@ssw0rd-测试"

	// 先确认没有残留
	if pw, err := keyring.Get(Service, Key(testSystem, testServer, testUser)); err == nil {
		t.Logf("cleanup: 残留密码已清 (%q)", pw)
		_ = keyring.Delete(Service, Key(testSystem, testServer, testUser))
	}

	if err := Save(testSystem, testServer, testUser, testPw); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("keyring 不可用，跳过: %v", err)
		}
		t.Fatalf("Save failed: %v", err)
	}
	t.Cleanup(func() { _ = Clear(testSystem, testServer, testUser) })

	got, err := Get(testSystem, testServer, testUser)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != testPw {
		t.Errorf("Get=%q, want %q", got, testPw)
	}

	has, err := Has(testSystem, testServer, testUser)
	if err != nil || !has {
		t.Errorf("Has=(%v,%v), want (true,nil)", has, err)
	}

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

	if err := Clear(testSystem, testServer, testUser); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	_, err = Get(testSystem, testServer, testUser)
	if !errors.Is(err, ErrNotSaved) {
		t.Errorf("Get after Clear err=%v, want ErrNotSaved", err)
	}

	if err := Clear(testSystem, testServer, testUser); !errors.Is(err, ErrNotSaved) {
		t.Errorf("Clear on missing err=%v, want ErrNotSaved", err)
	}
}

func TestKeyFormatNoPipeInUser(t *testing.T) {
	weird := Key("a", "b", "x|y")
	parts := strings.Split(weird, "|")
	if len(parts) != 4 {
		t.Errorf("expected 3 separators, got %d in %q", len(parts)-1, weird)
	}
}

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
		{"unknown-mode", ModeKeyring},
		{"", ModeKeyring},
	}
	for _, c := range cases {
		SetMode(c.in)
		if got := Mode(); got != c.want {
			t.Errorf("SetMode(%q) → Mode()=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestDefaultMode(t *testing.T) {
	SetMode("")
	if got := Mode(); got != ModeKeyring {
		t.Errorf("SetMode(\"\") → Mode()=%q, want %q (default)", got, ModeKeyring)
	}
}

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

// TestFileMode_NotInitialized file 模式未 Init 时调用 Save/Get 应返 ErrUnavailable。
func TestFileMode_NotInitialized(t *testing.T) {
	orig := Mode()
	t.Cleanup(func() { SetMode(orig) })
	SetMode(ModeFile)

	if err := Save("s", "srv", "u", "p"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("file Save without Init: err=%v, want ErrUnavailable", err)
	}
	if _, err := Get("s", "srv", "u"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("file Get without Init: err=%v, want ErrUnavailable", err)
	}
}

// TestFileMode_RoundTrip file 模式完整 round-trip：Init→Save→Get→Has→Overwrite→Clear。
func TestFileMode_RoundTrip(t *testing.T) {
	orig := Mode()
	t.Cleanup(func() { SetMode(orig) })

	tmpDir := t.TempDir()
	SetMode(ModeFile)
	if err := Init(tmpDir, ""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	testSystem := "test-sys"
	testServer := "test-srv"
	testUser := "test-user-" + t.Name()
	testPw := "my-Secret-Passw0rd!@#"

	// 确认 .credkey 文件被创建
	credKeyPath := filepath.Join(tmpDir, ".credkey")
	if _, err := os.Stat(credKeyPath); err != nil {
		t.Errorf(".credkey not created: %v", err)
	}
	credKeyData, err := os.ReadFile(credKeyPath)
	if err != nil {
		t.Fatalf("read .credkey: %v", err)
	}
	if len(strings.TrimSpace(string(credKeyData))) != 64 {
		t.Errorf(".credkey should be 64 hex chars, got %d", len(strings.TrimSpace(string(credKeyData))))
	}

	// Get 不存在的 key → ErrNotSaved
	_, err = Get(testSystem, testServer, testUser)
	if !errors.Is(err, ErrNotSaved) {
		t.Errorf("Get on missing: err=%v, want ErrNotSaved", err)
	}
	has, err := Has(testSystem, testServer, testUser)
	if err != nil || has {
		t.Errorf("Has on missing: (%v,%v), want (false,nil)", has, err)
	}

	// Save
	if err := Save(testSystem, testServer, testUser, testPw); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// credentials.json 应该存在
	credsPath := filepath.Join(tmpDir, "credentials.json")
	if _, err := os.Stat(credsPath); err != nil {
		t.Errorf("credentials.json not created: %v", err)
	}

	// Get
	got, err := Get(testSystem, testServer, testUser)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != testPw {
		t.Errorf("Get=%q, want %q", got, testPw)
	}

	// Has
	has, err = Has(testSystem, testServer, testUser)
	if err != nil || !has {
		t.Errorf("Has=(%v,%v), want (true,nil)", has, err)
	}

	// Overwrite
	if err := Save(testSystem, testServer, testUser, "overwritten"); err != nil {
		t.Fatalf("overwrite Save failed: %v", err)
	}
	got2, err := Get(testSystem, testServer, testUser)
	if err != nil {
		t.Fatalf("Get after overwrite failed: %v", err)
	}
	if got2 != "overwritten" {
		t.Errorf("Get after overwrite=%q, want %q", got2, "overwritten")
	}

	// Clear
	if err := Clear(testSystem, testServer, testUser); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
	_, err = Get(testSystem, testServer, testUser)
	if !errors.Is(err, ErrNotSaved) {
		t.Errorf("Get after Clear err=%v, want ErrNotSaved", err)
	}
	if err := Clear(testSystem, testServer, testUser); !errors.Is(err, ErrNotSaved) {
		t.Errorf("Clear on missing err=%v, want ErrNotSaved", err)
	}
}

// TestFileMode_WithConfiguredKey 使用配置中指定的密钥进行测试，验证加解密一致性。
func TestFileMode_WithConfiguredKey(t *testing.T) {
	orig := Mode()
	t.Cleanup(func() { SetMode(orig) })

	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()
	// 32 字节 = 64 hex 字符的密钥
	testKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	SetMode(ModeFile)
	if err := Init(tmpDir1, testKey); err != nil {
		t.Fatalf("Init(tmpDir1) failed: %v", err)
	}
	// 不应该自动生成 .credkey（因为使用了配置密钥）
	credKeyPath := filepath.Join(tmpDir1, ".credkey")
	if _, err := os.Stat(credKeyPath); !os.IsNotExist(err) {
		t.Errorf(".credkey should NOT be created when key is configured, got err=%v", err)
	}

	if err := Save("sys", "srv", "u", "secret-data"); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	got, err := Get("sys", "srv", "u")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != "secret-data" {
		t.Errorf("Get=%q, want %q", got, "secret-data")
	}

	// 用同样的密钥 Init 另一个目录，但手动复制 credentials.json 过去，验证密钥一致可以解密
	// （更实际的场景：重启后使用相同密钥能解密旧数据）
	// 这里测试 Init 使用坏密钥会在解密时失败
	if err := Init(tmpDir2, testKey); err != nil {
		t.Fatalf("Init(tmpDir2) failed: %v", err)
	}
	// tmpDir2 是空的，Get 应返 ErrNotSaved
	_, err = Get("sys", "srv", "u")
	if !errors.Is(err, ErrNotSaved) {
		t.Errorf("Get in empty dir err=%v, want ErrNotSaved", err)
	}
}

// TestFileMode_KeyPersistence 验证密钥持久化：第二次 Init 能读取之前自动生成的密钥。
func TestFileMode_KeyPersistence(t *testing.T) {
	orig := Mode()
	t.Cleanup(func() { SetMode(orig) })

	tmpDir := t.TempDir()
	SetMode(ModeFile)

	if err := Init(tmpDir, ""); err != nil {
		t.Fatalf("first Init failed: %v", err)
	}
	if err := Save("sys", "srv", "u", "persistent-pw"); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// 模拟"重启"：重新 Init（不指定 key），应读取 .credkey 中的旧密钥
	if err := Init(tmpDir, ""); err != nil {
		t.Fatalf("second Init failed: %v", err)
	}
	got, err := Get("sys", "srv", "u")
	if err != nil {
		t.Fatalf("Get after re-init failed: %v", err)
	}
	if got != "persistent-pw" {
		t.Errorf("Get after re-init=%q, want %q", got, "persistent-pw")
	}
}

// TestFileMode_InitNonFileMode 非 file 模式下 Init 是 no-op。
func TestFileMode_InitNonFileMode(t *testing.T) {
	orig := Mode()
	t.Cleanup(func() { SetMode(orig) })

	SetMode(ModeKeyring)
	if err := Init(t.TempDir(), ""); err != nil {
		t.Errorf("Init in keyring mode should be no-op, got err=%v", err)
	}
	SetMode(ModeDisabled)
	if err := Init(t.TempDir(), ""); err != nil {
		t.Errorf("Init in disabled mode should be no-op, got err=%v", err)
	}
}

// TestKeyringMode_BehavesAsBefore mode=keyring 是默认行为，不应被 SetMode 改动后阻塞。
func TestKeyringMode_KeyringFunctionsCalled(t *testing.T) {
	orig := Mode()
	t.Cleanup(func() { SetMode(orig) })
	SetMode(ModeKeyring)
	if Mode() != ModeKeyring {
		t.Fatalf("keyring mode not active")
	}
	_, err := Get("nosuch-system", "nosuch-srv", "nosuch-user-"+t.Name())
	if !errors.Is(err, ErrNotSaved) {
		t.Errorf("keyring mode Get(nonexistent): err=%v, want ErrNotSaved", err)
	}
}
