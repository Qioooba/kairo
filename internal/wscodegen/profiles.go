package wscodegen

// Profiles 返回前端展示用的引擎说明。内容是配置清单，不是营销文案。
func Profiles() []EngineProfile {
	return []EngineProfile{
		{
			ID:        EnginePortable,
			Name:      "纯 JDK HttpURLConnection",
			Summary:   "零第三方依赖，Java 1.6 源码，放进 JDK 6 / 8 / 11 工程都能编过。",
			WhenToUse: "工程里没有 Axis / XFire / CXF / JAX-WS，或只想要一个能发 SOAP 的客户端。最稳的保底选项。",
			JavaMin:   "1.6",
			BuiltinOK: true,
			RuntimeNotes: []string{
				"运行时只需 JRE，不需要 tools.jar / wsimport。",
				"报文按 document/literal 组装，复杂类型用嵌套 JavaBean + 字符串字段，避免 JAXB 版本冲突。",
			},
			CompileNotes: []string{
				"javac -source 1.6 -target 1.6。不要用 diamond、try-with-resources、lambda。",
			},
		},
		{
			ID:            EngineJAXWS,
			Name:          "JAX-WS（JDK 6 / 8 自带）",
			Summary:       "用 JDK 自带的 javax.xml.ws.Dispatch 或官方 wsimport 生成 SEI。",
			WhenToUse:     "工程是 JDK 6/7/8，代码里已经在用 @WebService / Dispatch / Service.getPort。",
			JavaMin:       "1.6",
			BuiltinOK:     true,
			ToolAvailable: true,
			ToolClass:     "com.sun.tools.ws.WsImport / bin/wsimport",
			RequiredJars:  []string{"JDK 6–8 的 wsimport（或 lib/tools.jar）"},
			RecommendedFlags: []string{
				"wsimport -keep -s <src> -p <pkg> -Xnocompile -encoding UTF-8",
				"目标运行时是 JDK 6 时必须加 -target 2.1（JAX-WS 2.1）；JDK 8 默认为 2.2",
			},
			RuntimeNotes: []string{
				"JDK 6 = JAX-WS 2.1 + JAXB 2.1；JDK 8 = JAX-WS 2.2 + JAXB 2.2。用 8 生成、6 编译会缺 API。",
				"JDK 11+ 已移除 JAX-WS，需要额外 jaxws-rt / jaxws-tools，或改选 portable / CXF。",
			},
			CompileNotes: []string{
				"生成 JDK 尽量 ≤ 目标工程 JDK。不要拿本机新 JDK 的默认 wsimport 去喂老工程。",
			},
		},
		{
			ID:            EngineCXF,
			Name:          "Apache CXF",
			Summary:       "内置生成 JaxWsProxyFactoryBean + SEI；有 jar 时调用 org.apache.cxf.tools.wsdlto.WSDLToJava。",
			WhenToUse:     "Spring / 较新的 Java EE 工程，WEB-INF/lib 里有 cxf-*.jar。",
			JavaMin:       "1.6",
			BuiltinOK:     true,
			ToolAvailable: true,
			ToolClass:     "org.apache.cxf.tools.wsdlto.WSDLToJava",
			RequiredJars:  []string{"cxf-core", "cxf-tools-wsdlto-*", "cxf-rt-frontend-jaxws", "wsdl4j", "neethi", "xmlschema"},
			OptionalJars:  []string{"cxf-rt-transports-http", "jaxb-impl", "woodstox / stax"},
			RecommendedFlags: []string{
				"WSDLToJava -d <src> -p <pkg> -autoNameResolution",
				"CXF 2.x 可跑在 JDK 6；CXF 3.x 需要 JDK 8+",
			},
			RuntimeNotes: []string{
				"必须用工程里那一套 CXF 大版本去生成，2.x 和 3.x 的包结构不混用。",
			},
			CompileNotes: []string{
				"扫描到的 cxf-*.jar 全部进 classpath，缺 xmlschema / neethi / wsdl4j 时官方工具会立刻失败。",
			},
		},
		{
			ID:            EngineAxis1,
			Name:          "Apache Axis 1.4",
			Summary:       "内置 org.apache.axis.client.Call 发 SOAP；有 jar 时调用 WSDL2Java 出 stub。",
			WhenToUse:     "WebSphere 6/7、JDK 1.6 前后的老工程，lib 里能看到 axis.jar + jaxrpc.jar。",
			JavaMin:       "1.4+",
			BuiltinOK:     true,
			ToolAvailable: true,
			ToolClass:     "org.apache.axis.wsdl.WSDL2Java",
			RequiredJars:  []string{"axis.jar", "jaxrpc.jar", "saaj.jar", "wsdl4j.jar", "commons-logging", "commons-discovery"},
			OptionalJars:  []string{"mail.jar / geronimo-javamail", "activation.jar", "commons-httpclient"},
			RecommendedFlags: []string{
				"WSDL2Java -o <src> -p <pkg> -w (wrap arrays)",
			},
			RuntimeNotes: []string{
				"生成的 stub 依赖 org.apache.axis.*，工程 classpath 必须有同一套 Axis 1.4 jar。",
				"内置 Call 模式不生成 stub，只按 SOAP 报文调用，对 jar 版本更宽容。",
			},
			CompileNotes: []string{
				"缺 commons-discovery / activation 时 WSDL2Java 会 NoClassDefFoundError，扫描项目后把这几个 jar 勾上。",
			},
		},
		{
			ID:            EngineAxis2,
			Name:          "Apache Axis2",
			Summary:       "内置 ServiceClient + AXIOM 字符串报文；有 jar 时调用 Axis2 WSDL2Java。",
			WhenToUse:     "工程里有 axis2-*.jar / axiom-*.jar，常见于 WebSphere 8 和一批中间件。",
			JavaMin:       "1.6",
			BuiltinOK:     true,
			ToolAvailable: true,
			ToolClass:     "org.apache.axis2.wsdl.WSDL2Java",
			RequiredJars:  []string{"axis2-kernel", "axis2-codegen", "axiom-api", "axiom-impl", "wsdl4j", "neethi", "xmlschema"},
			OptionalJars:  []string{"axis2-transport-http", "axis2-transport-local", "commons-logging", "commons-httpclient"},
			RecommendedFlags: []string{
				"WSDL2Java -uri <wsdl> -o <out> -p <pkg> -s (sync only)",
			},
			RuntimeNotes: []string{
				"ADB / XMLBeans / JAXB 三种 databinding 不要混。内置模式走裸 SOAP，避开 databinding 版本坑。",
			},
			CompileNotes: []string{
				"官方工具 jar 很多，务必选工程目录扫描，不要只丢一个 axis2-codegen.jar。",
			},
		},
		{
			ID:            EngineXFire,
			Name:          "Codehaus XFire 1.2",
			Summary:       "内置 org.codehaus.xfire.client.Client 动态调用；有 jar 时走 Wsdl11Generator。",
			WhenToUse:     "信贷 / 老 Spring 工程：WEB-INF/lib 里是 xfire-all-1.2.6，常再拆一份 xfire-core/aegis/spring/jaxb2。",
			JavaMin:       "1.5 / 1.6",
			BuiltinOK:     true,
			ToolAvailable: true,
			ToolClass:     "org.codehaus.xfire.gen.Wsdl11Generator",
			RequiredJars:  []string{"xfire-all 或 xfire-core + xfire-aegis"},
			OptionalJars:  []string{"xfire-spring", "xfire-jaxb2", "xfire-java5", "xfire-annotations", "xfire-generator", "wsdl4j", "stax/wstx"},
			RecommendedFlags: []string{
				"内置：org.codehaus.xfire.client.Client 动态调用（缺 generator 时用这个）",
				"官方：Wsdl11Generator 需要单独的 xfire-generator.jar，xfire-all 里通常没有",
			},
			RuntimeNotes: []string{
				"生成代码必须对准工程里的 1.2.6：import org.codehaus.xfire.*，不要改成 CXF / JAX-WS。",
				"xfire-all 与拆开的 xfire-core/aegis/jaxb2/spring 可以同时存在，内置 Client 按 all 的包名出代码。",
				"动态 Client 需要能读到 WSDL（http URL 或 file:/// 本地路径）。",
			},
			CompileNotes: []string{
				"没扫到 xfire-generator.jar 时不要开官方工具，用内置生成。运行时仍要有 wsdl4j + stax/wstx（常和 xfire 放同一 lib）。",
			},
		},
	}
}

func profileByID(id string) (EngineProfile, bool) {
	for _, p := range Profiles() {
		if p.ID == id {
			return p, true
		}
	}
	return EngineProfile{}, false
}
