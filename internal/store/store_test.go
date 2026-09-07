package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUserCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := &User{Username: "alice", Email: "alice@example.com", PasswordHash: "hash",
		Role: RoleUser, Status: StatusActive, HomeDir: "/home/alice"}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 {
		t.Fatal("expected id to be assigned")
	}
	got, err := s.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "alice@example.com" || got.Status != StatusActive {
		t.Errorf("unexpected user: %+v", got)
	}
	// Duplicate username.
	if err := s.CreateUser(ctx, &User{Username: "alice"}); err != ErrConflict {
		t.Errorf("duplicate create err = %v, want ErrConflict", err)
	}
	// Update.
	got.Status = StatusSuspended
	if err := s.UpdateUser(ctx, got); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.GetUserByID(ctx, got.ID)
	if got2.Status != StatusSuspended {
		t.Errorf("status not updated")
	}
	// Password.
	if err := s.SetUserPassword(ctx, got.ID, "newhash"); err != nil {
		t.Fatal(err)
	}
	got3, _ := s.GetUserByID(ctx, got.ID)
	if got3.PasswordHash != "newhash" {
		t.Errorf("password not updated")
	}
	// Delete.
	if err := s.DeleteUser(ctx, got.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUserByID(ctx, got.ID); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestPackageDefaultSeed(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	pkgs, err := s.ListPackages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].Name != "starter" || !pkgs[0].IsDefault {
		t.Fatalf("expected seeded starter package, got %+v", pkgs)
	}
}

func TestDomainWithZoneAndRecords(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := &User{Username: "bob", Role: RoleUser, Status: StatusActive, HomeDir: "/home/bob"}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	d := &Domain{UserID: u.ID, Domain: "example.com", DocumentRoot: "/home/bob/example.com/public", PHPVersion: "8.3"}
	if err := s.CreateDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	z := &DNSZone{DomainID: d.ID, Provider: "local"}
	if err := s.CreateZone(ctx, z); err != nil {
		t.Fatal(err)
	}
	recs := []*DNSRecord{
		{ZoneID: z.ID, Name: "@", Type: "A", TTL: 3600, Content: "1.2.3.4"},
		{ZoneID: z.ID, Name: "www", Type: "CNAME", TTL: 3600, Content: "example.com"},
	}
	for _, r := range recs {
		if err := s.CreateRecord(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	zone, err := s.GetZoneByDomain(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ListRecords(ctx, zone.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d", len(got))
	}
	// Replace records.
	replacement := []*DNSRecord{{Name: "@", Type: "A", TTL: 300, Content: "5.6.7.8"}}
	if err := s.ReplaceRecords(ctx, zone.ID, replacement); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.ListRecords(ctx, zone.ID)
	if len(got2) != 1 || got2[0].Content != "5.6.7.8" {
		t.Fatalf("records not replaced: %+v", got2)
	}
}

func TestSessionsAndAudit(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	u := &User{Username: "carol", Role: RoleUser, Status: StatusActive}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour)
	if err := s.CreateSession(ctx, "sess1", u.ID, exp); err != nil {
		t.Fatal(err)
	}
	uid, err := s.GetSessionUser(ctx, "sess1")
	if err != nil {
		t.Fatal(err)
	}
	if uid != u.ID {
		t.Errorf("session user = %d, want %d", uid, u.ID)
	}
	if err := s.DeleteSession(ctx, "sess1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSessionUser(ctx, "sess1"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete")
	}
	if err := s.AppendAudit(ctx, u.ID, "carol", "test.action", "target", "detail", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	log, err := s.ListAudit(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 1 || log[0].Action != "test.action" || log[0].ActorName != "carol" {
		t.Fatalf("unexpected audit rows: %+v", log)
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.SetSetting(ctx, "key", "value"); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetSetting(ctx, "key")
	if err != nil || v != "value" {
		t.Fatalf("setting round trip = %q, %v", v, err)
	}
	if err := s.SetSetting(ctx, "key", "value2"); err != nil {
		t.Fatal(err)
	}
	v, _ = s.GetSetting(ctx, "key")
	if v != "value2" {
		t.Errorf("setting upsert failed: %q", v)
	}
}
