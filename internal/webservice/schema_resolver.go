package webservice

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// SchemaResolverConfig 配置外部 XSD 依赖图解析器。
type SchemaResolverConfig struct {
	BaseURI        string            // 起始基准 URI（file:///... 或 http(s)://... 或 memory:///...）
	AllowedRootDir string            // 允许读取本地文件的根目录（防目录穿越），空则以 BaseURI 目录为准
	Attachments    map[string]string // 上传或附带的 XSD 文件表（相对路径或文件名 -> 内容）
	Context        context.Context   // 上下文（控制超时与取消）
	HTTPClient     *http.Client      // 可选自定义 HTTP Client
	AuthHeaders    map[string]string // 针对远程 HTTP 请求的认证头（跨源重定向自动剥离）

	MaxSchemas     int   // 最大解析 schema 数量上限（默认 100）
	MaxDepth       int   // 最大递归解析深度上限（默认 16）
	MaxSingleBytes int64 // 单个 XSD 最大字节上限（默认 4MB）
	MaxTotalBytes  int64 // 所有依赖 XSD 总字节上限（默认 20MB）
}

const (
	defaultMaxSchemas     = 100
	defaultMaxDepth       = 16
	defaultMaxSingleBytes = 4 * 1024 * 1024  // 4MB
	defaultMaxTotalBytes  = 20 * 1024 * 1024 // 20MB
)

// PathToFileURI 将本地文件路径规范化为标准 file:/// URI。
func PathToFileURI(filePath string) string {
	raw := strings.TrimSpace(filePath)
	if raw == "" {
		return ""
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		abs = raw
	}
	abs = filepath.Clean(abs)
	slashed := filepath.ToSlash(abs)

	// Windows 盘符路径：C:/foo/bar -> file:///C:/foo/bar
	if len(slashed) >= 2 && slashed[1] == ':' {
		return "file:///" + slashed
	}
	// UNC 路径：//server/share/file -> file://server/share/file
	if strings.HasPrefix(slashed, "//") {
		return "file:" + slashed
	}
	// Unix 绝对路径：/foo/bar -> file:///foo/bar
	if strings.HasPrefix(slashed, "/") {
		return "file://" + slashed
	}
	return "file:///" + slashed
}

// FileURIToPath 将 file:/// URI 还原为平台本地文件路径。
func FileURIToPath(uriStr string) (string, error) {
	u, err := url.Parse(uriStr)
	if err != nil {
		return "", fmt.Errorf("解析 URI 失败: %w", err)
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("非 file scheme URI: %s", uriStr)
	}
	p := u.Path
	if u.Host != "" {
		// UNC 路径: file://server/share/path -> //server/share/path
		return filepath.FromSlash("//" + u.Host + p), nil
	}
	// Windows 盘符: /C:/foo/bar -> C:/foo/bar
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p), nil
}

// ResolveURIReference 根据基准 URI 和引用的相对路径，计算规范化的目标完整 URI。
func ResolveURIReference(baseURI, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("引用的 location 为空")
	}
	// Windows 反斜杠统一转斜杠
	ref = strings.ReplaceAll(ref, "\\", "/")

	refURL, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("解析引用 location 失败: %w", err)
	}
	if refURL.IsAbs() {
		return refURL.String(), nil
	}

	baseURI = strings.TrimSpace(baseURI)
	if baseURI == "" {
		return ref, nil
	}

	// 若 baseURI 不含 scheme，则尝试作为本地路径转 file:///
	if !strings.Contains(baseURI, "://") {
		baseURI = PathToFileURI(baseURI)
	}

	baseU, err := url.Parse(baseURI)
	if err != nil {
		return "", fmt.Errorf("解析基准 URI 失败: %w", err)
	}

	if baseU.Scheme == "file" {
		basePath, err := FileURIToPath(baseURI)
		if err == nil {
			baseDir := filepath.Dir(basePath)
			targetPath := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(ref)))
			return PathToFileURI(targetPath), nil
		}
	}

	// HTTP/HTTPS/Memory 或其它 URL scheme
	resolved := baseU.ResolveReference(refURL)
	return resolved.String(), nil
}

