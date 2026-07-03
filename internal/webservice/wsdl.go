package webservice

// WSDL 解析（SOAP 1.1 优先，尽量兼容 1.2）。
//
// 实现说明：
//   - 用 encoding/xml 的"按 local name 匹配"特性，不依赖具体 namespace 前缀，
//     这样 wsdl:/xsd:/soap:/soap12: 各种前缀写法都能命中。
//   - 文档/字面量（document/literal）是 Java/WebSphere/XFire 的主流模式，重点支持；
//     rpc/literal 尽量兼容但不保证参数结构完整。
//   - 外部 XSD import/include 当前不联网拉取（内网工具不依赖公网），
//     缺失的类型会在 Warnings 里提示，对应 operation 的 InputRaw 保留原始片段，
//     不影响其它能解析的 operation。

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

var defaultNSRe = regexp.MustCompile(`\sxmlns\s*=\s*("[^"]*"|'[^']*')`)

// ---------- 解析用的中间结构 ----------

type wsdlDefinitions struct {
	XMLName   xml.Name       `xml:"definitions"`
	TargetNS  string         `xml:"targetNamespace,attr"`
	Types     *wsdlTypes     `xml:"types"`
	Messages  []wsdlMessage  `xml:"message"`
	PortTypes []wsdlPortType `xml:"portType"`
	Bindings  []wsdlBinding  `xml:"binding"`
	Services  []wsdlService  `xml:"service"`
}

type wsdlTypes struct {
	Schemas []xsdSchema `xml:"schema"`
	// 其它非 schema 内容（外部 import 失败时降级）原样忽略。
}

type xsdSchema struct {
	XMLName      xml.Name         `xml:"schema"`
	TargetNS     string           `xml:"targetNamespace,attr"`
	Elements     []xsdElement     `xml:"element"`
	ComplexTypes []xsdComplexType `xml:"complexType"`
	SimpleTypes  []xsdSimpleType  `xml:"simpleType"`
	Imports      []xsdImport      `xml:"import"`
	Includes     []xsdImport      `xml:"include"`
}

type xsdImport struct {
	Namespace      string `xml:"namespace,attr"`
	SchemaLocation string `xml:"schemaLocation,attr"`
}

type xsdElement struct {
	Name        string          `xml:"name,attr"`
	Type        string          `xml:"type,attr"`
	Ref         string          `xml:"ref,attr"`
	MinOccurs   string          `xml:"minOccurs,attr"`
	MaxOccurs   string          `xml:"maxOccurs,attr"`
	Nillable    bool            `xml:"nillable,attr"`
	ComplexType *xsdComplexType `xml:"complexType"`
	SimpleType  *xsdSimpleType  `xml:"simpleType"`
	Sequence    *xsdSequence    `xml:"sequence"`
}

type xsdComplexType struct {
	Name         string       `xml:"name,attr"`
	Sequence     *xsdSequence `xml:"sequence"`
	Choice       *xsdSequence `xml:"choice"`
	All          *xsdSequence `xml:"all"`
	ComplexContent *xsdComplexContent `xml:"complexContent"`
	SimpleContent  *xsdSimpleContent `xml:"simpleContent"`
	RawInner     string `xml:",innerxml"`
}

type xsdComplexContent struct {
	Extension *xsdExtension `xml:"extension"`
	Restriction *xsdRestriction `xml:"restriction"`
}

type xsdSimpleContent struct {
	Extension *xsdExtension `xml:"extension"`
	Restriction *xsdRestriction `xml:"restriction"`
}

type xsdExtension struct {
	Base     string       `xml:"base,attr"`
	Sequence *xsdSequence `xml:"sequence"`
	Choice   *xsdSequence `xml:"choice"`
	All      *xsdSequence `xml:"all"`
}

type xsdSimpleType struct {
	Name        string          `xml:"name,attr"`
	Restriction *xsdRestriction `xml:"restriction"`
}

type xsdRestriction struct {
	Base  string           `xml:"base,attr"`
	Enums []xsdEnumeration `xml:"enumeration"`
}

