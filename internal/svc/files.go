package svc

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Files implements the built-in file manager. All operations are chrooted to
// the user's home directory with strict path-traversal protection.
type Files struct {
	Cfg *config.Config

	// runAs performs file I/O as a system account. nil means runAsAccount.
	// Unexported on purpose: only tests in this package may swap it, so no
	// caller can tell production code to skip the identity switch.
	runAs func(name string, fn func() error) error
}

func NewFiles(cfg *config.Config) *Files { return &Files{Cfg: cfg} }

// ErrForbidden is returned when a path escapes the user's home.
var ErrForbidden = errors.New("path escapes your home directory")

// Resolve maps a panel-relative path (e.g. "/public/index.html") to an
// absolute path inside the user's home, rejecting traversal.
func (f *Files) Resolve(user *store.User, rel string) (string, error) {
	root := homeRoot(f.Cfg, user)
	if rel == "" || rel == "/" {
		return root, nil
	}
	clean := filepath.Clean("/" + rel)
	abs := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	// Lexical guard: the resolved path must sit under the home directory.
	relToRoot, err := filepath.Rel(root, abs)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", ErrForbidden
	}
	// Symlink guard: walk up from abs to the nearest existing ancestor and
	// verify it resolves to a real path inside the home (no symlink escape).
	if err := guardSymlinks(root, abs); err != nil {
		return "", err
	}
	return abs, nil
}

// as runs fn with file I/O performed AS the account that owns the home (see
// runAsAccount). Resolve only guards the *path*; this makes the kernel enforce
// the account's real permissions on every operation, so a symlink swapped in
// after the check, or a crafted archive entry, still cannot reach anything the
// customer could not open themselves. Every method below that touches customer
// files goes through it.
func (f *Files) as(user *store.User, fn func() error) error {
	if f.runAs != nil {
		return f.runAs(user.Username, fn)
	}
	return runAsAccount(user.Username, fn)
}

// OpenRead opens rel for reading as the account and returns the handle. The
// permission decision is made by the kernel at open time, so callers can stream
// from the handle without re-opening the path as root (which is how a symlink
// planted in a web root used to be followed with root's privileges).
func (f *Files) OpenRead(user *store.User, rel string) (*os.File, os.FileInfo, error) {
	abs, err := f.Resolve(user, rel)
	if err != nil {
		return nil, nil, err
	}
	var fh *os.File
	var info os.FileInfo
	err = f.as(user, func() error {
		var e error
		if fh, e = os.Open(abs); e != nil {
			return e
		}
		if info, e = fh.Stat(); e != nil {
			fh.Close()
			fh = nil
		}
		return e
	})
	if err != nil {
		return nil, nil, err
	}
	return fh, info, nil
}

// OpenWrite creates or truncates rel for writing as the account, so the new
// file is owned by the customer. Returns the handle for streaming.
func (f *Files) OpenWrite(user *store.User, rel string, mode os.FileMode) (*os.File, error) {
	abs, err := f.Resolve(user, rel)
	if err != nil {
		return nil, err
	}
	var fh *os.File
	err = f.as(user, func() error {
		var e error
		fh, e = os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, mode.Perm())
		return e
	})
	return fh, err
}

// Entry is one file-manager listing row.
type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"` // relative to home
	Type    string    `json:"type"` // file | dir | symlink
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"` // octal perms e.g. "755"
	ModTime time.Time `json:"mod_time"`
	Owner   string    `json:"owner"`
	Group   string    `json:"group"`
}

// List returns directory entries sorted (dirs first, then name).
func (f *Files) List(user *store.User, rel string) (out []Entry, err error) {
	dir, err := f.Resolve(user, rel)
	if err != nil {
		return nil, err
	}
	if _, serr := os.Stat(dir); errors.Is(serr, os.ErrNotExist) && dir == homeRoot(f.Cfg, user) {
		// Not every account has a home directory on disk yet. Rather than
		// error on the very first visit to the file manager, create it
		// lazily — as root, since /home is not writable by the account — and
		// hand it to the account so the listing below can read it.
		if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
			return nil, serr
		}
		if uid, gid, _, ierr := terminalIdentity(user.Username); ierr == nil {
			_ = os.Chown(dir, int(uid), int(gid))
		}
	}
	err = f.as(user, func() error {
		var lerr error
		out, lerr = f.list(user, dir)
		return lerr
	})
	return out, err
}

