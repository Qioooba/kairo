package waspack

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
		"src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java":                      "class Company {}",
		"src/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.java":                         "class Mini {}",
		"WebRoot/WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.class": "CLS-C",
		"WebRoot/WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.class":    "CLS",
		"WebRoot/WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo$1.class":  "INNER",
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

func TestResolveInnerClassOnlyAddsOuterJavaAndSiblings(t *testing.T) {
	root := ideaTree(t)
	listed, err := ParseManifest("./WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo$1.class\n", root)
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
	got := map[string]bool{}
	for _, f := range pv.Files {
		got[f.Rel] = true
	}
	for _, want := range []string{
		"./src/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.java",
		"./WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.class",
		"./WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo$1.class",
	} {
		if !got[want] {
			t.Errorf("missing paired entry %s: %+v", want, got)
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
	wantBakPrefix := "tar -cvf Bak" + pkg + `.tar "$HOME"'/credit.ear/credit.war/`
	if !strings.HasPrefix(bakS, wantBakPrefix) {
		t.Fatalf("备份脚本开头不对:\n%s", bakS)
	}
	if !strings.Contains(bakS, `"$HOME"'/credit.ear/credit.war/CreditManage/CreditLine/ProductInfo.jsp'`) {
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
	if !strings.HasPrefix(exeS, "tar -cvf "+pkg+".tar './") {
		t.Fatalf("执行脚本开头不对:\n%s", exeS)
	}
	if strings.Contains(exeS, BackupHomePrefix) {
		t.Fatal("执行脚本不应带 $HOME/credit.ear")
	}
	if !strings.Contains(exeS, "'./CreditManage/CreditLine/ProductInfo.jsp'") {
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

func TestScriptsQuoteManifestPathsAsSingleShellArguments(t *testing.T) {
	rel := `./Credit Manage/a&b$(touch PWN)'s.jsp`
	files := []ResolvedFile{{Rel: rel}}
	wantLiteral := shellQuoteArg(rel)
	execScript := renderExecuteScript("TTsafe", files)
	if !strings.Contains(execScript, wantLiteral) {
		t.Fatalf("执行脚本未安全引用路径:\n%s\nwant token %s", execScript, wantLiteral)
	}
	backupToken := `"$HOME"` + shellQuoteArg(`/credit.ear/credit.war/`+relNoDot(rel))
	backupScript := renderBackupScript("TTsafe", files)
	if !strings.Contains(backupScript, backupToken) {
		t.Fatalf("备份脚本未安全引用路径:\n%s\nwant token %s", backupScript, backupToken)
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

func TestExtractThenPackageUsesEditedWARAndKeepsPaths(t *testing.T) {
	project := ideaTree(t)
	out := filepath.Join(t.TempDir(), "staged")
	req := Request{ProjectDir: project, OutputDir: out, PackageName: "TTstaged", Manifest: "./src/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.java\n", AutoPair: true}
	extracted, err := Extract(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"src/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.java",
		"WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.class",
		"WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo$1.class",
	} {
		if _, err := os.Stat(filepath.Join(extracted.WarDir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("抽取缺少 %s: %v", rel, err)
		}
	}
	edited := filepath.Join(extracted.WarDir, "CreditManage", "manual.jsp")
	writeTree(t, extracted.WarDir, map[string]string{"CreditManage/manual.jsp": "edited-after-extract"})
	res, err := PackageExtracted(req)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(res.TarFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	foundEdited := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(hdr.Name, "./") {
			t.Fatalf("包内路径变化: %s", hdr.Name)
		}
		if hdr.Name == "./CreditManage/manual.jsp" {
			body, _ := io.ReadAll(tr)
			foundEdited = string(body) == "edited-after-extract"
		}
	}
	if !foundEdited {
		t.Fatalf("抽取后新增/修改的文件没有进入 tar: %s", edited)
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

func TestIsSafeLocalPathRejectsSensitivePaths(t *testing.T) {
	cases := []string{
		"~",
		"~/test",
		`~\test`,
		`C:\Windows`,
		`C:\Windows\System32`,
		`C:\Program Files`,
		`C:\Program Files\App`,
		`C:\Program Files (x86)`,
		`C:\Program Files (x86)\App`,
		`C:\ProgramData`,
		`C:\ProgramData\test`,
		`c:/windows/system32`,
		`C:/Program Files/tool`,
		`C:\Users`,
		"/bin",
		"/bin/sh",
		"/sbin",
		"/etc",
		"/etc/nginx",
		"/usr",
		"/usr/local",
		"/var",
		"/var/log",
		"/boot",
		"/root",
		"/home",
		"/",
		`C:\`,
		"",
	}
	for _, foreign := range []string{`C:\Windows\System32`, `C:\`, `D:\work`} {
		if isSafeLocalPathForGOOS(foreign, "darwin") {
			t.Errorf("darwin accepted foreign Windows drive path %q", foreign)
		}
	}
	for _, c := range cases {
		if isSafeLocalPath(c) {
			t.Errorf("isSafeLocalPath(%q) should be false", c)
		}
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if isSafeLocalPath(home) {
			t.Errorf("isSafeLocalPath(%q) [user home] should be false", home)
		}
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		if isSafeLocalPath(cwd) {
			t.Errorf("isSafeLocalPath(%q) [cwd] should be false", cwd)
		}
	}

	// Normal valid directory should be safe
	valid := filepath.Join(t.TempDir(), "subfolder")
	if !isSafeLocalPath(valid) {
		t.Errorf("isSafeLocalPath(%q) should be true", valid)
	}
}

func TestOutputPolicyReplaceRequiresConfirmation(t *testing.T) {
	// 第三项为强制清空：无论是否为 Kairo 目录，只要二次确认都应清空。
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"user_file.txt": "do not delete"})
	abs, _, err := prepareOutputDirWithPolicy(dir, OutputPolicyReplace, true)
	if err != nil {
		t.Fatalf("expected force-clear success without marker, got: %v", err)
	}
	if ents, _ := os.ReadDir(abs); len(ents) != 0 {
		t.Fatalf("强制清空后目录应为空，剩余: %+v", ents)
	}

	// 未确认时仍应拒绝
	dir2 := t.TempDir()
	writeTree(t, dir2, map[string]string{"user_file.txt": "do not delete"})
	if _, _, err := prepareOutputDirWithPolicy(dir2, OutputPolicyReplace, false); err == nil {
		t.Fatalf("未确认时应拒绝覆盖")
	}

	// 确认后应允许，不依赖输出目录里的内部标记文件。
	if _, _, err = prepareOutputDirWithPolicy(dir2, OutputPolicyReplace, true); err != nil {
		t.Fatalf("expected success after confirmation, got: %v", err)
	}
}

func TestBuildRejectsProjectOverlapAndSymlinkParent(t *testing.T) {
	project := ideaTree(t)
	for _, output := range []string{project, filepath.Dir(project), filepath.Join(project, "out")} {
		_, err := Build(Request{ProjectDir: project, OutputDir: output, PackageName: "TT-overlap", Manifest: "./WEB-INF/web.xml\n"})
		if err == nil {
			t.Errorf("output %q should not overlap project", output)
		}
	}
	// A symlink/junction parent must be compared after canonicalisation. Some
	// Windows environments disallow symlink creation; skip only that setup.
	linkRoot := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(project, linkRoot); err == nil {
		_, err = Build(Request{ProjectDir: project, OutputDir: filepath.Join(linkRoot, "out"), PackageName: "TT-link", Manifest: "./WEB-INF/web.xml\n"})
		if err == nil {
			t.Fatal("output under symlinked project should be rejected")
		}
	}
}

func TestLargeBatchScriptsUseTarListFile(t *testing.T) {
	files := make([]ResolvedFile, 0, 5000)
	for i := 0; i < 5000; i++ {
		files = append(files, ResolvedFile{Rel: fmt.Sprintf("./module/file-%04d-%s.xml", i, strings.Repeat("x", 18))})
	}
	if got := renderBatchExecuteScript("DDDlarge", files); !strings.Contains(got, "-T list.txt") || len(got) > 256 {
		t.Fatalf("large batch script should avoid argv expansion: %q", got)
	}
	if got := renderBatchBackupScript("DDDlarge", files); !strings.Contains(got, "-T list.txt") || len(got) > 256 {
		t.Fatalf("large batch backup should avoid argv expansion: %q", got)
	}
	if got, err := renderBatchBackupScriptAtRoot("DDDlarge", files, "/batch/credit"); err != nil || !strings.Contains(got, "SCRIPT_DIR=") || !strings.Contains(got, `-T "$SCRIPT_DIR/list.txt"`) || !strings.Contains(got, `"$SCRIPT_DIR/BakDDDlarge.tar"`) {
		t.Fatalf("large batch backup should use deployment root/list: %q, %v", got, err)
	}
	if got, err := renderBatchBackupScriptAtRoot("DDDsafe", []ResolvedFile{{Rel: "./scripts/safe name's.sh"}}, "/batch/deploy root"); err != nil || !strings.Contains(got, `tar -C '/batch/deploy root'`) || strings.Contains(got, "$(touch") {
		t.Fatalf("backup script root/path quoting unsafe: %q, %v", got, err)
	}
}

func TestOutputPolicyCleanKnownArtifactsWithoutMarker(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"list.txt":               "old list",
		"chmod.txt":              "old chmod",
		"Demo.tar":               "old tar",
		"Demo.sh":                "old execute",
		"BakDemo.sh":             "old backup",
		"Demo.zip":               "old zip",
		".kairo-waspack.json":    "legacy marker",
		"keep-user-file.txt":     "must stay",
		"other-package-data.tar": "must stay",
	})
	writeTree(t, filepath.Join(dir, ExtractedWARDirName), map[string]string{"WEB-INF/a.class": "old"})

	abs, _, err := prepareOutputDirWithPolicy(dir, OutputPolicyCleanOwned, false, "Demo")
	if err != nil {
		t.Fatalf("clean known artifacts without marker: %v", err)
	}
	for _, name := range []string{"list.txt", "chmod.txt", "Demo.tar", "Demo.sh", "BakDemo.sh", "Demo.zip", ".kairo-waspack.json", ExtractedWARDirName} {
		if _, err := os.Stat(filepath.Join(abs, name)); !os.IsNotExist(err) {
			t.Errorf("known artifact %q should be removed, got: %v", name, err)
		}
	}
	for _, name := range []string{"keep-user-file.txt", "other-package-data.tar"} {
		if _, err := os.Stat(filepath.Join(abs, name)); err != nil {
			t.Errorf("unrelated file %q should be preserved: %v", name, err)
		}
	}
}

func TestOutputPolicyCleanRejectsUnownedKnownNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ListFileName), []byte("user list"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareOutputDirWithPolicy(dir, OutputPolicyCleanOwned, false, "Demo"); err == nil {
		t.Fatal("clean policy must not delete an unowned list.txt")
	}
}

func TestScopedOwnershipReconcilesPackageSwitchAndPreservesUserFiles(t *testing.T) {
	project := ideaTree(t)
	out := filepath.Join(t.TempDir(), "out")
	metadata := filepath.Join(t.TempDir(), "metadata")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	base := Request{ProjectDir: project, OutputDir: out, Manifest: "./WEB-INF/web.xml\n", AutoPair: true, MetadataDir: metadata}
	first := base
	first.PackageName = "Alpha"
	if _, err := Build(first); err != nil {
		t.Fatalf("build Alpha: %v", err)
	}
	if err := os.WriteFile(filepath.Join(out, "keep.txt"), []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := base
	second.PackageName = "Beta"
	second.OutputPolicy = OutputPolicyCleanOwned
	if _, err := Build(second); err != nil {
		t.Fatalf("clean build Beta: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "Alpha.tar")); !os.IsNotExist(err) {
		t.Fatalf("old Alpha artifact should be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "Beta.tar")); err != nil {
		t.Fatalf("new Beta artifact missing: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(out, "keep.txt")); err != nil || string(body) != "user" {
		t.Fatalf("unrelated user file changed: %q %v", body, err)
	}
	// A subsequent replacement drops Alpha from ownership. A later user file
	// with that name must therefore not be deleted by another clean build.
	third := base
	third.PackageName = "Gamma"
	third.OutputPolicy = OutputPolicyReplace
	third.ConfirmReplace = true
	third.ReplaceToken = "test-confirmation"
	third.ReplaceAuthorized = true
	if _, err := Build(third); err != nil {
		t.Fatalf("replace build Gamma: %v", err)
	}
	if err := os.WriteFile(filepath.Join(out, "Alpha.tar"), []byte("user alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	fourth := base
	fourth.PackageName = "Delta"
	fourth.OutputPolicy = OutputPolicyCleanOwned
	if _, err := Build(fourth); err != nil {
		t.Fatalf("clean build Delta: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(out, "Alpha.tar")); err != nil || string(body) != "user alpha" {
		t.Fatalf("unowned recreated Alpha artifact changed: %q %v", body, err)
	}
}

func TestCleanOwnedRejectsNewPackageNameCollision(t *testing.T) {
	project := ideaTree(t)
	out := filepath.Join(t.TempDir(), "out")
	req := Request{ProjectDir: project, OutputDir: out, Manifest: "./WEB-INF/web.xml\n", PackageName: "Alpha", MetadataDir: t.TempDir()}
	if _, err := Build(req); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(out, "Beta.tar")
	if err := os.WriteFile(name, []byte("user archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	req.PackageName = "Beta"
	req.OutputPolicy = OutputPolicyCleanOwned
	if _, err := Build(req); err == nil {
		t.Fatal("new package overwrote an unowned filename")
	}
	if data, err := os.ReadFile(name); err != nil || string(data) != "user archive" {
		t.Fatalf("user file changed: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(out, "Alpha.tar")); err != nil {
		t.Fatal("failed validation removed previous package", err)
	}
}

func TestScopedOwnershipPackageKeepsExtractedWAR(t *testing.T) {
	project := ideaTree(t)
	out := filepath.Join(t.TempDir(), "out")
	metadata := filepath.Join(t.TempDir(), "metadata")
	req := Request{ProjectDir: project, OutputDir: out, PackageName: "Demo", Manifest: "./WEB-INF/web.xml\n", MetadataDir: metadata}
	extracted, err := Extract(req)
	if err != nil {
		t.Fatal(err)
	}
	if extracted.StageToken == "" {
		t.Fatal("expected stage token")
	}
	packageReq := req
	packageReq.OutputPolicy = OutputPolicyCleanOwned
	packageReq.StageToken = extracted.StageToken
	result, err := PackageExtracted(packageReq)
	if err != nil {
		t.Fatal(err)
	}
	if result.WarDir == "" {
		t.Fatal("package result should retain WAR path")
	}
	if _, err := os.Stat(filepath.Join(out, ExtractedWARDirName, "WEB-INF", "web.xml")); err != nil {
		t.Fatalf("clean named package must preserve WAR: %v", err)
	}
}

func TestScopedOwnershipCorruptMetadataFailsClosed(t *testing.T) {
	project := ideaTree(t)
	out := filepath.Join(t.TempDir(), "out")
	metadata := filepath.Join(t.TempDir(), "metadata")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(metadata, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, "waspack-ownership.json"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "user.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Build(Request{ProjectDir: project, OutputDir: out, PackageName: "Demo", Manifest: "./WEB-INF/web.xml\n", MetadataDir: metadata, OutputPolicy: OutputPolicyCleanOwned})
	if err == nil || !strings.Contains(err.Error(), "归属") {
		t.Fatalf("corrupt ownership should fail closed: %v", err)
	}
	if body, readErr := os.ReadFile(filepath.Join(out, "user.txt")); readErr != nil || string(body) != "keep" {
		t.Fatalf("user output changed after corrupt metadata: %q %v", body, readErr)
	}
}

func TestExtractPublicationFailureRestoresPreviousOutput(t *testing.T) {
	project := ideaTree(t)
	out := filepath.Join(t.TempDir(), "out")
	writeTree(t, out, map[string]string{"war/old.txt": "previous", "keep.txt": "user"})
	originalRename := waspackRename
	waspackRename = func(src, dst string) error {
		if strings.Contains(src, ".kairo-waspack-stage-") {
			return fmt.Errorf("injected publish failure")
		}
		return originalRename(src, dst)
	}
	defer func() { waspackRename = originalRename }()
	_, err := Extract(Request{ProjectDir: project, OutputDir: out, PackageName: "TTrestore", Manifest: "./WEB-INF/web.xml\n", OutputPolicy: OutputPolicyReplace, ConfirmReplace: true, ReplaceToken: "test-confirmation", ReplaceAuthorized: true})
	if err == nil {
		t.Fatal("expected injected publication failure")
	}
	if body, readErr := os.ReadFile(filepath.Join(out, "war", "old.txt")); readErr != nil || string(body) != "previous" {
		t.Fatalf("old war lost: %q %v", body, readErr)
	}
	if body, readErr := os.ReadFile(filepath.Join(out, "keep.txt")); readErr != nil || string(body) != "user" {
		t.Fatalf("user file lost: %q %v", body, readErr)
	}
}

func TestZipCleanRefusesUnownedExistingZip(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	writeTree(t, out, map[string]string{"war/file.txt": "content", "Demo.zip": "user-owned"})
	_, err := BuildZip(Request{OutputDir: out, PackageName: "Demo", OutputPolicy: OutputPolicyCleanOwned})
	if err == nil {
		t.Fatal("clean ZIP should reject an unowned existing archive")
	}
	if body, readErr := os.ReadFile(filepath.Join(out, "Demo.zip")); readErr != nil || string(body) != "user-owned" {
		t.Fatalf("unowned zip changed: %q %v", body, readErr)
	}
}

func TestZipPublicationFailureRestoresPreviousZip(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	writeTree(t, out, map[string]string{"war/file.txt": "content", "Demo.zip": "previous-zip"})
	originalRename := waspackRename
	waspackRename = func(src, dst string) error {
		if strings.Contains(src, ".kairo-waspack-stage-") {
			return fmt.Errorf("injected zip publish failure")
		}
		return originalRename(src, dst)
	}
	defer func() { waspackRename = originalRename }()
	_, err := BuildZip(Request{OutputDir: out, PackageName: "Demo", OutputPolicy: OutputPolicyReplace, ConfirmReplace: true, ReplaceToken: "test-confirmation", ReplaceAuthorized: true})
	if err == nil {
		t.Fatal("expected injected ZIP publication failure")
	}
	if body, readErr := os.ReadFile(filepath.Join(out, "Demo.zip")); readErr != nil || string(body) != "previous-zip" {
		t.Fatalf("old zip lost: %q %v", body, readErr)
	}
}

func TestBuildPublicationFailureRestoresPreviousArtifacts(t *testing.T) {
	project := ideaTree(t)
	out := filepath.Join(t.TempDir(), "out")
	pkg := "TTrestore"
	writeTree(t, out, map[string]string{
		"list.txt":          "previous-list",
		pkg + ".tar":        "previous-tar",
		pkg + ".sh":         "previous-exec",
		"Bak" + pkg + ".sh": "previous-backup",
	})
	originalRename := waspackRename
	waspackRename = func(src, dst string) error {
		if strings.Contains(src, ".kairo-waspack-stage-") {
			return fmt.Errorf("injected build publish failure")
		}
		return originalRename(src, dst)
	}
	defer func() { waspackRename = originalRename }()
	_, err := Build(Request{ProjectDir: project, OutputDir: out, PackageName: pkg, Manifest: "./WEB-INF/web.xml\n", OutputPolicy: OutputPolicyReplace, ConfirmReplace: true, ReplaceToken: "test-confirmation", ReplaceAuthorized: true})
	if err == nil {
		t.Fatal("expected injected build publication failure")
	}
	for name, want := range map[string]string{"list.txt": "previous-list", pkg + ".tar": "previous-tar", pkg + ".sh": "previous-exec", "Bak" + pkg + ".sh": "previous-backup"} {
		body, readErr := os.ReadFile(filepath.Join(out, name))
		if readErr != nil || string(body) != want {
			t.Errorf("old artifact %s lost: %q %v", name, body, readErr)
		}
	}
}

func TestPublicationRollbackFailureRetainsRecoveryBackup(t *testing.T) {
	project := ideaTree(t)
	out := filepath.Join(t.TempDir(), "out")
	writeTree(t, out, map[string]string{"war/old.txt": "previous"})
	originalRename := waspackRename
	waspackRename = func(src, dst string) error {
		if strings.Contains(src, ".kairo-waspack-stage-") || strings.Contains(src, ".kairo-waspack-old-") {
			return fmt.Errorf("injected rename failure")
		}
		return originalRename(src, dst)
	}
	defer func() { waspackRename = originalRename }()
	_, err := Extract(Request{ProjectDir: project, OutputDir: out, PackageName: "TTrestore", Manifest: "./WEB-INF/web.xml\n", OutputPolicy: OutputPolicyReplace, ConfirmReplace: true, ReplaceToken: "test-confirmation", ReplaceAuthorized: true})
	if err == nil || !strings.Contains(err.Error(), "备份保留于") {
		t.Fatalf("expected recoverable rollback error, got %v", err)
	}
}

func TestSafeTarZipRelRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../secret.txt", "./../../secret.txt", "/absolute.txt", `..\secret.txt`} {
		if _, err := safeTarZipRel(name); err == nil {
			t.Errorf("safeTarZipRel(%q) should reject traversal/absolute path", name)
		}
	}
	if got, err := safeTarZipRel("./WEB-INF/classes/A.class"); err != nil || got != "WEB-INF/classes/A.class" {
		t.Fatalf("safe tar path mismatch: got=%q err=%v", got, err)
	}
}

func TestRenderChmodScriptRejectsUnsafeDeploymentPath(t *testing.T) {
	files := []ResolvedFile{{Rel: "./scripts/run.sh"}}
	for _, base := range []string{"batch/credit", "/batch/../credit", "/batch//credit", "/batch/credit\n$(touch PWN)", `C:\batch\credit`, "/"} {
		if _, err := renderChmodScriptSafe(base, files, "755"); err == nil {
			t.Errorf("unsafe base %q should be rejected", base)
		}
	}
	content, err := renderChmodScriptSafe("/batch/credit", []ResolvedFile{{Rel: "./scripts/safe name.sh"}}, "755")
	if err != nil || !strings.Contains(content, "chmod 755 '/batch/credit/scripts/safe name.sh'") {
		t.Fatalf("safe quoting failed: %q, %v", content, err)
	}
}

func TestT062_RenameFailureRollbackAndUserFilesPreserved(t *testing.T) {
	project := ideaTree(t)
	out := filepath.Join(t.TempDir(), "out")
	pkg := "T062Pkg"
	writeTree(t, out, map[string]string{
		"list.txt":            "previous-list",
		pkg + ".tar":          "previous-tar",
		".kairo-waspack.json": "{}",
		"custom.txt":          "important-user-content",
		"notes.md":            "# User Notes",
	})

	originalRename := waspackRename
	waspackRename = func(src, dst string) error {
		if strings.HasSuffix(dst, pkg+".tar") && strings.Contains(src, ".kairo-waspack-stage-") {
			return fmt.Errorf("injected rename failure for tar file")
		}
		return originalRename(src, dst)
	}
	defer func() { waspackRename = originalRename }()

	_, err := Build(Request{
		ProjectDir:        project,
		OutputDir:         out,
		PackageName:       pkg,
		Manifest:          "./WEB-INF/web.xml\n",
		OutputPolicy:      OutputPolicyCleanOwned,
		ConfirmReplace:    true,
		ReplaceToken:      "test-confirmation",
		ReplaceAuthorized: true,
	})
	if err == nil {
		t.Fatal("expected build failure due to injected rename failure")
	}

	// 1. Verify user-owned files were NEVER touched or modified
	if body, readErr := os.ReadFile(filepath.Join(out, "custom.txt")); readErr != nil || string(body) != "important-user-content" {
		t.Fatalf("user file custom.txt modified or lost: %q %v", body, readErr)
	}
	if body, readErr := os.ReadFile(filepath.Join(out, "notes.md")); readErr != nil || string(body) != "# User Notes" {
		t.Fatalf("user file notes.md modified or lost: %q %v", body, readErr)
	}

	// 2. Verify previous artifacts were rolled back to out
	if body, readErr := os.ReadFile(filepath.Join(out, "list.txt")); readErr != nil || string(body) != "previous-list" {
		t.Fatalf("previous list.txt not restored: %q %v", body, readErr)
	}
	if body, readErr := os.ReadFile(filepath.Join(out, pkg+".tar")); readErr != nil || string(body) != "previous-tar" {
		t.Fatalf("previous tar not restored: %q %v", body, readErr)
	}

	// 3. Test scenario where rollback itself fails: backup location must be retained and reported
	out2 := filepath.Join(t.TempDir(), "out2")
	writeTree(t, out2, map[string]string{
		"list.txt":            "previous-list-2",
		pkg + ".tar":          "previous-tar-2",
		".kairo-waspack.json": "{}",
	})
	waspackRename = func(src, dst string) error {
		if strings.Contains(src, ".kairo-waspack-stage-") || strings.Contains(src, ".kairo-waspack-old-") {
			return fmt.Errorf("injected catastrophic rename failure")
		}
		return originalRename(src, dst)
	}
	_, err2 := Build(Request{
		ProjectDir:        project,
		OutputDir:         out2,
		PackageName:       pkg,
		Manifest:          "./WEB-INF/web.xml\n",
		OutputPolicy:      OutputPolicyCleanOwned,
		ConfirmReplace:    true,
		ReplaceToken:      "test-confirmation",
		ReplaceAuthorized: true,
	})
	if err2 == nil || !strings.Contains(err2.Error(), "旧产物备份保留于") {
		t.Fatalf("expected error mentioning backup location, got: %v", err2)
	}
}

func TestT063_BackupCleanupFailureProducesWarning(t *testing.T) {
	project := ideaTree(t)
	out := filepath.Join(t.TempDir(), "out")
	pkg := "T063Pkg"
	writeTree(t, out, map[string]string{
		"list.txt":            "old-list",
		pkg + ".tar":          "old-tar",
		".kairo-waspack.json": "{}",
	})

	originalRemoveAll := waspackRemoveAll
	waspackRemoveAll = func(path string) error {
		if strings.Contains(path, ".kairo-waspack-old-") {
			return fmt.Errorf("injected permission denied when deleting backup dir")
		}
		return originalRemoveAll(path)
	}
	defer func() { waspackRemoveAll = originalRemoveAll }()

	res, err := Build(Request{
		ProjectDir:        project,
		OutputDir:         out,
		PackageName:       pkg,
		Manifest:          "./WEB-INF/web.xml\n",
		OutputPolicy:      OutputPolicyCleanOwned,
		ConfirmReplace:    true,
		ReplaceToken:      "test-confirmation",
		ReplaceAuthorized: true,
	})
	if err != nil {
		t.Fatalf("build should succeed with warning when backup cleanup fails, got error: %v", err)
	}
	if !res.OK {
		t.Fatalf("res.OK should be true, got false")
	}

	// Output dir must contain new published artifacts
	if body, readErr := os.ReadFile(filepath.Join(out, "list.txt")); readErr != nil || string(body) == "old-list" {
		t.Fatalf("expected new list.txt in output dir, got err=%v body=%q", readErr, body)
	}

	// Must contain warning indicating backup removal failed and where backup is located
	hasWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "旧产物备份清理失败") && strings.Contains(w, ".kairo-waspack-old-") {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Fatalf("expected warning about backup cleanup failure, got warnings: %+v", res.Warnings)
	}
}

func TestT064_PathBoundariesAndSymlinkRejection(t *testing.T) {
	project := ideaTree(t)

	// 1. Chinese and long path support
	chineseOut := filepath.Join(t.TempDir(), "投产输出目录_测试2026_中文路径", "多层级长路径_SubDir_AlphaBetaGamma")
	res, err := Build(Request{
		ProjectDir:  project,
		OutputDir:   chineseOut,
		PackageName: "T064Chinese",
		Manifest:    "./WEB-INF/web.xml\n",
	})
	if err != nil || !res.OK {
		t.Fatalf("build to Chinese long path failed: res=%+v, err=%v", res, err)
	}
	if _, statErr := os.Stat(filepath.Join(chineseOut, "T064Chinese.tar")); statErr != nil {
		t.Fatalf("artifact not found in Chinese long path: %v", statErr)
	}

	// 2. Traversal and invalid root paths
	traversalPaths := []string{
		"../relative/escaped",
		"folder/../../escaped",
	}
	for _, p := range traversalPaths {
		if _, err := ValidateOutputPath(p); err == nil {
			t.Errorf("ValidateOutputPath should reject traversal path %q", p)
		}
	}

	// Root path rejection
	rootPath := "/"
	if runtime.GOOS == "windows" {
		rootPath = "C:\\"
	}
	if _, err := ValidateOutputPath(rootPath); err == nil {
		t.Errorf("ValidateOutputPath should reject drive/filesystem root %q", rootPath)
	}

	// 3. Project and output subpath / ancestor collision
	if _, err := validateOutputAgainstProject(project, project); err == nil {
		t.Errorf("output cannot be project dir itself")
	}
	if _, err := validateOutputAgainstProject(project, filepath.Join(project, "nested_out")); err == nil {
		t.Errorf("output cannot be subdirectory of project")
	}
	if _, err := validateOutputAgainstProject(filepath.Join(project, "sub_proj"), project); err == nil {
		t.Errorf("output cannot be ancestor directory of project")
	}

	// 4. Symlink rejection
	symlinkTarget := filepath.Join(t.TempDir(), "symlink_real_target")
	if mkErr := os.MkdirAll(symlinkTarget, 0o755); mkErr == nil {
		symlinkDir := filepath.Join(t.TempDir(), "symlink_pointer")
		if symlinkErr := os.Symlink(symlinkTarget, symlinkDir); symlinkErr == nil {
			// Using symlink as output dir must fail closed
			_, _, prepErr := prepareOutputDirWithPolicy(symlinkDir, OutputPolicyFail, false)
			if prepErr == nil || !strings.Contains(prepErr.Error(), "符号链接") {
				t.Fatalf("prepareOutputDirWithPolicy should reject symlink output path, got: %v", prepErr)
			}
		}
	}
}


