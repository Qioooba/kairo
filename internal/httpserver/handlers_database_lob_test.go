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

	srv.database.SetCachedFields(source.ID, "HR", "DOCS", []dbconsole.Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "CONTENT", DataType: "CLOB"},
	})

	reqBody := databaseLobRequest{
		SourceID:   source.ID,
		Table:      "DOCS",
		Column:     "CONTENT",
		ColumnType: "CLOB",
		PrimaryKey: []string{"ID"},
		Keys:       map[string]any{"ID": 1001},
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

	// Without PK and without ROWID, must be rejected with 400
	srv.database.SetCachedFields(source.ID, "HR", "NOPK_TABLE", []dbconsole.Field{
		{Name: "FOO", DataType: "VARCHAR2(100)"},
		{Name: "CONTENT", DataType: "CLOB"},
	})
	reqNoPK := databaseLobRequest{
		SourceID:   source.ID,
		Table:      "NOPK_TABLE",
		Column:     "CONTENT",
		ColumnType: "CLOB",
		Keys:       map[string]any{"FOO": "BAR"},
	}
	wNoPK := doRequestWithToken(srv, http.MethodPost, "/api/database/lob/token", rbacUserToken, reqNoPK)
	if wNoPK.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for table without PK/ROWID, got %d: %s", wNoPK.Code, wNoPK.Body.String())
	}
}

func TestDatabaseLOB_SessionMappingAndNoTransaction(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	source, err := srv.database.Store().Save(dbconsole.Source{Name: "test-src", Kind: dbconsole.KindOracle, Host: "127.0.0.1", Port: 1521, Username: "HR", OracleService: "XE", AllowedUsers: []string{"user1"}})
	if err != nil {
		t.Fatal(err)
	}
	srv.database.SetCachedFields(source.ID, "HR", "DOCS", []dbconsole.Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "CONTENT", DataType: "CLOB"},
	})

	// 1. Plain SELECT with raw client session ID -> server maps ID, sees no transaction -> token.SessionID is ""
	reqPlain := databaseLobRequest{
		SourceID:           source.ID,
		SessionID:          "dbtab-raw-12345",
		TransactionPending: false,
		Table:              "DOCS",
		Column:             "CONTENT",
		Keys:               map[string]any{"ID": 1},
	}
	wPlain := doRequestWithToken(srv, http.MethodPost, "/api/database/lob/token", rbacUserToken, reqPlain)
	if wPlain.Code != http.StatusOK {
		t.Fatalf("expected 200 for plain select, got %d: %s", wPlain.Code, wPlain.Body.String())
	}
	var respPlain map[string]any
	json.Unmarshal(wPlain.Body.Bytes(), &respPlain)
	payloadPlain, _, _ := dbconsole.VerifySignedLOBToken(respPlain["token"].(string))
	if payloadPlain.SessionID != "" {
		t.Fatalf("plain select token must have empty sessionID, got %q", payloadPlain.SessionID)
	}

	// 2. Client declares transaction_pending=true, but transaction is absent/expired on server -> 409 Conflict
	reqExpired := databaseLobRequest{
		SourceID:           source.ID,
		SessionID:          "dbtab-raw-12345",
		TransactionPending: true,
		Table:              "DOCS",
		Column:             "CONTENT",
		Keys:               map[string]any{"ID": 1},
	}
	wExpired := doRequestWithToken(srv, http.MethodPost, "/api/database/lob/token", rbacUserToken, reqExpired)
	if wExpired.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for expired transaction, got %d: %s", wExpired.Code, wExpired.Body.String())
	}
}

func TestDatabaseLOB_TrustedLocatorCompositePKAndROWID(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	source, err := srv.database.Store().Save(dbconsole.Source{Name: "test-src2", Kind: dbconsole.KindOracle, Host: "127.0.0.1", Port: 1521, Username: "HR", OracleService: "XE", AllowedUsers: []string{"user1"}})
	if err != nil {
		t.Fatal(err)
	}

	// Table with composite PK: TENANT_ID, DOC_ID
	srv.database.SetCachedFields(source.ID, "HR", "COMP_DOCS", []dbconsole.Field{
		{Name: "TENANT_ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "DOC_ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "BODY", DataType: "CLOB"},
	})

	// Missing DOC_ID -> must be rejected with 400
	reqMissingPart := databaseLobRequest{
		SourceID: source.ID, Table: "COMP_DOCS", Column: "BODY",
		Keys: map[string]any{"TENANT_ID": 1},
	}
	wMissing := doRequestWithToken(srv, http.MethodPost, "/api/database/lob/token", rbacUserToken, reqMissingPart)
	if wMissing.Code != http.StatusBadRequest || !strings.Contains(wMissing.Body.String(), "DOC_ID") {
		t.Fatalf("expected 400 missing composite key, got %d: %s", wMissing.Code, wMissing.Body.String())
	}

	// Full composite PK provided -> success
	reqFullPK := databaseLobRequest{
		SourceID: source.ID, Table: "COMP_DOCS", Column: "BODY",
		Keys: map[string]any{"TENANT_ID": 1, "DOC_ID": 42},
	}
	wFull := doRequestWithToken(srv, http.MethodPost, "/api/database/lob/token", rbacUserToken, reqFullPK)
	if wFull.Code != http.StatusOK {
		t.Fatalf("expected 200 for full composite PK, got %d: %s", wFull.Code, wFull.Body.String())
	}

	// Table with no PK, but valid ROWID provided -> success
	srv.database.SetCachedFields(source.ID, "HR", "LOGS", []dbconsole.Field{
		{Name: "MESSAGE", DataType: "CLOB"},
	})
	reqROWID := databaseLobRequest{
		SourceID: source.ID, Table: "LOGS", Column: "MESSAGE",
		UseRowID: true, RowID: "AAAB12AADAAAAwPAAA",
	}
	wROWID := doRequestWithToken(srv, http.MethodPost, "/api/database/lob/token", rbacUserToken, reqROWID)
	if wROWID.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid ROWID, got %d: %s", wROWID.Code, wROWID.Body.String())
	}

	// Malformed ROWID with injection chars -> rejected
	reqBadROWID := databaseLobRequest{
		SourceID: source.ID, Table: "LOGS", Column: "MESSAGE",
		UseRowID: true, RowID: "AAAB12' OR 1=1--",
	}
	wBadROWID := doRequestWithToken(srv, http.MethodPost, "/api/database/lob/token", rbacUserToken, reqBadROWID)
	if wBadROWID.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for illegal ROWID, got %d: %s", wBadROWID.Code, wBadROWID.Body.String())
	}
}

