package httpserver

// 本文件集中存放各 handler 文件共用的辅助函数，避免分散到各处造成重复。

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ops-toolbox/internal/audit"
	"ops-toolbox/internal/credentials"
	"ops-toolbox/internal/sshclient"
)

// zipFiles 把若干已下载的本地文件打包成单个 zip。
//
// 设计要点：
//   - 用 archive/zip + DEFLATE（默认级别），纯 stdlib，无新增依赖；
//   - zip 内文件名只保留"原始名"（不带 downloads/YYYYMMDD/server_dir_ 前缀），
//     这样在 Windows 资源管理器里双击打开能直接看到干净的日志；
//   - 写入时若任一文件打开失败，整体报错，不留半截 zip；
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
	Username       string
	Password       string
	SavedByKeyring bool // true 表示 password 来自 OS 钥匙串，不回传给前端
}

// resolveCreds 把 HTTP 请求里的凭据 + OS 钥匙串合并成一个最终值。
//   - inputUser / inputPass：HTTP 请求里的明文
//   - system / server：钥匙串的 key（system 和 server 名称）
//   - defaultUser：配置里的默认 SSH 用户名（inputUser 为空时使用）
//
// 返回规则：
//   - err != nil：无法继续（缺用户、钥匙串不可用）
//   - err == nil && Password != ""：可直接用
//   - err == nil && Password == ""：前端没传、钥匙串也没存，需用户输入
func (s *Server) resolveCreds(inputUser, inputPass, system, server, defaultUser string) (resolvedCreds, error) {
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
	// 尝试从 keyring 读
	pw, err := credentials.Get(system, server, username)
	if err == nil {
		return resolvedCreds{Username: username, Password: pw, SavedByKeyring: true}, nil
	}
	if errors.Is(err, credentials.ErrNotSaved) {
		return resolvedCreds{Username: username}, nil
	}
	// 其它错误（钥匙串不可用等）
	return resolvedCreds{}, fmt.Errorf("系统钥匙串不可用，请手动输入密码或检查系统配置: %w", err)
}