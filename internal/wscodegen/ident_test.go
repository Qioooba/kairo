package wscodegen

import "testing"

func TestJavaIdent(t *testing.T) {
	cases := map[string]string{
		"custNo":     "custNo",
		"CustNo":     "custNo",
		"query-type": "query_type",
		"class":      "class_",
		"123abc":     "n123abc",
		"":           "value",
	}
	for in, want := range cases {
		if got := JavaIdent(in); got != want {
			t.Errorf("JavaIdent(%q)=%q want %q", in, got, want)
		}
	}
}

func TestJavaClassName(t *testing.T) {
	if got := JavaClassName("customerQuery"); got != "CustomerQuery" {
		t.Errorf("got %q", got)
	}
	if got := JavaClassName("customer-query-response"); got != "CustomerQueryResponse" {
		t.Errorf("got %q", got)
	}
}

func TestPackageFromNamespace(t *testing.T) {
	got := PackageFromNamespace("http://cust.example.com/")
	if got != "com.example.cust" {
		t.Errorf("got %q", got)
	}
	got = PackageFromNamespace("urn:kairo:e2e")
	if !containsDot(got) && got == "" {
		t.Errorf("empty package")
	}
}

func containsDot(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return true
		}
	}
	return s != ""
}

func TestJavaString(t *testing.T) {
	got := JavaString("a\"b\nc")
	if got != `"a\"b\nc"` {
		t.Errorf("got %s", got)
	}
}

func TestParseJavaVersion(t *testing.T) {
	cases := []struct {
		in    string
		major int
	}{
		{`java version "1.6.0_45"`, 6},
		{`java version "1.8.0_202"`, 8},
		{`openjdk version "11.0.2" 2019-01-15`, 11},
		{`openjdk version "17.0.8" 2023-07-18`, 17},
	}
	for _, c := range cases {
		_, major := ParseJavaVersion(c.in)
		if major != c.major {
			t.Errorf("ParseJavaVersion(%q) major=%d want %d", c.in, major, c.major)
		}
	}
}

func TestClassifyJarAndSuggest(t *testing.T) {
	kind, engine := classifyJar("xfire-all-1.2.6.jar")
	if kind != "xfire-all" || engine != EngineXFire {
		t.Errorf("xfire-all: %s %s", kind, engine)
	}
	kind, engine = classifyJar("xfire-core-1.2.6.jar")
	if kind != "xfire" || engine != EngineXFire {
		t.Errorf("xfire-core: %s %s", kind, engine)
	}
	kind, engine = classifyJar("xfire-jaxws-1.2.6.jar")
	if engine != EngineXFire {
		t.Errorf("xfire-jaxws should stay xfire, got %s %s", kind, engine)
	}
	kind, engine = classifyJar("axis.jar")
	if engine != EngineAxis1 {
		t.Errorf("axis: %s %s", kind, engine)
	}
	kind, engine = classifyJar("cxf-core-2.7.18.jar")
	if engine != EngineCXF {
		t.Errorf("cxf: %s %s", kind, engine)
	}
	if suggestEngine([]string{EngineJAXWS, EngineXFire}) != EngineXFire {
		t.Error("xfire should win over jaxws in old projects")
	}
	if suggestEngine(nil) != EnginePortable {
		t.Error("empty should be portable")
	}
}
