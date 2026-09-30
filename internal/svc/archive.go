package svc

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/klauspost/compress/zstd"

	"aegis/internal/store"
)

// Archive formats the file manager can create and extract. Everything is
// implemented in-process and runs through Files.as, so the kernel enforces the
// account's own permissions; no archiver binary ever touches customer content
// as root.
const (
	fmtZip    = "zip"
	fmtTar    = "tar"
	fmtTarGz  = "tar.gz"
	fmtTarBz2 = "tar.bz2" // extract only: the standard library has no bzip2 writer
	fmtTarZst = "tar.zst"
	fmtGz     = "gz" // a single gzip-compressed file
	fmtBz2    = "bz2"
	fmtZst    = "zst"
)

// maxArchiveEntries bounds how many entries one archive may create, so a
// million empty files cannot exhaust inodes while staying under the byte cap.
const maxArchiveEntries = 500_000

var archiveSuffixes = []struct{ suffix, format string }{
	{".tar.gz", fmtTarGz}, {".tgz", fmtTarGz},
	{".tar.bz2", fmtTarBz2}, {".tbz2", fmtTarBz2}, {".tbz", fmtTarBz2},
	{".tar.zst", fmtTarZst}, {".tzst", fmtTarZst},
	{".tar", fmtTar}, {".zip", fmtZip},
	{".gz", fmtGz}, {".bz2", fmtBz2}, {".zst", fmtZst},
}

// archiveFormat reports the format implied by a file name, or "".
func archiveFormat(name string) string {
	l := strings.ToLower(name)
	for _, s := range archiveSuffixes {
		if strings.HasSuffix(l, s.suffix) {
			return s.format
		}
	}
	return ""
}

// Archive packs rel (a folder or a single file) into a new archive called name
// in the account's home. The format comes from the name's extension; a name
// with no recognised extension gets ".zip".
func (f *Files) Archive(user *store.User, rel, name string) error {
	format := archiveFormat(name)
	switch format {
	case "":
		name += ".zip"
		format = fmtZip
	case fmtZip, fmtTar, fmtTarGz, fmtTarZst:
	default:
		return fmt.Errorf("cannot create %s archives; use .zip, .tar, .tar.gz or .tar.zst", format)
	}
	src, err := f.Resolve(user, rel)
	if err != nil {
		return err
	}
	dst, err := f.Resolve(user, "/"+name)
	if err != nil {
		return err
	}
	return f.as(user, func() error {
		err := archiveTree(src, dst, format)
		if err != nil {
			os.Remove(dst) // never leave a truncated archive behind
		}
		return err
	})
}

// Extract unpacks the archive at archiveRel into destRel, choosing the format
// from the file name.
func (f *Files) Extract(user *store.User, archiveRel, destRel string) error {
	archAbs, err := f.Resolve(user, archiveRel)
	if err != nil {
		return err
	}
	destAbs, err := f.Resolve(user, destRel)
	if err != nil {
		return err
	}
	format := archiveFormat(archAbs)
	if format == "" {
		return errors.New("unsupported archive type; supported: .zip .tar .tar.gz .tgz .tar.bz2 .tar.zst .gz .bz2 .zst")
	}
	root := homeRoot(f.Cfg, user)
	return f.as(user, func() error { return extractInto(root, archAbs, destAbs, format) })
}

