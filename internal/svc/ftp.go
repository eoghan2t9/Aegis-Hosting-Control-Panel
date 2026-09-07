package svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"aegis/internal/config"
	"aegis/internal/store"
)

// FTP manages FTP accounts backed by system users and served by vsftpd
// (chrooted, writeable). Each panel user gets a primary FTP account matching
// their username plus optional sub-accounts rooted in their home.
type FTP struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewFTP(cfg *config.Config, st *store.Store) *FTP {
	return &FTP{Cfg: cfg, Store: st}
}

// vsftpdConfPath returns the vsftpd config location (Debian/Ubuntu default).
func (f *FTP) vsftpdConfPath() string {
	if _, err := os.Stat("/etc/vsftpd.conf"); err == nil {
		return "/etc/vsftpd.conf"
	}
	if _, err := os.Stat("/etc/vsftpd/vsftpd.conf"); err == nil {
		return "/etc/vsftpd/vsftpd.conf"
	}
	return "/etc/vsftpd.conf"
}

// EnsureVsftpd writes a sane vsftpd config when missing and starts the service.
func (f *FTP) EnsureVsftpd() error {
	conf := f.vsftpdConfPath()
	if _, err := os.Stat(conf); os.IsNotExist(err) {
		content := `listen=YES
listen_ipv6=NO
anonymous_enable=NO
local_enable=YES
write_enable=YES
local_umask=022
dirmessage_enable=YES
use_localtime=YES
xferlog_enable=YES
connect_from_port_20=YES
chroot_local_user=YES
allow_writeable_chroot=YES
pasv_min_port=40000
pasv_max_port=40100
seccomp_sandbox=NO
`
		if err := os.WriteFile(conf, []byte(content), 0o644); err != nil {
			return err
		}
	}
	if LookPath("systemctl") {
		out, err := RunTimeout(15*time.Second, "systemctl", "restart", "vsftpd")
		if err != nil && !strings.Contains(out, "Unit vsftpd.service could not be found") {
			return fmt.Errorf("vsftpd: %w", err)
		}
	}
	return nil
}

// Create provisions an FTP account:
//   - system user (useradd) with home inside the panel user's home
//   - password set via chpasswd (SHA-512 by default)
//   - row in ftp_accounts
func (f *FTP) Create(ctx context.Context, owner *store.User, username, password string, extra bool) (*store.FTPAccount, error) {
	if !ValidUsername(username) {
		return nil, errors.New("invalid username (lowercase letters, digits, underscore, 3-30 chars)")
	}
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	if _, err := f.Store.GetFTPAccountByUsername(ctx, username); err == nil {
		return nil, fmt.Errorf("ftp account %s already exists", username)
	}

	home := filepath.Join(f.Cfg.HomeRoot, owner.Username)
	if extra {
		home = filepath.Join(home, username)
	}

	// Ensure the system user exists (idempotent).
	uid, err := UIDFor(username)
	if err != nil {
		if _, err := RunTimeout(15*time.Second, "useradd", "-m", "-d", home, "-s", "/sbin/nologin", "-g", "www-data", username); err != nil {
			return nil, fmt.Errorf("create system user: %w", err)
		}
		uid, _ = UIDFor(username)
	}
	_ = uid

	// Set the password.
	if err := SetSystemPassword(username, password); err != nil {
		return nil, err
	}
	// Ensure home exists with the right ownership.
	_ = os.MkdirAll(home, 0o755)
	_, _ = RunTimeout(10*time.Second, "chown", "-R", username+":www-data", home)
	_, _ = RunTimeout(10*time.Second, "chmod", "750", home)

	hash, err := authHash(password)
	if err != nil {
		return nil, err
	}
	acct := &store.FTPAccount{
		UserID:       owner.ID,
		Username:     username,
		PasswordHash: hash,
		HomeDir:      home,
		Enabled:      true,
	}
	if err := f.Store.CreateFTPAccount(ctx, acct); err != nil {
		return nil, err
	}
	_ = f.EnsureVsftpd()
	return acct, nil
}

// ResetPassword changes an FTP account's password (system + store).
func (f *FTP) ResetPassword(ctx context.Context, acct *store.FTPAccount, password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if err := SetSystemPassword(acct.Username, password); err != nil {
		return err
	}
	hash, err := authHash(password)
	if err != nil {
		return err
	}
	acct.PasswordHash = hash
	return f.Store.UpdateFTPAccount(ctx, acct)
}

// Delete removes an FTP account: system user and row.
func (f *FTP) Delete(ctx context.Context, acct *store.FTPAccount) error {
	// Only delete the system user if they're not a panel user.
	isPanelUser := false
	if u, err := f.Store.GetUserByUsername(ctx, acct.Username); err == nil && u != nil {
		isPanelUser = true
	}
	if !isPanelUser {
		_, _ = RunTimeout(15*time.Second, "userdel", acct.Username)
	}
	return f.Store.DeleteFTPAccount(ctx, acct.ID)
}

// SetSystemPassword sets a system user's password via chpasswd (SHA-512).
func SetSystemPassword(username, password string) error {
	cmd := exec.Command("chpasswd")
	cmd.Stdin = strings.NewReader(username + ":" + password + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("chpasswd: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// authHash returns a bcrypt hash for storing in the panel DB (the system user
// itself uses /etc/shadow, which chpasswd manages).
func authHash(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

// ToggleEnabled suspends/unsuspends an FTP account by locking the system user.
func (f *FTP) ToggleEnabled(ctx context.Context, acct *store.FTPAccount, enabled bool) error {
	acct.Enabled = enabled
	if enabled {
		_, _ = RunTimeout(10*time.Second, "usermod", "-U", acct.Username)
	} else {
		_, _ = RunTimeout(10*time.Second, "usermod", "-L", acct.Username)
	}
	return f.Store.UpdateFTPAccount(ctx, acct)
}
