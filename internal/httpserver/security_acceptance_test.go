package httpserver

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kairo/internal/config"
	"kairo/internal/credentials"
	"kairo/internal/dbconsole"
)

// TestT071_CrossUserResourceAccessAndRevocation 验证不同用户访问隔离、权限回收与猜测ID防护 (T071)
func TestT071_CrossUserResourceAccessAndRevocation(t *testing.T) {
	tokens := []config.AuthToken{
		{Name: "alice", Token: "alice-token-0123456789abcdef", Role: "user"},
		{Name: "bob", Token: "bob-token-01234567890abcdef1", Role: "user"},
		{Name: "admin1", Token: "admin-token-0123456789abcdef", Role: "admin"},
	}
	srv := newTestServerWithAuth(t, tokens)

	// 1. 创建仅授权 alice 的数据源
	source, err := srv.database.Store().Save(dbconsole.Source{
		Name: "alice-only-db", Kind: dbconsole.KindMySQL, Host: "127.0.0.1", Port: 3306,
		Username: "app_user", Database: "app", Environment: "development",
		AllowedUsers: []string{"alice"},
	})
	if err != nil {
		t.Fatalf("save source: %v", err)
	}

	// 2. alice 能够通过鉴权访问此数据源 (返回有效状态或因为网络未连返回502，但绝非403/404)
	wAlice := doRequestWithToken(srv, http.MethodPost, "/api/database/query", tokens[0].Token, databaseQueryRequest{
		SourceID: source.ID, SQL: "SELECT 1",
	})
	if wAlice.Code == http.StatusForbidden || wAlice.Code == http.StatusNotFound {
		t.Fatalf("alice should be authorized, got status=%d body=%s", wAlice.Code, wAlice.Body.String())
	}

	// 3. bob 尝试访问 alice 的数据源：必须被拦截并返回 403 Forbidden
	wBob := doRequestWithToken(srv, http.MethodPost, "/api/database/query", tokens[1].Token, databaseQueryRequest{
		SourceID: source.ID, SQL: "SELECT 1",
	})
	if wBob.Code != http.StatusForbidden {
		t.Fatalf("bob should be forbidden from accessing alice's source, got status=%d body=%s", wBob.Code, wBob.Body.String())
	}

	// 4. 任意用户猜测不存在的资源 ID：返回 404
	wGuessed := doRequestWithToken(srv, http.MethodPost, "/api/database/query", tokens[0].Token, databaseQueryRequest{
		SourceID: "guessed-random-source-id-999", SQL: "SELECT 1",
	})
	if wGuessed.Code != http.StatusNotFound {
		t.Fatalf("guessed source id should return 404, got status=%d body=%s", wGuessed.Code, wGuessed.Body.String())
	}

	// 5. 权限回收：将 source 的授权修改为仅 bob
	source.AllowedUsers = []string{"bob"}
	updatedSource, err := srv.database.Store().Save(source)
	if err != nil {
		t.Fatalf("update source: %v", err)
	}

	// 6. 权限回收后：alice 立即被拒绝访问 (403)，而无需客户端协作
	wAliceRevoked := doRequestWithToken(srv, http.MethodPost, "/api/database/query", tokens[0].Token, databaseQueryRequest{
		SourceID: updatedSource.ID, SQL: "SELECT 1",
	})
	if wAliceRevoked.Code != http.StatusForbidden {
		t.Fatalf("alice should be immediately forbidden after permission revocation, got status=%d body=%s", wAliceRevoked.Code, wAliceRevoked.Body.String())
	}
}

