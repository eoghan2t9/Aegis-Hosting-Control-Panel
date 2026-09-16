package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aegis/internal/config"
	"aegis/internal/store"
)

func newTestDomains(t *testing.T) (*Domains, *store.User) {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{HomeRoot: home}
	d := NewDomains(cfg, nil, nil, nil, nil, nil)
	u := &store.User{Username: "alice", HomeDir: home}
	return d, u
}

// A subdomain nested inside the master domain's folder.
func TestResolveDocRootNestedUnderMaster(t *testing.T) {
	d, u := newTestDomains(t)
	got, err := d.ResolveDocRoot(u, "example.com/sub")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(u.HomeDir, "example.com", "sub")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A subdomain with its own top-level root folder.
func TestResolveDocRootOwnFolder(t *testing.T) {
	d, u := newTestDomains(t)
	got, err := d.ResolveDocRoot(u, "shop.example.com")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(u.HomeDir, "shop.example.com")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Deeper custom layouts are fine too.
func TestResolveDocRootDeepPath(t *testing.T) {
	d, u := newTestDomains(t)
	got, err := d.ResolveDocRoot(u, "sites/shop/public")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(u.HomeDir, "sites", "shop", "public")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveDocRootRejects(t *testing.T) {
	d, u := newTestDomains(t)

	// Create a symlink target to prove symlink escapes are refused.
	if err := os.MkdirAll(filepath.Join(u.HomeDir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(u.HomeDir, "real"), filepath.Join(u.HomeDir, "link")); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		"",                 // empty (caller falls back to default before this)
		".",                // home itself
		"..",               // parent
		"../other",         // traversal
		"a/../../b",        // traversal mid-path
		"/etc/nginx",       // absolute
		"~/thing",          // shell-ish; tilde is not expanded by design
		".hidden/site",     // hidden segment
		"bad name/site",    // space is legal on disk but excluded for sanity
		"link/site",        // symlink component
		"back\\slash",      // windows separator treated as invalid name
	} {
		if _, err := d.ResolveDocRoot(u, rel); err == nil {
			t.Errorf("ResolveDocRoot(%q): expected error, got none", rel)
		}
	}
}

// The home itself is rejected, but a normal nested path is not.
func TestResolveDocRootAllowsNormalAfterRejects(t *testing.T) {
	d, u := newTestDomains(t)
	if _, err := d.ResolveDocRoot(u, strings.Repeat("a", 80)); err == nil {
		// very long single segment is allowed by the resolver (fs decides);
		// this test just ensures normal paths still resolve after rejects.
		_ = err
	}
	got, err := d.ResolveDocRoot(u, "example.com/public")
	if err != nil {
		t.Fatalf("normal path rejected: %v", err)
	}
	if !strings.HasPrefix(got, u.HomeDir) {
		t.Errorf("resolved outside home: %q", got)
	}
}
