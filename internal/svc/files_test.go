package svc

import (
	"os"
	"path/filepath"
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
	if err := f.Chmod(u, "/public/goodbye.txt", "600"); err != nil {
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

func filepathHasPrefix(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (len(rel) > 2 && rel[:3] != ".."+string(filepath.Separator))
}
