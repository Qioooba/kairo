package reminder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreDoesNotOverwriteFutureVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reminders.json")
	original := []byte(`{"version":99,"reminders":[],"future":"keep"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(path).Save(nil); err == nil {
		t.Fatal("future reminder format must reject writes")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("future reminder file was overwritten")
	}
}
