package svc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aegis/internal/config"
	"aegis/internal/store"
)

type purgeFixture struct {
	p        *Purger
	st       *store.Store
	homeRoot string
	sys      map[string]sysAccount // stubbed system accounts by name
}

func newPurgeFixture(t *testing.T) *purgeFixture {
	t.Helper()
	homeRoot := t.TempDir()
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &purgeFixture{st: st, homeRoot: homeRoot, sys: map[string]sysAccount{}}
	f.p = NewPurger(&config.Config{HomeRoot: homeRoot, DNSDir: t.TempDir(), CertDir: t.TempDir()}, st, nil, nil, &FTP{Store: st}, nil, nil)
	f.p.lookupUser = func(_ context.Context, name string) (sysAccount, bool) {
		a, ok := f.sys[name]
		return a, ok
	}
	withTempUserConfDir(t)
	return f
}

func (f *purgeFixture) addUser(t *testing.T, name, role string, ownerID int64) *store.User {
	t.Helper()
	u := &store.User{Username: name, Email: name + "@example.com", PasswordHash: "h", Role: role,
		Status: store.StatusActive, HomeDir: filepath.Join(f.homeRoot, name), OwnerID: ownerID}
	if err := f.st.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func hasBlocker(plan *PurgePlan, substr string) bool {
	for _, b := range plan.Blockers {
		if strings.Contains(b, substr) {
			return true
		}
	}
	return false
}

func TestPurgeCheckHome(t *testing.T) {
	p := &Purger{Cfg: &config.Config{HomeRoot: "/home"}}
	for home, ok := range map[string]bool{
		"/home/alice":       true,
		"/home":             false, // the root itself
		"/":                 false,
		"":                  false,
		"/home/alice/site":  false, // nested
		"/etc":              false,
		"/homealice":        false,
		"/home/../etc/x":    false, // cleaned by callers, but must not pass either way
		"/var/lib/aegis":    false,
		"/home/alice/../..": false,
	} {
		if err := p.checkHome(filepath.Clean(home)); (err == nil) != ok {
			t.Errorf("checkHome(%q) err=%v, want ok=%v", home, err, ok)
		}
	}
}

func TestSafeHostname(t *testing.T) {
	for name, ok := range map[string]bool{
		"example.com": true, "a-b.example.co.uk": true, "x": true,
		"": false, "../etc": false, "a/b": false, "a..b": false, "-bad.com": false, "Upper.com": false, "a b.com": false,
	} {
		if safeHostname(name) != ok {
			t.Errorf("safeHostname(%q) = %v, want %v", name, !ok, ok)
		}
	}
}

func TestPurgePlanInventoryAndBlockers(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	alice := f.addUser(t, "alice", store.RoleUser, 0)
	f.sys["alice"] = sysAccount{UID: 1500, Home: alice.HomeDir}

	dom := &store.Domain{UserID: alice.ID, Domain: "site.example.com", DocumentRoot: alice.HomeDir + "/site/public"}
	wf := &store.Domain{UserID: alice.ID, Domain: "webftp.site.example.com", DocumentRoot: alice.HomeDir + "/site/.webftp"}
	for _, d := range []*store.Domain{dom, wf} {
		if err := f.st.CreateDomain(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.st.CreateMailDomain(ctx, &store.MailDomain{DomainID: dom.ID, Domain: dom.Domain}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.CreateDatabase(ctx, &store.Database{UserID: alice.ID, Server: "mariadb", Name: "alice_db", DBUser: "alice_u", DBPassword: "pw"}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.CreateFTPAccount(ctx, &store.FTPAccount{UserID: alice.ID, Username: "alice", PasswordHash: "h", HomeDir: alice.HomeDir, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	plan, err := f.p.Plan(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 0 {
		t.Fatalf("clean user has blockers: %v", plan.Blockers)
	}
	// The Web FTP subdomain is torn down with its parent, so it is not listed on its own.
	if len(plan.Domains) != 1 || plan.Domains[0] != "site.example.com" ||
		len(plan.MailDomains) != 1 || len(plan.Databases) != 1 || plan.Databases[0] != "mariadb/alice_db" ||
		len(plan.FTPAccounts) != 1 || plan.Home != alice.HomeDir {
		t.Errorf("unexpected inventory: %+v", plan)
	}

	// Each safety rule blocks on its own.
	cases := []struct {
		name, want string
		mutate     func()
	}{
		{"system uid below 1000", "not a panel account", func() { f.sys["alice"] = sysAccount{UID: 33, Home: alice.HomeDir} }},
		{"system home differs", "differs from the panel record", func() { f.sys["alice"] = sysAccount{UID: 1500, Home: "/srv/elsewhere"} }},
	}
	for _, c := range cases {
		f.sys["alice"] = sysAccount{UID: 1500, Home: alice.HomeDir}
		c.mutate()
		got, _ := f.p.Plan(ctx, alice)
		if !hasBlocker(got, c.want) {
			t.Errorf("%s: blockers = %v, want one containing %q", c.name, got.Blockers, c.want)
		}
	}
	f.sys["alice"] = sysAccount{UID: 1500, Home: alice.HomeDir}

	// A reseller that still owns accounts cannot be deleted.
	reseller := f.addUser(t, "reseller", store.RoleReseller, 0)
	f.addUser(t, "client", store.RoleUser, reseller.ID)
	if got, _ := f.p.Plan(ctx, reseller); !hasBlocker(got, "client") {
		t.Errorf("reseller with a client has blockers %v, want the client named", got.Blockers)
	}

	// Overlapping homes.
	tight := f.addUser(t, "tight", store.RoleUser, 0)
	inner := f.addUser(t, "inner", store.RoleUser, 0)
	inner.HomeDir = filepath.Join(tight.HomeDir, "inner")
	if err := f.st.UpdateUser(ctx, inner); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.p.Plan(ctx, tight); !hasBlocker(got, "overlaps") {
		t.Errorf("overlapping homes not blocked: %v", got.Blockers)
	}

	// A home that is not directly inside HomeRoot.
	stray := f.addUser(t, "stray", store.RoleUser, 0)
	stray.HomeDir = "/etc"
	if got, _ := f.p.Plan(ctx, stray); !hasBlocker(got, "not directly inside") {
		t.Errorf("home /etc not blocked: %v", got.Blockers)
	}

	// The last admin, but not one of two.
	admin := f.addUser(t, "boss", store.RoleAdmin, 0)
	if got, _ := f.p.Plan(ctx, admin); !hasBlocker(got, "last admin") {
		t.Errorf("last admin not blocked: %v", got.Blockers)
	}
	f.addUser(t, "boss2", store.RoleAdmin, 0)
	if got, _ := f.p.Plan(ctx, admin); hasBlocker(got, "last admin") {
		t.Errorf("one of two admins wrongly blocked: %v", got.Blockers)
	}
}

func TestPurgeDeleteUserBlockedChangesNothing(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	admin := f.addUser(t, "boss", store.RoleAdmin, 0)
	if err := os.MkdirAll(admin.HomeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(admin.HomeDir, "keep.txt")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := f.p.DeleteUser(ctx, admin)
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v, want *BlockedError", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("a blocked delete removed files")
	}
	if _, err := f.st.GetUserByID(ctx, admin.ID); err != nil {
		t.Error("a blocked delete removed the panel user")
	}
}

func TestPurgeDeleteUserRemovesHomeRowsAndConfigs(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	alice := f.addUser(t, "alice", store.RoleUser, 0)
	bob := f.addUser(t, "bob", store.RoleUser, 0)
	for _, u := range []*store.User{alice, bob} {
		if err := os.MkdirAll(filepath.Join(u.HomeDir, "site", "public"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(u.HomeDir, "site", "public", "index.html"), []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// alice has an FTP account with a vsftpd config; bob's must survive.
	for _, a := range []struct {
		u    *store.User
		name string
	}{{alice, "alice_ftp"}, {bob, "bob_ftp"}} {
		if err := f.st.CreateFTPAccount(ctx, &store.FTPAccount{UserID: a.u.ID, Username: a.name, PasswordHash: "h", HomeDir: a.u.HomeDir, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		if err := f.p.FTP.WriteUserConf(a.name, a.u.Username, a.u.HomeDir); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.st.CreateCronJob(ctx, &store.CronJob{UserID: alice.ID, Schedule: "* * * * *", Command: "true", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	rep, err := f.p.DeleteUser(ctx, alice)
	if err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if rep.Plan.CronJobs != 1 {
		t.Errorf("plan cron jobs = %d, want 1", rep.Plan.CronJobs)
	}
	if _, err := os.Stat(alice.HomeDir); !os.IsNotExist(err) {
		t.Errorf("alice's home still exists: %v", err)
	}
	if _, err := f.st.GetUserByID(ctx, alice.ID); err == nil {
		t.Error("alice's panel record still exists")
	}
	if _, err := os.Stat(filepath.Join(vsftpdUserConfDir, "alice_ftp")); !os.IsNotExist(err) {
		t.Error("alice's vsftpd config was not pruned")
	}
	// bob is untouched.
	if _, err := os.Stat(filepath.Join(bob.HomeDir, "site", "public", "index.html")); err != nil {
		t.Errorf("bob's files were affected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vsftpdUserConfDir, "bob_ftp")); err != nil {
		t.Errorf("bob's vsftpd config was removed: %v", err)
	}
	if _, err := f.st.GetUserByID(ctx, bob.ID); err != nil {
		t.Errorf("bob's record was affected: %v", err)
	}
}

// A home that is a symlink must be unlinked, never followed out of HomeRoot.
func TestPurgeDoesNotFollowHomeSymlink(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	alice := f.addUser(t, "alice", store.RoleUser, 0)

	outside := t.TempDir()
	precious := filepath.Join(outside, "precious.txt")
	if err := os.WriteFile(precious, []byte("do not delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, alice.HomeDir); err != nil {
		t.Fatal(err)
	}

	if _, err := f.p.DeleteUser(ctx, alice); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := os.Stat(precious); err != nil {
		t.Fatalf("the purge followed the symlink and deleted files outside HomeRoot: %v", err)
	}
	if _, err := os.Lstat(alice.HomeDir); !os.IsNotExist(err) {
		t.Errorf("the home symlink itself was not removed: %v", err)
	}
}
