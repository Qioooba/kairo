package preferences

import "testing"

func TestReadErrorMustNotBecomeEmptyPreferences(t *testing.T) {
	// Reading a directory fails on every supported platform, even as admin.
	s := NewStore(t.TempDir())
	if _, err := s.Global(); err == nil {
		t.Fatal("read error was silently converted to empty preferences")
	}
}
