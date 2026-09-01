package wscodegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanProject_DetectsXFireAndAxis(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "WEB-INF", "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"xfire-all-1.2.6.jar", "wsdl4j-1.6.2.jar", "stax-api-1.0.1.jar"} {
		if err := os.WriteFile(filepath.Join(lib, name), []byte("jar"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><artifactId>xfire-all</artifactId></project>`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ScanProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if res.SuggestedEngine != EngineXFire {
		t.Fatalf("suggested=%s jars=%d engines=%v", res.SuggestedEngine, len(res.Jars), res.DetectedEngines)
	}
	if len(res.Jars) < 2 {
		t.Fatalf("jars=%+v", res.Jars)
	}
}

func TestScanProject_CreditSplitXFireJars(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "WEB-INF", "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{
		"xfire-aegis-1.2.6.jar",
		"xfire-all-1.2.6.jar",
		"xfire-annotations-1.2.6.jar",
		"xfire-core-1.2.6.jar",
		"xfire-java5-1.2.6.jar",
		"xfire-jaxb2-1.2.6.jar",
		"xfire-jaxws-1.2.6.jar",
		"xfire-jsr181-api-1.0-M1.jar",
		"xfire-spring-1.2.6.jar",
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(lib, name), []byte("jar"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := ScanProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if res.SuggestedEngine != EngineXFire {
		t.Fatalf("suggested=%s engines=%v", res.SuggestedEngine, res.DetectedEngines)
	}
	joined := strings.Join(res.Notes, "\n")
	if !strings.Contains(joined, "xfire-generator") {
		t.Fatalf("expected generator note, notes=%v", res.Notes)
	}
	if !strings.Contains(joined, "拆开") && !strings.Contains(joined, "xfire-core") {
		t.Fatalf("expected split-module note, notes=%v", res.Notes)
	}
	for _, e := range res.DetectedEngines {
		if e == EngineJAXWS {
			t.Fatal("xfire-jaxws.jar must not be classified as jaxws")
		}
	}
}

func TestScanProject_MissingDir(t *testing.T) {
	res, err := ScanProject(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if res.SuggestedEngine != EnginePortable {
		t.Errorf("got %s", res.SuggestedEngine)
	}
}

func TestInspectJDKHome_RejectsEmpty(t *testing.T) {
	if _, ok := InspectJDKHome("", "user"); ok {
		t.Fatal("empty home should fail")
	}
	if _, ok := InspectJDKHome(t.TempDir(), "user"); ok {
		t.Fatal("empty dir without java.exe should fail")
	}
}

func TestPomEngineHints(t *testing.T) {
	got := pomEngineHints(`<dependency><groupId>org.codehaus.xfire</groupId><artifactId>xfire-all</artifactId></dependency>`)
	if len(got) == 0 || got[0] != EngineXFire {
		t.Fatalf("got %v", got)
	}
}