func (f *Files) list(user *store.User, dir string) ([]Entry, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("not a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, e := range entries {
		info, err := os.Lstat(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		typ := "file"
		switch {
		case info.IsDir():
			typ = "dir"
		case info.Mode()&os.ModeSymlink != 0:
			typ = "symlink"
		}
		out = append(out, Entry{
			Name:    e.Name(),
			Path:    filepath.ToSlash(filepath.Join(strings.TrimPrefix(dir, user.HomeDir), e.Name())),
			Type:    typ,
			Size:    info.Size(),
			Mode:    strconv.FormatUint(uint64(info.Mode().Perm()), 8),
			ModTime: info.ModTime(),
			Owner:   ownerName(info),
			Group:   groupName(info),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Type == "dir") != (out[j].Type == "dir") {
			return out[i].Type == "dir"
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Stat returns a single entry.
func (f *Files) Stat(user *store.User, rel string) (*Entry, error) {
	abs, err := f.Resolve(user, rel)
	if err != nil {
		return nil, err
	}
	var info os.FileInfo
	if err := f.as(user, func() (e error) { info, e = os.Lstat(abs); return }); err != nil {
		return nil, err
	}
	typ := "file"
	if info.IsDir() {
		typ = "dir"
	}
	return &Entry{
		Name: info.Name(), Path: rel, Type: typ, Size: info.Size(),
		Mode: strconv.FormatUint(uint64(info.Mode().Perm()), 8), ModTime: info.ModTime(),
		Owner: ownerName(info), Group: groupName(info),
	}, nil
}

const maxReadSize = 2 << 20 // 2 MiB safety cap for reads through the API

// Read returns file contents (capped at maxReadSize).
func (f *Files) Read(user *store.User, rel string) ([]byte, error) {
	fh, info, err := f.OpenRead(user, rel)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	if info.IsDir() {
		return nil, errors.New("is a directory")
	}
	if info.Size() > maxReadSize {
		return nil, fmt.Errorf("file too large to edit inline (%d bytes); download instead", info.Size())
	}
	return io.ReadAll(io.LimitReader(fh, maxReadSize+1))
}

// Write creates or overwrites a file.
func (f *Files) Write(user *store.User, rel string, data []byte, mode os.FileMode) error {
	abs, err := f.Resolve(user, rel)
	if err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	// Permission bits only: a caller-supplied setuid/setgid/sticky bit is never
	// honoured for a customer's files.
	mode = mode.Perm()
	return f.as(user, func() error {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		fh, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, mode)
		if err != nil {
			return err
		}
		if _, err := fh.Write(data); err != nil {
			fh.Close()
			return err
		}
		return fh.Close()
	})
}

// Mkdir creates a directory (all parents too).
func (f *Files) Mkdir(user *store.User, rel string, mode os.FileMode) error {
	abs, err := f.Resolve(user, rel)
	if err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o755
	}
	mode = mode.Perm()
	return f.as(user, func() error { return os.MkdirAll(abs, mode) })
}

// Rename moves a file or directory.
func (f *Files) Rename(user *store.User, oldRel, newRel string) error {
	oldAbs, err := f.Resolve(user, oldRel)
	if err != nil {
		return err
	}
	newAbs, err := f.Resolve(user, newRel)
	if err != nil {
		return err
	}
	return f.as(user, func() error { return os.Rename(oldAbs, newAbs) })
}

// Delete removes a file or directory (recursively).
func (f *Files) Delete(user *store.User, rel string) error {
	abs, err := f.Resolve(user, rel)
	if err != nil {
		return err
	}
	return f.as(user, func() error {
		info, err := os.Lstat(abs)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.RemoveAll(abs)
		}
		return os.Remove(abs)
	})
}

// Chmod sets unix permissions (octal string like "755"). When recursive is
// true, mode is applied to every descendant too (dirs and files alike; there
// is no separate dir-mode/file-mode split). Symlinks are skipped: Linux has
// no real lchmod, so a symlink's own "permission bits" are meaningless, and
// chmod-ing through one would silently affect whatever it points at.
func (f *Files) Chmod(user *store.User, rel, modeStr string, recursive bool) error {
	abs, err := f.Resolve(user, rel)
	if err != nil {
		return err
	}
	m, err := strconv.ParseUint(modeStr, 8, 32)
	if err != nil {
		return errors.New("invalid mode: use octal like 755")
	}
	if m > 0o777 {
		return errors.New("invalid mode: setuid, setgid and sticky bits are not allowed")
	}
	mode := os.FileMode(m)
	return f.as(user, func() error {
		if !recursive {
			return os.Chmod(abs, mode)
		}
		return filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			return os.Chmod(path, mode)
		})
	})
}

