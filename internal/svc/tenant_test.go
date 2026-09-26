package svc

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// recordCmds replaces runCmd so no test ever changes a real account.
func recordCmds(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := runCmd
	runCmd = func(_ time.Duration, name string, args ...string) (string, error) {
		got = append(got, name+" "+strings.Join(args, " "))
		return "", nil
	}
	t.Cleanup(func() { runCmd = old })
	return &got
}

// Accounts used to be created with -g www-data, one group shared by every
// customer, so any customer with a shell could read every other customer's
// files. Each account now gets a private group named after it.
func TestProvisionAccountCreatesAPrivateGroup(t *testing.T) {
	cmds := recordCmds(t)
	home := filepath.Join(t.TempDir(), "zz_newtenant")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ProvisionAccount("zz_newtenant", home, "/sbin/nologin"); err != nil {
		t.Fatal(err)
	}
	if len(*cmds) != 1 {
		t.Fatalf("commands = %v", *cmds)
	}
	c := (*cmds)[0]
	if !strings.HasPrefix(c, "useradd ") || !strings.Contains(c, " -U ") || strings.Contains(c, "www-data") {
		t.Errorf("command %q must create a private group (-U) and must not use www-data", c)
	}
	if st, err := os.Stat(home); err != nil || st.Mode().Perm() != 0o750 {
		t.Errorf("home mode = %v (err %v), want 0750", st.Mode().Perm(), err)
	}
}

func TestProvisionAccountInUsesTheGivenGroup(t *testing.T) {
	cmds := recordCmds(t)
	home := filepath.Join(t.TempDir(), "ftp1")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ProvisionAccountIn("zz_ftp1", home, "/sbin/nologin", "zz_owner"); err != nil {
		t.Fatal(err)
	}
	if c := (*cmds)[0]; !strings.Contains(c, "-g zz_owner") || strings.Contains(c, " -U") {
		t.Errorf("command %q should join the owner's group", c)
	}
}

func TestTraverseDirs(t *testing.T) {
	got := traverseDirs("/home/a", "/home/a/site.com/public")
	want := []string{"/home/a", "/home/a/site.com"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("traverseDirs = %v, want %v", got, want)
	}
	// A docroot directly in the home needs nothing beyond the home itself.
	if got := traverseDirs("/home/a", "/home/a/public"); len(got) != 1 || got[0] != "/home/a" {
		t.Errorf("traverseDirs = %v", got)
	}
	// A target outside the home never grants access to anything else.
	if got := traverseDirs("/home/a", "/etc"); len(got) != 1 {
		t.Errorf("traverseDirs = %v", got)
	}
}

// The web server must be able to read a site but nothing else in the home, and
// must not be given the customer's group.
func TestGrantWebAccessIsTargeted(t *testing.T) {
	if !LookPath("setfacl") {
		t.Skip("setfacl (package acl) not installed")
	}
	if len(webServerUsers()) == 0 {
		t.Skip("no web server user on this machine")
	}
	cmds := recordCmds(t)
	home := t.TempDir()
	root := filepath.Join(home, "site.com", "public")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := GrantWebAccess("alice", home, root); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*cmds, "\n")
	for _, want := range []string{":x " + home, ":x " + filepath.Join(home, "site.com"), "-R -m u:", "-R -d -m u:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "usermod") {
		t.Errorf("the web server was added to a group instead of getting an ACL:\n%s", joined)
	}
}

