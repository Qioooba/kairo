// Package testutil contains cross-package test assertions that encode Kairo's
// platform contracts once instead of duplicating OS assumptions in each store.
package testutil

import (
	"os"
	"runtime"
	"testing"
)

// ReadPrivateFile asserts the durable-file invariants that are observable on
// the current platform and returns the content for further checks.
//
// Unix exposes owner/group/other mode bits, so 0600 is part of the contract.
// Windows security is ACL-based and os.FileMode reports synthesized 0666 even
// when callers create/chmod with 0600; comparing those bits there tests the Go
// compatibility layer, not access control. Windows tests still require a
// regular, readable, non-empty file and exercise atomic persistence separately.
func ReadPrivateFile(t testing.TB, path string) []byte {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("private file stat %q: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("private path %q is not a regular file: %s", path, info.Mode())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("private file %q mode = %o, want 0600", path, info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("private file read %q: %v", path, err)
	}
	if len(raw) == 0 {
		t.Fatalf("private file %q is empty", path)
	}
	return raw
}
