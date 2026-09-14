package httpserver

import (
	"encoding/json"
	"errors"
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
	for _, input := range []string{"=cmd", "+1", "@x", "\tformula", "\rformula"} {
		if got := safeCSVCell(input); got != "'"+input {
			t.Fatalf("formula was not protected: input=%q got=%q", input, got)
		}
	}
	if got := safeCSVCell("中文"); got != "中文" {
		t.Fatalf("normal UTF-8 text changed: %q", got)
	}
	for _, input := range []string{"/api/original/path", "C:\\tmp\\a.log", "-1", "2024-01-02", "a/b-c"} {
		if got := safeCSVCell(input); got != input {
			t.Fatalf("database text must stay original: input=%q got=%q", input, got)
		}
	}
}

func TestDatabaseSafeErrorTTC(t *testing.T) {
	s := &Server{}
	source := dbconsole.Source{ID: "src1", Kind: dbconsole.KindOracle}
	ttcErr := errors.New("TTC error: received code 10 during response reading")
	safeErr := s.databaseSafeError(source, ttcErr)
	if safeErr == nil {
		t.Fatal("expected error, got nil")
	}
	msg := safeErr.Error()
	if !strings.Contains(msg, "Oracle TTC 协议报文解析异常") {
		t.Fatalf("expected diagnostic prefix, got: %s", msg)
	}
	if !strings.Contains(msg, "received code 10") {
		t.Fatalf("expected original error details preserved, got: %s", msg)
	}
	if !strings.Contains(msg, "DBMS_LOB.SUBSTR") {
		t.Fatalf("expected actionable advice, got: %s", msg)
	}
}

func TestDatabaseSessionBackupFloatEditorHeight(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	payload := map[string]any{
		"activeId":     1,
		"tabSeq":       1,
		"sourceId":     "",
		"editorHeight": 169.18753051757812,
		"sessions": []map[string]any{
			{"id": 1, "sql": "SELECT 1"},
		},
		"updatedAt": 1700000000000,
	}
	w := doRequestWithToken(srv, http.MethodPost, "/api/database/sessions/backup", rbacUserToken, payload)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for backup with float editorHeight, got %d: %s", w.Code, w.Body.String())
	}

	wRestore := doRequestWithToken(srv, http.MethodGet, "/api/database/sessions/restore", rbacUserToken, nil)
	if wRestore.Code != http.StatusOK {
		t.Fatalf("expected 200 for restore, got %d: %s", wRestore.Code, wRestore.Body.String())
	}
}

func TestDatabaseSafeErrorConnection(t *testing.T) {
	s := &Server{}
	source := dbconsole.Source{
		ID:   "oracle_test",
		Kind: dbconsole.KindOracle,
		Host: "192.168.1.50",
		Port: 1521,
	}

	timeoutErr := errors.New("dial tcp 192.168.1.50:1521: i/o timeout")
	safeErr := s.databaseSafeError(source, timeoutErr)
	if safeErr == nil {
		t.Fatal("expected error, got nil")
	}
	msg := safeErr.Error()
	if !strings.Contains(msg, "[192.168.1.50:1521]") || !strings.Contains(msg, "连接数据库超时") {
		t.Fatalf("expected target host and diagnostic in timeout error, got: %s", msg)
	}

	refusedErr := errors.New("dial tcp 192.168.1.50:1521: connection refused")
	safeErr2 := s.databaseSafeError(source, refusedErr)
	if safeErr2 == nil {
		t.Fatal("expected error, got nil")
	}
	msg2 := safeErr2.Error()
	if !strings.Contains(msg2, "[192.168.1.50:1521]") || !strings.Contains(msg2, "无法连接数据库") {
		t.Fatalf("expected target host and diagnostic in connection refused error, got: %s", msg2)
	}
}