// CheckPathWithinRoot 校验目标文件路径是否在允许的根目录内（防止路径穿越）。
func CheckPathWithinRoot(targetPath, rootDir string) error {
	cleanTarget := filepath.Clean(targetPath)
	cleanRoot := filepath.Clean(rootDir)
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return fmt.Errorf("路径 %s 无法计算相对于根目录 %s 的相对路径: %w", targetPath, rootDir, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("路径 %s 越界，超出允许的根目录 %s", targetPath, rootDir)
	}
	return nil
}

// SchemaResolver 负责按照规范 URI 构建 XML Schema 依赖图并加载到 schemaIndex。
type SchemaResolver struct {
	cfg        SchemaResolverConfig
	ctx        context.Context
	byNormAtts map[string]string   // 规范化相对路径 -> 内容
	byBaseAtts map[string][]string // 文件名 -> [规范化相对路径列表]
	httpClient *http.Client
}

// NewSchemaResolver 创建 SchemaResolver 实例。
func NewSchemaResolver(cfg SchemaResolverConfig) *SchemaResolver {
	if cfg.MaxSchemas <= 0 {
		cfg.MaxSchemas = defaultMaxSchemas
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = defaultMaxDepth
	}
	if cfg.MaxSingleBytes <= 0 {
		cfg.MaxSingleBytes = defaultMaxSingleBytes
	}
	if cfg.MaxTotalBytes <= 0 {
		cfg.MaxTotalBytes = defaultMaxTotalBytes
	}
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}

	byNorm := make(map[string]string, len(cfg.Attachments))
	byBase := make(map[string][]string, len(cfg.Attachments))
	for k, v := range cfg.Attachments {
		norm := path.Clean(strings.ReplaceAll(k, "\\", "/"))
		norm = strings.TrimPrefix(norm, "/")
		byNorm[norm] = v
		base := path.Base(norm)
		byBase[base] = append(byBase[base], norm)
	}

	client := cfg.HTTPClient
	if client == nil {
		client = makeSafeHTTPClient(cfg.AuthHeaders)
	}

	return &SchemaResolver{
		cfg:        cfg,
		ctx:        ctx,
		byNormAtts: byNorm,
		byBaseAtts: byBase,
		httpClient: client,
	}
}

func makeSafeHTTPClient(authHeaders map[string]string) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			ResponseHeaderTimeout: 25 * time.Second,
			DialContext:           SafeDialContext,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("重定向次数过多（最多允许 10 次）")
			}
			if err := ValidateEndpointURL(req.URL.String()); err != nil {
				return fmt.Errorf("重定向目标 URL 不受信任: %w", err)
			}
			prev := via[len(via)-1]
			prevOrigin := prev.URL.Scheme + "://" + prev.URL.Host
			newOrigin := req.URL.Scheme + "://" + req.URL.Host
			if !strings.EqualFold(prevOrigin, newOrigin) {
				// 跨源重定向：剥离 Authorization/Cookie 等敏感凭据
				req.Header.Del("Authorization")
				req.Header.Del("Proxy-Authorization")
				req.Header.Del("Cookie")
			}
			return nil
		},
	}
}

type schemaQueueItem struct {
	imp       xsdImport
	parentURI string
	parentNS  string
	depth     int
}