// TestT072_HostOriginAndWebSocketSecurity 验证跨站 Origin/Host 策略与 WebSocket 握手防护 (T072)
func TestT072_HostOriginAndWebSocketSecurity(t *testing.T) {
	srv, _, _, _ := newTestServer(t)

	// 1. 恶意跨站 Origin 请求被拒绝
	reqEvilOrigin := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	reqEvilOrigin.Header.Set("Origin", "http://evil.attacker.com")
	reqEvilOrigin.Host = "127.0.0.1:18090"
	wEvil := httptest.NewRecorder()
	srv.ServeHTTP(wEvil, reqEvilOrigin)

	if wEvil.Code != http.StatusForbidden {
		t.Fatalf("cross-origin request with evil origin must be forbidden, got %d", wEvil.Code)
	}

	// 2. Sandboxed null Origin 被拒绝
	reqNullOrigin := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	reqNullOrigin.Header.Set("Origin", "null")
	reqNullOrigin.Host = "127.0.0.1:18090"
	wNull := httptest.NewRecorder()
	srv.ServeHTTP(wNull, reqNullOrigin)

	if wNull.Code != http.StatusForbidden {
		t.Fatalf("null origin must be forbidden, got %d", wNull.Code)
	}

	// 3. 本地合法同源请求放行
	reqLocal := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	reqLocal.Header.Set("Origin", "http://127.0.0.1:18090")
	reqLocal.Host = "127.0.0.1:18090"
	wLocal := httptest.NewRecorder()
	srv.ServeHTTP(wLocal, reqLocal)

	if wLocal.Code != http.StatusOK {
		t.Fatalf("local origin request should be allowed, got %d body=%s", wLocal.Code, wLocal.Body.String())
	}

	// 4. WebSocket Upgrader CheckOrigin 检验
	if sshShellUpgrader.CheckOrigin(reqEvilOrigin) {
		t.Fatalf("websocket upgrader must reject evil origin")
	}
	if sshShellUpgrader.CheckOrigin(reqNullOrigin) {
		t.Fatalf("websocket upgrader must reject null origin")
	}
	if !sshShellUpgrader.CheckOrigin(reqLocal) {
		t.Fatalf("websocket upgrader must allow local origin")
	}
}

// TestT073_ErrorAndLogSanitization 验证凭据与连接秘密在错误与日志中被完全脱敏 (T073)
func TestT073_ErrorAndLogSanitization(t *testing.T) {
	srv, _, _, _ := newTestServer(t)

	secretPassword := "SuperSecretP@ssword123!#"
	source, err := srv.database.Store().Save(dbconsole.Source{
		Name: "secret-db", Kind: dbconsole.KindOracle, Host: "10.0.0.1", Port: 1521,
		Username: "sec_admin", Database: "finance", Environment: "production",
		OracleConnectBy: "service_name", OracleService: "finance",
	})
	if err != nil {
		t.Fatalf("save source: %v", err)
	}

	// 保存数据库凭据
	if err := credentials.SaveResource(dbconsole.CredentialNamespace, source.ID, source.CredentialUser(), secretPassword); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	defer func() {
		_ = credentials.ClearResource(dbconsole.CredentialNamespace, source.ID, source.CredentialUser())
	}()

	// 模拟驱动在连接失败时返回包含明文密码和 URL 编码密码的错误字符串
	rawDriverErr := errors.New("failed to connect oracle://sec_admin:SuperSecretP%40ssword123%21%23@10.0.0.1:1521/finance: invalid credentials SuperSecretP@ssword123!#")

	sanitizedErr := srv.databaseSafeError(source, rawDriverErr)
	if sanitizedErr == nil {
		t.Fatalf("expected non-nil sanitized error")
	}

	sanitizedMsg := sanitizedErr.Error()
	t.Logf("Sanitized error message:\n%s", sanitizedMsg)

	// 绝不能出现明文密码或其 URL 编码形式
	if strings.Contains(sanitizedMsg, secretPassword) {
		t.Errorf("Sanitized message leaked plain secret password: %s", sanitizedMsg)
	}
	if strings.Contains(sanitizedMsg, "SuperSecretP%40ssword123%21%23") {
		t.Errorf("Sanitized message leaked URL encoded secret password: %s", sanitizedMsg)
	}

	// 应当包含 [REDACTED] 脱敏标记
	if !strings.Contains(sanitizedMsg, "[REDACTED]") {
		t.Errorf("Sanitized message should contain [REDACTED] placeholder: %s", sanitizedMsg)
	}
}
