package httpserver

// sftp_f_identity_api_test.go — Batch F（OTH-05）修复后的 HTTP 契约回归。
//
// 前置失败回归见 sftp_f_identity_handlers_test.go（那份文件只用修复前就存在的
// 请求字段）。这里补的是修复中新增的契约：
//   - 列表结果由"已解析原始父路径"生成 path_id / path_id / parent_path_id；
//   - (parent_path_id + name) 服务端组合，禁止前端把 "/name" 拼到身份上；
//   - path_ids 必须与 paths 平行等长；
//   - 身份不能绕过 free_file_roots（客户端给的 display_path 不参与授权）。

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path"
	"strconv"
	"testing"

	"kairo/internal/sftpclient"
	"kairo/internal/sshclient"
)

// sftpFRawClient 在 sftpFClient 之上模拟"真实 sftpclient 的列表结果"：
// 条目携带已解析原始绝对路径，并提供 ResolveDirIdentityCtx。
type sftpFRawClient struct {
	*sftpFClient
	rawDirs     map[string]string // 展示目录 → 已解析原始目录
	dirIdentity string            // ResolveDirIdentityCtx 的返回
}

func (c *sftpFRawClient) ReadDir(p string) ([]os.FileInfo, error) {
	p = sftpTestResolveIdentity(p)
	entries, ok := c.dirs[p]
	if !ok {
		return nil, &os.PathError{Op: "readdir", Path: p, Err: os.ErrNotExist}
	}
	rawParent := p
	if r, ok := c.rawDirs[p]; ok {
		rawParent = r
	}
	out := make([]os.FileInfo, 0, len(entries))
	for _, e := range entries {
		fi := fakeFileInfo{name: e.name, size: e.size, isDir: e.isDir, mode: 0o644}
		// 与真实 Client.ReadDir 一样：条目携带"已解析原始父目录 + 原始名"的绝对路径。
		out = append(out, sftpclient.WithRawPath(fi, path.Join(rawParent, e.name)))
	}
	return out, nil
}

func (c *sftpFRawClient) ResolveDirIdentityCtx(_ context.Context, dir string) (string, error) {
	if c.dirIdentity != "" {
		return c.dirIdentity, nil
	}
	return dir, nil
}

// ListLimited 必须显式实现：内嵌类型的 ListLimited 内部调的是它自己的 ReadDir，
// 不会派发到外层覆写，否则这里的"携带原始路径"条目会被绕过。
func (c *sftpFRawClient) ListLimited(p string, max int) ([]os.FileInfo, bool, error) {
	entries, err := c.ReadDir(p)
	if err != nil {
		return nil, false, err
	}
	if max > 0 && len(entries) > max {
		return entries[:max], true, nil
	}
	return entries, false, nil
}

// sftpFWithFakeRoots 启假 SSH + 假 SFTP，并把 free_file_roots 设成指定白名单。
func sftpFWithFakeRoots(t *testing.T, cli sftpClientLike, roots []string) *Server {
	t.Helper()
	addr := startFakeSSH(t, "ops", "testpw")
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	srv, mgr, _, _ := newTestServer(t)
	cfg := mgr.Get()
	cfg.App.FreeFileRoots = roots
	for si := range cfg.Systems {
		for sj := range cfg.Systems[si].Servers {
			if cfg.Systems[si].Servers[sj].Name == "mock-1" {
				cfg.Systems[si].Servers[sj].Host = "127.0.0.1"
				cfg.Systems[si].Servers[sj].Port = port
			}
		}
	}
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	orig := sftpDialer
	sftpDialer = func(_ *sshclient.Client) (sftpClientLike, error) { return cli, nil }
	t.Cleanup(func() { sftpDialer = orig })
	return srv
}

