package downloads

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFutureMetadataIndexIsNeverOverwritten(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, metaIndexFile)
	original := []byte(`{"version":99,"files":{},"future":"keep"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteMeta(filepath.Join(root, "x.log"), Meta{}); err == nil {
		t.Fatal("future download index must reject writes")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("future download index was overwritten")
	}
}
