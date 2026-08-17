package httpserver

// 本文件集中存放各 handler 文件共用的辅助函数，避免分散到各处造成重复。

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"kairo/internal/audit"
	"kairo/internal/credentials"
	"kairo/internal/sshclient"
)

// ZipSource 描述一个要打进 zip 的源文件。
//
//   - Path：磁盘上要读取的本地路径
//   - NameInZip：zip 内的文件名（通常是远端原始文件名，方便用户解压后辨认）
//
// 项 17 修复：原 zipFiles 用本地 basename 当 zip 内的文件名，远端原始名丢了；
// 现在允许调用方显式指定 NameInZip，本地路径跟 zip 内的"展示名"解耦。
type ZipSource struct {
	Path      string
	NameInZip string
}

// zipFiles 把若干已下载的本地文件打包成单个 zip。
//
// 设计要点：
//
//   - 用 archive/zip + DEFLATE（默认级别），纯 stdlib，无新增依赖；
//
//   - zip 内文件名只保留"原始名"（不带 downloads/YYYYMMDD/server_dir_ 前缀），
//     这样在 Windows 资源管理器里双击打开能直接看到干净的日志；
//
//   - 写入时若任一文件打开失败，整体报错，不留半截 zip；
//
//   - srcPaths 是后端自己下载生成的本地路径，**不是用户输入**，
//     所以不再做路径穿越 / 反斜杠检查 —— Windows 本地路径天然含 `\`。
//
//   - 第二个起同名文件加 `_2`、`_3` 后缀，避免 zip 内覆盖。
func zipFiles(srcPaths []string, destPath string) error {
	if len(srcPaths) == 0 {
		return errors.New("没有可打包的文件")
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("创建目标目录失败: %w", err)
	}
	dst, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("创建 zip 文件失败: %w", err)
	}
	zw := zip.NewWriter(dst)
	closed := false
	defer func() {
		if !closed {
			_ = zw.Close()
			_ = dst.Close()
			_ = os.Remove(destPath)
		}
	}()
	usedNames := map[string]int{}
	for _, p := range srcPaths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("解析源文件路径失败: %w", err)
		}
		f, err := os.Open(abs)
		if err != nil {
			return fmt.Errorf("打开源文件 %s 失败: %w", abs, err)
		}
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("stat %s 失败: %w", abs, err)
		}
		if st.IsDir() {
			_ = f.Close()
			return fmt.Errorf("不能打包目录: %s", abs)
		}
		// zip 内只保留原始文件名（去前缀、去目录），避免解压后路径嵌套太深
		name := filepath.Base(abs)
		// 同名文件避免覆盖：第二个起加 _N 后缀
		if n := usedNames[name]; n > 0 {
			ext := filepath.Ext(name)
			base := strings.TrimSuffix(name, ext)
			name = fmt.Sprintf("%s_%d%s", base, n+1, ext)
		}
		usedNames[filepath.Base(abs)]++
		header := &zip.FileHeader{
			Name:     name,
			Method:   zip.Deflate,
			Modified: st.ModTime(),
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("写入 zip 头失败: %w", err)
		}
		if _, err := io.Copy(w, f); err != nil {
			_ = f.Close()
			return fmt.Errorf("写入 zip 内容失败: %w", err)
		}
		_ = f.Close()
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("关闭 zip writer 失败: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("关闭 zip 文件失败: %w", err)
	}
	closed = true
	return nil
}

