package httpserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHTTPCasesFutureVersionIsNeverOverwritten(t *testing.T) {
	srv, mgr, _, _ := newTestServer(t)
	path := filepath.Join(mgr.Get().DataDir(), "http_cases.json")
	original := []byte(`{"version":99,"cases":[],"envs":[],"future":"keep"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := srv.loadHTTPCases()
	if err == nil {
		t.Fatal("future HTTP case format must be rejected")
	}
	if err := srv.saveHTTPCases(f); err == nil {
		t.Fatal("low-level save must also reject a future file")
	}
	resp := doRequest(srv, "POST", "/api/http/cases", map[string]any{"case": map[string]any{"name": "new"}})
	if resp.Code != 500 {
		t.Fatalf("future format should block handler write, status=%d body=%s", resp.Code, resp.Body.String())
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("future HTTP cases file was overwritten")
	}
}
