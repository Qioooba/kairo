package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// P2-20：统一偏好存储（data/preferences.json）。
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
	data, err := os.ReadFile(prefFile)
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
	tmp := prefFile + ".tmp"
	encoded, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		writeErr(w, 500, fmt.Errorf("偏好 JSON 序列化失败: %w", err))
		return
	}
	if err := os.WriteFile(tmp, encoded, 0644); err != nil {
		writeErr(w, 500, fmt.Errorf("写入临时文件失败: %w", err))
		return
	}
	if err := os.Rename(tmp, prefFile); err != nil {
		os.Remove(tmp)
		writeErr(w, 500, fmt.Errorf("原子替换 preferences 失败: %w", err))
		return
	}
	s.audit.Write("preferences.put", "file", prefFile)
	writeJSON(w, 200, map[string]any{"ok": true, "path": prefFile})
}

func (s *Server) preferencesPath() string {
	return filepath.Join(s.cur().DataDir(), "preferences.json")
}
