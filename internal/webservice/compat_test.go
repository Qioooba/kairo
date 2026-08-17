package webservice

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGenerateEnvelope_BareDocumentLiteral 验证 document/literal bare 场景：
// 输入元素名 != operation 名时，body 根元素应为输入元素（默认命名空间），
// 而不是用 operation 名再包一层（<op><input>...</input></op> 的双层错误结构）。
func TestGenerateEnvelope_BareDocumentLiteral(t *testing.T) {
	op := Operation{
		Name:       "querySuLianLoanHengLiService",
		InputName:  "SuLianLoanHengLiRequest",
		Namespace:  "http://www.jscb.com.cn/ExpertDecision/",
		SOAPAction: "",
		InputParams: []Param{
			{
				Name: "SuLianLoanHengLiRequest",
				Type: "cas:SuLianLoanHengLiRequest",
				Children: []Param{
					{Name: "SEQ_NO", Type: "string"},
					{Name: "creditcode", Type: "xs:string"},
				},
			},
		},
	}
	env := GenerateEnvelope(op, "1.1")

	// 根元素必须是输入元素（默认命名空间），不能出现 operation 名包裹
	if !strings.Contains(env, `<SuLianLoanHengLiRequest xmlns="http://www.jscb.com.cn/ExpertDecision/">`) {
		t.Errorf("bare 模式应使用输入元素作为默认命名空间根元素:\n%s", env)
	}
	if strings.Contains(env, "querySuLianLoanHengLiService") {
		t.Errorf("bare 模式不应出现 operation 名标签:\n%s", env)
	}
	// 子字段应为输入元素的直接子节点，不能再包一层 SuLianLoanHengLiRequest
	if strings.Count(env, "<SuLianLoanHengLiRequest") != 1 {
		t.Errorf("输入元素应只出现一次:\n%s", env)
	}
	if !strings.Contains(env, "<SEQ_NO></SEQ_NO>") || !strings.Contains(env, "<creditcode></creditcode>") {
		t.Errorf("子字段应展开为直接子节点:\n%s", env)
	}
}

// TestGenerateEnvelope_WrappedDocumentLiteral 验证 wrapped 模式（InputName == Name）
// 仍保持原有行为：<web:opName> 包裹参数。
func TestGenerateEnvelope_WrappedDocumentLiteral(t *testing.T) {
	op := Operation{
		Name:      "customerQuery",
		InputName: "customerQuery",
		Namespace: "http://cust.example.com/",
		InputParams: []Param{
			{Name: "custNo", Type: "xsd:string"},
		},
	}
	env := GenerateEnvelope(op, "1.1")
	if !strings.Contains(env, `xmlns:web="http://cust.example.com/"`) {
		t.Errorf("wrapped 应使用 web 前缀: %s", env)
	}
	if !strings.Contains(env, "<web:customerQuery>") {
		t.Errorf("wrapped 根元素应为 <web:customerQuery>: %s", env)
	}
	if !strings.Contains(env, "<custNo></custNo>") {
		t.Errorf("参数应展开: %s", env)
	}
}

