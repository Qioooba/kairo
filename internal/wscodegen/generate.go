package wscodegen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kairo/internal/webservice"
)

var errNoWSDL = errors.New("请提供 WSDL：已导入项目 / 本地文件 / URL / 粘贴内容 四选一")

// ResolveWSDL 把请求里的四种来源收成已解析的 WSDL。store 可为 nil。
func ResolveWSDL(req Request, store *webservice.Store) (resolvedWSDL, error) {
	var out resolvedWSDL
	if id := strings.TrimSpace(req.WSDLProjectID); id != "" && store != nil {
		p, ok, err := store.GetProject(id)
		if err != nil {
			return out, err
		}
		if !ok {
			return out, fmt.Errorf("WSDL 项目不存在: %s", id)
		}
		out.Project = p
		out.Raw = p.RawWSDL
		out.URL = p.SourceURL
		return out, nil
	}
	if f := strings.TrimSpace(req.WSDLFile); f != "" {
		decoded, err := readXMLFile(f)
		if err != nil {
			return out, fmt.Errorf("读取 WSDL 文件失败: %w", err)
		}
		atts := loadLocalXSDs(f)
		var p *webservice.WSDLProject
		if len(atts) > 0 {
			p = webservice.ParseWSDL(decoded, atts)
		} else {
			p = webservice.ParseWSDL(decoded)
		}
		p.Source = "file"
		out.Project = p
		out.Raw = p.RawWSDL
		out.FilePath = f
		out.SiblingXSDCount = len(atts)
		return out, nil
	}
	if u := strings.TrimSpace(req.WSDLURL); u != "" {
		out.URL = u
	}
	if c := strings.TrimSpace(req.WSDLContent); c != "" {
		var p *webservice.WSDLProject
		if out.URL != "" {
			// 相对 schemaLocation 要从 WSDL URL 解析，否则恒力这类拆 XSD 的服务参数树是空的。
			p = webservice.ParseWSDL(c, out.URL)
			p.Source = "url"
			p.SourceURL = out.URL
		} else {
			p = webservice.ParseWSDL(c)
			p.Source = "upload"
		}
		out.Project = p
		out.Raw = p.RawWSDL
		return out, nil
	}
	if out.URL != "" {
		// 官方工具可以直接吃 URL；内置模式需要文本，由 handler 先拉取。
		return out, nil
	}
	return out, errNoWSDL
}

// Generate 根据请求生成 Java 代码。store 仅在使用已导入项目时需要。
func Generate(req Request, store *webservice.Store) (Result, error) {
	res := Result{Engine: strings.TrimSpace(req.Engine), Mode: strings.TrimSpace(req.Mode)}
	if res.Engine == "" {
		res.Engine = EnginePortable
	}
	if _, ok := profileByID(res.Engine); !ok {
		return res, fmt.Errorf("不支持的引擎 %s", res.Engine)
	}
	if res.Mode == "" {
		res.Mode = ModeBuiltin
	}
	if res.Mode != ModeBuiltin && res.Mode != ModeTool {
		return res, fmt.Errorf("mode 只能是 builtin 或 tool")
	}
	resolved, err := ResolveWSDL(req, store)
	if err != nil {
		return res, err
	}
	if res.Mode == ModeBuiltin && resolved.Project == nil {
		return res, fmt.Errorf("内置生成需要 WSDL 文本。请先导入项目、粘贴内容、选本地文件，或让服务端拉取 URL")
	}
	if req.PackageName == "" && resolved.Project != nil {
		req.PackageName = PackageFromNamespace(resolved.Project.TargetNS)
	}
	if req.PackageName == "" {
		req.PackageName = "com.example.ws"
	}
	req.PackageName = JavaPackage(req.PackageName)
	res.PackageName = req.PackageName

	switch res.Mode {
	case ModeBuiltin:
		files, warns, err := generateBuiltin(req, resolved)
		if err != nil {
			return res, err
		}
		res.Files = files
		res.Warnings = warns
		res.Notes = compatibilityNotes(req, resolved)
	case ModeTool:
		outDir := strings.TrimSpace(req.OutputDir)
		if outDir == "" {
			return res, fmt.Errorf("官方工具模式必须指定输出目录")
		}
		abs, err := filepath.Abs(outDir)
		if err != nil {
			return res, err
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return res, fmt.Errorf("创建输出目录失败: %w", err)
		}
		plan, err := planTool(req, resolved, abs)
		if err != nil {
			return res, err
		}
		res.Command = formatCommand(plan)
		if req.DryRun {
			res.OK = true
			res.OutputDir = abs
			res.Notes = append(compatibilityNotes(req, resolved), "dry-run：未真正执行官方工具")
			return res, nil
		}
		logText, err := runTool(plan)
		res.ToolLog = trimLog(logText, 8000)
		if err != nil {
			return res, err
		}
		files, written, err := collectWrittenJava(abs)
		if err != nil {
			return res, err
		}
		res.Files = files
		res.Written = written
		res.OutputDir = abs
		res.Notes = compatibilityNotes(req, resolved)
	}

	if !req.DryRun && res.Mode == ModeBuiltin {
		outDir := strings.TrimSpace(req.OutputDir)
		if outDir == "" {
			return res, fmt.Errorf("请指定输出目录")
		}
		abs, err := filepath.Abs(outDir)
		if err != nil {
			return res, err
		}
		written, err := writeFiles(abs, res.Files, req.Overwrite)
		if err != nil {
			return res, err
		}
		res.Written = written
		res.OutputDir = abs
	}
	if req.DryRun && res.Mode == ModeBuiltin {
		res.Notes = append(res.Notes, "预览模式：未写入磁盘")
	}
	if resolved.SiblingXSDCount > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("已从 WSDL 同目录加载 %d 个 XSD。", resolved.SiblingXSDCount))
	}
	if w := missingExternalXSDWarning(resolved.Project); w != "" {
		res.Warnings = append(res.Warnings, w)
	}
	res.OK = true
	return res, nil
}

