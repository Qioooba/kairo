package wscodegen

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"kairo/internal/sysutil"
)

var javaVersionRe = regexp.MustCompile(`(?i)(?:openjdk|java)\s+version\s+"([^"]+)"`)
var javaMajorRe = regexp.MustCompile(`^1\.(\d+)`)

// ParseJavaVersion 从 `java -version` 的 stderr/stdout 解析发行字串和主版本。
func ParseJavaVersion(output string) (version string, major int) {
	m := javaVersionRe.FindStringSubmatch(output)
	if len(m) < 2 {
		// 某些发行版只打 "17.0.8"
		alt := regexp.MustCompile(`(?m)^"?(\d+(?:\.\d+){0,2})`).FindStringSubmatch(strings.TrimSpace(output))
		if len(alt) >= 2 {
			version = alt[1]
			return version, javaMajor(version)
		}
		return "", 0
	}
	version = m[1]
	return version, javaMajor(version)
}

func javaMajor(version string) int {
	if m := javaMajorRe.FindStringSubmatch(version); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	dot := strings.IndexByte(version, '.')
	head := version
	if dot > 0 {
		head = version[:dot]
	}
	n, err := strconv.Atoi(head)
	if err != nil {
		return 0
	}
	return n
}

func javaBinName() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}

func javacBinName() string {
	if runtime.GOOS == "windows" {
		return "javac.exe"
	}
	return "javac"
}

