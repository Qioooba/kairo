package httpserver

import "testing"

// TestShouldFallbackToPOSIX 验证 list_mode=auto 时降级到 posix_ls 的判定逻辑。
//
// 触发条件：远端 exit != 0 + stderr 含典型"命令不存在 / 选项不支持"关键字。
// 不触发：exit==0、err!=nil（网络类错）、普通权限不足（再试也是同个错）。
func TestShouldFallbackToPOSIX(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		stderr string
		code   int
		err    error
		want   bool
	}{
		{name: "exit 0 不降级", stdout: "12345\t1700000000\ta.log", code: 0, want: false},
		{name: "无 stderr 但 exit 非 0 不降级（避免误触发）", stdout: "", code: 1, want: false},
		{
			name:   "find: not found → 降级",
			stdout: "",
			stderr: "sh: find: not found",
			code:   127,
			want:   true,
		},
		{
			name:   "find: 'paths must precede expression' → 降级",
			stdout: "",
			stderr: "find: paths must precede expression: .",
			code:   1,
			want:   true,
		},
		{
			name:   "AIX find unknown option → 降级",
			stdout: "",
			stderr: "find: 0652-018 An expression term lacks a required parameter",
			code:   1,
			want:   true,
		},
		{
			name:   "command not found → 降级",
			stdout: "",
			stderr: "/bin/sh: command not found: find",
			code:   127,
			want:   true,
		},
		{
			name:   "Permission denied 不降级（posix_ls 也是同个错）",
			stdout: "",
			stderr: "find: .: Permission denied",
			code:   1,
			want:   false,
		},
		{
			name:   "err != nil 不降级（网络错）",
			stdout: "",
			stderr: "",
			code:   -1,
			err:    errFakeNet,
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shouldFallbackToPOSIX(c.stdout, c.stderr, c.code, c.err)
			if got != c.want {
				t.Errorf("want %v, got %v", c.want, got)
			}
		})
	}
}

// errFakeNet 是测试用的占位 err，避免 import net 包。
var errFakeNet = &fakeErr{msg: "fake network error"}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }
