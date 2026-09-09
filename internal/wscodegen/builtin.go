package wscodegen

import (
	"fmt"
	"sort"
	"strings"

	"kairo/internal/webservice"
)

func generateBuiltin(req Request, resolved resolvedWSDL) ([]GeneratedFile, []string, error) {
	pkg := JavaPackage(req.PackageName)
	if strings.TrimSpace(req.PackageName) == "" {
		if resolved.Project != nil && resolved.Project.TargetNS != "" {
			pkg = PackageFromNamespace(resolved.Project.TargetNS)
		}
	}
	javaSrc := req.JavaSource
	if javaSrc != JavaSource18 {
		javaSrc = JavaSource16
	}
	model := buildModel(resolved.Project, pkg, req.ServiceName)
	files := []GeneratedFile{
		genXmlUtil(pkg),
		genSoapSupport(pkg, javaSrc),
	}
	seen := map[string]bool{}
	for _, bean := range model.beans {
		key := bean.ClassName
		if seen[key] {
			continue
		}
		seen[key] = true
		files = append(files, genBean(pkg, bean))
	}
	switch req.Engine {
	case EnginePortable, "":
		files = append(files, genPortableClient(pkg, model, javaSrc, req.IncludeMain)...)
	case EngineJAXWS:
		files = append(files, genJAXWSClient(pkg, model, javaSrc, req.IncludeMain, req.JAXWSTarget)...)
	case EngineCXF:
		files = append(files, genCXFClient(pkg, model, javaSrc, req.IncludeMain)...)
	case EngineAxis1:
		files = append(files, genAxis1Client(pkg, model, javaSrc, req.IncludeMain)...)
	case EngineAxis2:
		files = append(files, genAxis2Client(pkg, model, javaSrc, req.IncludeMain)...)
	case EngineXFire:
		files = append(files, genXFireClient(pkg, model, javaSrc, req.IncludeMain, resolved)...)
	default:
		return nil, nil, fmt.Errorf("未知引擎 %s", req.Engine)
	}
	files = append(files, genReadme(pkg, req, model, resolved))
	var warns []string
	if resolved.Project != nil {
		warns = append(warns, resolved.Project.Warnings...)
		if resolved.Project.ParseError != "" {
			warns = append(warns, "WSDL 解析告警: "+resolved.Project.ParseError)
		}
		if len(resolved.Project.Operations) == 0 {
			warns = append(warns, "没有解析到 operation，已生成通用 call(String soap) 入口，请自己填 SOAP 报文。")
		}
	}
	return files, warns, nil
}

type beanField struct {
	XMLName   string
	FieldName string
	ClassName string // empty = String
	Repeated  bool
	XSDType   string
	Children  []beanField
}

type beanType struct {
	ClassName string
	XMLName   string
	Fields    []beanField
}

type opModel struct {
	Name       string
	MethodName string
	SOAPAction string
	Endpoint   string
	SOAPVer    string
	Namespace  string
	Style      string
	InputXML   string
	OutputXML  string
	InputBean  string
	OutputBean string
	Input      *beanType
	Output     *beanType
}

type wsModel struct {
	Package     string
	ServiceName string
	ClassName   string
	Namespace   string
	Endpoint    string
	SOAPVer     string
	WSDLURL     string
	ops         []opModel
	beans       []beanType
}

func buildModel(p *webservice.WSDLProject, pkg, serviceName string) wsModel {
	m := wsModel{Package: pkg, ServiceName: "GeneratedService", ClassName: "GeneratedServiceClient", SOAPVer: "1.1"}
	if p == nil {
		return m
	}
	if serviceName != "" {
		m.ServiceName = serviceName
	} else if p.Name != "" {
		m.ServiceName = p.Name
	} else if len(p.Services) > 0 && p.Services[0].Name != "" {
		m.ServiceName = p.Services[0].Name
	}
	m.ClassName = JavaClassName(m.ServiceName) + "Client"
	m.Namespace = p.TargetNS
	if p.SOAPVersion != "" {
		m.SOAPVer = p.SOAPVersion
	}
	if p.SourceURL != "" {
		m.WSDLURL = p.SourceURL
	}
	if len(p.Services) > 0 && len(p.Services[0].Ports) > 0 {
		m.Endpoint = p.Services[0].Ports[0].Endpoint
		if p.Services[0].Ports[0].SOAPVersion != "" {
			m.SOAPVer = p.Services[0].Ports[0].SOAPVersion
		}
	}
	beanIndex := map[string]*beanType{}
	for _, op := range p.Operations {
		om := opModel{
			Name:       op.Name,
			MethodName: JavaIdent(op.Name),
			SOAPAction: op.SOAPAction,
			Endpoint:   op.Endpoint,
			SOAPVer:    op.SOAPVersion,
			Namespace:  op.Namespace,
			Style:      op.Style,
			InputXML:   op.InputName,
			OutputXML:  op.OutputName,
		}
		if om.SOAPVer == "" {
			om.SOAPVer = m.SOAPVer
		}
		if om.Endpoint == "" {
			om.Endpoint = m.Endpoint
		}
		if om.Namespace == "" {
			om.Namespace = m.Namespace
		}
		if om.InputXML == "" {
			om.InputXML = op.Name
		}
		inBean := collectBean(op.InputName, op.Name, op.InputParams, beanIndex)
		outBean := collectBean(op.OutputName, op.Name+"Response", op.OutputParams, beanIndex)
		if inBean != nil {
			om.Input = inBean
			om.InputBean = inBean.ClassName
		}
		if outBean != nil {
			om.Output = outBean
			om.OutputBean = outBean.ClassName
		}
		m.ops = append(m.ops, om)
	}
	for _, b := range beanIndex {
		m.beans = append(m.beans, *b)
	}
	sort.Slice(m.beans, func(i, j int) bool { return m.beans[i].ClassName < m.beans[j].ClassName })
	return m
}

func collectBean(xmlName, fallback string, params []webservice.Param, index map[string]*beanType) *beanType {
	name := xmlName
	if name == "" {
		name = fallback
	}
	if name == "" {
		name = "Payload"
	}
	className := JavaClassName(name)
	if existing, ok := index[className]; ok {
		return existing
	}
	bean := &beanType{ClassName: className, XMLName: name}
	index[className] = bean
	use := params
	if len(params) == 1 && len(params[0].Children) > 0 && strings.EqualFold(params[0].Name, name) {
		use = params[0].Children
	}
	for _, p := range use {
		f := beanField{
			XMLName:   p.Name,
			FieldName: JavaIdent(p.Name),
			XSDType:   p.Type,
			Repeated:  p.MaxOccurs == "unbounded" || (p.MaxOccurs != "" && p.MaxOccurs != "1"),
		}
		if len(p.Children) > 0 {
			child := collectBean(p.Name, p.Name, p.Children, index)
			if child != nil {
				f.ClassName = child.ClassName
			}
		}
		bean.Fields = append(bean.Fields, f)
	}
	return bean
}

func srcPath(pkg, className string) string {
	return packageToPath(pkg) + "/" + className + ".java"
}

func genXmlUtil(pkg string) GeneratedFile {
	body := `package ` + pkg + `;

/**
 * JDK 1.6 兼容的极小 XML 工具：转义 + 按标签抽取。
 * 不依赖 JAXB / DOM，避免 JDK 6/8/11 之间的 XML 实现差异。
 */
public class XmlUtil {
    public static String escape(String s) {
        if (s == null) {
            return "";
        }
        StringBuffer sb = new StringBuffer(s.length() + 16);
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            switch (c) {
                case '&':  sb.append("&amp;"); break;
                case '<':  sb.append("&lt;"); break;
                case '>':  sb.append("&gt;"); break;
                case '"':  sb.append("&quot;"); break;
                case '\'': sb.append("&apos;"); break;
                default:   sb.append(c);
            }
        }
        return sb.toString();
    }

    public static String firstTag(String xml, String tag) {
        if (xml == null || tag == null || tag.length() == 0) {
            return "";
        }
        int start = indexOfOpen(xml, tag, 0);
        if (start < 0) {
            int colon = tag.indexOf(':');
            if (colon >= 0) {
                return firstTag(xml, tag.substring(colon + 1));
            }
            return "";
        }
        int gt = xml.indexOf('>', start);
        if (gt < 0) {
            return "";
        }
        if (gt > 0 && xml.charAt(gt - 1) == '/') {
            return "";
        }
        String local = localName(tag);
        int closeIdx = xml.indexOf("</" + tag + ">", gt + 1);
        if (closeIdx < 0) {
            closeIdx = xml.indexOf("</" + local + ">", gt + 1);
        }
        if (closeIdx < 0) {
            return "";
        }
        return xml.substring(gt + 1, closeIdx);
    }

    public static String localName(String tag) {
        if (tag == null) {
            return "";
        }
        int i = tag.indexOf(':');
        if (i >= 0 && i + 1 < tag.length()) {
            return tag.substring(i + 1);
        }
        return tag;
    }

    private static int indexOfOpen(String xml, String tag, int from) {
        String open = "<" + tag;
        int idx = xml.indexOf(open, from);
        while (idx >= 0) {
            int after = idx + open.length();
            if (after >= xml.length()) {
                return -1;
            }
            char c = xml.charAt(after);
            if (c == '>' || c == ' ' || c == '/' || c == '\t' || c == '\n' || c == '\r') {
                return idx;
            }
            idx = xml.indexOf(open, after);
        }
        return -1;
    }
}
`
	return GeneratedFile{RelPath: srcPath(pkg, "XmlUtil"), Content: body, Kind: "java"}
}

func genSoapSupport(pkg, javaSrc string) GeneratedFile {
	_ = javaSrc
	body := `package ` + pkg + `;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;

/**
 * SOAP 1.1 / 1.2 HttpURLConnection 传输。仅用 JDK 类，兼容 Java 1.6。
 */
public class SoapTransport {
    private int timeoutMs = 30000;
    private String encoding = "UTF-8";

    public void setTimeoutMs(int timeoutMs) {
        this.timeoutMs = timeoutMs;
    }

    public void setEncoding(String encoding) {
        if (encoding != null && encoding.length() > 0) {
            this.encoding = encoding;
        }
    }

    public static String envelope(String soapVersion, String namespace, String bodyLocalName, String innerXml) {
        String envNs = "1.2".equals(soapVersion)
                ? "http://www.w3.org/2003/05/soap-envelope"
                : "http://schemas.xmlsoap.org/soap/envelope/";
        StringBuffer sb = new StringBuffer();
        sb.append("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n");
        sb.append("<soapenv:Envelope xmlns:soapenv=\"").append(envNs).append("\"");
        if (namespace != null && namespace.length() > 0) {
            sb.append(" xmlns:web=\"").append(XmlUtil.escape(namespace)).append("\"");
        }
        sb.append(">\n  <soapenv:Header/>\n  <soapenv:Body>\n");
        if (namespace != null && namespace.length() > 0) {
            sb.append("    <web:").append(bodyLocalName).append(">\n");
        } else {
            sb.append("    <").append(bodyLocalName).append(">\n");
        }
        if (innerXml != null) {
            sb.append(innerXml);
        }
        if (namespace != null && namespace.length() > 0) {
            sb.append("    </web:").append(bodyLocalName).append(">\n");
        } else {
            sb.append("    </").append(bodyLocalName).append(">\n");
        }
        sb.append("  </soapenv:Body>\n</soapenv:Envelope>\n");
        return sb.toString();
    }

    public String post(String endpoint, String soap, String soapAction, String soapVersion) throws Exception {
        if (endpoint == null || endpoint.length() == 0) {
            throw new IllegalArgumentException("endpoint 为空");
        }
        URL url = new URL(endpoint);
        HttpURLConnection conn = (HttpURLConnection) url.openConnection();
        conn.setConnectTimeout(timeoutMs);
        conn.setReadTimeout(timeoutMs);
        conn.setDoOutput(true);
        conn.setDoInput(true);
        conn.setRequestMethod("POST");
        conn.setRequestProperty("Content-Type", contentType(soapVersion, encoding));
        if (soapAction == null) {
            soapAction = "";
        }
        if ("1.2".equals(soapVersion)) {
            // SOAP 1.2 动作用 action 参数，同时保留 SOAPAction 头兼容老网关
            conn.setRequestProperty("Content-Type", contentType(soapVersion, encoding) + "; action=\"" + soapAction + "\"");
        }
        conn.setRequestProperty("SOAPAction", "\"" + soapAction + "\"");
        byte[] payload = soap.getBytes(encoding);
        conn.setRequestProperty("Content-Length", String.valueOf(payload.length));
        OutputStream os = null;
        InputStream in = null;
        try {
            os = conn.getOutputStream();
            os.write(payload);
            os.flush();
            int code = conn.getResponseCode();
            if (code >= 400) {
                in = conn.getErrorStream();
            } else {
                in = conn.getInputStream();
            }
            String body = readAll(in, encoding);
            if (code >= 400) {
                throw new RuntimeException("HTTP " + code + " " + conn.getResponseMessage() + "\n" + body);
            }
            return body;
        } finally {
            if (os != null) {
                try { os.close(); } catch (Exception ignore) {}
            }
            if (in != null) {
                try { in.close(); } catch (Exception ignore) {}
            }
            conn.disconnect();
        }
    }

    private static String contentType(String soapVersion, String encoding) {
        if ("1.2".equals(soapVersion)) {
            return "application/soap+xml; charset=" + encoding;
        }
        return "text/xml; charset=" + encoding;
    }

    private static String readAll(InputStream in, String encoding) throws Exception {
        if (in == null) {
            return "";
        }
        ByteArrayOutputStream bos = new ByteArrayOutputStream();
        byte[] buf = new byte[4096];
        int n;
        while ((n = in.read(buf)) >= 0) {
            bos.write(buf, 0, n);
        }
        return bos.toString(encoding);
    }
}
`
	return GeneratedFile{RelPath: srcPath(pkg, "SoapTransport"), Content: body, Kind: "java"}
}

