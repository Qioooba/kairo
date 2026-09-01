package wscodegen

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSuggestProjectSourceDir_WebInfLib(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	lib := filepath.Join(root, "WebRoot", "WEB-INF", "lib")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	got, notes, err := SuggestProjectSourceDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Fatalf("got %s want %s notes=%v", got, src, notes)
	}
}

func TestSuggestProjectSourceDir_Maven(t *testing.T) {
	root := t.TempDir()
	java := filepath.Join(root, "src", "main", "java")
	if err := os.MkdirAll(java, 0o755); err != nil {
		t.Fatal(err)
	}
	got, _, err := SuggestProjectSourceDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != java {
		t.Fatalf("got %s want %s", got, java)
	}
}

func TestSuggestProjectSourceDir_CreatesSrc(t *testing.T) {
	root := t.TempDir()
	got, notes, err := SuggestProjectSourceDir(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "src")
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "没有现成 src") {
		t.Fatalf("notes=%v", notes)
	}
}

func TestScanProject_SuggestedSrc(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	lib := filepath.Join(root, "WEB-INF", "lib")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "axis.jar"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ScanProject(lib)
	if err != nil {
		t.Fatal(err)
	}
	if res.SuggestedSrc != src {
		t.Fatalf("suggested_src=%s want %s", res.SuggestedSrc, src)
	}
}

func TestZipFiles_AndRejectTraversal(t *testing.T) {
	data, err := ZipFiles([]GeneratedFile{
		{RelPath: "com/demo/A.java", Content: "class A {}"},
		{RelPath: "com/demo/A.java", Content: "class A duplicate {}"},
		{RelPath: "com/demo/A_2.java", Content: "class A_2 original {}"},
		{RelPath: "com/demo/A.java", Content: "class A third {}"},
		{RelPath: "com/demo/B.java", Content: "class B {}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	names := fileNames(zr)
	if len(names) != 5 {
		t.Fatalf("files=%d names=%v", len(names), names)
	}
	expected := []string{"com/demo/A.java", "com/demo/A_2.java", "com/demo/A_2_2.java", "com/demo/A_3.java", "com/demo/B.java"}
	for i, name := range names {
		if name != expected[i] {
			t.Fatalf("at index %d got %s want %s (all: %v)", i, name, expected[i], names)
		}
	}
	if _, err := ZipFiles([]GeneratedFile{{RelPath: "../evil.java", Content: "x"}}); err == nil {
		t.Fatal("expected traversal error")
	}
}

func TestZipFileName(t *testing.T) {
	got := ZipFileName(Result{PackageName: "com.demo.cust", Engine: EnginePortable})
	if got != "cust-portable.zip" {
		t.Fatalf("got %s", got)
	}
}

func TestPrepareZip_Builtin(t *testing.T) {
	name, data, n, res, err := PrepareZip(t.Context(), Request{
		Engine:      EnginePortable,
		Mode:        ModeBuiltin,
		PackageName: "com.demo.cust",
		IncludeMain: true,
		JavaSource:  JavaSource16,
		WSDLContent: sampleWSDL,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n < 3 || !res.OK || !strings.HasSuffix(name, ".zip") {
		t.Fatalf("name=%s n=%d ok=%v", name, n, res.OK)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "CustomerServiceClient.java") {
			found = true
		}
	}
	if !found {
		t.Fatalf("zip missing client: %+v", fileNames(zr))
	}
}

func fileNames(zr *zip.Reader) []string {
	var out []string
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	return out
}

func TestZipGeneratedTree(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "com", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "A.java"), []byte("class A {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skip.bin"), []byte("xx"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, n, err := ZipGeneratedTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if zr.File[0].Name != "com/demo/A.java" {
		t.Fatalf("name=%s", zr.File[0].Name)
	}
}
