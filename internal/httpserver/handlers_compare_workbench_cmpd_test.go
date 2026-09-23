package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kairo/internal/comparefs"
)

// Batch D（CT01/CT02 的后端半边）回归：
//   - 版本 token 必须绑定读取来源，size+mtime 完全相同的另一个文件不得被写入；
//   - 成功的写入必须返回这次写入真正产生的版本/规范路径/正文摘要；
//   - 同一路径上的旧版本仍然必须冲突。
//
// 本文件是新增文件，所有辅助函数统一带 cmpD 前缀，避免与同包内其它会话的
// 新增辅助/用例重名；用例只依赖本文件与既有测试基础设施（newTestServer/doRequest）。

// cmpDFixedTime 是 A/B 两个文件共用的 mtime，让“仅看 size+mtime 无法区分来源”
// 这一前置条件在任意文件系统上都成立（两个文件写入同一个时间值，取整方式一致）。
var cmpDFixedTime = time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

// cmpDCreateTwinFiles 建出大小与 mtime 完全一致的 A/B 两个文件。
func cmpDCreateTwinFiles(t *testing.T, dir string) (string, string) {
	t.Helper()
	first := filepath.Join(dir, "A.txt")
	second := filepath.Join(dir, "B.txt")
	if err := os.WriteFile(first, []byte("AAA"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("BBB"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(first, cmpDFixedTime, cmpDFixedTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(second, cmpDFixedTime, cmpDFixedTime); err != nil {
		t.Fatal(err)
	}
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstInfo.Size() != secondInfo.Size() || !firstInfo.ModTime().Equal(secondInfo.ModTime()) {
		t.Fatalf("前置条件失败：A/B 的 size/mtime 不一致 size=%d/%d mtime=%s/%s",
			firstInfo.Size(), secondInfo.Size(), firstInfo.ModTime(), secondInfo.ModTime())
	}
	return first, second
}

// cmpDCompareRead 走真实 HTTP 读取一个本地文件，返回解码后的响应。
func cmpDCompareRead(t *testing.T, srv *Server, path string) compareReadResp {
	t.Helper()
	response := doRequest(srv, "POST", "/api/compare/read", map[string]any{
		"source": map[string]any{"kind": "local", "path": path, "encoding": "auto"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("read status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded compareReadResp
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version.Path == "" {
		t.Fatalf("版本 token 必须绑定读取来源路径，实际=%+v", decoded.Version)
	}
	return decoded
}

// cmpDCompareWrite 走真实 HTTP 发起条件写入；codec 字段沿用读取响应，模拟前端负载。
func cmpDCompareWrite(t *testing.T, srv *Server, target string, content string, read compareReadResp, backup bool) *httptest.ResponseRecorder {
	t.Helper()
	expected := read.Version
	return doRequest(srv, "POST", "/api/compare/write", map[string]any{
		"target":  map[string]any{"kind": "local", "path": target},
		"content": content,
		"expected": map[string]any{
			"size":  expected.Size,
			"mtime": expected.ModTime,
			"path":  expected.Path,
		},
		"encoding": read.Encoding,
		"eol":      read.EOL,
		"bom":      read.BOM,
		"backup":   backup,
	})
}

// cmpDWriteRaw 用于构造“expected 与目标身份不符”的错配负载。
func cmpDWriteRaw(t *testing.T, srv *Server, target string, content string, expected *comparefs.Version, backup bool) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{
		"target":  map[string]any{"kind": "local", "path": target},
		"content": content,
		"backup":  backup,
	}
	if expected != nil {
		body["expected"] = map[string]any{"size": expected.Size, "mtime": expected.ModTime, "path": expected.Path}
	}
	return doRequest(srv, "POST", "/api/compare/write", body)
}

func cmpDReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// cmpDBackupFiles 列出目录下 compare 备份文件，用来证明被拒绝的写入没有落盘副作用。
func cmpDBackupFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var backups []string
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".kairo-backup-") {
			backups = append(backups, entry.Name())
		}
	}
	return backups
}

// 1) 评审交叉确认的场景：A/B 同大小同 mtime，用 A 的版本去写 B 必须被拒绝，且 B 字节不变。
func TestCompareWriteCmPDRejectsPathIdentityMismatch(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	dir := t.TempDir()
	first, second := cmpDCreateTwinFiles(t, dir)

	readFirst := cmpDCompareRead(t, srv, first)
	if !comparefs.SamePathIdentity(readFirst.Version.Path, first) {
		t.Fatalf("版本 token 的来源路径应为 %q，实际 %q", first, readFirst.Version.Path)
	}
	if comparefs.SamePathIdentity(readFirst.Version.Path, second) {
		t.Fatalf("版本 token 不得与另一个文件同身份：%q", readFirst.Version.Path)
	}

	// 错配负载：target=B，expected=A 的版本，正文是 A 的编辑稿。
	response := cmpDWriteRaw(t, srv, second, "A edited", &readFirst.Version, true)
	if response.Code != http.StatusConflict {
		t.Fatalf("错配写入必须 409，实际 status=%d body=%s", response.Code, response.Body.String())
	}
	if got := string(cmpDReadFile(t, second)); got != "BBB" {
		t.Fatalf("被拒绝的写入不得改动目标文件，实际内容=%q", got)
	}
	if got := string(cmpDReadFile(t, first)); got != "AAA" {
		t.Fatalf("来源文件不得被改动，实际内容=%q", got)
	}
	if backups := cmpDBackupFiles(t, dir); len(backups) != 0 {
		t.Fatalf("被拒绝的写入不得创建备份，实际=%v", backups)
	}
}

// 2) 合法写入返回这次写入产生的版本/规范路径/正文摘要，且新版本可直接用于下一次写入。
func TestCompareWriteCmPDReturnsWrittenVersion(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(target, []byte("current\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	readBefore := cmpDCompareRead(t, srv, target)
	const updated = "current updated\n"
	response := cmpDCompareWrite(t, srv, target, updated, readBefore, false)
	if response.Code != http.StatusOK {
		t.Fatalf("合法写入必须成功，实际 status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded compareWriteResp
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.OK {
		t.Fatalf("响应必须声明 ok：%s", response.Body.String())
	}
	if !comparefs.SamePathIdentity(decoded.Version.Path, target) {
		t.Fatalf("返回版本必须绑定目标规范路径 %q，实际 %q", target, decoded.Version.Path)
	}
	if !comparefs.SamePathIdentity(decoded.Path, target) {
		t.Fatalf("返回的规范路径应为目标路径 %q，实际 %q", target, decoded.Path)
	}

	onDisk := cmpDReadFile(t, target)
	if string(onDisk) != updated {
		t.Fatalf("磁盘正文应为本次写入内容，实际=%q", string(onDisk))
	}
	if decoded.Size != int64(len(onDisk)) {
		t.Fatalf("返回 size 必须描述实际写入字节：got=%d want=%d", decoded.Size, len(onDisk))
	}
	sum := sha256.Sum256(onDisk)
	if want := "sha256:" + hex.EncodeToString(sum[:]); decoded.Digest != want {
		t.Fatalf("返回摘要必须描述实际写入正文：got=%q want=%q", decoded.Digest, want)
	}

	// 返回的新版本必须能直接支撑下一次条件写入（CT02 的版本推进契约）。
	second := doRequest(srv, "POST", "/api/compare/write", map[string]any{
		"target":  map[string]any{"kind": "local", "path": target},
		"content": "current again\n",
		"expected": map[string]any{
			"size":  decoded.Version.Size,
			"mtime": decoded.Version.ModTime,
			"path":  decoded.Version.Path,
		},
		"encoding": readBefore.Encoding,
		"eol":      readBefore.EOL,
	})
	if second.Code != http.StatusOK {
		t.Fatalf("用返回的新版本继续写入必须成功，实际 status=%d body=%s", second.Code, second.Body.String())
	}
	// 而用写入前的旧版本再写同一路径必须冲突（不能因为绑定身份就放过陈旧版本）。
	stale := cmpDCompareWrite(t, srv, target, "current stale\n", readBefore, false)
	if stale.Code != http.StatusConflict {
		t.Fatalf("旧版本写同一路径必须 409，实际 status=%d body=%s", stale.Code, stale.Body.String())
	}
	if got := string(cmpDReadFile(t, target)); got != "current again\n" {
		t.Fatalf("冲突写入不得改动磁盘，实际=%q", got)
	}
}

// 3) 同一路径上的真正陈旧版本仍然冲突（不回归既有的乐观并发检查）。
func TestCompareWriteCmPDRejectsStaleVersionSamePath(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(target, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}

	readBefore := cmpDCompareRead(t, srv, target)

	// 第三方在写之前改了同一个文件：大小与 mtime 都变化。
	if err := os.WriteFile(target, []byte("changed externally"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := time.Date(2026, 9, 23, 11, 22, 33, 0, time.UTC)
	if err := os.Chtimes(target, external, external); err != nil {
		t.Fatal(err)
	}

	response := cmpDCompareWrite(t, srv, target, "replacement", readBefore, true)
	if response.Code != http.StatusConflict {
		t.Fatalf("同路径陈旧版本必须 409，实际 status=%d body=%s", response.Code, response.Body.String())
	}
	if got := string(cmpDReadFile(t, target)); got != "changed externally" {
		t.Fatalf("冲突写入不得改动磁盘，实际=%q", got)
	}
	if backups := cmpDBackupFiles(t, dir); len(backups) != 0 {
		t.Fatalf("被拒绝的写入不得创建备份，实际=%v", backups)
	}
}
