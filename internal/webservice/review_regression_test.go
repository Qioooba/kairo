package webservice

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFailedProjectLoadDoesNotBecomeEmptyCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wsdl_projects.json")
	before := []byte(`{"version":99,"items":[{"id":"keep"}]}`)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir)
	for i := 0; i < 2; i++ {
		if _, err := s.ListProjects(); err == nil {
			t.Fatalf("load %d silently accepted unreadable store", i)
		}
	}
	if _, err := s.SaveProject(WSDLProject{Name: "new"}); err == nil {
		t.Fatal("save must not overwrite unreadable data")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("durable file changed: %s, %v", after, err)
	}
}

func TestWarmCacheCannotOverwriteFutureWebServiceFile(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, err := s.SaveProject(WSDLProject{Name: "original"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "wsdl_projects.json")
	before := []byte(`{"version":99,"items":[],"keep":"future"}`)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProject(WSDLProject{Name: "new"}); err == nil {
		t.Fatal("warm cache overwrote future file")
	}
	if _, err := s.ListProjects(); err == nil {
		t.Fatal("failed save left a misleading warm cache")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("durable file changed: %s, %v", after, err)
	}
}
