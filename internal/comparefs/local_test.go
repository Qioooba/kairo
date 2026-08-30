package comparefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalWriteAtomicAndConflict(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "nested", "target.txt")
	fsys := NewLocal()
	if err := fsys.WriteAtomic(context.Background(), name, strings.NewReader("first"), WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	entry, err := fsys.Stat(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	stale := entry.Version()
	if err := os.WriteFile(name, []byte("changed elsewhere"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteAtomic(context.Background(), name, strings.NewReader("overwrite"), WriteOptions{Expected: &stale}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	raw, _ := os.ReadFile(name)
	if string(raw) != "changed elsewhere" {
		t.Fatalf("conflict must not overwrite target: %q", raw)
	}
}

func TestLocalWriteAtomicBackup(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "target.txt")
	if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsys := NewLocal()
	if err := fsys.WriteAtomic(context.Background(), name, strings.NewReader("new"), WriteOptions{Backup: true}); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(name + ".kairo-backup-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("backup missing: %v %v", matches, err)
	}
	raw, _ := os.ReadFile(matches[0])
	if string(raw) != "old" {
		t.Fatalf("backup=%q", raw)
	}
}

func TestLocalWriteAtomicPreservesModTime(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "preserve.txt")
	want := time.Date(2025, 4, 3, 2, 1, 0, 0, time.Local)
	fsys := NewLocal()
	if err := fsys.WriteAtomic(context.Background(), name, strings.NewReader("value"), WriteOptions{ModTime: want}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(want) {
		t.Fatalf("mtime=%s want=%s", info.ModTime(), want)
	}
}
