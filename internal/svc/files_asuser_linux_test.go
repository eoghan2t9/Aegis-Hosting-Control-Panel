//go:build linux

package svc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"aegis/internal/config"
	"aegis/internal/store"
)

// accountName returns the name of an unprivileged system account, or skips.
func accountName(t *testing.T) (string, uint32) {
	t.Helper()
	nobodyIDs(t) // skips unless root and an account exists
	for _, name := range []string{"nobody", "daemon"} {
		if uid, gid, _, err := terminalIdentity(name); err == nil && uid != 0 && gid != 0 {
			return name, uid
		}
	}
	t.Skip("no unprivileged system account")
	return "", 0
}

// End to end through the real Files type (no in-process shortcut): the file
// manager must act as the customer, not as the root panel process.
func TestFilesActAsTheAccountNotRoot(t *testing.T) {
	name, uid := accountName(t)
	home := accessibleTempDir(t, 0o777)
	if err := os.WriteFile(filepath.Join(home, "root-only.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := NewFiles(config.Default())
	u := &store.User{Username: name, HomeDir: home}

	// The panel is root and could read it; the customer could not.
	if _, err := f.Read(u, "/root-only.txt"); !errors.Is(err, os.ErrPermission) {
		t.Errorf("Read of a root-only file gave %v, want a permission error", err)
	}

	// Files the customer creates belong to the customer.
	if err := f.Write(u, "/mine.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var st syscall.Stat_t
	if err := syscall.Stat(filepath.Join(home, "mine.txt"), &st); err != nil {
		t.Fatal(err)
	}
	if st.Uid != uid {
		t.Errorf("file created through the API is owned by uid %d, want %d", st.Uid, uid)
	}

	// The customer cannot give their file to root.
	err := f.Chown(u, "/mine.txt", "root", "root", false)
	if err == nil || !strings.Contains(err.Error(), "your own account") {
		t.Errorf("chown to root gave %v, want a refusal", err)
	}
	if err := syscall.Stat(filepath.Join(home, "mine.txt"), &st); err != nil || st.Uid != uid {
		t.Errorf("owner changed despite the refusal (uid %d, err %v)", st.Uid, err)
	}
}

// The attack reproduced live: a customer symlink inside the tree redirected an
// archive entry to a directory only root could write, and the panel wrote there.
func TestUnzipThroughSymlinkCannotWriteOutsideEvenWithRealIdentity(t *testing.T) {
	name, _ := accountName(t)
	home := accessibleTempDir(t, 0o777)
	outside := accessibleTempDir(t, 0o700) // root-only, like /tmp/zz_probe_dir was
	if err := os.Symlink(outside, filepath.Join(home, "a")); err != nil {
		t.Fatal(err)
	}
	writeZip(t, filepath.Join(home, "x.zip"), map[string]zipEntry{"a/planted.txt": {data: "x"}})
	if err := os.Chmod(filepath.Join(home, "x.zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := NewFiles(config.Default())
	u := &store.User{Username: name, HomeDir: home}
	_ = f.Unzip(u, "/x.zip", "/")
	if _, err := os.Stat(filepath.Join(outside, "planted.txt")); err == nil {
		t.Fatal("the archive planted a file outside the home")
	}
}
