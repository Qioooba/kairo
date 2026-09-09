package httpserver

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestCodegenOperationsRejectNonAdmin(t *testing.T) {
	srv := newTestServerWithAuth(t, databaseTokens())
	for _, action := range []string{"detect-jdk", "scan-project", "preview", "generate", "download-zip", "push-project"} {
		w := doRequestWithToken(srv, http.MethodPost, "/api/wscodegen/"+action, rbacUserToken, map[string]any{})
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: expected 403, got %d", action, w.Code)
		}
	}
}

func TestDirectorySyncPreservesEmptyDirectories(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(left, "folder", "nested", "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	srv, _, _, _ := newTestServer(t)
	r, err := srv.performCompareSync(context.Background(), compareSyncReq{Left: compareSourceSpec{Kind: "local", Path: left}, Right: compareSourceSpec{Kind: "local", Path: right}, Direction: "right", Items: []compareSyncItem{{RelPath: "folder"}}}, nil)
	if err != nil || r.Failed != 0 {
		t.Fatalf("sync=%+v error=%v", r, err)
	}
	if r.Copied != 0 || r.Directories != 3 {
		t.Fatalf("expected three directories and no files, got %+v", r)
	}
	if info, err := os.Stat(filepath.Join(right, "folder", "nested", "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory missing: %v", err)
	}
}
