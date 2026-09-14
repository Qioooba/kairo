package wscodegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kairo/internal/webservice"
)

const sampleWSDL = `<?xml version="1.0" encoding="UTF-8"?>
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
            <xsd:element name="serialNo" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
      <xsd:element name="customerQueryResponse">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="custName" type="xsd:string"/>
            <xsd:element name="status" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="customerQueryRequest">
    <wsdl:part name="parameters" element="tns:customerQuery"/>
  </wsdl:message>
  <wsdl:message name="customerQueryResponse">
    <wsdl:part name="parameters" element="tns:customerQueryResponse"/>
  </wsdl:message>
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

func TestGenerateBuiltinPortable(t *testing.T) {
	p := webservice.ParseWSDL(sampleWSDL)
	out := t.TempDir()
	res, err := Generate(Request{
		Engine:      EnginePortable,
		Mode:        ModeBuiltin,
		PackageName: "com.demo.cust",
		OutputDir:   out,
		Overwrite:   true,
		IncludeMain: true,
		JavaSource:  JavaSource16,
		WSDLContent: p.RawWSDL,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatal("not ok")
	}
	joined := joinContents(res.Files)
	for _, need := range []string{
		"package com.demo.cust;",
		"public class CustomerServiceClient",
		"customerQuery(",
		"HttpURLConnection",
		"custNo",
		"-source 1.6",
	} {
		if !strings.Contains(joined, need) {
			t.Errorf("missing %q", need)
		}
	}
	if strings.Contains(joined, "try (") {
		t.Error("Java 6 code must not use try-with-resources")
	}
	if strings.Contains(joined, "<>") {
		t.Error("Java 6 code must not use diamond")
	}
	clientPath := filepath.Join(out, "com", "demo", "cust", "CustomerServiceClient.java")
	if _, err := os.Stat(clientPath); err != nil {
		t.Fatalf("expected written file: %v", err)
	}
}

func TestGenerateBuiltinAllEnginesDryRun(t *testing.T) {
	engines := []string{EnginePortable, EngineJAXWS, EngineCXF, EngineAxis1, EngineAxis2, EngineXFire}
	for _, eng := range engines {
		res, err := Generate(Request{
			Engine:      eng,
			Mode:        ModeBuiltin,
			DryRun:      true,
			IncludeMain: true,
			JavaSource:  JavaSource16,
			JAXWSTarget: JAXWSTarget21,
			WSDLContent: sampleWSDL,
		}, nil)
		if err != nil {
			t.Fatalf("%s: %v", eng, err)
		}
		if len(res.Files) < 3 {
			t.Fatalf("%s files=%d", eng, len(res.Files))
		}
		joined := joinContents(res.Files)
		if !strings.Contains(joined, "customerQuery") {
			t.Errorf("%s missing operation", eng)
		}
	}
}

func TestGenerateRejectsUnknownEngine(t *testing.T) {
	_, err := Generate(Request{Engine: "hessian", Mode: ModeBuiltin, DryRun: true, WSDLContent: sampleWSDL}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteRefusesDotDot(t *testing.T) {
	_, err := writeFiles(t.TempDir(), []GeneratedFile{{RelPath: "../evil.java", Content: "x"}}, true)
	if err == nil {
		t.Fatal("expected path error")
	}
}

func TestProfilesNonEmpty(t *testing.T) {
	if len(Profiles()) != 6 {
		t.Fatalf("want 6 engines, got %d", len(Profiles()))
	}
}

func TestFileURI(t *testing.T) {
	got := fileURI(`C:\ideaSpaces\credit\service.wsdl`)
	if got != "file:///C:/ideaSpaces/credit/service.wsdl" {
		t.Fatalf("windows URI = %s", got)
	}
	unc := fileURI(`\\server\share\credit service.wsdl`)
	if unc != "file://server/share/credit%20service.wsdl" {
		t.Fatalf("UNC URI = %s", unc)
	}
	local := fileURI(filepath.Join(t.TempDir(), "中文 空格.wsdl"))
	if !strings.HasPrefix(local, "file:///") || !strings.Contains(local, "%20") {
		t.Fatalf("local URI = %s", local)
	}
}

func TestGenerateBuiltinXFireFileURI(t *testing.T) {
	dir := t.TempDir()
	wsdlPath := filepath.Join(dir, "CustomerService.wsdl")
	if err := os.WriteFile(wsdlPath, []byte(sampleWSDL), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Generate(Request{
		Engine:      EngineXFire,
		Mode:        ModeBuiltin,
		DryRun:      true,
		IncludeMain: false,
		JavaSource:  JavaSource16,
		WSDLFile:    wsdlPath,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinContents(res.Files)
	if !strings.Contains(joined, "org.codehaus.xfire.client.Client") {
		t.Fatal("missing xfire client")
	}
	if !strings.Contains(joined, "file:///") {
		t.Fatalf("local WSDL must become file URI, got:\n%s", joined)
	}
}

func joinContents(files []GeneratedFile) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString(f.RelPath)
		b.WriteByte('\n')
		b.WriteString(f.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestGenerate_MultiDirSameNameXSD_PreservesBothBeans(t *testing.T) {
	tempDir := t.TempDir()
	dirA := filepath.Join(tempDir, "a")
	dirB := filepath.Join(tempDir, "b")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	commonA := `<?xml version="1.0" encoding="UTF-8"?>
