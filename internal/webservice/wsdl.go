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
	"crypto/tls"
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

// stripDefaultNSRe 匹配 xmlns="..." 或 xmlns = "..."（带空格），保留 xmlns:xxx="..."。
// 提到包级别，避免 stripDefaultNamespace 每次调用重新编译。
var stripDefaultNSRe = regexp.MustCompile(`\sxmlns\s*=\s*("[^"]*"|'[^']*')`)

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
	Name           string             `xml:"name,attr"`
	Sequence       *xsdSequence       `xml:"sequence"`
	Choice         *xsdSequence       `xml:"choice"`
	All            *xsdSequence       `xml:"all"`
	ComplexContent *xsdComplexContent `xml:"complexContent"`
	SimpleContent  *xsdSimpleContent  `xml:"simpleContent"`
	RawInner       string             `xml:",innerxml"`
}

type xsdComplexContent struct {
	Extension   *xsdExtension   `xml:"extension"`
	Restriction *xsdRestriction `xml:"restriction"`
}

type xsdSimpleContent struct {
	Extension   *xsdExtension   `xml:"extension"`
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
	Input         wsdlBOperationMsg  `xml:"input"`
	Output        wsdlBOperationMsg  `xml:"output"`
}

type wsdlBOperationMsg struct {
	SoapBody *soapBodyAttr `xml:"body"` // soap:body（local name "body"）
}

