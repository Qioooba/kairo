package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxPreferenceBytes = 64 * 1024

// /api/preferences remains the compatibility API for global process-wide
// preferences such as tail highlights. New workbench state uses the namespaced,
// user-scoped /api/preferences/{module} API below.
func (s *Server) handlePreferences(w http.ResponseWriter, r *http.Request) {
	if s.preferences == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("偏好服务未初始化"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		prefs, err := s.preferences.Global()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, prefs)
	case http.MethodPut:
		var prefs map[string]any
		if err := decodePreferenceBody(w, r, &prefs); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := s.preferences.ReplaceGlobal(prefs); err != nil {
			writeErr(w, 500, err)
			return
		}
		s.audit.Write("preferences.global.put", "scope", preferenceUser(r))
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 405, errors.New("仅支持 GET / PUT"))
	}
}

func (s *Server) handlePreferenceNamespace(w http.ResponseWriter, r *http.Request) {
	if s.preferences == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("偏好服务未初始化"))
		return
	}
	namespace := strings.TrimPrefix(r.URL.Path, "/api/preferences/")
	if namespace == "" || strings.Contains(namespace, "/") {
		writeErr(w, 404, errors.New("偏好模块不存在"))
		return
	}
	user := preferenceUser(r)
	switch r.Method {
	case http.MethodGet:
		value, exists, err := s.preferences.Get(user, namespace)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if !exists {
			writeJSON(w, 200, map[string]any{"exists": false, "value": map[string]any{}})
			return
		}
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			writeErr(w, 500, fmt.Errorf("解析已保存偏好失败: %w", err))
			return
		}
		writeJSON(w, 200, map[string]any{"exists": true, "value": decoded})
	case http.MethodPut:
		var value json.RawMessage
		if err := decodePreferenceBody(w, r, &value); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := rejectSensitivePreferenceFields(value); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := s.preferences.Put(user, namespace, value); err != nil {
			writeErr(w, 400, err)
			return
		}
		s.audit.Write("preferences.module.put", "scope", user, "module", namespace)
		writeJSON(w, 200, map[string]any{"ok": true})
	case http.MethodDelete:
		if err := s.preferences.Delete(user, namespace); err != nil {
			writeErr(w, 400, err)
			return
		}
		s.audit.Write("preferences.module.delete", "scope", user, "module", namespace)
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 405, errors.New("仅支持 GET / PUT / DELETE"))
	}
}

func rejectSensitivePreferenceFields(value json.RawMessage) error {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return fmt.Errorf("偏好 JSON 解析失败: %w", err)
	}
	if _, ok := decoded.(map[string]any); !ok {
		return errors.New("模块偏好必须是 JSON 对象")
	}
	forbidden := map[string]struct{}{
		"password": {}, "token": {}, "secret": {}, "manifest": {}, "wsdl_content": {},
	}
	var walk func(any) error
	walk = func(current any) error {
		switch item := current.(type) {
		case map[string]any:
			for key, child := range item {
				if _, blocked := forbidden[strings.ToLower(key)]; blocked {
					return fmt.Errorf("字段 %q 不允许写入偏好文件", key)
				}
				if strings.EqualFold(key, "wsdl_url") {
					if raw, ok := child.(string); ok {
						parsed, err := url.Parse(raw)
						if err == nil && parsed.User != nil {
							return errors.New("包含用户名或密码的 WSDL URL 不允许写入偏好文件")
						}
					}
				}
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range item {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(decoded)
}

func decodePreferenceBody(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.ContentLength > maxPreferenceBytes {
		return errors.New("偏好内容太大（最大 64KB）")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPreferenceBytes))
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("偏好 JSON 解析失败: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("偏好内容只能包含一个 JSON 值")
		}
		return fmt.Errorf("偏好 JSON 解析失败: %w", err)
	}
	return nil
}

func preferenceUser(r *http.Request) string {
	if user, _ := r.Context().Value(authUserKey).(*authUser); user != nil && strings.TrimSpace(user.Name) != "" {
		return "user:" + strings.TrimSpace(user.Name)
	}
	return "local"
}