// TestSFTPF_ListMintsIdentityFromResolvedRawParent
// 列表条目的 path_id 必须等于"已解析原始父路径 + 原始名"的身份，
// 目录自身与父目录身份也要下发。
func TestSFTPF_ListMintsIdentityFromResolvedRawParent(t *testing.T) {
	displayDir := "/tmp/" + sftpFUTF8ZhongWen
	rawDir := "/tmp/" + sftpFGBKZhongWen
	rawFile := rawDir + "/" + sftpFGBKZhongWen + ".log"
	c := &sftpFRawClient{
		sftpFClient: sftpFNewBasic(),
		rawDirs:     map[string]string{displayDir: rawDir},
		dirIdentity: rawDir,
	}
	c.dirs[displayDir] = []sftpFEntry{{name: sftpFGBKZhongWen + ".log", size: 3}}
	c.files[rawFile] = []byte("abc")
	srv := sftpFWithFake(t, c)

	w := doRequest(srv, "POST", "/api/files/list", sftpFCreds(map[string]any{
		"path": displayDir,
	}))
	if w.Code != 200 {
		t.Fatalf("期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		PathID       string `json:"path_id"`
		ParentPathID string `json:"parent_path_id"`
		Entries      []struct {
			Name        string `json:"name"`
			DisplayPath string `json:"display_path"`
			PathID      string `json:"path_id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("期望 1 条条目，得到 %d", len(got.Entries))
	}
	wantID := sftpclient.EncodePathIdentity(rawFile)
	if got.Entries[0].PathID != wantID {
		t.Fatalf("条目 path_id = %q (%s)，期望由原始父路径生成的 %q",
			got.Entries[0].PathID, sftpFDecode(got.Entries[0].PathID), wantID)
	}
	if got.PathID != sftpclient.EncodePathIdentity(rawDir) {
		t.Fatalf("目录 path_id = %q，期望 %q", got.PathID, sftpclient.EncodePathIdentity(rawDir))
	}
	if got.ParentPathID != sftpclient.EncodePathIdentity("/tmp") {
		t.Fatalf("parent_path_id = %q，期望 %q", got.ParentPathID, sftpclient.EncodePathIdentity("/tmp"))
	}
	if got.Entries[0].DisplayPath != "/tmp/"+sftpFUTF8ZhongWen+"/"+sftpFUTF8ZhongWen+".log" {
		t.Fatalf("display_path 必须是人类可读路径，得到 %q", got.Entries[0].DisplayPath)
	}
}

// TestSFTPF_PathIDCannotBypassRootWhitelist
// 客户端不能靠"白名单内的 display_path"把越权身份带进来：授权只看身份推导出的路径。
func TestSFTPF_PathIDCannotBypassRootWhitelist(t *testing.T) {
	insideRaw := "/allowed/ok.log"
	outsideRaw := "/secret/x.log"
	c := sftpFNewBasic()
	c.files[insideRaw] = []byte("ok")
	c.files[outsideRaw] = []byte("secret")
	srv := sftpFWithFakeRoots(t, c, []string{"/allowed"})

	// 1) 白名单内：正常放行
	w := doRequest(srv, "POST", "/api/files/preview", sftpFCreds(map[string]any{
		"path_id": sftpclient.EncodePathIdentity(insideRaw),
	}))
	if w.Code != 200 {
		t.Fatalf("白名单内身份应 200，得到 %d body=%s", w.Code, w.Body.String())
	}

	// 2) 越权身份 + 白名单内的展示路径 → 必须 403，且不能访问后端
	before := len(c.openedPaths)
	w = doRequest(srv, "POST", "/api/files/preview", sftpFCreds(map[string]any{
		"path":         insideRaw,
		"display_path": insideRaw,
		"path_id":      sftpclient.EncodePathIdentity(outsideRaw),
	}))
	if w.Code != 403 {
		t.Fatalf("越权身份应 403（不能靠 display_path 绕过白名单），得到 %d body=%s", w.Code, w.Body.String())
	}
	if len(c.openedPaths) != before {
		t.Fatalf("越权请求仍然访问了后端: %q", c.openedPaths[before:])
	}
}

// TestSFTPF_DownloadPathIDsRejectedOnLengthMismatch：平行数组必须等长。
func TestSFTPF_DownloadPathIDsRejectedOnLengthMismatch(t *testing.T) {
	c := sftpFNewBasic()
	srv := sftpFWithFake(t, c)
	w := doRequest(srv, "POST", "/api/files/download", sftpFCreds(map[string]any{
		"paths":    []string{"/a.log", "/b.log"},
		"path_ids": []string{sftpclient.EncodePathIdentity("/a.log")},
	}))
	if w.Code != 400 {
		t.Fatalf("path_ids 长度不匹配应 400，得到 %d body=%s", w.Code, w.Body.String())
	}
}

// TestSFTPF_CreateTargetCombinesParentIdentityAndName
// 新建/重命名由服务端组合 (parent_path_id + name)，不做字符串拼 token。
func TestSFTPF_CreateTargetCombinesParentIdentityAndName(t *testing.T) {
	parentRaw := "/tmp/" + sftpFGBKZhongWen
	parentID := sftpclient.EncodePathIdentity(parentRaw)

	target, display, err := sftpCreateTarget(parentID, "新文件.txt", "", "")
	if err != nil {
		t.Fatalf("sftpCreateTarget 失败: %v", err)
	}
	// 审核第 10 项：组合结果以身份 token 下发（客户端直接解出精确原始字节路径，
	// 不再把原始字节当成展示路径重新解析）。
	rawTarget, ok := sftpclient.DecodePathIdentity(target)
	if !ok {
		t.Fatalf("target 必须是身份 token，得到 %q (hex %x)", target, []byte(target))
	}
	if want := parentRaw + "/新文件.txt"; rawTarget != want {
		t.Fatalf("target 解码后 = %q (hex %x)，期望 %q (hex %x)", rawTarget, []byte(rawTarget), want, []byte(want))
	}
	if want := "/tmp/" + sftpFUTF8ZhongWen + "/新文件.txt"; display != want {
		t.Fatalf("display = %q，期望 %q", display, want)
	}

	// 名字里带 "/" 或 NUL 必须被拒（不能把分隔符塞进身份组合里）
	for _, bad := range []string{"../evil", "a/b", "a\\b", "x\x00y", ""} {
		if _, _, err := sftpCreateTarget(parentID, bad, "", ""); err == nil {
			t.Errorf("name=%q 应被拒绝", bad)
		}
	}
	// 旧契约回退（只有 path）仍然可用
	if target, _, err := sftpCreateTarget("", "", "", "/tmp/a.txt"); err != nil || target != "/tmp/a.txt" {
		t.Fatalf("旧契约回退失败: target=%q err=%v", target, err)
	}
	// 相对路径必须被拒
	if _, _, err := sftpCreateTarget("", "", "", "tmp/a.txt"); err == nil {
		t.Fatal("相对路径应被拒绝")
	}
}

func sftpFDecode(id string) string {
	raw, _ := sftpclient.DecodePathIdentity(id)
	return string([]byte(raw))
}
