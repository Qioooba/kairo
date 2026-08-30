package dbconsole

import (
	"path/filepath"
	"testing"

	"kairo/internal/testutil"
)

func testSource(name string) Source {
	return Source{
		Name:           name,
		Kind:           KindRedis,
		Host:           "127.0.0.1",
		Port:           6379,
		AllowedUsers:   []string{" alice ", "ALICE", "bob"},
		MaxResultBytes: 1 << 20,
	}
}

func TestStoreRoundTripAndAtomicFailure(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Save(testSource("production-cache"))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || len(created.AllowedUsers) != 2 || created.AllowedUsers[0] != "alice" {
		t.Fatalf("defaults/normalization failed: %#v", created)
	}
	raw := testutil.ReadPrivateFile(t, filepath.Join(dir, "database-sources.json"))
	if len(raw) == 0 {
		t.Fatal("source file must be non-empty")
	}

	// Point the writer at an existing directory so Rename fails. The update must
	// not leak into the in-memory snapshot when the durable write did not commit.
	store.path = dir
	changed := created
	changed.Name = "must-not-commit"
	if _, err := store.Save(changed); err == nil {
		t.Fatal("expected durable write failure")
	}
	got, ok := store.Get(created.ID)
	if !ok || got.Name != created.Name {
		t.Fatalf("memory changed after failed write: %#v", got)
	}
}

func TestStoreRejectsDuplicateName(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(testSource("Primary")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(testSource("primary")); err == nil {
		t.Fatal("case-insensitive duplicate name should be rejected")
	}
}

func TestSourceUserAllowedIsFailClosed(t *testing.T) {
	source := Source{}
	if source.UserAllowed("alice", "user") {
		t.Fatal("empty allowed_users must not grant regular users")
	}
	if !source.UserAllowed("alice", "admin") {
		t.Fatal("admin must always be allowed")
	}
	source.AllowedUsers = []string{"*"}
	if !source.UserAllowed("alice", "user") {
		t.Fatal("explicit wildcard should grant regular users")
	}
}
