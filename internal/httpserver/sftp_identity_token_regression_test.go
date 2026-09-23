package httpserver

// sftp_identity_token_regression_test.go — 审核第 10 项（P2）回归。
//
// 症状：远端存在"UTF-8 与 GBK 两种字节编码、显示名相同"的文件时，用户已经在列表里
// 选中精确条目（前端传回来的是 kairo-raw: 身份 token），预览/下载仍报"路径歧义"。
//
// 根因：HTTP 层 sftpRequestPath 把 path_id **提前解码**成原始字节路径再交给 sftpclient。
// 客户端拿到的是"看起来像展示路径"的字节串，于是重新展开 [UTF-8, GBK] 编码候选，
// 两个候选都存在 → ErrAmbiguousPath；GBK 条目的原始字节还会被当成展示名进入本地命名链路。
//
// 修复：HTTP 层把身份 token 原样下发给客户端（客户端解密即得精确原始字节，零解析、无歧义），
// 展示路径单独由 DecodeServerName 算出，只用于 UI / 审计 / 本地命名。

import (
	"encoding/json"
	"io"
	"os"
	"path"
	"strings"
	"testing"

	"kairo/internal/sftpclient"
)

// sftpFStrictClient 模拟真实 *sftpclient.Client 的关键语义：
//   - 身份 token → 直接解出精确的原始字节路径（resolve.go 里所有入口的第一步）；
//   - 展示路径 → 需要展开编码候选；这里两个同名条目都存在，所以一律判为歧义（绝不任选）。
type sftpFStrictClient struct {
	files   map[string][]byte
	opened  string
	statted string
}

func (c *sftpFStrictClient) Close() error { return nil }

func (c *sftpFStrictClient) resolve(p string) (string, error) {
	if raw, ok := sftpclient.DecodePathIdentity(p); ok {
		return raw, nil
	}
	// 展示路径 + 同显示名多编码条目 → 真实客户端返回的也是这个错误。
	return "", sftpclient.ErrAmbiguousPath
}

func (c *sftpFStrictClient) Stat(p string) (os.FileInfo, error) {
	raw, err := c.resolve(p)
	if err != nil {
		return nil, err
	}
	c.statted = raw
	content, ok := c.files[raw]
	if !ok {
		return nil, &os.PathError{Op: "stat", Path: raw, Err: os.ErrNotExist}
	}
	return fakeFileInfo{name: path.Base(raw), size: int64(len(content)), mode: 0o644}, nil
}

func (c *sftpFStrictClient) Open(p string) (sftpclient.SftpFile, error) {
	raw, err := c.resolve(p)
	if err != nil {
		return nil, err
	}
	c.opened = raw
	content, ok := c.files[raw]
	if !ok {
		return nil, &os.PathError{Op: "open", Path: raw, Err: os.ErrNotExist}
	}
	return &fakeSftpFile{r: strings.NewReader(string(content)), size: int64(len(content)), name: path.Base(raw)}, nil
}

func (c *sftpFStrictClient) ReadDir(string) ([]os.FileInfo, error) {
	return nil, &os.PathError{Op: "readdir", Err: os.ErrNotExist}
}

func (c *sftpFStrictClient) ListLimited(string, int) ([]os.FileInfo, bool, error) {
	return nil, false, &os.PathError{Op: "readdir", Err: os.ErrNotExist}
}

func (c *sftpFStrictClient) DownloadFile(remote, local string) (int64, error) {
	return c.download(remote, local)
}

func (c *sftpFStrictClient) DownloadFileWithProgress(remote, local string, _ func(int64, int64)) (int64, error) {
	return c.download(remote, local)
}

