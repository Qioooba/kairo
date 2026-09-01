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
