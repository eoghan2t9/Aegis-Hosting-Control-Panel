package svc

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aegis/internal/config"
	"aegis/internal/store"
)

// recorder swaps the FTP service's side-effect seams for ones that only record,
// so a regression can never change a real account on the machine running tests.
type recorder struct {
	passwords []string
	commands  []string
}

func recordFTP(t *testing.T) *recorder {
	t.Helper()
	r := &recorder{}
	oldRun, oldPw, oldCmd := ftpRun, ftpSetPassword, runCmd
	ftpRun = func(_ time.Duration, name string, args ...string) (string, error) {
		r.commands = append(r.commands, name+" "+strings.Join(args, " "))
		return "", nil
	}
	runCmd = ftpRun
	ftpSetPassword = func(user, _ string) error { r.passwords = append(r.passwords, user); return nil }
	t.Cleanup(func() { ftpRun, ftpSetPassword, runCmd = oldRun, oldPw, oldCmd })
	return r
}

func newFTPT(t *testing.T) (*FTP, *store.User) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	owner := &store.User{Username: "alice", Email: "a@example.com", PasswordHash: "x", Role: store.RoleUser, Status: store.StatusActive, HomeDir: "/home/alice"}
	if err := st.CreateUser(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	return NewFTP(config.Default(), st), owner
}

// Reproduced live: a customer created an FTP account named after an existing
// system account, the panel "reused" it and set its password, and deleting the
// FTP account then deleted the system account.
func TestFTPCreateRefusesExistingSystemAndReservedAccounts(t *testing.T) {
	f, owner := newFTPT(t)
	rec := recordFTP(t)
	// root and daemon exist on every Linux host; the rest are reserved names.
	for _, name := range []string{"root", "daemon", "nobody", "postgres", "mysql", "caddy", "sshd"} {
		if _, err := f.Create(context.Background(), owner, name, "a-long-password-1", true); err == nil {
			t.Errorf("FTP account %q was created", name)
		}
	}
	if len(rec.passwords) != 0 {
		t.Errorf("passwords were set for existing accounts: %v", rec.passwords)
	}
	for _, c := range rec.commands {
		if strings.HasPrefix(c, "useradd") || strings.HasPrefix(c, "userdel") {
			t.Errorf("unexpected account change: %s", c)
		}
	}
}

func TestCheckLoginReuse(t *testing.T) {
	for _, tc := range []struct {
		name         string
		uid          int
		existingHome string
		wantHome     string
		ok           bool
	}{
		{"system account", 0, "/root", "/home/alice/x", false},
		{"service account", 105, "/var/lib/postgresql", "/home/alice/x", false},
		{"another customer's account", 1005, "/home/bob", "/home/alice/bob", false},
		{"account with no home", 1005, "", "/home/alice/x", false},
		{"leftover of this very login", 1005, "/home/alice/site", "/home/alice/site", true},
		{"same path spelled differently", 1005, "/home/alice/site/", "/home/alice//site", true},
	} {
		err := checkLoginReuse(tc.uid, tc.existingHome, tc.wantHome)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestFTPDeleteNeverRemovesASystemAccount(t *testing.T) {
	f, owner := newFTPT(t)
	rec := recordFTP(t)
	ctx := context.Background()
	for _, name := range []string{"daemon", "root", "postgres"} {
		acct := &store.FTPAccount{UserID: owner.ID, Username: name, PasswordHash: "x", HomeDir: "/home/alice/" + name, Enabled: true}
		if err := f.Store.CreateFTPAccount(ctx, acct); err != nil {
			t.Fatal(err)
		}
		if err := f.Delete(ctx, acct); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, c := range rec.commands {
		if strings.HasPrefix(c, "userdel") {
			t.Errorf("a system account was deleted: %s", c)
		}
	}
}

func TestFTPResetPasswordAndToggleRefuseSystemAccounts(t *testing.T) {
	f, owner := newFTPT(t)
	rec := recordFTP(t)
	acct := &store.FTPAccount{UserID: owner.ID, Username: "daemon", PasswordHash: "x", HomeDir: "/home/alice/d", Enabled: true}
	if err := f.Store.CreateFTPAccount(context.Background(), acct); err != nil {
		t.Fatal(err)
	}
	if err := f.ResetPassword(context.Background(), acct, "a-long-password-1"); err == nil {
		t.Error("reset-password on a system account was allowed")
	}
	if err := f.ToggleEnabled(context.Background(), acct, false); err == nil {
		t.Error("locking a system account was allowed")
	}
	if len(rec.passwords) != 0 {
		t.Errorf("a system account's password was changed: %v", rec.passwords)
	}
	for _, c := range rec.commands {
		if strings.HasPrefix(c, "usermod") {
			t.Errorf("a system account was modified: %s", c)
		}
	}
}

func TestReservedAccountName(t *testing.T) {
	for _, n := range []string{"root", "ROOT", " postgres ", "mysql", "www", "nobody"} {
		if !ReservedAccountName(n) {
			t.Errorf("%q should be reserved", n)
		}
	}
	for _, n := range []string{"alice", "bob_site", "example_com", "admin", "administrator"} {
		if ReservedAccountName(n) {
			t.Errorf("%q must stay usable", n)
		}
	}
}