// zipFilesNamed 用源对象 ZipSource 指定的 NameInZip 打包（项 17 修复：保留远端原始文件名）。
//
// 行为跟 zipFiles 几乎一样，差别只在 zip 内文件名来源：
//   - 优先用 NameInZip（远端原始名，更直观）；
//   - 同名 NameInZip 第二个起加 _N 后缀防覆盖；
//   - NameInZip 为空时回退到本地 basename（旧行为）。
func zipFilesNamed(sources []ZipSource, destPath string) error {
	if len(sources) == 0 {
		return errors.New("没有可打包的文件")
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("创建目标目录失败: %w", err)
	}
	dst, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("创建 zip 文件失败: %w", err)
	}
	zw := zip.NewWriter(dst)
	closed := false
	defer func() {
		if !closed {
			_ = zw.Close()
			_ = dst.Close()
			_ = os.Remove(destPath)
		}
	}()
	usedNames := map[string]int{}
	for _, src := range sources {
		abs, err := filepath.Abs(src.Path)
		if err != nil {
			return fmt.Errorf("解析源文件路径失败: %w", err)
		}
		f, err := os.Open(abs)
		if err != nil {
			return fmt.Errorf("打开源文件 %s 失败: %w", abs, err)
		}
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("stat %s 失败: %w", abs, err)
		}
		if st.IsDir() {
			_ = f.Close()
			return fmt.Errorf("不能打包目录: %s", abs)
		}
		name := src.NameInZip
		if name == "" {
			name = filepath.Base(abs)
		}
		// v1.4：支持目录层级（目录递归下载）。把 "\" 归一成 "/"，逐个路径段过滤
		// 危险片段（""、"."、".."、穿越形态），防 zip 解压路径逃逸。
		name = sanitizeZipName(name)
		// 同名文件避免覆盖：第二个起加 _N 后缀（对最后一段加）
		orig := name
		n := usedNames[orig]
		if n > 0 {
			dir := path.Dir(name)
			last := path.Base(name)
			ext := path.Ext(last)
			base := strings.TrimSuffix(last, ext)
			last = fmt.Sprintf("%s_%d%s", base, n+1, ext)
			if dir == "." || dir == "/" {
				name = last
			} else {
				name = path.Join(dir, last)
			}
		}
		usedNames[orig] = n + 1
		header := &zip.FileHeader{
			Name:     name,
			Method:   zip.Deflate,
			Modified: st.ModTime(),
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("写入 zip 头失败: %w", err)
		}
		if _, err := io.Copy(w, f); err != nil {
			_ = f.Close()
			return fmt.Errorf("写入 zip 内容失败: %w", err)
		}
		_ = f.Close()
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("关闭 zip writer 失败: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("关闭 zip 文件失败: %w", err)
	}
	closed = true
	return nil
}

// sanitizeZipName 把 NameInZip 归一成安全的 zip 内路径（v1.4 目录递归下载）。
//
// 规则：
//   - "\" 归一成 "/"（Windows 风格路径）
//   - 逐段过滤：去掉空段、"."、".."、含穿越形态的段
//   - 过滤后为空 → 回退 basename（若 basename 也是 "."/".." → "x"）
//
// NameInZip 由后端从远端文件名构造（非用户直接输入），这里只是防御性收口，
// 防解压路径逃逸（zip-slip）。
func sanitizeZipName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "/")
	segs := strings.Split(name, "/")
	clean := make([]string, 0, len(segs))
	for _, seg := range segs {
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		clean = append(clean, seg)
	}
	if len(clean) == 0 {
		base := path.Base(name)
		if base == "" || base == "." || base == ".." {
			return "x"
		}
		return base
	}
	return path.Join(clean...)
}

// sanitize 把字符串清成安全文件名片段
func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "x"
	}
	bad := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", " "}
	for _, b := range bad {
		s = strings.ReplaceAll(s, b, "_")
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// uniqueLocalName 在 targetDir 下找一个不冲突的本地文件名（项 4：保留原文件名）。
//
// 规则：
//   - 如果 rawBase 在 targetDir 下不存在 → 直接用 rawBase
//   - 否则加 "<server>__" 前缀（避免跨 server 同名冲突），还不够再降级到 _2/_3
//
// 设计动机：用户希望保留远端原始文件名（SystemOut.log），但多台 server 同时下
// 同一文件时仍需区分。
func uniqueLocalName(targetDir, rawBase, serverName string) string {
	candidate := rawBase
	if !fileExists(filepath.Join(targetDir, candidate)) {
		return candidate
	}
	// 多 server 冲突：加 server 前缀
	if serverName != "" {
		candidate = sanitize(serverName) + "__" + rawBase
		if !fileExists(filepath.Join(targetDir, candidate)) {
			return candidate
		}
	}
	// 仍冲突：拆 ext 拼 _N
	ext := filepath.Ext(rawBase)
	base := strings.TrimSuffix(rawBase, ext)
	for i := 2; i < 1000; i++ {
		candidate = fmt.Sprintf("%s_%d%s", base, i, ext)
		if !fileExists(filepath.Join(targetDir, candidate)) {
			return candidate
		}
	}
	// 真撞了 1000 次：放弃可读性，直接用时间戳兜底
	return fmt.Sprintf("%s_%d%s", base, time.Now().UnixNano(), ext)
}

// hasPathTraversal 检测 name 是否含路径穿越片段（精确匹配路径分隔符形式的 ".."）。
//
// 旧实现 strings.Contains(name, "..") 会误拒所有含 ".." 子串的合法文件名
// （如 "my..file.log"），这里改为只匹配真正能构成穿越的形态：
//
//   - 形如 "/../"（夹在中间）、"../"（前缀）、"/.."（后缀）
//   - 形如 "\\..\\"、"..\\"、"\\.."（Windows 路径分隔符版本）
//   - 单独的 ".."（整串就是父目录引用）
//
// 不含上述任意形态则返回 false，允许 "my..file.log" 这类合法双点文件名。
func hasPathTraversal(name string) bool {
	return strings.Contains(name, "/../") ||
		strings.HasPrefix(name, "../") ||
		strings.HasSuffix(name, "/..") ||
		strings.Contains(name, "\\..\\") ||
		strings.HasPrefix(name, "..\\") ||
		strings.HasSuffix(name, "\\..") ||
		name == ".."
}

// trim 把字符串按 rune 数截断到 n 个，加 "..."
func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "..."
}

