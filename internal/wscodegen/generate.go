package wscodegen

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

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
		absPath, err := filepath.Abs(f)
		if err != nil {
			return out, fmt.Errorf("解析 WSDL 路径失败: %w", err)
		}
		decoded, err := readXMLFile(absPath)
		if err != nil {
			return out, fmt.Errorf("读取 WSDL 文件失败: %w", err)
		}
		baseURI := webservice.PathToFileURI(absPath)
		cfg := webservice.SchemaResolverConfig{
			BaseURI:        baseURI,
			AllowedRootDir: filepath.Dir(absPath),
		}
		p := webservice.ParseWSDL(decoded, cfg)
		p.Source = "file"
		out.Project = p
		out.Raw = p.RawWSDL
		out.FilePath = absPath
		loadedCount := 0
		for _, d := range p.Dependencies {
			if d.Status == "loaded" {
				loadedCount++
			}
		}
		out.SiblingXSDCount = loadedCount
		return out, nil
	}
	if u := strings.TrimSpace(req.WSDLURL); u != "" {
		out.URL = u
	}
	if c := strings.TrimSpace(req.WSDLContent); c != "" {
		var p *webservice.WSDLProject
		if out.URL != "" {
			// 相对 schemaLocation 要从 WSDL URL 解析，否则拆 XSD 的服务参数树是空的。
			p = webservice.ParseWSDL(c, webservice.SchemaResolverConfig{BaseURI: out.URL})
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
	return GenerateContext(context.Background(), req, store)
}

// GenerateContext 生成客户端代码，并把调用方取消信号传递给外部生成工具。
func GenerateContext(ctx context.Context, req Request, store *webservice.Store) (Result, error) {
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
		if outDir == "" && !req.DryRun {
			return res, fmt.Errorf("官方工具模式必须指定输出目录")
		}
		abs := ""
		if outDir != "" {
			var err error
			abs, err = filepath.Abs(outDir)
			if err != nil {
				return res, err
			}
		}
		if req.DryRun {
			planDir := abs
			if planDir == "" {
				planDir = "<output-dir>"
			}
			plan, err := planTool(req, resolved, planDir, false)
			if err != nil {
				return res, err
			}
			defer plan.Close()
			res.Command = formatCommand(plan)
			res.OK = true
			res.OutputDir = abs
			res.Notes = append(compatibilityNotes(req, resolved), "dry-run：未创建目录、未写临时 WSDL、未执行官方工具")
			return res, nil
		}

		// 1. 目标目录互斥与预检
		unlockDir := targetDirLock.acquire(abs)
		defer unlockDir()

		if err := prepareToolOutputDir(abs, req.Overwrite); err != nil {
			return res, err
		}

		// 2. 隔离临时工作目录
		stageDir, err := os.MkdirTemp(os.TempDir(), "kairo-wstool-stage-")
		if err != nil {
			return res, fmt.Errorf("创建工具临时目录失败: %w", err)
		}
		defer os.RemoveAll(stageDir)

		plan, err := planTool(req, resolved, stageDir, true)
		if err != nil {
			return res, err
		}
		defer plan.Close()
		res.Command = formatCommand(plan)

		// 3. 全局工具并发限制
		select {
		case toolSem <- struct{}{}:
			defer func() { <-toolSem }()
		case <-ctx.Done():
			return res, ctx.Err()
		}

		logText, err := runTool(ctx, plan)
		res.ToolLog = trimLog(logText, 8000)
		if err != nil {
			return res, err
		}

		// 4. 收集并校验生成产物
		files, err := collectAndValidateGeneratedFiles(stageDir)
		if err != nil {
			return res, fmt.Errorf("校验工具产物失败: %w", err)
		}
		if len(files) == 0 {
			return res, fmt.Errorf("官方工具执行成功但未生成任何文件")
		}

		// 5. 安全发布到目标目录（与内置模式共用备份与回滚机制）
		written, err := writeFilesLocked(abs, files, req.Overwrite)
		if err != nil {
			return res, fmt.Errorf("发布生成文件失败: %w", err)
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
	if resolved.Project != nil {
		res.Dependencies = resolved.Project.Dependencies
	}
	if resolved.SiblingXSDCount > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("已从 WSDL 依赖图加载 %d 个 XSD。", resolved.SiblingXSDCount))
	}
	if w := missingExternalXSDWarning(resolved.Project); w != "" {
		res.Warnings = append(res.Warnings, w)
	}
	res.OK = true
	return res, nil
}

