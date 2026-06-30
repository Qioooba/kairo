package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"kairo/internal/credentials"
)

// ---------- /api/credentials/* ----------

type credSaveReq struct {
	System   string `json:"system"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleCredSave 保存 SSH 密码到 OS 钥匙串。
func (s *Server) handleCredSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req credSaveReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(req.Password) == "" {
		writeErr(w, 400, errors.New("password 不能为空"))
		return
	}
	if err := s.credCheckSysSrv(req.System, req.Server); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := credentials.Save(req.System, req.Server, strings.TrimSpace(req.Username), req.Password); err != nil {
		if errors.Is(err, credentials.ErrUnavailable) {
			writeErr(w, 503, err)
			return
		}
		writeErrSanitized(w, 500, err)
		return
	}
	s.audit.Write("credentials.save",
		"system", req.System, "server", req.Server,
		"username", strings.TrimSpace(req.Username), "mode", credentials.Mode(), "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleCredHas 检查指定凭据是否存在。
//
// Query: ?system=&server=&username=
// 返回: {ok, has, available, mode, reason}
//   - ok=true 总是
//   - has=true 表示已保存
//   - available=false 表示凭据不可用（前端可提示"无法使用记住密码"）
//     原因可能是：keyring 在当前平台不可用 / mode=disabled / mode=file 暂未实现
//   - mode=当前凭据后端模式（"keyring"/"file"/"disabled"），前端据此决定
//     是否渲染「记住密码」控件
//   - reason=不可用时的简短原因（前端可展示）
//
// username 缺省时降级返回 has=false（避免前端还没填用户名就触发 500）。
func (s *Server) handleCredHas(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errors.New("仅支持 GET"))
		return
	}
	q := r.URL.Query()
	system, server, user := q.Get("system"), q.Get("server"), q.Get("username")
	if err := s.credCheckSysSrv(system, server); err != nil {
		writeErr(w, 400, err)
		return
	}
	mode := credentials.Mode()
	// mode=disabled 时直接返 available=false，不去真正调 Has()。
	// 这样前端能根据 mode 决定显隐，不会被 ErrUnavailable 兜底逻辑误判。
	if mode == credentials.ModeDisabled {
		writeJSON(w, 200, map[string]any{
			"ok":        true,
			"has":       false,
			"available": false,
			"mode":      mode,
			"reason":    "凭据存储已被配置禁用（credential_store=disabled），密码不会被保存",
		})
		return
	}
	if strings.TrimSpace(user) == "" {
		// username 缺省：降级返回 has=false。
		// 用场景：前端 onChange 时还在打字，不应该触发 500。
		writeJSON(w, 200, map[string]any{"ok": true, "has": false, "available": true, "mode": mode})
		return
	}
	has, err := credentials.Has(system, server, user)
	if err != nil {
		if errors.Is(err, credentials.ErrUnavailable) {
			writeJSON(w, 200, map[string]any{
				"ok":        true,
				"has":       false,
				"available": false,
				"mode":      mode,
				"reason":    "系统钥匙串不可用",
				"err":       err.Error(),
			})
			return
		}
		writeErrSanitized(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "has": has, "available": true, "mode": mode})
}

// handleCredClear 删除指定凭据。
func (s *Server) handleCredClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req credSaveReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.credCheckSysSrv(req.System, req.Server); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := credentials.Clear(req.System, req.Server, strings.TrimSpace(req.Username)); err != nil {
		if errors.Is(err, credentials.ErrNotSaved) {
			writeJSON(w, 200, map[string]any{"ok": true, "cleared": false})
			return
		}
		if errors.Is(err, credentials.ErrUnavailable) {
			writeErr(w, 503, err)
			return
		}
		writeErrSanitized(w, 500, err)
		return
	}
	s.audit.Write("credentials.clear",
		"system", req.System, "server", req.Server,
		"username", strings.TrimSpace(req.Username), "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true, "cleared": true})
}

// credCheckSysSrv 校验 system/server 都在配置白名单里（防止前端乱传 keyring 索引）。
func (s *Server) credCheckSysSrv(system, server string) error {
	if strings.TrimSpace(system) == "" || strings.TrimSpace(server) == "" {
		return errors.New("system 和 server 必填")
	}
	_, _, ok := s.cur().FindServer(system, server)
	if !ok {
		return errors.New("系统或服务器不存在")
	}
	return nil
}
