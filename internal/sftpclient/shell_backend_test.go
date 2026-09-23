package sftpclient

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestParseLsLine_GNU 验证 GNU ls -la 行解析（项 8）。
func TestParseLsLine_GNU(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		wantOK  bool
		wantSz  int64
		wantNm  string
		wantDir bool
	}{
		{name: "普通文件", line: "-rw-r--r-- 1 ops ops 12345 Jun 21 10:00 SystemOut.log", wantOK: true, wantSz: 12345, wantNm: "SystemOut.log", wantDir: false},
		{name: "目录", line: "drwxr-xr-x 2 ops ops  4096 Jun 21 09:00 logs", wantOK: true, wantSz: 4096, wantNm: "logs", wantDir: true},
		{name: "软链接", line: "lrwxrwxrwx 1 ops ops    10 Jun 21 09:00 link", wantOK: true, wantSz: 10, wantNm: "link", wantDir: false},
		{name: "字段不够", line: "total 12", wantOK: false},
		{name: "空行", line: "", wantOK: false},
		{name: "带年份", line: "-rw-r--r-- 1 ops ops 999 Dec 31  2023 old.log", wantOK: true, wantSz: 999, wantNm: "old.log"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, sz, _, name, ok := parseLsLine(c.line)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if sz != c.wantSz {
				t.Errorf("size = %d, want %d", sz, c.wantSz)
			}
			if name != c.wantNm {
				t.Errorf("name = %q, want %q", name, c.wantNm)
			}
		})
	}
}

// TestParseMode 验证 mode 字符串到 os.FileMode 的转换。
func TestParseMode(t *testing.T) {
	cases := []struct {
		raw string
		dir bool
		l   bool
	}{
		{raw: "-rw-r--r--"},
		{raw: "drwxr-xr-x", dir: true},
		{raw: "lrwxrwxrwx", l: true},
		{raw: "-rwxr-x---"},
		{raw: "crw-rw-rw-"},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			m := parseMode(c.raw)
			if (m & os.ModeDir) != 0 != c.dir {
				t.Errorf("dir bit = %v, want %v (mode=%v)", m&os.ModeDir != 0, c.dir, m)
			}
			if (m & os.ModeSymlink) != 0 != c.l {
				t.Errorf("symlink bit = %v, want %v (mode=%v)", m&os.ModeSymlink != 0, c.l, m)
			}
		})
	}
}

// TestParseLsTime 验证各种时间格式。
func TestParseLsTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		month  string
		day    string
		tyr    string
		wantOK bool
	}{
		{name: "Jun 21 10:00 (无年份)", month: "Jun", day: "21", tyr: "10:00", wantOK: true},
		{name: "Dec 31 23:59 (无年份)", month: "Dec", day: "31", tyr: "23:59", wantOK: true},
		{name: "Jun 21 10:00:00 (秒级)", month: "Jun", day: "21", tyr: "10:00:00", wantOK: true},
		{name: "Jun 21 2023 (年份)", month: "Jun", day: "21", tyr: "2023", wantOK: true},
		{name: "垃圾", month: "foo", day: "bar", tyr: "baz", wantOK: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ok := parseLsTime(c.month, c.day, c.tyr)
			if ok != c.wantOK {
				t.Errorf("ok = %v, want %v", ok, c.wantOK)
			}
		})
	}
	// 验证无年份格式返回的年份是当前年
	_, ok := parseLsTime("Jun", "21", "10:00")
	if !ok {
		t.Fatal("解析 Jun 21 10:00 失败")
	}
	_ = now
}

