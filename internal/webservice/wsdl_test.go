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

// 两个 schema（targetNamespace 不同）含同名 element/complexType，
// 且故意让 schema B 排在前面：验证 ns+name 索引按 message part 的 QName 前缀
// 解析到正确的 schema（旧实现按 name 匹配会拿错 namespace 和参数）。
const multiSchemaWSDL = `<?xml version="1.0" encoding="UTF-8"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="http://multi.example.com/"
  xmlns:a="http://multi.example.com/a"
  xmlns:b="http://multi.example.com/b"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  targetNamespace="http://multi.example.com/">
  <wsdl:types>
    <xsd:schema targetNamespace="http://multi.example.com/b" elementFormDefault="qualified">
      <xsd:element name="query">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="fromB" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
      <xsd:element name="typed" type="b:Shared"/>
      <xsd:element name="getInfo" type="b:FullInfo"/>
      <xsd:complexType name="Shared">
        <xsd:sequence>
          <xsd:element name="fieldB" type="xsd:string"/>
        </xsd:sequence>
      </xsd:complexType>
      <xsd:complexType name="BaseInfo">
        <xsd:sequence>
          <xsd:element name="baseFieldB" type="xsd:string"/>
        </xsd:sequence>
      </xsd:complexType>
      <xsd:complexType name="FullInfo">
        <xsd:complexContent>
          <xsd:extension base="b:BaseInfo">
            <xsd:sequence>
              <xsd:element name="extraFieldB" type="xsd:string"/>
            </xsd:sequence>
          </xsd:extension>
        </xsd:complexContent>
      </xsd:complexType>
    </xsd:schema>
    <xsd:schema targetNamespace="http://multi.example.com/a" elementFormDefault="qualified">
      <xsd:element name="query">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="fromA" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
      <xsd:element name="queryResponse">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="resultA" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
      <xsd:element name="typed" type="a:Shared"/>
      <xsd:element name="getInfo" type="a:FullInfo"/>
      <xsd:complexType name="Shared">
        <xsd:sequence>
          <xsd:element name="fieldA" type="xsd:string"/>
        </xsd:sequence>
      </xsd:complexType>
      <xsd:complexType name="BaseInfo">
        <xsd:sequence>
          <xsd:element name="baseFieldA" type="xsd:string"/>
        </xsd:sequence>
      </xsd:complexType>
      <xsd:complexType name="FullInfo">
        <xsd:complexContent>
          <xsd:extension base="a:BaseInfo">
            <xsd:sequence>
              <xsd:element name="extraFieldA" type="xsd:string"/>
            </xsd:sequence>
          </xsd:extension>
        </xsd:complexContent>
      </xsd:complexType>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="queryARequest">
    <wsdl:part name="parameters" element="a:query"/>
  </wsdl:message>
  <wsdl:message name="queryAResponse">
    <wsdl:part name="parameters" element="a:queryResponse"/>
  </wsdl:message>
  <wsdl:message name="queryBRequest">
    <wsdl:part name="parameters" element="b:query"/>
  </wsdl:message>
  <wsdl:message name="typedARequest">
    <wsdl:part name="parameters" element="a:typed"/>
  </wsdl:message>
  <wsdl:message name="typedBRequest">
    <wsdl:part name="parameters" element="b:typed"/>
  </wsdl:message>
  <wsdl:message name="infoARequest">
    <wsdl:part name="parameters" element="a:getInfo"/>
  </wsdl:message>
  <wsdl:message name="infoBRequest">
    <wsdl:part name="parameters" element="b:getInfo"/>
  </wsdl:message>
  <wsdl:portType name="MultiPortType">
    <wsdl:operation name="queryA">
      <wsdl:input message="tns:queryARequest"/>
      <wsdl:output message="tns:queryAResponse"/>
    </wsdl:operation>
    <wsdl:operation name="queryB">
      <wsdl:input message="tns:queryBRequest"/>
    </wsdl:operation>
    <wsdl:operation name="typedA">
      <wsdl:input message="tns:typedARequest"/>
    </wsdl:operation>
    <wsdl:operation name="typedB">
      <wsdl:input message="tns:typedBRequest"/>
    </wsdl:operation>
    <wsdl:operation name="infoA">
      <wsdl:input message="tns:infoARequest"/>
    </wsdl:operation>
    <wsdl:operation name="infoB">
      <wsdl:input message="tns:infoBRequest"/>
    </wsdl:operation>
  </wsdl:portType>
  <wsdl:binding name="MultiBinding" type="tns:MultiPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <wsdl:operation name="queryA">
      <soap:operation soapAction="http://multi.example.com/queryA"/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
      <wsdl:output><soap:body use="literal"/></wsdl:output>
    </wsdl:operation>
    <wsdl:operation name="queryB">
      <soap:operation soapAction="http://multi.example.com/queryB"/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
    </wsdl:operation>
    <wsdl:operation name="typedA">
      <soap:operation soapAction="http://multi.example.com/typedA"/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
    </wsdl:operation>
    <wsdl:operation name="typedB">
      <soap:operation soapAction="http://multi.example.com/typedB"/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
    </wsdl:operation>
    <wsdl:operation name="infoA">
      <soap:operation soapAction="http://multi.example.com/infoA"/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
    </wsdl:operation>
    <wsdl:operation name="infoB">
      <soap:operation soapAction="http://multi.example.com/infoB"/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
    </wsdl:operation>
  </wsdl:binding>
  <wsdl:service name="MultiService">
    <wsdl:port name="MultiPort" binding="tns:MultiBinding">
      <soap:address location="http://10.0.0.1:8080/multi"/>
    </wsdl:port>
  </wsdl:service>
</wsdl:definitions>`

