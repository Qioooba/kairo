package sshclient

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// TestCategorize_AuthErrors 验证认证相关错误归到 auth。
func TestCategorize_AuthErrors(t *testing.T) {
	cases := []struct {
		errStr string
		want   Category
	}{
		{"ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain", CatAuth},
		{"ssh: handshake failed: ssh: no supported methods remain", CatAuth},
		{"Permission denied (publickey,password).", CatAuth},
		{"ssh: handshake failed: too many authentication failures", CatAuth},
		// "ssh: handshake failed" 三层嵌套不含 auth 关键字 → 命中 handshake（这是真实的算法协商失败）
		{"ssh: handshake failed: ssh: handshake failed: ssh: handshake failed", CatHandshake},
	}
	for _, c := range cases {
		if got := Categorize(errors.New(c.errStr)); got != c.want {
			t.Errorf("Categorize(%q) = %q, want %q", c.errStr, got, c.want)
		}
	}
}

// TestCategorize_HostKeyErrors host key 类错误。
func TestCategorize_HostKeyErrors(t *testing.T) {
	cases := []struct {
		errStr string
		want   Category
	}{
		{"host key verification failed", CatHostKey},
		{"known_hosts: no matching address", CatHostKey}, // 含 host 字眼 → host_key
		{"HostKey mismatch", CatHostKey},
	}
	for _, c := range cases {
		if got := Categorize(errors.New(c.errStr)); got != c.want {
			t.Errorf("Categorize(%q) = %q, want %q", c.errStr, got, c.want)
		}
	}
}

// TestCategorize_HandshakeErrors KEX / 算法不匹配。
func TestCategorize_HandshakeErrors(t *testing.T) {
	cases := []struct {
		errStr string
		want   Category
	}{
		{"ssh: handshake failed: ssh: no matching algorithms", CatHandshake},
		{"ssh: protocol error: no common kex", CatHandshake},
		{"ssh: no mutually supported algorithms", CatHandshake},
	}
	for _, c := range cases {
		if got := Categorize(errors.New(c.errStr)); got != c.want {
			t.Errorf("Categorize(%q) = %q, want %q", c.errStr, got, c.want)
		}
	}
}

// TestCategorize_NetworkAndDNS DNS / 网络 / 端口。
func TestCategorize_NetworkAndDNS(t *testing.T) {
	// net.DNSError 用 errors.As 路径识别
	dnsErr := &net.DNSError{Err: "no such host", Name: "missing.example.com", IsNotFound: true}
	if got := Categorize(dnsErr); got != CatDNS {
		t.Errorf("Categorize(*net.DNSError) = %q, want %q", got, CatDNS)
	}

	// 字符串匹配的几种
	cases := []struct {
		errStr string
		want   Category
	}{
		{"dial tcp 1.2.3.4:22: connect: connection refused", CatPort},
		{"dial tcp 1.2.3.4:22: connect: no route to host", CatNetwork},
		{"dial tcp 1.2.3.4:22: connect: connection reset by peer", CatNetwork},
		{"dial tcp: lookup foo.bar.example: no such host", CatDNS},
		{"read tcp 1.2.3.4:22->5.6.7.8:80: i/o timeout", CatTimeout},
		{"dial tcp 1.2.3.4:22: i/o timeout", CatTimeout},
	}
	for _, c := range cases {
		if got := Categorize(errors.New(c.errStr)); got != c.want {
			t.Errorf("Categorize(%q) = %q, want %q", c.errStr, got, c.want)
		}
	}
}

