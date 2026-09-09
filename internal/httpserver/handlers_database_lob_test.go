package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kairo/internal/dbconsole"
)

func TestDatabaseLOBRequiresSignedIdentity(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	for _, body := range []databaseLobRequest{{SourceID: "src", Owner: "HR", Table: "DOCS", Column: "BODY", RowID: "fake"}, {Token: "tampered"}} {
		w := doRequestWithToken(srv, http.MethodPost, "/api/database/lob", rbacAdminToken, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("unsigned identity accepted: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestDatabaseLOBRejectsAnotherUsersTransaction(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), authUserKey, &authUser{Name: "someone-else"}))
	token, err := dbconsole.GenerateSignedLOBToken("src", "reader", "HR", "DOCS", "BODY", "CLOB", "", map[string]any{"ID": 1}, []string{"ID"}, scopedDatabaseSessionID(r, "tab-1"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	w := doRequestWithToken(srv, http.MethodPost, "/api/database/lob", rbacUserToken, databaseLobRequest{Token: token})
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign transaction accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestDatabaseLOBFailureIsNotSuccessfulEmptyDownload(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	source, err := srv.database.Store().Save(dbconsole.Source{Name: "unavailable Oracle", Kind: dbconsole.KindOracle, Host: "127.0.0.1", Port: 1, Username: "reader", OracleService: "XE", AllowedUsers: []string{"user1"}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), authUserKey, &authUser{Name: "user1"}))
	token, err := dbconsole.GenerateSignedLOBToken(source.ID, source.Username, "HR", "DOCS", "BODY", "CLOB", "", map[string]any{"ID": 1}, []string{"ID"}, scopedDatabaseSessionID(r, "expired-session"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	w := doRequestWithToken(srv, http.MethodPost, "/api/database/lob", rbacUserToken, databaseLobRequest{Token: token, SessionID: "forged-session"})
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Disposition") != "" {
		t.Fatal("error returned as download")
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), "事务已结束") {
		t.Fatalf("unexpected failure: %s", w.Body.String())
	}
}

func TestDatabaseLOBTokenLazyIssue(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	source, err := srv.database.Store().Save(dbconsole.Source{Name: "lab Oracle", Kind: dbconsole.KindOracle, Host: "127.0.0.1", Port: 1521, Username: "HR", OracleService: "XE", AllowedUsers: []string{"user1"}})
	if err != nil {
		t.Fatal(err)
	}

	reqBody := databaseLobRequest{
		SourceID: source.ID,
		Table:    "DOCS",
		Column:   "CONTENT",
		Keys:     map[string]any{"ID": 1001},
	}
	w := doRequestWithToken(srv, http.MethodPost, "/api/database/lob/token", rbacUserToken, reqBody)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	token, ok := resp["token"].(string)
	if !ok || token == "" {
		t.Fatalf("missing token in response: %v", resp)
	}

	payload, ref, err := dbconsole.VerifySignedLOBToken(token)
	if err != nil {
		t.Fatalf("issued token verification failed: %v", err)
	}
	if payload.Table != "DOCS" || payload.Column != "CONTENT" || ref.Table != "DOCS" {
		t.Fatalf("unexpected payload or ref: %+v %+v", payload, ref)
	}
}

