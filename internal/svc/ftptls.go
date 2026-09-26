package svc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// FTP over TLS.
//
// vsftpd used to run with ssl_enable=NO: every FTP login, and every file, crossed
// the internet in plaintext. An FTP account is a system account, so that
// password was also the account's Linux password (and, for the primary account,
// the panel password). FTP is now encrypted, and plaintext logins are refused.

// vsftpdTLSDirectives returns the vsftpd settings that force encryption.
// Explicit FTPS (AUTH TLS on port 21): clients that cannot do TLS are refused.
// require_ssl_reuse stays off because many clients (and NAT'd data connections)
// cannot satisfy it.
//
// Only options that vsftpd 3.0.5 (Debian/Ubuntu) knows are used. It has no
// ssl_tlsv1_1 / ssl_tlsv1_2 (they exist only in later builds): an unknown option
// makes vsftpd refuse to start at all, which took FTP down when this was first
// deployed. TLS 1.1 and older are refused by the system OpenSSL policy
// (MinProtocol), and ssl_tlsv1=NO covers TLS 1.0 explicitly. applyTLS also
// trial-starts vsftpd on the result, so an option a given build rejects can never
// again leave FTP down.
func vsftpdTLSDirectives(cert, key string) [][2]string {
	return [][2]string{
		{"ssl_enable", "YES"},
		{"rsa_cert_file", cert},
		{"rsa_private_key_file", key},
		{"force_local_logins_ssl", "YES"},
		{"force_local_data_ssl", "YES"},
		{"allow_anon_ssl", "NO"},
		{"ssl_sslv2", "NO"},
		{"ssl_sslv3", "NO"},
		{"ssl_tlsv1", "NO"},
		{"ssl_ciphers", "HIGH"},
		{"require_ssl_reuse", "NO"},
		{"max_login_fails", "3"},
		{"delay_failed_login", "3"},
	}
}