func genBean(pkg string, bean beanType) GeneratedFile {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s;\n\n", pkg)
	fmt.Fprintf(&b, "/** SOAP 参数 Bean，字段一律 String，避免 JDK 6/8 JAXB 类型映射差异。 xml=%s */\n", bean.XMLName)
	fmt.Fprintf(&b, "public class %s {\n", bean.ClassName)
	for _, f := range bean.Fields {
		typ := "String"
		if f.ClassName != "" {
			typ = f.ClassName
		}
		if f.Repeated {
			fmt.Fprintf(&b, "    /** xsd=%s maxOccurs=unbounded */\n", f.XSDType)
			fmt.Fprintf(&b, "    public java.util.List %s = new java.util.ArrayList();\n", f.FieldName)
		} else {
			fmt.Fprintf(&b, "    /** xml=%s xsd=%s */\n", f.XMLName, f.XSDType)
			fmt.Fprintf(&b, "    public %s %s;\n", typ, f.FieldName)
		}
	}
	b.WriteString("\n    public String toXml() {\n")
	b.WriteString("        StringBuffer sb = new StringBuffer();\n")
	for _, f := range bean.Fields {
		xml := f.XMLName
		if xml == "" {
			xml = f.FieldName
		}
		if f.Repeated {
			fmt.Fprintf(&b, "        if (this.%s != null) {\n", f.FieldName)
			fmt.Fprintf(&b, "            for (int i = 0; i < this.%s.size(); i++) {\n", f.FieldName)
			fmt.Fprintf(&b, "                Object it = this.%s.get(i);\n", f.FieldName)
			if f.ClassName != "" {
				fmt.Fprintf(&b, "                sb.append(\"<%s>\");\n", xml)
				fmt.Fprintf(&b, "                if (it instanceof %s) { sb.append(((%s) it).toXml()); } else if (it != null) { sb.append(XmlUtil.escape(String.valueOf(it))); }\n", f.ClassName, f.ClassName)
				fmt.Fprintf(&b, "                sb.append(\"</%s>\");\n", xml)
			} else {
				fmt.Fprintf(&b, "                sb.append(\"<%s>\");\n", xml)
				fmt.Fprintf(&b, "                if (it != null) { sb.append(XmlUtil.escape(String.valueOf(it))); }\n")
				fmt.Fprintf(&b, "                sb.append(\"</%s>\");\n", xml)
			}
			b.WriteString("            }\n        }\n")
			continue
		}
		if f.ClassName != "" {
			fmt.Fprintf(&b, "        if (this.%s != null) {\n", f.FieldName)
			fmt.Fprintf(&b, "            sb.append(\"<%s>\");\n", xml)
			fmt.Fprintf(&b, "            sb.append(this.%s.toXml());\n", f.FieldName)
			fmt.Fprintf(&b, "            sb.append(\"</%s>\");\n", xml)
			b.WriteString("        }\n")
		} else {
			fmt.Fprintf(&b, "        sb.append(\"<%s>\");\n", xml)
			fmt.Fprintf(&b, "        sb.append(XmlUtil.escape(this.%s));\n", f.FieldName)
			fmt.Fprintf(&b, "        sb.append(\"</%s>\");\n", xml)
		}
	}
	b.WriteString("        return sb.toString();\n    }\n\n")
	fmt.Fprintf(&b, "    public static %s fromXml(String xml) {\n", bean.ClassName)
	fmt.Fprintf(&b, "        %s o = new %s();\n", bean.ClassName, bean.ClassName)
	for _, f := range bean.Fields {
		xml := f.XMLName
		if xml == "" {
			xml = f.FieldName
		}
		if f.Repeated {
			continue
		}
		if f.ClassName != "" {
			fmt.Fprintf(&b, "        o.%s = %s.fromXml(XmlUtil.firstTag(xml, %s));\n", f.FieldName, f.ClassName, JavaString(xml))
		} else {
			fmt.Fprintf(&b, "        o.%s = XmlUtil.firstTag(xml, %s);\n", f.FieldName, JavaString(xml))
		}
	}
	b.WriteString("        return o;\n    }\n}\n")
	return GeneratedFile{RelPath: srcPath(pkg, bean.ClassName), Content: b.String(), Kind: "java"}
}

func firstEndpoint(m wsModel) string {
	if m.Endpoint != "" {
		return m.Endpoint
	}
	for _, op := range m.ops {
		if op.Endpoint != "" {
			return op.Endpoint
		}
	}
	return ""
}

