package main

import (
	"os"
	"strings"
	"testing"

	"kairo/internal/httpserver"
)

func TestEmbeddedVERSIONMatchesRepoFile(t *testing.T) {
	want, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	got := strings.TrimSpace(versionFile)
	if got != strings.TrimSpace(string(want)) {
		t.Fatalf("embed VERSION=%q, file=%q", got, strings.TrimSpace(string(want)))
	}
	if !strings.HasPrefix(got, "v") {
		t.Fatalf("VERSION 应以 v 开头, got %q", got)
	}
}

func TestInitAppliesEmbeddedVersionWhenSentinel(t *testing.T) {
	if httpserver.Version == "dev" {
		t.Fatal("main init 应把 VERSION embed 填进 httpserver.Version")
	}
	if httpserver.Version != strings.TrimSpace(versionFile) {
		t.Fatalf("httpserver.Version=%q, embed=%q", httpserver.Version, strings.TrimSpace(versionFile))
	}
}