type xsdEnumeration struct {
	Value string `xml:"value,attr"`
}

// xsdSequence 复用于 sequence/choice/all，因为它们的子元素结构一致。
type xsdSequence struct {
	Elements []xsdElement `xml:"element"`
	// 嵌套 sequence/choice（递归复杂类型）当前仅展开一层 element，足够覆盖大多数业务报文。
	Sequences []xsdSequence `xml:"sequence"`
	Choices   []xsdSequence `xml:"choice"`
}

type wsdlMessage struct {
	Name  string            `xml:"name,attr"`
	Parts []wsdlMessagePart `xml:"part"`
}

type wsdlMessagePart struct {
	Name    string `xml:"name,attr"`
	Element string `xml:"element,attr"` // document/literal 用 element
	Type    string `xml:"type,attr"`    // rpc/literal 用 type
}

type wsdlPortType struct {
	Name       string            `xml:"name,attr"`
	Operations []wsdlPTOperation `xml:"operation"`
}

type wsdlPTOperation struct {
	Name   string        `xml:"name,attr"`
	Input  wsdlPTMessage `xml:"input"`
	Output wsdlPTMessage `xml:"output"`
}

type wsdlPTMessage struct {
	Message string `xml:"message,attr"`
	Name    string `xml:"name,attr"`
}

type wsdlBinding struct {
	Name       string           `xml:"name,attr"`
	Type       string           `xml:"type,attr"`
	Style      string           `xml:"style,attr"` // 可能没有，operation 级也有
	Operations []wsdlBOperation `xml:"operation"`
	// soap:binding（local name "binding"）。soap vs soap12 难以从结构区分（local name 相同），
	// 版本判定统一走项目级 raw 字符串检测；这里只取 transport/style。
	SoapBinding *soapBindingAttr `xml:"binding"`
}

type soapBindingAttr struct {
	Transport string `xml:"transport,attr"`
	Style     string `xml:"style,attr"`
}

type wsdlBOperation struct {
	Name          string             `xml:"name,attr"`
	SoapOperation *soapOperationAttr `xml:"operation"` // soap:operation（local name "operation"）
	Style         string             `xml:"style,attr"`
}

type soapOperationAttr struct {
	SOAPAction string `xml:"soapAction,attr"`
	Style      string `xml:"style,attr"`
}

type wsdlService struct {
	Name  string     `xml:"name,attr"`
	Ports []wsdlPort `xml:"port"`
}

type wsdlPort struct {
	Name        string           `xml:"name,attr"`
	Binding     string           `xml:"binding,attr"`
	SoapAddress *soapAddressAttr `xml:"address"` // soap:address（local name "address"）
}

type soapAddressAttr struct {
	Location string `xml:"location,attr"`
}

// ---------- QName 解析 ----------

// localName 取 QName 的 local 部分（"tns:Foo" → "Foo"）。
func localName(qname string) string {
	if i := strings.Index(qname, ":"); i >= 0 {
		return qname[i+1:]
	}
	return qname
}

// ParseOption 是 ParseWSDL 的可选参数类型。
// 支持 string（作为 sourceURL）和 map[string]string（作为 attachments）。
type ParseOption any

// ---------- 主解析入口 ----------

