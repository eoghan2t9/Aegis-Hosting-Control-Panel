package svc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Tenant isolation.
//
// Every panel account used to be created with primary group www-data, so that
// the web server could read every site. The side effect is that all customers
// share one group: homes are 750 user:www-data, so any customer with a shell or
// PHP exec could list and read every other customer's files. Now each account
// gets a PRIVATE group named after it, and the web server is granted read access
// to a site's document root with a POSIX ACL (no group membership, so no web
// server restart is needed when a customer is added).

// runCmd runs a system command. Tests replace it so they never touch real
// accounts.
var runCmd = RunTimeout

// sharedWebGroup is the group accounts no longer share.
const sharedWebGroup = "www-data"

func groupExists(name string) bool {
	_, err := lookupGID(name)
	return err == nil
}

// OwnerSpec returns the "user:group" argument for chown, using the account's
// real primary group (its private group, or www-data on a not yet migrated
// account) so callers never hard-code the shared group.
func OwnerSpec(username string) string { return username + ":" + primaryGroup(username) }

// ProvisionAccount creates a system account with a private primary group. It
// fails if the account already exists, on purpose: a panel user whose name
// matches an existing system account must never be adopted.
func ProvisionAccount(username, home, shell string) error {
	return ProvisionAccountIn(username, home, shell, "")
}

// ProvisionAccountIn is ProvisionAccount with an explicit primary group, used
// for an FTP login that belongs to a customer's private group. An empty group
// means "a private group named after the account".
func ProvisionAccountIn(username, home, shell, group string) error {
	args := []string{"-m", "-d", home, "-s", shell}
	switch {
	case group != "":
		args = append(args, "-g", group)
	case groupExists(username):
		args = append(args, "-g", username)
	default:
		args = append(args, "-U")
	}
	args = append(args, username)
	if _, err := runCmd(15*time.Second, "useradd", args...); err != nil {
		return err
	}
	// The home is only for its owner (the web server gets a targeted ACL).
	_ = os.Chmod(home, 0o750)
	return nil
}

// webServerUsers lists the web-server accounts that exist on this host.
func webServerUsers() []string {
	var out []string
	for _, n := range []string{"www-data", "caddy", "nginx", "apache"} {
		if _, err := lookupUID(n); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// traverseDirs returns home and every directory between it and target, i.e.
// the directories the web server must be able to pass through (but not list).
func traverseDirs(home, target string) []string {
	dirs := []string{home}
	rel, err := filepath.Rel(home, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return dirs
	}
	parts := strings.Split(rel, string(filepath.Separator))
	cur := home
	for _, p := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, p)
		dirs = append(dirs, cur)
	}
	return dirs
}

// GrantWebAccess lets the web server read a site without giving it the
// customer's group: search-only (x) on the home and on the folders leading to the
// document root, and read (rX) on the document root itself, now and (through a
// default ACL) for files created later. Nothing else in the home is exposed.
func GrantWebAccess(owner, home, docroot string) error {
	users := webServerUsers()
	if len(users) == 0 {
		return nil
	}
	if !LookPath("setfacl") {
		// Without ACL tooling the only way to keep the web server working is group
		// membership, which needs a web-server restart to take effect.
		g := primaryGroup(owner)
		for _, wu := range users {
			_, _ = runCmd(10*time.Second, "usermod", "-aG", g, wu)
		}
		return errors.New("setfacl is not installed (package \"acl\"): added the web server to the account's group instead; restart the web server")
	}
	real := docroot
	if r, err := filepath.EvalSymlinks(docroot); err == nil && guardSymlinks(home, docroot) == nil {
		real = r
	}
	var first error
	note := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	for _, wu := range users {
		spec := "u:" + wu
		for _, d := range traverseDirs(home, real) {
			_, err := runCmd(10*time.Second, "setfacl", "-m", spec+":x", d)
			note(err)
		}
		_, err := runCmd(5*time.Minute, "setfacl", "-R", "-m", spec+":rX", real)
		note(err)
		_, err = runCmd(5*time.Minute, "setfacl", "-R", "-d", "-m", spec+":rX", real)
		note(err)
	}
	return first
}

// countGroupOwned counts entries under dir whose group is gid.
func countGroupOwned(dir string, gid int) int {
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			if _, g := statIDs(info); g == gid {
				n++
			}
		}
		return nil
	})
	return n
}

// IsolationReport describes what IsolateAccount did (or would do).
type IsolationReport struct {
	Username       string
	AlreadyPrivate bool
	Files          int // entries whose group changes (or would change)
	Warnings       []string
}

// IsolateAccount moves one account off the shared www-data group onto its own
// private group. It is idempotent, changes only entries whose group is exactly
// www-data (never other groups), and refuses an account whose primary group is
// neither www-data nor already its own. With dryRun it only counts.
func IsolateAccount(username, home string, docroots []string, dryRun bool) (IsolationReport, error) {
	rep := IsolationReport{Username: username}
	cur := primaryGroup(username)
	switch cur {
	case username:
		rep.AlreadyPrivate = true
	case sharedWebGroup:
	default:
		return rep, fmt.Errorf("primary group is %q, expected %q; leaving it alone", cur, sharedWebGroup)
	}
	shared, err := lookupGID(sharedWebGroup)
	if err != nil {
		return rep, err
	}
	rep.Files = countGroupOwned(home, shared)
	if dryRun {
		return rep, nil
	}
	if !rep.AlreadyPrivate {
		if !groupExists(username) {
			if _, err := runCmd(15*time.Second, "groupadd", username); err != nil {
				return rep, err
			}
		}
		if _, err := runCmd(15*time.Second, "usermod", "-g", username, username); err != nil {
			return rep, err
		}
	}
	// Only entries that are currently www-data-grouped change; -h never follows
	// a symlink out of the tree.
	if _, err := runCmd(30*time.Minute, "chgrp", "-R", "-h", "--from=:"+sharedWebGroup, username, home); err != nil {
		return rep, err
	}
	_ = os.Chmod(home, 0o750)
	for _, d := range docroots {
		if err := GrantWebAccess(username, home, d); err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("web access for %s: %v", d, err))
		}
	}
	return rep, nil
}

// IsolateLogin moves an FTP login's system account into its owner's private
// group (it used to sit in www-data like everything else).
func IsolateLogin(login, ownerGroup string) error {
	if primaryGroup(login) != sharedWebGroup {
		return nil
	}
	_, err := runCmd(15*time.Second, "usermod", "-g", ownerGroup, login)
	return err
}

// RollbackIsolation undoes IsolateAccount for one account: back to www-data.
func RollbackIsolation(username, home string) error {
	if _, err := runCmd(15*time.Second, "usermod", "-g", sharedWebGroup, username); err != nil {
		return err
	}
	_, err := runCmd(30*time.Minute, "chgrp", "-R", "-h", "--from=:"+username, sharedWebGroup, home)
	return err
}
