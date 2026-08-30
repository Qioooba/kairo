package httpserver

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"kairo/internal/note"
)

func attachTestNotes(t *testing.T, srv *Server) *note.Manager {
	t.Helper()
	m, err := note.NewManager(note.NewStore(filepath.Join(t.TempDir(), "notes.json")))
	if err != nil {
		t.Fatal(err)
	}
	srv.notes = m
	return m
}

func TestNotesAPI_CRUDAndConflict(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	attachTestNotes(t, srv)

	w := doRequest(srv, http.MethodPost, "/api/notes", map[string]any{
		"title": "排障", "body": "request-id=abc", "floating": true,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var created note.Note
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Revision != 1 || !created.Floating {
		t.Fatalf("bad created note: %+v", created)
	}

	w = doRequest(srv, http.MethodPatch, "/api/notes/"+created.ID, map[string]any{
		"base_revision": 1, "body": "changed", "color": "blue",
	})
	if w.Code != 200 {
		t.Fatalf("patch status=%d body=%s", w.Code, w.Body.String())
	}
	var updated note.Note
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Revision != 2 || updated.Body != "changed" || updated.Color != "blue" {
		t.Fatalf("bad update: %+v", updated)
	}

	w = doRequest(srv, http.MethodPatch, "/api/notes/"+created.ID, map[string]any{
		"base_revision": 1, "body": "stale",
	})
	if w.Code != http.StatusConflict || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("want 409 JSON, got %d %s", w.Code, w.Body.String())
	}

	w = doRequest(srv, http.MethodGet, "/api/notes?q=CHANGED&archived=false", nil)
	if w.Code != 200 {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	var list []note.Note
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("bad list: %+v", list)
	}

	w = doRequest(srv, http.MethodDelete, "/api/notes/"+created.ID, nil)
	if w.Code != 200 {
		t.Fatalf("delete status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestNotesAPI_DesktopNullAndActions(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	attachTestNotes(t, srv)
	w := doRequest(srv, http.MethodPost, "/api/notes", map[string]any{"body": "x"})
	var n note.Note
	_ = json.Unmarshal(w.Body.Bytes(), &n)

	w = doRequest(srv, http.MethodPost, "/api/notes/"+n.ID+"/desktop", map[string]any{
		"base_revision": n.Revision,
		"desktop":       map[string]any{"visible": true, "x_ratio": .5, "y_ratio": .5},
	})
	if w.Code != 200 {
		t.Fatalf("desktop status=%d body=%s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &n)
	if n.Desktop == nil || !n.Desktop.Visible || n.Desktop.Width != 340 {
		t.Fatalf("desktop not normalized: %+v", n)
	}

	w = doRequest(srv, http.MethodPost, "/api/notes/"+n.ID+"/archive", map[string]any{
		"base_revision": n.Revision, "archived": true,
	})
	if w.Code != 200 {
		t.Fatalf("archive status=%d body=%s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &n)
	if !n.Archived {
		t.Fatal("archive action did not update note")
	}
}

func TestNotesAPI_Unavailable(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	w := doRequest(srv, http.MethodGet, "/api/notes", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
