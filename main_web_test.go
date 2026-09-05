package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestWebAssetsRequireExplicitDevelopmentOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("development-assets"), 0o600); err != nil {
		t.Fatal(err)
	}
	embedded, err := resolveWebRoot("")
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(embedded, "index.html")
	if err != nil || string(data) == "development-assets" || len(data) == 0 {
		t.Fatalf("unexpected embedded assets: %v", err)
	}
	development, err := resolveWebRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err = fs.ReadFile(development, "index.html")
	if err != nil || string(data) != "development-assets" {
		t.Fatalf("explicit development directory ignored: %v", err)
	}
	if _, err = resolveWebRoot(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("invalid explicit asset directory silently accepted")
	}
}
