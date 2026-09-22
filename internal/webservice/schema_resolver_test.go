package webservice

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T048: 验证不同子目录下的同名 XSD (a/common.xsd 与 b/common.xsd) 不会被 basename 覆盖，
// 两个命名空间的类型均完整保留并在 WSDL 中正确展开。
func TestSchemaResolver_T048_MultiDirSameNameXSD(t *testing.T) {
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
    <xsd:sequence>
      <xsd:element name="fieldA" type="xsd:string"/>
    </xsd:sequence>
  </xsd:complexType>
</xsd:schema>`

	commonB := `<?xml version="1.0" encoding="UTF-8"?>
<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="http://example.com/nsB">
  <xsd:complexType name="TypeB">
    <xsd:sequence>
      <xsd:element name="fieldB" type="xsd:int"/>
    </xsd:sequence>
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
      <xsd:element name="CombineReq">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="itemA" type="a:TypeA"/>
            <xsd:element name="itemB" type="b:TypeB"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="CombineMsg">
    <wsdl:part name="parameters" element="tns:CombineReq"/>
  </wsdl:message>
  <wsdl:portType name="CombinePT">
    <wsdl:operation name="combine">
      <wsdl:input message="tns:CombineMsg"/>
    </wsdl:operation>
  </wsdl:portType>
  <wsdl:binding name="CombineBinding" type="tns:CombinePT">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <wsdl:operation name="combine">
      <soap:operation soapAction=""/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
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

	cfg := SchemaResolverConfig{
		BaseURI:        PathToFileURI(wsdlPath),
		AllowedRootDir: tempDir,
	}
	p := ParseWSDL(wsdl, cfg)
	if p.ParseError != "" {
		t.Fatalf("ParseError: %s", p.ParseError)
	}

	if len(p.Operations) != 1 {
		t.Fatalf("operations count = %d, want 1", len(p.Operations))
	}
	op := p.Operations[0]
	params := op.InputParams
	if len(params) == 1 && len(params[0].Children) > 0 {
		params = params[0].Children
	}
	if len(params) != 2 {
		t.Fatalf("InputParams length = %d, want 2 (itemA, itemB). Got: %+v", len(params), params)
	}

	var hasFieldA, hasFieldB bool
	for _, p := range params {
		if p.Name == "itemA" {
			for _, child := range p.Children {
				if child.Name == "fieldA" {
					hasFieldA = true
				}
			}
		}
		if p.Name == "itemB" {
			for _, child := range p.Children {
				if child.Name == "fieldB" {
					hasFieldB = true
				}
			}
		}
	}

	if !hasFieldA {
		t.Errorf("a/common.xsd 未正确展开: itemA 缺少 fieldA")
	}
	if !hasFieldB {
		t.Errorf("b/common.xsd 未正确展开: itemB 缺少 fieldB (可能被 a/common.xsd 覆盖)")
	}

	// 依赖列表检查：a/common.xsd 与 b/common.xsd 均应为 loaded 状态
	loadedCount := 0
	for _, dep := range p.Dependencies {
		if dep.Status == "loaded" {
			loadedCount++
		}
	}
	if loadedCount < 2 {
		t.Errorf("loaded dependencies count = %d, want >= 2: %+v", loadedCount, p.Dependencies)
	}
}

