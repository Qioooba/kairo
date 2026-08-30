package httpserver

// handlers_webservice_test.go — WebService 调试中心 HTTP 端点测试。
//
// 覆盖：
//   - WSDL URL 导入（用 httptest 起一个本地服务返回 WSDL 文本）
//   - WSDL 文件导入（直接 POST 文本）
//   - WSDL 项目保存 / 列表 / 取单个 / 删除
//   - SOAP 报文生成
//   - SOAP 发送（用 httptest 起 mock SOAP 服务端，成功 + 500）
//   - 模板 CRUD
//   - 历史 CRUD + 清空
//   - Mock 配置 CRUD + Mock 路由实际可访问
//   - XML format / minify / validate
//
// 所有测试都用 newTestServer 构造的真实 Server + 临时 data 目录，避免互相污染。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sampleWSDLForHandler 一个最小可解析的 document/literal wrapped WSDL。
const sampleWSDLForHandler = `<?xml version="1.0" encoding="UTF-8"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="http://example.com/demo"
  targetNamespace="http://example.com/demo">
  <wsdl:types>
    <xsd:schema targetNamespace="http://example.com/demo" elementFormDefault="qualified">
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
  <wsdl:binding name="CustomerBinding" type="tns:CustomerPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <wsdl:operation name="customerQuery">
      <soap:operation soapAction="urn:customerQuery"/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
      <wsdl:output><soap:body use="literal"/></wsdl:output>
    </wsdl:operation>
  </wsdl:binding>
  <wsdl:service name="CustomerService">
    <wsdl:port name="CustomerPort" binding="tns:CustomerBinding">
      <soap:address location="http://example.com/services/Customer"/>
    </wsdl:port>
  </wsdl:service>
</wsdl:definitions>`

// TestWS_WSDLImportFile 验证 POST /api/wsdl/import-file 能解析 WSDL 并返回结构化 project。
func TestWS_WSDLImportFile(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wsdl/import-file", map[string]any{
		"content": sampleWSDLForHandler,
		"name":    "demo.wsdl",
	})
	if w.Code != 200 {
		t.Fatalf("应 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK      bool `json:"ok"`
		Project struct {
			Name       string `json:"name"`
			TargetNS   string `json:"target_ns"`
			Operations []struct {
				Name       string `json:"name"`
				SOAPAction string `json:"soap_action"`
				Endpoint   string `json:"endpoint"`
			} `json:"operations"`
			Services []struct {
				Name  string `json:"name"`
				Ports []struct {
					Name     string `json:"name"`
					Endpoint string `json:"endpoint"`
				} `json:"ports"`
			} `json:"services"`
			ParseError string `json:"parse_error"`
		} `json:"project"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("resp.ok 应为 true, body=%s", w.Body.String())
	}
	if resp.Project.ParseError != "" {
		t.Errorf("parse_error 应为空, 得到 %q", resp.Project.ParseError)
	}
	if resp.Project.TargetNS != "http://example.com/demo" {
		t.Errorf("target_ns = %q, 期望 http://example.com/demo", resp.Project.TargetNS)
	}
	if len(resp.Project.Operations) != 1 {
		t.Fatalf("operations 数量 = %d, 期望 1", len(resp.Project.Operations))
	}
	op := resp.Project.Operations[0]
	if op.Name != "customerQuery" {
		t.Errorf("op.name = %q, 期望 customerQuery", op.Name)
	}
	if op.SOAPAction != "urn:customerQuery" {
		t.Errorf("op.soap_action = %q, 期望 urn:customerQuery", op.SOAPAction)
	}
	if op.Endpoint != "http://example.com/services/Customer" {
		t.Errorf("op.endpoint = %q, 期望 http://example.com/services/Customer", op.Endpoint)
	}
	if len(resp.Project.Services) != 1 || resp.Project.Services[0].Name != "CustomerService" {
		t.Errorf("services 不正确: %+v", resp.Project.Services)
	}
}

// TestWS_WSDLImportFile_Malformed 验证非法 WSDL 不崩溃，降级返回 parse_error。
func TestWS_WSDLImportFile_Malformed(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wsdl/import-file", map[string]any{
		"content": "not a wsdl at all",
	})
	if w.Code != 200 {
		t.Fatalf("应 200（降级不报 500），得到 %d", w.Code)
	}
	var resp struct {
		Project struct {
			ParseError string `json:"parse_error"`
		} `json:"project"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Project.ParseError == "" {
		t.Errorf("parse_error 应非空，body=%s", w.Body.String())
	}
}

// TestWS_WSDLImportFile_EmptyContent 验证空 content 报 400。
func TestWS_WSDLImportFile_EmptyContent(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wsdl/import-file", map[string]any{
		"content": "  ",
	})
	if w.Code != 400 {
		t.Errorf("应 400, 得到 %d", w.Code)
	}
}