// setVsftpdDirectives makes every key=value in want present exactly once in the
// config at conf: an existing line is corrected, duplicates are dropped, missing
// keys are appended. Comments and every other line are left as they are. It
// reports whether the file changed (and only then rewrites it, keeping a
// one-time copy of the original next to it).
func setVsftpdDirectives(conf string, want [][2]string) (bool, error) {
	b, err := os.ReadFile(conf)
	if err != nil {
		return false, err
	}
	wantMap := map[string]string{}
	for _, kv := range want {
		wantMap[kv[0]] = kv[1]
	}
	seen := map[string]bool{}
	changed := false
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(ln)
		if t != "" && !strings.HasPrefix(t, "#") {
			if k, v, ok := strings.Cut(t, "="); ok {
				k = strings.TrimSpace(k)
				if nv, isWanted := wantMap[k]; isWanted {
					switch {
					case seen[k]:
						changed = true // drop a duplicate
					case strings.TrimSpace(v) != nv:
						seen[k], changed = true, true
						out = append(out, k+"="+nv)
					default:
						seen[k] = true
						out = append(out, ln)
					}
					continue
				}
			}
		}
		out = append(out, ln)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	for _, kv := range want {
		if !seen[kv[0]] {
			out = append(out, kv[0]+"="+kv[1])
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	if _, err := os.Stat(conf + ".pre-tls"); os.IsNotExist(err) {
		_ = os.WriteFile(conf+".pre-tls", b, 0o644)
	}
	return true, os.WriteFile(conf, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

// ftpCertPaths returns where the FTP certificate and key live.
func (f *FTP) ftpCertPaths() (cert, key string) {
	dir := filepath.Join(f.Cfg.Dir, "ftp")
	return filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
}

// ftpCertUsable reports whether the pair at cert/key loads and stays valid for
// at least another 30 days.
func ftpCertUsable(cert, key string) bool {
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil || len(pair.Certificate) == 0 {
		return false
	}
	c, err := x509.ParseCertificate(pair.Certificate[0])
	return err == nil && time.Until(c.NotAfter) > 30*24*time.Hour
}

// ensureFTPCert creates a private self-signed certificate for FTPS when there is
// no usable one. It is issued for the server's host name and addresses; clients
// will ask the user to trust it once (its fingerprint is logged).
func (f *FTP) ensureFTPCert() (certPath, keyPath string, err error) {
	certPath, keyPath = f.ftpCertPaths()
	if ftpCertUsable(certPath, keyPath) {
		return certPath, keyPath, nil
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o750); err != nil {
		return "", "", err
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return "", "", err
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "localhost"
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host, Organization: []string{"Aegis FTP"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{host, "localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	if ip := net.ParseIP(DetectPrimaryIP()); ip != nil {
		tpl.IPAddresses = append(tpl.IPAddresses, ip)
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}), 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(der)
	slog.Info("generated a self-signed FTP certificate", "host", host, "path", certPath, "sha256", fmt.Sprintf("%X", sum))
	return certPath, keyPath, nil
}

// vsftpdLoadCheck is vsftpdConfigLoads; tests replace it to prove the restore.
var vsftpdLoadCheck = vsftpdConfigLoads

// vsftpdConfigLoads trial-starts vsftpd on conf (on a spare port, for two
// seconds) to learn whether it accepts the file. vsftpd exits at once on an
// option it does not know; one that is still running when the time is up loaded
// fine. It returns nil when there is no vsftpd binary to ask.
func vsftpdConfigLoads(conf string) error {
	bin, err := exec.LookPath("vsftpd")
	if err != nil {
		if _, serr := os.Stat("/usr/sbin/vsftpd"); serr != nil {
			return nil
		}
		bin = "/usr/sbin/vsftpd"
	}
	b, err := os.ReadFile(conf)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	tmp, err := os.CreateTemp("", "vsftpd-trial-*.conf")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	trial := string(b) + fmt.Sprintf("\nlisten=YES\nlisten_ipv6=NO\nlisten_address=127.0.0.1\nlisten_port=%d\n", port)
	if _, err := tmp.WriteString(trial); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, runErr := exec.CommandContext(ctx, bin, tmp.Name()).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return nil // still running when we stopped it: the config loaded
	}
	if runErr == nil {
		return nil
	}
	return fmt.Errorf("vsftpd rejected the configuration (%v): %s", runErr, strings.TrimSpace(string(out)))
}

// applyTLS makes the vsftpd config at conf enforce TLS. It reports whether the
// config changed. If vsftpd would not start on the result, the previous config is
// put back and an error is returned, so a setting a given vsftpd build does not
// understand can never take FTP down.
func (f *FTP) applyTLS(conf string) (bool, error) {
	cert, key, err := f.ensureFTPCert()
	if err != nil {
		return false, fmt.Errorf("ftp certificate: %w", err)
	}
	orig, _ := os.ReadFile(conf)
	changed, err := setVsftpdDirectives(conf, vsftpdTLSDirectives(cert, key))
	if err != nil || !changed {
		return changed, err
	}
	if lerr := vsftpdLoadCheck(conf); lerr != nil {
		if werr := os.WriteFile(conf, orig, 0o644); werr != nil {
			return false, fmt.Errorf("%v; and restoring the previous config failed: %w", lerr, werr)
		}
		return false, fmt.Errorf("the TLS settings were not applied, previous config restored: %w", lerr)
	}
	return true, nil
}

// EnsureTLS brings an existing vsftpd install up to "TLS required", restarting
// it only if its config actually changed. It does nothing when vsftpd is not
// installed. Called at panel start so existing servers are upgraded.
func (f *FTP) EnsureTLS() error {
	conf := f.vsftpdConfPath()
	if _, err := os.Stat(conf); err != nil {
		return nil
	}
	changed, err := f.applyTLS(conf)
	if err != nil {
		return err
	}
	if changed && LookPath("systemctl") {
		if out, err := RunTimeout(15*time.Second, "systemctl", "restart", "vsftpd"); err != nil {
			return fmt.Errorf("restart vsftpd: %w: %s", err, out)
		}
		slog.Info("vsftpd now requires TLS for logins and data")
	}
	return nil
}