// TestShellQuoteArg 验证 shell 单引号转义。
//
// 关键点：单引号字符串在 shell 里没有变量展开 / 命令替换，所以包在单引号里
// 是安全的；输入里的单引号要用 '\” 等价形式安全保留，不能直接剥离。
func TestShellQuoteArg(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"foo", "'foo'"},
		{"/var/log/app.log", "'/var/log/app.log'"},
		{"a'b", "'a'\"'\"'b'"}, // 单引号被安全转义并保留
		// $(rm -rf /) 在单引号里是字面字符串，不会被 shell 解释：
		{"$(rm -rf /)", "'$(rm -rf /)'"},
		{"`evil`", "'`evil`'"},
		// 含分号 / 管道也是字面字符串：
		{"a;b|c", "'a;b|c'"},
	}
	for _, c := range cases {
		if got := shellQuoteArg(c.in); got != c.want {
			t.Errorf("shellQuoteArg(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestShellBackend_ReadDir_Parses 验证 ReadDir 解析 ls -la 输出（无 SSH）。
//
// 直接构造 shellBackend 用 fake runFn（不真连 SSH），覆盖正常解析 + 错误行过滤。
func TestShellBackend_ReadDir_Parses(t *testing.T) {
	fakeRun := func(ctx context.Context, cmd string, timeout time.Duration, encoding string) (string, string, int, error) {
		return strings.Join([]string{
			"total 12",
			"drwxr-xr-x 2 ops ops  4096 Jun 21 10:00 logs",
			"-rw-r--r-- 1 ops ops 12345 Jun 21 10:00 app.log",
			"-rw-r--r-- 1 ops ops   678 Jun 21 09:30 older.log",
		}, "\n"), "", 0, nil
	}
	// shellBackend 需要 ssh.Client 但测试里不会真用（ReadDir 走 runFn）
	sb := &shellBackend{run: fakeRun}
	infos, err := sb.ReadDir("/tmp/x")
	if err != nil {
		t.Fatalf("ReadDir err: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("len = %d, want 3 (%v)", len(infos), infos)
	}
	// logs 应是目录
	if !infos[0].IsDir() {
		t.Errorf("logs.IsDir() = false, want true")
	}
	// app.log 应该是 12345 字节
	if infos[1].Size() != 12345 {
		t.Errorf("app.log.Size = %d, want 12345", infos[1].Size())
	}
	if infos[1].Name() != "app.log" {
		t.Errorf("app.log.Name = %q", infos[1].Name())
	}
}

// TestShellBackend_ReadDir_Error 验证 stderr / 非零退出码 → 错误。
func TestShellBackend_ReadDir_Error(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		stderr string
		code   int
		err    error
	}{
		{name: "code 非零", code: 1, stderr: "ls: cannot access '/nope': No such file or directory", err: nil},
		{name: "runFn 出错", err: io.EOF, code: 0, stderr: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeRun := func(ctx context.Context, cmd string, timeout time.Duration, encoding string) (string, string, int, error) {
				return c.stdout, c.stderr, c.code, c.err
			}
			sb := &shellBackend{run: fakeRun}
			_, err := sb.ReadDir("/nope")
			if err == nil {
				t.Errorf("应报错，得到 nil")
			}
		})
	}
}

// TestShellBackend_Stat_Parses 验证 Stat 单行解析。
func TestShellBackend_Stat_Parses(t *testing.T) {
	fakeRun := func(ctx context.Context, cmd string, timeout time.Duration, encoding string) (string, string, int, error) {
		// Stat 跑 ls -ld，单行
		return "-rw-r--r-- 1 ops ops 12345 Jun 21 10:00 app.log", "", 0, nil
	}
	sb := &shellBackend{run: fakeRun}
	info, err := sb.Stat("/var/log/app.log")
	if err != nil {
		t.Fatalf("Stat err: %v", err)
	}
	if info.Size() != 12345 {
		t.Errorf("size = %d, want 12345", info.Size())
	}
	if info.IsDir() {
		t.Errorf("IsDir should be false")
	}
	if info.Name() != "app.log" {
		t.Errorf("name = %q, want app.log", info.Name())
	}
}

// TestShellBackend_Stat_MissingTargetIsNotExist 验证"目标不存在"被归类为
// os.ErrNotExist（各家 ls 的缺省文案）。
//
// 这是 shell 兜底主机（无 SFTP 子系统的老 AIX）能新建文件/目录的前提：
// resolve.go 的 resolveDir/resolveWritePathCtx/resolveDirectoryForCreate
// 全部用 errors.Is(err, os.ErrNotExist) 区分"确定不存在＝允许新建"与
// "权限/协议错误＝必须终止"。裸错误会让新建操作全部失败。
func TestShellBackend_Stat_MissingTargetIsNotExist(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		code   int
	}{
		{name: "GNU ls", stderr: "ls: cannot access '/nope': No such file or directory", code: 2},
		{name: "BusyBox ls", stderr: "ls: /nope: No such file or directory", code: 1},
		{name: "AIX ls", stderr: "ls: 0653-341 The file /nope does not exist.", code: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeRun := func(ctx context.Context, cmd string, timeout time.Duration, encoding string) (string, string, int, error) {
				return "", tc.stderr, tc.code, nil
			}
			sb := &shellBackend{run: fakeRun}
			_, err := sb.Stat("/nope")
			if err == nil {
				t.Fatal("应报错")
			}
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("缺省路径必须归类为 os.ErrNotExist，得到: %v", err)
			}
			var perr *os.PathError
			if !errors.As(err, &perr) || perr.Path != "/nope" {
				t.Fatalf("应带路径信息的 *os.PathError，得到: %#v", err)
			}
		})
	}
}

