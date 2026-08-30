package webservice

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// bytesContain 检查字节切片里是否连续出现 b1,b2。
func bytesContain(s []byte, b1, b2 byte) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == b1 && s[i+1] == b2 {
			return true
		}
	}
	return false
}

func TestGenerateEnvelope_NamespaceAndEmptyValues(t *testing.T) {
	op := Operation{
		Name:      "customerQuery",
		Namespace: "http://cust.example.com/",
		InputParams: []Param{
			{Name: "custNo", Type: "xsd:string"},
			{Name: "serialNo", Type: "xsd:string"},
			{Name: "addr", Children: []Param{
				{Name: "city", Type: "xsd:string"},
				{Name: "street", Type: "xsd:string"},
			}},
		},
	}
	env := GenerateEnvelope(op, "1.1")
	// namespace
	if !strings.Contains(env, `xmlns:web="http://cust.example.com/"`) {
		t.Errorf("envelope missing web namespace: %s", env)
	}
	if !strings.Contains(env, "http://schemas.xmlsoap.org/soap/envelope/") {
		t.Errorf("envelope missing SOAP 1.1 env ns")
	}
	// operation node
	if !strings.Contains(env, "<web:customerQuery>") {
		t.Errorf("envelope missing operation node: %s", env)
	}
	// 叶子节点：空值形式（<tag></tag>），不再嵌入 ${name} 占位
	if !strings.Contains(env, "<custNo></custNo>") {
		t.Errorf("missing empty <custNo></custNo>: %s", env)
	}
	if !strings.Contains(env, "<serialNo></serialNo>") {
		t.Errorf("missing empty <serialNo></serialNo>: %s", env)
	}
	// 严禁出现旧的 ${...} 占位符
	if strings.Contains(env, "${") {
		t.Errorf("envelope should not embed ${...} placeholders anymore: %s", env)
	}
	// 嵌套子节点也是空值
	if !strings.Contains(env, "<addr>") {
		t.Errorf("missing nested container: %s", env)
	}
	if !strings.Contains(env, "<city></city>") {
		t.Errorf("missing empty nested <city></city>: %s", env)
	}
}

func TestGenerateEnvelope_SOAP12(t *testing.T) {
	op := Operation{Name: "ping", Namespace: "http://x/"}
	env := GenerateEnvelope(op, "1.2")
	if !strings.Contains(env, "http://www.w3.org/2003/05/soap-envelope") {
		t.Errorf("SOAP 1.2 envelope should use 1.2 env ns: %s", env)
	}
}

func TestGenerateEnvelope_NoNamespace(t *testing.T) {
	op := Operation{Name: "ping", InputParams: []Param{{Name: "a"}}}
	env := GenerateEnvelope(op, "1.1")
	if strings.Contains(env, "xmlns:web") {
		t.Errorf("should not add web ns when namespace empty: %s", env)
	}
	if !strings.Contains(env, "<ping>") {
		t.Errorf("should use bare operation tag: %s", env)
	}
}

func TestFormatXML_Minify_Validate(t *testing.T) {
	raw := `<a><b>1</b><c><d>2</d></c></a>`
	formatted, err := FormatXML(raw, "  ")
	if err != nil {
		t.Fatalf("FormatXML: %v", err)
	}
	if !strings.Contains(formatted, "\n") {
		t.Errorf("formatted should contain newlines: %q", formatted)
	}
	minified, err := MinifyXML(formatted)
	if err != nil {
		t.Fatalf("MinifyXML: %v", err)
	}
	if strings.Contains(minified, "\n  ") {
		t.Errorf("minified should not keep indentation: %q", minified)
	}
	if err := ValidateXML(formatted); err != nil {
		t.Errorf("formatted should be valid: %v", err)
	}
	if err := ValidateXML(`<a><b></a>`); err == nil {
		t.Error("malformed should fail validate")
	}
	if _, err := FormatXML("", "  "); err == nil {
		t.Error("empty should fail")
	}
}

