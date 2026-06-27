package httpserver

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// handleConfigExport GET /api/config/export
// 读取 config.yaml 并对常见敏感字段脱敏后以 text/yaml 返回，触发浏览器下载。
func (s *Server) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	path := s.cfg.Path()
	data, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, 500, fmt.Errorf("读取配置文件失败: %w", err))
		return
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="config.yaml"`)
	w.WriteHeader(200)
	_, _ = w.Write(redactConfigYAML(data))
}

// sensitiveKeys 是需要脱敏的 YAML key 子串列表（大小写不敏感匹配）。
// 命中任一子串的 key，其 value 会被替换为 "***"。
//
// 采用子串匹配可覆盖复合 key（如 db_password、api_key_secret）。
var sensitiveKeys = []string{
	"password", "passwd", "secret", "token",
	"api_key", "private_key", "host_key_sha256", "credential_key",
}

// isSensitiveKey 判断（小写化后的）key 是否包含任一敏感子串。
func isSensitiveKey(key string) bool {
	lk := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(lk, s) {
			return true
		}
	}
	return false
}

// redactConfigYAML 解析 YAML 后递归脱敏敏感字段，再重新序列化为 YAML 文本。
// 相比旧的逐行扫描，能正确处理多行字符串、flow 风格、嵌套 map 等结构。
// 若 YAML 解析失败（语法错误），回退到逐行脱敏逻辑，避免完全无法导出。
func redactConfigYAML(data []byte) []byte {
	var root any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return redactConfigYAMLLegacy(data)
	}
	if root == nil {
		return data
	}
	out, err := yaml.Marshal(redactValue(root))
	if err != nil {
		return redactConfigYAMLLegacy(data)
	}
	return out
}

// redactValue 递归遍历 YAML 解析出的值，命中敏感 key 时把 value 替换为 "***"。
func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if isSensitiveKey(k) {
				t[k] = "***"
			} else {
				t[k] = redactValue(val)
			}
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = redactValue(val)
		}
		return t
	default:
		return v
	}
}

// redactConfigYAMLLegacy 是旧的逐行脱敏实现，仅在 YAML 解析失败时作为回退使用。
func redactConfigYAMLLegacy(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		colon := strings.Index(trimmed, ":")
		if colon <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(trimmed[:colon]))
		if !isSensitiveKey(key) {
			continue
		}
		indentLen := len(line) - len(strings.TrimLeft(line, " \t"))
		lines[i] = line[:indentLen] + strings.TrimSpace(trimmed[:colon]) + `: "***"`
	}
	return []byte(strings.Join(lines, "\n"))
}

// handleConfigImport POST /api/config/import
// 接收 YAML 文本，验证后替换 config.yaml（自动备份旧文件）。
func (s *Server) handleConfigImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}

	// 限制请求体大小 1MB（配置文件不会很大）
	yamlData, err := io.ReadAll(io.LimitReader(r.Body, 1*1024*1024))
	if err != nil {
		writeErr(w, 400, fmt.Errorf("读取请求体失败: %w", err))
		return
	}
	defer r.Body.Close()

	yamlStr := strings.TrimSpace(string(yamlData))
	if yamlStr == "" {
		writeErr(w, 400, errors.New("请求体为空"))
		return
	}

	if err := s.cfg.ImportYAML(yamlData); err != nil {
		s.audit.Write("admin.config.import", "result", "fail", "err", err.Error())
		writeErr(w, 400, err)
		return
	}

	s.audit.Write("admin.config.import", "result", "ok", "path", s.cfg.Path())
	writeJSON(w, 200, map[string]any{
		"ok":   true,
		"path": s.cfg.Path(),
	})
}