// TestWS_WSDLImportURL 验证 POST /api/wsdl/import-url 能拉取远端 WSDL。
func TestWS_WSDLImportURL(t *testing.T) {
	// 起一个本地 HTTP 服务返回 WSDL 文本
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, sampleWSDLForHandler)
	}))
	defer ts.Close()

	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wsdl/import-url", map[string]any{
		"url": ts.URL + "/demo?wsdl",
	})
	if w.Code != 200 {
		t.Fatalf("应 200, 得到 %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK      bool `json:"ok"`
		Project struct {
			Source     string `json:"source"`
			SourceURL  string `json:"source_url"`
			Operations []struct {
				Name string `json:"name"`
			} `json:"operations"`
		} `json:"project"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.OK {
		t.Fatalf("ok 应为 true, body=%s", w.Body.String())
	}
	if resp.Project.Source != "url" {
		t.Errorf("source = %q, 期望 url", resp.Project.Source)
	}
	if len(resp.Project.Operations) != 1 {
		t.Errorf("operations 数量 = %d, 期望 1", len(resp.Project.Operations))
	}
}

func TestWS_WSDLImportURL_LegacyGBKDeclaration(t *testing.T) {
	// 即使主体只有 ASCII，encoding/xml 遇到 encoding="GBK" 也会因没有
	// CharsetReader 直接失败；URL 导入层必须先统一解码为 UTF-8。
	legacy := strings.Replace(sampleWSDLForHandler, "UTF-8", "GBK", 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=GBK")
		_, _ = io.WriteString(w, legacy)
	}))
	defer ts.Close()

	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wsdl/import-url", map[string]any{"url": ts.URL + "/legacy.wsdl"})
	if w.Code != 200 {
		t.Fatalf("GBK declaration WSDL import status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Project struct {
			ParseError string `json:"parse_error"`
			Operations []any  `json:"operations"`
		} `json:"project"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Project.ParseError != "" || len(resp.Project.Operations) == 0 {
		t.Fatalf("GBK WSDL 未正确解析: %+v", resp.Project)
	}
}

// TestWS_WSDLImportURL_InvalidURL 验证非 http URL 报 400。
func TestWS_WSDLImportURL_InvalidURL(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/wsdl/import-url", map[string]any{
		"url": "ftp://example.com/foo.wsdl",
	})
	if w.Code != 400 {
		t.Errorf("应 400, 得到 %d", w.Code)
	}
}

// TestWS_WSDLProjectsCRUD 验证 WSDL 项目的保存 / 列表 / 取单个 / 删除完整流程。
func TestWS_WSDLProjectsCRUD(t *testing.T) {
	srv, _, _, _ := newTestServer(t)

	// 先导入一个
	w := doRequest(srv, "POST", "/api/wsdl/import-file", map[string]any{
		"content": sampleWSDLForHandler, "name": "crud-demo",
	})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var importResp struct {
		Project struct {
			Name       string `json:"name"`
			TargetNS   string `json:"target_ns"`
			Operations []struct {
				Name string `json:"name"`
			} `json:"operations"`
			Services []struct {
				Name string `json:"name"`
			} `json:"services"`
		} `json:"project"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &importResp)

	// 保存
	w = doRequest(srv, "POST", "/api/wsdl/projects", map[string]any{
		"project": importResp.Project,
	})
	if w.Code != 200 {
		t.Fatalf("保存应 200, 得到 %d body=%s", w.Code, w.Body.String())
	}
	var saveResp struct {
		Project struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			CreatedAt string `json:"created_at"`
		} `json:"project"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &saveResp)
	id := saveResp.Project.ID
	if id == "" {
		t.Fatal("id 不应为空")
	}

	// 列表
	w = doRequest(srv, "GET", "/api/wsdl/projects", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var listResp struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Projects) != 1 || listResp.Projects[0].ID != id {
		t.Errorf("列表应只有 1 个且 id 匹配, 得到 %+v", listResp.Projects)
	}

	// 取单个
	w = doRequest(srv, "GET", "/api/wsdl/projects/"+id, nil)
	if w.Code != 200 {
		t.Errorf("取单个应 200, 得到 %d", w.Code)
	}

	// 删除
	w = doRequest(srv, "DELETE", "/api/wsdl/projects/"+id, nil)
	if w.Code != 200 {
		t.Errorf("删除应 200, 得到 %d", w.Code)
	}

	// 再列表应空
	w = doRequest(srv, "GET", "/api/wsdl/projects", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Projects) != 0 {
		t.Errorf("删除后列表应空, 得到 %d 项", len(listResp.Projects))
	}
}