func genPortableClient(pkg string, m wsModel, javaSrc string, includeMain bool) []GeneratedFile {
	_ = javaSrc
	var b strings.Builder
	fmt.Fprintf(&b, "package %s;\n\n", pkg)
	b.WriteString("/**\n * 纯 JDK SOAP 客户端。无 Axis / XFire / CXF / JAX-WS 依赖。\n")
	fmt.Fprintf(&b, " * 目标源码级别 Java 1.6。默认 endpoint=%s\n */\n", firstEndpoint(m))
	fmt.Fprintf(&b, "public class %s {\n", m.ClassName)
	b.WriteString("    private String endpoint;\n")
	b.WriteString("    private final SoapTransport transport = new SoapTransport();\n\n")
	fmt.Fprintf(&b, "    public %s() {\n        this(%s);\n    }\n\n", m.ClassName, JavaString(firstEndpoint(m)))
	fmt.Fprintf(&b, "    public %s(String endpoint) {\n        this.endpoint = endpoint;\n    }\n\n", m.ClassName)
	b.WriteString("    public void setEndpoint(String endpoint) { this.endpoint = endpoint; }\n")
	b.WriteString("    public void setTimeoutMs(int timeoutMs) { this.transport.setTimeoutMs(timeoutMs); }\n\n")
	b.WriteString("    public String callRaw(String soap, String soapAction, String soapVersion) throws Exception {\n")
	b.WriteString("        return this.transport.post(this.endpoint, soap, soapAction, soapVersion);\n")
	b.WriteString("    }\n")
	if len(m.ops) == 0 {
		b.WriteString("}\n")
	} else {
		for _, op := range m.ops {
			writeOpMethod(&b, op)
		}
		b.WriteString("}\n")
	}
	files := []GeneratedFile{{RelPath: srcPath(pkg, m.ClassName), Content: b.String(), Kind: "java"}}
	if includeMain {
		files = append(files, genMain(pkg, m.ClassName, m))
	}
	return files
}

func writeOpMethod(b *strings.Builder, op opModel) {
	ret := "String"
	if op.OutputBean != "" {
		ret = op.OutputBean
	}
	arg := ""
	argUse := "\"\""
	if op.InputBean != "" {
		arg = op.InputBean + " req"
		argUse = "req == null ? \"\" : req.toXml()"
	}
	fmt.Fprintf(b, "\n    /** %s SOAP %s action=%s */\n", op.Name, op.SOAPVer, op.SOAPAction)
	fmt.Fprintf(b, "    public %s %s(%s) throws Exception {\n", ret, op.MethodName, arg)
	fmt.Fprintf(b, "        String inner = %s;\n", argUse)
	fmt.Fprintf(b, "        String soap = SoapTransport.envelope(%s, %s, %s, inner);\n",
		JavaString(op.SOAPVer), JavaString(op.Namespace), JavaString(op.InputXML))
	if op.Endpoint != "" {
		fmt.Fprintf(b, "        String ep = this.endpoint;\n")
		fmt.Fprintf(b, "        if (ep == null || ep.length() == 0) { ep = %s; }\n", JavaString(op.Endpoint))
		fmt.Fprintf(b, "        String resp = this.transport.post(ep, soap, %s, %s);\n", JavaString(op.SOAPAction), JavaString(op.SOAPVer))
	} else {
		fmt.Fprintf(b, "        String resp = this.transport.post(this.endpoint, soap, %s, %s);\n", JavaString(op.SOAPAction), JavaString(op.SOAPVer))
	}
	if op.OutputBean != "" {
		fmt.Fprintf(b, "        return %s.fromXml(resp);\n", op.OutputBean)
	} else {
		b.WriteString("        return resp;\n")
	}
	b.WriteString("    }\n")
}

func genMain(pkg, className string, m wsModel) GeneratedFile {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s;\n\n", pkg)
	fmt.Fprintf(&b, "/** 用法示例。把 TODO 填上再运行。 */\n")
	fmt.Fprintf(&b, "public class %sMain {\n", className)
	b.WriteString("    public static void main(String[] args) throws Exception {\n")
	fmt.Fprintf(&b, "        %s client = new %s();\n", className, className)
	b.WriteString("        client.setTimeoutMs(30000);\n")
	if len(m.ops) > 0 {
		op := m.ops[0]
		if op.InputBean != "" {
			fmt.Fprintf(&b, "        %s req = new %s();\n", op.InputBean, op.InputBean)
			if op.Input != nil {
				for _, f := range op.Input.Fields {
					if f.ClassName == "" && !f.Repeated {
						fmt.Fprintf(&b, "        req.%s = \"TODO\";\n", f.FieldName)
					}
				}
			}
			fmt.Fprintf(&b, "        Object resp = client.%s(req);\n", op.MethodName)
			b.WriteString("        System.out.println(resp);\n")
		} else {
			b.WriteString("        String resp = client.callRaw(\"<soapenv:Envelope/>\", \"\", \"1.1\");\n")
			b.WriteString("        System.out.println(resp);\n")
		}
	} else {
		b.WriteString("        System.out.println(\"no operations parsed\");\n")
	}
	b.WriteString("    }\n}\n")
	return GeneratedFile{RelPath: srcPath(pkg, className+"Main"), Content: b.String(), Kind: "java"}
}

func genSEI(pkg string, m wsModel) GeneratedFile {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s;\n\n", pkg)
	b.WriteString("import javax.jws.WebMethod;\nimport javax.jws.WebParam;\nimport javax.jws.WebResult;\nimport javax.jws.WebService;\nimport javax.jws.soap.SOAPBinding;\n\n")
	fmt.Fprintf(&b, "@WebService(name = %s, targetNamespace = %s)\n", JavaString(m.ServiceName), JavaString(m.Namespace))
	b.WriteString("@SOAPBinding(style = SOAPBinding.Style.DOCUMENT, use = SOAPBinding.Use.LITERAL, parameterStyle = SOAPBinding.ParameterStyle.BARE)\n")
	fmt.Fprintf(&b, "public interface %sPortType {\n", JavaClassName(m.ServiceName))
	for _, op := range m.ops {
		in := "String"
		out := "String"
		if op.InputBean != "" {
			in = op.InputBean
		}
		if op.OutputBean != "" {
			out = op.OutputBean
		}
		fmt.Fprintf(&b, "    @WebMethod(operationName = %s, action = %s)\n", JavaString(op.Name), JavaString(op.SOAPAction))
		fmt.Fprintf(&b, "    @WebResult(name = %s, targetNamespace = %s)\n", JavaString(op.OutputXML), JavaString(op.Namespace))
		fmt.Fprintf(&b, "    %s %s(@WebParam(name = %s, targetNamespace = %s) %s req);\n\n",
			out, op.MethodName, JavaString(op.InputXML), JavaString(op.Namespace), in)
	}
	b.WriteString("}\n")
	return GeneratedFile{RelPath: srcPath(pkg, JavaClassName(m.ServiceName)+"PortType"), Content: b.String(), Kind: "java"}
}