// ParseWSDL 解析 WSDL 文本，返回一个 WSDLProject（不含 ID/时间戳，由调用方补）。
// opts 可选，目前支持：
//   - sourceURL string：用于解析相对路径的 XSD schemaLocation
//   - attachments map[string]string：文件名 → 文件内容，上传模式下 XSD 直接从这里取
//
// 解析失败不返回 error 中断，而是把错误填到 ParseError + Warnings，
// 并尽量返回已解析到的 operation 列表（降级原则）。
func ParseWSDL(raw string, opts ...ParseOption) *WSDLProject {
	p := &WSDLProject{
		Version:  DataVersion,
		TargetNS: "",
		RawWSDL:  raw,
	}
	// 解析可选参数
	var sourceURL string
	var attachments map[string]string
	for _, o := range opts {
		switch v := o.(type) {
		case string:
			sourceURL = v
		case map[string]string:
			attachments = v
		}
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		p.ParseError = "WSDL 内容为空"
		return p
	}

	// 检测是否含 WSDL 根元素（按 local name definitions）。
	if !strings.Contains(trimmed, "<") || !containsLocalName(trimmed, "definitions") {
		p.ParseError = "未找到 wsdl:definitions 根元素（不是合法的 WSDL）"
		return p
	}

	var defs wsdlDefinitions
	dec := xml.NewDecoder(strings.NewReader(raw))
	dec.Strict = false
	if err := dec.Decode(&defs); err != nil {
		// 解析失败时仍尝试给一个可读错误
		p.ParseError = fmt.Sprintf("WSDL XML 解析失败: %v", err)
		return p
	}
	p.TargetNS = defs.TargetNS

	// SOAP 版本检测：优先看 soap12 namespace 是否出现，再看 soap: 出现。
	soap12 := strings.Contains(raw, "http://schemas.xmlsoap.org/wsdl/soap12/") ||
		strings.Contains(raw, "http://www.w3.org/2003/05/soap-bindings/")
	soap11 := strings.Contains(raw, "http://schemas.xmlsoap.org/wsdl/soap/") ||
		strings.Contains(raw, "http://schemas.xmlsoap.org/soap/envelope/")
	if soap12 && !soap11 {
		p.SOAPVersion = "1.2"
	} else {
		p.SOAPVersion = "1.1"
	}

	// 加载外部 XSD import/include
	var imports []xsdImport
	if defs.Types != nil {
		for _, sch := range defs.Types.Schemas {
			imports = append(imports, sch.Imports...)
			imports = append(imports, sch.Includes...)
		}
	}
	// 构建元素/类型查找表（先加载外部 XSD，再合并）。
	elements := map[string]xsdElement{}         // top-level element by name
	complexTypes := map[string]xsdComplexType{} // top-level complexType by name
	if defs.Types != nil {
		for _, sch := range defs.Types.Schemas {
			for _, el := range sch.Elements {
				if el.Name != "" {
					elements[el.Name] = el
				}
			}
			for _, ct := range sch.ComplexTypes {
				if ct.Name != "" {
					complexTypes[ct.Name] = ct
				}
			}
		}
	}
	// 加载外部 XSD（递归处理 XSD 内部的 import）
	hasSource := sourceURL != "" || len(attachments) > 0
	if hasSource {
		loaded := map[string]bool{} // 防止循环引用
		queue := imports
		for len(queue) > 0 {
			imp := queue[0]
			queue = queue[1:]
			if imp.SchemaLocation == "" || loaded[imp.SchemaLocation] {
				continue
			}
			loaded[imp.SchemaLocation] = true
			// 优先从 attachments 里按文件名取（上传模式），再从 URL 下载（URL 导入模式）
			var xsdRaw string
			var err error
			basename := path.Base(imp.SchemaLocation)
			if v, ok := attachments[basename]; ok {
				xsdRaw = v
			} else if v, ok = attachments[imp.SchemaLocation]; ok {
				xsdRaw = v
			} else if sourceURL != "" {
				xsdRaw, err = fetchExternalSchema(sourceURL, imp.SchemaLocation)
			} else {
				p.Warnings = append(p.Warnings,
					fmt.Sprintf("外部 XSD 未找到（namespace=%s, location=%s），附件中无此文件，相关类型参数可能无法展开",
						imp.Namespace, imp.SchemaLocation))
				continue
			}
			if err != nil {
				p.Warnings = append(p.Warnings,
					fmt.Sprintf("外部 XSD 加载失败（namespace=%s, location=%s）：%v，相关类型参数可能无法展开",
						imp.Namespace, imp.SchemaLocation, err))
				continue
			}
			extSchema, err := parseExternalXSD(xsdRaw)
			if err != nil {
				p.Warnings = append(p.Warnings,
					fmt.Sprintf("外部 XSD 解析失败（location=%s）：%v", imp.SchemaLocation, err))
				continue
			}
			for _, el := range extSchema.Elements {
				if el.Name != "" {
					elements[el.Name] = el
				}
			}
			for _, ct := range extSchema.ComplexTypes {
				if ct.Name != "" {
					complexTypes[ct.Name] = ct
				}
			}
			_ = extSchema.SimpleTypes
			// 递归加载 XSD 内部的 import/include
			for _, nestedImp := range extSchema.Imports {
				if nestedImp.SchemaLocation != "" {
					queue = append(queue, nestedImp)
				}
			}
			for _, nestedInc := range extSchema.Includes {
				if nestedInc.SchemaLocation != "" {
					queue = append(queue, nestedInc)
				}
			}
		}
	} else {
		// 没有 sourceURL 也没有 attachments，无法解析外部 XSD，仅记录 warning
		for _, imp := range imports {
			if imp.SchemaLocation != "" {
				p.Warnings = append(p.Warnings,
					fmt.Sprintf("外部 XSD 未加载（namespace=%s, location=%s），无 sourceURL 也无附件，相关类型参数可能无法展开",
						imp.Namespace, imp.SchemaLocation))
			}
		}
	}

	// message 查找表（按 local name）
	msgs := map[string]wsdlMessage{}
	for _, m := range defs.Messages {
		msgs[m.Name] = m
	}

	// binding 查找表（按 local name，绑定 portType 的 QName）
	bindings := map[string]wsdlBinding{}
	for _, b := range defs.Bindings {
		bindings[b.Name] = b
	}

	// portType 查找表
	portTypes := map[string]wsdlPortType{}
	for _, pt := range defs.PortTypes {
		portTypes[pt.Name] = pt
	}

	// 1) 展开 services → ports
	for _, sv := range defs.Services {
		svc := Service{Name: sv.Name}
		for _, prt := range sv.Ports {
			port := Port{
				Name:    prt.Name,
				Binding: localName(prt.Binding),
			}
			loc := ""
			if prt.SoapAddress != nil && prt.SoapAddress.Location != "" {
				loc = prt.SoapAddress.Location
			}
			port.Endpoint = loc
			// 端口级 SOAP 版本：根据绑定是 soap 还是 soap12 难以从结构区分（local name 相同），
			// 用项目级版本作为兜底；若 WSDL 同时含 1.1/1.2 默认按 1.1。
			port.SOAPVersion = p.SOAPVersion
			svc.Ports = append(svc.Ports, port)
		}
		p.Services = append(p.Services, svc)
	}

	// 2) 遍历 bindings → operations，关联 portType 的 input/output message 与 soapAction
	for _, b := range defs.Bindings {
		ptName := localName(b.Type)
		pt, ok := portTypes[ptName]
		if !ok {
			continue
		}
		// 该 binding 对应的 endpoint：从 services 里找 binding local name == b.Name 的 port
		endpoint := ""
		bindingSOAPVer := p.SOAPVersion
		for _, sv := range p.Services {
			for _, prt := range sv.Ports {
				if prt.Binding == b.Name && endpoint == "" {
					endpoint = prt.Endpoint
				}
			}
		}
		// binding 级 soapAction 风格
		bindingStyle := b.Style
		for _, bop := range b.Operations {
			// 找 portType 里同名 operation
			var ptOp *wsdlPTOperation
			for i := range pt.Operations {
				if pt.Operations[i].Name == bop.Name {
					ptOp = &pt.Operations[i]
					break
				}
			}
			if ptOp == nil {
				continue
			}
			op := Operation{
				Name:        bop.Name,
				Endpoint:    endpoint,
				SOAPVersion: bindingSOAPVer,
				Style:       bindingStyle,
			}
			if bop.SoapOperation != nil && bop.SoapOperation.SOAPAction != "" {
				op.SOAPAction = bop.SoapOperation.SOAPAction
			}
			// operation namespace：优先用 input message 对应的 element 所在 schema targetNamespace，
			// 取不到则用 definitions targetNamespace。
			op.Namespace = p.TargetNS

			// 解析 input
			if inMsg := resolveMessage(ptOp.Input.Message, msgs); inMsg != nil {
				for _, part := range inMsg.Parts {
					if part.Element != "" {
						op.InputName = localName(part.Element)
						el, ok := elements[op.InputName]
						if ok {
							op.InputParams = buildParams(el, complexTypes, 0)
							op.Namespace = lookupElementNS(el, defs.Types, p.TargetNS)
							// document/literal wrapped：input 元素名 == operation 名且含子元素时，
							// 把外层包装剥掉，直接用其子元素作为参数（避免生成双层嵌套）。
							op.InputParams = unwrapWrapper(op.InputParams, op.Name, op.InputName)
						}
						if len(op.InputParams) == 0 {
							op.InputRaw = elementRawFragment(el)
						}
					} else if part.Type != "" {
						// rpc/literal：type 直接引用 complexType
						ctName := localName(part.Type)
						if ct, ok := complexTypes[ctName]; ok {
							op.InputParams = paramsFromComplexType(ct, complexTypes, 0)
							op.InputName = ctName
						}
					}
				}
			}
			// 解析 output
			if outMsg := resolveMessage(ptOp.Output.Message, msgs); outMsg != nil {
				for _, part := range outMsg.Parts {
					if part.Element != "" {
						op.OutputName = localName(part.Element)
						el, ok := elements[op.OutputName]
						if ok {
							op.OutputParams = buildParams(el, complexTypes, 0)
							op.OutputParams = unwrapWrapper(op.OutputParams, op.Name+"Response", op.OutputName)
						}
						if len(op.OutputParams) == 0 {
							op.OutputRaw = elementRawFragment(el)
						}
					} else if part.Type != "" {
						ctName := localName(part.Type)
						if ct, ok := complexTypes[ctName]; ok {
							op.OutputParams = paramsFromComplexType(ct, complexTypes, 0)
							op.OutputName = ctName
						}
					}
				}
			}
			p.Operations = append(p.Operations, op)
		}
	}

	if len(p.Operations) == 0 && p.ParseError == "" {
		p.Warnings = append(p.Warnings, "未解析到任何 operation（可能是 rpc/encoded 或外部 XSD 未导入）")
	}
	return p
}

