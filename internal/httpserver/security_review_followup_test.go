package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kairo/internal/config"
)

func TestReviewCompletedJobsAreBounded(t *testing.T) {
	m := newCompareJobManager()
	for i := 0; i < compareJobRetainedLimit+4; i++ {
		m.starts = nil // simulate submissions in separate rate windows
		j, _, err := m.tryCreate(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		j.mu.Lock()
		j.Status = "completed"
		j.mu.Unlock()
		j.release()
	}
	if len(m.jobs) > compareJobRetainedLimit {
		t.Fatalf("retained %d jobs", len(m.jobs))
	}
}

func TestReviewMixedWildcardDoesNotDisableGateway(t *testing.T) {
	a := config.AppConfig{CompareAllowedRoots: []string{filepath.Join(t.TempDir(), "allowed"), "*"}}
	if a.ComparePathAllowed(t.TempDir()) {
		t.Fatal("mixed wildcard disables gateway")
	}
}

func TestReviewDisabledAuthRequiresLoopbackBinding(t *testing.T) {
	s, manager, _, _ := newTestServer(t)
	cfg := manager.Get().Clone()
	cfg.App.Host = "192.0.2.5"
	// Inject an invalid binding to exercise runtime defense in depth; normal
	// configuration validation already rejects this unauthenticated host.
	s.cfg = config.NewManager(cfg, manager.Path(), t.TempDir())
	w := doRequest(s, http.MethodGet, "/api/config", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated network binding returned %d", w.Code)
	}
}

func TestReviewDiffRejectsExcessiveLinesBeforeSplitting(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	w := doRequest(s, http.MethodPost, "/api/diff/compare", map[string]any{"left": strings.Repeat("\n", 200000), "right": "x"})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("line bomb returned %d", w.Code)
	}
}

func TestReviewConfigRedactsPasswordsWithoutMutatingConfig(t *testing.T) {
	systems := []config.SystemConfig{{Servers: []config.ServerConfig{{ID: "server", Password: "secret-password"}}}}
	raw, err := json.Marshal(sanitizeSystems(systems))
	if err != nil || strings.Contains(string(raw), "secret-password") || strings.Contains(string(raw), "password") {
		t.Fatalf("password exposed: %s (%v)", raw, err)
	}
	if systems[0].Servers[0].Password != "secret-password" {
		t.Fatal("live SSH credential was changed")
	}
}

func TestReviewClientIPIgnoresForwardingHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	r.RemoteAddr = "192.0.2.5:1234"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("X-Real-IP", "127.0.0.1")
	if got := clientIP(r); got != "192.0.2.5" {
		t.Fatalf("spoofed client IP: %s", got)
	}
}

func TestReviewCompareRoutesRequireAdmin(t *testing.T) {
	s := newTestServerWithAuth(t, databaseTokens())
	for _, action := range []string{"list", "read", "write", "copy", "sync", "scan-level", "test", "jobs/unknown", "file-diff", "folder-scan", "deep-check"} {
		w := doRequestWithToken(s, http.MethodPost, "/api/compare/"+action, rbacUserToken, map[string]any{})
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", action, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s allows caching", action)
		}
	}
}

func TestReviewLegacyCompareRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	app := config.AppConfig{CompareAllowedRoots: []string{root}}
	if legacyComparePathAllowed(&app, filepath.Join(link, "secret.txt")) {
		t.Fatal("symlink escaped allowlist")
	}
}