func genJAXWSClient(pkg string, m wsModel, javaSrc string, includeMain bool, jaxwsTarget string) []GeneratedFile {
	_ = javaSrc
	if jaxwsTarget != JAXWSTarget22 {
		jaxwsTarget = JAXWSTarget21
	}
	files := []GeneratedFile{genSEI(pkg, m)}
	var b strings.Builder
	fmt.Fprintf(&b, "package %s;\n\n", pkg)
	b.WriteString("import java.net.URL;\n")
	b.WriteString("import javax.xml.namespace.QName;\n")
	b.WriteString("import javax.xml.soap.MessageFactory;\n")
	b.WriteString("import javax.xml.soap.SOAPMessage;\n")
	b.WriteString("import javax.xml.ws.BindingProvider;\n")
	b.WriteString("import javax.xml.ws.Dispatch;\n")
	b.WriteString("import javax.xml.ws.Service;\n")
	b.WriteString("import javax.xml.ws.soap.SOAPBinding;\n")
	b.WriteString("import java.io.ByteArrayInputStream;\n")
	b.WriteString("import java.io.ByteArrayOutputStream;\n\n")
	b.WriteString("/**\n * JAX-WS Dispatch 客户端（MESSAGE 模式）。\n")
	fmt.Fprintf(&b, " * 目标 JAX-WS %s：JDK 6 请保持 2.1，JDK 8 可用 2.2。不依赖 wsimport 生成的 Service 子类。\n */\n", jaxwsTarget)
	fmt.Fprintf(&b, "public class %s {\n", m.ClassName)
	b.WriteString("    private int timeoutMs = 30000;\n")
	b.WriteString("    private String endpoint;\n    private final SoapTransport fallback = new SoapTransport();\n\n")
	fmt.Fprintf(&b, "    public %s() { this(%s); }\n", m.ClassName, JavaString(firstEndpoint(m)))
	fmt.Fprintf(&b, "    public %s(String endpoint) { this.endpoint = endpoint; }\n\n", m.ClassName)
	b.WriteString("    public void setEndpoint(String endpoint) { this.endpoint = endpoint; }\n\n")
	b.WriteString("    public String callRaw(String soap, String soapAction, String soapVersion) throws Exception {\n")
	b.WriteString("        try {\n")
	b.WriteString("            String binding = \"1.2\".equals(soapVersion) ? SOAPBinding.SOAP12HTTP_BINDING : SOAPBinding.SOAP11HTTP_BINDING;\n")
	fmt.Fprintf(&b, "            QName svcName = new QName(%s, %s);\n", JavaString(m.Namespace), JavaString(m.ServiceName))
	b.WriteString("            QName portName = new QName(svcName.getNamespaceURI(), svcName.getLocalPart() + \"Port\");\n")
	b.WriteString("            Service svc = Service.create(svcName);\n")
	b.WriteString("            svc.addPort(portName, binding, this.endpoint);\n")
	b.WriteString("            Dispatch dispatch = svc.createDispatch(portName, SOAPMessage.class, Service.Mode.MESSAGE);\n")
	b.WriteString("            dispatch.getRequestContext().put(\"com.sun.xml.internal.ws.connect.timeout\", Integer.valueOf(this.timeoutMs));\n")
	b.WriteString("            dispatch.getRequestContext().put(\"com.sun.xml.internal.ws.request.timeout\", Integer.valueOf(this.timeoutMs));\n")
	b.WriteString("            dispatch.getRequestContext().put(\"com.sun.xml.ws.connect.timeout\", Integer.valueOf(this.timeoutMs));\n")
	b.WriteString("            dispatch.getRequestContext().put(\"com.sun.xml.ws.request.timeout\", Integer.valueOf(this.timeoutMs));\n")
	b.WriteString("            if (soapAction != null) {\n")
	b.WriteString("                dispatch.getRequestContext().put(BindingProvider.SOAPACTION_USE_PROPERTY, Boolean.TRUE);\n")
	b.WriteString("                dispatch.getRequestContext().put(BindingProvider.SOAPACTION_URI_PROPERTY, soapAction);\n")
	b.WriteString("            }\n")
	b.WriteString("            MessageFactory mf = MessageFactory.newInstance();\n")
	b.WriteString("            SOAPMessage req = mf.createMessage(null, new ByteArrayInputStream(soap.getBytes(\"UTF-8\")));\n")
	b.WriteString("            SOAPMessage resp = (SOAPMessage) dispatch.invoke(req);\n")
	b.WriteString("            ByteArrayOutputStream bos = new ByteArrayOutputStream();\n")
	b.WriteString("            resp.writeTo(bos);\n")
	b.WriteString("            return bos.toString(\"UTF-8\");\n")
	b.WriteString("        } catch (Throwable jaxwsFailed) {\n")
	b.WriteString("            // JDK 11+ 无 javax.xml.ws 时回退到 HttpURLConnection\n")
	b.WriteString("            return this.fallback.post(this.endpoint, soap, soapAction, soapVersion);\n")
	b.WriteString("        }\n    }\n")
	b.WriteString("    public void setTimeoutMs(int timeoutMs) { this.timeoutMs = timeoutMs; this.fallback.setTimeoutMs(timeoutMs); }\n")
	for _, op := range m.ops {
		writeOpMethodDispatch(&b, op)
	}
	b.WriteString("}\n")
	files = append(files, GeneratedFile{RelPath: srcPath(pkg, m.ClassName), Content: b.String(), Kind: "java"})
	if includeMain {
		files = append(files, genMain(pkg, m.ClassName, m))
	}
	return files
}

