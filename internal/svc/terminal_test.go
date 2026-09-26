package svc

import (
	"strings"
	"testing"
)

// The panel runs as root and a child process inherits its parent's credentials,
// so the terminal must drop to the customer's own uid/gid explicitly. Before
// this it did not, and every customer with the terminal feature got a root shell.
func TestTerminalCommandRunsAsTheAccount(t *testing.T) {
	cmd, err := terminalCommand("alice", "/home/alice", 1001, 1001, []uint32{1001, 33})
	if err != nil {
		t.Fatal(err)
	}
	cred := cmd.SysProcAttr.Credential
	if cred == nil {
		t.Fatal("no Credential set: the shell would run with the panel's (root) identity")
	}
	if cred.Uid != 1001 || cred.Gid != 1001 {
		t.Errorf("credential = %d:%d, want 1001:1001", cred.Uid, cred.Gid)
	}
	if len(cred.Groups) != 2 || cred.Groups[0] != 1001 || cred.Groups[1] != 33 {
		t.Errorf("supplementary groups = %v, want [1001 33]", cred.Groups)
	}
	if !cmd.SysProcAttr.Setsid || !cmd.SysProcAttr.Setctty {
		t.Error("shell must be a session leader with a controlling terminal")
	}
	if cmd.Dir != "/home/alice" {
		t.Errorf("Dir = %q", cmd.Dir)
	}
}

func TestTerminalCommandRefusesRoot(t *testing.T) {
	for _, tc := range []struct{ uid, gid uint32 }{{0, 1001}, {1001, 0}, {0, 0}} {
		if _, err := terminalCommand("x", "/home/x", tc.uid, tc.gid, nil); err == nil {
			t.Errorf("uid=%d gid=%d: expected a refusal", tc.uid, tc.gid)
		}
	}
	// End to end through the real lookup: the root account must be refused.
	uid, gid, groups, err := terminalIdentity("root")
	if err != nil {
		t.Skip("no root entry in the user database")
	}
	if _, err := terminalCommand("root", "/root", uid, gid, groups); err == nil {
		t.Error("a terminal for the root account must be refused")
	}
}

// The panel's environment holds the database admin passwords (aegis.env). A
// customer's shell must get a clean, minimal environment instead.
func TestTerminalEnvDoesNotLeakPanelSecrets(t *testing.T) {
	t.Setenv("AEGIS_MARIADB_PASSWORD", "hunter2-mariadb")
	t.Setenv("AEGIS_POSTGRES_PASSWORD", "hunter2-postgres")
	t.Setenv("SOME_OTHER_SECRET", "hunter2-other")

	cmd, err := terminalCommand("alice", "/home/alice", 1001, 1001, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Env == nil {
		t.Fatal("cmd.Env is nil, which makes the child inherit the panel's environment")
	}
	for _, kv := range cmd.Env {
		if strings.Contains(kv, "hunter2") || strings.HasPrefix(kv, "AEGIS_") {
			t.Errorf("panel secret leaked into the shell environment: %q", kv)
		}
	}
	want := map[string]bool{"HOME=/home/alice": false, "USER=alice": false, "LOGNAME=alice": false}
	for _, kv := range cmd.Env {
		if _, ok := want[kv]; ok {
			want[kv] = true
		}
	}
	for kv, seen := range want {
		if !seen {
			t.Errorf("environment is missing %q", kv)
		}
	}
}

func TestTerminalIdentityUnknownUser(t *testing.T) {
	if _, _, _, err := terminalIdentity("zz_no_such_user_exists"); err == nil {
		t.Error("expected an error for an account with no system user")
	}
}
