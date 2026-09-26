//go:build linux

package svc

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

// nobodyIDs returns the ids of an unprivileged account that exists on the
// machine, or skips: these tests must run as root, like the panel does.
func nobodyIDs(t *testing.T) (uid, gid uint32, groups []uint32) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root (the panel runs as root)")
	}
	for _, name := range []string{"nobody", "daemon"} {
		u, g, gr, err := terminalIdentity(name)
		if err == nil && u != 0 && g != 0 {
			return u, g, gr
		}
	}
	t.Skip("no unprivileged system account to switch to")
	return
}

// accessibleTempDir makes a scratch directory the unprivileged account can
// traverse. t.TempDir() is not enough: its parent is 0700 root, which would make
// every access fail for the wrong reason and hide what the tests are checking.
func accessibleTempDir(t *testing.T, mode os.FileMode) string {
	t.Helper()
	d, err := os.MkdirTemp("", "aegis-asuser-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	if err := os.Chmod(d, mode); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRunAsIDsDropsAndRestoresRoot(t *testing.T) {
	uid, gid, groups := nobodyIDs(t)
	if _, err := os.ReadFile("/etc/shadow"); err != nil {
		t.Skipf("cannot read /etc/shadow even as root here: %v", err)
	}

	var insideErr error
	err := runAsIDs(uid, gid, groups, func() error {
		_, insideErr = os.ReadFile("/etc/shadow") // root-only file
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(insideErr, os.ErrPermission) {
		t.Errorf("as the unprivileged account /etc/shadow read gave %v, want a permission error", insideErr)
	}
	// Identity must be back to root afterwards.
	if _, err := os.ReadFile("/etc/shadow"); err != nil {
		t.Errorf("root privileges were not restored: %v", err)
	}
}

// Files created while switched belong to the account, not to root: this is
// what stops a customer from making root-owned or setuid-root files.
func TestRunAsIDsCreatesFilesOwnedByTheAccount(t *testing.T) {
	uid, gid, groups := nobodyIDs(t)
	dir := accessibleTempDir(t, 0o777)
	p := filepath.Join(dir, "made-by-account")
	err := runAsIDs(uid, gid, groups, func() error {
		if e := os.WriteFile(p, []byte("x"), 0o644); e != nil {
			return e
		}
		// The account may chmod its own file, but cannot hand it to root.
		if e := os.Chown(p, 0, 0); e == nil {
			t.Error("the account was able to chown its file to root")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var st syscall.Stat_t
	if err := syscall.Stat(p, &st); err != nil {
		t.Fatal(err)
	}
	if st.Uid != uid {
		t.Errorf("file owner uid = %d, want %d (the account)", st.Uid, uid)
	}
}

// A symlink cannot be used to reach a file the account itself could not read.
func TestRunAsIDsSymlinkCannotReachPrivateFiles(t *testing.T) {
	uid, gid, groups := nobodyIDs(t)
	// Both directories are traversable by the account, so the only thing that
	// can stop the read is the file's own 0600 root permission.
	dir := accessibleTempDir(t, 0o755)
	secret := filepath.Join(accessibleTempDir(t, 0o755), "secret")
	if err := os.WriteFile(secret, []byte("root only"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	// Sanity: root can follow the link, so a failure below is about identity.
	if b, err := os.ReadFile(link); err != nil || string(b) != "root only" {
		t.Fatalf("root could not read through the link: %v", err)
	}
	var readErr error
	_ = runAsIDs(uid, gid, groups, func() error {
		_, readErr = os.ReadFile(link)
		return nil
	})
	if !errors.Is(readErr, os.ErrPermission) {
		t.Errorf("read through the symlink gave %v, want a permission error", readErr)
	}
}

// Thread identity must never leak into other work. Run many switched
// operations concurrently with root-only reads on other goroutines; if a
// thread were returned to the pool still switched, some root reads would fail.
func TestRunAsIDsDoesNotLeakIdentityAcrossThreads(t *testing.T) {
	uid, gid, groups := nobodyIDs(t)
	if _, err := os.ReadFile("/etc/shadow"); err != nil {
		t.Skipf("cannot read /etc/shadow even as root here: %v", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := 0
	for i := 0; i < 64; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = runAsIDs(uid, gid, groups, func() error {
				_, _ = os.ReadFile("/etc/hostname")
				return nil
			})
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := os.ReadFile("/etc/shadow"); err != nil {
					mu.Lock()
					failures++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if failures != 0 {
		t.Errorf("%d root-only reads failed: a thread kept the account's identity", failures)
	}
}

func TestRunAsIDsRefusesRoot(t *testing.T) {
	if err := runAsIDs(0, 1000, nil, func() error { return nil }); err == nil {
		t.Error("uid 0 must be refused")
	}
	if err := runAsIDs(1000, 0, nil, func() error { return nil }); err == nil {
		t.Error("gid 0 must be refused")
	}
}