<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="http://example.com/nsA">
  <xsd:complexType name="TypeA">
    <xsd:sequence><xsd:element name="fieldA" type="xsd:string"/></xsd:sequence>
  </xsd:complexType>
</xsd:schema>`

	commonB := `<?xml version="1.0" encoding="UTF-8"?>
<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="http://example.com/nsB">
  <xsd:complexType name="TypeB">
    <xsd:sequence><xsd:element name="fieldB" type="xsd:int"/></xsd:sequence>
  </xsd:complexType>
</xsd:schema>`

	if err := os.WriteFile(filepath.Join(dirA, "common.xsd"), []byte(commonA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "common.xsd"), []byte(commonB), 0o644); err != nil {
		t.Fatal(err)
	}

	wsdl := `<?xml version="1.0" encoding="UTF-8"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="http://example.com/main"
  xmlns:a="http://example.com/nsA"
  xmlns:b="http://example.com/nsB"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  targetNamespace="http://example.com/main">
  <wsdl:types>
    <xsd:schema targetNamespace="http://example.com/main">
      <xsd:import namespace="http://example.com/nsA" schemaLocation="a/common.xsd"/>
      <xsd:import namespace="http://example.com/nsB" schemaLocation="b/common.xsd"/>
      <xsd:element name="combine">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="itemA" type="a:TypeA"/>
            <xsd:element name="itemB" type="b:TypeB"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
      <xsd:element name="combineResponse">
        <xsd:complexType><xsd:sequence><xsd:element name="result" type="xsd:string"/></xsd:sequence></xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="combineReq"><wsdl:part name="p" element="tns:combine"/></wsdl:message>
  <wsdl:message name="combineResp"><wsdl:part name="p" element="tns:combineResponse"/></wsdl:message>
  <wsdl:portType name="CombinePT">
    <wsdl:operation name="combine">
      <wsdl:input message="tns:combineReq"/>
      <wsdl:output message="tns:combineResp"/>
    </wsdl:operation>
  </wsdl:portType>
  <wsdl:binding name="CombineBinding" type="tns:CombinePT">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <wsdl:operation name="combine">
      <soap:operation soapAction=""/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
      <wsdl:output><soap:body use="literal"/></wsdl:output>
    </wsdl:operation>
  </wsdl:binding>
  <wsdl:service name="CombineService">
    <wsdl:port name="CombinePort" binding="tns:CombineBinding">
      <soap:address location="http://127.0.0.1/ws"/>
    </wsdl:port>
  </wsdl:service>
</wsdl:definitions>`

	wsdlPath := filepath.Join(tempDir, "service.wsdl")
	if err := os.WriteFile(wsdlPath, []byte(wsdl), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(Request{
		Engine:      EnginePortable,
		Mode:        ModeBuiltin,
		DryRun:      true,
		IncludeMain: false,
		JavaSource:  JavaSource16,
		WSDLFile:    wsdlPath,
	}, nil)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if !res.OK {
		t.Fatalf("res.OK = false, warnings = %v", res.Warnings)
	}

	joined := joinContents(res.Files)
	if !strings.Contains(joined, "class ItemA") || !strings.Contains(joined, "fieldA") || !strings.Contains(joined, "xsd=a:TypeA") {
		t.Errorf("未能生成 a/common.xsd 中的 ItemA(TypeA) 类，生成文件内容:\n%s", joined)
	}
	if !strings.Contains(joined, "class ItemB") || !strings.Contains(joined, "fieldB") || !strings.Contains(joined, "xsd=b:TypeB") {
		t.Errorf("未能生成 b/common.xsd 中的 ItemB(TypeB) 类，生成文件内容:\n%s", joined)
	}
	if len(res.Dependencies) < 2 {
		t.Errorf("res.Dependencies 数量 = %d, want >= 2: %+v", len(res.Dependencies), res.Dependencies)
	}
}

