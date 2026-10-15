package svc

import (
	"os"
	osuser "os/user"
	"path/filepath"
	"testing"
)

// Moving a site to another PHP version used to leave the old version's pool
// behind; both pools wanted the same socket and the new version's FPM refused
// to start, taking every site on it down.
func TestRemoveStalePools(t *testing.T) {
	root := t.TempDir()
	put := func(ver, name string) string {
		dir := filepath.Join(root, ver, "fpm", "pool.d")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := put("8.4", "aegis-example.com.conf")
	keep := put("7.4", "aegis-example.com.conf")
	other := put("8.4", "aegis-other.example.com.conf")
	sibling := put("8.4", "aegis-example.com.conf.bak")

	changed := removeStalePools(root, "example.com", "7.4")
	if len(changed) != 1 || changed[0] != "8.4" {
		t.Fatalf("changed = %v", changed)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("stale pool still present")
	}
	for _, p := range []string{keep, other, sibling} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("wrongly removed %s", p)
		}
	}
	if got := removeStalePools(root, "example.com", "7.4"); len(got) != 0 {
		t.Errorf("second run should be a no-op, got %v", got)
	}
}

// open_basedir forbids /tmp, so PHP's default upload dir made every upload fail
// with "Missing a temporary folder". The account gets a private dir under the
// one extra path open_basedir allows.
func TestPHPTmpDirInsideOpenBasedirAndPrivate(t *testing.T) {
	home := t.TempDir()
	if got, want := phpTmpDir(home, "alice"), filepath.Join(home, "tmp", "alice"); got != want {
		t.Fatalf("phpTmpDir = %q, want %q", got, want)
	}
	if !ReservedAccountName("tmp") {
		t.Error("tmp must be reserved: <home>/tmp is the shared parent")
	}
	cur, err := osuser.Current()
	if err != nil {
		t.Skip("no current user")
	}
	if err := ensurePHPTmp(home, cur.Username); err != nil {
		t.Fatal(err)
	}
	// idempotent
	if err := ensurePHPTmp(home, cur.Username); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(phpTmpDir(home, cur.Username))
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("tmp dir = %v, %v", fi, err)
	}
}
