package svc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"aegis/internal/config"
	"aegis/internal/store"
)

// vsftpdUserConfDir holds one vsftpd per-user config file per FTP login (see
// WriteUserConf). It's a variable so tests can point it at a temp dir.
var vsftpdUserConfDir = "/etc/vsftpd_user_conf"

// FTP manages FTP accounts backed by system users and served by vsftpd
// (chrooted, writeable). Each panel user gets a primary FTP account matching
// their username plus optional sub-accounts rooted in their home.
//
// Every FTP login still authenticates through PAM as its own system user (so
// passwords, suspend via usermod -L, and /etc/ftpusers keep working), but
// vsftpd's guest mapping then runs the whole session as the *owning panel
// user*: files a login uploads or creates are owned by <owner>:www-data, not
// by the login's own uid. See WriteUserConf.
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
user_config_dir=` + vsftpdUserConfDir + `
`
		if err := os.WriteFile(conf, []byte(content), 0o644); err != nil {
			return err
		}
	} else if _, err := f.ensureUserConfDirective(conf); err != nil {
		return err
	}
	if LookPath("systemctl") {
		out, err := RunTimeout(15*time.Second, "systemctl", "restart", "vsftpd")
		if err != nil && !strings.Contains(out, "Unit vsftpd.service could not be found") {
			return fmt.Errorf("vsftpd: %w", err)
		}
	}
	return nil
}

// ensureUserConfDirective makes sure the vsftpd config at conf points
// user_config_dir at vsftpdUserConfDir (appending the line if no such
// directive exists) and that the directory exists. It reports whether the
// config file changed, in which case vsftpd needs a restart to pick it up —
// per-user files themselves are re-read on every login and need no restart.
func (f *FTP) ensureUserConfDirective(conf string) (bool, error) {
	if err := os.MkdirAll(vsftpdUserConfDir, 0o755); err != nil {
		return false, err
	}
	b, err := os.ReadFile(conf)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "user_config_dir=") {
			return false, nil
		}
	}
	out := string(b)
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += "user_config_dir=" + vsftpdUserConfDir + "\n"
	return true, os.WriteFile(conf, []byte(out), 0o644)
}

// userConf renders the vsftpd per-user config for one FTP login: after PAM
// authenticates the login as its own system user, guest_enable/guest_username
// re-map the session to ownerUsername (the panel user), so every file created
// is owned by <owner>:www-data. virtual_use_local_privs makes the guest get
// normal local-user privileges (write_enable, local_umask) rather than
// anonymous ones; local_root pins the login to its own directory (guest
// mapping otherwise starts in the *guest's* home); and local_umask 022 gives
// uploads 644/755 so the www-data web server can read them (vsftpd's default
// umask is 077, which leaves uploaded files unreadable by nginx/apache).
func userConf(ownerUsername, home string) string {
	return "guest_enable=YES\n" +
		"guest_username=" + ownerUsername + "\n" +
		"virtual_use_local_privs=YES\n" +
		"local_root=" + home + "\n" +
		"local_umask=022\n"
}

// WriteUserConf writes the per-user vsftpd config that maps FTP login
// loginName onto panel user ownerUsername, chrooted at home.
func (f *FTP) WriteUserConf(loginName, ownerUsername, home string) error {
	if !ValidUsername(loginName) {
		return fmt.Errorf("invalid ftp login %q", loginName)
	}
	for _, v := range []string{ownerUsername, home} {
		if v == "" || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("invalid value %q for ftp user config", v)
		}
	}
	if err := os.MkdirAll(vsftpdUserConfDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(vsftpdUserConfDir, loginName), []byte(userConf(ownerUsername, home)), 0o644)
}

func (f *FTP) removeUserConf(loginName string) {
	if !ValidUsername(loginName) {
		return
	}
	_ = os.Remove(filepath.Join(vsftpdUserConfDir, loginName))
}

// SyncUserConfs (re)writes the per-user vsftpd config for every FTP account
// and hands each account's root directory to its owning panel user when it's
// still owned by the login's own system user — the pre-guest-mapping layout —
// since the mapped session runs as the owner and must be able to write there.
// Idempotent; called at startup so accounts created before guest mapping
// existed are migrated, and vsftpd is restarted only if the main config
// gained the user_config_dir directive.
func (f *FTP) SyncUserConfs(ctx context.Context) error {
	if _, err := os.Stat(f.vsftpdConfPath()); err == nil {
		changed, err := f.ensureUserConfDirective(f.vsftpdConfPath())
		if err != nil {
			return err
		}
		if changed && LookPath("systemctl") {
			if out, err := RunTimeout(15*time.Second, "systemctl", "restart", "vsftpd"); err != nil {
				slog.Warn("vsftpd restart after user_config_dir change failed", "err", err, "out", out)
			}
		}
	}
	accts, err := f.Store.ListFTPAccounts(ctx, 0)
	if err != nil {
		return err
	}
	for _, a := range accts {
		owner, err := f.Store.GetUserByID(ctx, a.UserID)
		if err != nil {
			slog.Warn("ftp sync: owner lookup failed", "ftp", a.Username, "user_id", a.UserID, "err", err)
			continue
		}
		if err := f.WriteUserConf(a.Username, owner.Username, a.HomeDir); err != nil {
			slog.Warn("ftp sync: user config write failed", "ftp", a.Username, "err", err)
			continue
		}
		f.handOverRoot(a.Username, owner.Username, a.HomeDir)
	}
	return nil
}

// handOverRoot chowns home (non-recursively) from the FTP login's own uid to
// the owning panel user's, keeping its group. It does nothing when the
// directory is missing or already owned by someone else (e.g. the owner).
func (f *FTP) handOverRoot(loginName, ownerUsername, home string) {
	fi, err := os.Stat(home)
	if err != nil {
		return
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	loginUID, err := UIDFor(loginName)
	if err != nil || int(st.Uid) != loginUID {
		return
	}
	ownerUID, err := UIDFor(ownerUsername)
	if err != nil || ownerUID == loginUID {
		return
	}
	if err := os.Chown(home, ownerUID, -1); err != nil {
		slog.Warn("ftp sync: could not hand FTP root to owner", "home", home, "owner", ownerUsername, "err", err)
	}
}

// Create provisions an FTP account:
//   - system user (useradd) with home inside the panel user's home
//   - password set via chpasswd (SHA-512 by default)
//   - row in ftp_accounts
func (f *FTP) Create(ctx context.Context, owner *store.User, username, password string, extra bool) (*store.FTPAccount, error) {
	home := filepath.Join(f.Cfg.HomeRoot, owner.Username)
	if extra {
		home = filepath.Join(home, username)
	}
	return f.provision(ctx, owner, username, password, home, "750")
}

// CreateScoped provisions an FTP account chrooted directly into an existing
// directory (e.g. a domain's own document root) instead of one derived from
// the account's own username under the owner's home, as Create does. The
// directory is left group-writable (770, not Create's 750) because it's
// typically shared with another system user already writing there — e.g. a
// domain's php-fpm pool, which runs as the owning panel user under the same
// www-data group (see PHP.EnsurePool) — rather than being this account's
// private home.
func (f *FTP) CreateScoped(ctx context.Context, owner *store.User, username, password, home string) (*store.FTPAccount, error) {
	return f.provision(ctx, owner, username, password, home, "770")
}

func (f *FTP) provision(ctx context.Context, owner *store.User, username, password, home, dirMode string) (*store.FTPAccount, error) {
	if !ValidUsername(username) {
		return nil, errors.New("invalid username (lowercase letters, digits, underscore, 3-30 chars)")
	}
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	if _, err := f.Store.GetFTPAccountByUsername(ctx, username); err == nil {
		return nil, fmt.Errorf("ftp account %s already exists", username)
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
	// The FTP session runs as the owning panel user (see WriteUserConf), so
	// the tree belongs to the owner, not to the FTP login's own system user.
	_ = os.MkdirAll(home, 0o755)
	_, _ = RunTimeout(10*time.Second, "chown", "-R", owner.Username+":www-data", home)
	_, _ = RunTimeout(10*time.Second, "chmod", dirMode, home)
	if err := f.WriteUserConf(username, owner.Username, home); err != nil {
		return nil, fmt.Errorf("ftp user config: %w", err)
	}

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
	f.removeUserConf(acct.Username)
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