// IsolateAccount only ever migrates an account that is in the shared group (or
// already private); anything else is left alone.
func TestIsolateAccountRefusesAnUnexpectedGroup(t *testing.T) {
	cmds := recordCmds(t)
	// root's primary group is root: neither www-data nor "private" by name.
	if _, err := IsolateAccount("root", "/root", nil, false); err == nil {
		// root's group name equals its username, so it counts as already private;
		// use an account whose group differs from its name instead.
		if len(*cmds) != 0 && strings.Contains(strings.Join(*cmds, ";"), "usermod") {
			t.Errorf("a usermod ran for root: %v", *cmds)
		}
	}
	*cmds = nil
	// daemon's primary group is "daemon" on Debian; bin/sys likewise. Find an
	// account whose primary group is not its own name and not www-data.
	for _, name := range []string{"nobody", "sync", "man"} {
		if primaryGroup(name) == name || primaryGroup(name) == sharedWebGroup {
			continue
		}
		if _, err := IsolateAccount(name, "/nonexistent", nil, false); err == nil {
			t.Errorf("%s (group %s) was migrated", name, primaryGroup(name))
		}
		if len(*cmds) != 0 {
			t.Errorf("commands were run for %s: %v", name, *cmds)
		}
		return
	}
	t.Skip("no account with an unrelated primary group on this machine")
}

func TestIsolateAccountDryRunChangesNothing(t *testing.T) {
	cmds := recordCmds(t)
	// www-data's own primary group is www-data, so it is accepted; the point is
	// only that a dry run executes no command.
	if _, err := IsolateAccount("www-data", t.TempDir(), nil, true); err != nil {
		t.Fatal(err)
	}
	if len(*cmds) != 0 {
		t.Errorf("a dry run executed commands: %v", *cmds)
	}
}

func TestCountGroupOwned(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chgrp")
	}
	gid, err := lookupGID("www-data")
	if err != nil {
		t.Skip("no www-data group")
	}
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Lchown(filepath.Join(dir, "a"), -1, gid); err != nil {
		t.Fatal(err)
	}
	if err := os.Lchown(filepath.Join(dir, "b"), -1, gid); err != nil {
		t.Fatal(err)
	}
	// A symlink is counted by its own group and never followed.
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if got := countGroupOwned(dir, gid); got != 2 {
		t.Errorf("countGroupOwned = %d, want 2", got)
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(filepath.Join(dir, "c"), &st); err != nil || int(st.Gid) == gid {
		t.Errorf("file c should not be www-data-grouped")
	}
}

// The migration command is run for real here (on temp files, as root): the first
// version used chgrp --from, which does not exist, and failed halfway through an
// account. Only entries currently in the source group may change, the owner must
// not change, and a symlink must never be followed out of the tree.
func TestRegroupArgsMovesOnlyTheSourceGroup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	from, err := lookupGID("www-data")
	if err != nil {
		t.Skip("no www-data group")
	}
	to, err := lookupGID("daemon")
	if err != nil {
		t.Skip("no daemon group")
	}
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	for p, gid := range map[string]int{"in": from, "sub/in2": from, "other": 0} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Lchown(full, -1, gid); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Lchown(outside, -1, from); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	if out, err := RunTimeout(30*time.Second, "chown", regroupArgs("www-data", "daemon", dir)...); err != nil {
		t.Fatalf("the migration command failed: %v %s", err, out)
	}
	gidOf := func(p string) int {
		var st syscall.Stat_t
		if err := syscall.Lstat(p, &st); err != nil {
			t.Fatal(err)
		}
		return int(st.Gid)
	}
	if gidOf(filepath.Join(dir, "in")) != to || gidOf(filepath.Join(dir, "sub", "in2")) != to {
		t.Error("www-data-grouped entries were not moved")
	}
	if gidOf(filepath.Join(dir, "other")) != 0 {
		t.Error("an entry in another group was changed")
	}
	if gidOf(outside) != from {
		t.Error("a symlink was followed out of the tree")
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(filepath.Join(dir, "in"), &st); err != nil || st.Uid != 0 {
		t.Errorf("the owner changed (uid %d)", st.Uid)
	}
}

func TestOwnerSpecUsesTheRealPrimaryGroup(t *testing.T) {
	if got := OwnerSpec("root"); got != "root:root" {
		t.Errorf("OwnerSpec(root) = %q", got)
	}
	// An account that does not exist falls back to its own name, never to www-data.
	if got := OwnerSpec("zz_no_such_account"); got != "zz_no_such_account:zz_no_such_account" {
		t.Errorf("OwnerSpec = %q", got)
	}
}
