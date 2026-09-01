package wscodegen

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"kairo/internal/sysutil"
)

const toolTimeout = 90 * time.Second

type toolPlan struct {
	JavaPath  string
	ClassPath string
	MainClass string
	Args      []string
	WorkDir   string
	Wsimport  string // 非空时直接跑 wsimport 二进制，不再 java -cp
}

func planTool(req Request, resolved resolvedWSDL, outDir string) (toolPlan, error) {
	jdk, ok := InspectJDKHome(req.JDKHome, "user")
	if !ok {
		found := DetectJDKs("")
		if len(found) == 0 {
			return toolPlan{}, fmt.Errorf("没有可用的 JDK。请选择工程自带的 JDK Home，不要依赖本机 PATH 上那个可能过新的 java")
		}
		jdk = found[0]
	}
	javaPath := filepath.Join(jdk.Home, "bin", javaBinName())
	if !fileExists(javaPath) {
		javaPath = filepath.Join(jdk.Home, "jre", "bin", javaBinName())
	}
	wsdlArg, err := wsdlArgForTool(resolved)
	if err != nil {
		return toolPlan{}, err
	}
	pkg := JavaPackage(req.PackageName)
	cp := strings.Join(uniqueKeep(req.ClasspathJars), classpathSep())
	plan := toolPlan{JavaPath: javaPath, ClassPath: cp, WorkDir: outDir}

	switch req.Engine {
	case EngineJAXWS:
		ws := filepath.Join(jdk.Home, "bin", wsimportBinName())
		if !fileExists(ws) {
			return plan, fmt.Errorf("该 JDK（%s, major=%d）没有 wsimport。JDK 6/8 才自带；JDK 11+ 请改用内置 JAX-WS 或 CXF", jdk.Home, jdk.Major)
		}
		plan.Wsimport = ws
		args := []string{"-keep", "-s", outDir, "-Xnocompile", "-encoding", "UTF-8"}
		if pkg != "" {
			args = append(args, "-p", pkg)
		}
		target := req.JAXWSTarget
		if target == "" {
			if jdk.Major > 0 && jdk.Major <= 6 {
				target = JAXWSTarget21
			}
		}
		if target == JAXWSTarget21 {
			args = append(args, "-target", "2.1")
		}
		args = append(args, req.ExtraFlags...)
		args = append(args, wsdlArg)
		plan.Args = args
	case EngineAxis1:
		plan.MainClass = "org.apache.axis.wsdl.WSDL2Java"
		args := []string{"-o", outDir}
		if pkg != "" {
			args = append(args, "-p", pkg)
		}
		args = append(args, "-w")
		args = append(args, req.ExtraFlags...)
		args = append(args, wsdlArg)
		plan.Args = args
	case EngineAxis2:
		plan.MainClass = "org.apache.axis2.wsdl.WSDL2Java"
		args := []string{"-uri", wsdlArg, "-o", outDir, "-S", "src", "-s"}
		if pkg != "" {
			args = append(args, "-p", pkg)
		}
		args = append(args, req.ExtraFlags...)
		plan.Args = args
	case EngineCXF:
		plan.MainClass = "org.apache.cxf.tools.wsdlto.WSDLToJava"
		args := []string{"-d", outDir, "-autoNameResolution"}
		if pkg != "" {
			args = append(args, "-p", pkg)
		}
		args = append(args, req.ExtraFlags...)
		args = append(args, wsdlArg)
		plan.Args = args
	case EngineXFire:
		plan.MainClass = "org.codehaus.xfire.gen.Wsdl11Generator"
		args := []string{"-wsdl", wsdlArg, "-o", outDir, "-overwrite", "true"}
		if pkg != "" {
			args = append(args, "-p", pkg)
		}
		args = append(args, req.ExtraFlags...)
		plan.Args = args
	default:
		return plan, fmt.Errorf("引擎 %s 没有官方命令行生成器，请用内置模式", req.Engine)
	}
	if plan.Wsimport == "" && strings.TrimSpace(plan.ClassPath) == "" {
		return plan, fmt.Errorf("官方生成需要工程 jar。请先扫描项目目录或手动勾选 axis/xfire/cxf 相关 jar")
	}
	return plan, nil
}

func wsdlArgForTool(resolved resolvedWSDL) (string, error) {
	if resolved.FilePath != "" {
		return resolved.FilePath, nil
	}
	if resolved.URL != "" {
		return resolved.URL, nil
	}
	if strings.TrimSpace(resolved.Raw) == "" {
		return "", fmt.Errorf("没有 WSDL 文件或 URL，官方工具无法运行")
	}
	tmp, err := os.CreateTemp("", "kairo-wsdl-*.wsdl")
	if err != nil {
		return "", fmt.Errorf("写临时 WSDL 失败: %w", err)
	}
	if _, err := tmp.WriteString(resolved.Raw); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

func formatCommand(plan toolPlan) string {
	if plan.Wsimport != "" {
		return shellJoin(append([]string{plan.Wsimport}, plan.Args...))
	}
	parts := []string{plan.JavaPath, "-cp", plan.ClassPath, plan.MainClass}
	parts = append(parts, plan.Args...)
	return shellJoin(parts)
}

func shellJoin(parts []string) string {
	var out []string
	for _, p := range parts {
		if strings.ContainsAny(p, " \t\"") {
			out = append(out, `"`+strings.ReplaceAll(p, `"`, `\"`)+`"`)
		} else {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func runTool(plan toolPlan) (logText string, err error) {
	var cmd *exec.Cmd
	if plan.Wsimport != "" {
		cmd = exec.Command(plan.Wsimport, plan.Args...)
	} else {
		args := append([]string{"-cp", plan.ClassPath, plan.MainClass}, plan.Args...)
		cmd = exec.Command(plan.JavaPath, args...)
	}
	cmd.Dir = plan.WorkDir
	sysutil.HideConsoleWindow(cmd)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err = <-done:
	case <-time.After(toolTimeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		err = fmt.Errorf("生成超时（%s）", toolTimeout)
	}
	logText = buf.String()
	if err != nil {
		if logText == "" {
			return logText, err
		}
		return logText, fmt.Errorf("%v\n%s", err, trimLog(logText, 4000))
	}
	return logText, nil
}

func collectWrittenJava(root string) ([]GeneratedFile, []string, error) {
	var files []GeneratedFile
	var written []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if ext != ".java" && ext != ".txt" && ext != ".xml" && ext != ".wsdd" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		written = append(written, rel)
		if ext == ".java" && len(files) < 80 && info.Size() < 512*1024 {
			data, rerr := os.ReadFile(path)
			if rerr == nil {
				files = append(files, GeneratedFile{RelPath: rel, Content: string(data), Kind: "java"})
			}
		}
		return nil
	})
	return files, written, err
}

func uniqueKeep(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func trimLog(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (log truncated)"
}
