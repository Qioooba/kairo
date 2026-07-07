package sftpclient

import (
	"os"
	"testing"
)

func TestFormatMode(t *testing.T) {
	cases := []struct {
		desc string
		m    os.FileMode
		want string
	}{
		{"0o644 普通文件", 0o644, "-rw-r--r--"},
		{"0o755 目录", os.ModeDir | 0o755, "drwxr-xr-x"},
		{"0o755 symlink", os.ModeSymlink | 0o755, "lrwxr-xr-x"},
		{"0o777 symlink", os.ModeSymlink | 0o777, "lrwxrwxrwx"},
		{"setuid+exec 模拟 sftp 0o4755", 0o755 | os.ModeSetuid, "-rwsr-xr-x"},
		{"setgid+exec 模拟 sftp 0o2755", 0o755 | os.ModeSetgid, "-rwxr-sr-x"},
		{"setuid+setgid+sticky 模拟 0o7755", 0o755 | os.ModeSetuid | os.ModeSetgid | os.ModeSticky, "-rwsr-sr-t"},
		{"sticky dir all_x", os.ModeDir | 0o777 | os.ModeSticky, "drwxrwxrwt"},
		{"sticky file r-x", 0o577 | os.ModeSticky, "-r-xrwxrwt"},
		{"sticky dir other rx", os.ModeDir | 0o775 | os.ModeSticky, "drwxrwxr-t"},
		{"setuid no_exec_owner", 0o644 | os.ModeSetuid, "-rwSr--r--"},
		{"sticky no_exec_other", os.ModeDir | 0o750 | os.ModeSticky, "drwxr-x--T"},
		{"all_specials_dir", os.ModeDir | 0o777 | os.ModeSetuid | os.ModeSetgid | os.ModeSticky, "drwsrwsrwt"},
		{"dir_empty", os.ModeDir | 0o0, "d---------"},
		{"0o600 file", 0o600, "-rw-------"},
	}
	for _, c := range cases {
		got := FormatMode(c.m)
		if got != c.want {
			t.Errorf("%s: FormatMode(0x%08x) = %q, want %q", c.desc, uint32(c.m), got, c.want)
		}
	}
}
