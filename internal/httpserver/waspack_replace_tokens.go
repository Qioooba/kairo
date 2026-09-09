package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"kairo/internal/waspack"
)

const waspackReplaceTokenTTL = 5 * time.Minute
const waspackReplaceTokenLimit = 256

type waspackReplaceGrant struct {
	path    string
	expires time.Time
}

type waspackReplaceTokenManager struct {
	mu     sync.Mutex
	grants map[string]waspackReplaceGrant
}

func newWASPackReplaceTokenManager() *waspackReplaceTokenManager {
	return &waspackReplaceTokenManager{grants: make(map[string]waspackReplaceGrant)}
}

func normalizeWASPackReplacePath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" || strings.ContainsAny(raw, "\x00\r\n") {
		return "", errors.New("输出目录无效")
	}
	abs, err := waspack.ValidateOutputPath(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("输出目录无效")
	}
	return filepath.Clean(abs), nil
}

func (m *waspackReplaceTokenManager) issue(rawPath string) (string, error) {
	path, err := normalizeWASPackReplacePath(rawPath)
	if err != nil {
		return "", err
	}
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	token := hex.EncodeToString(random)
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, grant := range m.grants {
		if grant.expires.Before(now) {
			delete(m.grants, key)
		}
	}
	if len(m.grants) >= waspackReplaceTokenLimit {
		return "", errors.New("覆盖确认请求过于频繁，请稍后重试")
	}
	m.grants[token] = waspackReplaceGrant{path: path, expires: now.Add(waspackReplaceTokenTTL)}
	return token, nil
}

func (m *waspackReplaceTokenManager) consume(rawPath, token string) bool {
	path, err := normalizeWASPackReplacePath(rawPath)
	if err != nil || strings.TrimSpace(token) == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	grant, ok := m.grants[token]
	delete(m.grants, token)
	if !ok || time.Now().After(grant.expires) {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(grant.path, path)
	}
	return grant.path == path
}

func (s *Server) handleWASPackReplaceToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var body struct {
		OutputDir string `json:"output_dir"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("请求体解析失败"))
		return
	}
	token, err := s.waspackReplace.issue(body.OutputDir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.audit.Write("waspack.replace_token.issue", "output", body.OutputDir, "result", "ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "replace_token": token, "expires_in_seconds": int(waspackReplaceTokenTTL.Seconds())})
}

func (s *Server) authorizeWASPackReplace(w http.ResponseWriter, req waspackReq, packReq *waspack.Request) bool {
	if strings.ToLower(strings.TrimSpace(req.OutputPolicy)) != waspack.OutputPolicyReplace {
		return true
	}
	if !req.ConfirmReplace || !s.waspackReplace.consume(req.OutputDir, req.ReplaceToken) {
		writeErr(w, http.StatusBadRequest, errors.New("覆盖输出目录需要有效的一次性确认凭据"))
		return false
	}
	packReq.ReplaceAuthorized = true
	return true
}