// ResolveGraph 遍历 WSDL 及引用的所有外部 XSD，填充 schemaIndex，并返回依赖清单与告警。
func (r *SchemaResolver) ResolveGraph(rootImports []xsdImport, idx *schemaIndex) ([]SchemaDependency, []string) {
	var (
		dependencies []SchemaDependency
		warnings     []string
		visited      = make(map[string]bool)
		totalBytes   int64
		queue        []schemaQueueItem
	)

	for _, imp := range rootImports {
		queue = append(queue, schemaQueueItem{
			imp:       imp,
			parentURI: r.cfg.BaseURI,
			parentNS:  "",
			depth:     1,
		})
	}

	for len(queue) > 0 {
		if err := r.ctx.Err(); err != nil {
			warnings = append(warnings, fmt.Sprintf("解析外部 XSD 依赖被取消: %v", err))
			break
		}

		item := queue[0]
		queue = queue[1:]

		if item.depth > r.cfg.MaxDepth {
			warnings = append(warnings, fmt.Sprintf("外部 XSD 超过最大递归深度 %d，已跳过: %s", r.cfg.MaxDepth, item.imp.SchemaLocation))
			dependencies = append(dependencies, SchemaDependency{
				URI:       item.imp.SchemaLocation,
				Namespace: item.imp.Namespace,
				Status:    "error",
				Error:     fmt.Sprintf("超过最大深度限制 %d", r.cfg.MaxDepth),
			})
			continue
		}

		if len(visited) >= r.cfg.MaxSchemas {
			warnings = append(warnings, fmt.Sprintf("外部 XSD 依赖数量超过上限 %d，已终止进一步解析", r.cfg.MaxSchemas))
			dependencies = append(dependencies, SchemaDependency{
				URI:       item.imp.SchemaLocation,
				Namespace: item.imp.Namespace,
				Status:    "error",
				Error:     fmt.Sprintf("超过最大 schema 节点数限制 %d", r.cfg.MaxSchemas),
			})
			break
		}

		// 1. 若没有 schemaLocation，仅通过 namespace 引用
		if strings.TrimSpace(item.imp.SchemaLocation) == "" {
			if item.imp.Namespace == "" {
				continue
			}
			// 尝试从附件或已知列表中找该 namespace
			attKey, attVal, found := r.findAttachmentByNamespace(item.imp.Namespace)
			if !found {
				dependencies = append(dependencies, SchemaDependency{
					URI:       "namespace:" + item.imp.Namespace,
					Namespace: item.imp.Namespace,
					Status:    "unresolved",
					Error:     "缺少 schemaLocation 且未在附件或依赖表中找到对应 namespace",
				})
				warnings = append(warnings, fmt.Sprintf("外部 XSD（namespace=%s）未提供 schemaLocation 且无匹配附件，相关类型可能无法展开", item.imp.Namespace))
				continue
			}
			item.imp.SchemaLocation = attKey
			_ = attVal
		}

		// 2. 解析规范 URI
		canonicalURI, err := ResolveURIReference(item.parentURI, item.imp.SchemaLocation)
		if err != nil {
			dependencies = append(dependencies, SchemaDependency{
				URI:       item.imp.SchemaLocation,
				Namespace: item.imp.Namespace,
				Status:    "error",
				Error:     fmt.Sprintf("计算规范 URI 失败: %v", err),
			})
			warnings = append(warnings, fmt.Sprintf("外部 XSD 路径解析失败（%s）: %v", item.imp.SchemaLocation, err))
			continue
		}

		// 循环引用与重复加载检测
		if visited[canonicalURI] {
			continue
		}

		// 3. 读取并解码内容
		rawContent, sizeBytes, sourceKind, fetchErr := r.fetchContent(canonicalURI, item.parentURI, item.imp.SchemaLocation)
		if fetchErr != nil {
			status := "unresolved"
			if errors.Is(fetchErr, os.ErrNotExist) {
				status = "unresolved"
			} else if strings.Contains(fetchErr.Error(), "歧义") {
				status = "conflict"
			} else {
				status = "error"
			}
			dependencies = append(dependencies, SchemaDependency{
				URI:       canonicalURI,
				Namespace: item.imp.Namespace,
				Source:    sourceKind,
				Status:    status,
				Error:     fetchErr.Error(),
			})
			warnings = append(warnings, fmt.Sprintf("外部 XSD 加载失败（URI=%s, ns=%s）: %v，相关类型参数可能无法展开", canonicalURI, item.imp.Namespace, fetchErr))
			continue
		}

		// 字节预算检查
		if sizeBytes > r.cfg.MaxSingleBytes {
			errStr := fmt.Sprintf("单文件大小 %d 超过限制 %d 字节", sizeBytes, r.cfg.MaxSingleBytes)
			dependencies = append(dependencies, SchemaDependency{
				URI:       canonicalURI,
				Namespace: item.imp.Namespace,
				Source:    sourceKind,
				Status:    "error",
				SizeBytes: sizeBytes,
				Error:     errStr,
			})
			warnings = append(warnings, fmt.Sprintf("外部 XSD 超出单文件大小上限: %s", canonicalURI))
			continue
		}
		if totalBytes+sizeBytes > r.cfg.MaxTotalBytes {
			errStr := fmt.Sprintf("累计大小超过总预算 %d 字节", r.cfg.MaxTotalBytes)
			dependencies = append(dependencies, SchemaDependency{
				URI:       canonicalURI,
				Namespace: item.imp.Namespace,
				Source:    sourceKind,
				Status:    "error",
				SizeBytes: sizeBytes,
				Error:     errStr,
			})
			warnings = append(warnings, fmt.Sprintf("外部 XSD 超出总字节预算上限: %s", canonicalURI))
			break
		}
		totalBytes += sizeBytes

		// 4. 解析 XSD XML
		extSchema, err := parseExternalXSD(rawContent)
		if err != nil {
			dependencies = append(dependencies, SchemaDependency{
				URI:       canonicalURI,
				Namespace: item.imp.Namespace,
				Source:    sourceKind,
				Status:    "error",
				SizeBytes: sizeBytes,
				Error:     fmt.Sprintf("XSD XML 解析失败: %v", err),
			})
			warnings = append(warnings, fmt.Sprintf("外部 XSD 解析失败（URI=%s）: %v", canonicalURI, err))
			continue
		}

		// 变色龙 schema 继承命名空间（chameleon schema include）
		if extSchema.TargetNS == "" {
			if item.imp.Namespace != "" {
				extSchema.TargetNS = item.imp.Namespace
			} else if item.parentNS != "" {
				extSchema.TargetNS = item.parentNS
			}
		}

		// 注册进索引
		extRootNS, _ := collectNSContexts(rawContent)
		idx.addSchema(*extSchema, nsContext{prefixes: extRootNS, self: extSchema.TargetNS})

		visited[canonicalURI] = true
		dependencies = append(dependencies, SchemaDependency{
			URI:       canonicalURI,
			Namespace: extSchema.TargetNS,
			Source:    sourceKind,
			Status:    "loaded",
			SizeBytes: sizeBytes,
		})

		// 5. 递归收集子级 imports / includes
		for _, childImp := range extSchema.Imports {
			if childImp.SchemaLocation != "" || childImp.Namespace != "" {
				queue = append(queue, schemaQueueItem{
					imp:       childImp,
					parentURI: canonicalURI,
					parentNS:  extSchema.TargetNS,
					depth:     item.depth + 1,
				})
			}
		}
		for _, childInc := range extSchema.Includes {
			if childInc.SchemaLocation != "" {
				queue = append(queue, schemaQueueItem{
					imp:       childInc,
					parentURI: canonicalURI,
					parentNS:  extSchema.TargetNS,
					depth:     item.depth + 1,
				})
			}
		}
	}

	return dependencies, warnings
}

