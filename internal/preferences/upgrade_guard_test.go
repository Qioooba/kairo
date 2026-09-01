package preferences

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFuturePreferencesAreNeverOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	original := []byte(`{"version":99,"global":{},"users":{},"future":"keep"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(path).Put("user", "module", json.RawMessage(`{"x":1}`)); err == nil {
		t.Fatal("future preference format must reject writes")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("future preferences were overwritten")
	}
}
