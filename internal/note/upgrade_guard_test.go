package note

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreDoesNotOverwriteFutureVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.json")
	original := []byte(`{"version":99,"notes":[],"future":"keep"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(path).Save([]Note{{ID: "old"}}); err == nil {
		t.Fatal("future note format must reject writes")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("future note file was overwritten")
	}
}