func TestParseWSDL_MultiSchemaSameElementName(t *testing.T) {
	p := ParseWSDL(multiSchemaWSDL)
	if p.ParseError != "" {
		t.Fatalf("ParseError: %s", p.ParseError)
	}
	if len(p.Operations) != 6 {
		t.Fatalf("operations = %d, want 6", len(p.Operations))
	}
	byName := map[string]*Operation{}
	for i := range p.Operations {
		byName[p.Operations[i].Name] = &p.Operations[i]
	}

	// 同名 element "query" 在不同 schema：namespace 和参数都要按 QName 前缀命中
	queryA := byName["queryA"]
	if queryA.Namespace != "http://multi.example.com/a" {
		t.Errorf("queryA.Namespace = %q, want ns a", queryA.Namespace)
	}
	if got := inputChildNames(queryA); len(got) != 1 || got[0] != "fromA" {
		t.Errorf("queryA input children = %v, want [fromA]", got)
	}
	if got := outputChildNames(queryA); len(got) != 1 || got[0] != "resultA" {
		t.Errorf("queryA output children = %v, want [resultA]", got)
	}
	queryB := byName["queryB"]
	if queryB.Namespace != "http://multi.example.com/b" {
		t.Errorf("queryB.Namespace = %q, want ns b", queryB.Namespace)
	}
	if got := inputChildNames(queryB); len(got) != 1 || got[0] != "fromB" {
		t.Errorf("queryB input children = %v, want [fromB]", got)
	}

	// 同名 complexType "Shared"：type="a:Shared" 和 type="b:Shared" 各拿各的
	if got := inputChildNames(byName["typedA"]); len(got) != 1 || got[0] != "fieldA" {
		t.Errorf("typedA children = %v, want [fieldA]", got)
	}
	if got := inputChildNames(byName["typedB"]); len(got) != 1 || got[0] != "fieldB" {
		t.Errorf("typedB children = %v, want [fieldB]", got)
	}

	// extension base QName（a:BaseInfo / b:BaseInfo）按前缀解析
	if got := inputChildNames(byName["infoA"]); len(got) != 2 || got[0] != "baseFieldA" || got[1] != "extraFieldA" {
		t.Errorf("infoA children = %v, want [baseFieldA extraFieldA]", got)
	}
	if got := inputChildNames(byName["infoB"]); len(got) != 2 || got[0] != "baseFieldB" || got[1] != "extraFieldB" {
		t.Errorf("infoB children = %v, want [baseFieldB extraFieldB]", got)
	}
}

