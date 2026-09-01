// Package wscodegen 根据 WSDL / 已导入的 WebService 项目生成 Java 客户端代码。
//
// 设计取舍（兼容老 Java 工程是第一目标）：
//
//  1. 不要默认拿“本机 PATH 上那个 JDK”去生成。JDK 8 wsimport 默认产出 JAX-WS 2.2，
//     丢进 JDK 6 工程会在 javax.xml.ws / JAXB 上编译失败；JDK 11+ 更是直接没有 wsimport。
//  2. 优先对齐目标工程：用户选项目目录（扫描 lib / WEB-INF/lib 里的 axis / xfire / cxf jar）
//     + 选工程自带的 JDK Home。官方 wsdl2java / wsimport 用这套 classpath 跑，生成结果
//     才能跟工程里真正在用的运行时一致。
//  3. 内置生成器永远可用：产出 Java 1.6 源码，不依赖本机是否装着对应工具。
//     portable 引擎只用 HttpURLConnection，放进任何 JDK 6/8 工程都能编过。
package wscodegen

import "kairo/internal/webservice"

const (
	EnginePortable = "portable"
	EngineJAXWS    = "jaxws"
	EngineCXF      = "cxf"
	EngineAxis1    = "axis1"
	EngineAxis2    = "axis2"
	EngineXFire    = "xfire"

	ModeBuiltin = "builtin"
	ModeTool    = "tool"

	JavaSource16 = "1.6"
	JavaSource18 = "1.8"

	JAXWSTarget21 = "2.1"
	JAXWSTarget22 = "2.2"
)

// EngineProfile 描述一种可生成的客户端栈，前端用来展示“何时选 / 要配什么”。
type EngineProfile struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Summary          string   `json:"summary"`
	WhenToUse        string   `json:"when_to_use"`
	JavaMin          string   `json:"java_min"`
	BuiltinOK        bool     `json:"builtin_ok"`
	ToolAvailable    bool     `json:"tool_available"`
	ToolClass        string   `json:"tool_class,omitempty"`
	RequiredJars     []string `json:"required_jars"`
	OptionalJars     []string `json:"optional_jars"`
	RecommendedFlags []string `json:"recommended_flags"`
	RuntimeNotes     []string `json:"runtime_notes"`
	CompileNotes     []string `json:"compile_notes"`
}

// JDKInfo 是一次 JDK 探测结果。
type JDKInfo struct {
	Home        string `json:"home"`
	Version     string `json:"version"`
	Major       int    `json:"major"`
	Vendor      string `json:"vendor,omitempty"`
	HasJava     bool   `json:"has_java"`
	HasJavac    bool   `json:"has_javac"`
	HasWsimport bool   `json:"has_wsimport"`
	HasToolsJar bool   `json:"has_tools_jar"`
	Source      string `json:"source"`
	Notes       string `json:"notes,omitempty"`
}

// JarHit 是项目目录里扫到的生成器 / 运行时 jar。
type JarHit struct {
	Path     string `json:"path"`
	FileName string `json:"file_name"`
	Kind     string `json:"kind"` // axis1 / axis2 / xfire / cxf / jaxws / wsdl4j / other
}

// ScanResult 是项目目录扫描结果。
type ScanResult struct {
	ProjectDir        string   `json:"project_dir"`
	Jars              []JarHit `json:"jars"`
	DetectedEngines   []string `json:"detected_engines"`
	SuggestedEngine   string   `json:"suggested_engine"`
	SuggestedSrc      string   `json:"suggested_src,omitempty"`
	MissingForSuggest []string `json:"missing_for_suggest,omitempty"`
	PomHints          []string `json:"pom_hints,omitempty"`
	Notes             []string `json:"notes,omitempty"`
	Truncated         bool     `json:"truncated,omitempty"`
}

// GeneratedFile 是一份即将写出或预览的源文件。
type GeneratedFile struct {
	RelPath string `json:"rel_path"`
	Content string `json:"content"`
	Kind    string `json:"kind"` // java / txt
}

// Request 是一次代码生成请求。
type Request struct {
	Engine        string   `json:"engine"`
	Mode          string   `json:"mode"`
	PackageName   string   `json:"package"`
	OutputDir     string   `json:"output_dir"`
	ProjectDir    string   `json:"project_dir"`
	Overwrite     bool     `json:"overwrite"`
	IncludeMain   bool     `json:"include_main"`
	JavaSource    string   `json:"java_source"`
	JAXWSTarget   string   `json:"jaxws_target"`
	JDKHome       string   `json:"jdk_home"`
	ClasspathJars []string `json:"classpath_jars"`
	ExtraFlags    []string `json:"extra_flags"`
	OpenAfter     bool     `json:"open_after"`
	DryRun        bool     `json:"dry_run"`

	WSDLProjectID string `json:"wsdl_project_id"`
	WSDLContent   string `json:"wsdl_content"`
	WSDLURL       string `json:"wsdl_url"`
	WSDLFile      string `json:"wsdl_file"`
	ServiceName   string `json:"service_name"`
}

// Result 是一次生成（或预览）的结果。
type Result struct {
	OK          bool            `json:"ok"`
	Engine      string          `json:"engine"`
	Mode        string          `json:"mode"`
	PackageName string          `json:"package"`
	OutputDir   string          `json:"output_dir,omitempty"`
	Files       []GeneratedFile `json:"files"`
	Written     []string        `json:"written,omitempty"`
	Command     string          `json:"command,omitempty"`
	ToolLog     string          `json:"tool_log,omitempty"`
	Warnings    []string        `json:"warnings,omitempty"`
	Notes       []string        `json:"notes,omitempty"`
}

// resolvedWSDL 是内部用的、已经解析好的 WSDL 输入。
type resolvedWSDL struct {
	Project         *webservice.WSDLProject
	FilePath        string // 官方工具优先用真实文件，方便相对 XSD import
	URL             string
	Raw             string
	SiblingXSDCount int
}
