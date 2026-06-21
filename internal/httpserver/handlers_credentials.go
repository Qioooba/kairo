package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"ops-toolbox/internal/credentials"
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
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("credentials.save",
		"system", req.System, "server", req.Server,
		"username", strings.TrimSpace(req.Username), "result", "ok")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleCredHas 检查指定凭据是否存在。
//
// Query: ?system=&server=&username=
// 返回: {ok, has, available}
//   - ok=true 总是
//   - has=true 表示已保存
//   - available=false 表示 keyring 在当前平台不可用（前端可提示"无法使用记住密码"）
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
	has, err := credentials.Has(system, server, user)
	if err != nil {
		if errors.Is(err, credentials.ErrUnavailable) {
			writeJSON(w, 200, map[string]any{"ok": true, "has": false, "available": false, "err": err.Error()})
			return
		}
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "has": has, "available": true})
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
		writeErr(w, 500, err)
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