// TestWS_SOAPGenerate 验证 POST /api/soap/generate 能生成 Envelope。
func TestWS_SOAPGenerate(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/soap/generate", map[string]any{
		"operation": map[string]any{
			"name":      "customerQuery",
			"namespace": "http://example.com/demo",
			"input_params": []map[string]any{
				{"name": "custNo", "type": "xsd:string"},
				{"name": "serialNo", "type": "xsd:string"},
			},
		},
		"soap_version": "1.1",
	})
	if w.Code != 200 {
		t.Fatalf("应 200, 得到 %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Envelope string `json:"envelope"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.Contains(resp.Envelope, "soapenv:Envelope") {
		t.Errorf("envelope 缺 soapenv:Envelope: %s", resp.Envelope)
	}
	if !strings.Contains(resp.Envelope, "web:customerQuery") {
		t.Errorf("envelope 缺 web:customerQuery: %s", resp.Envelope)
	}
	// v0.14+ 不再嵌 ${...} 占位符，叶子节点默认空值，由用户直接编辑 XML
	if !strings.Contains(resp.Envelope, "<custNo></custNo>") {
		t.Errorf("envelope 缺空值字段 <custNo></custNo>: %s", resp.Envelope)
	}
	if !strings.Contains(resp.Envelope, "<serialNo></serialNo>") {
		t.Errorf("envelope 缺空值字段 <serialNo></serialNo>: %s", resp.Envelope)
	}
	if strings.Contains(resp.Envelope, "${") {
		t.Errorf("envelope 不应再含 ${...} 占位符: %s", resp.Envelope)
	}
}

// TestWS_SOAPSend_Success 验证 POST /api/soap/send 能成功发送并记录历史。
func TestWS_SOAPSend_Success(t *testing.T) {
	// mock SOAP 服务端
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `<?xml version="1.0"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><customerQueryResponse><custName>张三</custName></customerQueryResponse></soapenv:Body></soapenv:Envelope>`)
	}))
	defer ts.Close()

	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/soap/send", map[string]any{
		"endpoint":     ts.URL,
		"soap_action":  "urn:customerQuery",
		"soap_version": "1.1",
		"encoding":     "UTF-8",
		"timeout_ms":   5000,
		"body":         `<?xml version="1.0"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><customerQuery><custNo>123</custNo></customerQuery></soapenv:Body></soapenv:Envelope>`,
		"operation":    "customerQuery",
	})
	if w.Code != 200 {
		t.Fatalf("应 200, 得到 %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK       bool `json:"ok"`
		Response struct {
			OK        bool   `json:"ok"`
			Status    int    `json:"status"`
			Body      string `json:"body"`
			ElapsedMs int64  `json:"elapsed_ms"`
		} `json:"response"`
		Keywords []string `json:"keywords"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Response.OK || resp.Response.Status != 200 {
		t.Errorf("response.ok=%v status=%d, 期望 ok=true status=200", resp.Response.OK, resp.Response.Status)
	}
	if !strings.Contains(resp.Response.Body, "张三") {
		t.Errorf("response body 应含 张三: %s", resp.Response.Body)
	}
	if len(resp.Keywords) == 0 {
		t.Errorf("keywords 不应空")
	}

	// 验证历史已写入
	w = doRequest(srv, "GET", "/api/soap/history", nil)
	var histResp struct {
		History []struct {
			Operation string `json:"operation"`
			Success   bool   `json:"success"`
		} `json:"history"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &histResp)
	if len(histResp.History) != 1 {
		t.Errorf("历史应有 1 条, 得到 %d", len(histResp.History))
	}
	if histResp.History[0].Operation != "customerQuery" || !histResp.History[0].Success {
		t.Errorf("历史条目不正确: %+v", histResp.History[0])
	}
}

