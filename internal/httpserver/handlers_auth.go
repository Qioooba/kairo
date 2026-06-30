package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"kairo/internal/config"
)

type authLoginReq struct {
	Token string `json:"token"`
}

type authStatusResp struct {
	AuthRequired bool   `json:"auth_required"`
	User         string `json:"user,omitempty"`
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request, cur *config.Config) {
	switch r.Method {
	case http.MethodGet:
		if !cur.Auth.EffectiveEnabled() {
			writeJSON(w, 200, authStatusResp{AuthRequired: false})
			return
		}
		tokenStr := extractToken(r)
		if tokenStr != "" {
			token := cur.Auth.LookupToken(tokenStr)
			ip := clientIP(r)
			if token != nil && token.IPAllowed(ip) {
				writeJSON(w, 200, authStatusResp{AuthRequired: false, User: token.Name})
				return
			}
		}
		writeJSON(w, 200, authStatusResp{AuthRequired: true})
	case http.MethodPost:
		if !cur.Auth.EffectiveEnabled() {
			writeErr(w, http.StatusBadRequest, errors.New("认证未启用"))
			return
		}
		var req authLoginReq
		if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, errors.New("请求体解析失败"))
			return
		}
		tokenStr := req.Token
		if tokenStr == "" {
			tokenStr = extractToken(r)
		}
		if tokenStr == "" {
			writeErr(w, http.StatusUnauthorized, errors.New("请提供 token"))
			return
		}
		ip := clientIP(r)
		token := cur.Auth.LookupToken(tokenStr)
		if token == nil {
			writeErr(w, http.StatusUnauthorized, errors.New("认证失败：无效的 token"))
			return
		}
		if !token.IPAllowed(ip) {
			writeErr(w, http.StatusForbidden, errors.New("访问被拒绝：IP 不在允许列表"))
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     authCookieName,
			Value:    tokenStr,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Now().Add(authCookieTTL),
		})
		writeJSON(w, 200, authStatusResp{AuthRequired: false, User: token.Name})
	default:
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET/POST"))
	}
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