// fetchContent 综合附件表、本地文件和 HTTP 远程拉取内容。
func (r *SchemaResolver) fetchContent(canonicalURI, parentURI, rawRef string) (content string, sizeBytes int64, source string, err error) {
	// A. 优先尝试从附件查找（支持相对路径与唯一 basename 回退）
	if len(r.cfg.Attachments) > 0 {
		attContent, ok, attErr := r.lookupAttachment(canonicalURI, parentURI, rawRef)
		if attErr != nil {
			return "", 0, "attachment", attErr
		}
		if ok {
			return attContent, int64(len(attContent)), "attachment", nil
		}
	}

	// B. 本地文件模式
	if strings.HasPrefix(canonicalURI, "file://") {
		filePath, convErr := FileURIToPath(canonicalURI)
		if convErr != nil {
			return "", 0, "file", convErr
		}

		// 检查路径穿越限制
		allowedRoot := r.cfg.AllowedRootDir
		if allowedRoot == "" && r.cfg.BaseURI != "" && strings.HasPrefix(r.cfg.BaseURI, "file://") {
			if bp, berr := FileURIToPath(r.cfg.BaseURI); berr == nil {
				allowedRoot = filepath.Dir(bp)
			}
		}
		if allowedRoot != "" {
			if chkErr := CheckPathWithinRoot(filePath, allowedRoot); chkErr != nil {
				return "", 0, "file", chkErr
			}
		}

		st, statErr := os.Stat(filePath)
		if statErr != nil {
			return "", 0, "file", statErr
		}
		if st.Size() > r.cfg.MaxSingleBytes {
			return "", st.Size(), "file", fmt.Errorf("文件大小 %d 超过单文件限制 %d", st.Size(), r.cfg.MaxSingleBytes)
		}

		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return "", 0, "file", readErr
		}
		decoded, decErr := DecodeXMLBytes(data, "")
		if decErr != nil {
			return string(data), int64(len(data)), "file", nil
		}
		return decoded, int64(len(decoded)), "file", nil
	}

	// C. 远程 HTTP/HTTPS 模式
	if strings.HasPrefix(canonicalURI, "http://") || strings.HasPrefix(canonicalURI, "https://") {
		if valErr := ValidateEndpointURL(canonicalURI); valErr != nil {
			return "", 0, "http", fmt.Errorf("SSRF 检查未通过: %w", valErr)
		}

		httpReq, reqErr := http.NewRequestWithContext(r.ctx, http.MethodGet, canonicalURI, nil)
		if reqErr != nil {
			return "", 0, "http", fmt.Errorf("构造请求失败: %w", reqErr)
		}
		httpReq.Header.Set("User-Agent", "kairo-wsdl/0.1")
		for k, v := range r.cfg.AuthHeaders {
			httpReq.Header.Set(k, v)
		}

		resp, doErr := r.httpClient.Do(httpReq)
		if doErr != nil {
			return "", 0, "http", fmt.Errorf("下载 XSD 失败: %w", doErr)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", 0, "http", fmt.Errorf("XSD 请求返回状态 %d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))
		}

		limitReader := io.LimitReader(resp.Body, r.cfg.MaxSingleBytes+1)
		data, readErr := io.ReadAll(limitReader)
		if readErr != nil {
			return "", 0, "http", fmt.Errorf("读取 XSD 响应失败: %w", readErr)
		}
		if int64(len(data)) > r.cfg.MaxSingleBytes {
			return "", int64(len(data)), "http", fmt.Errorf("XSD 响应大小超过单文件限制 %d", r.cfg.MaxSingleBytes)
		}

		decoded, decErr := DecodeXMLBytes(data, resp.Header.Get("Content-Type"))
		if decErr != nil {
			return "", 0, "http", fmt.Errorf("解码 XSD XML 失败: %w", decErr)
		}
		return decoded, int64(len(decoded)), "http", nil
	}

	return "", 0, "unknown", fmt.Errorf("无法识别或不支持的 URI Scheme: %s", canonicalURI)
}

