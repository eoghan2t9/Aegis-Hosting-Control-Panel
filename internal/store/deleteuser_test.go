package store

import (
	"context"
	"testing"
	"time"
)

// Every table that holds rows belonging to a user or to one of their domains.
// No foreign keys are declared, so DeleteUser has to clean each one itself.
var userOwnedTables = []string{
	"users", "domains", "domain_aliases", "dns_zones", "dns_records", "ssl_orders",
	"ftp_accounts", "databases", "sessions", "totp_challenges", "api_tokens",
	"mail_domains", "mailboxes", "mail_aliases", "cron_jobs", "containers",
}

func rowCounts(t *testing.T, s *Store) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, tbl := range userOwnedTables {
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[tbl] = n
	}
	return out
}

// seedUser creates one user with at least one row in every owned table.
func seedUser(t *testing.T, s *Store, name string) *User {
	t.Helper()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	u := &User{Username: name, Email: name + "@example.com", PasswordHash: "h", Role: RoleUser, Status: StatusActive, HomeDir: "/home/" + name}
	must(s.CreateUser(ctx, u))

	dom := &Domain{UserID: u.ID, Domain: name + ".example.com", DocumentRoot: "/home/" + name + "/site/public"}
	must(s.CreateDomain(ctx, dom))
	must(s.AddAlias(ctx, dom.ID, "www."+dom.Domain))
	zone := &DNSZone{DomainID: dom.ID, Provider: "local"}
	must(s.CreateZone(ctx, zone))
	must(s.CreateRecord(ctx, &DNSRecord{ZoneID: zone.ID, Name: "@", Type: "A", TTL: 300, Content: "192.0.2.1"}))
	must(s.CreateSSLOrder(ctx, &SSLOrder{DomainID: dom.ID, Status: "pending", Provider: "letsencrypt", Challenge: "http"}))

	md := &MailDomain{DomainID: dom.ID, Domain: dom.Domain}
	must(s.CreateMailDomain(ctx, md))
	must(s.CreateMailbox(ctx, &Mailbox{MailDomainID: md.ID, Localpart: "info", PasswordHash: "h", Enabled: true}))
	must(s.CreateMailAlias(ctx, &MailAlias{MailDomainID: md.ID, Source: "sales@" + dom.Domain, Destination: "info@" + dom.Domain}))

	must(s.CreateFTPAccount(ctx, &FTPAccount{UserID: u.ID, Username: name + "_ftp", PasswordHash: "h", HomeDir: dom.DocumentRoot, Enabled: true}))
	must(s.CreateDatabase(ctx, &Database{UserID: u.ID, Server: "mariadb", Name: name + "_db", DBUser: name + "_u", DBPassword: "pw"}))
	must(s.CreateSession(ctx, "sess-"+name, u.ID, time.Now().Add(time.Hour)))
	must(s.CreateTOTPChallenge(ctx, "totp-"+name, u.ID, time.Now().Add(time.Minute)))
	must(s.CreateAPIToken(ctx, &APIToken{UserID: u.ID, Label: "ci", TokenHash: "hash-" + name}))
	must(s.CreateCronJob(ctx, &CronJob{UserID: u.ID, Schedule: "* * * * *", Command: "true", Enabled: true}))
	must(s.CreateContainer(ctx, &Container{UserID: u.ID, DomainID: dom.ID, Name: name + "-app", Image: "nginx"}))
	return u
}

func TestDeleteUserRemovesEveryOwnedRowAndOnlyTheirs(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	bob := seedUser(t, s, "bob")
	want := rowCounts(t, s) // exactly one user's worth of rows
	for tbl, n := range want {
		if n == 0 {
			t.Fatalf("seedUser left %s empty; the test would not cover it", tbl)
		}
	}

	alice := seedUser(t, s, "alice")
	if err := s.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	got := rowCounts(t, s)
	for _, tbl := range userOwnedTables {
		if got[tbl] != want[tbl] {
			t.Errorf("after deleting alice, %s has %d rows, want %d (only bob's)", tbl, got[tbl], want[tbl])
		}
	}
	if _, err := s.GetUserByID(ctx, bob.ID); err != nil {
		t.Errorf("bob was affected by deleting alice: %v", err)
	}
	if _, err := s.GetUserByID(ctx, alice.ID); err == nil {
		t.Error("alice still exists")
	}
}

func TestCountAdmins(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if n, err := s.CountAdmins(ctx); err != nil || n != 0 {
		t.Fatalf("CountAdmins on empty = %d, %v", n, err)
	}
	for _, u := range []*User{
		{Username: "root_admin", Email: "a@example.com", PasswordHash: "h", Role: RoleAdmin, Status: StatusActive},
		{Username: "helper", Email: "b@example.com", PasswordHash: "h", Role: RoleUser, Status: StatusActive},
	} {
		if err := s.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.CountAdmins(ctx); err != nil || n != 1 {
		t.Errorf("CountAdmins = %d, %v; want 1", n, err)
	}
}