// inputChildNames 展平 InputParams 第一层的 children 名字。
func inputChildNames(op *Operation) []string {
	var names []string
	for _, p := range op.InputParams {
		for _, c := range p.Children {
			names = append(names, c.Name)
		}
	}
	return names
}

// outputChildNames 展平 OutputParams 第一层的 children 名字。
func outputChildNames(op *Operation) []string {
	var names []string
	for _, p := range op.OutputParams {
		for _, c := range p.Children {
			names = append(names, c.Name)
		}
	}
	return names
}

// 外部 XSD 与 inline schema 含同名 element "query"：
// 旧实现 lookupElementNS 只扫 inline schema，会返回 inline 的 targetNamespace。
func TestParseWSDL_ExternalXSDSameElementName(t *testing.T) {
	raw := `<?xml version="1.0" encoding="UTF-8"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="http://inline.example.com/"
  xmlns:ext="http://ext.example.com/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  targetNamespace="http://inline.example.com/">
  <wsdl:types>
    <xsd:schema targetNamespace="http://inline.example.com/">
      <xsd:import namespace="http://ext.example.com/" schemaLocation="types.xsd"/>
      <xsd:element name="query">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="inlineField" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="extQueryRequest">
    <wsdl:part name="parameters" element="ext:query"/>
  </wsdl:message>
  <wsdl:portType name="ExtPortType">
    <wsdl:operation name="extQuery">
      <wsdl:input message="tns:extQueryRequest"/>
    </wsdl:operation>
  </wsdl:portType>
  <wsdl:binding name="ExtBinding" type="tns:ExtPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <wsdl:operation name="extQuery">
      <soap:operation soapAction="http://inline.example.com/extQuery"/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
    </wsdl:operation>
  </wsdl:binding>
  <wsdl:service name="ExtService">
    <wsdl:port name="ExtPort" binding="tns:ExtBinding">
      <soap:address location="http://10.0.0.1:8080/ext"/>
    </wsdl:port>
  </wsdl:service>
</wsdl:definitions>`
	attachments := map[string]string{
		"types.xsd": `<?xml version="1.0"?>
<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  targetNamespace="http://ext.example.com/">
  <xsd:element name="query">
    <xsd:complexType>
      <xsd:sequence>
        <xsd:element name="extField" type="xsd:string"/>
      </xsd:sequence>
    </xsd:complexType>
  </xsd:element>
</xsd:schema>`,
	}
	p := ParseWSDL(raw, attachments)
	if p.ParseError != "" {
		t.Fatalf("ParseError: %s", p.ParseError)
	}
	for _, w := range p.Warnings {
		if strings.Contains(w, "外部 XSD") {
			t.Fatalf("外部 XSD 不应有加载警告: %s", w)
		}
	}
	if len(p.Operations) != 1 {
		t.Fatalf("operations = %d, want 1", len(p.Operations))
	}
	op := p.Operations[0]
	if op.Namespace != "http://ext.example.com/" {
		t.Errorf("Namespace = %q, want 外部 XSD 的 targetNamespace", op.Namespace)
	}
	if got := inputChildNames(&op); len(got) != 1 || got[0] != "extField" {
		t.Errorf("input children = %v, want [extField]", got)
	}
}

// definitions 用默认 xmlns，message part 的 element 无前缀：
// 按 QName 语义解析到默认命名空间（= targetNamespace）。
func TestParseWSDL_UnprefixedPartElement(t *testing.T) {
	raw := `<?xml version="1.0"?>
<wsdl:definitions xmlns="http://def.example.com/"
  xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  targetNamespace="http://def.example.com/">
  <wsdl:types>
    <xsd:schema targetNamespace="http://def.example.com/">
      <xsd:element name="hello">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="msg" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="helloRequest">
    <wsdl:part name="parameters" element="hello"/>
  </wsdl:message>
  <wsdl:portType name="HelloPortType">
    <wsdl:operation name="hello">
      <wsdl:input message="helloRequest"/>
    </wsdl:operation>
  </wsdl:portType>
  <wsdl:binding name="HelloBinding" type="HelloPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <wsdl:operation name="hello">
      <soap:operation soapAction="http://def.example.com/hello"/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
    </wsdl:operation>
  </wsdl:binding>
  <wsdl:service name="HelloService">
    <wsdl:port name="HelloPort" binding="HelloBinding">
      <soap:address location="http://10.0.0.1:8080/hello"/>
    </wsdl:port>
  </wsdl:service>
</wsdl:definitions>`
	p := ParseWSDL(raw)
	if p.ParseError != "" {
		t.Fatalf("ParseError: %s", p.ParseError)
	}
	if len(p.Operations) != 1 {
		t.Fatalf("operations = %d, want 1", len(p.Operations))
	}
	op := p.Operations[0]
	if op.Namespace != "http://def.example.com/" {
		t.Errorf("Namespace = %q, want default ns (= targetNamespace)", op.Namespace)
	}
	// op 名 hello == element 名 hello → wrapped 模式剥掉外层包装
	if len(op.InputParams) != 1 || op.InputParams[0].Name != "msg" {
		t.Errorf("InputParams = %+v, want [msg]", op.InputParams)
	}
}