func writeOpMethodDispatch(b *strings.Builder, op opModel) {
	ret := "String"
	if op.OutputBean != "" {
		ret = op.OutputBean
	}
	arg := ""
	argUse := "\"\""
	if op.InputBean != "" {
		arg = op.InputBean + " req"
		argUse = "req == null ? \"\" : req.toXml()"
	}
	fmt.Fprintf(b, "\n    public %s %s(%s) throws Exception {\n", ret, op.MethodName, arg)
	fmt.Fprintf(b, "        String soap = SoapTransport.envelope(%s, %s, %s, %s);\n",
		JavaString(op.SOAPVer), JavaString(op.Namespace), JavaString(op.InputXML), argUse)
	fmt.Fprintf(b, "        String resp = callRaw(soap, %s, %s);\n", JavaString(op.SOAPAction), JavaString(op.SOAPVer))
	if op.OutputBean != "" {
		fmt.Fprintf(b, "        return %s.fromXml(resp);\n", op.OutputBean)
	} else {
		b.WriteString("        return resp;\n")
	}
	b.WriteString("    }\n")
}

func genCXFClient(pkg string, m wsModel, javaSrc string, includeMain bool) []GeneratedFile {
	_ = javaSrc
	files := []GeneratedFile{genSEI(pkg, m)}
	port := JavaClassName(m.ServiceName) + "PortType"
	var b strings.Builder
	fmt.Fprintf(&b, "package %s;\n\n", pkg)
	b.WriteString("/**\n * Apache CXF 客户端：JaxWsProxyFactoryBean + 上面的 SEI。\n")
	b.WriteString(" * 请把工程里的 cxf-core / cxf-rt-frontend-jaxws / cxf-rt-transports-http 放进 classpath。\n")
	b.WriteString(" * CXF 2.x → JDK 6；CXF 3.x → JDK 8+。版本必须和工程一致。\n */\n")
	fmt.Fprintf(&b, "public class %s {\n", m.ClassName)
	b.WriteString("    private String endpoint;\n")
	b.WriteString("    private final SoapTransport fallback = new SoapTransport();\n\n")
	b.WriteString("    public void setTimeoutMs(int timeoutMs) { this.fallback.setTimeoutMs(timeoutMs); }\n")
	fmt.Fprintf(&b, "    public %s() { this(%s); }\n", m.ClassName, JavaString(firstEndpoint(m)))
	fmt.Fprintf(&b, "    public %s(String endpoint) { this.endpoint = endpoint; }\n\n", m.ClassName)
	b.WriteString("    public void setEndpoint(String endpoint) { this.endpoint = endpoint; }\n\n")
	fmt.Fprintf(&b, "    public %s createPort() {\n", port)
	b.WriteString("        org.apache.cxf.jaxws.JaxWsProxyFactoryBean factory = new org.apache.cxf.jaxws.JaxWsProxyFactoryBean();\n")
	fmt.Fprintf(&b, "        factory.setServiceClass(%s.class);\n", port)
	b.WriteString("        factory.setAddress(this.endpoint);\n")
	fmt.Fprintf(&b, "        return (%s) factory.create();\n", port)
	b.WriteString("    }\n\n")
	b.WriteString("    public String callRaw(String soap, String soapAction, String soapVersion) throws Exception {\n")
	b.WriteString("        return this.fallback.post(this.endpoint, soap, soapAction, soapVersion);\n")
	b.WriteString("    }\n")
	for _, op := range m.ops {
		writeOpMethodDispatch(&b, op)
	}
	b.WriteString("}\n")
	files = append(files, GeneratedFile{RelPath: srcPath(pkg, m.ClassName), Content: b.String(), Kind: "java"})
	if includeMain {
		files = append(files, genMain(pkg, m.ClassName, m))
	}
	return files
}

func genAxis1Client(pkg string, m wsModel, javaSrc string, includeMain bool) []GeneratedFile {
	_ = javaSrc
	var b strings.Builder
	fmt.Fprintf(&b, "package %s;\n\n", pkg)
	b.WriteString("import java.net.URL;\n")
	b.WriteString("import org.apache.axis.client.Call;\n")
	b.WriteString("import org.apache.axis.client.Service;\n")
	b.WriteString("import org.apache.axis.Message;\n\n")
	b.WriteString("/**\n * Apache Axis 1.4 Call 客户端。不生成 stub，避免 WSDL2Java 与工程里 axis.jar 版本对不上。\n")
	b.WriteString(" * 需要：axis.jar, jaxrpc.jar, saaj.jar, commons-logging, commons-discovery, wsdl4j。\n */\n")
	fmt.Fprintf(&b, "public class %s {\n", m.ClassName)
	b.WriteString("    private String endpoint;\n    private int timeoutMs = 30000;\n")
	b.WriteString("    private final SoapTransport fallback = new SoapTransport();\n\n")
	fmt.Fprintf(&b, "    public %s() { this(%s); }\n", m.ClassName, JavaString(firstEndpoint(m)))
	fmt.Fprintf(&b, "    public %s(String endpoint) { this.endpoint = endpoint; }\n\n", m.ClassName)
	b.WriteString("    public void setEndpoint(String endpoint) { this.endpoint = endpoint; }\n")
	b.WriteString("    public void setTimeoutMs(int timeoutMs) { this.timeoutMs = timeoutMs; this.fallback.setTimeoutMs(timeoutMs); }\n\n")
	b.WriteString("    public String callRaw(String soap, String soapAction, String soapVersion) throws Exception {\n")
	b.WriteString("        try {\n")
	b.WriteString("            Service service = new Service();\n")
	b.WriteString("            Call call = (Call) service.createCall();\n")
	b.WriteString("            call.setTargetEndpointAddress(new URL(this.endpoint));\n")
	b.WriteString("            call.setTimeout(new Integer(this.timeoutMs));\n")
	b.WriteString("            if (soapAction != null) {\n")
	b.WriteString("                call.setUseSOAPAction(true);\n")
	b.WriteString("                call.setSOAPActionURI(soapAction);\n")
	b.WriteString("            }\n")
	b.WriteString("            Message req = new Message(soap);\n")
	b.WriteString("            call.setRequestMessage(req);\n")
	b.WriteString("            call.invoke();\n")
	b.WriteString("            Message resp = call.getResponseMessage();\n")
	b.WriteString("            return resp.getSOAPPartAsString();\n")
	b.WriteString("        } catch (Throwable axisFailed) {\n")
	b.WriteString("            return this.fallback.post(this.endpoint, soap, soapAction, soapVersion);\n")
	b.WriteString("        }\n    }\n")
	for _, op := range m.ops {
		writeOpMethodDispatch(&b, op)
	}
	b.WriteString("}\n")
	files := []GeneratedFile{{RelPath: srcPath(pkg, m.ClassName), Content: b.String(), Kind: "java"}}
	if includeMain {
		files = append(files, genMain(pkg, m.ClassName, m))
	}
	return files
}

