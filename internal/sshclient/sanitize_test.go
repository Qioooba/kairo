package sshclient

import (
	"strings"
	"testing"
)

func TestSanitizeError(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain", "ssh: handshake failed: ssh: unable to authenticate, attempted methods [none ***], no supported methods remain"},
		{"Password=ops", "***=ops"},
		// secret 本身也是敏感词，所以两段都被遮蔽
		{"passwd=secret", "***=***"},
		{"Authorization: Bearer x", "***: Bearer x"},
		{"privateKey loaded", "*** loaded"},
		{"普通错误无敏感词", "普通错误无敏感词"},
		{"", ""},
	}
	for _, c := range cases {
		got := SanitizeError(c.in)
		if got != c.want {
			t.Errorf("SanitizeError(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 真实值（不是敏感词）应该原样保留，避免运维信息丢失
	if got := SanitizeError("got password=ops user=admin"); !strings.Contains(got, "ops") {
		t.Errorf("非敏感值被误伤: %s", got)
	}
	if got := SanitizeError("got password=ops user=admin"); !strings.Contains(got, "***") {
		t.Errorf("password 没脱敏: %s", got)
	}
}
