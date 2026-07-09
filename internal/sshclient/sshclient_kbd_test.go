package sshclient

import (
	"reflect"
	"testing"
)

// TestPasswordKeyboardInteractive 覆盖真实堡垒机/PAM/OTP 场景下的
// keyboard-interactive challenge 回调逻辑（v0.14 修复）。
//
// 历史 bug：v0.14 之前用 `singleQuestion || isEchoFalse || isPassword || allEchoFalse`
// 作为"是否回密码"的判断，单 question 的 echo=true 非密码 prompt
// （如 "Press ENTER to continue: " / "Verification code: "）
// 会被 `singleQuestion=true` 短路，回填 password，触发 server 校验失败。
// 典型表现：连接 66.0.248.118（只开 keyboard-interactive 的堡垒机）报
// "attempted methods [none keyboard-interactive], no supported methods remain"。
//
// 修复后规则：
//   - echo=false → 一律回密码
//   - echo=true  +  prompt 文本含 password/密码/口令/pass/otp 等密码类词 → 回密码
//   - echo=true  +  非密码类 → 填空字符串
func TestPasswordKeyboardInteractive(t *testing.T) {
	const password = "secret"

	type tc struct {
		name      string
		questions []string
		echos     []bool
		want      []string
	}

	cases := []tc{
		{
			name:      "1 prompt echo=false Password",
			questions: []string{"Password: "},
			echos:     []bool{false},
			want:      []string{password},
		},
		{
			name:      "1 prompt echo=true Verification code (non-password)",
			questions: []string{"Verification code: "},
			echos:     []bool{true},
			want:      []string{""}, // 之前 v0.14 bug 会回 password
		},
		{
			name:      "1 prompt echo=true Press ENTER to continue",
			questions: []string{"Press ENTER to continue: "},
			echos:     []bool{true},
			want:      []string{""}, // 之前 v0.14 bug 会回 password
		},
		{
			name:      "1 prompt echo=true but text contains Password",
			questions: []string{"Password: "},
			echos:     []bool{true},
			want:      []string{password}, // 奇葩：echo=true 的密码 prompt
		},
		{
			name:      "2 prompts: login echo=true + Password echo=false",
			questions: []string{"login: ", "Password: "},
			echos:     []bool{true, false},
			want:      []string{"", password}, // SSH USERAUTH user 字段传 username，answers[0] 填空
		},
		{
			name:      "2 prompts: both echo=false (typical PAM)",
			questions: []string{"Password: ", "Token: "},
			echos:     []bool{false, false},
			want:      []string{password, password},
		},
		{
			name:      "1 prompt echo=false with Chinese 密码",
			questions: []string{"请输入密码: "},
			echos:     []bool{false},
			want:      []string{password},
		},
		{
			name:      "1 prompt echo=true with Chinese 确认 (non-password)",
			questions: []string{"请确认(y/n): "},
			echos:     []bool{true},
			want:      []string{""},
		},
		{
			name:      "0 prompts (server 不发 prompt) — 边界",
			questions: []string{},
			echos:     []bool{},
			want:      []string{},
		},
		// "pass" 不再作为独立密码 token（太短，容易误中非密码提示）
		{
			name:      "1 prompt echo=true contains 'pass' alone (NOT a password token)",
			questions: []string{"Enter your pass: "},
			echos:     []bool{true},
			want:      []string{""}, // "pass" 不在 passwordTokens 里
		},
		{
			name:      "1 prompt echo=true contains 'pass' but in 'passwordless' (NOT a token)",
			questions: []string{"Use passwordless login? "},
			echos:     []bool{true},
			want:      []string{""}, // "passwordless" 不应被视作密码 prompt
		},
	}

	cb := passwordKeyboardInteractive(password)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := cb("test", "", c.questions, c.echos)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("answers mismatch:\n  got:  %#v\n  want: %#v", got, c.want)
			}
		})
	}
}
