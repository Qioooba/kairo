package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// P2-20：统一偏好存储（data/preferences.json）。
var preferencesMu sync.Mutex

func (s *Server) handlePreferences(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handlePreferencesGet(w, r)
	case http.MethodPut:
		s.handlePreferencesPut(w, r)
	default:
		writeErr(w, 405, errors.New("仅支持 GET / PUT"))
	}
}

func (s *Server) handlePreferencesGet(w http.ResponseWriter, r *http.Request) {
	prefFile := s.preferencesPath()
	preferencesMu.Lock()
	data, err := os.ReadFile(prefFile)
	preferencesMu.Unlock()
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, 200, map[string]any{})
			return
		}
		writeErr(w, 500, fmt.Errorf("读取 preferences 失败: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (s *Server) handlePreferencesPut(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > 64*1024 {
		writeErr(w, 400, errors.New("请求体太大（最大 64KB）"))
		return
	}
	var prefs map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&prefs); err != nil {
		writeErr(w, 400, fmt.Errorf("偏好 JSON 解析失败: %w", err))
		return
	}
	prefFile := s.preferencesPath()
	dir := filepath.Dir(prefFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		writeErr(w, 500, fmt.Errorf("创建偏好目录失败: %w", err))
		return
	}
	encoded, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		writeErr(w, 500, fmt.Errorf("偏好 JSON 序列化失败: %w", err))
		return
	}
	preferencesMu.Lock()
	defer preferencesMu.Unlock()
	tmpFile, err := os.CreateTemp(dir, ".preferences-*.tmp")
	if err != nil {
		writeErr(w, 500, fmt.Errorf("创建临时文件失败: %w", err))
		return
	}
	tmp := tmpFile.Name()
	if _, err := tmpFile.Write(encoded); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		writeErr(w, 500, fmt.Errorf("写入临时文件失败: %w", err))
		return
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		writeErr(w, 500, fmt.Errorf("关闭临时文件失败: %w", err))
		return
	}
	// BE-016：preferences.json 含敏感数据，显式收紧到 0600（避免依赖平台默认 / umask）。
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		writeErr(w, 500, fmt.Errorf("收紧临时文件权限失败: %w", err))
		return
	}
	if err := os.Rename(tmp, prefFile); err != nil {
		_ = os.Remove(tmp)
		writeErr(w, 500, fmt.Errorf("原子替换 preferences 失败: %w", err))
		return
	}
	s.audit.Write("preferences.put", "file", prefFile)
	writeJSON(w, 200, map[string]any{"ok": true, "path": prefFile})
}

func (s *Server) preferencesPath() string {
	return filepath.Join(s.cur().DataDir(), "preferences.json")
}