// archiveTree writes src to dst in the given format. It runs as the account.
func archiveTree(src, dst, format string) error {
	if format == fmtZip {
		return zipTree(src, dst)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	var w io.Writer = out
	var compressor io.Closer
	switch format {
	case fmtTarGz:
		gz := gzip.NewWriter(out)
		compressor, w = gz, gz
	case fmtTarZst:
		zw, err := zstd.NewWriter(out)
		if err != nil {
			return err
		}
		compressor, w = zw, zw
	}
	tw := tar.NewWriter(w)
	if err := tarTree(tw, src, dst); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if compressor != nil {
		if err := compressor.Close(); err != nil {
			return err
		}
	}
	return out.Close()
}

// tarTree adds src to tw. A folder is stored with its contents at the top
// level (same as zipTree); a single file is stored under its own name. The
// archive itself (skip) is left out when it sits inside the folder. Symlinks
// are skipped for the reason given on zipTree.
func tarTree(tw *tar.Writer, src, skip string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	base := src
	if !st.IsDir() {
		base = filepath.Dir(src)
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == skip || (path == src && info.IsDir()) {
			return nil
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return nil // symlinks, sockets, devices, pipes
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			hdr.Name += "/"
		}
		// The account's uid/gid mean nothing on another machine, and would make
		// a restore run as root hand files to that uid.
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		fh, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fh.Close()
		// The file may have changed size since Walk stat'ed it; tar needs the
		// exact byte count declared in the header.
		_, err = io.CopyN(tw, fh, hdr.Size)
		return err
	})
}

// extractInto unpacks archAbs into destAbs. It runs as the account.
func extractInto(root, archAbs, destAbs, format string) error {
	if format == fmtZip {
		return unzipInto(root, archAbs, destAbs)
	}
	in, err := os.Open(archAbs)
	if err != nil {
		return err
	}
	defer in.Close()
	var r io.Reader = in
	switch format {
	case fmtTarGz, fmtGz:
		gz, err := gzip.NewReader(in)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	case fmtTarBz2, fmtBz2:
		r = bzip2.NewReader(in)
	case fmtTarZst, fmtZst:
		zr, err := zstd.NewReader(in)
		if err != nil {
			return err
		}
		defer zr.Close()
		r = zr
	}
	switch format {
	case fmtGz, fmtBz2, fmtZst:
		// "notes.txt.gz" -> "notes.txt"
		base := filepath.Base(archAbs)
		out := base[:strings.LastIndex(base, ".")]
		if out == "" {
			return errors.New("cannot derive a file name from the archive name")
		}
		target, err := safeTarget(root, destAbs, out)
		if err != nil {
			return err
		}
		return writeFileFrom(target, r, 0o644, &byteBudget{left: maxUnzipBytes})
	}
	return untar(root, destAbs, tar.NewReader(r))
}

var errUnsafePath = errors.New("archive contains unsafe paths")

// safeTarget maps an archive entry name to a path under destAbs, refusing any
// that would land outside it, either lexically (zip-slip) or through a symlink
// already present in the tree (see guardSymlinks).
func safeTarget(root, destAbs, name string) (string, error) {
	target := filepath.Join(destAbs, filepath.FromSlash(name))
	rel, err := filepath.Rel(destAbs, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errUnsafePath
	}
	if guardSymlinks(root, target) != nil {
		return "", errUnsafePath
	}
	return target, nil
}

// byteBudget is the shared decompressed-size allowance for one extraction.
type byteBudget struct{ left int64 }

func (b *byteBudget) Write(p []byte) (int, error) {
	if int64(len(p)) > b.left {
		return 0, errors.New("archive expands to more than 4 GiB")
	}
	b.left -= int64(len(p))
	return len(p), nil
}

func writeFileFrom(target string, r io.Reader, mode os.FileMode, budget *byteBudget) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	// Permission bits only: setuid/setgid/sticky from the archive are dropped.
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, mode.Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(io.MultiWriter(dst, budget), r)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	return err
}

func untar(root, destAbs string, tr *tar.Reader) error {
	budget := &byteBudget{left: maxUnzipBytes}
	for n := 0; ; n++ {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if n >= maxArchiveEntries {
			return errors.New("archive has too many entries")
		}
		target, err := safeTarget(root, destAbs, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFileFrom(target, tr, os.FileMode(hdr.Mode), budget); err != nil {
				return err
			}
		default:
			// Symlinks, hard links, devices and FIFOs are never created from an
			// archive.
		}
	}
}
