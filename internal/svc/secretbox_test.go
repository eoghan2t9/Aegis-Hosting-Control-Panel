package svc

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"aegis/internal/store"
)

// The real Cipher must satisfy store.SecretBox and round-trip a database
// password through the store, leaving only ciphertext in the table.
func TestStoreSecretBoxWithCipher(t *testing.T) {
	ctx := context.Background()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, err := NewCipher("test-panel-secret")
	if err != nil {
		t.Fatal(err)
	}
	st.SetSecretBox(c)

	d := &store.Database{UserID: 1, Server: "mariadb", Name: "db_alice", DBUser: "db_alice", DBPassword: "s3cret-Passw0rd"}
	if err := st.CreateDatabase(ctx, d); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := st.DB().QueryRow("SELECT db_password FROM databases WHERE id = ?", d.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "s3cret-Passw0rd") || !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatalf("password not encrypted at rest: %q", raw)
	}
	got, err := st.GetDatabase(ctx, d.ID)
	if err != nil || got.DBPassword != "s3cret-Passw0rd" {
		t.Fatalf("round trip = %+v, err %v", got, err)
	}

	// A different panel secret cannot read it.
	other, _ := NewCipher("a-different-secret")
	st.SetSecretBox(other)
	if _, err := st.GetDatabase(ctx, d.ID); err == nil {
		t.Error("read with the wrong panel secret succeeded")
	}
}