// TestWS_SOAPSend_500Response 验证 5xx 响应 ok=false 但仍返回 body。
func TestWS_SOAPSend_500Response(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(500)
		_, _ = io.WriteString(w, `<soapenv:Fault><faultcode>Server</faultcode><faultstring>internal error</faultstring></soapenv:Fault>`)
	}))
	defer ts.Close()

	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/soap/send", map[string]any{
		"endpoint":     ts.URL,
		"soap_version": "1.1",
		"timeout_ms":   5000,
		"body":         "<x/>",
	})
	var resp struct {
		Response struct {
			OK     bool   `json:"ok"`
			Status int    `json:"status"`
			Body   string `json:"body"`
		} `json:"response"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Response.OK {
		t.Errorf("500 响应 ok 应为 false")
	}
	if resp.Response.Status != 500 {
		t.Errorf("status = %d, 期望 500", resp.Response.Status)
	}
	if !strings.Contains(resp.Response.Body, "fault") && !strings.Contains(resp.Response.Body, "Fault") {
		t.Errorf("body 应含 fault: %s", resp.Response.Body)
	}
}

// TestWS_SOAPSend_InvalidEndpoint 验证非法 endpoint 返回 ok=false + 错误信息。
func TestWS_SOAPSend_InvalidEndpoint(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/soap/send", map[string]any{
		"endpoint": "not-a-url",
		"body":     "<x/>",
	})
	var resp struct {
		Response struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"response"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Response.OK {
		t.Errorf("ok 应为 false")
	}
	if resp.Response.Error == "" {
		t.Errorf("error 应非空")
	}
}

// TestWS_TemplatesCRUD 验证模板完整 CRUD 流程。
func TestWS_TemplatesCRUD(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	// 新建
	w := doRequest(srv, "POST", "/api/soap/templates", map[string]any{
		"template": map[string]any{
			"name":        "tpl-1",
			"group":       "test",
			"endpoint":    "http://example.com",
			"operation":   "op1",
			"soap_action": "urn:op1",
			"body":        "<x/>",
			"encoding":    "UTF-8",
			"timeout_ms":  30000,
		},
	})
	if w.Code != 200 {
		t.Fatalf("新建应 200, 得到 %d body=%s", w.Code, w.Body.String())
	}
	var saveResp struct {
		Template struct {
			ID string `json:"id"`
		} `json:"template"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &saveResp)
	id := saveResp.Template.ID
	if id == "" {
		t.Fatal("id 不应空")
	}

	// 列表
	w = doRequest(srv, "GET", "/api/soap/templates", nil)
	var listResp struct {
		Templates []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"templates"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Templates) != 1 || listResp.Templates[0].ID != id {
		t.Errorf("列表应只有 1 个且 id 匹配, 得到 %+v", listResp.Templates)
	}

	// 删除
	w = doRequest(srv, "DELETE", "/api/soap/templates/"+id, nil)
	if w.Code != 200 {
		t.Errorf("删除应 200, 得到 %d", w.Code)
	}
	// 再列表应空
	w = doRequest(srv, "GET", "/api/soap/templates", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Templates) != 0 {
		t.Errorf("删除后应空, 得到 %d 项", len(listResp.Templates))
	}
}

// TestWS_Templates_EmptyName 验证 name 不能空。
func TestWS_Templates_EmptyName(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/soap/templates", map[string]any{
		"template": map[string]any{"name": "  ", "body": "<x/>"},
	})
	if w.Code != 400 {
		t.Errorf("应 400, 得到 %d", w.Code)
	}
}