// containsLocalName 粗略判断 XML 文本里是否出现某 local name 的开始标签。
func containsLocalName(raw, name string) bool {
	// 匹配 <prefix:name 或 <name（name 后跟空格/>>
	needle1 := "<" + name + " "
	needle2 := "<" + name + ">"
	needle3 := "<" + name + "/"
	needle4 := ":" + name
	return strings.Contains(raw, needle1) || strings.Contains(raw, needle2) ||
		strings.Contains(raw, needle3) || strings.Contains(raw, needle4)
}

func resolveMessage(qname string, msgs map[string]wsdlMessage) *wsdlMessage {
	if qname == "" {
		return nil
	}
	ln := localName(qname)
	if m, ok := msgs[ln]; ok {
		return &m
	}
	return nil
}

// buildParams 从一个 xsd:element 出发构建参数树。
// element 可能有：type 引用（可能指向 complexType）、inline complexType、inline sequence。
func buildParams(el xsdElement, complexTypes map[string]xsdComplexType, depth int) []Param {
	if depth > 6 {
		return nil // 防止递归爆栈
	}
	// type 引用了 complexType（优先检查，因为外部 XSD 的类型都在这里）
	if el.Type != "" {
		ctName := localName(el.Type)
		if ct, ok := complexTypes[ctName]; ok {
			p := Param{Name: el.Name, Type: el.Type, MinOccurs: el.MinOccurs, MaxOccurs: el.MaxOccurs, Nillable: el.Nillable}
			p.Children = paramsFromComplexType(ct, complexTypes, depth)
			return []Param{p}
		}
	}
	// inline complexType
	if el.ComplexType != nil {
		p := Param{Name: el.Name, Type: el.ComplexType.Name, MinOccurs: el.MinOccurs, MaxOccurs: el.MaxOccurs, Nillable: el.Nillable}
		p.Children = paramsFromComplexType(*el.ComplexType, complexTypes, depth)
		return []Param{p}
	}
	// 直接 inline sequence（无 complexType 包装，少见）
	if el.Sequence != nil {
		p := Param{Name: el.Name, MinOccurs: el.MinOccurs, MaxOccurs: el.MaxOccurs, Nillable: el.Nillable}
		p.Children = paramsFromSequence(*el.Sequence, complexTypes, depth)
		return []Param{p}
	}
	// 简单类型 element：<element name="x" type="xsd:string"/>（前面没匹配到的才是简单类型）
	if el.Type != "" {
		return []Param{{
			Name:      el.Name,
			Type:      el.Type,
			MinOccurs: el.MinOccurs,
			MaxOccurs: el.MaxOccurs,
			Nillable:  el.Nillable,
		}}
	}
	// 啥都没有：占位叶子
	if el.Name != "" {
		return []Param{{Name: el.Name, MinOccurs: el.MinOccurs, MaxOccurs: el.MaxOccurs, Nillable: el.Nillable}}
	}
	return nil
}