func TestDatabaseQueryDDLGate(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	// Source with AllowDDL = false
	srcNoDDL, err := srv.database.Store().Save(dbconsole.Source{
		Name: "no-ddl-source", Kind: dbconsole.KindOracle, Host: "127.0.0.1", Port: 1521,
		Username: "app", OracleService: "ORCL", AllowDDL: false, AllowedUsers: []string{"admin1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := databaseQueryRequest{
		SourceID:  srcNoDDL.ID,
		SessionID: "sess-1",
		SQL:       "CREATE TABLE test_tab (id INT)",
	}
	w := doRequestWithToken(srv, http.MethodPost, "/api/database/query", rbacAdminToken, req)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "DDL") {
		t.Fatalf("expected 403 DDL rejected when AllowDDL=false, got %d: %s", w.Code, w.Body.String())
	}

	// Source with AllowDDL = true, ReadOnly = true
	srcRO, err := srv.database.Store().Save(dbconsole.Source{
		Name: "ro-ddl-source", Kind: dbconsole.KindOracle, Host: "127.0.0.1", Port: 1521,
		Username: "app", OracleService: "ORCL", AllowDDL: true, AllowedUsers: []string{"admin1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reqRO := databaseQueryRequest{
		SourceID:  srcRO.ID,
		SessionID: "sess-2",
		SQL:       "CREATE TABLE test_tab (id INT)",
	}
	wRO := doRequestWithToken(srv, http.MethodPost, "/api/database/query", rbacAdminToken, reqRO)
	if wRO.Code != http.StatusForbidden || !strings.Contains(wRO.Body.String(), "DDL") {
		t.Fatalf("expected 403 when ReadOnly=true, got %d: %s", wRO.Code, wRO.Body.String())
	}
}

func TestDatabaseExport_PrecheckAndSafeKeys(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	src, err := srv.database.Store().Save(dbconsole.Source{
		Name: "test-src", Kind: dbconsole.KindOracle, Host: "127.0.0.1", Port: 1521,
		Username: "app", OracleService: "ORCL", AllowedUsers: []string{"admin1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. JOIN query for UPDATE export -> rejected with 422 AMBIGUOUS_ROW_LOCATOR and no attachment
	wJoin := doRequestWithToken(srv, http.MethodPost, "/api/database/export", rbacAdminToken, databaseQueryRequest{
		SourceID: src.ID,
		SQL:      "SELECT a.id, b.name FROM table_a a JOIN table_b b ON a.id = b.id",
		Format:   "update",
		Table:    "table_a",
	})
	if wJoin.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for JOIN query UPDATE export, got: %d (%s)", wJoin.Code, wJoin.Body.String())
	}
	if disp := wJoin.Header().Get("Content-Disposition"); disp != "" {
		t.Fatalf("expected no Content-Disposition on error, got: %s", disp)
	}
	if !strings.Contains(wJoin.Body.String(), "AMBIGUOUS_ROW_LOCATOR") {
		t.Fatalf("expected AMBIGUOUS_ROW_LOCATOR in body: %s", wJoin.Body.String())
	}

	// 2. Table without primary key -> rejected with 422 EXPORT_UNSAFE_KEY
	wNoPK := doRequestWithToken(srv, http.MethodPost, "/api/database/export", rbacAdminToken, databaseQueryRequest{
		SourceID: src.ID,
		SQL:      "SELECT id, note FROM audit_logs",
		Format:   "update",
		Table:    "audit_logs",
	})
	if wNoPK.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for table without primary key, got: %d (%s)", wNoPK.Code, wNoPK.Body.String())
	}
	if disp := wNoPK.Header().Get("Content-Disposition"); disp != "" {
		t.Fatalf("expected no Content-Disposition on error, got: %s", disp)
	}
	if !strings.Contains(wNoPK.Body.String(), "EXPORT_UNSAFE_KEY") && !strings.Contains(wNoPK.Body.String(), "EXPORT_DICTIONARY_FAILED") {
		t.Fatalf("expected EXPORT_UNSAFE_KEY or EXPORT_DICTIONARY_FAILED in body: %s", wNoPK.Body.String())
	}
}

func TestDatabaseExport_ParameterAndSessionContext(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	src, err := srv.database.Store().Save(dbconsole.Source{
		Name: "test-export-params", Kind: dbconsole.KindOracle, Host: "127.0.0.1", Port: 1521,
		Username: "app", OracleService: "ORCL", AllowedUsers: []string{"admin1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Invalid session ID format -> rejected with 400
	wBadSess := doRequestWithToken(srv, http.MethodPost, "/api/database/export", rbacAdminToken, databaseQueryRequest{
		SourceID:  src.ID,
		SessionID: "invalid session id with spaces!!",
		SQL:       "SELECT 1 FROM dual",
		Format:    "csv",
	})
	if wBadSess.Code != http.StatusBadRequest || !strings.Contains(wBadSess.Body.String(), "session_id") {
		t.Fatalf("expected 400 for invalid session_id, got %d: %s", wBadSess.Code, wBadSess.Body.String())
	}

	// 2. Mismatched named/positional parameters validation -> rejected with 400 PARAM_BIND_FAILED
	wBadParam := doRequestWithToken(srv, http.MethodPost, "/api/database/export", rbacAdminToken, databaseQueryRequest{
		SourceID: src.ID,
		SQL:      "SELECT id FROM users WHERE id = :id AND name = :name",
		Parameters: []dbconsole.BindParameter{
			{Name: "id", Value: 1},
			// missing :name parameter
		},
		Format: "csv",
	})
	if wBadParam.Code != http.StatusBadRequest || !strings.Contains(wBadParam.Body.String(), "PARAM_BIND_FAILED") {
		t.Fatalf("expected 400 PARAM_BIND_FAILED for missing parameter, got %d: %s", wBadParam.Code, wBadParam.Body.String())
	}
}

func TestDatabaseTransaction_TerminalStateAndUncertainCommit(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	src, err := srv.database.Store().Save(dbconsole.Source{
		Name: "test-tx-source", Kind: dbconsole.KindMySQL, Host: "127.0.0.1", Port: 3306,
		Username: "root", Database: "testdb", AllowedUsers: []string{"admin1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	rawSID := "tab-tx-test"
	fingerprint := dbconsole.SourceFingerprint(src)

	// 1. Initially no transaction exists
	wStatus := doRequestWithToken(srv, http.MethodGet, "/api/database/transaction/status?source_id="+src.ID+"&session_id="+rawSID, rbacAdminToken, nil)
	if wStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 for status, got: %d (%s)", wStatus.Code, wStatus.Body.String())
	}
	var resAbsent struct {
		OK          bool                       `json:"ok"`
		Transaction dbconsole.TransactionState `json:"transaction"`
	}
	if err := json.Unmarshal(wStatus.Body.Bytes(), &resAbsent); err != nil {
		t.Fatal(err)
	}
	if resAbsent.Transaction.Active || resAbsent.Transaction.Reason != "server_transaction_absent" {
		t.Fatalf("expected absent transaction, got %+v", resAbsent.Transaction)
	}

	scopedSID := resAbsent.Transaction.SessionID

	// 2. Set terminal state to outcome_unknown
	unknownMsg := "提交确认丢失或连接中断，事务最终状态未知，请核对目标数据，切勿盲目重试写入"
	srv.database.SetTerminalRecordForTest(src.ID, scopedSID, fingerprint, dbconsole.OutcomeUnknown, unknownMsg)

	// Verify status endpoint reflects outcome_unknown
	wStatusUnknown := doRequestWithToken(srv, http.MethodGet, "/api/database/transaction/status?source_id="+src.ID+"&session_id="+rawSID, rbacAdminToken, nil)
	if wStatusUnknown.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wStatusUnknown.Code)
	}
	var resUnknown struct {
		OK          bool                       `json:"ok"`
		Transaction dbconsole.TransactionState `json:"transaction"`
	}
	if err := json.Unmarshal(wStatusUnknown.Body.Bytes(), &resUnknown); err != nil {
		t.Fatal(err)
	}
	if resUnknown.Transaction.Active || resUnknown.Transaction.TerminalOutcome != dbconsole.OutcomeUnknown {
		t.Fatalf("expected outcome_unknown in status, got %+v", resUnknown.Transaction)
	}

	// 3. Repeated COMMIT request on outcome_unknown returns 502 with OUTCOME_UNKNOWN and retryable=false
	wCommit := doRequestWithToken(srv, http.MethodPost, "/api/database/transaction", rbacAdminToken, databaseTransactionRequest{
		SourceID:  src.ID,
		SessionID: rawSID,
		Action:    "COMMIT",
	})
	if wCommit.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 Bad Gateway for uncertain commit, got %d (%s)", wCommit.Code, wCommit.Body.String())
	}
	var errResp StructuredErrorResponse
	if err := json.Unmarshal(wCommit.Body.Bytes(), &errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Code != "OUTCOME_UNKNOWN" || errResp.EffectStatus != "unknown" || errResp.Retryable {
		t.Fatalf("expected structured error with OUTCOME_UNKNOWN non-retryable, got %+v", errResp)
	}

	// 4. ROLLBACK request on outcome_unknown succeeds and dismisses state
	wRollback := doRequestWithToken(srv, http.MethodPost, "/api/database/transaction", rbacAdminToken, databaseTransactionRequest{
		SourceID:  src.ID,
		SessionID: rawSID,
		Action:    "ROLLBACK",
	})
	if wRollback.Code != http.StatusOK {
		t.Fatalf("expected 200 for graceful rollback on outcome_unknown, got %d (%s)", wRollback.Code, wRollback.Body.String())
	}
	if !strings.Contains(wRollback.Body.String(), "最终状态未知") {
		t.Fatalf("expected summary acknowledging unknown outcome dismissal, got: %s", wRollback.Body.String())
	}

	// 5. Set terminal state to committed and verify idempotency
	srv.database.SetTerminalRecordForTest(src.ID, scopedSID, fingerprint, dbconsole.OutcomeCommitted, "事务已提交")
	wCommitRepeat := doRequestWithToken(srv, http.MethodPost, "/api/database/transaction", rbacAdminToken, databaseTransactionRequest{
		SourceID:  src.ID,
		SessionID: rawSID,
		Action:    "COMMIT",
	})
	if wCommitRepeat.Code != http.StatusOK || !strings.Contains(wCommitRepeat.Body.String(), "重复操作已忽略") {
		t.Fatalf("expected 200 idempotent ignore for committed transaction, got %d (%s)", wCommitRepeat.Code, wCommitRepeat.Body.String())
	}
}


