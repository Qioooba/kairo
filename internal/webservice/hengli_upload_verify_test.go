package webservice

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHengLi_UploadModeWithAttachments 模拟前端"导入文件"全流程：
// 上传 WSDL + 2 个 XSD 附件，验证外部 XSD 从附件正确加载、operation 参数完整展开。
func TestHengLi_UploadModeWithAttachments(t *testing.T) {
	testDir := filepath.Join("testdata", "hengli")
	wsdl, err := os.ReadFile(filepath.Join(testDir, "SuLianLoanHengLiService.wsdl"))
	if err != nil {
		t.Fatal(err)
	}
	core, err := os.ReadFile(filepath.Join(testDir, "SuLianLoanHengLiServiceCore.xsd"))
	if err != nil {
		t.Fatal(err)
	}
	esb, err := os.ReadFile(filepath.Join(testDir, "esb.xsd"))
	if err != nil {
		t.Fatal(err)
	}
	attachments := map[string]string{
		"SuLianLoanHengLiServiceCore.xsd": string(core),
		"esb.xsd":                         string(esb),
	}
	p := ParseWSDL(string(wsdl), attachments)
	if p.ParseError != "" {
		t.Fatalf("ParseError: %s", p.ParseError)
	}
	if len(p.Operations) != 1 {
		t.Fatalf("operations = %d", len(p.Operations))
	}
	op := p.Operations[0]
	if len(op.InputParams) != 1 || len(op.InputParams[0].Children) == 0 {
		t.Fatalf("附件模式下外部 XSD 未展开: %+v", op.InputParams)
	}
	// ESB 头（esb.xsd）与业务字段（core.xsd）都要在
	names := map[string]bool{}
	var walk func(pp []Param)
	walk = func(pp []Param) {
		for _, x := range pp {
			names[x.Name] = true
			walk(x.Children)
		}
	}
	walk(op.InputParams)
	for _, want := range []string{"SEQ_NO", "EXT_HEAD", "creditcode", "custType"} {
		if !names[want] {
			t.Errorf("附件模式下缺少字段 %q", want)
		}
	}
	env := GenerateEnvelope(op, "1.1")
	if env == "" || !containsLocalName(env, "SuLianLoanHengLiRequest") {
		t.Errorf("生成 envelope 失败:\n%s", env)
	}
	t.Logf("上传模式解析 OK: %d 个字段, envelope 根元素正确", len(names))
}
