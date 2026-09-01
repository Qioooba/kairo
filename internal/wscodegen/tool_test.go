package wscodegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanTool_AxisRequiresJars(t *testing.T) {
	_, err := planTool(Request{
		Engine:  EngineAxis1,
		JDKHome: t.TempDir(),
	}, resolvedWSDL{URL: "http://127.0.0.1/a.wsdl"}, t.TempDir(), false)
	if err == nil {
		t.Fatal("expected jdk or jar error")
	}
}

func TestFormatCommandQuotes(t *testing.T) {
	cmd := formatCommand(toolPlan{
		JavaPath:  `C:\Program Files\Java\jdk1.6.0_45\bin\java.exe`,
		ClassPath: `D:\proj\lib\axis.jar;D:\proj\lib\jaxrpc.jar`,
		MainClass: "org.apache.axis.wsdl.WSDL2Java",
		Args:      []string{"-o", `D:\out dir`, "-p", "com.demo"},
		Wsimport:  "",
	})
	if !strings.Contains(cmd, `org.apache.axis.wsdl.WSDL2Java`) {
		t.Errorf("cmd=%s", cmd)
	}
	if !strings.Contains(cmd, `"-o"`) && !strings.Contains(cmd, "-o") {
		t.Errorf("missing -o: %s", cmd)
	}
}

func TestMissingJarsForAxis(t *testing.T) {
	miss := missingJarsFor(EngineAxis1, []JarHit{{FileName: "axis.jar", Kind: "axis1"}})
	if len(miss) == 0 {
		t.Fatal("should still miss jaxrpc/saaj/...")
	}
}

func TestToolPreviewDoesNotCreateOutputOrTemporaryWSDL(t *testing.T) {
	jdk := t.TempDir()
	bin := filepath.Join(jdk, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, javaBinName()), []byte("not executed during preview"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "must-not-exist")
	res, err := Generate(Request{
		Engine:        EngineAxis1,
		Mode:          ModeTool,
		DryRun:        true,
		OutputDir:     out,
		JDKHome:       jdk,
		ClasspathJars: []string{"axis.jar"},
		WSDLContent:   sampleWSDL,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatal("preview should succeed")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("preview created output directory: %v", err)
	}
}

func TestMaterializedWSDLHasExplicitLifecycle(t *testing.T) {
	arg, temp, err := wsdlArgForTool(resolvedWSDL{Raw: sampleWSDL}, true)
	if err != nil {
		t.Fatal(err)
	}
	if arg == "" || temp == "" || arg != temp {
		t.Fatalf("arg=%q temp=%q", arg, temp)
	}
	plan := toolPlan{TempWSDL: temp}
	plan.Close()
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Fatalf("temporary WSDL was not removed: %v", err)
	}
	plan.Close() // idempotent
}
