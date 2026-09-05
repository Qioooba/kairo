package waspack

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestHistoryStoreRetentionFiltersAndCorruptIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "历史", "waspack-history.json")
	store := NewHistoryStore(path)
	store.retention = 2
	for i, operation := range []string{"build", "extract", "zip"} {
		record := NewHistoryRecord(operation, Request{PackageName: "包" + string(rune('A'+i)), Manifest: "./a"})
		record.Status = "success"
		if err := store.Append(record); err != nil {
			t.Fatal(err)
		}
	}
	records, err := store.List(20, "success", "zip")
	if err != nil || len(records) != 1 || records[0].Operation != "zip" {
		t.Fatalf("filtered history = %#v, err=%v", records, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "zip") {
		t.Fatalf("history should contain newest record: %s", raw)
	}
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := store.Append(NewHistoryRecord("build", Request{})); err == nil {
		t.Fatal("corrupt history must reject append")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("corrupt history must never be overwritten")
	}
}

func TestHistoryStoreConcurrentAppendAndDigest(t *testing.T) {
	dir := t.TempDir()
	store := NewHistoryStore(filepath.Join(dir, "history.json"))
	const count = 24
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			record := NewHistoryRecord("build", Request{OutputDir: dir, PackageName: "Demo"})
			record.Status = "success"
			record.Files = i
			if err := store.Append(record); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	records, err := store.List(count+1, "", "")
	if err != nil || len(records) != count {
		t.Fatalf("concurrent records=%d err=%v", len(records), err)
	}
	file := filepath.Join(dir, "产物.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := DigestArtifact("text", file)
	if err != nil || digest.Size != 5 || digest.SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("digest=%#v err=%v", digest, err)
	}
	if _, err := DigestArtifact("missing", filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing digest err=%v", err)
	}
}