// TestParseWSDL_DocumentBare_StyleAndNamespace 验证从 soap:binding 读取 style，
// 以及 soap:body namespace 作为 operation namespace 的权威来源。
func TestParseWSDL_DocumentBare_StyleAndNamespace(t *testing.T) {
	raw := `<?xml version="1.0" encoding="UTF-8"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="http://svc.example.com/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  targetNamespace="http://svc.example.com/">
  <wsdl:types>
    <xsd:schema targetNamespace="http://svc.example.com/">
      <xsd:element name="doWorkRequest">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="a" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
      <xsd:element name="doWorkResponse">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="b" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="doWorkRequestMsg">
    <wsdl:part name="parameters" element="tns:doWorkRequest"/>
  </wsdl:message>
  <wsdl:message name="doWorkResponseMsg">
    <wsdl:part name="parameters" element="tns:doWorkResponse"/>
  </wsdl:message>
  <wsdl:portType name="WorkPortType">
    <wsdl:operation name="doWork">
      <wsdl:input message="tns:doWorkRequestMsg"/>
      <wsdl:output message="tns:doWorkResponseMsg"/>
    </wsdl:operation>
  </wsdl:portType>
  <wsdl:binding name="WorkBinding" type="tns:WorkPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <wsdl:operation name="doWork">
      <soap:operation soapAction=""/>
      <wsdl:input><soap:body use="literal" namespace="http://body.example.com/"/></wsdl:input>
      <wsdl:output><soap:body use="literal" namespace="http://body.example.com/"/></wsdl:output>
    </wsdl:operation>
  </wsdl:binding>
  <wsdl:service name="WorkService">
    <wsdl:port name="WorkPort" binding="tns:WorkBinding">
      <soap:address location="http://10.0.0.9:8080/work"/>
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
	if op.Style != "document" {
		t.Errorf("Style = %q, want document (应从 soap:binding 读取)", op.Style)
	}
	// soap:body namespace 是权威来源，即使它与 schema targetNamespace 不同
	if op.Namespace != "http://body.example.com/" {
		t.Errorf("Namespace = %q, want http://body.example.com/ (soap:body namespace)", op.Namespace)
	}
	if op.InputName != "doWorkRequest" {
		t.Errorf("InputName = %q, want doWorkRequest", op.InputName)
	}
}

// TestFetchExternalSchema_URLResolution 验证 schemaLocation 的各种写法都能正确解析。
func TestFetchExternalSchema_URLResolution(t *testing.T) {
	var hit []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = append(hit, r.URL.Path)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`))
	}))
	defer srv.Close()

	cases := []struct {
		schemaLocation string
		wantPath       string
	}{
		{"a.xsd", "/dir/a.xsd"},
		{"sub/b.xsd", "/dir/sub/b.xsd"},
		{"../up.xsd", "/up.xsd"},
		{"/abs.xsd", "/abs.xsd"},
	}
	for _, c := range cases {
		hit = hit[:0]
		sourceURL := srv.URL + "/dir/service.wsdl"
		if _, err := fetchExternalSchema(sourceURL, c.schemaLocation); err != nil {
			t.Fatalf("schemaLocation=%q 失败: %v", c.schemaLocation, err)
		}
		if len(hit) != 1 || hit[0] != c.wantPath {
			t.Errorf("schemaLocation=%q 解析到 %v, want [%s]", c.schemaLocation, hit, c.wantPath)
		}
	}
}