// Chown changes file ownership (numeric or named user/group). When recursive
// is true, ownership is applied to every descendant too. uid/gid are
// resolved once up front rather than per-file, since lookupUID/lookupGID
// re-parse /etc/passwd and /etc/group on every call. Symlinks use Lchown
// (changes the link itself, not its target) so a symlink pointing outside
// the jailed home can't be used to reach files this call shouldn't touch.
func (f *Files) Chown(user *store.User, rel, owner, group string, recursive bool) error {
	abs, err := f.Resolve(user, rel)
	if err != nil {
		return err
	}
	uid, err := lookupUID(owner)
	if err != nil {
		return err
	}
	gid, err := lookupGID(group)
	if err != nil {
		return err
	}
	// Runs as the account, so the kernel itself refuses to give a file to any
	// other user or a group the account is not in. (This used to run as root,
	// which let a customer chown their own files to root.)
	err = f.as(user, func() error {
		if !recursive {
			return os.Chown(abs, uid, gid)
		}
		return filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return os.Lchown(path, uid, gid)
			}
			return os.Chown(path, uid, gid)
		})
	})
	if errors.Is(err, syscall.EPERM) {
		return errors.New("you can only assign files to your own account and groups you belong to")
	}
	return err
}

// Search walks the tree under rel and returns paths containing needle
// (filename match, case-insensitive). Limited to avoid runaway scans.
func (f *Files) Search(user *store.User, rel, needle string) ([]string, error) {
	abs, err := f.Resolve(user, rel)
	if err != nil {
		return nil, err
	}
	needle = strings.ToLower(needle)
	if needle == "" {
		return nil, errors.New("empty search term")
	}
	var hits []string
	errSearchLimit := errors.New("limit reached")
	err = f.as(user, func() error {
		return filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if len(hits) >= 200 {
				return errSearchLimit
			}
			if strings.Contains(strings.ToLower(info.Name()), needle) {
				rel2, err := filepath.Rel(user.HomeDir, path)
				if err == nil {
					hits = append(hits, filepath.ToSlash(rel2))
				}
			}
			return nil
		})
	})
	// Hitting the cap isn't a failure — the caller gets the first 200 matches
	// (still ordered by Walk's lexical directory order) instead of losing
	// every hit to what looks like a search error.
	if err == errSearchLimit {
		err = nil
	}
	return hits, err
}

// Zip archives a directory into a zip file inside the user's home.
func (f *Files) Zip(user *store.User, rel, name string) error {
	src, err := f.Resolve(user, rel)
	if err != nil {
		return err
	}
	if !strings.HasSuffix(strings.ToLower(name), ".zip") {
		name += ".zip"
	}
	dst, err := f.Resolve(user, "/"+name)
	if err != nil {
		return err
	}
	return f.as(user, func() error { return zipTree(src, dst) })
}

