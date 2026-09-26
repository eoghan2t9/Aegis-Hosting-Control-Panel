package api

import (
	"encoding/json"
	"strings"
	"testing"

	"aegis/internal/store"
)

// The panel's FTP page reads the account fields it always has, plus
// webftp_domains to decide whether to show the "open in Web FTP" button. The
// embedded account must flatten into the same JSON object, and an account with
// no Web FTP must serialise an empty array (not null) so the UI can .length it.
func TestFTPAccountViewJSON(t *testing.T) {
	acct := &store.FTPAccount{ID: 4, UserID: 7, Username: "site_alice", HomeDir: "/home/alice/site", Enabled: true}

	with, err := json.Marshal(ftpAccountView{FTPAccount: acct, WebFTPDomains: []string{"site.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(with, &got); err != nil {
		t.Fatal(err)
	}
	if got["username"] != "site_alice" || got["home_dir"] != "/home/alice/site" || got["enabled"] != true {
		t.Errorf("account fields not flattened into the view: %s", with)
	}
	if d, _ := got["webftp_domains"].([]any); len(d) != 1 || d[0] != "site.example.com" {
		t.Errorf("webftp_domains = %v, want [site.example.com]", got["webftp_domains"])
	}

	none, _ := json.Marshal(ftpAccountView{FTPAccount: acct, WebFTPDomains: []string{}})
	if !strings.Contains(string(none), `"webftp_domains":[]`) {
		t.Errorf("empty webftp_domains must be [], got: %s", none)
	}
	// The password hash must never be serialised into the list.
	if strings.Contains(string(none), "password") {
		t.Errorf("FTP account view leaks a password field: %s", none)
	}
}