func paramsFromComplexType(ct xsdComplexType, complexTypes map[string]xsdComplexType, depth int) []Param {
	if depth > 6 {
		return nil
	}
	// 优先处理 complexContent/extension：先展开父类型，再追加当前字段
	if ct.ComplexContent != nil && ct.ComplexContent.Extension != nil {
		ext := ct.ComplexContent.Extension
		var out []Param
		// 展开 base 类型的字段
		baseName := localName(ext.Base)
		if baseCT, ok := complexTypes[baseName]; ok {
			out = paramsFromComplexType(baseCT, complexTypes, depth+1)
		}
		// 追加 extension 里的字段
		if ext.Sequence != nil {
			out = append(out, paramsFromSequence(*ext.Sequence, complexTypes, depth+1)...)
		}
		if ext.Choice != nil {
			out = append(out, paramsFromSequence(*ext.Choice, complexTypes, depth+1)...)
		}
		if ext.All != nil {
			out = append(out, paramsFromSequence(*ext.All, complexTypes, depth+1)...)
		}
		return out
	}
	// 普通 complexType
	if ct.Sequence != nil {
		return paramsFromSequence(*ct.Sequence, complexTypes, depth)
	}
	if ct.Choice != nil {
		return paramsFromSequence(*ct.Choice, complexTypes, depth)
	}
	if ct.All != nil {
		return paramsFromSequence(*ct.All, complexTypes, depth)
	}
	return nil
}