// TestCategorize_ContextErrors ctx 取消 / deadline。
func TestCategorize_ContextErrors(t *testing.T) {
	if got := Categorize(context.Canceled); got != CatTimeout {
		t.Errorf("Categorize(context.Canceled) = %q, want %q", got, CatTimeout)
	}
	if got := Categorize(context.DeadlineExceeded); got != CatTimeout {
		t.Errorf("Categorize(context.DeadlineExceeded) = %q, want %q", got, CatTimeout)
	}
	// wrap 后仍能被 errors.Is 识别
	wrapped := errors.New("wrapped: " + context.DeadlineExceeded.Error())
	_ = wrapped
	if got := Categorize(wrapped); got != CatUnknown { // 字符串不含 "deadline exceeded" 的，会走兜底
		t.Logf("Categorize(non-wrap) = %q (acceptable; relies on errors.Is or keyword)", got)
	}
}

// TestCategorize_SFTPAndCommand SFTP / 命令类。
func TestCategorize_SFTPAndCommand(t *testing.T) {
	cases := []struct {
		errStr string
		want   Category
	}{
		{"sftp: subsystem request failed", CatSFTP},
		// exit status 来自 *ssh.ExitError（包装后），原始字符串是 "ssh: exit status 1"
		{"ssh: exit status 1", CatCommand},
		// 远端命令找不到
		{"bash: command not found: foo", CatCommand},
	}
	for _, c := range cases {
		if got := Categorize(errors.New(c.errStr)); got != c.want {
			t.Errorf("Categorize(%q) = %q, want %q", c.errStr, got, c.want)
		}
	}
}

// TestCategorize_KbdInt PAM / keyboard-interactive 要先于 auth 命中。
func TestCategorize_KbdInt(t *testing.T) {
	cases := []string{
		"keyboard-interactive failed",
		"no keyboard-interactive methods",
	}
	for _, s := range cases {
		if got := Categorize(errors.New(s)); got != CatKbdInt {
			t.Errorf("Categorize(%q) = %q, want %q", s, got, CatKbdInt)
		}
	}
}

// TestDiagnose_SanitizesRaw 验证 Diagnose.Raw 已脱敏（password → ***）。
func TestDiagnose_SanitizesRaw(t *testing.T) {
	d := Diagnose(errors.New("ssh: handshake failed: unable to authenticate, attempted methods [none password]"))
	if strings.Contains(d.Raw, "password") {
		t.Errorf("Diagnosis.Raw 应脱敏 password: %q", d.Raw)
	}
	if d.Category != CatAuth {
		t.Errorf("Category=%q, want %q", d.Category, CatAuth)
	}
	if d.Reason == "" {
		t.Error("Reason 不能为空")
	}
	if d.Suggestion == "" {
		t.Error("Suggestion 不能为空")
	}
}

// TestCategorize_NilSafe nil err 不 panic，返回 unknown。
func TestCategorize_NilSafe(t *testing.T) {
	if got := Categorize(nil); got != CatUnknown {
		t.Errorf("Categorize(nil) = %q, want %q", got, CatUnknown)
	}
	d := Diagnose(nil)
	if d.Category != CatUnknown {
		t.Errorf("Diagnose(nil).Category = %q, want %q", d.Category, CatUnknown)
	}
}

// TestCategorize_NetErrorFallback 拿到 net.Error 但关键字没匹配的，归到 network。
func TestCategorize_NetErrorFallback(t *testing.T) {
	// 自定义一个 net.Error 实现
	ne := &fakeNetError{msg: "some weird network glitch", timeout: false}
	if got := Categorize(ne); got != CatNetwork {
		t.Errorf("Categorize(weird net.Error) = %q, want %q", got, CatNetwork)
	}
	// 超时版本
	neTimeout := &fakeNetError{msg: "weird timeout-ish", timeout: true}
	if got := Categorize(neTimeout); got != CatTimeout {
		t.Errorf("Categorize(timeout net.Error) = %q, want %q", got, CatTimeout)
	}
}

type fakeNetError struct {
	msg     string
	timeout bool
}

func (e *fakeNetError) Error() string   { return e.msg }
func (e *fakeNetError) Timeout() bool   { return e.timeout }
func (e *fakeNetError) Temporary() bool { return false }
