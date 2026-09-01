package dbconsole

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFutureDatabaseSourcesAreNeverOpenedForWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "database-sources.json")
	original := []byte(`{"version":99,"sources":[],"future":"keep"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if store, err := NewStore(dir); err == nil || store != nil {
		t.Fatalf("future database source format must be rejected: store=%v err=%v", store, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("future database sources were changed")
	}
}
