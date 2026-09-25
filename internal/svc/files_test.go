package svc

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"aegis/internal/config"
	"aegis/internal/store"
)

func newFilesT(t *testing.T) (*Files, *store.User) {
	t.Helper()
	home := t.TempDir()
	cfg := config.Default()
	f := NewFiles(cfg)
	u := &store.User{Username: "tester", HomeDir: home}
	return f, u
}

func TestResolveSafe(t *testing.T) {
	f, u := newFilesT(t)
	good := []string{"/", "", "index.html", "/public", "/public/index.php", "sub/dir/file.txt"}
	for _, p := range good {
		abs, err := f.Resolve(u, p)
		if err != nil {
			t.Errorf("Resolve(%q) error: %v", p, err)
			continue
		}
		if !filepathHasPrefix(abs, u.HomeDir) {
			t.Errorf("Resolve(%q) escaped home: %s", p, abs)
		}
	}
}

func TestResolveTraversalSanitized(t *testing.T) {
	f, u := newFilesT(t)
	// Dot-dot segments are collapsed against the virtual root, so they can
	// never climb above the user's home: every resolution must stay inside.
	bad := []string{"../etc/passwd", "/../../etc/passwd", "..", "../../..", "a/../../etc"}
	for _, p := range bad {
		abs, err := f.Resolve(u, p)
		if err != nil {
			t.Errorf("Resolve(%q) error: %v", p, err)
			continue
		}
		if !filepathHasPrefix(abs, u.HomeDir) {
			t.Errorf("Resolve(%q) escaped home: %s", p, abs)
		}
	}
}

func TestResolveSymlinkEscapeRejected(t *testing.T) {
	f, u := newFilesT(t)
	// Create a symlink inside home pointing outside.
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(u.HomeDir, "escape")); err != nil {
		t.Skip("symlink not permitted")
	}
	if _, err := f.Resolve(u, "/escape/secret"); err == nil {
		t.Error("symlink escape should be rejected")
	}
}

func TestFileCRUD(t *testing.T) {
	f, u := newFilesT(t)
	if err := f.Write(u, "/public/hello.txt", []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := f.Read(u, "/public/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hi" {
		t.Errorf("read = %q", data)
	}
	entries, err := f.List(u, "/public")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "hello.txt" {
		t.Fatalf("list = %+v", entries)
	}
	if err := f.Rename(u, "/public/hello.txt", "/public/goodbye.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(u, "/public/hello.txt"); err == nil {
		t.Error("old name should be gone")
	}
	if err := f.Chmod(u, "/public/goodbye.txt", "600", false); err != nil {
		t.Fatal(err)
	}
	st, _ := f.Stat(u, "/public/goodbye.txt")
	if st.Mode != "600" {
		t.Errorf("chmod failed, mode = %s", st.Mode)
	}
	// mkdir + delete.
	if err := f.Mkdir(u, "/public/deep/nested", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(u, "/public/deep"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.List(u, "/public/deep"); err == nil {
		t.Error("deleted dir should be gone")
	}
}

func TestChmodRecursiveAppliesToAllDescendants(t *testing.T) {
	f, u := newFilesT(t)
	if err := f.Write(u, "/d/a.txt", []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(u, "/d/sub/b.txt", []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Chmod(u, "/d", "700", true); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/d", "/d/a.txt", "/d/sub", "/d/sub/b.txt"} {
		st, err := f.Stat(u, p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if st.Mode != "700" {
			t.Errorf("recursive chmod: %s mode = %s, want 700", p, st.Mode)
		}
	}
}

func TestChmodRecursiveSkipsSymlinks(t *testing.T) {
	f, u := newFilesT(t)
	if err := f.Mkdir(u, "/d", 0o755); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "target.txt")
	if err := os.WriteFile(outsideFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(u.HomeDir, "d", "link")); err != nil {
		t.Skip("symlink not permitted")
	}
	before, err := os.Stat(outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Chmod(u, "/d", "700", true); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Errorf("recursive chmod followed a symlink: outside file mode changed %v -> %v", before.Mode().Perm(), after.Mode().Perm())
	}
}

func TestChownRecursiveSetsAllDescendants(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root; dev container runs as root")
	}
	f, u := newFilesT(t)
	if err := f.Write(u, "/d/a.txt", []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(u, "/d/sub/b.txt", []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Chown(u, "/d", "root", "root", true); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/d", "/d/a.txt", "/d/sub", "/d/sub/b.txt"} {
		st, err := f.Stat(u, p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if st.Owner != "root" || st.Group != "root" {
			t.Errorf("recursive chown: %s owner:group = %s:%s, want root:root", p, st.Owner, st.Group)
		}
	}
}

func TestListCreatesMissingHomeDir(t *testing.T) {
	// Mirrors the bootstrap admin account: a HomeDir value that has never
	// been provisioned on disk (no real system account, no `useradd -m`).
	// Browsing the file manager for the first time must self-heal by
	// creating it, not surface a raw "stat: no such file or directory".
	cfg := config.Default()
	f := NewFiles(cfg)
	home := filepath.Join(t.TempDir(), "admin")
	u := &store.User{Username: "admin", HomeDir: home}

	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("test setup: home dir should not exist yet, stat err = %v", err)
	}
	entries, err := f.List(u, "")
	if err != nil {
		t.Fatalf("List on a missing home dir should self-heal, got: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("freshly created home dir should be empty, got %+v", entries)
	}
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		t.Errorf("home dir was not created: stat err = %v", err)
	}

	// A missing subdirectory (as opposed to the home root itself) must
	// still error rather than being silently recreated.
	if _, err := f.List(u, "/does-not-exist"); err == nil {
		t.Error("List on a missing non-root subdirectory should still error")
	}
}

func TestSearchFinds(t *testing.T) {
	f, u := newFilesT(t)
	_ = f.Write(u, "/www/index.html", []byte("x"), 0o644)
	_ = f.Write(u, "/www/secret.log", []byte("x"), 0o644)
	hits, err := f.Search(u, "/", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("search hits = %v", hits)
	}
}

// TestSearchCapDoesNotError guards against the 200-result cap being treated
// as a search failure — hitting the cap should still return the first 200
// matches with a nil error, not discard them (see Search's errSearchLimit
// handling).
func TestSearchCapDoesNotError(t *testing.T) {
	f, u := newFilesT(t)
	for i := 0; i < 250; i++ {
		_ = f.Write(u, "/many/match-"+strconv.Itoa(i)+".txt", []byte("x"), 0o644)
	}
	hits, err := f.Search(u, "/", "match")
	if err != nil {
		t.Fatalf("Search returned an error at the cap instead of truncated results: %v", err)
	}
	if len(hits) != 200 {
		t.Fatalf("expected 200 capped hits, got %d", len(hits))
	}
}

func filepathHasPrefix(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (len(rel) > 2 && rel[:3] != ".."+string(filepath.Separator))
}