// humanBytes 把字节数转成"1.2 MB"这种格式
func humanBytes(n int64) string {
	const k = 1024
	if n < k {
		return strconv.FormatInt(n, 10) + " B"
	}
	if n < k*k {
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	}
	if n < k*k*k {
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(k*k*k))
}

// auditErr 把 err 写进审计日志，自动脱敏敏感字面量，并保证 502 时也写。
// 接受 http.ResponseWriter 是为了和 writeErr 风格保持一致（未来可挂中间件）。
func auditErr(w http.ResponseWriter, a *audit.Logger, op string, kv ...any) {
	if len(kv) < 2 {
		return
	}
	// 把最后一个 kv 视为 err
	errVal := kv[len(kv)-1]
	errStr, ok := errVal.(error)
	if !ok {
		errStr = fmt.Errorf("%v", errVal)
	}
	clean := sshclient.SanitizeError(errStr.Error())
	kv = append(kv[:len(kv)-1], "err", clean)
	a.Write(op, kv...)
}

// resolvedCreds SSH 凭据解析结果。Password 为空表示"需要前端提示用户输入"。
type resolvedCreds struct {
	Username     string
	Password     string
	SavedByStore bool // true 表示 password 来自凭据存储（keyring/file），不是用户本次输入
}

// resolveCreds 把 HTTP 请求里的凭据 + 配置里的默认密码 + 凭据存储合并成一个最终值。
//   - inputUser / inputPass：HTTP 请求里的明文
//   - system / server：凭据存储的 key（system 和 server 名称）
//   - defaultUser：配置里的默认 SSH 用户名（inputUser 为空时使用）
//   - defaultPass：配置里的 SSH 密码（inputPass 为空时使用，优先级低于 keyring）
//
// 返回规则：
//   - err != nil：无法继续（缺用户、keyring 不可用等配置错误）
//   - err == nil && Password != ""：可直接用
//   - err == nil && Password == ""：前端没传、配置里没存、存储里也没存（或 disabled 模式），需用户输入
func (s *Server) resolveCreds(inputUser, inputPass, system, server, defaultUser, defaultPass string) (resolvedCreds, error) {
	username := strings.TrimSpace(inputUser)
	if username == "" {
		username = defaultUser
	}
	if username == "" {
		return resolvedCreds{}, errors.New("缺少用户名")
	}
	if inputPass != "" {
		return resolvedCreds{Username: username, Password: inputPass}, nil
	}

	mode := credentials.Mode()
	// disabled 模式：不从存储读，尝试配置密码
	if mode == credentials.ModeDisabled {
		if defaultPass != "" {
			return resolvedCreds{Username: username, Password: defaultPass}, nil
		}
		return resolvedCreds{Username: username}, nil
	}

	// 尝试从凭据存储读（keyring/file）—— 优先级高于配置文件密码（用户主动保存 vs 管理员默认）
	pw, err := credentials.Get(system, server, username)
	if err == nil {
		return resolvedCreds{Username: username, Password: pw, SavedByStore: true}, nil
	}
	if errors.Is(err, credentials.ErrNotSaved) {
		// keyring 没存 → 尝试配置文件里的默认密码
		if defaultPass != "" {
			return resolvedCreds{Username: username, Password: defaultPass}, nil
		}
		return resolvedCreds{Username: username}, nil
	}
	// 其它错误（keyring 不可用 / file 后端未初始化等）
	// keyring 出错时仍可尝试配置密码
	if defaultPass != "" {
		return resolvedCreds{Username: username, Password: defaultPass}, nil
	}
	return resolvedCreds{}, fmt.Errorf("凭据存储不可用，请手动输入密码或检查配置: %w", err)
}
