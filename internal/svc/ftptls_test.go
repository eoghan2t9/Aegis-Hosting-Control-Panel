package svc

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aegis/internal/config"
)

// vsftpd ran with ssl_enable=NO, so FTP logins and files crossed the internet in
// plaintext, and an FTP password is also the account's Linux password.
func TestSetVsftpdDirectivesForcesTLSAndKeepsTheRest(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "vsftpd.conf")
	orig := `# my comment
listen=YES
anonymous_enable=NO
ssl_enable=NO
pasv_min_port=30100
ssl_tlsv1=YES
# ssl_sslv3=YES stays a comment
rsa_cert_file=/etc/ssl/certs/ssl-cert-snakeoil.pem
ssl_tlsv1=YES
`
	if err := os.WriteFile(conf, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	want := vsftpdTLSDirectives("/etc/aegis/ftp/cert.pem", "/etc/aegis/ftp/key.pem")
	changed, err := setVsftpdDirectives(conf, want)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	b, _ := os.ReadFile(conf)
	got := string(b)

	count := func(key string) int {
		n := 0
		for _, ln := range strings.Split(got, "\n") {
			if strings.HasPrefix(strings.TrimSpace(ln), key+"=") {
				n++
			}
		}
		return n
	}
	for _, kv := range want {
		if count(kv[0]) != 1 {
			t.Errorf("%s appears %d times, want exactly 1", kv[0], count(kv[0]))
		}
		if !strings.Contains(got, kv[0]+"="+kv[1]+"\n") {
			t.Errorf("%s is not set to %s", kv[0], kv[1])
		}
	}
	for _, keep := range []string{"# my comment", "listen=YES", "anonymous_enable=NO", "pasv_min_port=30100", "# ssl_sslv3=YES stays a comment"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q", keep)
		}
	}
	if strings.Contains(got, "ssl_enable=NO") || strings.Contains(got, "snakeoil") {
		t.Error("an insecure old value survived")
	}
	if pre, err := os.ReadFile(conf + ".pre-tls"); err != nil || string(pre) != orig {
		t.Errorf("the original config was not preserved (err %v)", err)
	}

	// Idempotent: a second run changes nothing and does not touch the file.
	before, _ := os.Stat(conf)
	changed, err = setVsftpdDirectives(conf, want)
	if err != nil || changed {
		t.Errorf("second run: changed=%v err=%v", changed, err)
	}
	after, _ := os.Stat(conf)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("an unchanged config was rewritten (that would restart vsftpd for nothing)")
	}
}

func TestEnsureFTPCert(t *testing.T) {
	cfg := config.Default()
	cfg.Dir = t.TempDir()
	f := NewFTP(cfg, nil)

	certPath, keyPath, err := f.ensureFTPCert()
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(keyPath); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("private key mode = %v (err %v), want 0600", st.Mode().Perm(), err)
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("the certificate and key do not match: %v", err)
	}
	c, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(c.NotAfter) < 9*365*24*time.Hour {
		t.Errorf("certificate expires too soon: %v", c.NotAfter)
	}
	host, _ := os.Hostname()
	if host != "" && c.VerifyHostname(host) != nil {
		t.Errorf("the certificate is not valid for the host name %q", host)
	}
	if c.VerifyHostname("127.0.0.1") != nil {
		t.Error("the certificate is not valid for 127.0.0.1")
	}

	// A usable certificate is reused, never regenerated (clients pin it).
	before, _ := os.ReadFile(certPath)
	if _, _, err := f.ensureFTPCert(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(certPath)
	if string(before) != string(after) {
		t.Error("a valid certificate was regenerated")
	}

	// A broken pair is replaced.
	if err := os.WriteFile(certPath, []byte("not a cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ftpCertUsable(certPath, keyPath) {
		t.Fatal("a corrupt certificate was accepted")
	}
	if _, _, err := f.ensureFTPCert(); err != nil {
		t.Fatal(err)
	}
	if !ftpCertUsable(certPath, keyPath) {
		t.Error("the corrupt certificate was not replaced")
	}
}

// The first deployment added ssl_tlsv1_1/ssl_tlsv1_2, which vsftpd 3.0.5 does not
// know. vsftpd exits on an unknown option, so FTP went down until a person
// fixed the file. This runs the REAL vsftpd binary against the settings we
// generate, so such a mistake now fails here instead of in production.
func TestVsftpdAcceptsTheGeneratedDirectives(t *testing.T) {
	if _, err := os.Stat("/usr/sbin/vsftpd"); err != nil {
		t.Skip("vsftpd is not installed")
	}
	cfg := config.Default()
	cfg.Dir = t.TempDir()
	f := NewFTP(cfg, nil)
	cert, key, err := f.ensureFTPCert()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "vsftpd.conf")
	if err := os.WriteFile(conf, []byte("anonymous_enable=NO\nlocal_enable=YES\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := setVsftpdDirectives(conf, vsftpdTLSDirectives(cert, key)); err != nil {
		t.Fatal(err)
	}
	if err := vsftpdConfigLoads(conf); err != nil {
		t.Fatalf("this vsftpd rejects the generated settings: %v", err)
	}

	// And the check really does catch a bad option (the mistake that took FTP down).
	bad := filepath.Join(dir, "bad.conf")
	b, _ := os.ReadFile(conf)
	if err := os.WriteFile(bad, append(b, []byte("ssl_tlsv1_2=YES\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vsftpdConfigLoads(bad); err == nil {
		t.Error("a config vsftpd cannot start on was reported as fine")
	}
}

// If vsftpd would not start on the new config, the previous one is put back.
func TestApplyTLSRestoresTheConfigWhenVsftpdRejectsIt(t *testing.T) {
	old := vsftpdLoadCheck
	vsftpdLoadCheck = func(string) error { return os.ErrInvalid }
	t.Cleanup(func() { vsftpdLoadCheck = old })

	cfg := config.Default()
	cfg.Dir = t.TempDir()
	f := NewFTP(cfg, nil)
	conf := filepath.Join(t.TempDir(), "vsftpd.conf")
	orig := "listen=YES\nssl_enable=NO\n"
	if err := os.WriteFile(conf, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := f.applyTLS(conf)
	if err == nil || changed {
		t.Fatalf("changed=%v err=%v, want an error and no change", changed, err)
	}
	if b, _ := os.ReadFile(conf); string(b) != orig {
		t.Errorf("the previous config was not restored:\n%s", b)
	}
}

func TestApplyTLSOnAnExistingInsecureConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Dir = t.TempDir()
	f := NewFTP(cfg, nil)
	conf := filepath.Join(t.TempDir(), "vsftpd.conf")
	if err := os.WriteFile(conf, []byte("listen=YES\nssl_enable=NO\nforce_local_logins_ssl=NO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := f.applyTLS(conf)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	b, _ := os.ReadFile(conf)
	for _, want := range []string{"ssl_enable=YES", "force_local_logins_ssl=YES", "force_local_data_ssl=YES", "ssl_tlsv1=NO", "ssl_sslv3=NO"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s", want)
		}
	}
	if changed, err := f.applyTLS(conf); err != nil || changed {
		t.Errorf("second apply: changed=%v err=%v", changed, err)
	}
}
