package svc

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zipEntry = struct {
	data string
	mode os.FileMode
}

// writeZip builds an archive at path. mode==0 leaves the entry's mode unset.
func writeZip(t *testing.T, path string, entries map[string]zipEntry) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	for name, e := range entries {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if e.mode != 0 {
			hdr.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// A symlink that already sits in the customer's home used to redirect a
// harmless-looking entry ("a/file") to wherever the link pointed, and the panel
// (root) wrote the file there. Reproduced live: a root-owned file appeared
// outside the home.
func TestUnzipRefusesToWriteThroughASymlink(t *testing.T) {
	f, u := newFilesT(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(u.HomeDir, "a")); err != nil {
		t.Fatal(err)
	}
	writeZip(t, filepath.Join(u.HomeDir, "x.zip"), map[string]zipEntry{
		"a/planted.txt": {data: "written outside the home"},
	})
	if err := f.Unzip(u, "/x.zip", "/"); err == nil {
		t.Error("expected the archive to be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "planted.txt")); err == nil {
		t.Fatal("a file was written outside the home through the symlink")
	}
}

// Setuid/setgid bits in an archive's attributes used to be applied verbatim,
// producing setuid-root files inside the customer's own home.
func TestUnzipDropsSpecialModeBits(t *testing.T) {
	f, u := newFilesT(t)
	writeZip(t, filepath.Join(u.HomeDir, "x.zip"), map[string]zipEntry{
		"suid":  {data: "x", mode: os.ModeSetuid | 0o755},
		"sgid":  {data: "x", mode: os.ModeSetgid | 0o755},
		"plain": {data: "x", mode: 0o644},
	})
	if err := f.Unzip(u, "/x.zip", "/out"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"suid", "sgid", "plain"} {
		st, err := os.Stat(filepath.Join(u.HomeDir, "out", name))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			t.Errorf("%s was extracted with special bits: %v", name, st.Mode())
		}
	}
}

func TestUnzipNeverCreatesSymlinksFromAnArchive(t *testing.T) {
	f, u := newFilesT(t)
	writeZip(t, filepath.Join(u.HomeDir, "x.zip"), map[string]zipEntry{
		"link": {data: "/etc/passwd", mode: os.ModeSymlink | 0o777},
	})
	if err := f.Unzip(u, "/x.zip", "/out"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(filepath.Join(u.HomeDir, "out", "link")); err == nil && st.Mode()&os.ModeSymlink != 0 {
		t.Error("an archive entry created a symlink")
	}
}

func TestUnzipStillRejectsPlainTraversalAndAllowsDotDotNames(t *testing.T) {
	f, u := newFilesT(t)
	writeZip(t, filepath.Join(u.HomeDir, "bad.zip"), map[string]zipEntry{"../../escape.txt": {data: "x"}})
	if err := f.Unzip(u, "/bad.zip", "/out"); err == nil {
		t.Error("a ../ entry must be rejected")
	}
	// A name that merely starts with two dots is a legitimate file name.
	writeZip(t, filepath.Join(u.HomeDir, "ok.zip"), map[string]zipEntry{"..hidden": {data: "x"}})
	if err := f.Unzip(u, "/ok.zip", "/out2"); err != nil {
		t.Errorf("legitimate file name ..hidden was rejected: %v", err)
	}
}

func TestChmodRejectsSpecialBits(t *testing.T) {
	f, u := newFilesT(t)
	if err := f.Write(u, "/f", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"4755", "2755", "1777", "40000755", "7777"} {
		if err := f.Chmod(u, "/f", m, false); err == nil {
			t.Errorf("chmod %s should be rejected", m)
		}
	}
	if err := f.Chmod(u, "/f", "755", false); err != nil {
		t.Errorf("chmod 755 must still work: %v", err)
	}
}

func TestWriteIgnoresSpecialBitsInTheRequestedMode(t *testing.T) {
	f, u := newFilesT(t)
	if err := f.Write(u, "/f", []byte("x"), os.ModeSetuid|0o755); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(u.HomeDir, "f"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSetuid != 0 {
		t.Error("setuid bit was honoured on write")
	}
}

// Following a symlink while zipping would put the target's contents (possibly a
// file outside the home) into an archive the customer then downloads.
func TestZipSkipsSymlinks(t *testing.T) {
	f, u := newFilesT(t)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(u, "/site/real.txt", []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(u.HomeDir, "site", "leak.txt")); err != nil {
		t.Fatal(err)
	}
	if err := f.Zip(u, "/site", "out.zip"); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(filepath.Join(u.HomeDir, "out.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, zf := range zr.File {
		names = append(names, zf.Name)
	}
	joined := strings.Join(names, ",")
	if strings.Contains(joined, "leak.txt") {
		t.Errorf("the symlink was followed into the archive: %s", joined)
	}
	if !strings.Contains(joined, "real.txt") {
		t.Errorf("the real file is missing from the archive: %s", joined)
	}
}
