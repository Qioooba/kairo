package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestNamespacedPreferencesPersist(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, http.MethodPut, "/api/preferences/waspack", map[string]any{
		"project_dir": "/work/credit", "auto_pair": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(srv, http.MethodGet, "/api/preferences/waspack", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Exists bool           `json:"exists"`
		Value  map[string]any `json:"value"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Exists || got.Value["project_dir"] != "/work/credit" {
		t.Fatalf("unexpected preference: %#v", got)
	}
}

func TestNamespacedPreferencesAreUserScoped(t *testing.T) {
	srv := newTestServerWithAuth(t, rbacTokens())
	if w := doRequestWithToken(srv, http.MethodPut, "/api/preferences/database", rbacAdminToken, map[string]any{"last_source": "admin-db"}); w.Code != 200 {
		t.Fatalf("admin PUT status=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequestWithToken(srv, http.MethodPut, "/api/preferences/database", rbacUserToken, map[string]any{"last_source": "user-db"}); w.Code != 200 {
		t.Fatalf("user PUT status=%d body=%s", w.Code, w.Body.String())
	}
	w := doRequestWithToken(srv, http.MethodGet, "/api/preferences/database", rbacAdminToken, nil)
	if w.Code != 200 || !stringsContainsJSON(w.Body.Bytes(), "admin-db") || stringsContainsJSON(w.Body.Bytes(), "user-db") {
		t.Fatalf("admin GET status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLegacyGlobalPreferencesRemainCompatible(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, http.MethodPut, "/api/preferences", map[string]any{"tail": map[string]any{"highlights": []any{}}})
	if w.Code != 200 {
		t.Fatalf("PUT status=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(srv, http.MethodGet, "/api/preferences", nil)
	if w.Code != 200 || !stringsContainsJSON(w.Body.Bytes(), "tail") {
		t.Fatalf("GET status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestNamespacedPreferencesRejectSensitiveFields(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	for _, body := range []map[string]any{
		{"password": "secret"},
		{"source": map[string]any{"wsdl_content": "<definitions/>"}},
		{"manifest": "WEB-INF/web.xml"},
		{"wsdl_url": "http://user:password@example.test/service?wsdl"},
	} {
		w := doRequest(srv, http.MethodPut, "/api/preferences/wscodegen", body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%v status=%d response=%s", body, w.Code, w.Body.String())
		}
	}
}

func stringsContainsJSON(raw []byte, value string) bool {
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return false
	}
	encoded, _ := json.Marshal(decoded)
	return bytes.Contains(encoded, []byte(value))
}
