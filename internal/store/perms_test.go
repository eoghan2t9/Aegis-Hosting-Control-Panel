package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// A new database must be 0600 even under a permissive umask, and so must any
// SQLite -wal/-shm sidecar that exists.
func TestNewCreatesDatabase0600(t *testing.T) {
	old := syscall.Umask(0) // worst case: nothing masked
	defer syscall.Umask(old)

	path := filepath.Join(t.TempDir(), "new.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if m := mode(t, path); m != 0o600 {
		t.Errorf("new database mode = %o, want 600", m)
	}
	for _, side := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(side); err == nil {
			if m := mode(t, side); m != 0o600 {
				t.Errorf("%s mode = %o, want 600", filepath.Base(side), m)
			}
		}
	}
}

// An existing world-readable database (as left by older versions) is
// tightened the next time the store opens it, and its data survives.
func TestNewTightensExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if m := mode(t, path); m != 0o600 {
		t.Errorf("existing database mode = %o after New, want 600", m)
	}
}

func TestIsFilePath(t *testing.T) {
	for path, want := range map[string]bool{
		"/var/lib/aegis/aegis.db": true,
		"relative.db":             true,
		":memory:":                false,
		"file:aegis.db?mode=rw":   false,
		"":                        false,
	} {
		if got := isFilePath(path); got != want {
			t.Errorf("isFilePath(%q) = %v, want %v", path, got, want)
		}
	}
}
