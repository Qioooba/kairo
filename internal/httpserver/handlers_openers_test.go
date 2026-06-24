package httpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalOpenWith_BadInput 验证 /api/local/open-with 的 fail-fast 分支。
//
// 不真起 opener 程序（测试环境没有 GUI 软件），所有 case 应在 exec.Command 之前就被拒。
func TestLocalOpenWith_BadInput(t *testing.T) {
	srv, _, _, tmpDir := newTestServer(t)

	// 准备一个合法文件 + 一个配置的 opener（路径写不存在的程序没关系，
	// 因为合法路径的 happy path 会试图 exec.Command.Start，要避免）。
	goodFile := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(goodFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		body    map[string]any
		want    int
		wantSub string // body 必须含这个子串（不区分大小写）
	}{
		{
			name:    "opener 空",
			body:    map[string]any{"opener": "", "name": "test.log"},
			want:    400,
			wantSub: "opener 不能为空",
		},
		{
			name:    "name 空",
			body:    map[string]any{"opener": "anything", "name": ""},
			want:    400,
			wantSub: "name 不能为空",
		},
		{
			name:    "opener 不在白名单",
			body:    map[string]any{"opener": "未配置的Notepad++", "name": "test.log"},
			want:    404,
			wantSub: "未找到打开器",
		},
		{
			name:    "name 含 ..",
			body:    map[string]any{"opener": "x", "name": "../etc/passwd"},
			want:    400,
			wantSub: "name 含非法字符",
		},
		{
			name:    "name 含反斜杠",
			body:    map[string]any{"opener": "x", "name": "..\\win.ini"},
			want:    400,
			wantSub: "name 含非法字符",
		},
		{
			name:    "name 含 NUL",
			body:    map[string]any{"opener": "x", "name": "x\x00.log"},
			want:    400,
			wantSub: "name 含非法字符",
		},
		{
			name:    "name 不存在",
			body:    map[string]any{"opener": "x", "name": "不存在.log"},
			want:    404,
			wantSub: "下载文件不存在",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(srv, "POST", "/api/local/open-with", tc.body)
			if w.Code != tc.want {
				t.Errorf("期望 %d，得到 %d body=%s", tc.want, w.Code, w.Body.String())
				return
			}
			if tc.wantSub != "" && !strings.Contains(strings.ToLower(w.Body.String()), strings.ToLower(tc.wantSub)) {
				t.Errorf("body 未含 %q，body=%s", tc.wantSub, w.Body.String())
			}
		})
	}
}

// TestAdminOpeners_PutGet 验证 /api/admin/openers GET/PUT 基础行为。
//
// 设计目的：确保 PUT 写盘后 GET 能拿到；空数组能清空；非法值被 400 拒。
func TestAdminOpeners_PutGet(t *testing.T) {
	srv, _, _, _ := newTestServer(t)

	// 1) GET 初始（应该空数组 / nil）
	w := doRequest(srv, "GET", "/api/admin/openers", nil)
	if w.Code != 200 {
		t.Fatalf("GET 期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}

	// 2) PUT 一个 opener
	w = doRequest(srv, "PUT", "/api/admin/openers", map[string]any{
		"openers": []map[string]any{
			{"name": "notepad++", "path": "/tmp/npp.sh", "icon": "📝"},
			{"name": "vscode", "path": "/tmp/code.sh", "icon": "💡"},
		},
	})
	if w.Code != 200 {
		t.Fatalf("PUT 期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}

	// 3) GET 应能拿到 2 个
	w = doRequest(srv, "GET", "/api/admin/openers", nil)
	if w.Code != 200 {
		t.Fatalf("GET 期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "notepad++") || !strings.Contains(w.Body.String(), "vscode") {
		t.Errorf("GET 结果未含两个 opener，body=%s", w.Body.String())
	}

	// 4) 非法：name 空
	w = doRequest(srv, "PUT", "/api/admin/openers", map[string]any{
		"openers": []map[string]any{
			{"name": "", "path": "/tmp/x", "icon": ""},
		},
	})
	if w.Code != 400 {
		t.Errorf("name 空期望 400，得到 %d body=%s", w.Code, w.Body.String())
	}

	// 5) 非法：path 空
	w = doRequest(srv, "PUT", "/api/admin/openers", map[string]any{
		"openers": []map[string]any{
			{"name": "x", "path": "", "icon": ""},
		},
	})
	if w.Code != 400 {
		t.Errorf("path 空期望 400，得到 %d body=%s", w.Code, w.Body.String())
	}

	// 6) 非法：name 重复
	w = doRequest(srv, "PUT", "/api/admin/openers", map[string]any{
		"openers": []map[string]any{
			{"name": "x", "path": "/tmp/a", "icon": ""},
			{"name": "x", "path": "/tmp/b", "icon": ""},
		},
	})
	if w.Code != 400 {
		t.Errorf("name 重复期望 400，得到 %d body=%s", w.Code, w.Body.String())
	}

	// 7) 合法：清空
	w = doRequest(srv, "PUT", "/api/admin/openers", map[string]any{
		"openers": []map[string]any{},
	})
	if w.Code != 200 {
		t.Errorf("清空期望 200，得到 %d body=%s", w.Code, w.Body.String())
	}

	// 8) 非法：openers 字段缺失（nil）
	w = doRequest(srv, "PUT", "/api/admin/openers", map[string]any{})
	if w.Code != 400 {
		t.Errorf("openers 缺失期望 400，得到 %d body=%s", w.Code, w.Body.String())
	}
}