package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"kairo/internal/config"
	"kairo/internal/dbconsole"
)

const rbacOtherUserToken = "other-token-0123456789abcdef"

func databaseTokens() []config.AuthToken {
	return []config.AuthToken{
		{Name: "admin1", Token: rbacAdminToken, Role: "admin"},
		{Name: "user1", Token: rbacUserToken, Role: "user"},
		{Name: "user2", Token: rbacOtherUserToken, Role: "user"},
	}
}

func TestDatabaseSourcesCRUDAndRBAC(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	body := databaseSourceRequest{Source: dbconsole.Source{
		Name: "production-cache", Kind: dbconsole.KindRedis, Host: "127.0.0.1",
		Port: 6379, AllowedUsers: []string{"user1"},
	}}
	w := doRequestWithToken(srv, http.MethodPost, "/api/database/sources", rbacAdminToken, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create source: status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(strings.ToLower(w.Body.String()), `"password"`) {
		t.Fatalf("source response leaked password field: %s", w.Body.String())
	}
	var created struct {
		Source dbconsole.SourceView `json:"source"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Source.ID == "" || created.Source.HasPassword {
		t.Fatalf("unexpected source response: %#v", created.Source)
	}

	w = doRequestWithToken(srv, http.MethodGet, "/api/database/sources", rbacUserToken, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), created.Source.ID) {
		t.Fatalf("allowed user cannot list source: status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "127.0.0.1") || strings.Contains(w.Body.String(), "allowed_users") {
		t.Fatalf("regular-user source view exposed connection metadata: %s", w.Body.String())
	}
	w = doRequestWithToken(srv, http.MethodGet, "/api/database/sources", rbacOtherUserToken, nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), created.Source.ID) {
		t.Fatalf("unauthorized source was visible: status=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequestWithToken(srv, http.MethodDelete, "/api/database/sources/"+created.Source.ID, rbacUserToken, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("regular user deleted source: status=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequestWithToken(srv, http.MethodDelete, "/api/database/sources/"+created.Source.ID, rbacAdminToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin delete source: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDatabaseQueryRejectsMutationBeforeConnect(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	source, err := srv.database.Store().Save(dbconsole.Source{
		Name: "mysql-report", Kind: dbconsole.KindMySQL, Host: "127.0.0.1", Port: 3306,
		Username: "readonly", Database: "reporting", AllowedUsers: []string{"user1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := doRequestWithToken(srv, http.MethodPost, "/api/database/query", rbacUserToken, databaseQueryRequest{
		SourceID: source.ID, SQL: "WITH x AS (SELECT 1) UPDATE accounts SET balance = 0",
	})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "只读") {
		t.Fatalf("mutation should be rejected before driver connect: status=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequestWithToken(srv, http.MethodPost, "/api/database/query", rbacOtherUserToken, databaseQueryRequest{
		SourceID: source.ID, SQL: "SELECT 1",
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("unauthorized query should be rejected: status=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequestWithToken(srv, http.MethodPost, "/api/database/explain", rbacUserToken, databaseQueryRequest{
		SourceID: source.ID, SQL: "DELETE FROM accounts",
	})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "只读") {
		t.Fatalf("explain mutation should be rejected: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDatabaseCSVEncodingAndFormulaProtection(t *testing.T) {
	if got := databaseCSVValue(map[string]any{"kind": "binary", "bytes": 3}); !strings.Contains(got, `"kind":"binary"`) {
		t.Fatalf("structured value should be JSON in CSV: %q", got)
	}
	for _, input := range []string{"=cmd", "+1", "-1", "@x", "\tformula", "\rformula"} {
		if got := safeCSVCell(input); got != "'"+input {
			t.Fatalf("formula was not protected: input=%q got=%q", input, got)
		}
	}
	if got := safeCSVCell("中文"); got != "中文" {
		t.Fatalf("normal UTF-8 text changed: %q", got)
	}
	if got := safeCSVCell("/api/original/path"); got != "/api/original/path" {
		t.Fatalf("slash-containing database text changed: %q", got)
	}
}
