package config

import (
	"path/filepath"
	"testing"
)

func TestResolveLocationExplicitConfigWins(t *testing.T) {
	t.Setenv(homeEnv, filepath.Join(t.TempDir(), "ignored"))
	want := filepath.Join(t.TempDir(), "custom.yaml")
	got, err := ResolveLocation(want, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigPath != want || got.RootDir != filepath.Dir(want) {
		t.Fatalf("location=%+v want config=%q", got, want)
	}
}

func TestResolveLocationUsesKairoHome(t *testing.T) {
	root := filepath.Join(t.TempDir(), "profile")
	t.Setenv(homeEnv, root)
	got, err := ResolveLocation("", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.RootDir != root || got.ConfigPath != filepath.Join(root, "config.yaml") {
		t.Fatalf("location=%+v", got)
	}
}