func TestNSContextResolve(t *testing.T) {
	ctx := nsContext{prefixes: nsMap{"a": "http://a/", "": "http://def/"}, self: "http://self/"}
	cases := []struct {
		qname   string
		wantNS  string
		wantNam string
	}{
		{"a:Foo", "http://a/", "Foo"},
		{"Foo", "http://self/", "Foo"},
		{"undeclared:Foo", "", "Foo"},
		{"", "http://self/", ""},
	}
	for _, c := range cases {
		ns, name := ctx.resolve(c.qname)
		if ns != c.wantNS || name != c.wantNam {
			t.Errorf("resolve(%q) = (%q, %q), want (%q, %q)", c.qname, ns, name, c.wantNS, c.wantNam)
		}
	}
}

func TestCollectNSContexts(t *testing.T) {
	raw := `<root xmlns="http://def/" xmlns:a="http://a/">
  <child xmlns:b="http://b/">
    <schema xmlns:c="http://c/"/>
  </child>
</root>`
	root, schemas := collectNSContexts(raw)
	if root[""] != "http://def/" || root["a"] != "http://a/" {
		t.Errorf("root ns = %v", root)
	}
	if len(schemas) != 1 {
		t.Fatalf("schemas = %d, want 1", len(schemas))
	}
	s := schemas[0]
	if s["a"] != "http://a/" || s["b"] != "http://b/" || s["c"] != "http://c/" {
		t.Errorf("schema ns = %v", s)
	}
}

func TestSchemaIndex_UniqueNameFallback(t *testing.T) {
	idx := newSchemaIndex()
	idx.addSchema(xsdSchema{
		TargetNS: "http://a/",
		Elements: []xsdElement{{Name: "only", Type: "xsd:string"}},
	}, nsContext{})
	idx.addSchema(xsdSchema{
		TargetNS: "http://b/",
		Elements: []xsdElement{{Name: "dup", Type: "xsd:string"}},
	}, nsContext{})
	idx.addSchema(xsdSchema{
		TargetNS: "http://c/",
		Elements: []xsdElement{{Name: "dup", Type: "xsd:int"}},
	}, nsContext{})

	// ns 精确命中
	if el, _, ns, ok := idx.findElement("http://b/", "dup"); !ok || ns != "http://b/" || el.Type != "xsd:string" {
		t.Errorf("findElement(http://b/, dup) = (%+v, %q, %v), want b 的 string 版", el, ns, ok)
	}
	if el, _, ns, ok := idx.findElement("http://c/", "dup"); !ok || ns != "http://c/" || el.Type != "xsd:int" {
		t.Errorf("findElement(http://c/, dup) = (%+v, %q, %v), want c 的 int 版", el, ns, ok)
	}
	// 全局唯一名兜底（ns 未命中）
	if el, _, ns, ok := idx.findElement("", "only"); !ok || ns != "http://a/" || el.Name != "only" {
		t.Errorf("findElement(\"\", only) = (%+v, %q, %v), want a 的 only", el, ns, ok)
	}
	// 同名多处存在且 ns 未命中 → 兜底不给（防止误判）
	if _, _, _, ok := idx.findElement("", "dup"); ok {
		t.Error("findElement(\"\", dup) 不应命中兜底")
	}
}
