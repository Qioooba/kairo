package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const wscodegenSampleWSDL = `<?xml version="1.0" encoding="UTF-8"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="http://cust.example.com/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  targetNamespace="http://cust.example.com/">
  <wsdl:types>
    <xsd:schema targetNamespace="http://cust.example.com/" elementFormDefault="qualified">
      <xsd:element name="customerQuery">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="custNo" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
      <xsd:element name="customerQueryResponse">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="custName" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="customerQueryRequest"><wsdl:part name="parameters" element="tns:customerQuery"/></wsdl:message>
  <wsdl:message name="customerQueryResponse"><wsdl:part name="parameters" element="tns:customerQueryResponse"/></wsdl:message>
  <wsdl:portType name="CustomerPortType">
    <wsdl:operation name="customerQuery">
      <wsdl:input message="tns:customerQueryRequest"/>
      <wsdl:output message="tns:customerQueryResponse"/>
    </wsdl:operation>
  </wsdl:portType>
  <wsdl:binding name="CustomerSoapBinding" type="tns:CustomerPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <wsdl:operation name="customerQuery">
      <soap:operation soapAction="http://cust.example.com/customerQuery"/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
      <wsdl:output><soap:body use="literal"/></wsdl:output>
    </wsdl:operation>
  </wsdl:binding>
  <wsdl:service name="CustomerService">
    <wsdl:port name="CustomerPort" binding="tns:CustomerSoapBinding">
      <soap:address location="http://10.0.0.1:8080/cust/services/CustomerService"/>
    </wsdl:port>
  </wsdl:service>
</wsdl:definitions>`

func TestWSCodegenEngines(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/wscodegen/engines", nil)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"portable"`) || !strings.Contains(w.Body.String(), `"xfire"`) {
		t.Fatalf("missing engines: %s", w.Body.String())
	}
}