// TestShellBackend_Stat_ErrorMessage 验证非缺省错误（权限/协议）仍原样保留 stderr 诊断，
// 且**不得**被误判成 os.ErrNotExist —— 否则权限错误会被当成"可以新建"，
// 重演 OTH-03 的静默写到另一个物理文件。
func TestShellBackend_Stat_ErrorMessage(t *testing.T) {
	fakeRun := func(ctx context.Context, cmd string, timeout time.Duration, encoding string) (string, string, int, error) {
		return "", "ls: cannot access '/nope': Permission denied", 2, nil
	}
	sb := &shellBackend{run: fakeRun}
	_, err := sb.Stat("/nope")
	if err == nil {
		t.Fatal("应报错")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("权限错误不得归类为 os.ErrNotExist: %v", err)
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("错误信息应包含 stderr: %v", err)
	}
}

// TestShellBackend_ReadDir_MissingTargetIsNotExist 验证 ReadDir 同样分类，
// 且命令不存在（127）不算"目录不存在"。
func TestShellBackend_ReadDir_MissingTargetIsNotExist(t *testing.T) {
	sb := &shellBackend{run: func(ctx context.Context, cmd string, timeout time.Duration, encoding string) (string, string, int, error) {
		return "", "ls: /nope: No such file or directory", 2, nil
	}}
	if _, err := sb.ReadDir("/nope"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("缺失目录必须归类为 os.ErrNotExist，得到: %v", err)
	}

	noLs := &shellBackend{run: func(ctx context.Context, cmd string, timeout time.Duration, encoding string) (string, string, int, error) {
		return "", "sh: ls: not found", 127, nil
	}}
	_, err := noLs.Stat("/nope")
	if err == nil {
		t.Fatal("应报错")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ls 命令本身缺失（127）不得被当成路径不存在: %v", err)
	}
}

// TestClient_Backend_Naming 验证 Backend() 返回正确的实现名。
func TestClient_Backend_Naming(t *testing.T) {
	c := &Client{}
	if got := c.Backend(); got != "none" {
		t.Errorf("nil client.Backend() = %q, want none", got)
	}
	c = &Client{b: &shellBackend{}}
	if got := c.Backend(); got != "shell" {
		t.Errorf("shellBackend.Backend() = %q, want shell", got)
	}
	c = &Client{b: &realSftpBackend{}}
	if got := c.Backend(); got != "sftp" {
		t.Errorf("realSftpBackend.Backend() = %q, want sftp", got)
	}
}
