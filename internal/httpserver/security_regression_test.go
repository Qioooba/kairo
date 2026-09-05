package httpserver

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"kairo/internal/dbconsole"
)

func TestDatabaseExportCannotBecomeWriteEndpoint(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	source, err := srv.database.Store().Save(dbconsole.Source{
		Name: "writable-dev", Kind: dbconsole.KindMySQL, Host: "127.0.0.1", Port: 3306,
		Username: "admin", Database: "app", Environment: "development",
		ReadOnly: false, ReadOnlyConfigured: true, AllowedUsers: []string{"user1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"user": rbacUserToken, "admin": rbacAdminToken} {
		t.Run(name, func(t *testing.T) {
			w := doRequestWithToken(srv, http.MethodPost, "/api/database/export", token, databaseQueryRequest{
				SourceID: source.ID, SQL: "UPDATE accounts SET balance = 0", Confirm: true,
			})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("export mutation reached execution: status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestWASPackEndpointsRequireAdminWhenAuthEnabled(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	for _, endpoint := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/waspack/preview"},
		{http.MethodPost, "/api/waspack/build"},
		{http.MethodPost, "/api/waspack/extract"},
		{http.MethodPost, "/api/waspack/package"},
		{http.MethodPost, "/api/waspack/zip"},
		{http.MethodPost, "/api/waspack/replace-token"},
		{http.MethodGet, "/api/waspack/history"},
		{http.MethodDelete, "/api/waspack/history/not-found"},
	} {
		w := doRequestWithToken(srv, endpoint.method, endpoint.path, rbacUserToken, map[string]any{})
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s status=%d body=%s", endpoint.method, endpoint.path, w.Code, w.Body.String())
		}
	}
}

func TestWASPackReplaceTokenIsBoundAndSingleUse(t *testing.T) {
	manager := newWASPackReplaceTokenManager()
	token, err := manager.issue(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if manager.consume(t.TempDir(), token) {
		t.Fatal("replace token was accepted for a different output directory")
	}
	target := t.TempDir()
	token, err = manager.issue(target)
	if err != nil {
		t.Fatal(err)
	}
	if !manager.consume(target, token) {
		t.Fatal("fresh token was rejected for its bound directory")
	}
	if manager.consume(target, token) {
		t.Fatal("replace token was reusable")
	}
}

func TestCompareJobManagerRejectsNinthConcurrentJob(t *testing.T) {
	manager := newCompareJobManager()
	jobs := make([]*compareJob, 0, compareJobLimit)
	for i := 0; i < compareJobLimit; i++ {
		job, _, err := manager.tryCreate(context.Background())
		if err != nil {
			t.Fatalf("job %d: %v", i, err)
		}
		jobs = append(jobs, job)
	}
	if _, _, err := manager.tryCreate(context.Background()); err == nil || !strings.Contains(err.Error(), "并发") {
		t.Fatalf("ninth job should be rate-limited, got %v", err)
	}
	for _, job := range jobs {
		job.release()
	}
}

func TestCompareRemoteEntryNameRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../evil", "a/b", `a\b`, "/absolute", "C:/drive"} {
		if _, err := cleanCompareEntryName(name); err == nil {
			t.Errorf("accepted unsafe remote entry name %q", name)
		}
	}
	if got, err := cleanCompareEntryName("legal:name.txt"); err != nil || got != "legal:name.txt" {
		t.Fatalf("legal unix name rejected: got=%q err=%v", got, err)
	}
}