type soapBodyAttr struct {
	Use       string `xml:"use,attr"`
	Namespace string `xml:"namespace,attr"`
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

// nsMap 保存 XML 命名空间声明（prefix → namespace），"" 前缀代表默认命名空间 xmlns="..."。
type nsMap map[string]string

// qnameKey 是 ns+name 复合键，用于区分不同 schema 里的同名 element/complexType。
type qnameKey struct {
	ns   string
	name string
}

// nsContext 提供 QName 解析上下文：前缀表 + 当前 schema 的 targetNamespace。
// 无前缀的 QName（如 type="Foo"）按 XSD 惯例解析到当前 schema 的 targetNamespace。
type nsContext struct {
	prefixes nsMap
	self     string
}

// resolve 把 QName 拆成 (namespace, localName)。
// 带前缀时查前缀表（前缀未声明得到空 namespace，走唯一名兜底）；
// 无前缀时用当前 schema 的 targetNamespace。
func (c nsContext) resolve(qname string) (ns, name string) {
	if i := strings.Index(qname, ":"); i >= 0 {
		return c.prefixes[qname[:i]], qname[i+1:]
	}
	return c.self, qname
}

// collectNSContexts 遍历 XML token 流，返回根元素的 namespace 声明，
// 以及按文档顺序每个 <schema> 元素处的 namespace 上下文（父级声明合并自身声明）。
// struct 解码会丢弃 QName 前缀信息，这里补回来用于 ns+name 解析。
func collectNSContexts(raw string) (root nsMap, schemas []nsMap) {
	root = nsMap{}
	dec := xml.NewDecoder(strings.NewReader(raw))
	dec.Strict = false
	stack := []nsMap{}
	for {
		tok, err := dec.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			own := nsMap{}
			for _, a := range t.Attr {
				switch {
				case a.Name.Space == "xmlns":
					own[a.Name.Local] = a.Value
				case a.Name.Local == "xmlns":
					own[""] = a.Value
				}
			}
			merged := own
			if len(stack) > 0 {
				merged = mergeNS(stack[len(stack)-1], own)
			}
			stack = append(stack, merged)
			if len(stack) == 1 {
				root = merged
			}
			if t.Name.Local == "schema" {
				schemas = append(schemas, merged)
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
}

// mergeNS 返回 base 与 own 合并后的新 map（内层声明覆盖外层同名前缀）。
func mergeNS(base, own nsMap) nsMap {
	out := nsMap{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range own {
		out[k] = v
	}
	return out
}

// schemaIndex 是 element/complexType 的 ns+name 索引。
// 每个条目同时记录其所在 schema 的 namespace 上下文，
// 供递归展开时正确解析 type/base 等 QName 引用。
type schemaIndex struct {
	elements map[qnameKey]xsdElement
	types    map[qnameKey]xsdComplexType
	elemCtx  map[qnameKey]nsContext
	typeCtx  map[qnameKey]nsContext
	// 全局唯一的 local name → key，作为 ns 解析失败（前缀未声明等）时的兜底。
	loneElems map[string]qnameKey
	loneTypes map[string]qnameKey
	elemCount map[string]int
	typeCount map[string]int
}

func newSchemaIndex() *schemaIndex {
	return &schemaIndex{
		elements:  map[qnameKey]xsdElement{},
		types:     map[qnameKey]xsdComplexType{},
		elemCtx:   map[qnameKey]nsContext{},
		typeCtx:   map[qnameKey]nsContext{},
		loneElems: map[string]qnameKey{},
		loneTypes: map[string]qnameKey{},
		elemCount: map[string]int{},
		typeCount: map[string]int{},
	}
}

// addSchema 注册一个 schema 的顶层 element/complexType（ns = schema 的 targetNamespace）。
// 同名不同 ns 的条目互不覆盖；同 ns 同名重复出现时后者为准。
func (idx *schemaIndex) addSchema(sch xsdSchema, ctx nsContext) {
	for _, el := range sch.Elements {
		if el.Name == "" {
			continue
		}
		key := qnameKey{sch.TargetNS, el.Name}
		if _, dup := idx.elements[key]; !dup {
			idx.addLone(idx.loneElems, idx.elemCount, el.Name, key)
		}
		idx.elements[key] = el
		idx.elemCtx[key] = ctx
	}
	for _, ct := range sch.ComplexTypes {
		if ct.Name == "" {
			continue
		}
		key := qnameKey{sch.TargetNS, ct.Name}
		if _, dup := idx.types[key]; !dup {
			idx.addLone(idx.loneTypes, idx.typeCount, ct.Name, key)
		}
		idx.types[key] = ct
		idx.typeCtx[key] = ctx
	}
}

// addLone 维护"全局唯一名"兜底表：同名出现两次及以上就从表里移除。
func (idx *schemaIndex) addLone(m map[string]qnameKey, counts map[string]int, name string, key qnameKey) {
	switch counts[name] {
	case 0:
		m[name] = key
	default:
		delete(m, name)
	}
	counts[name]++
}

// findElement 按 ns+name 查 element；ns 查不到时退回全局唯一名匹配。
// 返回 element、其所在 schema 的 nsContext、以及实际命中的 namespace（可能为空）。
func (idx *schemaIndex) findElement(ns, name string) (xsdElement, nsContext, string, bool) {
	if ns != "" {
		key := qnameKey{ns, name}
		if el, ok := idx.elements[key]; ok {
			return el, idx.elemCtx[key], ns, true
		}
	}
	if key, ok := idx.loneElems[name]; ok {
		return idx.elements[key], idx.elemCtx[key], key.ns, true
	}
	return xsdElement{}, nsContext{}, "", false
}

// findComplexType 同 findElement，用于 complexType。
func (idx *schemaIndex) findComplexType(ns, name string) (xsdComplexType, nsContext, bool) {
	if ns != "" {
		key := qnameKey{ns, name}
		if ct, ok := idx.types[key]; ok {
			return ct, idx.typeCtx[key], true
		}
	}
	if key, ok := idx.loneTypes[name]; ok {
		return idx.types[key], idx.typeCtx[key], true
	}
	return xsdComplexType{}, nsContext{}, false
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
	// 上传接口收到的 content 已经是 Unicode 字符串，但 XML declaration 可能
	// 仍写着 GBK/UTF-16。进入 encoding/xml 前统一声明为 UTF-8。
	raw = normalizeXMLDeclarationUTF8(strings.TrimPrefix(raw, "\uFEFF"))
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

	// SOAP 版本检测：项目级只作兜底；具体 port/binding 再按 namespace 精确判断，
	// 兼容同一份 WSDL 同时暴露 SOAP 1.1 与 SOAP 1.2。
	soap12 := strings.Contains(raw, "http://schemas.xmlsoap.org/wsdl/soap12/") ||
		strings.Contains(raw, "http://www.w3.org/2003/05/soap-bindings/")
	soap11 := strings.Contains(raw, "http://schemas.xmlsoap.org/wsdl/soap/") ||
		strings.Contains(raw, "http://schemas.xmlsoap.org/soap/envelope/")
	if soap12 && !soap11 {
		p.SOAPVersion = "1.2"
	} else {
		p.SOAPVersion = "1.1"
	}
	bindingVersions := collectSOAPBindingVersions(raw)

	// 加载外部 XSD import/include
	var imports []xsdImport
	if defs.Types != nil {
		for _, sch := range defs.Types.Schemas {
			imports = append(imports, sch.Imports...)
			imports = append(imports, sch.Includes...)
		}
	}
	// 构建 element/complexType 的 ns+name 索引。
	// struct 解码丢弃 QName 前缀，这里用 token 流把根元素 + 每个 schema 的
	// xmlns 声明找回来，供 message part 和 type/base 引用的前缀解析。
	rootNS, schemaNSs := collectNSContexts(raw)
	rootCtx := nsContext{prefixes: rootNS, self: defs.TargetNS}
	idx := newSchemaIndex()
	if defs.Types != nil {
		for i, sch := range defs.Types.Schemas {
			ctx := nsContext{prefixes: rootNS, self: sch.TargetNS}
			if i < len(schemaNSs) {
				ctx.prefixes = schemaNSs[i]
			}
			idx.addSchema(sch, ctx)
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
			// schemaLocation 可能是 URL（用 / 分隔）或 Windows 路径（用 \ 分隔），
			// path.Base 只认 /，这里归一化后再取文件名，兼容两种写法。
			basename := path.Base(strings.ReplaceAll(imp.SchemaLocation, "\\", "/"))
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
			extRootNS, _ := collectNSContexts(xsdRaw)
			idx.addSchema(*extSchema, nsContext{prefixes: extRootNS, self: extSchema.TargetNS})
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
			bindingName := localName(prt.Binding)
			port := Port{
				Name:    prt.Name,
				Binding: bindingName,
			}
			loc := ""
			if prt.SoapAddress != nil && prt.SoapAddress.Location != "" {
				loc = prt.SoapAddress.Location
			}
			port.Endpoint = loc
			// 端口级 SOAP 版本：根据绑定是 soap 还是 soap12 难以从结构区分（local name 相同），
			// 用项目级版本作为兜底；若 WSDL 同时含 1.1/1.2 默认按 1.1。
			port.SOAPVersion = bindingVersions[bindingName]
			if port.SOAPVersion == "" {
				port.SOAPVersion = p.SOAPVersion
			}
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
		bindingSOAPVer := bindingVersions[b.Name]
		if bindingSOAPVer == "" {
			bindingSOAPVer = p.SOAPVersion
		}
		for _, sv := range p.Services {
			for _, prt := range sv.Ports {
				if prt.Binding == b.Name && endpoint == "" {
					endpoint = prt.Endpoint
				}
			}
		}
		// binding 级 style：优先取 soap:binding 的 style（document/rpc），
		// 老 WSDL 常把它写在 soap:binding 而非 wsdl:binding 上；operation 级再覆盖。
		bindingStyle := b.Style
		if b.SoapBinding != nil && b.SoapBinding.Style != "" {
			bindingStyle = b.SoapBinding.Style
		}
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
			style := bindingStyle
			if bop.Style != "" {
				style = bop.Style
			} else if bop.SoapOperation != nil && bop.SoapOperation.Style != "" {
				style = bop.SoapOperation.Style
			}
			op := Operation{
				Name:        bop.Name,
				Endpoint:    endpoint,
				SOAPVersion: bindingSOAPVer,
				Style:       style,
			}
			if bop.SoapOperation != nil && bop.SoapOperation.SOAPAction != "" {
				op.SOAPAction = bop.SoapOperation.SOAPAction
			}
			// operation namespace：document/literal 时 soap:body 的 namespace 是权威来源
			// （决定 body 根元素的 namespace）；取不到再回退到 input element 所在
			// schema 的 targetNamespace，最后才用 definitions targetNamespace。
			op.Namespace = p.TargetNS
			if bop.Input.SoapBody != nil && bop.Input.SoapBody.Namespace != "" {
				op.Namespace = bop.Input.SoapBody.Namespace
			}

			// 解析 input
			if inMsg := resolveMessage(ptOp.Input.Message, msgs); inMsg != nil {
				for _, part := range inMsg.Parts {
					if part.Element != "" {
						partNS, partLocal := rootCtx.resolve(part.Element)
						op.InputName = partLocal
						el, elCtx, elNS, ok := idx.findElement(partNS, partLocal)
						if ok {
							op.InputParams = buildParams(el, idx, elCtx, 0)
							// namespace 兜底：soap:body namespace 没取到时，用 input element
							// 所在 schema 的 targetNamespace（soap:body namespace 更权威，勿覆盖）。
							if bop.Input.SoapBody == nil || bop.Input.SoapBody.Namespace == "" {
								if elNS != "" {
									op.Namespace = elNS
								} else {
									op.Namespace = p.TargetNS
								}
							}
							// document/literal wrapped：input 元素名 == operation 名且含子元素时，
							// 把外层包装剥掉，直接用其子元素作为参数（避免生成双层嵌套）。
							op.InputParams = unwrapWrapper(op.InputParams, op.Name, op.InputName)
						}
						if len(op.InputParams) == 0 {
							op.InputRaw = elementRawFragment(el)
						}
					} else if part.Type != "" {
						// rpc/literal：type 直接引用 complexType
						ctNS, ctName := rootCtx.resolve(part.Type)
						if ct, ctCtx, ok := idx.findComplexType(ctNS, ctName); ok {
							op.InputParams = paramsFromComplexType(ct, idx, ctCtx, 0)
							op.InputName = ctName
						}
					}
				}
			}
			// 解析 output
			if outMsg := resolveMessage(ptOp.Output.Message, msgs); outMsg != nil {
				for _, part := range outMsg.Parts {
					if part.Element != "" {
						partNS, partLocal := rootCtx.resolve(part.Element)
						op.OutputName = partLocal
						el, elCtx, _, ok := idx.findElement(partNS, partLocal)
						if ok {
							op.OutputParams = buildParams(el, idx, elCtx, 0)
							op.OutputParams = unwrapWrapper(op.OutputParams, op.Name+"Response", op.OutputName)
						}
						if len(op.OutputParams) == 0 {
							op.OutputRaw = elementRawFragment(el)
						}
					} else if part.Type != "" {
						ctNS, ctName := rootCtx.resolve(part.Type)
						if ct, ctCtx, ok := idx.findComplexType(ctNS, ctName); ok {
							op.OutputParams = paramsFromComplexType(ct, idx, ctCtx, 0)
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

// collectSOAPBindingVersions 按 soap:binding 元素的 namespace 判断每个 binding
// 使用 SOAP 1.1 还是 1.2。只看 local name 会把两种格式混在一起。
func collectSOAPBindingVersions(raw string) map[string]string {
	const wsdlNS = "http://schemas.xmlsoap.org/wsdl/"
	const soap11NS = "http://schemas.xmlsoap.org/wsdl/soap/"
	const soap12NS = "http://schemas.xmlsoap.org/wsdl/soap12/"
	out := map[string]string{}
	dec := xml.NewDecoder(strings.NewReader(raw))
	var bindingName string
	var bindingDepth int
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Space == wsdlNS && t.Name.Local == "binding" {
				bindingName = ""
				for _, a := range t.Attr {
					if a.Name.Local == "name" {
						bindingName = a.Value
						break
					}
				}
				bindingDepth = depth
				continue
			}
			if bindingName != "" && t.Name.Local == "binding" {
				switch t.Name.Space {
				case soap12NS:
					out[bindingName] = "1.2"
				case soap11NS:
					out[bindingName] = "1.1"
				}
			}
		case xml.EndElement:
			if bindingName != "" && depth == bindingDepth && t.Name.Space == wsdlNS && t.Name.Local == "binding" {
				bindingName = ""
				bindingDepth = 0
			}
			depth--
		}
	}
}

// containsLocalName 粗略判断 XML 文本里是否出现某 local name 的开始标签。
// 匹配无前缀(<name>、<name )和带前缀( <prefix:name )两种情况。
func containsLocalName(raw, name string) bool {
	return strings.Contains(raw, "<"+name+">") ||
		strings.Contains(raw, "<"+name+" ") ||
		strings.Contains(raw, ":"+name)
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
// ctx 是 element 所在 schema 的 namespace 上下文，用于解析 type/ref 等 QName 引用。
// element 可能有：type 引用（可能指向 complexType）、inline complexType、inline sequence。
func buildParams(el xsdElement, idx *schemaIndex, ctx nsContext, depth int) []Param {
	if depth > 6 {
		return nil // 防止递归爆栈
	}
	// type 引用了 complexType（优先检查，因为外部 XSD 的类型都在这里）
	if el.Type != "" {
		ctNS, ctName := ctx.resolve(el.Type)
		if ct, ctCtx, ok := idx.findComplexType(ctNS, ctName); ok {
			p := Param{Name: el.Name, Type: el.Type, MinOccurs: el.MinOccurs, MaxOccurs: el.MaxOccurs, Nillable: el.Nillable}
			p.Children = paramsFromComplexType(ct, idx, ctCtx, depth)
			return []Param{p}
		}
	}
	// inline complexType
	if el.ComplexType != nil {
		p := Param{Name: el.Name, Type: el.ComplexType.Name, MinOccurs: el.MinOccurs, MaxOccurs: el.MaxOccurs, Nillable: el.Nillable}
		p.Children = paramsFromComplexType(*el.ComplexType, idx, ctx, depth)
		return []Param{p}
	}
	// 直接 inline sequence（无 complexType 包装，少见）
	if el.Sequence != nil {
		p := Param{Name: el.Name, MinOccurs: el.MinOccurs, MaxOccurs: el.MaxOccurs, Nillable: el.Nillable}
		p.Children = paramsFromSequence(*el.Sequence, idx, ctx, depth)
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

func paramsFromComplexType(ct xsdComplexType, idx *schemaIndex, ctx nsContext, depth int) []Param {
	if depth > 6 {
		return nil
	}
	// 优先处理 complexContent/extension：先展开父类型，再追加当前字段
	if ct.ComplexContent != nil && ct.ComplexContent.Extension != nil {
		ext := ct.ComplexContent.Extension
		var out []Param
		// 展开 base 类型的字段
		baseNS, baseName := ctx.resolve(ext.Base)
		if baseCT, baseCtx, ok := idx.findComplexType(baseNS, baseName); ok {
			out = paramsFromComplexType(baseCT, idx, baseCtx, depth+1)
		}
		// 追加 extension 里的字段
		if ext.Sequence != nil {
			out = append(out, paramsFromSequence(*ext.Sequence, idx, ctx, depth+1)...)
		}
		if ext.Choice != nil {
			out = append(out, paramsFromSequence(*ext.Choice, idx, ctx, depth+1)...)
		}
		if ext.All != nil {
			out = append(out, paramsFromSequence(*ext.All, idx, ctx, depth+1)...)
		}
		return out
	}
	// 普通 complexType
	if ct.Sequence != nil {
		return paramsFromSequence(*ct.Sequence, idx, ctx, depth)
	}
	if ct.Choice != nil {
		return paramsFromSequence(*ct.Choice, idx, ctx, depth)
	}
	if ct.All != nil {
		return paramsFromSequence(*ct.All, idx, ctx, depth)
	}
	return nil
}

func paramsFromSequence(seq xsdSequence, idx *schemaIndex, ctx nsContext, depth int) []Param {
	if depth > 6 {
		return nil
	}
	var out []Param
	for _, child := range seq.Elements {
		out = append(out, buildParams(child, idx, ctx, depth+1)...)
	}
	// 嵌套 sequence/choice
	for _, ns := range seq.Sequences {
		out = append(out, paramsFromSequence(ns, idx, ctx, depth+1)...)
	}
	for _, nc := range seq.Choices {
		out = append(out, paramsFromSequence(nc, idx, ctx, depth+1)...)
	}
	return out
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
// 支持 http/https URL、相对路径（如 "SuLianLoanHengLiServiceCore.xsd"、"./xsd/a.xsd"）、
// 以及 schemaLocation 本身就是一个绝对 URL 的情况。
// 30s 超时（XSD 通常很小，超时主要是防挂起）。
func fetchExternalSchema(sourceURL, schemaLocation string) (string, error) {
	u, err := url.Parse(sourceURL)
	if err != nil {
		return "", fmt.Errorf("解析 sourceURL 失败: %w", err)
	}
	// 用 RFC 3986 相对引用解析：正确覆盖绝对 URL / 相对路径 / ../ / ./ / query / fragment，
	// 避免手写 path.Join 在 schemaLocation 是绝对 URL 时拼出垃圾路径。
	ref, err := url.Parse(schemaLocation)
	if err != nil {
		return "", fmt.Errorf("解析 schemaLocation 失败: %w", err)
	}
	schemaURL := u.ResolveReference(ref)

	client := &http.Client{
		Timeout: 30 * time.Second,
		// 与 WSDL URL 导入保持一致：跳过自签证书校验 + SSRF 安全拨号。
		Transport: &http.Transport{
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			ResponseHeaderTimeout: 25 * time.Second,
			DialContext:           SafeDialContext,
		},
	}
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
	decoded, err := DecodeXMLBytes(data, resp.Header.Get("Content-Type"))
	if err != nil {
		return "", fmt.Errorf("解码 XSD 内容失败: %w", err)
	}
	return decoded, nil
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

// stripDefaultNamespace 去掉 XML 里的默认 namespace 声明（xmlns="..."），
// 但保留带前缀的 namespace（xmlns:xxx="..."）。
func stripDefaultNamespace(raw string) string {
	return stripDefaultNSRe.ReplaceAllString(raw, "")
}
