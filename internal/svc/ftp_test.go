package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withTempUserConfDir(t *testing.T) string {
	t.Helper()
	old := vsftpdUserConfDir
	vsftpdUserConfDir = filepath.Join(t.TempDir(), "vsftpd_user_conf")
	t.Cleanup(func() { vsftpdUserConfDir = old })
	return vsftpdUserConfDir
}

func TestWriteUserConfMapsLoginToOwner(t *testing.T) {
	dir := withTempUserConfDir(t)
	f := &FTP{}
	if err := f.WriteUserConf("site_example_com", "alice", "/home/alice/example.com/public"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "site_example_com"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		"guest_enable=YES\n",
		"guest_username=alice\n",
		"virtual_use_local_privs=YES\n",
		"local_root=/home/alice/example.com/public\n",
		"local_umask=022\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("user conf missing %q, got:\n%s", want, got)
		}
	}
}

func TestWriteUserConfRejectsUnsafeInput(t *testing.T) {
	dir := withTempUserConfDir(t)
	f := &FTP{}
	cases := []struct{ login, owner, home string }{
		{"../etc/passwd", "alice", "/home/alice"},
		{"ab", "alice", "/home/alice"},
		{"good_login", "", "/home/alice"},
		{"good_login", "alice\nguest_username=root", "/home/alice"},
		{"good_login", "alice", "/home/alice\nlocal_root=/"},
	}
	for _, c := range cases {
		if err := f.WriteUserConf(c.login, c.owner, c.home); err == nil {
			t.Errorf("WriteUserConf(%q, %q, %q) succeeded, want error", c.login, c.owner, c.home)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("rejected inputs left files behind: %v", entries)
	}
}

func TestRemoveUserConf(t *testing.T) {
	dir := withTempUserConfDir(t)
	f := &FTP{}
	if err := f.WriteUserConf("bob", "bob", "/home/bob"); err != nil {
		t.Fatal(err)
	}
	f.removeUserConf("bob")
	if _, err := os.Stat(filepath.Join(dir, "bob")); !os.IsNotExist(err) {
		t.Errorf("user conf still present after remove: %v", err)
	}
	f.removeUserConf("../evil") // invalid names are ignored, must not panic or escape
}

func TestEnsureUserConfDirective(t *testing.T) {
	dir := withTempUserConfDir(t)
	f := &FTP{}
	conf := filepath.Join(t.TempDir(), "vsftpd.conf")

	// No trailing newline: the directive must land on its own line.
	if err := os.WriteFile(conf, []byte("listen=YES\nwrite_enable=YES"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := f.ensureUserConfDirective(conf)
	if err != nil || !changed {
		t.Fatalf("first call: changed=%v err=%v, want changed", changed, err)
	}
	b, _ := os.ReadFile(conf)
	want := "listen=YES\nwrite_enable=YES\nuser_config_dir=" + dir + "\n"
	if string(b) != want {
		t.Errorf("config after first call:\n%q\nwant:\n%q", b, want)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("user conf dir not created: %v", err)
	}

	// Idempotent: a second call must not duplicate the directive or report a change.
	changed, err = f.ensureUserConfDirective(conf)
	if err != nil || changed {
		t.Fatalf("second call: changed=%v err=%v, want unchanged", changed, err)
	}
	b2, _ := os.ReadFile(conf)
	if string(b2) != want {
		t.Errorf("config changed on second call:\n%q", b2)
	}

	// An admin-chosen directory must be respected, not overridden.
	custom := "user_config_dir=/srv/ftp-conf\n"
	if err := os.WriteFile(conf, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := f.ensureUserConfDirective(conf); err != nil || changed {
		t.Fatalf("custom directive: changed=%v err=%v, want unchanged", changed, err)
	}
}
