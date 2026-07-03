package webservice

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHengLi_WSDLParse(t *testing.T) {
	testDir := filepath.Join("testdata", "hengli")
	srv := httptest.NewServer(http.FileServer(http.Dir(testDir)))
	defer srv.Close()

	wsdlURL := srv.URL + "/SuLianLoanHengLiService.wsdl"

	resp, err := http.Get(wsdlURL)
	if err != nil {
		t.Fatalf("下载 WSDL: %v", err)
	}
	defer resp.Body.Close()
	data, err := os.ReadFile(filepath.Join(testDir, "SuLianLoanHengLiService.wsdl"))
	if err != nil {
		t.Fatalf("读取本地 WSDL: %v", err)
	}

	proj := ParseWSDL(string(data), wsdlURL)

	t.Logf("项目名: %s", proj.Name)
	t.Logf("TargetNS: %s", proj.TargetNS)
	t.Logf("SOAP版本: %s", proj.SOAPVersion)
	t.Logf("Services: %d", len(proj.Services))
	for _, svc := range proj.Services {
		t.Logf("  Service: %s", svc.Name)
		for _, port := range svc.Ports {
			t.Logf("    Port: %s endpoint=%s soapVer=%s", port.Name, port.Endpoint, port.SOAPVersion)
		}
	}
	t.Logf("Operations: %d", len(proj.Operations))
	for _, op := range proj.Operations {
		t.Logf("  Operation: %s", op.Name)
		t.Logf("    Endpoint: %s", op.Endpoint)
		t.Logf("    SOAPAction: %q", op.SOAPAction)
		t.Logf("    SOAPVersion: %s", op.SOAPVersion)
		t.Logf("    Namespace: %s", op.Namespace)
		t.Logf("    Style: %s", op.Style)
		t.Logf("    InputName: %s", op.InputName)
		t.Logf("    InputParams: %d", len(op.InputParams))
		for _, p := range op.InputParams {
			t.Logf("      Param: %s type=%s min=%s max=%s nillable=%v",
				p.Name, p.Type, p.MinOccurs, p.MaxOccurs, p.Nillable)
			if len(p.Children) > 0 {
				for _, c := range p.Children {
					t.Logf("        Child: %s type=%s", c.Name, c.Type)
				}
			}
		}
		if op.InputRaw != "" {
			t.Logf("    InputRaw: %s", op.InputRaw[:min(200, len(op.InputRaw))]+"...")
		}
	}
	t.Logf("Warnings: %v", proj.Warnings)
	t.Logf("ParseError: %s", proj.ParseError)

	if len(proj.Operations) == 0 {
		t.Error("未解析到任何 operation")
	}
	if len(proj.Operations) > 0 {
		op := proj.Operations[0]
		if op.Name != "querySuLianLoanHengLiService" {
			t.Errorf("operation 名称不对: %s", op.Name)
		}
		if op.Endpoint != "http://66.1.43.100:8888/esxsp/services/SuLianLoanHengLiService" {
			t.Errorf("endpoint 不对: %s", op.Endpoint)
		}
	}
}

func TestHengLi_GenerateEnvelope(t *testing.T) {
	testDir := filepath.Join("testdata", "hengli")
	srv := httptest.NewServer(http.FileServer(http.Dir(testDir)))
	defer srv.Close()

	wsdlURL := srv.URL + "/SuLianLoanHengLiService.wsdl"
	data, err := os.ReadFile(filepath.Join(testDir, "SuLianLoanHengLiService.wsdl"))
	if err != nil {
		t.Fatalf("读取本地 WSDL: %v", err)
	}

	proj := ParseWSDL(string(data), wsdlURL)
	if len(proj.Operations) == 0 {
		t.Skip("没有解析到 operation")
	}

	op := proj.Operations[0]
	t.Logf("生成 operation: %s", op.Name)
	t.Logf("namespace: %s", op.Namespace)
	t.Logf("input params: %d", len(op.InputParams))

	env := GenerateEnvelope(op, "1.1")
	t.Logf("\n生成的 SOAP Envelope:\n%s", env)

	if !strings.Contains(env, "http://schemas.xmlsoap.org/soap/envelope/") {
		t.Error("缺 SOAP 1.1 envelope namespace")
	}
	if !strings.Contains(env, "<SuLianLoanHengLiRequest") {
		t.Error("缺请求根元素")
	}
	if !strings.Contains(env, "<SEQ_NO>") {
		t.Error("缺 ESB 报文头 SEQ_NO")
	}
	if !strings.Contains(env, "<creditcode>") {
		t.Error("缺业务参数 creditcode")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}