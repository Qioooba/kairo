package waspack

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParseManifest(t *testing.T) {
	raw := `
# comment
./CreditManage/CreditApply/FixPrice/MiniFixPriceApplyList.jsp
./src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java
./WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.class
src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java
credit/Common/WorkFlow/CreditApplyList.jsp
C:\ideaSpaces\credit\src\com\amarsoft\app\lending\bizlets\CommonFlowTask.java
`
	got, err := ParseManifest(raw, `C:\ideaSpaces\credit`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"./CreditManage/CreditApply/FixPrice/MiniFixPriceApplyList.jsp",
		"./src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java",
		"./WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.class",
		"./Common/WorkFlow/CreditApplyList.jsp",
		"./src/com/amarsoft/app/lending/bizlets/CommonFlowTask.java",
	}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Rel != want[i] {
			t.Errorf("[%d] %s want %s", i, got[i].Rel, want[i])
		}
	}
}

func TestParseManifestStripsWebRootPrefix(t *testing.T) {
	got, err := ParseManifest("./WebRoot/CreditManage/CreditLine/ProductInfo.jsp\n", `C:\ideaSpaces\credit`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Rel != "./CreditManage/CreditLine/ProductInfo.jsp" {
		t.Fatalf("%+v", got)
	}
}

func TestParseManifestRejectsTraversal(t *testing.T) {
	cases := []string{
		"../etc/passwd",
		"./src/../../secret.java",
		"/opt/IBM/WebSphere/a.class",
		`C:\Windows\a.java`,
	}
	for _, c := range cases {
		if _, err := ParseManifest(c, `C:\ideaSpaces\credit`); err == nil {
			t.Errorf("应拒绝 %q", c)
		}
	}
}

func ideaTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java":                     "class Company {}",
		"src/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.java":                        "class Mini {}",
		"WebRoot/WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.class": "CLS-C",
		"WebRoot/WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.class":   "CLS",
		"WebRoot/WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo$1.class": "INNER",
		"WebRoot/WEB-INF/web.xml": "<web-app/>",
		"WebRoot/CreditManage/CreditApply/FixPrice/MiniFixPriceApplyList.jsp": "jsp1",
		"WebRoot/CreditManage/CreditLine/ProductInfo.jsp":                     "jsp2",
	})
	return root
}

func TestResolveIntelliJWebRoot(t *testing.T) {
	root := ideaTree(t)
	listed, err := ParseManifest(`
./CreditManage/CreditApply/FixPrice/MiniFixPriceApplyList.jsp
./CreditManage/CreditLine/ProductInfo.jsp
./src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java
./WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.class
`, root)
	if err != nil {
		t.Fatal(err)
	}
	pv, err := Resolve(root, listed, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Missing) != 0 {
		t.Fatalf("missing: %+v", pv.Missing)
	}
	layout := DetectLayout(root)
	rels := map[string]bool{}
	for _, f := range pv.Files {
		rels[f.Rel] = true
		clean := strings.TrimPrefix(f.Rel, "./")
		if strings.HasPrefix(clean, "src/") {
			if !strings.HasPrefix(f.Abs, layout.SrcDir) {
				t.Errorf("java 应落在 src: %s -> %s", f.Rel, f.Abs)
			}
		} else if !strings.HasPrefix(f.Abs, layout.WebRoot) {
			t.Errorf("jsp/class 应落在 WebRoot: %s -> %s", f.Rel, f.Abs)
		}
	}
	for _, want := range []string{
		"./CreditManage/CreditApply/FixPrice/MiniFixPriceApplyList.jsp",
		"./CreditManage/CreditLine/ProductInfo.jsp",
		"./src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java",
		"./WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.class",
		"./WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo$1.class",
		"./src/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.java",
	} {
		if !rels[want] {
			t.Errorf("缺少 %s, got %+v", want, rels)
		}
	}
}

func TestDetectLayoutWhenWebRootSelected(t *testing.T) {
	root := ideaTree(t)
	layout := DetectLayout(filepath.Join(root, "WebRoot"))
	if layout.Root != root {
		t.Fatalf("Root=%s want %s", layout.Root, root)
	}
	if layout.WebRoot != filepath.Join(root, "WebRoot") {
		t.Fatalf("WebRoot=%s", layout.WebRoot)
	}
}