func paramsFromSequence(seq xsdSequence, complexTypes map[string]xsdComplexType, depth int) []Param {
	if depth > 6 {
		return nil
	}
	var out []Param
	for _, child := range seq.Elements {
		out = append(out, buildParams(child, complexTypes, depth+1)...)
	}
	// 嵌套 sequence/choice
	for _, ns := range seq.Sequences {
		out = append(out, paramsFromSequence(ns, complexTypes, depth+1)...)
	}
	for _, nc := range seq.Choices {
		out = append(out, paramsFromSequence(nc, complexTypes, depth+1)...)
	}
	return out
}

// lookupElementNS 找 element 所在 schema 的 targetNamespace。
//
// TODO: 当前只按 element.Name 匹配，多 schema 含同名 element 时会拿到
// 第一个匹配的 namespace（可能误判）。修对的话需要把查找表从
// map[name]element 改成 map[ns+name]element，并在 buildParams 链路里
// 透传 namespace 上下文 —— 改动面偏大，且多 schema 同名 element 在
// 实际内网 WSDL 中极少见，暂留作后续优化。
func lookupElementNS(el xsdElement, types *wsdlTypes, fallback string) string {
	if types == nil {
		return fallback
	}
	for _, sch := range types.Schemas {
		for _, e := range sch.Elements {
			if e.Name == el.Name {
				if sch.TargetNS != "" {
					return sch.TargetNS
				}
			}
		}
	}
	return fallback
}