func TestFormatXML_PreservesSignificantWhitespaceAndMixedContent(t *testing.T) {
	raw := `<Envelope><fixed>  A B  </fixed><mixed>Hello <b>world</b> !</mixed><empty> </empty></Envelope>`
	formatted, err := FormatXML(raw, "  ")
	if err != nil {
		t.Fatalf("FormatXML: %v", err)
	}
	for _, want := range []string{
		`<fixed>  A B  </fixed>`,
		`<mixed>Hello <b>world</b> !</mixed>`,
		`<empty> </empty>`,
	} {
		if !strings.Contains(formatted, want) {
			t.Errorf("格式化改变了 XML 文本语义，缺少 %q:\n%s", want, formatted)
		}
	}
	minified, err := MinifyXML(formatted)
	if err != nil {
		t.Fatalf("MinifyXML: %v", err)
	}
	if minified != raw {
		t.Errorf("format → minify 应恢复原始紧凑报文\n got: %q\nwant: %q", minified, raw)
	}
	if _, err := FormatXML(`<a><b></a>`, "  "); err == nil {
		t.Error("FormatXML 不应接受标签不匹配的 XML")
	}
}

func TestGenerateEnvelope_BareMissingXSDDoesNotDoubleWrap(t *testing.T) {
	op := Operation{
		Name:      "queryService",
		InputName: "QueryRequest",
		Namespace: "urn:query",
		InputParams: []Param{{
			Name: "QueryRequest",
			Type: "ext:QueryRequest",
		}},
	}
	env := GenerateEnvelope(op, "1.1")
	if strings.Count(env, "<QueryRequest") != 1 {
		t.Fatalf("缺外部 XSD 的 bare 请求根元素只能出现一次:\n%s", env)
	}
}

func TestSuggestLogKeywords(t *testing.T) {
	body := `<?xml version="1.0"?>
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
  <soapenv:Body>
    <web:customerQuery xmlns:web="http://x/">
      <custNo>C001</custNo>
      <serialNo>SN20260702001</serialNo>
      <requestId>REQ-12345</requestId>
    </web:customerQuery>
  </soapenv:Body>
</soapenv:Envelope>`
	kw := SuggestLogKeywords("customerQuery", `"http://x/customerQuery"`, body)
	joined := strings.Join(kw, ",")
	// 必须保留原始大小写：大写流水号 SN20260702001 / REQ-12345 不能被小写化，
	// 否则拿去搜日志会匹配不到。
	for _, want := range []string{"customerQuery", "http://x/customerQuery", "SN20260702001", "REQ-12345"} {
		if !strings.Contains(joined, want) {
			t.Errorf("keywords missing %q in %v", want, kw)
		}
	}
}

// ---------- SOAP 发送测试 ----------

func TestSend_Success(t *testing.T) {
	var gotBody, gotCT, gotAction string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotAction = r.Header.Get("SOAPAction")
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><resp><custName>Zhang</custName></resp></soapenv:Body></soapenv:Envelope>`))
	}))
	defer srv.Close()

	resp := Send(SendRequest{
		Endpoint:   srv.URL,
		SOAPAction: "http://x/customerQuery",
		Body:       `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><q><custNo>C001</custNo></q></soapenv:Body></soapenv:Envelope>`,
		Encoding:   "UTF-8",
		TimeoutMs:  5000,
	})
	if !resp.OK {
		t.Fatalf("send failed: %s", resp.Error)
	}
	if resp.Status != 200 {
		t.Errorf("status = %d", resp.Status)
	}
	if !strings.Contains(gotCT, "text/xml") {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if gotAction != `"http://x/customerQuery"` {
		t.Errorf("SOAPAction = %q (want quoted)", gotAction)
	}
	if !strings.Contains(gotBody, "custNo") {
		t.Errorf("request body not forwarded: %q", gotBody)
	}
	if !strings.Contains(resp.Body, "Zhang") {
		t.Errorf("response body missing custName: %s", resp.Body)
	}
	if resp.ElapsedMs < 0 {
		t.Errorf("elapsed negative: %d", resp.ElapsedMs)
	}
}

func TestSend_500Response(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><soapenv:Fault><faultcode>Server</faultcode><faultstring>boom</faultstring></soapenv:Fault>`))
	}))
	defer srv.Close()

	resp := Send(SendRequest{Endpoint: srv.URL, Body: "<x/>", TimeoutMs: 5000})
	if resp.OK {
		t.Errorf("500 should be OK=false")
	}
	if resp.Status != 500 {
		t.Errorf("status = %d, want 500", resp.Status)
	}
	if !strings.Contains(resp.Body, "boom") {
		t.Errorf("should return fault body: %s", resp.Body)
	}
}

