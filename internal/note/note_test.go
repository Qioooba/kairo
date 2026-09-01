package note

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestManager(t *testing.T) (*Manager, *Store) {
	t.Helper()
	s := NewStore(filepath.Join(t.TempDir(), "notes.json"))
	m, err := NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	return m, s
}

func TestManagerCRUDAndPersistence(t *testing.T) {
	m, s := newTestManager(t)
	created, err := m.Add(Note{Title: " 排障 ", Body: "line 1\nline 2", Floating: true})
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.Color != "yellow" || created.Title != "排障" {
		t.Fatalf("unexpected created note: %+v", created)
	}
	body := "updated"
	green := "green"
	updated, err := m.Patch(created.ID, Patch{BaseRevision: 1, Body: &body, Color: &green})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Body != body || updated.Color != green {
		t.Fatalf("unexpected updated note: %+v", updated)
	}

	m2, err := NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m2.Get(created.ID)
	if !ok || got.Body != body || got.Revision != 2 {
		t.Fatalf("round trip failed: %+v, ok=%v", got, ok)
	}
	if err := m2.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := m2.Get(created.ID); ok {
		t.Fatal("deleted note still exists")
	}
}

func TestManagerConflict(t *testing.T) {
	m, _ := newTestManager(t)
	n, err := m.Add(Note{Body: "a"})
	if err != nil {
		t.Fatal(err)
	}
	body := "b"
	_, err = m.Patch(n.ID, Patch{BaseRevision: 99, Body: &body})
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("want ConflictError, got %v", err)
	}
	if conflict.Current.Body != "a" || conflict.Current.Revision != 1 {
		t.Fatalf("bad current value: %+v", conflict.Current)
	}
}

func TestOnlyOneBrowserFloatingNote(t *testing.T) {
	m, _ := newTestManager(t)
	a, err := m.Add(Note{Body: "a", Floating: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Add(Note{Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	b, err = m.Patch(b.ID, Patch{BaseRevision: b.Revision, Floating: &yes})
	if err != nil {
		t.Fatal(err)
	}
	gotA, _ := m.Get(a.ID)
	if gotA.Floating || !b.Floating || gotA.Revision != 2 {
		t.Fatalf("floating invariant failed: a=%+v b=%+v", gotA, b)
	}
}

func TestListFilterSortAndClone(t *testing.T) {
	m, _ := newTestManager(t)
	base := time.Date(2026, 8, 30, 12, 0, 0, 0, time.Local)
	m.now = func() time.Time { return base }
	a, _ := m.Add(Note{Title: "alpha", Body: "request id", Pinned: false})
	m.now = func() time.Time { return base.Add(time.Minute) }
	b, _ := m.Add(Note{Title: "beta", Body: "host", Pinned: true, Desktop: &DesktopLayout{Visible: true}})
	got := m.List(Filter{})
	if len(got) != 2 || got[0].ID != b.ID || got[1].ID != a.ID {
		t.Fatalf("bad sort: %+v", got)
	}
	got[0].Desktop.Width = 999
	original, _ := m.Get(b.ID)
	if original.Desktop.Width == 999 {
		t.Fatal("desktop layout leaked through clone")
	}
	q := m.List(Filter{Query: "REQUEST"})
	if len(q) != 1 || q[0].ID != a.ID {
		t.Fatalf("bad query result: %+v", q)
	}
	visible := m.List(Filter{DesktopOnly: true})
	if len(visible) != 1 || visible[0].ID != b.ID {
		t.Fatalf("bad desktop filter: %+v", visible)
	}
}

func TestValidationAndLayoutNormalization(t *testing.T) {
	n := Note{Title: strings.Repeat("x", MaxTitleRunes+1), Color: "yellow"}
	if err := n.Validate(); err == nil {
		t.Fatal("expected title validation error")
	}
	l := &DesktopLayout{XRatio: -2, YRatio: 4, Width: 1, Height: 4000}
	n = Note{Body: "ok", Desktop: l}
	n.Normalize()
	if l.XRatio != 0 || l.YRatio != 1 || l.Width != 240 || l.Height != 1000 {
		t.Fatalf("layout not normalized: %+v", l)
	}
}

func TestStoreCorruptBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.json")
	if err := os.WriteFile(path, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	if _, err := s.Load(); err == nil {
		t.Fatal("expected corrupt load error")
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestAddDefaultsToListOnly(t *testing.T) {
	m, _ := newTestManager(t)
	n, err := m.Add(Note{Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if n.Floating || n.Desktop != nil {
		t.Fatalf("plain create must stay list-only: %+v", n)
	}
}

func TestDesktopVisibleLimit(t *testing.T) {
	m, _ := newTestManager(t)
	for i := 0; i < MaxDesktopVisible; i++ {
		if _, err := m.Add(Note{Body: fmt.Sprintf("d%d", i), Desktop: DefaultDesktop()}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	if _, err := m.Add(Note{Body: "overflow", Desktop: DefaultDesktop()}); !errors.Is(err, ErrDesktopLimit) {
		t.Fatalf("want ErrDesktopLimit, got %v", err)
	}
	plain, err := m.Add(Note{Body: "not desktop"})
	if err != nil {
		t.Fatal(err)
	}
	layout := DefaultDesktop()
	if _, err := m.Patch(plain.ID, Patch{BaseRevision: plain.Revision, Desktop: &layout}); !errors.Is(err, ErrDesktopLimit) {
		t.Fatalf("patch want ErrDesktopLimit, got %v", err)
	}
}

func TestSubscription(t *testing.T) {
	m, _ := newTestManager(t)
	ch, cancel := m.Subscribe()
	defer cancel()
	n, err := m.Add(Note{Body: "event"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if ev.Kind != "created" || ev.Note == nil || ev.Note.ID != n.ID {
			t.Fatalf("bad event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("event timeout")
	}
}