func genAxis2Client(pkg string, m wsModel, javaSrc string, includeMain bool) []GeneratedFile {
	_ = javaSrc
	var b strings.Builder
	fmt.Fprintf(&b, "package %s;\n\n", pkg)
	b.WriteString("import org.apache.axiom.om.OMElement;\n")
	b.WriteString("import org.apache.axiom.om.util.AXIOMUtil;\n")
	b.WriteString("import org.apache.axis2.addressing.EndpointReference;\n")
	b.WriteString("import org.apache.axis2.client.Options;\n")
	b.WriteString("import org.apache.axis2.client.ServiceClient;\n\n")
	b.WriteString("/**\n * Apache Axis2 ServiceClient + 裸 SOAP。避开 ADB/XMLBeans/JAXB databinding 版本坑。\n */\n")
	fmt.Fprintf(&b, "public class %s {\n", m.ClassName)
	b.WriteString("    private String endpoint;\n    private int timeoutMs = 30000;\n")
	b.WriteString("    private final SoapTransport fallback = new SoapTransport();\n\n")
	fmt.Fprintf(&b, "    public %s() { this(%s); }\n", m.ClassName, JavaString(firstEndpoint(m)))
	fmt.Fprintf(&b, "    public %s(String endpoint) { this.endpoint = endpoint; }\n\n", m.ClassName)
	b.WriteString("    public void setEndpoint(String endpoint) { this.endpoint = endpoint; }\n")
	b.WriteString("    public void setTimeoutMs(int timeoutMs) { this.timeoutMs = timeoutMs; this.fallback.setTimeoutMs(timeoutMs); }\n\n")
	b.WriteString("    public String callRaw(String soap, String soapAction, String soapVersion) throws Exception {\n")
	b.WriteString("        ServiceClient client = null;\n")
	b.WriteString("        try {\n")
	b.WriteString("            client = new ServiceClient();\n")
	b.WriteString("            Options options = new Options();\n")
	b.WriteString("            options.setTo(new EndpointReference(this.endpoint));\n")
	b.WriteString("            options.setAction(soapAction);\n")
	b.WriteString("            options.setTimeOutInMilliSeconds(this.timeoutMs);\n")
	b.WriteString("            client.setOptions(options);\n")
	b.WriteString("            OMElement payload = AXIOMUtil.stringToOM(soap);\n")
	b.WriteString("            OMElement resp = client.sendReceive(payload);\n")
	b.WriteString("            return resp == null ? \"\" : resp.toString();\n")
	b.WriteString("        } catch (Throwable axis2Failed) {\n")
	b.WriteString("            return this.fallback.post(this.endpoint, soap, soapAction, soapVersion);\n")
	b.WriteString("        } finally {\n")
	b.WriteString("            if (client != null) {\n")
	b.WriteString("                try { client.cleanupTransport(); } catch (Exception ignore) {}\n")
	b.WriteString("                try { client.cleanup(); } catch (Exception ignore) {}\n")
	b.WriteString("            }\n")
	b.WriteString("        }\n    }\n")
	for _, op := range m.ops {
		writeOpMethodDispatch(&b, op)
	}
	b.WriteString("}\n")
	files := []GeneratedFile{{RelPath: srcPath(pkg, m.ClassName), Content: b.String(), Kind: "java"}}
	if includeMain {
		files = append(files, genMain(pkg, m.ClassName, m))
	}
	return files
}

func genXFireClient(pkg string, m wsModel, javaSrc string, includeMain bool, resolved resolvedWSDL) []GeneratedFile {
	_ = javaSrc
	wsdlLoc := strings.TrimSpace(resolved.URL)
	if wsdlLoc == "" && resolved.FilePath != "" {
		wsdlLoc = fileURI(resolved.FilePath)
	} else if wsdlLoc != "" && !strings.Contains(wsdlLoc, "://") {
		wsdlLoc = fileURI(wsdlLoc)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "package %s;\n\n", pkg)
	b.WriteString("import java.net.URL;\n")
	b.WriteString("import org.codehaus.xfire.client.Client;\n\n")
	b.WriteString("/**\n * Codehaus XFire 1.2 动态客户端。构造时需要能读到 WSDL（URL 或 file:）。\n")
	b.WriteString(" * 需要 xfire-all-1.2.6 + wsdl4j + stax/wstx。不要拿 CXF 的包名来编译这份代码。\n */\n")
	fmt.Fprintf(&b, "public class %s {\n", m.ClassName)
	b.WriteString("    private int timeoutMs = 30000;\n")
	b.WriteString("    private String endpoint;\n    private String wsdlLocation;\n")
	b.WriteString("    private final SoapTransport fallback = new SoapTransport();\n\n")
	fmt.Fprintf(&b, "    public %s() { this(%s, %s); }\n", m.ClassName, JavaString(firstEndpoint(m)), JavaString(wsdlLoc))
	fmt.Fprintf(&b, "    public %s(String endpoint, String wsdlLocation) {\n        this.endpoint = endpoint;\n        this.wsdlLocation = wsdlLocation;\n    }\n\n", m.ClassName)
	b.WriteString("    public void setEndpoint(String endpoint) { this.endpoint = endpoint; }\n")
	b.WriteString("    public void setWsdlLocation(String wsdlLocation) { this.wsdlLocation = wsdlLocation; }\n\n")
	b.WriteString("    public void setTimeoutMs(int timeoutMs) { this.timeoutMs = timeoutMs; this.fallback.setTimeoutMs(timeoutMs); }\n")
	b.WriteString("    public Object[] invoke(String operation, Object[] params) throws Exception {\n")
	b.WriteString("        // Keep the WSDL system ID so relative XSD imports resolve beside the WSDL.\n")
	b.WriteString("        javax.wsdl.xml.WSDLReader reader = javax.wsdl.factory.WSDLFactory.newInstance().newWSDLReader();\n")
	b.WriteString("        javax.wsdl.Definition definition = reader.readWSDL(this.wsdlLocation);\n")
	b.WriteString("        Client client = new Client(definition, null);\n")
	b.WriteString("        client.setTimeout(this.timeoutMs);\n")
	b.WriteString("        if (this.endpoint != null && this.endpoint.length() > 0) {\n")
	b.WriteString("            client.setUrl(this.endpoint);\n")
	b.WriteString("        }\n")
	b.WriteString("        return client.invoke(operation, params);\n")
	b.WriteString("    }\n\n")
	b.WriteString("    public String callRaw(String soap, String soapAction, String soapVersion) throws Exception {\n")
	b.WriteString("        return this.fallback.post(this.endpoint, soap, soapAction, soapVersion);\n")
	b.WriteString("    }\n")
	for _, op := range m.ops {
		writeXFireOp(&b, op)
	}
	b.WriteString("}\n")
	files := []GeneratedFile{{RelPath: srcPath(pkg, m.ClassName), Content: b.String(), Kind: "java"}}
	if includeMain {
		files = append(files, genMain(pkg, m.ClassName, m))
	}
	return files
}