// T049: 相对依赖(../)、多层 include、循环引用 (A<->B)、越权读路径阻断。
func TestSchemaResolver_T049_RelativeIncludeCycleAndTraversal(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "sub")
	sharedDir := filepath.Join(tempDir, "shared")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. 相对路径 ../shared/shared.xsd
	sharedXSD := `<?xml version="1.0" encoding="UTF-8"?>
<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="http://example.com/shared">
  <xsd:include schemaLocation="shared_level2.xsd"/>
  <xsd:complexType name="SharedType">
    <xsd:sequence>
      <xsd:element name="sharedField" type="xsd:string"/>
    </xsd:sequence>
  </xsd:complexType>
</xsd:schema>`

	sharedLevel2 := `<?xml version="1.0" encoding="UTF-8"?>
<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="http://example.com/shared">
  <xsd:complexType name="Level2Type">
    <xsd:sequence>
      <xsd:element name="l2" type="xsd:string"/>
    </xsd:sequence>
  </xsd:complexType>
</xsd:schema>`

	// 循环引用：cycleA 引用 cycleB，cycleB 又引用 cycleA
	cycleA := `<?xml version="1.0" encoding="UTF-8"?>
<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="http://example.com/cycleA"
  xmlns:b="http://example.com/cycleB">
  <xsd:import namespace="http://example.com/cycleB" schemaLocation="cycleB.xsd"/>
  <xsd:complexType name="TypeCycleA">
    <xsd:sequence><xsd:element name="fromA" type="xsd:string"/></xsd:sequence>
  </xsd:complexType>
</xsd:schema>`

	cycleB := `<?xml version="1.0" encoding="UTF-8"?>
<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="http://example.com/cycleB"
  xmlns:a="http://example.com/cycleA">
  <xsd:import namespace="http://example.com/cycleA" schemaLocation="cycleA.xsd"/>
  <xsd:complexType name="TypeCycleB">
    <xsd:sequence><xsd:element name="fromB" type="xsd:string"/></xsd:sequence>
  </xsd:complexType>
</xsd:schema>`

	// 试图穿越根目录读取外层文件的 evil.xsd
	evilXSD := `<?xml version="1.0" encoding="UTF-8"?>
<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="http://example.com/evil">
  <xsd:import namespace="http://example.com/secret" schemaLocation="../../secret.txt"/>
</xsd:schema>`

	if err := os.WriteFile(filepath.Join(sharedDir, "shared.xsd"), []byte(sharedXSD), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "shared_level2.xsd"), []byte(sharedLevel2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "cycleA.xsd"), []byte(cycleA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "cycleB.xsd"), []byte(cycleB), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "evil.xsd"), []byte(evilXSD), 0o644); err != nil {
		t.Fatal(err)
	}

	wsdl := `<?xml version="1.0" encoding="UTF-8"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="http://example.com/main"
  xmlns:s="http://example.com/shared"
  xmlns:ca="http://example.com/cycleA"
  xmlns:ev="http://example.com/evil"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  targetNamespace="http://example.com/main">
  <wsdl:types>
    <xsd:schema targetNamespace="http://example.com/main">
      <xsd:import namespace="http://example.com/shared" schemaLocation="shared/shared.xsd"/>
      <xsd:import namespace="http://example.com/cycleA" schemaLocation="sub/cycleA.xsd"/>
      <xsd:import namespace="http://example.com/evil" schemaLocation="sub/evil.xsd"/>
      <xsd:element name="Req">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="s" type="s:SharedType"/>
            <xsd:element name="l2" type="s:Level2Type"/>
            <xsd:element name="ca" type="ca:TypeCycleA"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="Msg"><wsdl:part name="p" element="tns:Req"/></wsdl:message>
  <wsdl:portType name="PT"><wsdl:operation name="op"><wsdl:input message="tns:Msg"/></wsdl:operation></wsdl:portType>
  <wsdl:binding name="B" type="tns:PT">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <wsdl:operation name="op"><soap:operation soapAction=""/><wsdl:input><soap:body use="literal"/></wsdl:input></wsdl:operation>
  </wsdl:binding>
  <wsdl:service name="S"><wsdl:port name="P" binding="tns:B"><soap:address location="http://127.0.0.1/ws"/></wsdl:port></wsdl:service>
</wsdl:definitions>`

	wsdlPath := filepath.Join(tempDir, "service.wsdl")
	if err := os.WriteFile(wsdlPath, []byte(wsdl), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := SchemaResolverConfig{
		BaseURI:        PathToFileURI(wsdlPath),
		AllowedRootDir: tempDir, // 授权根目录为 tempDir
	}

	p := ParseWSDL(wsdl, cfg)
	if p.ParseError != "" {
		t.Fatalf("ParseError: %s", p.ParseError)
	}

	// 1. 验证多层 include (shared.xsd -> shared_level2.xsd)
	var foundL2 bool
	for _, op := range p.Operations {
		params := op.InputParams
		if len(params) == 1 && len(params[0].Children) > 0 {
			params = params[0].Children
		}
		for _, param := range params {
			if param.Name == "l2" && len(param.Children) > 0 && param.Children[0].Name == "l2" {
				foundL2 = true
			}
		}
	}
	if !foundL2 {
		t.Errorf("多层 include (shared_level2.xsd) 未能成功展开")
	}

	// 2. 验证循环引用 cycleA <-> cycleB：依赖图应安全终止且两者均被加载
	cycleALoaded := false
	cycleBLoaded := false
	traversalBlocked := false

	for _, dep := range p.Dependencies {
		if strings.Contains(dep.URI, "cycleA.xsd") && dep.Status == "loaded" {
			cycleALoaded = true
		}
		if strings.Contains(dep.URI, "cycleB.xsd") && dep.Status == "loaded" {
			cycleBLoaded = true
		}
		if strings.Contains(dep.URI, "secret.txt") {
			if dep.Status == "error" && strings.Contains(dep.Error, "越界") {
				traversalBlocked = true
			}
		}
	}

	if !cycleALoaded || !cycleBLoaded {
		t.Errorf("循环引用 cycleA/cycleB 未能成功加载: cycleA=%v, cycleB=%v", cycleALoaded, cycleBLoaded)
	}
	if !traversalBlocked {
		t.Errorf("越权目录穿越 (../../secret.txt) 未被正确阻断，Dependencies = %+v", p.Dependencies)
	}
}