// TestWS_HistoryClear 验证历史一键清空。
func TestWS_HistoryClear(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	// 先写一条历史：通过发送请求
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<x/>")
	}))
	defer ts.Close()
	_ = doRequest(srv, "POST", "/api/soap/send", map[string]any{
		"endpoint": ts.URL, "soap_version": "1.1", "body": "<x/>", "timeout_ms": 5000,
	})

	// 确认有 1 条
	w := doRequest(srv, "GET", "/api/soap/history", nil)
	var listResp struct {
		History []struct{} `json:"history"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.History) != 1 {
		t.Fatalf("应有 1 条历史, 得到 %d", len(listResp.History))
	}

	// 清空
	w = doRequest(srv, "DELETE", "/api/soap/history", nil)
	if w.Code != 200 {
		t.Errorf("清空应 200, 得到 %d", w.Code)
	}
	// 再查应空
	w = doRequest(srv, "GET", "/api/soap/history", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.History) != 0 {
		t.Errorf("清空后应空, 得到 %d 项", len(listResp.History))
	}
}

// TestWS_HistoryReplay 验证历史重放：发送一次 → 取历史 → replay。
func TestWS_HistoryReplay(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<replay-ok/>")
	}))
	defer ts.Close()
	srv, _, _, _ := newTestServer(t)
	// 发送一次
	_ = doRequest(srv, "POST", "/api/soap/send", map[string]any{
		"endpoint": ts.URL, "soap_version": "1.1", "body": "<x/>", "timeout_ms": 5000,
	})
	// 取历史
	w := doRequest(srv, "GET", "/api/soap/history", nil)
	var listResp struct {
		History []struct {
			ID string `json:"id"`
		} `json:"history"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.History) == 0 {
		t.Fatal("历史应非空")
	}
	id := listResp.History[0].ID
	// replay
	w = doRequest(srv, "POST", "/api/soap/history/"+id+"/replay", map[string]any{})
	if w.Code != 200 {
		t.Fatalf("replay 应 200, 得到 %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Response struct {
			OK     bool   `json:"ok"`
			Status int    `json:"status"`
			Body   string `json:"body"`
		} `json:"response"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Response.OK || resp.Response.Status != 200 {
		t.Errorf("replay response.ok=%v status=%d", resp.Response.OK, resp.Response.Status)
	}
	if !strings.Contains(resp.Response.Body, "replay-ok") {
		t.Errorf("replay body 应含 replay-ok: %s", resp.Response.Body)
	}
	// 历史应多一条（replay 也记）
	w = doRequest(srv, "GET", "/api/soap/history", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.History) != 2 {
		t.Errorf("历史应 2 条（原 1 + replay 1）, 得到 %d", len(listResp.History))
	}
}

// TestWS_MocksCRUD 验证 Mock 配置 CRUD + Mock 路由实际可访问。
func TestWS_MocksCRUD(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	// 新建 mock
	w := doRequest(srv, "POST", "/api/soap/mocks", map[string]any{
		"mock": map[string]any{
			"name":        "mock-1",
			"path":        "/mock/test1",
			"status_code": 200,
			"delay_ms":    0,
			"enabled":     true,
			"body":        "<mockResp>ok</mockResp>",
		},
	})
	if w.Code != 200 {
		t.Fatalf("新建 mock 应 200, 得到 %d body=%s", w.Code, w.Body.String())
	}
	var saveResp struct {
		Mock struct {
			ID string `json:"id"`
		} `json:"mock"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &saveResp)
	id := saveResp.Mock.ID
	if id == "" {
		t.Fatal("id 不应空")
	}

	// 列表
	w = doRequest(srv, "GET", "/api/soap/mocks", nil)
	var listResp struct {
		Mocks []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"mocks"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Mocks) != 1 {
		t.Errorf("列表应 1 项, 得到 %d", len(listResp.Mocks))
	}

	// 直接访问 mock 路由（外部系统调用，不走 /api/ 鉴权）
	req := httptest.NewRequest("POST", "/mock/test1", strings.NewReader("<req/>"))
	req.Header.Set("Content-Type", "text/xml")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("mock 路由应 200, 得到 %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "mockResp") {
		t.Errorf("mock body 应含 mockResp: %s", rec.Body.String())
	}

	// 验证 mock 请求记录已写入（异步落盘，先 flush）
	srv.wsMocks.FlushRecords()
	w = doRequest(srv, "GET", "/api/soap/mocks/records", nil)
	var recResp struct {
		Records []struct {
			Path string `json:"path"`
		} `json:"records"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &recResp)
	if len(recResp.Records) == 0 {
		t.Errorf("mock records 应非空")
	}

	// 删除 mock
	w = doRequest(srv, "DELETE", "/api/soap/mocks/"+id, nil)
	if w.Code != 200 {
		t.Errorf("删除应 200, 得到 %d", w.Code)
	}
	// 再访问 mock 路由应 404
	req = httptest.NewRequest("POST", "/mock/test1", strings.NewReader("<req/>"))
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Errorf("删除后 mock 应 404, 得到 %d", rec.Code)
	}
	srv.wsMocks.FlushRecords()
}

// TestWS_MockDisabled 验证 enabled=false 的 mock 不会被路由。
func TestWS_MockDisabled(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	_ = doRequest(srv, "POST", "/api/soap/mocks", map[string]any{
		"mock": map[string]any{
			"name":    "disabled-mock",
			"path":    "/mock/disabled",
			"enabled": false,
			"body":    "<x/>",
		},
	})
	// 访问应 404（因为 disabled 没注册路由）
	req := httptest.NewRequest("GET", "/mock/disabled", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Errorf("disabled mock 应 404, 得到 %d", rec.Code)
	}
	srv.wsMocks.FlushRecords()
}

// TestWS_XML_FormatMinifyValidate 验证 XML format / minify / validate 端点。
func TestWS_XML_FormatMinifyValidate(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	// format
	w := doRequest(srv, "POST", "/api/ws/xml/format", map[string]any{
		"input": "<a><b>1</b></a>",
	})
	if w.Code != 200 {
		t.Fatalf("format 应 200, 得到 %d", w.Code)
	}
	var resp struct {
		Output string `json:"output"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.Contains(resp.Output, "\n") {
		t.Errorf("format 后应含换行: %q", resp.Output)
	}
	// minify
	w = doRequest(srv, "POST", "/api/ws/xml/minify", map[string]any{
		"input": "<a>\n  <b>1</b>\n</a>",
	})
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if strings.Contains(resp.Output, "\n") {
		t.Errorf("minify 后不应含换行: %q", resp.Output)
	}
	// validate 合法
	w = doRequest(srv, "POST", "/api/ws/xml/validate", map[string]any{
		"input": "<a><b>1</b></a>",
	})
	var vresp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &vresp)
	if !vresp.OK {
		t.Errorf("合法 XML validate 应 ok=true, error=%s", vresp.Error)
	}
	// validate 非法（标签不匹配）
	w = doRequest(srv, "POST", "/api/ws/xml/validate", map[string]any{
		"input": "<a><b></a>",
	})
	_ = json.Unmarshal(w.Body.Bytes(), &vresp)
	if vresp.OK {
		t.Errorf("标签不匹配应 ok=false")
	}
	if vresp.Error == "" {
		t.Errorf("error 应非空")
	}
}

