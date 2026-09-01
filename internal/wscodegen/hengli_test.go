package wscodegen

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kairo/internal/webservice"
)

func hengliDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "webservice", "testdata", "hengli")
	if _, err := os.Stat(filepath.Join(dir, "SuLianLoanHengLiService.wsdl")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func hengliWSDLPath(t *testing.T) string {
	return filepath.Join(hengliDir(t), "SuLianLoanHengLiService.wsdl")
}

func mustHengliFields(t *testing.T, joined string) {
	t.Helper()
	for _, need := range []string{
		"querySuLianLoanHengLiService",
		"creditcode",
		"custType",
		"SEQ_NO",
	} {
		if !strings.Contains(joined, need) {
			t.Errorf("missing real field/op %q", need)
		}
	}
}

func TestResolveWSDL_FileLoadsSiblingXSD(t *testing.T) {
	resolved, err := ResolveWSDL(Request{WSDLFile: hengliWSDLPath(t)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SiblingXSDCount < 2 {
		t.Fatalf("sibling xsd = %d, want >= 2 (Core + esb)", resolved.SiblingXSDCount)
	}
	if resolved.Project == nil || len(resolved.Project.Operations) != 1 {
		t.Fatalf("ops = %+v", resolved.Project)
	}
	names := map[string]bool{}
	var walk func(pp []webservice.Param)
	walk = func(pp []webservice.Param) {
		for _, x := range pp {
			names[x.Name] = true
			walk(x.Children)
		}
	}
	walk(resolved.Project.Operations[0].InputParams)
	for _, want := range []string{"SEQ_NO", "EXT_HEAD", "creditcode", "custType"} {
		if !names[want] {
			t.Errorf("file+sibling XSD missing %q in %+v", want, names)
		}
	}
}

func TestGenerateHengliFileAllEngines(t *testing.T) {
	path := hengliWSDLPath(t)
	engines := []string{EnginePortable, EngineJAXWS, EngineCXF, EngineAxis1, EngineAxis2, EngineXFire}
	for _, eng := range engines {
		res, err := Generate(Request{
			Engine:      eng,
			Mode:        ModeBuiltin,
			DryRun:      true,
			IncludeMain: true,
			JavaSource:  JavaSource16,
			JAXWSTarget: JAXWSTarget21,
			WSDLFile:    path,
		}, nil)
		if err != nil {
			t.Fatalf("%s: %v", eng, err)
		}
		joined := joinContents(res.Files)
		mustHengliFields(t, joined)
		if strings.Contains(joined, "try (") {
			t.Errorf("%s Java 6 must not use try-with-resources", eng)
		}
		if strings.Contains(joined, "<>") {
			t.Errorf("%s Java 6 must not use diamond", eng)
		}
		noteHit := false
		for _, n := range res.Notes {
			if strings.Contains(n, "XSD") {
				noteHit = true
			}
		}
		if !noteHit {
			t.Errorf("%s expected sibling XSD note, notes=%v", eng, res.Notes)
		}
	}
}

func TestGenerateHengliFileWritesDisk(t *testing.T) {
	out := t.TempDir()
	res, err := Generate(Request{
		Engine:      EnginePortable,
		Mode:        ModeBuiltin,
		OutputDir:   out,
		Overwrite:   true,
		IncludeMain: true,
		JavaSource:  JavaSource16,
		WSDLFile:    hengliWSDLPath(t),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) == 0 {
		t.Fatal("expected written files")
	}
	joined := joinContents(res.Files)
	mustHengliFields(t, joined)
	found := false
	_ = filepath.Walk(out, func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(path, "Client.java") {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("expected Client.java on disk")
	}
}

func TestGenerateHengliPasteWarnsMissingXSD(t *testing.T) {
	raw, err := os.ReadFile(hengliWSDLPath(t))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Generate(Request{
		Engine:      EnginePortable,
		Mode:        ModeBuiltin,
		DryRun:      true,
		WSDLContent: string(raw),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	hit := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "外部 XSD") {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected missing-XSD warning, got %v", res.Warnings)
	}
	if strings.Contains(joinContents(res.Files), "creditcode") {
		t.Fatal("paste without XSD should not expand creditcode")
	}
}

func TestGenerateHengliURLFetchesRelativeXSD(t *testing.T) {
	dir := hengliDir(t)
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()
	raw, err := os.ReadFile(hengliWSDLPath(t))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Generate(Request{
		Engine:      EnginePortable,
		Mode:        ModeBuiltin,
		DryRun:      true,
		IncludeMain: true,
		WSDLURL:     srv.URL + "/SuLianLoanHengLiService.wsdl",
		WSDLContent: string(raw),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustHengliFields(t, joinContents(res.Files))
}

func TestGenerateHengliSavedProject(t *testing.T) {
	dir := hengliDir(t)
	wsdl, err := os.ReadFile(filepath.Join(dir, "SuLianLoanHengLiService.wsdl"))
	if err != nil {
		t.Fatal(err)
	}
	core, err := os.ReadFile(filepath.Join(dir, "SuLianLoanHengLiServiceCore.xsd"))
	if err != nil {
		t.Fatal(err)
	}
	esb, err := os.ReadFile(filepath.Join(dir, "esb.xsd"))
	if err != nil {
		t.Fatal(err)
	}
	p := webservice.ParseWSDL(string(wsdl), map[string]string{
		"SuLianLoanHengLiServiceCore.xsd": string(core),
		"esb.xsd":                         string(esb),
	})
	p.Name = "hengli-codegen"
	store := webservice.NewStore(t.TempDir())
	saved, err := store.SaveProject(*p)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Generate(Request{
		Engine:        EngineAxis1,
		Mode:          ModeBuiltin,
		DryRun:        true,
		IncludeMain:   true,
		WSDLProjectID: saved.ID,
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	mustHengliFields(t, joinContents(res.Files))
}

func TestGenerateEmptyNoWSDL(t *testing.T) {
	_, err := Generate(Request{Engine: EnginePortable, Mode: ModeBuiltin, DryRun: true}, nil)
	if err == nil {
		t.Fatal("expected missing WSDL error")
	}
}