// zipTree writes an archive of src to dst. It runs as the account (see Zip).
// Symlinks are skipped: following one would put the contents of whatever it
// points at, possibly outside the home, into an archive the customer downloads.
func zipTree(src, dst string) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	base := src
	if st, err := os.Lstat(src); err == nil && !st.IsDir() {
		base = filepath.Dir(src) // a single file is stored under its own name
	}
	err = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == dst {
			return nil // the archive being written
		}
		relPath, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		relPath = filepath.ToSlash(relPath)
		if info.IsDir() {
			relPath += "/"
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = relPath
		if info.IsDir() {
			hdr.Name = relPath
		} else {
			hdr.Method = zip.Deflate
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
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
		_, err = io.Copy(w, fh)
		return err
	})
	if err != nil {
		zw.Close()
		return err
	}
	return zw.Close()
}

// Unzip extracts a zip archive into rel (default: current dir).
func (f *Files) Unzip(user *store.User, zipRel, destRel string) error {
	zipAbs, err := f.Resolve(user, zipRel)
	if err != nil {
		return err
	}
	destAbs, err := f.Resolve(user, destRel)
	if err != nil {
		return err
	}
	root := homeRoot(f.Cfg, user)
	return f.as(user, func() error { return unzipInto(root, zipAbs, destAbs) })
}

// maxUnzipBytes bounds what one archive may expand to (zip-bomb guard).
const maxUnzipBytes = 4 << 30

// guardSymlinks fails when the nearest existing ancestor of abs resolves,
// through any symlink, to somewhere outside root. Resolve does this for the
// path it is handed; extraction has to repeat it for every archive entry,
// because a symlink already sitting in the tree redirects a "safe" relative
// entry name such as "a/file" to wherever the link points.
func guardSymlinks(root, abs string) error {
	check := abs
	for {
		if ev, err := filepath.EvalSymlinks(check); err == nil {
			relEval, err := filepath.Rel(root, ev)
			if err != nil || relEval == ".." || strings.HasPrefix(relEval, ".."+string(filepath.Separator)) {
				return ErrForbidden
			}
			return nil
		}
		parent := filepath.Dir(check)
		if parent == check {
			return nil
		}
		check = parent
	}
}

// unzipInto extracts zipAbs into destAbs. It runs as the account (see Unzip).
func unzipInto(root, zipAbs, destAbs string) error {
	zr, err := zip.OpenReader(zipAbs)
	if err != nil {
		return err
	}
	defer zr.Close()
	var total uint64
	for _, zf := range zr.File {
		total += zf.UncompressedSize64
		if total > maxUnzipBytes {
			return errors.New("archive expands to more than 4 GiB")
		}
	}
	unsafe := errors.New("zip contains unsafe paths")
	for _, zf := range zr.File {
		target := filepath.Join(destAbs, filepath.FromSlash(zf.Name))
		// Zip-slip protection: the entry must stay under the destination...
		relCheck, err := filepath.Rel(destAbs, target)
		if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
			return unsafe
		}
		// ...and, because that check is only lexical, no symlink already in the
		// tree may lead it out of the home.
		if guardSymlinks(root, target) != nil {
			return unsafe
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if zf.Mode()&os.ModeSymlink != 0 {
			continue // an archive never gets to create links
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		// Permission bits only: setuid/setgid/sticky from the archive are dropped.
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, zf.Mode().Perm())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(dst, rc)
		rc.Close()
		dst.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// homeRoot returns the absolute path of user's home directory, falling back
// to <HomeRoot>/<username> when the account record has none set.
func homeRoot(cfg *config.Config, user *store.User) string {
	if user.HomeDir != "" {
		return user.HomeDir
	}
	return filepath.Join(cfg.HomeRoot, user.Username)
}

func ownerName(info os.FileInfo) string { return lookupUserName(info) }

func groupName(info os.FileInfo) string { return lookupGroupName(info) }