func wsimportBinName() string {
	if runtime.GOOS == "windows" {
		return "wsimport.exe"
	}
	return "wsimport"
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// InspectJDKHome 检查一个目录是不是能用的 JDK/JRE。
func InspectJDKHome(home, source string) (JDKInfo, bool) {
	home = strings.TrimSpace(home)
	info := JDKInfo{Home: home, Source: source}
	if home == "" || !dirExists(home) {
		return info, false
	}
	javaPath := filepath.Join(home, "bin", javaBinName())
	if !fileExists(javaPath) {
		// 有人会选到 jre 子目录的上一级，也有人直接选 jre
		alt := filepath.Join(home, "jre", "bin", javaBinName())
		if fileExists(alt) {
			javaPath = alt
		} else {
			return info, false
		}
	}
	info.HasJava = true
	info.HasJavac = fileExists(filepath.Join(home, "bin", javacBinName()))
	info.HasWsimport = fileExists(filepath.Join(home, "bin", wsimportBinName()))
	info.HasToolsJar = fileExists(filepath.Join(home, "lib", "tools.jar"))
	verOut := runJavaVersion(javaPath)
	info.Version, info.Major = ParseJavaVersion(verOut)
	if strings.Contains(strings.ToLower(verOut), "openjdk") {
		info.Vendor = "OpenJDK"
	} else if strings.Contains(strings.ToLower(verOut), "hotspot") || strings.Contains(strings.ToLower(verOut), "java(tm)") {
		info.Vendor = "Oracle/Sun"
	}
	switch {
	case info.Major > 0 && info.Major <= 6 && !info.HasWsimport && !info.HasToolsJar:
		info.Notes = "像 JRE 而不是 JDK，JAX-WS 官方生成不可用。"
	case info.Major >= 11 && !info.HasWsimport:
		info.Notes = "JDK 11+ 已移除 wsimport，JAX-WS 官方生成需要额外 jaxws-tools，或改用内置 / CXF / portable。"
	case info.Major == 8 && info.HasWsimport:
		info.Notes = "JDK 8 wsimport 默认 JAX-WS 2.2。目标工程是 JDK 6 时请勾 -target 2.1。"
	case info.Major == 6 && info.HasWsimport:
		info.Notes = "JDK 6 自带 JAX-WS 2.1，生成结果最适合老工程。"
	}
	return info, true
}

func runJavaVersion(javaPath string) string {
	cmd := exec.Command(javaPath, "-version")
	sysutil.HideConsoleWindow(cmd)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	_ = cmd.Run()
	return buf.String()
}

// DetectJDKs 探测 JAVA_HOME、PATH、常见安装目录。userHome 非空时优先放在最前。
func DetectJDKs(userHome string) []JDKInfo {
	seen := map[string]bool{}
	var out []JDKInfo
	add := func(home, source string) {
		home = filepath.Clean(strings.TrimSpace(home))
		if home == "" || home == "." {
			return
		}
		key := strings.ToLower(home)
		if seen[key] {
			return
		}
		info, ok := InspectJDKHome(home, source)
		if !ok {
			return
		}
		seen[key] = true
		out = append(out, info)
	}

	if userHome != "" {
		add(userHome, "user")
	}
	if v := strings.TrimSpace(os.Getenv("JAVA_HOME")); v != "" {
		add(v, "JAVA_HOME")
	}
	if v := strings.TrimSpace(os.Getenv("JDK_HOME")); v != "" {
		add(v, "JDK_HOME")
	}
	if p, err := exec.LookPath(javaBinName()); err == nil {
		// .../bin/java -> home
		add(filepath.Dir(filepath.Dir(p)), "PATH")
	}
	for _, cand := range commonJDKDirs() {
		add(cand, "scan")
	}
	return out
}

func commonJDKDirs() []string {
	var roots []string
	switch runtime.GOOS {
	case "windows":
		pf := os.Getenv("ProgramFiles")
		pf86 := os.Getenv("ProgramFiles(x86)")
		roots = []string{
			pf + `\Java`,
			pf86 + `\Java`,
			pf + `\Eclipse Adoptium`,
			pf + `\Microsoft`,
			pf + `\Amazon Corretto`,
			`C:\Java`,
			`D:\Java`,
			`C:\jdk`,
			`D:\jdk`,
		}
	case "darwin":
		roots = []string{
			"/Library/Java/JavaVirtualMachines",
			"/System/Library/Java/JavaVirtualMachines",
		}
	default:
		roots = []string{
			"/usr/lib/jvm",
			"/usr/java",
			"/opt/java",
			"/opt/jdk",
		}
	}
	var dirs []string
	for _, root := range roots {
		if root == "" || !dirExists(root) {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := strings.ToLower(e.Name())
			if strings.Contains(name, "jdk") || strings.Contains(name, "jre") || strings.Contains(name, "temurin") || strings.Contains(name, "hotspot") || strings.Contains(name, "corretto") {
				dirs = append(dirs, filepath.Join(root, e.Name()))
				// macOS Contents/Home
				home := filepath.Join(root, e.Name(), "Contents", "Home")
				if dirExists(home) {
					dirs = append(dirs, home)
				}
			}
		}
		// 根目录本身就是 JDK
		dirs = append(dirs, root)
	}
	return dirs
}

var jarKindPatterns = []struct {
	kind    string
	engine  string
	needles []string
}{
	{kind: "axis1", engine: EngineAxis1, needles: []string{"axis-1", "axis.jar"}},
	{kind: "jaxrpc", engine: EngineAxis1, needles: []string{"jaxrpc"}},
	{kind: "saaj", engine: EngineAxis1, needles: []string{"saaj"}},
	{kind: "axis2", engine: EngineAxis2, needles: []string{"axis2-"}},
	{kind: "axiom", engine: EngineAxis2, needles: []string{"axiom-"}},
	{kind: "xfire-generator", engine: EngineXFire, needles: []string{"xfire-generator"}},
	{kind: "xfire-all", engine: EngineXFire, needles: []string{"xfire-all"}},
	{kind: "xfire", engine: EngineXFire, needles: []string{"xfire"}},
	{kind: "cxf", engine: EngineCXF, needles: []string{"cxf-"}},
	{kind: "jaxws", engine: EngineJAXWS, needles: []string{"jaxws-tools", "jaxws-rt", "jaxws-ri"}},
	{kind: "wsdl4j", engine: "", needles: []string{"wsdl4j"}},
	{kind: "stax", engine: "", needles: []string{"stax-api", "wstx", "woodstox"}},
	{kind: "jaxb", engine: "", needles: []string{"jaxb-impl", "jaxb-api", "jaxb-xjc"}},
	{kind: "neethi", engine: "", needles: []string{"neethi"}},
	{kind: "xmlschema", engine: "", needles: []string{"xmlschema"}},
	{kind: "commons-logging", engine: "", needles: []string{"commons-logging"}},
	{kind: "commons-discovery", engine: "", needles: []string{"commons-discovery"}},
	{kind: "commons-httpclient", engine: "", needles: []string{"commons-httpclient", "httpclient-"}},
	{kind: "activation", engine: "", needles: []string{"activation", "jakarta.activation"}},
	{kind: "mail", engine: "", needles: []string{"mail.jar", "geronimo-javamail", "jakarta.mail"}},
}

var skipScanDir = map[string]bool{
	".git": true, ".svn": true, ".hg": true, ".idea": true, ".cursor": true,
	"node_modules": true, "tmp": true, "temp": true, ".settings": true,
}

const maxScanJars = 200
const maxScanDepth = 5

// ScanProject 扫描项目目录里的 jar / pom，推断该用哪套引擎。
func ScanProject(root string) (ScanResult, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	res := ScanResult{ProjectDir: root, SuggestedEngine: EnginePortable}
	if root == "" || !dirExists(root) {
		res.Notes = append(res.Notes, "项目目录不存在")
		return res, nil
	}

	type counted struct {
		jars int
	}
	c := counted{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		depth := strings.Count(rel, string(os.PathSeparator))
		if info.IsDir() {
			base := strings.ToLower(info.Name())
			if skipScanDir[base] || strings.HasPrefix(base, ".") {
				if path != root {
					return filepath.SkipDir
				}
			}
			if depth >= maxScanDepth {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(info.Name())
		if name == "pom.xml" {
			data, rerr := os.ReadFile(path)
			if rerr == nil {
				res.PomHints = append(res.PomHints, pomEngineHints(string(data))...)
			}
			return nil
		}
		if !strings.HasSuffix(name, ".jar") {
			return nil
		}
		if c.jars >= maxScanJars {
			res.Truncated = true
			return nil
		}
		kind, engine := classifyJar(name)
		if kind == "" {
			return nil
		}
		c.jars++
		res.Jars = append(res.Jars, JarHit{Path: path, FileName: info.Name(), Kind: kind})
		if engine != "" {
			res.DetectedEngines = appendUnique(res.DetectedEngines, engine)
		}
		return nil
	})

	for _, h := range res.PomHints {
		res.DetectedEngines = appendUnique(res.DetectedEngines, h)
	}
	res.SuggestedEngine = suggestEngine(res.DetectedEngines)
	if res.SuggestedEngine != EnginePortable {
		res.MissingForSuggest = missingJarsFor(res.SuggestedEngine, res.Jars)
	}
	res.Notes = append(res.Notes, xfireCompatNotes(res.Jars)...)
	if len(res.Jars) == 0 && len(res.PomHints) == 0 {
		res.Notes = append(res.Notes, "没扫到 axis / xfire / cxf / jaxws jar。可以改用 portable，或手动勾选工程 lib 里的 jar。")
	}
	if res.Truncated {
		res.Notes = append(res.Notes, "jar 数量超过扫描上限，结果可能不完整。请尽量选 lib / WEB-INF/lib 而不是整个磁盘。")
	}
	return res, nil
}

func classifyJar(lowerName string) (kind, engine string) {
	for _, pat := range jarKindPatterns {
		for _, n := range pat.needles {
			if strings.Contains(lowerName, n) {
				return pat.kind, pat.engine
			}
		}
	}
	return "", ""
}

func pomEngineHints(xml string) []string {
	low := strings.ToLower(xml)
	var out []string
	if strings.Contains(low, "xfire") {
		out = append(out, EngineXFire)
	}
	if strings.Contains(low, "cxf") {
		out = append(out, EngineCXF)
	}
	if strings.Contains(low, "axis2") {
		out = append(out, EngineAxis2)
	} else if strings.Contains(low, "axis") && strings.Contains(low, "org.apache.axis") {
		out = append(out, EngineAxis1)
	}
	if strings.Contains(low, "jaxws") || strings.Contains(low, "jax-ws") {
		out = append(out, EngineJAXWS)
	}
	return out
}

func suggestEngine(detected []string) string {
	// 老工程里 XFire / Axis1 比 JAX-WS 更“像它真正在用的栈”
	priority := []string{EngineXFire, EngineAxis1, EngineAxis2, EngineCXF, EngineJAXWS}
	for _, p := range priority {
		for _, d := range detected {
			if d == p {
				return p
			}
		}
	}
	return EnginePortable
}

func missingJarsFor(engine string, jars []JarHit) []string {
	need := map[string][]string{
		EngineAxis1: {"axis", "jaxrpc", "saaj", "wsdl4j", "commons-logging", "commons-discovery"},
		EngineAxis2: {"axis2", "axiom", "wsdl4j"},
		EngineXFire: {"xfire"},
		EngineCXF:   {"cxf"},
		EngineJAXWS: {},
	}[engine]
	have := map[string]bool{}
	for _, j := range jars {
		have[j.Kind] = true
		low := strings.ToLower(j.FileName)
		for _, n := range need {
			if strings.Contains(low, n) {
				have[n] = true
			}
		}
	}
	var missing []string
	for _, n := range need {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	return missing
}

func xfireCompatNotes(jars []JarHit) []string {
	var hasXFire, hasAll, hasGen, hasSplit bool
	for _, j := range jars {
		low := strings.ToLower(j.FileName)
		if !strings.Contains(low, "xfire") {
			continue
		}
		hasXFire = true
		if strings.Contains(low, "xfire-all") {
			hasAll = true
		}
		if strings.Contains(low, "xfire-generator") {
			hasGen = true
		}
		if strings.Contains(low, "xfire-core") || strings.Contains(low, "xfire-aegis") ||
			strings.Contains(low, "xfire-spring") || strings.Contains(low, "xfire-jaxb2") {
			hasSplit = true
		}
	}
	if !hasXFire {
		return nil
	}
	notes := []string{"检测到 XFire 1.2。生成代码必须用 org.codehaus.xfire.*，不要改成 CXF / JAX-WS。"}
	if hasAll && hasSplit {
		notes = append(notes, "lib 里同时有 xfire-all 和拆开的 xfire-core/aegis/spring/jaxb2，信贷工程常见。内置动态 Client 按 xfire-all 的包名出代码即可。")
	}
	if !hasGen {
		notes = append(notes, "没扫到 xfire-generator.jar。官方 Wsdl11Generator 常常单独打包，这份工程请用「内置生成」。")
	}
	return notes
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func classpathSep() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}
