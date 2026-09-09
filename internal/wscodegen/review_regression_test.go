package wscodegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLateConflictLeavesNoGeneratedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Existing.java"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := writeFiles(root, []GeneratedFile{{RelPath: "New.java", Content: "new"}, {RelPath: "Existing.java", Content: "replace"}}, false)
	if err == nil {
		t.Fatal("expected conflict")
	}
	if _, err := os.Stat(filepath.Join(root, "New.java")); !os.IsNotExist(err) {
		t.Fatal("partial generation left a file")
	}
	b, err := os.ReadFile(filepath.Join(root, "Existing.java"))
	if err != nil || string(b) != "keep" {
		t.Fatal("existing content changed")
	}
}

func TestExplicitInvalidJDKNeverFallsBack(t *testing.T) {
	_, err := planTool(Request{JDKHome: filepath.Join(t.TempDir(), "missing"), Engine: EngineAxis1}, resolvedWSDL{}, t.TempDir(), false)
	if err == nil {
		t.Fatal("expected invalid explicit JDK error")
	}
}
