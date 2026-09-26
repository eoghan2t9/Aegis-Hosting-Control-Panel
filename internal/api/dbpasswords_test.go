package api

import (
	"encoding/json"
	"strings"
	"testing"

	"aegis/internal/store"
)

// The database list must never carry passwords, while a freshly created row
// (returned once by create) still must.
func TestWithoutPasswords(t *testing.T) {
	rows := []*store.Database{
		{ID: 1, UserID: 7, Server: "mariadb", Name: "db_alice", DBUser: "db_alice", DBPassword: "s3cret-Passw0rd"},
	}
	list := withoutPasswords(rows)

	b, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "s3cret-Passw0rd") || strings.Contains(string(b), "db_password") {
		t.Errorf("list JSON leaks the password: %s", b)
	}
	if !strings.Contains(string(b), "db_alice") {
		t.Errorf("list JSON lost the row: %s", b)
	}
	if rows[0].DBPassword != "s3cret-Passw0rd" {
		t.Error("withoutPasswords mutated its input")
	}

	// A single row with a password (create / credentials responses) keeps it.
	one, _ := json.Marshal(rows[0])
	if !strings.Contains(string(one), `"db_password":"s3cret-Passw0rd"`) {
		t.Errorf("credentials response lost the password: %s", one)
	}
}