func TestSend_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	resp := Send(SendRequest{Endpoint: srv.URL, Body: "<x/>", TimeoutMs: 100})
	if resp.OK {
		t.Errorf("timeout should be OK=false")
	}
	if resp.Error == "" {
		t.Errorf("timeout should report error")
	}
	if !strings.Contains(resp.Error, "超时") && !strings.Contains(resp.Error, "timeout") && !strings.Contains(resp.Error, "Timeout") {
		t.Errorf("error should mention timeout: %s", resp.Error)
	}
}

func TestSend_InvalidEndpoint(t *testing.T) {
	resp := Send(SendRequest{Endpoint: "", Body: "<x/>"})
	if resp.OK || resp.Error == "" {
		t.Errorf("empty endpoint should fail")
	}
	resp = Send(SendRequest{Endpoint: "ftp://x", Body: "<x/>"})
	if resp.OK || resp.Error == "" {
		t.Errorf("non-http endpoint should fail")
	}
	resp = Send(SendRequest{Endpoint: "http://x", Body: "<x/>", Encoding: "BIG5"})
	if resp.OK || resp.Error == "" {
		t.Errorf("unsupported encoding should fail")
	}
}

func TestSend_GBKRoundTrip(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		received = buf[:n]
		w.Header().Set("Content-Type", "text/xml; charset=GBK")
		w.WriteHeader(200)
		// 返回 GBK 编码的 "张"
		w.Write([]byte{0xD5, 0xC5})
	}))
	defer srv.Close()

	resp := Send(SendRequest{
		Endpoint:  srv.URL,
		Body:      "<x>张</x>",
		Encoding:  "GBK",
		TimeoutMs: 5000,
	})
	if !resp.OK {
		t.Fatalf("send failed: %s", resp.Error)
	}
	// 请求体应是 GBK 编码（"张" = 0xD5 0xC5）。整体为 "<x>" + 0xD5 0xC5 + "</x>"
	if len(received) < 2 || !bytesContain(received, 0xD5, 0xC5) {
		t.Errorf("request body not GBK-encoded: % x", received)
	}
	// 响应应解码回 "张"
	if !strings.Contains(resp.Body, "张") {
		t.Errorf("response not GBK-decoded: %q", resp.Body)
	}
}

func TestSend_SOAP12AndEncodingDeclaration(t *testing.T) {
	var gotCT, gotAction string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotAction = r.Header.Get("SOAPAction")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/soap+xml; charset=UTF-8")
		_, _ = w.Write([]byte(`<ok/>`))
	}))
	defer srv.Close()

	resp := Send(SendRequest{
		Endpoint:    srv.URL,
		SOAPAction:  `urn:test-action`,
		SOAPVersion: "1.2",
		Encoding:    "GBK",
		Body:        `<?xml version="1.0" encoding="UTF-8"?><请求>中文</请求>`,
		TimeoutMs:   5000,
	})
	if !resp.OK {
		t.Fatalf("Send: %s", resp.Error)
	}
	if gotAction != "" {
		t.Errorf("SOAP 1.2 不应发送 SOAPAction header, got %q", gotAction)
	}
	if !strings.Contains(gotCT, `application/soap+xml`) || !strings.Contains(gotCT, `action="urn:test-action"`) || !strings.Contains(gotCT, `charset=GBK`) {
		t.Errorf("SOAP 1.2 Content-Type 不完整: %q", gotCT)
	}
	decoded, ok := decodeChinese(gotBody, "GBK")
	if !ok || !strings.Contains(decoded, `encoding="GBK"`) || !strings.Contains(decoded, `中文`) {
		t.Errorf("线上的 GBK XML declaration/正文不一致: %q", decoded)
	}
}

func TestDecodeResponseBody_PrefersValidXMLDeclaration(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?><name>张三</name>`)
	got := decodeResponseBody(raw, "text/xml; charset=GBK", "GBK")
	if !strings.Contains(got, "张三") {
		t.Errorf("HTTP charset 错报 GBK、XML 实际 UTF-8 时应避免乱码: %q", got)
	}
}