// T050: 缺文件/错误编码/字节超限/附件歧义冲突。
func TestSchemaResolver_T050_FailuresAndBudgets(t *testing.T) {
	tempDir := t.TempDir()

	// 1. 错误 XML 编码 / 损坏 XML 文件
	corruptXSDPath := filepath.Join(tempDir, "corrupt.xsd")
	if err := os.WriteFile(corruptXSDPath, []byte("NOT_VALID_XML <<<<<"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. 超大文件
	hugeXSDPath := filepath.Join(tempDir, "huge.xsd")
	if err := os.WriteFile(hugeXSDPath, []byte("<schema>"+strings.Repeat(" ", 2000)+"</schema>"), 0o644); err != nil {
		t.Fatal(err)
	}

	wsdl := `<?xml version="1.0" encoding="UTF-8"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="http://example.com/main"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  targetNamespace="http://example.com/main">
  <wsdl:types>
    <xsd:schema targetNamespace="http://example.com/main">
      <xsd:import namespace="http://example.com/missing" schemaLocation="missing.xsd"/>
      <xsd:import namespace="http://example.com/corrupt" schemaLocation="corrupt.xsd"/>
      <xsd:import namespace="http://example.com/huge" schemaLocation="huge.xsd"/>
      <xsd:import namespace="http://example.com/dup" schemaLocation="dup.xsd"/>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="M"><wsdl:part name="p" type="xsd:string"/></wsdl:message>
  <wsdl:portType name="PT"><wsdl:operation name="op"><wsdl:input message="tns:M"/></wsdl:operation></wsdl:portType>
  <wsdl:binding name="B" type="tns:PT"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/></wsdl:binding>
  <wsdl:service name="S"><wsdl:port name="P" binding="tns:B"><soap:address location="http://127.0.0.1/ws"/></wsdl:port></wsdl:service>
</wsdl:definitions>`

	// 设置单文件上限 1KB，触发 huge.xsd 超限
	cfg := SchemaResolverConfig{
		BaseURI:        PathToFileURI(filepath.Join(tempDir, "service.wsdl")),
		AllowedRootDir: tempDir,
		MaxSingleBytes: 1024,
		Attachments: map[string]string{
			"a/dup.xsd": "<schema></schema>",
			"b/dup.xsd": "<schema></schema>",
		},
	}

	p := ParseWSDL(wsdl, cfg)

	var missingUnresolved, corruptError, hugeError, dupConflict bool
	for _, dep := range p.Dependencies {
		if strings.Contains(dep.URI, "missing.xsd") && dep.Status == "unresolved" {
			missingUnresolved = true
		}
		if strings.Contains(dep.URI, "corrupt.xsd") && dep.Status == "error" {
			corruptError = true
		}
		if strings.Contains(dep.URI, "huge.xsd") && dep.Status == "error" && strings.Contains(dep.Error, "超过") {
			hugeError = true
		}
		if strings.Contains(dep.URI, "dup.xsd") && dep.Status == "conflict" {
			dupConflict = true
		}
	}

	if !missingUnresolved {
		t.Errorf("缺失的 XSD 未标记为 unresolved: %+v", p.Dependencies)
	}
	if !corruptError {
		t.Errorf("损坏的 XSD 未标记为 error: %+v", p.Dependencies)
	}
	if !hugeError {
		t.Errorf("超出大小上限的 XSD 未报告 size error: %+v", p.Dependencies)
	}
	if !dupConflict {
		t.Errorf("同名附件未指定目录未报告 conflict 歧义错误: %+v", p.Dependencies)
	}
	if len(p.Warnings) == 0 {
		t.Errorf("存在未加载依赖时未记录任何 warnings")
	}
}

// T051: 远程依赖跨来源重定向剥离认证头，同源重定向保留，内网合法 URL 正常支持。
func TestSchemaResolver_T051_RemoteRedirectAuthAndIntranet(t *testing.T) {
	var targetReceivedAuth string
	var secondTargetReceivedAuth string

	// 目标服务 B（不同 origin）
	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetReceivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="http://example.com/b"/>`))
	}))
	defer serverB.Close()

	// 目标服务 A（同 origin 内部重定向测试）
	var serverA *httptest.Server
	serverA = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-to-b" {
			http.Redirect(w, r, serverB.URL+"/schema.xsd", http.StatusFound)
			return
		}
		if r.URL.Path == "/redirect-internal" {
			http.Redirect(w, r, serverA.URL+"/internal-schema.xsd", http.StatusFound)
			return
		}
		if r.URL.Path == "/internal-schema.xsd" {
			secondTargetReceivedAuth = r.Header.Get("Authorization")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="http://example.com/internal"/>`))
			return
		}
		w.WriteHeader(404)
	}))
	defer serverA.Close()

	// 1. 跨源重定向：从 Server A 重定向到 Server B，Authorization 应被剥离
	cfgCross := SchemaResolverConfig{
		BaseURI:     serverA.URL + "/service.wsdl",
		AuthHeaders: map[string]string{"Authorization": "Bearer secret-token-123"},
	}
	resCross := NewSchemaResolver(cfgCross)
	idx := newSchemaIndex()
	deps, warns := resCross.ResolveGraph([]xsdImport{
		{SchemaLocation: serverA.URL + "/redirect-to-b"},
	}, idx)

	if len(deps) == 0 || deps[0].Status != "loaded" {
		t.Fatalf("跨源重定向加载失败: deps=%+v, warns=%v", deps, warns)
	}
	if targetReceivedAuth != "" {
		t.Errorf("跨源重定向泄露了 Authorization 头: %q", targetReceivedAuth)
	}

	// 2. 同源重定向：同一 Server A 内部重定向，Authorization 应该保留
	cfgSame := SchemaResolverConfig{
		BaseURI:     serverA.URL + "/service.wsdl",
		AuthHeaders: map[string]string{"Authorization": "Bearer secret-token-456"},
	}
	resSame := NewSchemaResolver(cfgSame)
	depsSame, warnsSame := resSame.ResolveGraph([]xsdImport{
		{SchemaLocation: serverA.URL + "/redirect-internal"},
	}, idx)

	if len(depsSame) == 0 || depsSame[0].Status != "loaded" {
		t.Fatalf("同源重定向加载失败: deps=%+v, warns=%v", depsSame, warnsSame)
	}
	if secondTargetReceivedAuth != "Bearer secret-token-456" {
		t.Errorf("同源重定向未保留 Authorization 头，got %q", secondTargetReceivedAuth)
	}

	// 3. WSDL-01 跨源直接 import（无 302 重定向）：直接引用 Server B 的 schema，Authorization 必须剥离
	targetReceivedAuth = ""
	cfgDirectCross := SchemaResolverConfig{
		BaseURI:     serverA.URL + "/service.wsdl",
		AuthHeaders: map[string]string{"Authorization": "Bearer secret-token-direct-789"},
	}
	resDirectCross := NewSchemaResolver(cfgDirectCross)
	depsDirectCross, warnsDirectCross := resDirectCross.ResolveGraph([]xsdImport{
		{SchemaLocation: serverB.URL + "/direct-schema.xsd"},
	}, idx)

	if len(depsDirectCross) == 0 || depsDirectCross[0].Status != "loaded" {
		t.Fatalf("跨源直接引用加载失败: deps=%+v, warns=%v", depsDirectCross, warnsDirectCross)
	}
	if targetReceivedAuth != "" {
		t.Errorf("跨源直接引用泄露了 Authorization 头: %q", targetReceivedAuth)
	}
}