// lookupAttachment 在附件表中匹配 XSD。
func (r *SchemaResolver) lookupAttachment(canonicalURI, parentURI, rawRef string) (string, bool, error) {
	normRef := path.Clean(strings.ReplaceAll(rawRef, "\\", "/"))
	normRef = strings.TrimPrefix(normRef, "/")

	// 1. 精确相对路径匹配
	if val, ok := r.byNormAtts[normRef]; ok {
		return val, true, nil
	}

	// 2. 基于 parentURI 相对路径推导匹配（例如 parent 是 a/sub.xsd, 引用 ../b/common.xsd -> b/common.xsd）
	if strings.Contains(parentURI, "://") {
		if pu, perr := url.Parse(parentURI); perr == nil {
			pDir := path.Dir(strings.TrimPrefix(pu.Path, "/"))
			combined := path.Clean(path.Join(pDir, normRef))
			if val, ok := r.byNormAtts[combined]; ok {
				return val, true, nil
			}
		}
	}

	// 3. Basename 回退（仅在唯一匹配时允许）
	base := path.Base(normRef)
	matches := r.byBaseAtts[base]
	if len(matches) == 1 {
		return r.byNormAtts[matches[0]], true, nil
	}
	if len(matches) > 1 {
		return "", false, fmt.Errorf("歧义的 XSD 附件引用: 多个附件匹配文件名 %s (%s)，请在 schemaLocation 中指定相对子目录路径", base, strings.Join(matches, ", "))
	}

	return "", false, nil
}

// findAttachmentByNamespace 尝试寻找声明了指定 targetNamespace 的附件。
func (r *SchemaResolver) findAttachmentByNamespace(ns string) (string, string, bool) {
	for k, v := range r.byNormAtts {
		if strings.Contains(v, ns) {
			sch, err := parseExternalXSD(v)
			if err == nil && sch != nil && sch.TargetNS == ns {
				return k, v, true
			}
		}
	}
	return "", "", false
}
