package httpserver

import (
	"net/http"
	"strings"
	"testing"

	"kairo/internal/config"
	"kairo/internal/dbconsole"
)

func TestDatabaseObjectStudioPreviewAndRBAC(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	source, err := srv.database.Store().Save(dbconsole.Source{
		Name: "oracle-design", Kind: dbconsole.KindOracle, Host: "127.0.0.1", Port: 1521,
		Username: "app", OracleService: "ORCL", AllowDDL: true, AllowedUsers: []string{"user1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := objectStudioRequest{SourceID: source.ID, ObjectStudioChange: dbconsole.ObjectStudioChange{
		Action: "create", ObjectType: "table", Schema: "APP", Name: "EMP",
		Columns: []dbconsole.ObjectStudioColumn{{Name: "ID", DataType: "NUMBER", PrimaryKey: true}},
	}}
	w := doRequestWithToken(srv, http.MethodPost, "/api/database/object-studio/preview", rbacUserToken, request)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "CREATE TABLE") {
		t.Fatalf("preview failed: status=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequestWithToken(srv, http.MethodPost, "/api/database/object-studio/preview", rbacOtherUserToken, request)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unauthorized preview should be rejected by source ACL: status=%d body=%s", w.Code, w.Body.String())
	}
	request.Columns[0].Name = "ID; DROP TABLE X"
	w = doRequestWithToken(srv, http.MethodPost, "/api/database/object-studio/preview", rbacUserToken, request)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unsafe object preview status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDatabaseObjectStudioApplyRequiresAdmin(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	source, err := srv.database.Store().Save(dbconsole.Source{
		Name: "oracle-design", Kind: dbconsole.KindOracle, Host: "127.0.0.1", Port: 1521,
		Username: "app", OracleService: "ORCL", AllowDDL: true, AllowedUsers: []string{"user1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := objectStudioRequest{SourceID: source.ID, ObjectStudioChange: dbconsole.ObjectStudioChange{
		Action: "create", ObjectType: "table", Schema: "APP", Name: "EMP",
		Columns: []dbconsole.ObjectStudioColumn{{Name: "ID", DataType: "NUMBER"}},
	}}
	w := doRequestWithToken(srv, http.MethodPost, "/api/database/object-studio/apply", rbacUserToken, request)
	if w.Code != http.StatusForbidden {
		t.Fatalf("regular user applied DDL: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDatabaseFunctionPreviewRejectsNonOracle(t *testing.T) {
	srv := newTestServerWithAuth(t, []config.AuthToken{{Name: "admin", Token: rbacAdminToken, Role: "admin"}})
	source, err := srv.database.Store().Save(dbconsole.Source{
		Name: "mysql-design", Kind: dbconsole.KindMySQL, Host: "127.0.0.1", Port: 3306,
		Username: "app", Database: "app", AllowDDL: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	w := doRequestWithToken(srv, http.MethodPost, "/api/database/object-studio/function/preview", rbacAdminToken, oracleFunctionRequest{
		SourceID:             source.ID,
		OracleFunctionChange: dbconsole.OracleFunctionChange{Schema: "APP", Name: "F", Source: "CREATE FUNCTION APP.F RETURN NUMBER IS BEGIN RETURN 1; END;", Confirm: true},
	})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Oracle") {
		t.Fatalf("non-Oracle function preview should be rejected: status=%d body=%s", w.Code, w.Body.String())
	}
}