func TestDatabaseLOB_SourceConfigChangedInvalidation(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	source, err := srv.database.Store().Save(dbconsole.Source{Name: "src-mutate", Kind: dbconsole.KindOracle, Host: "127.0.0.1", Port: 1521, Username: "HR", OracleService: "XE", AllowedUsers: []string{"user1"}})
	if err != nil {
		t.Fatal(err)
	}
	srv.database.SetCachedFields(source.ID, "HR", "DOCS", []dbconsole.Field{
		{Name: "ID", DataType: "NUMBER", PrimaryKey: true},
		{Name: "CONTENT", DataType: "CLOB"},
	})

	// 1. Issue token with original source config
	wToken := doRequestWithToken(srv, http.MethodPost, "/api/database/lob/token", rbacUserToken, databaseLobRequest{
		SourceID: source.ID, Table: "DOCS", Column: "CONTENT", Keys: map[string]any{"ID": 1},
	})
	if wToken.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", wToken.Code, wToken.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(wToken.Body.Bytes(), &resp)
	token := resp["token"].(string)

	// 2. Change source configuration (e.g. port changed)
	source.Port = 1522
	_, err = srv.database.Store().Save(source)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Attempt download with old token -> must be rejected with 403 Forbidden
	wLob := doRequestWithToken(srv, http.MethodPost, "/api/database/lob", rbacUserToken, databaseLobRequest{Token: token})
	if wLob.Code != http.StatusForbidden || !strings.Contains(wLob.Body.String(), "数据源配置已变化") {
		t.Fatalf("expected 403 Forbidden for changed source config, got %d: %s", wLob.Code, wLob.Body.String())
	}
}

func TestDatabaseLOB_PlaceholderOwnerAndLocatorNormalization(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	source, err := srv.database.Store().Save(dbconsole.Source{Name: "test-src", Kind: dbconsole.KindOracle, Host: "127.0.0.1", Port: 1521, Username: "HR", OracleService: "XE", AllowedUsers: []string{"user1"}})
	if err != nil {
		t.Fatal(err)
	}
	srv.database.SetCachedFields(source.ID, "HR", "HTTP_REQ_LOG", []dbconsole.Field{
		{Name: "SEQNO", DataType: "VARCHAR2(64)", PrimaryKey: true},
		{Name: "REQUEST_BODY_CLOB", DataType: "CLOB"},
	})

	for _, badOwner := range []string{"加载中…", "加载中...", "加载失败", ""} {
		req := databaseLobRequest{
			SourceID:   source.ID,
			Owner:      badOwner,
			Table:      "HTTP_REQ_LOG",
			Column:     "REQUEST_BODY_CLOB",
			ColumnType: "OCICLOBLOCATOR",
			Keys:       map[string]any{"SEQNO": "243f21d5cc744d53b113d4e7c79ed240"},
		}
		w := doRequestWithToken(srv, http.MethodPost, "/api/database/lob/token", rbacUserToken, req)
		if w.Code != http.StatusOK {
			t.Fatalf("owner=%q: expected 200, got %d: %s", badOwner, w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		token := resp["token"].(string)
		payload, ref, err := dbconsole.VerifySignedLOBToken(token)
		if err != nil {
			t.Fatalf("owner=%q: token verification failed: %v", badOwner, err)
		}
		if payload.Owner != "HR" || ref.Owner != "HR" {
			t.Fatalf("owner=%q: expected normalized Owner HR, got payload=%q ref=%q", badOwner, payload.Owner, ref.Owner)
		}
		if payload.ColumnType != "CLOB" || ref.ColumnType != "CLOB" {
			t.Fatalf("expected normalized ColumnType CLOB, got payload=%q ref=%q", payload.ColumnType, ref.ColumnType)
		}
	}
}