// unwrapWrapper 处理 document/literal wrapped 模式：若 params 只有一个元素、
// 其 Name 等于 wrapperName（operation 名或响应名）且包含子元素，则返回其子元素。
// 注意：只比较 wrapperName（operation 名），不比较 elementName。
// document/literal wrapped 的特点是 operation name == input element name，
// 此时生成的报文应该是 <op><param>...</param></op> 而非 <op><op><param>...</param></op></op>。
// document/literal bare 模式下 operation name != input element name，不应 unwrap。
func unwrapWrapper(params []Param, wrapperName, elementName string) []Param {
	if len(params) != 1 {
		return params
	}
	only := params[0]
	if len(only.Children) == 0 {
		return params
	}
	if only.Name == wrapperName {
		return only.Children
	}
	return params
}

// elementRawFragment 返回 element 的原始内部 XML 片段用于回显。
func elementRawFragment(el xsdElement) string {
	if el.ComplexType != nil && el.ComplexType.RawInner != "" {
		return strings.TrimSpace(el.ComplexType.RawInner)
	}
	// 用类型名兜底
	if el.Type != "" {
		return "type: " + el.Type
	}
	if el.Ref != "" {
		return "ref: " + el.Ref
	}
	return ""
}

// ValidateWSDLText 做一次轻量 well-formed 校验，返回错误用于 import-url 时的预检。
func ValidateWSDLText(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return errors.New("内容为空")
	}
	dec := xml.NewDecoder(strings.NewReader(raw))
	dec.Strict = true
	for {
		_, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// fetchExternalSchema 从 sourceURL 解析相对路径的 schemaLocation，返回 XSD 原始文本。
// 支持 http/https URL 和相对路径（如 "SuLianLoanHengLiServiceCore.xsd"）。
// 30s 超时（XSD 通常很小，超时主要是防挂起）。
func fetchExternalSchema(sourceURL, schemaLocation string) (string, error) {
	u, err := url.Parse(sourceURL)
	if err != nil {
		return "", fmt.Errorf("解析 sourceURL 失败: %w", err)
	}
	// 拼接相对路径
	schemaURL := u.ResolveReference(&url.URL{Path: path.Join(path.Dir(u.Path), schemaLocation)})

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, schemaURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("构造 XSD 请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "kairo-wsdl/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载 XSD 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("XSD 返回状态 %d", resp.StatusCode)
	}
	// 4MB 上限（XSD 一般很小）
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return "", fmt.Errorf("读取 XSD 内容失败: %w", err)
	}
	return string(data), nil
}

// parseExternalXSD 解析 XSD 文本，提取 elements/complexTypes/simpleTypes。
// XSD 文件可能有默认 namespace（如 xmlns="http://www.w3.org/2001/XMLSchema"），
// 这里先去掉 namespace 声明，让 encoding/xml 能按 local name 匹配。
func parseExternalXSD(raw string) (*xsdSchema, error) {
	raw = stripDefaultNamespace(raw)
	var schema xsdSchema
	dec := xml.NewDecoder(strings.NewReader(raw))
	dec.Strict = false
	if err := dec.Decode(&schema); err != nil {
		return nil, fmt.Errorf("XSD XML 解析失败: %w", err)
	}
	return &schema, nil
}

// stripDefaultNSRe 匹配 xmlns="..." 或 xmlns = "..."（带空格），保留 xmlns:xxx="..."。
// 提到包级别，避免 stripDefaultNamespace 每次调用重新编译。
var stripDefaultNSRe = regexp.MustCompile(`\sxmlns\s*=\s*("[^"]*"|'[^']*')`)

// stripDefaultNamespace 去掉 XML 里的默认 namespace 声明（xmlns="..."），
// 但保留带前缀的 namespace（xmlns:xxx="..."）。
func stripDefaultNamespace(raw string) string {
	return stripDefaultNSRe.ReplaceAllString(raw, "")
}
