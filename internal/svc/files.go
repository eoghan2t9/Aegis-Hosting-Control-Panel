package svc

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Files implements the built-in file manager. All operations are chrooted to
// the user's home directory with strict path-traversal protection.
type Files struct {
	Cfg *config.Config
}

func NewFiles(cfg *config.Config) *Files { return &Files{Cfg: cfg} }

// ErrForbidden is returned when a path escapes the user's home.
var ErrForbidden = errors.New("path escapes your home directory")

// Resolve maps a panel-relative path (e.g. "/public/index.html") to an
// absolute path inside the user's home, rejecting traversal.
func (f *Files) Resolve(user *store.User, rel string) (string, error) {
	root := user.HomeDir
	if root == "" {
		root = filepath.Join(f.Cfg.HomeRoot, user.Username)
	}
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
	check := abs
	for {
		if ev, err := filepath.EvalSymlinks(check); err == nil {
			relEval, err := filepath.Rel(root, ev)
			if err != nil || relEval == ".." || strings.HasPrefix(relEval, ".."+string(filepath.Separator)) {
				return "", ErrForbidden
			}
			break
		}
		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}
	return abs, nil
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
func (f *Files) List(user *store.User, rel string) ([]Entry, error) {
	dir, err := f.Resolve(user, rel)
	if err != nil {
		return nil, err
	}
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
	info, err := os.Lstat(abs)
	if err != nil {
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
	abs, err := f.Resolve(user, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("is a directory")
	}
	if info.Size() > maxReadSize {
		return nil, fmt.Errorf("file too large to edit inline (%d bytes); download instead", info.Size())
	}
	return os.ReadFile(abs)
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
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, data, mode)
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
	return os.MkdirAll(abs, mode)
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
	return os.Rename(oldAbs, newAbs)
}

// Delete removes a file or directory (recursively).
func (f *Files) Delete(user *store.User, rel string) error {
	abs, err := f.Resolve(user, rel)
	if err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.RemoveAll(abs)
	}
	return os.Remove(abs)
}

// Chmod sets unix permissions (octal string like "755").
func (f *Files) Chmod(user *store.User, rel, modeStr string) error {
	abs, err := f.Resolve(user, rel)
	if err != nil {
		return err
	}
	m, err := strconv.ParseUint(modeStr, 8, 32)
	if err != nil {
		return errors.New("invalid mode: use octal like 755")
	}
	return os.Chmod(abs, os.FileMode(m))
}

// Chown changes file ownership (numeric or named user/group).
func (f *Files) Chown(user *store.User, rel, owner, group string) error {
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
	return os.Chown(abs, uid, gid)
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
	err = filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if len(hits) >= 200 {
			return errors.New("limit reached")
		}
		if strings.Contains(strings.ToLower(info.Name()), needle) {
			rel2, err := filepath.Rel(user.HomeDir, path)
			if err == nil {
				hits = append(hits, filepath.ToSlash(rel2))
			}
		}
		return nil
	})
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
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	err = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if relPath == "." {
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
	zr, err := zip.OpenReader(zipAbs)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		target := filepath.Join(destAbs, filepath.FromSlash(zf.Name))
		// Zip-slip protection.
		relCheck, err := filepath.Rel(destAbs, target)
		if err != nil || strings.HasPrefix(relCheck, "..") {
			return errors.New("zip contains unsafe paths")
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, zf.Mode())
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

func ownerName(info os.FileInfo) string { return lookupUserName(info) }

func groupName(info os.FileInfo) string { return lookupGroupName(info) }
