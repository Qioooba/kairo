package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestWSDLImportURL_NoIPRestriction 验证 /api/wsdl/import-url：
//   1) 整体能成功拉取并解析 WSDL
//   2) SSRF IP 校验代码已彻底移除（不再因公网 IP 拒绝）
func TestWSDLImportURL_NoIPRestriction(t *testing.T) {
	const sample = `<?xml version="1.0" encoding="UTF-8"?>
<definitions name="T"
  targetNamespace="urn:t"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns="http://schemas.xmlsoap.org/wsdl/">
  <types><xsd:schema targetNamespace="urn:t">
    <xsd:element name="R"><xsd:complexType><xsd:sequence>
      <xsd:element name="x" type="xsd:string"/></xsd:sequence></xsd:complexType></xsd:element>
  </xsd:schema></types>
  <message name="R"><part name="p" element="xsd:R"/></message>
  <portType name="P"><operation name="Op">
    <input message="R"/><output message="R"/></operation></portType>
  <binding name="B" type="P"><soap:binding style="rpc"
    transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Op"><soap:operation soapAction="Op"/>
      <input><soap:body use="encoded"/></input>
      <output><soap:body use="encoded"/></output></operation></binding>
  <service name="S"><port name="P" binding="B">
    <soap:address location="http://example.com/s"/></port></service>
</definitions>`

	// (1) 端到端：起 httptest server 模拟 WSDL 端点，POST import-url 应返回 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = io.WriteString(w, sample)
	}))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{
		"url":  srv.URL + "/test.wsdl",
		"name": "no-restriction-test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/wsdl/import-url", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	(&Server{}).handleWSDLImportURL(rr, req)

	if rr.Code != 200 {
		t.Fatalf("链路失败 status=%d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "安全限制") {
		t.Fatalf("不应出现 SSRF 限制错误，body=%s", rr.Body.String())
	}
	t.Logf("PASS (1/2): 整体链路 200 OK，WSDL 已解析")

	// (2) 静态断言：源码里不应再含 IP 段校验代码
	src, err := os.ReadFile("internal/httpserver/handlers_webservice.go")
	if err != nil {
		// 备选：从工作目录找
		if cwd, _ := os.Getwd(); cwd != "" {
			src, err = os.ReadFile("handlers_webservice.go")
		}
		if err != nil {
			t.Fatalf("读源码失败: %v", err)
		}
	}
	srcStr := string(src)
	if strings.Contains(srcStr, "IsLoopback") || strings.Contains(srcStr, "IsPrivate") {
		t.Fatalf("SSRF IP 校验代码仍在 handlers_webservice.go 中，需彻底移除")
	}
	if !strings.Contains(srcStr, "不做 IP 段限制") {
		t.Fatalf("源码缺少'不做 IP 段限制'标记注释，修复痕迹可能丢失")
	}
	t.Logf("PASS (2/2): 源码中无 IsLoopback/IsPrivate 校验，注释标记就位")
}
