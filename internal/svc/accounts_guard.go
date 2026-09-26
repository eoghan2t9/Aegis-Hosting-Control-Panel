package svc

import (
	"errors"
	"fmt"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// reservedAccountNames are names no customer, FTP login or panel user may claim.
// Most exist on any Linux host; the rest belong to services the panel installs or
// manages, and would collide with their packages (a customer FTP login named
// "mysql" would be adopted by the database package's post-install script).
var reservedAccountNames = map[string]bool{
	"root": true, "daemon": true, "bin": true, "sys": true, "sync": true, "games": true,
	"man": true, "lp": true, "mail": true, "news": true, "uucp": true, "proxy": true,
	"backup": true, "list": true, "irc": true, "gnats": true, "nobody": true,
	"mysql": true, "mariadb": true, "postgres": true, "postgresql": true,
	"www": true, "wwwrun": true, "ftp": true, "ssh": true, "sshd": true, "vsftpd": true,
	"dovecot": true, "dovenull": true, "postfix": true, "vmail": true, "opendkim": true,
	"opendmarc": true, "spamd": true, "clamav": true, "fail2ban": true, "chrony": true,
	"ntp": true, "messagebus": true, "syslog": true, "tss": true, "uuidd": true,
	"tcpdump": true, "polkitd": true, "landscape": true, "ubuntu": true, "debian": true,
	"caddy": true, "nginx": true, "apache": true, "httpd": true, "redis": true,
	"memcache": true, "git": true, "docker": true, "containerd": true, "aegis": true,
}

// ReservedAccountName reports whether name may not be used for a new account.
func ReservedAccountName(name string) bool {
	return reservedAccountNames[strings.ToLower(strings.TrimSpace(name))]
}

// loginHome returns the home directory a system account has, or "" when the
// account does not exist.
func loginHome(username string) string {
	u, err := osuser.Lookup(username)
	if err != nil {
		return ""
	}
	return u.HomeDir
}

// checkLoginReuse decides whether an FTP login may be provisioned onto a system
// account that already exists. The old behaviour treated "already has a uid" as
// "fine, just set its password", so a customer could create an FTP account named
// "root", another customer, or any system service and take it over (and deleting
// that FTP account then ran userdel on it).
//
// An existing account is reusable only if it is a leftover of the very same
// login: a normal account (uid >= minPanelUID) whose home is exactly the
// directory being provisioned. A system account, or another customer's account,
// never is.
func checkLoginReuse(uid int, existingHome, wantHome string) error {
	inUse := errors.New("that name is already in use on this server; choose another")
	if uid < minPanelUID {
		return inUse
	}
	if existingHome == "" || filepath.Clean(existingHome) != filepath.Clean(wantHome) {
		return inUse
	}
	return nil
}

// managedLoginError returns an error unless username is an account the panel
// created and may therefore modify or delete: a normal (uid >= minPanelUID)
// account whose home lies inside homeRoot. System accounts are never touched.
func managedLoginError(username, homeRoot string) error {
	if ReservedAccountName(username) {
		return fmt.Errorf("refusing to modify the reserved account %q", username)
	}
	u, err := osuser.Lookup(username)
	if err != nil {
		return nil // no system account: nothing to protect (and nothing to change)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil || uid < minPanelUID {
		return fmt.Errorf("refusing to modify the system account %q (uid %s)", username, u.Uid)
	}
	root := filepath.Clean(homeRoot) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(u.HomeDir)+string(filepath.Separator), root) {
		return fmt.Errorf("refusing to modify %q: its home %q is outside %s", username, u.HomeDir, homeRoot)
	}
	return nil
}
