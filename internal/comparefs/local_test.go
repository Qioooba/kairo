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

func TestLocalAllowedVolumeRootAndSiblingBoundary(t *testing.T) {
	dir := t.TempDir()
	root := filepath.VolumeName(dir) + string(os.PathSeparator)
	if !localPathAllowed(dir, []string{root}) {
		t.Fatalf("volume root %q must allow %q", root, dir)
	}
	if localPathAllowed(dir+"-outside", []string{dir}) {
		t.Fatal("directory prefix must not admit siblings")
	}
}

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

func TestLocalPathsAreAbsolute(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile("relative.txt", []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsys := NewLocal()
	entry, err := fsys.Stat(context.Background(), "relative.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "relative.txt")
	if entry.Path != want {
		t.Fatalf("entry path=%q want absolute path %q", entry.Path, want)
	}
	if got := fsys.Join(".", "nested/file.txt"); got != filepath.Join(root, "nested", "file.txt") {
		t.Fatalf("joined path=%q", got)
	}
}

func TestLocalWriteAtomicPreservesPermissions(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "script.sh")
	if err := os.WriteFile(name, []byte("#!/bin/sh\necho hi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	infoBefore, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	fsys := NewLocal()
	if err := fsys.WriteAtomic(context.Background(), name, strings.NewReader("#!/bin/sh\necho updated\n"), WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	infoAfter, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if infoAfter.Mode().Perm() != infoBefore.Mode().Perm() {
		t.Fatalf("permissions modified: before=%v after=%v", infoBefore.Mode().Perm(), infoAfter.Mode().Perm())
	}
}
