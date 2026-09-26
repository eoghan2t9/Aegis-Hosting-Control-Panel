//go:build linux

package svc

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedAccount registers a fake account whose "home" is dir, so the tests can
// use a temp directory. The identity is a real unprivileged account, so the
// kernel-level checks are real.
func seedAccount(t *testing.T, name, home string) {
	t.Helper()
	uid, gid, groups := nobodyIDs(t)
	accountMu.Lock()
	accountCache[name] = cachedAccount{a: account{uid: uid, gid: gid, groups: groups, home: home}, at: time.Now().Add(time.Hour)}
	accountMu.Unlock()
	t.Cleanup(func() {
		accountMu.Lock()
		delete(accountCache, name)
		accountMu.Unlock()
	})
}

func mustWrite(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

// The native web server is root and a customer controls the symlinks in their
// own web root. Reproduced live: a link to /etc/shadow returned HTTP 200.
func TestOpenDocFileConfinesSymlinksToTheOwnersHome(t *testing.T) {
	home := accessibleTempDir(t, 0o755)
	outside := accessibleTempDir(t, 0o755) // another "customer" or system dir, world-readable

	mustWrite(t, filepath.Join(home, "public", "index.html"), "hello", 0o644)
	mustWrite(t, filepath.Join(home, "storage", "app.css"), "body{}", 0o644)
	mustWrite(t, filepath.Join(outside, "world-readable.txt"), "another tenant", 0o644)
	mustWrite(t, filepath.Join(outside, "root-only.txt"), "root secret", 0o600)

	link := func(name, target string) string {
		p := filepath.Join(home, "public", name)
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	inHome := link("storage", filepath.Join(home, "storage")) // Laravel-style link inside the home
	toOtherTenant := link("other.txt", filepath.Join(outside, "world-readable.txt"))
	toRootOnly := link("secret.txt", filepath.Join(outside, "root-only.txt"))
	toShadow := link("shadow.txt", "/etc/shadow")

	seedAccount(t, "zz-doc-test", home)

	// Ordinary file, and a link that stays inside the home, still work.
	fh, _, err := openDocFile("zz-doc-test", filepath.Join(home, "public", "index.html"))
	if err != nil {
		t.Fatalf("a normal file was refused: %v", err)
	}
	b, _ := io.ReadAll(fh)
	fh.Close()
	if string(b) != "hello" {
		t.Errorf("read %q", b)
	}
	if fh, _, err := openDocFile("zz-doc-test", filepath.Join(inHome, "app.css")); err != nil {
		t.Errorf("a symlink that stays inside the home was refused: %v", err)
	} else {
		fh.Close()
	}

	// Everything that leads out is refused. The two "outside" cases are readable
	// by the account at the OS level, so only the containment check stops them.
	if _, _, err := openDocFile("zz-doc-test", toOtherTenant); !errors.Is(err, ErrForbidden) {
		t.Errorf("link to a world-readable file outside the home: err = %v, want ErrForbidden", err)
	}
	// Root-only files are stopped earlier, by the account's own permissions.
	for name, p := range map[string]string{"root-only": toRootOnly, "/etc/shadow": toShadow} {
		if fh, _, err := openDocFile("zz-doc-test", p); err == nil {
			fh.Close()
			t.Errorf("%s: opened through a symlink", name)
		}
	}

	// Directories are not files.
	if _, _, err := openDocFile("zz-doc-test", filepath.Join(home, "public")); !errors.Is(err, errNotRegularFile) {
		t.Errorf("a directory: err = %v, want errNotRegularFile", err)
	}
}

func TestStatAsCannotSeeWhatTheAccountCannot(t *testing.T) {
	home := accessibleTempDir(t, 0o755)
	seedAccount(t, "zz-stat-test", home)

	closed := accessibleTempDir(t, 0o700) // only root may enter
	mustWrite(t, filepath.Join(closed, "x"), "x", 0o644)
	if _, err := os.Stat(filepath.Join(closed, "x")); err != nil {
		t.Fatalf("root cannot stat it either: %v", err)
	}
	if _, err := statAs("zz-stat-test", filepath.Join(closed, "x")); err == nil {
		t.Error("statAs revealed a path inside a root-only directory")
	}
	if _, err := statAs("no-such-account-zz", "/"); err == nil {
		t.Error("an unknown account must not fall back to root")
	}
}
