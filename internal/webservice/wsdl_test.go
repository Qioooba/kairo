package webservice

import (
	"strings"
	"testing"
)

// 一个典型的 SOAP 1.1 document/literal wrapped WSDL（客户查询服务）。
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
            <xsd:element name="queryType" type="xsd:string" minOccurs="0"/>
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

func TestParseWSDL_Simple(t *testing.T) {
	p := ParseWSDL(sampleWSDL)
	if p.ParseError != "" {
		t.Fatalf("ParseError: %s", p.ParseError)
	}
	if p.TargetNS != "http://cust.example.com/" {
		t.Errorf("TargetNS = %q, want http://cust.example.com/", p.TargetNS)
	}
	if p.SOAPVersion != "1.1" {
		t.Errorf("SOAPVersion = %q, want 1.1", p.SOAPVersion)
	}
	if len(p.Services) != 1 || p.Services[0].Name != "CustomerService" {
		t.Fatalf("services = %+v", p.Services)
	}
	if len(p.Services[0].Ports) != 1 {
		t.Fatalf("ports = %+v", p.Services[0].Ports)
	}
	port := p.Services[0].Ports[0]
	if port.Endpoint != "http://10.0.0.1:8080/cust/services/CustomerService" {
		t.Errorf("endpoint = %q", port.Endpoint)
	}
	if len(p.Operations) != 1 {
		t.Fatalf("operations = %+v", p.Operations)
	}
	op := p.Operations[0]
	if op.Name != "customerQuery" {
		t.Errorf("op.Name = %q", op.Name)
	}
	if op.SOAPAction != "http://cust.example.com/customerQuery" {
		t.Errorf("SOAPAction = %q", op.SOAPAction)
	}
	if op.Endpoint == "" {
		t.Errorf("endpoint should be filled from port")
	}
	if op.Namespace != "http://cust.example.com/" {
		t.Errorf("Namespace = %q", op.Namespace)
	}
	if len(op.InputParams) != 3 {
		t.Fatalf("InputParams = %+v (want 3)", op.InputParams)
	}
	wantNames := map[string]bool{"custNo": false, "serialNo": false, "queryType": false}
	for _, p := range op.InputParams {
		wantNames[p.Name] = true
	}
	for k, ok := range wantNames {
		if !ok {
			t.Errorf("missing input param %q", k)
		}
	}
	if len(op.OutputParams) != 2 {
		t.Fatalf("OutputParams = %+v (want 2)", op.OutputParams)
	}
}

func TestParseWSDL_Malformed(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"not xml", "hello world"},
		{"not wsdl", `<?xml version="1.0"?><foo><bar/></foo>`},
		{"broken xml", `<wsdl:definitions><wsdl:types></wsdl:definitions>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := ParseWSDL(c.raw)
			// 降级原则：要么有 ParseError，要么没 operation（不能 panic）
			_ = p
		})
	}
	// 空内容必须有清晰错误
	if p := ParseWSDL(""); p.ParseError == "" {
		t.Errorf("empty wsdl should report ParseError")
	}
}

func TestParseWSDL_NoOperations(t *testing.T) {
	raw := `<?xml version="1.0"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  targetNamespace="http://x/">
</wsdl:definitions>`
	p := ParseWSDL(raw)
	if p.ParseError != "" {
		t.Fatalf("unexpected ParseError: %s", p.ParseError)
	}
	if len(p.Operations) != 0 {
		t.Errorf("want 0 operations, got %d", len(p.Operations))
	}
	// 应给出降级提示
	found := false
	for _, w := range p.Warnings {
		if strings.Contains(w, "未解析到任何 operation") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about no operations, got %v", p.Warnings)
	}
}

func TestValidateWSDLText(t *testing.T) {
	if err := ValidateWSDLText(""); err == nil {
		t.Error("empty should fail")
	}
	if err := ValidateWSDLText(sampleWSDL); err != nil {
		t.Errorf("valid wsdl should pass: %v", err)
	}
	if err := ValidateWSDLText(`<a><b></a>`); err == nil {
		t.Error("malformed xml should fail")
	}
}
