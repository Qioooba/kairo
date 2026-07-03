// Package webservice 提供 SOAP / WebService 调试能力：
// WSDL 解析、SOAP 报文生成、请求发送、Mock 服务端。
//
// 设计原则：
//   - 零外部依赖，仅用标准库 + golang.org/x/text（GBK 编码）；
//   - WSDL 解析做"尽力而为 + 降级"：复杂 XSD import/include 解析失败不致命，
//     保留原始片段并在 Warnings 里给出提示；
//   - 数据结构带 Version 字段，方便以后升级持久化格式。
package webservice

// DataVersion 是持久化数据的结构版本号，升级时递增。
const DataVersion = 1

// Param 描述一个 SOAP 输入/输出字段。
// 简单类型只有 Name/Type；复杂类型通过 Children 递归展开。
type Param struct {
	Name      string  `json:"name,omitempty"`
	Type      string  `json:"type,omitempty"`
	MinOccurs string  `json:"min_occurs,omitempty"`
	MaxOccurs string  `json:"max_occurs,omitempty"`
	Nillable  bool    `json:"nillable,omitempty"`
	Children  []Param `json:"children,omitempty"`
}

// Operation 描述一个 SOAP operation。
type Operation struct {
	Name         string  `json:"name"`
	SOAPAction   string  `json:"soap_action"`
	Namespace    string  `json:"namespace"`       // operation 元素所在 namespace（通常 = schema targetNamespace）
	Endpoint     string  `json:"endpoint"`        // 暴露该 operation 的 endpoint 地址
	SOAPVersion  string  `json:"soap_version"`    // "1.1" / "1.2"，空视为 1.1
	Style        string  `json:"style,omitempty"` // document / rpc
	InputName    string  `json:"input_name,omitempty"`
	OutputName   string  `json:"output_name,omitempty"`
	InputParams  []Param `json:"input_params,omitempty"`
	OutputParams []Param `json:"output_params,omitempty"`
	InputRaw     string  `json:"input_raw,omitempty"` // 解析不出参数时保留的原始片段
	OutputRaw    string  `json:"output_raw,omitempty"`
}

// Port 描述 service 下的一个 port（binding + endpoint）。
type Port struct {
	Name        string `json:"name"`
	Binding     string `json:"binding"`
	Endpoint    string `json:"endpoint"`
	SOAPVersion string `json:"soap_version"`
}

// Service 描述 WSDL 中的一个 service。
type Service struct {
	Name  string `json:"name"`
	Ports []Port `json:"ports"`
}

// WSDLProject 是一次 WSDL 导入的结果，可本地保存。
type WSDLProject struct {
	Version     int         `json:"version"`
	ID          string      `json:"id"`
	Name        string      `json:"name"`   // 用户命名或从 service 名推导
	Source      string      `json:"source"` // "url" / "upload"
	SourceURL   string      `json:"source_url,omitempty"`
	CreatedAt   string      `json:"created_at"`
	UpdatedAt   string      `json:"updated_at"`
	SOAPVersion string      `json:"soap_version,omitempty"` // 项目主 SOAP 版本（取首个 port）
	TargetNS    string      `json:"target_ns,omitempty"`
	Services    []Service   `json:"services"`
	Operations  []Operation `json:"operations"`
	Warnings    []string    `json:"warnings,omitempty"`
	ParseError  string      `json:"parse_error,omitempty"`
	RawWSDL     string      `json:"raw_wsdl,omitempty"` // 原始 WSDL 文本（便于回显/重解析）
}

// Template 是保存的 SOAP 请求模板。
type Template struct {
	Version     int               `json:"version"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Group       string            `json:"group"`
	Endpoint    string            `json:"endpoint"`
	Operation   string            `json:"operation"`
	SOAPAction  string            `json:"soap_action"`
	SOAPVersion string            `json:"soap_version,omitempty"` // 回放时需还原 SOAP 版本（与 HistoryEntry 对齐）
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body"`
	Encoding    string            `json:"encoding"` // "UTF-8" / "GBK"
	TimeoutMs   int               `json:"timeout_ms"`
	Note        string            `json:"note,omitempty"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
}

// HistoryEntry 是一次请求历史记录。
type HistoryEntry struct {
	Version      int               `json:"version"`
	ID           string            `json:"id"`
	Time         string            `json:"time"`
	Endpoint     string            `json:"endpoint"`
	Operation    string            `json:"operation"`
	SOAPAction   string            `json:"soap_action"`
	SOAPVersion  string            `json:"soap_version,omitempty"` // 回放时需还原 SOAP 版本
	Encoding     string            `json:"encoding"`
	TimeoutMs    int               `json:"timeout_ms,omitempty"` // 回放时需还原超时
	Headers      map[string]string `json:"headers,omitempty"`    // 自定义请求头，回放时还原
	RequestBody  string            `json:"request_body"`
	ResponseBody string            `json:"response_body,omitempty"`
	StatusCode   int               `json:"status_code"`
	DurationMs   int64             `json:"duration_ms"`
	Success      bool              `json:"success"`
	Error        string            `json:"error,omitempty"`
}

// MockConfig 是一个 Mock WebService 接口配置。
type MockConfig struct {
	Version    int    `json:"version"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Path       string `json:"path"` // 例如 /mock/customerQuery
	Operation  string `json:"operation,omitempty"`
	StatusCode int    `json:"status_code"` // 默认 200
	DelayMs    int    `json:"delay_ms"`
	Enabled    bool   `json:"enabled"`
	Body       string `json:"body"` // 固定响应 XML
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// MockRequestRecord 是 Mock 接口收到的一次请求记录。
type MockRequestRecord struct {
	Time    string            `json:"time"`
	Path    string            `json:"path"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}
