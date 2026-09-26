//go:build linux

package svc

import (
	"errors"
	"os"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// account is a resolved system account.
type account struct {
	uid, gid uint32
	groups   []uint32
	home     string
}

const accountTTL = 10 * time.Second

type cachedAccount struct {
	a  account
	at time.Time
}

var (
	accountMu    sync.Mutex
	accountCache = map[string]cachedAccount{}
)

// lookupAccount resolves a system account, caching the answer briefly: the
// native web server needs it on every static request, and re-reading
// /etc/passwd and /etc/group each time would be wasteful. The short TTL keeps
// a deleted-and-recreated account from being served under a stale uid.
func lookupAccount(name string) (account, error) {
	accountMu.Lock()
	if c, ok := accountCache[name]; ok && time.Since(c.at) < accountTTL {
		accountMu.Unlock()
		return c.a, nil
	}
	accountMu.Unlock()

	uid, gid, groups, err := terminalIdentity(name)
	if err != nil {
		return account{}, err
	}
	home := ""
	if u, uerr := osuser.Lookup(name); uerr == nil {
		home = u.HomeDir
	}
	a := account{uid: uid, gid: gid, groups: groups, home: home}

	accountMu.Lock()
	accountCache[name] = cachedAccount{a: a, at: time.Now()}
	accountMu.Unlock()
	return a, nil
}

var errNotRegularFile = errors.New("not a regular file")

// statAs stats path as the owner, so the answer cannot reveal files the owner
// could not see (the panel itself, running as root, could stat anything).
func statAs(owner, path string) (os.FileInfo, error) {
	a, err := lookupAccount(owner)
	if err != nil {
		return nil, err
	}
	var info os.FileInfo
	err = runAsIDs(a.uid, a.gid, a.groups, func() error {
		var e error
		info, e = os.Stat(path)
		return e
	})
	return info, err
}

// openDocFile opens a file from a customer's web root for serving.
//
// The native web server is the panel process (root), and a customer controls
// every symlink in their own web root. Serving with http.ServeFile would open
// the path as root and follow a link to /etc/shadow, the panel database, or
// another customer's config. So: the file is opened as the owning account (the
// kernel refuses anything they could not read), and the handle that was
// actually opened is then checked to live inside the owner's home, so a link to
// another customer's world-readable files is refused too. Links that stay
// inside the home (Laravel's public/storage, for one) keep working.
func openDocFile(owner, file string) (*os.File, os.FileInfo, error) {
	a, err := lookupAccount(owner)
	if err != nil {
		return nil, nil, err
	}
	if a.home == "" {
		return nil, nil, errors.New("account has no home directory")
	}
	var fh *os.File
	var info os.FileInfo
	err = runAsIDs(a.uid, a.gid, a.groups, func() error {
		f, e := os.Open(file)
		if e != nil {
			return e
		}
		st, e := f.Stat()
		if e != nil {
			f.Close()
			return e
		}
		fh, info = f, st
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		fh.Close()
		return nil, nil, errNotRegularFile
	}
	// Where the descriptor really points, resolved by the kernel at open time,
	// so nothing can be swapped in between a check and the use.
	real, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(int(fh.Fd())))
	if err != nil || !withinDir(real, a.home) {
		fh.Close()
		return nil, nil, ErrForbidden
	}
	return fh, info, nil
}

// withinDir reports whether real (an already-resolved path) is dir or inside
// it. dir is resolved through symlinks first, since /home may itself be one.
func withinDir(real, dir string) bool {
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(d, real)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