// TestWS_NotFound 验证未知 /api/soap/* 路径返 404。
func TestWS_NotFound(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/soap/nonexistent", nil)
	if w.Code != 404 {
		t.Errorf("应 404, 得到 %d", w.Code)
	}
}

// TestWS_MethodNotAllowed 验证 GET /api/soap/send 报 405。
func TestWS_MethodNotAllowed(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/soap/send", nil)
	if w.Code != 405 {
		t.Errorf("应 405, 得到 %d", w.Code)
	}
}

// TestWS_MockRecordsClear 验证 mock records 清空。
func TestWS_MockRecordsClear(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	// 先创建一个 mock 并访问一次
	_ = doRequest(srv, "POST", "/api/soap/mocks", map[string]any{
		"mock": map[string]any{
			"name": "test", "path": "/mock/clear-test", "enabled": true, "body": "<x/>",
		},
	})
	req := httptest.NewRequest("GET", "/mock/clear-test", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	// 确认有记录（异步落盘，先 flush）
	srv.wsMocks.FlushRecords()
	w := doRequest(srv, "GET", "/api/soap/mocks/records", nil)
	var r1 struct {
		Records []struct{} `json:"records"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &r1)
	if len(r1.Records) == 0 {
		t.Fatal("应有 1 条记录")
	}
	// 清空
	w = doRequest(srv, "DELETE", "/api/soap/mocks/records", nil)
	if w.Code != 200 {
		t.Errorf("清空应 200, 得到 %d", w.Code)
	}
	// 再查应空
	w = doRequest(srv, "GET", "/api/soap/mocks/records", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &r1)
	if len(r1.Records) != 0 {
		t.Errorf("清空后应 0 条, 得到 %d", len(r1.Records))
	}
}