func (c *sftpFStrictClient) download(remote, local string) (int64, error) {
	raw, err := c.resolve(remote)
	if err != nil {
		return 0, err
	}
	content, ok := c.files[raw]
	if !ok {
		return 0, &os.PathError{Op: "open", Path: raw, Err: os.ErrNotExist}
	}
	c.opened = raw
	if err := os.MkdirAll(path.Dir(local), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(local, content, 0o644); err != nil {
		return 0, err
	}
	return int64(len(content)), nil
}

// TestSFTPF_RequestPathKeepsIdentityToken：HTTP 层的身份契约——
// 身份 token 原样下发，展示路径单独算（GBK 原始字节必须解成可读中文）。
func TestSFTPF_RequestPathKeepsIdentityToken(t *testing.T) {
	raw := "/data/" + sftpFGBKZhongWen + ".log"
	identity, display, err := sftpRequestPath(sftpclient.EncodePathIdentity(raw), "", "")
	if err != nil {
		t.Fatalf("解析身份失败: %v", err)
	}
	if decoded, ok := sftpclient.DecodePathIdentity(identity); !ok || decoded != raw {
		t.Fatalf("identity 必须保持身份 token（解码后为原始字节路径 %q），得到 %q", raw, identity)
	}
	if want := "/data/" + sftpFUTF8ZhongWen + ".log"; display != want {
		t.Fatalf("display 必须是可读的 UTF-8 展示路径 %q，得到 %q", want, display)
	}
}

// TestSFTPF_PreviewKeepsIdentityWhenDisplayNamesCollide：
// 同显示名、不同字节编码的两个物理条目都存在时，凭 path_id 预览必须精确命中。
func TestSFTPF_PreviewKeepsIdentityWhenDisplayNamesCollide(t *testing.T) {
	utf8Raw := "/data/" + sftpFUTF8ZhongWen + ".log"
	gbkRaw := "/data/" + sftpFGBKZhongWen + ".log"
	c := &sftpFStrictClient{files: map[string][]byte{
		utf8Raw: []byte("utf8-entry"),
		gbkRaw:  []byte("gbk-entry"),
	}}
	srv := sftpFWithFake(t, c)

	w := doRequest(srv, "POST", "/api/files/preview", sftpFCreds(map[string]any{
		"path_id": sftpclient.EncodePathIdentity(gbkRaw),
	}))
	if w.Code != 200 {
		t.Fatalf("已选中精确条目仍失败（HTTP 层提前解码了身份就会报路径歧义）: %d body=%s", w.Code, w.Body.String())
	}
	if c.opened != gbkRaw {
		t.Fatalf("实际打开 = %q (hex %x)，期望原始字节路径 %q (hex %x)",
			c.opened, []byte(c.opened), gbkRaw, []byte(gbkRaw))
	}
}

// TestSFTPF_DownloadKeepsIdentityWhenDisplayNamesCollide：下载同理。
func TestSFTPF_DownloadKeepsIdentityWhenDisplayNamesCollide(t *testing.T) {
	utf8Raw := "/data/" + sftpFUTF8ZhongWen + ".log"
	gbkRaw := "/data/" + sftpFGBKZhongWen + ".log"
	c := &sftpFStrictClient{files: map[string][]byte{
		utf8Raw: []byte("utf8-entry"),
		gbkRaw:  []byte("gbk-entry"),
	}}
	srv := sftpFWithFake(t, c)

	w := doRequest(srv, "POST", "/api/files/download", sftpFCreds(map[string]any{
		"paths":    []string{"/data/" + sftpFUTF8ZhongWen + ".log"},
		"path_ids": []string{sftpclient.EncodePathIdentity(gbkRaw)},
	}))
	if w.Code != 200 {
		t.Fatalf("带精确身份的下载启动失败: %d body=%s", w.Code, w.Body.String())
	}
	// 下载是异步任务：订阅 events（阻塞到任务结束）再断言实际访问的远端路径。
	var dl struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &dl); err != nil {
		t.Fatal(err)
	}
	if dl.ID == "" {
		t.Fatal("下载 id 为空")
	}
	eventsW := doRequest(srv, "GET", "/api/files/download/"+dl.ID+"/events", nil)
	if eventsW.Code != 200 {
		t.Fatalf("events 应 200，得到 %d", eventsW.Code)
	}
	body, _ := io.ReadAll(eventsW.Body)
	if !strings.Contains(string(body), `"kind":"done"`) {
		t.Fatalf("下载未正常结束: %s", string(body))
	}
	if c.opened != gbkRaw {
		t.Fatalf("实际下载 = %q (hex %x)，期望原始字节路径 %q (hex %x)",
			c.opened, []byte(c.opened), gbkRaw, []byte(gbkRaw))
	}
}

// TestSFTPF_CreateTargetIsIdentity：父目录身份 + 名字的组合结果同样以身份下发，
// 避免写入前再解析一次父目录（父目录歧义会让写入失败或写错目录）。
func TestSFTPF_CreateTargetIsIdentity(t *testing.T) {
	parentRaw := "/tmp/" + sftpFGBKZhongWen
	target, display, err := sftpCreateTarget(sftpclient.EncodePathIdentity(parentRaw), "新文件.txt", "", "")
	if err != nil {
		t.Fatalf("sftpCreateTarget 失败: %v", err)
	}
	raw, ok := sftpclient.DecodePathIdentity(target)
	if !ok {
		t.Fatalf("target 必须是身份 token，得到 %q", target)
	}
	if want := parentRaw + "/新文件.txt"; raw != want {
		t.Fatalf("target 解码 = %q，期望 %q", raw, want)
	}
	if want := "/tmp/" + sftpFUTF8ZhongWen + "/新文件.txt"; display != want {
		t.Fatalf("display = %q，期望 %q", display, want)
	}
}