func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slashed := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return "file://" + slashed
}

func readXMLFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	decoded, err := webservice.DecodeXMLBytes(data, "")
	if err != nil {
		return string(data), nil
	}
	return decoded, nil
}

// loadLocalXSDs 把 WSDL 同目录（及一层子目录）的 .xsd 按文件名收成附件，
// 给 ParseWSDL 展开相对 schemaLocation。恒力 WSDL 的 Core.xsd / esb.xsd 就靠这个。
func loadLocalXSDs(wsdlPath string) map[string]string {
	dir := filepath.Dir(wsdlPath)
	atts := map[string]string{}
	add := func(path string) {
		if !strings.EqualFold(filepath.Ext(path), ".xsd") {
			return
		}
		text, err := readXMLFile(path)
		if err != nil || strings.TrimSpace(text) == "" {
			return
		}
		atts[filepath.Base(path)] = text
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return atts
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			subs, err := os.ReadDir(p)
			if err != nil {
				continue
			}
			for _, se := range subs {
				if se.IsDir() {
					continue
				}
				add(filepath.Join(p, se.Name()))
			}
			continue
		}
		add(p)
	}
	return atts
}

func missingExternalXSDWarning(p *webservice.WSDLProject) string {
	if p == nil || !strings.Contains(p.RawWSDL, "schemaLocation") {
		return ""
	}
	if len(p.Operations) == 0 {
		return ""
	}
	nested := false
	var walk func(params []webservice.Param)
	walk = func(params []webservice.Param) {
		for _, x := range params {
			if len(x.Children) > 0 {
				nested = true
				return
			}
		}
	}
	for _, op := range p.Operations {
		walk(op.InputParams)
		walk(op.OutputParams)
		if nested {
			return ""
		}
	}
	return "WSDL 引用了外部 XSD，但参数树是空的。请把 .xsd 放在 WSDL 同目录后选「本地文件」，或先在 WebService 页把 WSDL+XSD 一起导入再选已导入项目。"
}

func writeFiles(root string, files []GeneratedFile, overwrite bool) ([]string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	var written []string
	for _, f := range files {
		rel := filepath.ToSlash(f.RelPath)
		if rel == "" || strings.Contains(rel, "..") || filepath.IsAbs(rel) {
			return written, fmt.Errorf("非法输出路径: %s", f.RelPath)
		}
		dest := filepath.Join(root, filepath.FromSlash(rel))
		if !isInside(root, dest) {
			return written, fmt.Errorf("拒绝写出到输出目录之外: %s", f.RelPath)
		}
		if !overwrite {
			if _, err := os.Stat(dest); err == nil {
				return written, fmt.Errorf("文件已存在（未勾选覆盖）: %s", rel)
			}
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(dest, []byte(f.Content), 0o644); err != nil {
			return written, err
		}
		written = append(written, rel)
	}
	return written, nil
}

func isInside(root, dest string) bool {
	absRoot, err1 := filepath.Abs(root)
	absDest, err2 := filepath.Abs(dest)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absDest)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func compatibilityNotes(req Request, resolved resolvedWSDL) []string {
	var notes []string
	notes = append(notes, "生成 JDK 应 ≤ 目标工程 JDK。JDK 8 默认产物不要直接丢进 JDK 6。")
	switch req.Engine {
	case EngineJAXWS:
		if req.JAXWSTarget == JAXWSTarget21 || req.JavaSource == JavaSource16 {
			notes = append(notes, "已按 JAX-WS 2.1 / Java 1.6 思路生成，适合 JDK 6 工程。")
		}
		if req.Mode == ModeTool {
			notes = append(notes, "官方 wsimport 请使用工程自带的 JDK 6/8，不要用本机 PATH 上的 JDK 17。")
		}
	case EngineXFire:
		notes = append(notes, "XFire 代码必须用工程里的 xfire-all 1.2.x 编译，CXF jar 不能当 XFire 用。")
	case EngineAxis1:
		notes = append(notes, "Axis 1.4 需要 axis.jar + jaxrpc + saaj + commons-discovery。缺一个都会在工程里报 NoClassDefFoundError。")
	case EnginePortable:
		notes = append(notes, "portable 客户端零依赖，javac -source 1.6 -target 1.6 即可。")
	}
	if resolved.Project != nil && len(resolved.Project.Operations) > 0 {
		notes = append(notes, fmt.Sprintf("解析到 %d 个 operation。", len(resolved.Project.Operations)))
	}
	return notes
}
