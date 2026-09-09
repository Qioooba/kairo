package waspack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewHistoryManifestBudgetKeepsCompleteRecords(t *testing.T) {
	s := NewHistoryStore(filepath.Join(t.TempDir(), "history.json"))
	manifest := strings.Repeat("a", 1024*1024)
	for i := 0; i < 10; i++ {
		r := NewHistoryRecord("build", Request{Manifest: manifest})
		if err := s.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	records, err := s.List(200, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 8 {
		t.Fatalf("got %d records", len(records))
	}
	for _, r := range records {
		if r.Request.Manifest != manifest {
			t.Fatal("replay manifest was truncated")
		}
	}
}

func TestReviewProtectsAllWindowsSystemDrives(t *testing.T) {
	for _, p := range []string{`D:\Windows\System32`, `E:\Program Files\app`, `Z:\ProgramData\app`, `D:\`} {
		if !isBlockedSystemPath(p) {
			t.Errorf("system path accepted: %s", p)
		}
	}
	for _, p := range []string{`\\?\C:\Windows\x`, `\\server\share\out`} {
		for _, host := range []string{"windows", "linux"} {
			if isSafeLocalPathForGOOS(p, host) {
				t.Errorf("%s accepted %s", host, p)
			}
		}
	}
}

func TestReviewCanonicalOutputCannotEnterSystemDirectory(t *testing.T) {
	protected := "/etc"
	if os.PathSeparator == '\\' {
		protected = os.Getenv("SystemRoot")
	}
	if protected == "" {
		t.Skip("no system directory")
	}
	link := filepath.Join(t.TempDir(), "parent")
	if err := os.Symlink(protected, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ValidateOutputPath(filepath.Join(link, "kairo-test-never-create")); err == nil {
		t.Fatal("protected canonical output accepted")
	}
}

func TestReviewArchiveSourceRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	if err := os.WriteFile(src, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := openRegularFile(src)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(src, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if f, err := openRegularFile(link); err == nil {
		f.Close()
		t.Fatal("archive accepted symlink")
	}
}
