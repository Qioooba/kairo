package httpserver

import (
	"archive/tar"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWASPackPreviewAndBuild(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	project := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		abs := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("src/cn/com/jscb/Foo.java", "class Foo {}")
	mustWrite("WebRoot/WEB-INF/classes/cn/com/jscb/Foo.class", "CLS")
	mustWrite("WebRoot/CreditManage/CreditLine/ProductInfo.jsp", "<% %>")

	manifest := "./src/cn/com/jscb/Foo.java\n./CreditManage/CreditLine/ProductInfo.jsp\n"
	if w := doRequest(srv, "GET", "/api/waspack/preview", nil); w.Code != 405 {
		t.Fatalf("GET preview: %d", w.Code)
	}
	w := doRequest(srv, "POST", "/api/waspack/preview", map[string]any{
		"project_dir": project,
		"manifest":    manifest,
		"auto_pair":   true,
	})
	if w.Code != 200 {
		t.Fatalf("preview %d: %s", w.Code, w.Body.String())
	}

	out := filepath.Join(t.TempDir(), "tt_pack")
	w = doRequest(srv, "POST", "/api/waspack/build", map[string]any{
		"project_dir":  project,
		"output_dir":   out,
		"package_name": "TT20260709qijunV1",
		"manifest":     manifest,
		"auto_pair":    true,
	})
	if w.Code != 200 {
		t.Fatalf("build %d: %s", w.Code, w.Body.String())
	}
	var res map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if _, ok := res["was_app_root"]; ok {
		t.Fatal("响应不应再带 was_app_root")
	}
	if res["execute_script"] == nil {
		t.Fatalf("缺 execute_script: %s", w.Body.String())
	}

	for _, name := range []string{"list.txt", "TT20260709qijunV1.tar", "TT20260709qijunV1.sh", "BakTT20260709qijunV1.sh"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(filepath.Join(out, "TT20260709qijunV1.tar"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
		_, _ = io.Copy(io.Discard, tr)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "./src/cn/com/jscb/Foo.java") ||
		!strings.Contains(joined, "./WEB-INF/classes/cn/com/jscb/Foo.class") ||
		!strings.Contains(joined, "./CreditManage/CreditLine/ProductInfo.jsp") {
		t.Fatalf("tar names: %v", names)
	}

	w = doRequest(srv, "POST", "/api/waspack/build", map[string]any{
		"project_dir": project,
		"output_dir":  out,
		"manifest":    manifest,
	})
	if w.Code != 400 {
		t.Fatalf("非空目录应 400, 实际 %d %s", w.Code, w.Body.String())
	}
}

func TestWASPackPreviewRejectsEmpty(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/waspack/preview", map[string]any{
		"project_dir": t.TempDir(),
		"manifest":    "   \n",
	})
	if w.Code != 400 {
		t.Fatalf("空清单应 400, 实际 %d", w.Code)
	}
}

func TestWASPackPackageRequiresStageToken(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	project := t.TempDir()
	classPath := filepath.Join(project, "WebRoot", "WEB-INF", "classes", "demo", "Foo.class")
	if err := os.MkdirAll(filepath.Dir(classPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(classPath, []byte("CLS"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "package-stage")
	body := map[string]any{
		"project_dir":  project,
		"output_dir":   out,
		"package_name": "Demo",
		"manifest":     "./WEB-INF/classes/demo/Foo.class",
		"auto_pair":    false,
	}
	extracted := doRequest(srv, "POST", "/api/waspack/extract", body)
	if extracted.Code != 200 {
		t.Fatalf("extract %d: %s", extracted.Code, extracted.Body.String())
	}

	packaged := doRequest(srv, "POST", "/api/waspack/package", body)
	if packaged.Code != 400 || !strings.Contains(packaged.Body.String(), "阶段凭据") {
		t.Fatalf("空 stage_token 应拒绝，实际 %d %s", packaged.Code, packaged.Body.String())
	}
}

func TestWASPackOpenFolder(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	if w := doRequest(srv, "GET", "/api/waspack/open", nil); w.Code != 405 {
		t.Fatalf("GET open: %d", w.Code)
	}
	if w := doRequest(srv, "POST", "/api/waspack/open", map[string]any{"output_dir": ""}); w.Code != 400 {
		t.Fatalf("空路径应 400, 实际 %d %s", w.Code, w.Body.String())
	}
	missing := filepath.Join(t.TempDir(), "no-such-pack")
	if w := doRequest(srv, "POST", "/api/waspack/open", map[string]any{"output_dir": missing}); w.Code != 400 {
		t.Fatalf("不存在应 400, 实际 %d %s", w.Code, w.Body.String())
	}
	dir := t.TempDir()
	w := doRequest(srv, "POST", "/api/waspack/open", map[string]any{"output_dir": dir})
	if w.Code != 200 {
		t.Fatalf("打开已有目录应 200, 实际 %d %s", w.Code, w.Body.String())
	}
}