// TestFetchExternalSchema_AbsoluteURLLocation 验证 schemaLocation 本身是绝对 URL 的情况。
func TestFetchExternalSchema_AbsoluteURLLocation(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`))
	}))
	defer other.Close()
	base := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("绝对 URL 的 schemaLocation 不应请求 base 主机")
	}))
	defer base.Close()

	// schemaLocation 是绝对 URL，必须直接请求它，而不是拼到 base 上
	if _, err := fetchExternalSchema(base.URL+"/dir/a.wsdl", other.URL+"/schemas/core.xsd"); err != nil {
		t.Fatalf("绝对 schemaLocation 应能成功: %v", err)
	}
}

// TestDecodeResponseBody_GBKFallback 验证谎报 UTF-8 / 缺 charset 时自动回退 GBK。
func TestDecodeResponseBody_GBKFallback(t *testing.T) {
	gbkZhang := []byte{0xD5, 0xC5} // GBK 编码的 "张"

	// 声明 UTF-8 但字节是 GBK → 回退 GBK
	if got := decodeResponseBody(gbkZhang, "text/xml; charset=UTF-8", "UTF-8"); got != "张" {
		t.Errorf("谎报 UTF-8 应回退 GBK, got %q", got)
	}
	// 无 charset → 回退 GBK
	if got := decodeResponseBody(gbkZhang, "text/xml", "UTF-8"); got != "张" {
		t.Errorf("缺 charset 应回退 GBK, got %q", got)
	}
	// 真实 UTF-8 不应被误转
	utf8Body := []byte("张")
	if got := decodeResponseBody(utf8Body, "text/xml; charset=UTF-8", "UTF-8"); got != "张" {
		t.Errorf("合法 UTF-8 不应被转换, got %q", got)
	}
	// 显式 GBK
	if got := decodeResponseBody(gbkZhang, "text/xml; charset=GBK", "UTF-8"); got != "张" {
		t.Errorf("显式 GBK 应正确解码, got %q", got)
	}
	// 未知 charset 但字节是 UTF-8
	if got := decodeResponseBody([]byte("ok"), "text/xml; charset=ISO-8859-1", "UTF-8"); got != "ok" {
		t.Errorf("未知 charset + 合法 UTF-8 应原样返回, got %q", got)
	}
}

// TestParseCharset_Robust 验证 charset 解析的容错性。
func TestParseCharset_Robust(t *testing.T) {
	cases := map[string]string{
		"text/xml; charset=UTF-8":                           "UTF-8",
		"text/xml; charset = gbk":                           "gbk",
		"text/xml; Charset=UTF-8":                           "UTF-8",
		`text/xml; charset="GBK"`:                           "GBK",
		"text/xml":                                          "",
		"application/soap+xml; action=\"x\"; charset=utf-8": "utf-8",
	}
	for in, want := range cases {
		if got := parseCharset(in); got != want {
			t.Errorf("parseCharset(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestParseWSDL_AttachmentsBackslashPath 验证附件查找兼容 Windows 反斜杠 schemaLocation。
func TestParseWSDL_AttachmentsBackslashPath(t *testing.T) {
	wsdl := `<?xml version="1.0"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="http://x.example.com/"
  xmlns:y="http://y.example.com/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  targetNamespace="http://x.example.com/">
  <wsdl:types>
    <xsd:schema targetNamespace="http://x.example.com/">
      <xsd:import namespace="http://y.example.com/" schemaLocation="schemas\core.xsd"/>
      <xsd:element name="ping" type="y:PingInner"/>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="pingReq"><wsdl:part name="p" element="tns:ping"/></wsdl:message>
  <wsdl:portType name="PT"><wsdl:operation name="ping"><wsdl:input message="tns:pingReq"/></wsdl:operation></wsdl:portType>
  <wsdl:binding name="B" type="tns:PT">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <wsdl:operation name="ping"><soap:operation soapAction=""/><wsdl:input><soap:body use="literal"/></wsdl:input></wsdl:operation>
  </wsdl:binding>
  <wsdl:service name="S"><wsdl:port name="P" binding="tns:B"><soap:address location="http://10.0.0.1/ping"/></wsdl:port></wsdl:service>
</wsdl:definitions>`

	// 附件按文件名提供（反斜杠 schemaLocation 的 basename 是 core.xsd）
	attachments := map[string]string{
		"core.xsd": `<?xml version="1.0"?>
<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="http://y.example.com/">
  <xsd:complexType name="PingInner"><xsd:sequence><xsd:element name="extra" type="xsd:string"/></xsd:sequence></xsd:complexType>
</xsd:schema>`,
	}
	p := ParseWSDL(wsdl, attachments)
	if p.ParseError != "" {
		t.Fatalf("ParseError: %s", p.ParseError)
	}
	if len(p.Operations) != 1 {
		t.Fatalf("operations = %d", len(p.Operations))
	}
	// ping 元素引用外部 XSD 的 y:PingInner，其子字段 extra 应被展开，
	// 说明反斜杠 schemaLocation 的附件查找成功命中 core.xsd。
	var names []string
	for _, pp := range p.Operations[0].InputParams {
		names = append(names, pp.Name)
	}
	if len(names) == 0 || !containsStr(names, "extra") {
		t.Errorf("外部 XSD 未被正确加载, InputParams names = %v", names)
	}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestSafeDialContext_RefusesDangerous 验证 link-local 在 resolve 阶段就被拒绝。
func TestSafeDialContext_RefusesDangerous(t *testing.T) {
	_, err := SafeDialContext(context.Background(), "tcp", "169.254.169.254:80")
	if err == nil {
		t.Fatal("link-local 应被拒绝")
	}
	if !strings.Contains(err.Error(), "危险地址") {
		t.Errorf("错误应说明危险地址, got %q", err)
	}
}

// TestResolveCheckedHostIPs_IPv4Literal 验证 IP 字面量直接返回。
func TestResolveCheckedHostIPs_IPv4Literal(t *testing.T) {
	ips, err := resolveCheckedHostIPs(context.Background(), "192.168.1.10")
	if err != nil {
		t.Fatalf("普通 IP 不应报错: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "192.168.1.10" {
		t.Errorf("ips = %v", ips)
	}
}

// TestSafeDialContext_IPLiteralDial 验证 IP 字面量能正常建连（不依赖 DNS）。
func TestSafeDialContext_IPLiteralDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	// host 形如 127.0.0.1:port
	conn, err := SafeDialContext(context.Background(), "tcp", host)
	if err != nil {
		t.Fatalf("IP 字面量建连失败: %v", err)
	}
	_ = conn.Close()
}
