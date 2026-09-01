package pet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFuturePetStateIsNeverRebuiltOrOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pet.json")
	original := []byte(`{"v":99,"skin":0,"future":"keep"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(DefaultRules(), path, make([]byte, 32), nil)
	if engine != nil || !errors.Is(err, ErrFutureStateVersion) {
		t.Fatalf("future pet state must block old engine: engine=%v err=%v", engine, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("future pet state was overwritten")
	}
}
