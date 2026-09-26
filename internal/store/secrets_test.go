package store

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// reverseBox is a stand-in SecretBox: not secure, but reversible, distinct
// from the plaintext, and able to fail on demand.
type reverseBox struct{ failDecrypt bool }

func (b reverseBox) Encrypt(p string) (string, error) {
	r := []byte(p)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return base64.StdEncoding.EncodeToString(r), nil
}

func (b reverseBox) Decrypt(e string) (string, error) {
	if b.failDecrypt {
		return "", errors.New("wrong secret")
	}
	raw, err := base64.StdEncoding.DecodeString(e)
	if err != nil {
		return "", err
	}
	for i, j := 0, len(raw)-1; i < j; i, j = i+1, j-1 {
		raw[i], raw[j] = raw[j], raw[i]
	}
	return string(raw), nil
}

func rawDBPassword(t *testing.T, s *Store, id int64) string {
	t.Helper()
	var v string
	if err := s.db.QueryRow("SELECT db_password FROM databases WHERE id = ?", id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDatabasePasswordEncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	s.SetSecretBox(reverseBox{})

	d := &Database{UserID: 1, Server: "mariadb", Name: "db_alice", DBUser: "db_alice", DBPassword: "s3cret-Passw0rd"}
	if err := s.CreateDatabase(ctx, d); err != nil {
		t.Fatal(err)
	}
	raw := rawDBPassword(t, s, d.ID)
	if strings.Contains(raw, "s3cret-Passw0rd") || !strings.HasPrefix(raw, encPrefix) {
		t.Fatalf("stored value is not encrypted: %q", raw)
	}
	got, err := s.GetDatabase(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DBPassword != "s3cret-Passw0rd" {
		t.Errorf("GetDatabase password = %q, want plaintext", got.DBPassword)
	}
	list, err := s.ListDatabases(ctx, 1)
	if err != nil || len(list) != 1 || list[0].DBPassword != "s3cret-Passw0rd" {
		t.Errorf("ListDatabases = %+v, err %v", list, err)
	}
}

func TestEncryptLegacySecrets(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t) // no box yet: rows are written as plaintext, like before

	plain := &Database{UserID: 1, Server: "mariadb", Name: "db_plain", DBUser: "u1", DBPassword: "plain-one"}
	empty := &Database{UserID: 1, Server: "mariadb", Name: "db_empty", DBUser: "u2", DBPassword: ""}
	for _, d := range []*Database{plain, empty} {
		if err := s.CreateDatabase(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if rawDBPassword(t, s, plain.ID) != "plain-one" {
		t.Fatal("precondition: legacy row should be plaintext")
	}

	// Once a box is set, legacy plaintext is still readable before migration.
	s.SetSecretBox(reverseBox{})
	got, err := s.GetDatabase(ctx, plain.ID)
	if err != nil || got.DBPassword != "plain-one" {
		t.Fatalf("legacy read = %+v, err %v", got, err)
	}

	n, err := s.EncryptLegacySecrets(ctx)
	if err != nil || n != 1 {
		t.Fatalf("EncryptLegacySecrets = %d, %v; want 1 (empty password skipped)", n, err)
	}
	if raw := rawDBPassword(t, s, plain.ID); !strings.HasPrefix(raw, encPrefix) || strings.Contains(raw, "plain-one") {
		t.Errorf("row not encrypted after migration: %q", raw)
	}
	if raw := rawDBPassword(t, s, empty.ID); raw != "" {
		t.Errorf("empty password changed to %q", raw)
	}
	got, err = s.GetDatabase(ctx, plain.ID)
	if err != nil || got.DBPassword != "plain-one" {
		t.Errorf("read after migration = %+v, err %v", got, err)
	}

	// Idempotent: a second run must not re-encrypt already-encrypted rows.
	before := rawDBPassword(t, s, plain.ID)
	if n, err := s.EncryptLegacySecrets(ctx); err != nil || n != 0 {
		t.Errorf("second run = %d, %v; want 0", n, err)
	}
	if after := rawDBPassword(t, s, plain.ID); after != before {
		t.Errorf("second run rewrote the row: %q -> %q", before, after)
	}
}

func TestEncryptLegacySecretsNeedsBox(t *testing.T) {
	if _, err := newTestStore(t).EncryptLegacySecrets(context.Background()); err == nil {
		t.Error("EncryptLegacySecrets without a box succeeded, want error")
	}
}

func TestOpenSecretFailsLoudly(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	s.SetSecretBox(reverseBox{})
	d := &Database{UserID: 1, Server: "mariadb", Name: "db_alice", DBUser: "u", DBPassword: "pw-12345678"}
	if err := s.CreateDatabase(ctx, d); err != nil {
		t.Fatal(err)
	}

	// Wrong panel secret: the read must error, not return ciphertext as a password.
	s.SetSecretBox(reverseBox{failDecrypt: true})
	if got, err := s.GetDatabase(ctx, d.ID); err == nil {
		t.Errorf("decrypt failure returned no error, password %q", got.DBPassword)
	}
	// Encrypted row but no box configured at all.
	s.SetSecretBox(nil)
	if _, err := s.GetDatabase(ctx, d.ID); err == nil {
		t.Error("encrypted row with no secret box returned no error")
	}
}
