package sftpclient

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// shellRunFake 模拟老 AIX 的 ls：/tmp 存在，其它路径一律 "No such file"。
func shellRunFake(ctx context.Context, cmd string, timeout time.Duration, encoding string) (string, string, int, error) {
	switch {
	case strings.HasPrefix(cmd, "ls -ld "):
		arg := strings.Trim(strings.TrimPrefix(cmd, "ls -ld "), "'\"")
		if arg == "/" || arg == "/tmp" {
			return "drwxrwxrwt 2 root root 4096 Sep 23 10:00 " + arg, "", 0, nil
		}
		return "", "ls: cannot access '" + arg + "': No such file or directory", 2, nil
	case strings.HasPrefix(cmd, "ls -la "):
		arg := strings.Trim(strings.TrimPrefix(cmd, "ls -la "), "'\"")
		if arg == "/" || arg == "/tmp" {
			return "total 0", "", 0, nil
		}
		return "", "ls: cannot access '" + arg + "': No such file or directory", 2, nil
	default:
		return "", "", 0, nil
	}
}

// TestResolveWritePath_ShellBackendCreatesNewASCIIFile 验证 shell 兜底主机上
// "目标不存在 → 允许新建"的语义没有被 shellBackend 的裸错误破坏。
//
// 回归背景：resolveWritePathCtx 对 ASCII basename 先 Stat 目标，只有
// errors.Is(err, os.ErrNotExist) 才认为"可以新建"；旧 shellBackend.Stat 在缺省路径上
// 返回 fmt.Errorf("ls -ld 退出码 2: ...")，于是无 SFTP 子系统的主机上
// 新建文件/新建目录/改名为新名全部失败（上传新文件是主要用户路径）。
func TestResolveWritePath_ShellBackendCreatesNewASCIIFile(t *testing.T) {
	c := &Client{b: &shellBackend{run: shellRunFake}}
	got, err := c.resolveWritePathCtx(context.Background(), "/tmp/newfile.txt")
	if err != nil {
		t.Fatalf("缺省目标应解析为可新建，不应报错: %v", err)
	}
	if got != "/tmp/newfile.txt" {
		t.Fatalf("解析结果 = %q, want /tmp/newfile.txt", got)
	}
}

// TestResolveWritePath_ShellBackendPermissionDeniedStillFails 验证权限错误依然是致命错误，
// 不会被"目标不存在"误判收编（否则就是 OTH-03 的静默覆盖另一物理文件）。
func TestResolveWritePath_ShellBackendPermissionDeniedStillFails(t *testing.T) {
	run := func(ctx context.Context, cmd string, timeout time.Duration, encoding string) (string, string, int, error) {
		if strings.HasPrefix(cmd, "ls -ld ") {
			arg := strings.Trim(strings.TrimPrefix(cmd, "ls -ld "), "'\"")
			if arg == "/" || arg == "/tmp" {
				return "drwxrwxrwt 2 root root 4096 Sep 23 10:00 " + arg, "", 0, nil
			}
			return "", "ls: cannot access '" + arg + "': Permission denied", 2, nil
		}
		if strings.HasPrefix(cmd, "ls -la ") {
			return "total 0", "", 0, nil
		}
		return "", "", 0, nil
	}
	c := &Client{b: &shellBackend{run: run}}
	_, err := c.resolveWritePathCtx(context.Background(), "/tmp/newfile.txt")
	if err == nil {
		t.Fatal("权限错误必须终止写路径解析")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("权限错误不得被归类为 os.ErrNotExist: %v", err)
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("应保留原始诊断: %v", err)
	}
}

// TestResolveDirectoryForCreate_ShellBackendCreatesNestedDir 验证 shell 兜底主机上
// MkdirAll 的多级创建路径也依赖同一分类（"确定不存在"才允许开始创建）。
func TestResolveDirectoryForCreate_ShellBackendCreatesNestedDir(t *testing.T) {
	c := &Client{b: &shellBackend{run: shellRunFake}}
	got, err := c.resolveDirectoryForCreate(context.Background(), "/tmp/new/deep")
	if err != nil {
		t.Fatalf("已存在前缀 + 缺省新段应可创建: %v", err)
	}
	if got != "/tmp/new/deep" {
		t.Fatalf("解析结果 = %q, want /tmp/new/deep", got)
	}
}
