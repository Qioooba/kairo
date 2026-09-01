package preferences

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSeparatesUsersAndNamespaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	s := NewStore(path)
	if err := s.Put("local", "waspack", json.RawMessage(`{"project_dir":"/a"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("alice", "waspack", json.RawMessage(`{"project_dir":"/b"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("local", "compare", json.RawMessage(`{"mode":"side"}`)); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get("local", "waspack")
	var value map[string]string
	decodeErr := json.Unmarshal(got, &value)
	if err != nil || decodeErr != nil || !ok || value["project_dir"] != "/a" {
		t.Fatalf("got=%s ok=%v err=%v", got, ok, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if !json.Valid(raw) {
		t.Fatal("invalid persisted JSON")
	}
}

func TestStoreMigratesLegacyGlobalDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"tail":{"highlights":["ERROR"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	global, err := s.Global()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := global["tail"]; !ok {
		t.Fatalf("legacy global preference lost: %#v", global)
	}
	if err := s.Put("local", "database", json.RawMessage(`{"last_source":"db1"}`)); err != nil {
		t.Fatal(err)
	}
	global, err = s.Global()
	if err != nil || global["tail"] == nil {
		t.Fatalf("global=%#v err=%v", global, err)
	}
}

func TestStoreRejectsInvalidNamespaceAndJSON(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "preferences.json"))
	if err := s.Put("local", "../bad", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected invalid namespace error")
	}
	if err := s.Put("local", "waspack", json.RawMessage(`{`)); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}