func fileURI(path string) string {
	raw := strings.TrimSpace(path)
	if windowsDrivePathRe.MatchString(raw) {
		slashed := strings.ReplaceAll(raw, "\\", "/")
		return (&url.URL{Scheme: "file", Path: "/" + slashed}).String()
	}
	if strings.HasPrefix(raw, `\\`) || strings.HasPrefix(raw, "//") {
		slashed := strings.TrimLeft(strings.ReplaceAll(raw, "\\", "/"), "/")
		parts := strings.SplitN(slashed, "/", 2)
		if len(parts) == 2 && parts[0] != "" {
			return (&url.URL{Scheme: "file", Host: parts[0], Path: "/" + parts[1]}).String()
		}
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		abs = raw
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

var windowsDrivePathRe = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func prepareToolOutputDir(abs string, overwrite bool) error {
	st, err := os.Lstat(abs)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return fmt.Errorf("创建输出目录失败: %w", err)
		}
		return nil
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("输出目录是符号链接，拒绝写入")
	}
	if !st.IsDir() {
		return fmt.Errorf("输出路径已存在且不是目录")
	}
	if overwrite {
		return nil
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("输出目录不是空目录；未勾选覆盖时拒绝调用官方工具")
	}
	return nil
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

func missingExternalXSDWarning(p *webservice.WSDLProject) string {
	if p == nil {
		return ""
	}
	var failedDeps []string
	for _, d := range p.Dependencies {
		if d.Status != "loaded" {
			errMsg := d.Error
			if errMsg == "" {
				errMsg = d.Status
			}
			failedDeps = append(failedDeps, fmt.Sprintf("%s (%s)", d.URI, errMsg))
		}
	}
	if len(failedDeps) > 0 {
		return fmt.Sprintf("外部 XSD 依赖未完全加载: %s，相关类型参数可能无法展开", strings.Join(failedDeps, "; "))
	}
	if !strings.Contains(p.RawWSDL, "schemaLocation") {
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
	return "WSDL 引用了外部 XSD，但参数树是空的。请确认外部 XSD 路径正确且类型完整。"
}

type dirLockManager struct {
	mu    sync.Mutex
	locks map[string]*dirLockRef
}

type dirLockRef struct {
	mu       sync.Mutex
	refCount int
}

func (m *dirLockManager) acquire(dir string) func() {
	key := filepath.Clean(dir)
	if os.PathSeparator == '\\' {
		key = strings.ToLower(key)
	}
	m.mu.Lock()
	ref, ok := m.locks[key]
	if !ok {
		ref = &dirLockRef{}
		m.locks[key] = ref
	}
	ref.refCount++
	m.mu.Unlock()

	ref.mu.Lock()
	return func() {
		ref.mu.Unlock()
		m.mu.Lock()
		ref.refCount--
		if ref.refCount == 0 {
			delete(m.locks, key)
		}
		m.mu.Unlock()
	}
}

var (
	targetDirLock = &dirLockManager{locks: make(map[string]*dirLockRef)}
	toolSem       = make(chan struct{}, 4)
)

func collectAndValidateGeneratedFiles(stageDir string) ([]GeneratedFile, error) {
	var files []GeneratedFile
	fileCount := 0
	var totalBytes int64

	const (
		maxFilesCount = 1000
		maxSingleSize = 10 * 1024 * 1024 // 10MB
		maxTotalSize  = 50 * 1024 * 1024 // 50MB
	)

	err := filepath.Walk(stageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info == nil || info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("检测到符号链接产物，拒绝发布: %s", path)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("检测到非常规文件产物: %s", path)
		}

		rel, err := filepath.Rel(stageDir, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if relSlash == ".." || strings.HasPrefix(relSlash, "../") {
			return fmt.Errorf("产物路径越界: %s", rel)
		}

		fileCount++
		if fileCount > maxFilesCount {
			return fmt.Errorf("产物文件数量超过上限 %d", maxFilesCount)
		}
		size := info.Size()
		if size > maxSingleSize {
			return fmt.Errorf("单个产物文件 %s 大小 %d 超过限制 %d", rel, size, maxSingleSize)
		}
		totalBytes += size
		if totalBytes > maxTotalSize {
			return fmt.Errorf("产物总大小超过上限 %d", maxTotalSize)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取工具产物失败 %s: %w", rel, err)
		}

		kind := "txt"
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".java" {
			kind = "java"
		}
		files = append(files, GeneratedFile{
			RelPath: relSlash,
			Content: string(data),
			Kind:    kind,
		})
		return nil
	})

	return files, err
}

func writeFiles(root string, files []GeneratedFile, overwrite bool) ([]string, error) {
	unlock := targetDirLock.acquire(root)
	defer unlock()
	return writeFilesLocked(root, files, overwrite)
}

func writeFilesLocked(root string, files []GeneratedFile, overwrite bool) ([]string, error) {
	// Validate the entire plan before publishing any generated file.
	seen := map[string]bool{}
	for _, f := range files {
		rel := filepath.ToSlash(f.RelPath)
		dest := filepath.Join(root, filepath.FromSlash(rel))
		key := dest
		if os.PathSeparator == '\\' {
			key = strings.ToLower(key)
		}
		if rel == "" || strings.Contains(rel, "..") || filepath.IsAbs(rel) || !isInside(root, dest) || seen[key] {
			return nil, fmt.Errorf("非法或重复输出路径: %s", f.RelPath)
		}
		seen[key] = true
		for probe := dest; ; probe = filepath.Dir(probe) {
			info, err := os.Lstat(probe)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			if err == nil {
				if info.Mode()&os.ModeSymlink != 0 {
					return nil, fmt.Errorf("输出路径包含符号链接: %s", probe)
				}
				if probe == dest && (!overwrite || !info.Mode().IsRegular()) {
					return nil, fmt.Errorf("文件已存在或不可覆盖: %s", rel)
				}
				if probe != dest && !info.IsDir() {
					return nil, fmt.Errorf("输出父路径不是目录: %s", probe)
				}
			}
			if filepath.Dir(probe) == probe {
				break
			}
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(root, ".kairo-codegen-")
	if err != nil {
		return nil, err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stage)
		}
	}()
	type publication struct {
		dest, backup string
		installed    bool
	}
	var published []publication
	rollback := func(cause error) ([]string, error) {
		for i := len(published) - 1; i >= 0; i-- {
			p := published[i]
			if p.installed {
				if err := os.Remove(p.dest); err != nil && !os.IsNotExist(err) {
					keepStage = true
				}
			}
			if p.backup != "" {
				if err := os.Rename(p.backup, p.dest); err != nil {
					keepStage = true
				}
			}
		}
		if keepStage {
			return nil, fmt.Errorf("%w；回滚未完成，恢复文件保留在 %s", cause, stage)
		}
		return nil, cause
	}
	for i, f := range files {
		if err := os.WriteFile(filepath.Join(stage, fmt.Sprintf("new-%d", i)), []byte(f.Content), 0o644); err != nil {
			return nil, err
		}
	}
	var written []string
	for i, f := range files {
		rel := filepath.ToSlash(f.RelPath)
		dest := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return rollback(err)
		}
		p := publication{dest: dest}
		if overwrite {
			if info, err := os.Lstat(dest); err == nil {
				if !info.Mode().IsRegular() {
					return rollback(fmt.Errorf("输出目标不再是普通文件: %s", rel))
				}
				p.backup = filepath.Join(stage, fmt.Sprintf("old-%d", i))
				if err := os.Rename(dest, p.backup); err != nil {
					return rollback(err)
				}
			} else if !os.IsNotExist(err) {
				return rollback(err)
			}
		}
		published = append(published, p)
		srcFile := filepath.Join(stage, fmt.Sprintf("new-%d", i))
		if err := os.Link(srcFile, dest); err != nil {
			// Hardlink failed (cross-device / filesystem), fall back to copy
			data, rerr := os.ReadFile(srcFile)
			if rerr != nil {
				return rollback(err)
			}
			if werr := os.WriteFile(dest, data, 0o644); werr != nil {
				return rollback(werr)
			}
		}
		published[len(published)-1].installed = true
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
