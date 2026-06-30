package httpserver

// handlers_rbac_test.go — BE-003 RBAC 测试。
//
// 验证 requireAdmin 在 /api/admin/*、/api/config/import、/api/credentials/clear
// 上的行为：
//   - auth 未启用（无 authUser）→ 放行
//   - auth 启用 + admin token → 放行
//   - auth 启用 + user token → 403

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"kairo/internal/config"
)

// newTestServerWithAuth 构造一个启用 auth 的测试 Server。
// 基于 newTestServer 的配置，再设置 auth.enabled=true + tokens。
// Manager.Replace 内部会调用 Defaults/ResolvePaths/Validate/Auth.Prepare，
// 因此传入的 tokens 必须通过校验（name 非空、token 长度 >= 16、name/token 唯一）。
func newTestServerWithAuth(t *testing.T, tokens []config.AuthToken) *Server {
	t.Helper()
	srv, mgr, _, _ := newTestServer(t)
	cfg := mgr.Get()
	cfg.Auth.Enabled = true
	cfg.Auth.Tokens = tokens
	if err := mgr.Replace(cfg); err != nil {
		t.Fatalf("启用 auth 失败: %v", err)
	}
	return srv
}

// doRequestWithToken 像 doRequest，但额外带 Authorization: Bearer <token> 头。
// token 为空时不设置该头（等价于 doRequest）。
func doRequestWithToken(srv *Server, method, path, token string, body any) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rdr)
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

// RBAC 测试用 token（长度 >= 16 满足 config.Validate 的校验）。
const (
	rbacAdminToken = "admin-token-0123456789abcdef" // admin 角色
	rbacUserToken  = "user-token-0123456789abcdef"  // user 角色
)

// rbacTokens 返回一组 admin + user token，供 auth 启用场景使用。
func rbacTokens() []config.AuthToken {
	return []config.AuthToken{
		{Name: "admin1", Token: rbacAdminToken, Role: "admin"},
		{Name: "user1", Token: rbacUserToken, Role: "user"},
	}
}

// TestRBAC_AdminCanAccessAdminServers — admin token 可访问 GET /api/admin/servers（200）。
func TestRBAC_AdminCanAccessAdminServers(t *testing.T) {
	srv := newTestServerWithAuth(t, rbacTokens())
	w := doRequestWithToken(srv, "GET", "/api/admin/servers", rbacAdminToken, nil)
	if w.Code != 200 {
		t.Errorf("admin 访问 /api/admin/servers 应 200, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestRBAC_UserBlockedFromAdminServers — user token 访问 GET /api/admin/servers 返回 403。
func TestRBAC_UserBlockedFromAdminServers(t *testing.T) {
	srv := newTestServerWithAuth(t, rbacTokens())
	w := doRequestWithToken(srv, "GET", "/api/admin/servers", rbacUserToken, nil)
	if w.Code != 403 {
		t.Errorf("user 访问 /api/admin/servers 应 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestRBAC_UserBlockedFromConfigImport — user token 调 POST /api/config/import 返回 403。
func TestRBAC_UserBlockedFromConfigImport(t *testing.T) {
	srv := newTestServerWithAuth(t, rbacTokens())
	w := doRequestWithToken(srv, "POST", "/api/config/import", rbacUserToken, nil)
	if w.Code != 403 {
		t.Errorf("user 调 /api/config/import 应 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestRBAC_UserBlockedFromCredClear — user token 调 POST /api/credentials/clear 返回 403。
func TestRBAC_UserBlockedFromCredClear(t *testing.T) {
	srv := newTestServerWithAuth(t, rbacTokens())
	w := doRequestWithToken(srv, "POST", "/api/credentials/clear", rbacUserToken, nil)
	if w.Code != 403 {
		t.Errorf("user 调 /api/credentials/clear 应 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestRBAC_AdminCanAccessConfigImport — admin token 调 POST /api/config/import。
// 空 body 也会过 requireAdmin（admin 放行），由 handler 自身返回 400（请求体为空）而非 403，
// 据此区分"被 requireAdmin 拦截"与"handler 校验失败"。
func TestRBAC_AdminCanAccessConfigImport(t *testing.T) {
	srv := newTestServerWithAuth(t, rbacTokens())
	w := doRequestWithToken(srv, "POST", "/api/config/import", rbacAdminToken, nil)
	if w.Code == 403 {
		t.Fatalf("admin 调 /api/config/import 不应被 requireAdmin 拦截 (403), body=%s", w.Body.String())
	}
	if w.Code != 400 {
		t.Errorf("admin 调 /api/config/import 空 body 应 400（请求体为空）, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestRBAC_AuthDisabled_AllowsAll — auth 未启用时（newTestServer 默认）所有 admin 路由可访问，
// 验证 requireAdmin 在无 authUser 时放行。
func TestRBAC_AuthDisabled_AllowsAll(t *testing.T) {
	srv, _, _, _ := newTestServer(t) // 默认 auth 未启用
	// GET 类 admin 路由：requireAdmin 放行 + handler 成功 → 200
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/admin/servers"},
		{"GET", "/api/admin/openers"},
		{"GET", "/api/admin/download-retention"},
	} {
		w := doRequest(srv, c.method, c.path, nil)
		if w.Code != 200 {
			t.Errorf("auth 未启用时 %s %s 应 200, got %d body=%s", c.method, c.path, w.Code, w.Body.String())
		}
	}
	// POST 类 admin 路由：requireAdmin 放行后由 handler 校验空 body → 400（非 403）
	w := doRequest(srv, "POST", "/api/config/import", nil)
	if w.Code == 403 {
		t.Errorf("auth 未启用时 POST /api/config/import 不应 403（requireAdmin 应放行）, body=%s", w.Body.String())
	}
	if w.Code != 400 {
		t.Errorf("auth 未启用时 POST /api/config/import 空 body 应 400, got %d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(srv, "POST", "/api/credentials/clear", nil)
	if w.Code == 403 {
		t.Errorf("auth 未启用时 POST /api/credentials/clear 不应 403（requireAdmin 应放行）, body=%s", w.Body.String())
	}
	if w.Code != 400 {
		t.Errorf("auth 未启用时 POST /api/credentials/clear 空 body 应 400, got %d body=%s", w.Code, w.Body.String())
	}
}