func writeXFireOp(b *strings.Builder, op opModel) {
	ret := "String"
	if op.OutputBean != "" {
		ret = op.OutputBean
	}
	arg := ""
	if op.InputBean != "" {
		arg = op.InputBean + " req"
	}
	fmt.Fprintf(b, "\n    public %s %s(%s) throws Exception {\n", ret, op.MethodName, arg)
	if op.Input != nil && op.InputBean != "" {
		b.WriteString("        java.util.ArrayList args = new java.util.ArrayList();\n")
		for _, f := range op.Input.Fields {
			fmt.Fprintf(b, "        args.add(req == null ? null : req.%s);\n", f.FieldName)
		}
		fmt.Fprintf(b, "        Object[] result = invoke(%s, args.toArray());\n", JavaString(op.Name))
		b.WriteString("        if (result == null || result.length == 0 || result[0] == null) {\n")
		if op.OutputBean != "" {
			fmt.Fprintf(b, "            return %s.fromXml(callRaw(SoapTransport.envelope(%s, %s, %s, req == null ? \"\" : req.toXml()), %s, %s));\n",
				op.OutputBean, JavaString(op.SOAPVer), JavaString(op.Namespace), JavaString(op.InputXML), JavaString(op.SOAPAction), JavaString(op.SOAPVer))
		} else {
			b.WriteString("            return \"\";\n")
		}
		b.WriteString("        }\n")
		if op.OutputBean != "" {
			fmt.Fprintf(b, "        return %s.fromXml(String.valueOf(result[0]));\n", op.OutputBean)
		} else {
			b.WriteString("        return String.valueOf(result[0]);\n")
		}
	} else {
		fmt.Fprintf(b, "        String soap = SoapTransport.envelope(%s, %s, %s, \"\");\n",
			JavaString(op.SOAPVer), JavaString(op.Namespace), JavaString(op.InputXML))
		fmt.Fprintf(b, "        String resp = callRaw(soap, %s, %s);\n", JavaString(op.SOAPAction), JavaString(op.SOAPVer))
		if op.OutputBean != "" {
			fmt.Fprintf(b, "        return %s.fromXml(resp);\n", op.OutputBean)
		} else {
			b.WriteString("        return resp;\n")
		}
	}
	b.WriteString("    }\n")
}

func genReadme(pkg string, req Request, m wsModel, resolved resolvedWSDL) GeneratedFile {
	prof, _ := profileByID(req.Engine)
	if req.Engine == "" {
		prof, _ = profileByID(EnginePortable)
	}
	var b strings.Builder
	b.WriteString("Kairo WebService Java 代码生成说明\n")
	b.WriteString("================================\n\n")
	fmt.Fprintf(&b, "引擎: %s (%s)\n模式: %s\n包名: %s\n源码级别: %s\n", prof.Name, req.Engine, req.Mode, pkg, req.JavaSource)
	if req.Engine == EngineJAXWS {
		fmt.Fprintf(&b, "JAX-WS target: %s\n", req.JAXWSTarget)
	}
	fmt.Fprintf(&b, "服务: %s\n默认 endpoint: %s\n\n", m.ServiceName, firstEndpoint(m))
	b.WriteString("为什么不直接用本机默认 JDK？\n")
	b.WriteString("- JDK 8 wsimport 默认 JAX-WS 2.2，JDK 6 工程会缺类。\n")
	b.WriteString("- JDK 11+ 没有 wsimport / javax.xml.ws。\n")
	b.WriteString("- Axis / XFire / CXF 必须用工程里那一套 jar 生成，否则 import 对不上。\n\n")
	b.WriteString("建议配置\n")
	b.WriteString("--------\n")
	for _, n := range prof.CompileNotes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	for _, n := range prof.RuntimeNotes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	if len(prof.RequiredJars) > 0 {
		b.WriteString("\n需要放到工程 classpath 的 jar:\n")
		for _, j := range prof.RequiredJars {
			fmt.Fprintf(&b, "  - %s\n", j)
		}
	}
	b.WriteString("\n编译示例（Java 6）:\n")
	b.WriteString("  javac -source 1.6 -target 1.6 -encoding UTF-8 " + packageToPath(pkg) + "/*.java\n")
	if resolved.FilePath != "" {
		fmt.Fprintf(&b, "\nWSDL 文件: %s\n", resolved.FilePath)
	}
	if resolved.URL != "" {
		fmt.Fprintf(&b, "WSDL URL: %s\n", resolved.URL)
	}
	b.WriteString("\n内置生成器会附带 SoapTransport 回退路径：对应栈的 jar 缺失时仍可用 HttpURLConnection 发 SOAP。\n")
	return GeneratedFile{RelPath: "README-kairo-ws.txt", Content: b.String(), Kind: "txt"}
}
