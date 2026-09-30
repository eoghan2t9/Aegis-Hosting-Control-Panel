package svc

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestArchiveFormatFromName(t *testing.T) {
	for name, want := range map[string]string{
		"a.zip": fmtZip, "A.ZIP": fmtZip, "a.tar": fmtTar, "a.tar.gz": fmtTarGz, "a.tgz": fmtTarGz,
		"a.tar.bz2": fmtTarBz2, "a.tar.zst": fmtTarZst, "notes.txt.gz": fmtGz, "a.txt": "", "zip": "",
	} {
		if got := archiveFormat(name); got != want {
			t.Errorf("archiveFormat(%q) = %q, want %q", name, got, want)
		}
	}
}

// Every format we can create must extract back to the same tree, and the
// archive must not contain itself when it is written inside the folder.
func TestArchiveRoundTrip(t *testing.T) {
	for _, ext := range []string{".zip", ".tar", ".tar.gz", ".tgz", ".tar.zst"} {
		t.Run(ext, func(t *testing.T) {
			f, u := newFilesT(t)
			if err := f.Write(u, "/site/index.html", []byte("hello"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := f.Write(u, "/site/sub/deep.txt", []byte("deep"), 0o600); err != nil {
				t.Fatal(err)
			}
			// Archive the whole home so the output lands inside the source.
			if err := f.Archive(u, "/", "bundle"+ext); err != nil {
				t.Fatal(err)
			}
			if err := f.Extract(u, "/bundle"+ext, "/out"); err != nil {
				t.Fatal(err)
			}
			for path, want := range map[string]string{"/out/site/index.html": "hello", "/out/site/sub/deep.txt": "deep"} {
				got, err := f.Read(u, path)
				if err != nil || string(got) != want {
					t.Errorf("%s = %q, %v; want %q", path, got, err, want)
				}
			}
			if _, err := os.Stat(filepath.Join(u.HomeDir, "out", "bundle"+ext)); err == nil {
				t.Error("the archive contains itself")
			}
		})
	}
}

func TestArchiveSingleFileAndDefaultName(t *testing.T) {
	f, u := newFilesT(t)
	if err := f.Write(u, "/notes.txt", []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Archive(u, "/notes.txt", "one"); err != nil { // no extension -> .zip
		t.Fatal(err)
	}
	if err := f.Extract(u, "/one.zip", "/out"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Read(u, "/out/notes.txt"); err != nil || string(got) != "n" {
		t.Errorf("got %q, %v", got, err)
	}
	if err := f.Archive(u, "/notes.txt", "x.tar.bz2"); err == nil {
		t.Error("creating a bzip2 archive should be refused")
	}
}

func writeTar(t *testing.T, path string, compress string, hdrs []tar.Header, bodies []string) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := range hdrs {
		hdrs[i].Size = int64(len(bodies[i]))
		if err := tw.WriteHeader(&hdrs[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(bodies[i])); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	data := buf.Bytes()
	switch compress {
	case "gz":
		var gz bytes.Buffer
		w := gzip.NewWriter(&gz)
		w.Write(data)
		w.Close()
		data = gz.Bytes()
	case "zst":
		var z bytes.Buffer
		w, _ := zstd.NewWriter(&z)
		w.Write(data)
		w.Close()
		data = z.Bytes()
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTarRefusesUnsafeEntries(t *testing.T) {
	f, u := newFilesT(t)
	writeTar(t, filepath.Join(u.HomeDir, "evil.tar"), "", []tar.Header{
		{Name: "../escaped.txt", Typeflag: tar.TypeReg, Mode: 0o644},
	}, []string{"x"})
	if err := f.Extract(u, "/evil.tar", "/out"); err == nil {
		t.Error("a ../ entry was accepted")
	}
	if _, err := os.Stat(filepath.Join(u.HomeDir, "escaped.txt")); err == nil {
		t.Error("a file escaped the destination")
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(u.HomeDir, "a")); err != nil {
		t.Fatal(err)
	}
	writeTar(t, filepath.Join(u.HomeDir, "link.tar"), "gz", []tar.Header{
		{Name: "a/planted.txt", Typeflag: tar.TypeReg, Mode: 0o644},
	}, []string{"x"})
	if err := f.Extract(u, "/link.tar", "/"); err == nil {
		t.Error("extraction followed a symlink out of the home")
	}
	if _, err := os.Stat(filepath.Join(outside, "planted.txt")); err == nil {
		t.Error("a file was written through the symlink")
	}
}

func TestExtractTarDropsLinksAndSpecialBits(t *testing.T) {
	f, u := newFilesT(t)
	writeTar(t, filepath.Join(u.HomeDir, "m.tar.zst"), "zst", []tar.Header{
		{Name: "bin", Typeflag: tar.TypeReg, Mode: 0o4755},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "hard", Typeflag: tar.TypeLink, Linkname: "bin"},
	}, []string{"x", "", ""})
	if err := f.Extract(u, "/m.tar.zst", "/out"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(u.HomeDir, "out", "bin"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		t.Errorf("special mode bits were kept: %v", st.Mode())
	}
	for _, n := range []string{"link", "hard"} {
		if _, err := os.Lstat(filepath.Join(u.HomeDir, "out", n)); err == nil {
			t.Errorf("%s was created from the archive", n)
		}
	}
}

func TestExtractSingleCompressedFile(t *testing.T) {
	f, u := newFilesT(t)
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write([]byte("log line"))
	w.Close()
	if err := os.WriteFile(filepath.Join(u.HomeDir, "app.log.gz"), gz.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Extract(u, "/app.log.gz", "/"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Read(u, "/app.log"); err != nil || string(got) != "log line" {
		t.Errorf("got %q, %v", got, err)
	}
	if err := f.Extract(u, "/app.log", "/"); err == nil {
		t.Error("a non-archive was accepted")
	}
}
