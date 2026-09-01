package wscodegen

import (
	"strings"
	"testing"
)

func TestPlanTool_AxisRequiresJars(t *testing.T) {
	_, err := planTool(Request{
		Engine:  EngineAxis1,
		JDKHome: t.TempDir(),
	}, resolvedWSDL{URL: "http://127.0.0.1/a.wsdl"}, t.TempDir())
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