func TestWSCodegenPreviewAndGenerate(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wscodegen/preview", map[string]any{
		"engine":       "portable",
		"mode":         "builtin",
		"package":      "com.demo.ws",
		"include_main": true,
		"java_source":  "1.6",
		"wsdl_content": wscodegenSampleWSDL,
	})
	if w.Code != 200 {
		t.Fatalf("preview code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "CustomerServiceClient") {
		t.Fatalf("preview missing client: %s", w.Body.String()[:min(800, w.Body.Len())])
	}

	out := t.TempDir()
	w = doRequest(srv, "POST", "/api/wscodegen/generate", map[string]any{
		"engine":       "axis1",
		"mode":         "builtin",
		"package":      "com.demo.axis",
		"output_dir":   out,
		"overwrite":    true,
		"java_source":  "1.6",
		"wsdl_content": wscodegenSampleWSDL,
	})
	if w.Code != 200 {
		t.Fatalf("generate code=%d body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		OK     bool `json:"ok"`
		Result struct {
			Written []string `json:"written"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || len(payload.Result.Written) == 0 {
		t.Fatalf("payload=%+v", payload)
	}
	found := false
	_ = filepath.Walk(out, func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(path, "Client.java") {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("expected client java on disk")
	}
}

func TestWSCodegenScanProject(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	root := t.TempDir()
	lib := filepath.Join(root, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "axis.jar"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := doRequest(srv, "POST", "/api/wscodegen/scan-project", map[string]any{"project_dir": root})
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "axis1") {
		t.Fatalf("scan=%s", w.Body.String())
	}
}

func TestWSCodegenDetectJDK(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wscodegen/detect-jdk", map[string]any{})
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestWSCodegenUnknownPath(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/wscodegen/nope", nil)
	if w.Code != 404 {
		t.Fatalf("code=%d", w.Code)
	}
}

func TestWSCodegenDownloadZip(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wscodegen/download-zip", map[string]any{
		"engine":       "portable",
		"mode":         "builtin",
		"package":      "com.demo.ws",
		"include_main": true,
		"java_source":  "1.6",
		"wsdl_content": wscodegenSampleWSDL,
	})
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type=%s", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, ".zip") {
		t.Fatalf("disposition=%s", cd)
	}
	if w.Header().Get("X-Kairo-Files") == "" || w.Header().Get("X-Kairo-Files") == "0" {
		t.Fatalf("missing file count")
	}
	raw := w.Body.Bytes()
	if len(raw) < 4 || raw[0] != 'P' || raw[1] != 'K' {
		t.Fatalf("not a zip, len=%d", len(raw))
	}
}

func TestWSCodegenDownloadZipEmpty(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wscodegen/download-zip", map[string]any{
		"engine": "portable",
		"mode":   "builtin",
	})
	if w.Code != 400 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestWSCodegenPushProject(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	root := t.TempDir()
	src := filepath.Join(root, "src")
	lib := filepath.Join(root, "WebRoot", "WEB-INF", "lib")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	w := doRequest(srv, "POST", "/api/wscodegen/push-project", map[string]any{
		"engine":       "portable",
		"mode":         "builtin",
		"package":      "com.demo.ws",
		"project_dir":  lib,
		"overwrite":    true,
		"open_after":   false,
		"java_source":  "1.6",
		"wsdl_content": wscodegenSampleWSDL,
	})
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	client := filepath.Join(src, "com", "demo", "ws")
	found := false
	_ = filepath.Walk(client, func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(path, "Client.java") {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("expected client java under project src")
	}
}

func TestWSCodegenPushProjectMissingDir(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wscodegen/push-project", map[string]any{
		"engine":       "portable",
		"mode":         "builtin",
		"wsdl_content": wscodegenSampleWSDL,
	})
	if w.Code != 400 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestWSCodegenPreviewEmpty(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wscodegen/preview", map[string]any{
		"engine": "portable",
		"mode":   "builtin",
	})
	if w.Code != 400 {
		t.Fatalf("empty preview code=%d body=%s", w.Code, w.Body.String())
	}
}

func hengliTestdata(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "webservice", "testdata", "hengli")
	if _, err := os.Stat(filepath.Join(dir, "SuLianLoanHengLiService.wsdl")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWSCodegenHengliFilePreview(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	wsdl := filepath.Join(hengliTestdata(t), "SuLianLoanHengLiService.wsdl")
	w := doRequest(srv, "POST", "/api/wscodegen/preview", map[string]any{
		"engine":       "portable",
		"mode":         "builtin",
		"include_main": true,
		"java_source":  "1.6",
		"wsdl_file":    wsdl,
	})
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, need := range []string{"querySuLianLoanHengLiService", "creditcode", "custType", "SEQ_NO"} {
		if !strings.Contains(body, need) {
			t.Errorf("missing %q", need)
		}
	}
	if !strings.Contains(body, "XSD") {
		t.Error("expected sibling XSD note")
	}
}

func TestWSCodegenHengliProjectRoundTrip(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	dir := hengliTestdata(t)
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
	w := doRequest(srv, "POST", "/api/wsdl/import-file", map[string]any{
		"content": string(wsdl),
		"name":    "hengli-wscodegen",
		"attachments": map[string]string{
			"SuLianLoanHengLiServiceCore.xsd": string(core),
			"esb.xsd":                         string(esb),
		},
	})
	if w.Code != 200 {
		t.Fatalf("import code=%d body=%s", w.Code, w.Body.String())
	}
	var imported map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	w = doRequest(srv, "POST", "/api/wsdl/projects", map[string]any{
		"project": imported["project"],
	})
	if w.Code != 200 {
		t.Fatalf("save code=%d body=%s", w.Code, w.Body.String())
	}
	var saved struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Project.ID == "" {
		t.Fatal("empty project id")
	}
	w = doRequest(srv, "POST", "/api/wscodegen/preview", map[string]any{
		"engine":          "xfire",
		"mode":            "builtin",
		"include_main":    true,
		"wsdl_project_id": saved.Project.ID,
	})
	if w.Code != 200 {
		t.Fatalf("preview code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "creditcode") || !strings.Contains(w.Body.String(), "xfire") {
		t.Fatalf("project preview missing fields: %s", w.Body.String()[:min(600, w.Body.Len())])
	}
}

func TestWSCodegenHengliURLPreview(t *testing.T) {
	dir := hengliTestdata(t)
	hs := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(hs.Close)
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wscodegen/preview", map[string]any{
		"engine":      "portable",
		"mode":        "builtin",
		"java_source": "1.6",
		"wsdl_url":    hs.URL + "/SuLianLoanHengLiService.wsdl",
	})
	if w.Code != 200 {
		t.Fatalf("url preview code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "creditcode") {
		t.Fatalf("url+relative XSD not expanded: %s", w.Body.String()[:min(800, w.Body.Len())])
	}
}

func TestWSCodegenURLRejectsOversizedResponseInsteadOfTruncating(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write([]byte(strings.Repeat("x", maxFetchedWSDLBytes+1)))
	}))
	t.Cleanup(hs.Close)

	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wscodegen/preview", map[string]any{
		"engine":   "portable",
		"mode":     "builtin",
		"wsdl_url": hs.URL + "/oversized.wsdl",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "超过") {
		t.Fatalf("expected explicit size error, body=%s", w.Body.String())
	}
}

func TestWSCodegenScanXFire(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	root := t.TempDir()
	lib := filepath.Join(root, "WEB-INF", "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"xfire-all-1.2.6.jar", "wsdl4j-1.6.2.jar", "stax-api-1.0.1.jar"} {
		if err := os.WriteFile(filepath.Join(lib, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := doRequest(srv, "POST", "/api/wscodegen/scan-project", map[string]any{"project_dir": root})
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "xfire") {
		t.Fatalf("scan=%s", w.Body.String())
	}
}
