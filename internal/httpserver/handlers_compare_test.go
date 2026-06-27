package httpserver

// handlers_compare_test.go — BE-001 修复的回归测试：
// /api/compare/file-diff、/api/compare/folder-scan 必须按 app.compare_allowed_roots
// 做白名单校验（fail-closed），空 roots → 一律 403，防止任意本地文件读。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompare_FileDiff_NoRoots_ForbiddenByDefault 验证 BE-001 fail-closed：
// CompareAllowedRoots 为空时，传 /etc/passwd、/etc/shadow、C:\Windows 都应 403。
func TestCompare_FileDiff_NoRoots_ForbiddenByDefault(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)
	// newTestServer 默认配 "*"，这里显式清空回 fail-closed 默认行为
	cfg := mgr.Get()
	cfg.App.CompareAllowedRoots = nil
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		body map[string]any
	}{
		{"etc/passwd", map[string]any{"left_path": "/etc/passwd", "right_path": "/etc/passwd"}},
		{"etc/shadow", map[string]any{"left_path": "/etc/shadow", "right_path": "/etc/shadow"}},
		{"C:/Windows", map[string]any{"left_path": `C:\Windows\System32\drivers\etc\hosts`, "right_path": `C:\Windows\System32\drivers\etc\hosts`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(srv, "POST", "/api/compare/file-diff", tc.body)
			if w.Code != 403 {
				t.Errorf("BE-001 fail-closed: 空 roots 时 %v 应返 403，得到 %d body=%s",
					tc.body, w.Code, w.Body.String())
			}
		})
	}
}

// TestCompare_FolderScan_NoRoots_ForbiddenByDefault 同样验证 folder-scan 的 fail-closed。
func TestCompare_FolderScan_NoRoots_ForbiddenByDefault(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)
	cfg := mgr.Get()
	cfg.App.CompareAllowedRoots = nil
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	w := doRequest(srv, "POST", "/api/compare/folder-scan", map[string]any{
		"left_path":  "/etc",
		"right_path": "/etc",
	})
	if w.Code != 403 {
		t.Errorf("folder-scan 空 roots 应返 403，得到 %d body=%s", w.Code, w.Body.String())
	}
}

// TestCompare_FileDiff_RootsWhitelisted 配合法 roots 后，白名单内路径应放行到 diff 调用。
func TestCompare_FileDiff_RootsWhitelisted(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)
	tmp := t.TempDir()
	left := filepath.Join(tmp, "left.txt")
	right := filepath.Join(tmp, "right.txt")
	if err := os.WriteFile(left, []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte("a\nc\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := mgr.Get()
	cfg.App.CompareAllowedRoots = []string{tmp}
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}

	w := doRequest(srv, "POST", "/api/compare/file-diff", map[string]any{
		"left_path":  left,
		"right_path": right,
	})
	if w.Code != 200 {
		t.Fatalf("白名单内应 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	// unified diff 里应能看到 -b/+c
	body := w.Body.String()
	if !strings.Contains(body, "-b") || !strings.Contains(body, "+c") {
		t.Errorf("diff 内容缺失: %s", body)
	}
}

// TestCompare_FileDiff_PathOutsideRoot_Rejected 路径穿越：白名单 root 是 /tmp/a，
// 传 /tmp/b/x.txt 应被 403 拒绝（按目录边界匹配，不匹配 /tmp/abc 这种前缀）。
func TestCompare_FileDiff_PathOutsideRoot_Rejected(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)
	tmp := t.TempDir()
	rootA := filepath.Join(tmp, "a")
	rootB := filepath.Join(tmp, "b")
	if err := os.MkdirAll(rootA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rootB, 0o755); err != nil {
		t.Fatal(err)
	}
	left := filepath.Join(rootA, "l.txt")
	right := filepath.Join(rootB, "r.txt")
	_ = os.WriteFile(left, []byte("x"), 0o600)
	_ = os.WriteFile(right, []byte("x"), 0o600)

	cfg := mgr.Get()
	// 只放行 rootA
	cfg.App.CompareAllowedRoots = []string{rootA}
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}

	// right_path 在 rootB 下 → 403
	w := doRequest(srv, "POST", "/api/compare/file-diff", map[string]any{
		"left_path":  left,
		"right_path": right,
	})
	if w.Code != 403 {
		t.Errorf("右路径越界应 403，得到 %d body=%s", w.Code, w.Body.String())
	}
}

// TestCompare_FileDiff_ExplicitAnyAllowed 显式 "*" 放行所有路径（用户明确同意）。
func TestCompare_FileDiff_ExplicitAnyAllowed(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)
	tmp := t.TempDir()
	left := filepath.Join(tmp, "l.txt")
	right := filepath.Join(tmp, "r.txt")
	_ = os.WriteFile(left, []byte("x"), 0o600)
	_ = os.WriteFile(right, []byte("x"), 0o600)

	cfg := mgr.Get()
	cfg.App.CompareAllowedRoots = []string{"*"}
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}

	w := doRequest(srv, "POST", "/api/compare/file-diff", map[string]any{
		"left_path":  left,
		"right_path": right,
	})
	if w.Code != 200 {
		t.Errorf("显式 * 应 200，得到 %d body=%s", w.Code, w.Body.String())
	}
}
