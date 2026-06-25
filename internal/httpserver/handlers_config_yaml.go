package httpserver

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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

func redactConfigYAML(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	sensitiveKeys := []string{"password", "passwd", "secret", "token", "api_key", "apikey", "private_key", "host_key_sha256"}
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
		redact := false
		for _, s := range sensitiveKeys {
			if strings.Contains(key, s) {
				redact = true
				break
			}
		}
		if !redact {
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