func TestBuildScriptsMatchBankFormat(t *testing.T) {
	project := ideaTree(t)
	out := filepath.Join(t.TempDir(), "pack")
	pkg := "TT20260709qijunV1"
	res, err := Build(Request{
		ProjectDir:  project,
		OutputDir:   out,
		PackageName: pkg,
		Manifest:    "./CreditManage/CreditLine/ProductInfo.jsp\n./src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java\n",
		AutoPair:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.CreatedDir {
		t.Fatal("应自动创建空目录")
	}
	for _, name := range []string{ListFileName, TarFileName(pkg), BackupScriptName(pkg), ExecuteScriptName(pkg)} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatalf("缺少 %s: %v", name, err)
		}
	}

	listBody, _ := os.ReadFile(filepath.Join(out, ListFileName))
	list := string(listBody)
	if !strings.Contains(list, "./CreditManage/CreditLine/ProductInfo.jsp") ||
		!strings.Contains(list, "./src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java") ||
		!strings.Contains(list, "./WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.class") {
		t.Fatalf("list.txt 内容不对: %s", list)
	}

	bak, _ := os.ReadFile(filepath.Join(out, BackupScriptName(pkg)))
	bakS := string(bak)
	if strings.Contains(bakS, "\r") {
		t.Fatal("备份脚本不应含 CR")
	}
	if strings.Contains(bakS, "#!/bin/sh") || strings.Contains(bakS, "WAS_APP_ROOT") {
		t.Fatalf("备份脚本不该再带 WAS 包装: %s", bakS)
	}
	wantBakPrefix := "tar -cvf Bak" + pkg + ".tar " + BackupHomePrefix + "/"
	if !strings.HasPrefix(bakS, wantBakPrefix) {
		t.Fatalf("备份脚本开头不对:\n%s", bakS)
	}
	if !strings.Contains(bakS, BackupHomePrefix+"/CreditManage/CreditLine/ProductInfo.jsp") {
		t.Fatalf("备份脚本缺 jsp: %s", bakS)
	}
	if strings.Contains(bakS, "./CreditManage") {
		t.Fatal("备份脚本里不应出现 ./ 相对路径")
	}

	exe, _ := os.ReadFile(filepath.Join(out, ExecuteScriptName(pkg)))
	exeS := string(exe)
	if strings.Contains(exeS, "\r") {
		t.Fatal("执行脚本不应含 CR")
	}
	if !strings.HasPrefix(exeS, "tar -cvf "+pkg+".tar ./") {
		t.Fatalf("执行脚本开头不对:\n%s", exeS)
	}
	if strings.Contains(exeS, BackupHomePrefix) {
		t.Fatal("执行脚本不应带 $HOME/credit.ear")
	}
	if !strings.Contains(exeS, "./CreditManage/CreditLine/ProductInfo.jsp") {
		t.Fatalf("执行脚本缺 jsp: %s", exeS)
	}

	f, err := os.Open(filepath.Join(out, TarFileName(pkg)))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	names := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		names[hdr.Name] = string(b)
		if !strings.HasPrefix(hdr.Name, "./") {
			t.Errorf("tar 条目应以 ./ 开头: %s", hdr.Name)
		}
	}
	if names["./CreditManage/CreditLine/ProductInfo.jsp"] != "jsp2" {
		t.Fatalf("tar jsp: %+v", names)
	}
	if names["./src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java"] != "class Company {}" {
		t.Fatalf("tar java: %+v", names)
	}

	if _, err := Build(Request{
		ProjectDir:  project,
		OutputDir:   out,
		PackageName: pkg,
		Manifest:    "./CreditManage/CreditLine/ProductInfo.jsp\n",
		AutoPair:    false,
	}); err == nil {
		t.Fatal("非空目录应拒绝覆盖")
	}
}

func TestBuildMissingFile(t *testing.T) {
	project := t.TempDir()
	writeTree(t, project, map[string]string{"WebRoot/WEB-INF/web.xml": "<web/>"})
	_, err := Build(Request{
		ProjectDir: project,
		OutputDir:  filepath.Join(t.TempDir(), "out"),
		Manifest:   "./src/missing.java\n",
		AutoPair:   true,
	})
	if err == nil {
		t.Fatal("缺文件应失败")
	}
}

func TestSanitizePackageName(t *testing.T) {
	got, err := SanitizePackageName("TT20260709qijunV1.tar")
	if err != nil || got != "TT20260709qijunV1" {
		t.Fatalf("got %q err %v", got, err)
	}
	got, err = SanitizePackageName("TT20260709qijunV1.sh")
	if err != nil || got != "TT20260709qijunV1" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := SanitizePackageName("../x"); err == nil {
		t.Fatal("应拒绝路径包名")
	}
}
