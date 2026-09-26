package svc

import (
	"encoding/json"
	"strings"
	"testing"

	"aegis/internal/store"
)

// Backup manifests are written into every archive and any remote target, so
// database passwords must never end up in them.
func TestRedactDatabasesKeepsPasswordsOutOfManifest(t *testing.T) {
	rows := []*store.Database{
		{ID: 1, UserID: 7, Server: "mariadb", Name: "db_alice", DBUser: "db_alice", DBPassword: "s3cret-Passw0rd"},
		{ID: 2, UserID: 7, Server: "postgres", Name: "db_bob", DBUser: "db_bob", DBPassword: "another-Secret-1"},
	}
	red := redactDatabases(rows)

	if len(red) != 2 || red[0].Name != "db_alice" || red[1].Server != "postgres" || red[0].ID != 1 {
		t.Fatalf("redaction changed non-secret fields: %+v %+v", red[0], red[1])
	}
	for _, d := range red {
		if d.DBPassword != "" {
			t.Errorf("%s still carries a password", d.Name)
		}
	}
	// The caller's rows must be untouched: the dump step still needs them.
	if rows[0].DBPassword != "s3cret-Passw0rd" || rows[1].DBPassword != "another-Secret-1" {
		t.Error("redactDatabases mutated its input")
	}

	b, err := json.Marshal(ManifestUser{Username: "alice", Databases: red})
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"s3cret-Passw0rd", "another-Secret-1", "db_password"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("manifest JSON contains %q: %s", leak, b)
		}
	}
	if !strings.Contains(string(b), "db_alice") {
		t.Errorf("manifest lost the database names: %s", b)
	}
}
