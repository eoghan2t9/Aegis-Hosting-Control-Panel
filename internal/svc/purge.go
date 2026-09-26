package svc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"aegis/internal/config"
	"aegis/internal/store"
)

// minPanelUID is the lowest uid a panel account can have. useradd hands out
// uids from 1000 up; anything below is a system account and is never purged.
const minPanelUID = 1000

// safeHostRe matches a plain hostname, so a domain name is never able to
// carry a path separator or ".." into a filesystem path we delete.
var safeHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

func safeHostname(s string) bool { return safeHostRe.MatchString(s) && !strings.Contains(s, "..") }

// Purger deletes a panel user together with everything they own: containers,
// cron jobs, mail domains, domains (vhosts, PHP pools, certificates, local DNS
// zones), databases, FTP accounts, the system account and its home directory,
// and finally the panel rows. Deleting only the panel row (what
// Store.DeleteUser does) leaves all of that behind on the server.
//
// Order matters. Everything that can fail and is hard to redo (mail, domains,
// databases) runs first and stops the purge with the user still intact, so it
// can simply be run again; the system account, home directory and panel rows
// go only once those have succeeded. Backup archives are deliberately kept —
// they are the recovery path — and can be deleted from the Backups page.
type Purger struct {
	Cfg     *config.Config
	Store   *store.Store
	Domains *Domains
	DB      *Databases
	FTP     *FTP
	Mail    *Mail
	Docker  *Docker

	// lookupUser reports the system account for a name. It is a field so tests
	// can run without touching /etc/passwd.
	lookupUser func(ctx context.Context, name string) (sysAccount, bool)

	mu   sync.Mutex
	busy map[int64]bool
}

type sysAccount struct {
	UID  int
	Home string
}

func NewPurger(cfg *config.Config, st *store.Store, domains *Domains, db *Databases, ftp *FTP, mail *Mail, docker *Docker) *Purger {
	return &Purger{Cfg: cfg, Store: st, Domains: domains, DB: db, FTP: ftp, Mail: mail, Docker: docker,
		lookupUser: systemAccount, busy: map[int64]bool{}}
}

// systemAccount looks a user up via getent (works with NSS as well as files).
func systemAccount(ctx context.Context, name string) (sysAccount, bool) {
	out, err := Exec(ctx, "getent", "passwd", name)
	if err != nil {
		return sysAccount{}, false
	}
	f := strings.Split(strings.TrimSpace(out), ":")
	if len(f) < 6 {
		return sysAccount{}, false
	}
	uid, err := strconv.Atoi(f[2])
	if err != nil {
		return sysAccount{}, false
	}
	return sysAccount{UID: uid, Home: f[5]}, true
}

// PurgePlan is what deleting a user would remove, plus anything that forbids
// it. It powers the confirmation dialog and the CLI's dry run.
type PurgePlan struct {
	Username    string   `json:"username"`
	Home        string   `json:"home"`
	Domains     []string `json:"domains"`
	MailDomains []string `json:"mail_domains"`
	Databases   []string `json:"databases"`
	FTPAccounts []string `json:"ftp_accounts"`
	Containers  int      `json:"containers"`
	CronJobs    int      `json:"cron_jobs"`
	APITokens   int      `json:"api_tokens"`
	Blockers    []string `json:"blockers"`
}

// PurgeReport is the outcome of a completed purge.
type PurgeReport struct {
	Plan     *PurgePlan `json:"plan"`
	Warnings []string   `json:"warnings"`
}

// BlockedError is returned when a user may not be deleted at all (see
// PurgePlan.Blockers); nothing has been changed when it is.
type BlockedError struct {
	User     string
	Blockers []string
}

func (e *BlockedError) Error() string {
	return "cannot delete " + e.User + ": " + strings.Join(e.Blockers, "; ")
}

func (p *Purger) homeOf(u *store.User) string {
	home := u.HomeDir
	if home == "" {
		home = filepath.Join(p.Cfg.HomeRoot, u.Username)
	}
	return filepath.Clean(home)
}

// checkHome refuses any directory that is not exactly <HomeRoot>/<name>, so a
// bad record can never point the recursive delete at "/", at HomeRoot itself,
// or somewhere deeper or elsewhere on the filesystem.
func (p *Purger) checkHome(home string) error {
	root := filepath.Clean(p.Cfg.HomeRoot)
	if home == "" || home == "/" || home == root || filepath.Dir(home) != root {
		return fmt.Errorf("home directory %q is not directly inside %s", home, root)
	}
	return nil
}

func within(child, parent string) bool {
	return child == parent || strings.HasPrefix(child+"/", parent+"/")
}

// Plan inventories what deleting u would remove and lists the reasons it must
// not proceed (Blockers). It changes nothing.
func (p *Purger) Plan(ctx context.Context, u *store.User) (*PurgePlan, error) {
	plan := &PurgePlan{Username: u.Username, Home: p.homeOf(u)}

	doms, err := p.Store.ListDomains(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for _, d := range doms {
		if strings.HasPrefix(d.Domain, "webftp.") {
			continue // torn down with its parent
		}
		plan.Domains = append(plan.Domains, d.Domain)
		if _, err := p.Store.GetMailDomainByDomainID(ctx, d.ID); err == nil {
			plan.MailDomains = append(plan.MailDomains, d.Domain)
		}
	}
	dbs, err := p.Store.ListDatabases(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for _, d := range dbs {
		plan.Databases = append(plan.Databases, d.Server+"/"+d.Name)
	}
	ftps, err := p.Store.ListFTPAccounts(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for _, a := range ftps {
		plan.FTPAccounts = append(plan.FTPAccounts, a.Username)
	}
	if cs, err := p.Store.ListContainers(ctx, u.ID); err == nil {
		plan.Containers = len(cs)
	}
	if js, err := p.Store.ListCronJobs(ctx, u.ID); err == nil {
		plan.CronJobs = len(js)
	}
	if ts, err := p.Store.ListAPITokens(ctx, u.ID); err == nil {
		plan.APITokens = len(ts)
	}

	// --- reasons not to proceed ---
	if err := p.checkHome(plan.Home); err != nil {
		plan.Blockers = append(plan.Blockers, err.Error())
	}
	if acct, ok := p.lookupUser(ctx, u.Username); ok {
		if acct.UID < minPanelUID {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("system account %q (uid %d) is not a panel account", u.Username, acct.UID))
		}
		if filepath.Clean(acct.Home) != plan.Home {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("system account home %q differs from the panel record %q", acct.Home, plan.Home))
		}
	}
	if subs, err := p.Store.ListUsers(ctx, u.ID); err == nil && len(subs) > 0 {
		names := make([]string, len(subs))
		for i, s := range subs {
			names[i] = s.Username
		}
		plan.Blockers = append(plan.Blockers, "it still owns accounts that must be deleted or reassigned first: "+strings.Join(names, ", "))
	}
	if all, err := p.Store.ListUsers(ctx, 0); err == nil {
		for _, o := range all {
			if o.ID == u.ID {
				continue
			}
			if oh := p.homeOf(o); within(oh, plan.Home) || within(plan.Home, oh) {
				plan.Blockers = append(plan.Blockers, fmt.Sprintf("its home directory overlaps account %q", o.Username))
			}
		}
	}
	if u.Role == store.RoleAdmin {
		if n, err := p.Store.CountAdmins(ctx); err == nil && n <= 1 {
			plan.Blockers = append(plan.Blockers, "it is the last admin account")
		}
	}
	return plan, nil
}

// DeleteUser removes u and everything they own; see the Purger doc comment.
// On an error from one of the steps that must not be skipped, it returns with
// the user still present so the delete can be retried once the cause is fixed.
func (p *Purger) DeleteUser(ctx context.Context, u *store.User) (*PurgeReport, error) {
	plan, err := p.Plan(ctx, u)
	if err != nil {
		return nil, err
	}
	if len(plan.Blockers) > 0 {
		return nil, &BlockedError{User: u.Username, Blockers: plan.Blockers}
	}

	p.mu.Lock()
	if p.busy[u.ID] {
		p.mu.Unlock()
		return nil, fmt.Errorf("a delete of %s is already in progress", u.Username)
	}
	p.busy[u.ID] = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.busy, u.ID); p.mu.Unlock() }()

	rep := &PurgeReport{Plan: plan}
	warn := func(format string, a ...any) {
		m := fmt.Sprintf(format, a...)
		rep.Warnings = append(rep.Warnings, m)
		slog.Warn("purge", "user", u.Username, "warning", m)
	}
	slog.Info("purging user", "user", u.Username, "domains", len(plan.Domains), "databases", len(plan.Databases))

	// Cut off access first so nothing is created while we tear down.
	_ = p.Store.DeleteUserSessions(ctx, u.ID)

	// 1. Containers (their web-port proxy is cleared from the domain).
	if p.Docker != nil {
		if cs, err := p.Store.ListContainers(ctx, u.ID); err == nil {
			for _, c := range cs {
				if err := p.Docker.Delete(ctx, c); err != nil {
					warn("container %s: %v", c.Name, err)
				}
			}
		}
	}

	// 2. Cron jobs (the system crontab itself is removed with the account below).
	if js, err := p.Store.ListCronJobs(ctx, u.ID); err == nil {
		for _, j := range js {
			_ = p.Store.DeleteCronJob(ctx, j.ID)
		}
	}

	// 3. Domains: mail, then DNS zone file, certificates, vhost, PHP pool,
	// Web FTP subdomain and dedicated FTP account.
	doms, err := p.Store.ListDomains(ctx, u.ID)
	if err != nil {
		return rep, err
	}
	for _, d := range doms {
		if strings.HasPrefix(d.Domain, "webftp.") {
			continue
		}
		if !safeHostname(d.Domain) {
			return rep, fmt.Errorf("domain %q has an unsafe name; refusing to delete its files", d.Domain)
		}
		if md, err := p.Store.GetMailDomainByDomainID(ctx, d.ID); err == nil && p.Mail != nil {
			if err := p.Mail.DisableDomain(ctx, md); err != nil {
				return rep, fmt.Errorf("remove mail for %s: %w", d.Domain, err)
			}
			_ = os.RemoveAll(filepath.Join("/var/mail/vhosts", d.Domain))
		}
		// Domains.Delete removes the local zone file and certificates; a zone
		// hosted at an external provider is never touched, so say so.
		if zone, err := p.Store.GetZoneByDomain(ctx, d.ID); err == nil && zone != nil && zone.Provider != "local" {
			warn("DNS zone for %s is hosted at %q and was left in place", d.Domain, zone.Provider)
		}
		if err := p.Domains.Delete(ctx, d.ID); err != nil {
			return rep, fmt.Errorf("delete domain %s: %w", d.Domain, err)
		}
	}
	// Any Web FTP rows whose parent was already gone.
	if rest, err := p.Store.ListDomains(ctx, u.ID); err == nil {
		for _, d := range rest {
			if err := p.Domains.Delete(ctx, d.ID); err != nil {
				return rep, fmt.Errorf("delete domain %s: %w", d.Domain, err)
			}
		}
	}

	// 4. Databases: the real ones on the server, not just the panel rows.
	dbs, err := p.Store.ListDatabases(ctx, u.ID)
	if err != nil {
		return rep, err
	}
	for _, d := range dbs {
		if err := p.DB.Drop(ctx, d); err != nil {
			return rep, fmt.Errorf("drop database %s/%s: %w", d.Server, d.Name, err)
		}
	}

	// 5. Remaining FTP accounts (system users, vsftpd configs).
	if ftps, err := p.Store.ListFTPAccounts(ctx, u.ID); err == nil {
		for _, a := range ftps {
			if err := p.FTP.Delete(ctx, a); err != nil {
				warn("ftp account %s: %v", a.Username, err)
			}
		}
	}

	// 6. The system account and home directory.
	home := plan.Home
	if _, exists := p.lookupUser(ctx, u.Username); exists {
		_, _ = RunTimeout(15*time.Second, "pkill", "-KILL", "-u", u.Username)
		_, _ = RunTimeout(15*time.Second, "crontab", "-r", "-u", u.Username)
		_, uerr := RunTimeout(60*time.Second, "userdel", "-r", "-f", u.Username)
		if _, still := p.lookupUser(ctx, u.Username); still {
			return rep, fmt.Errorf("could not remove system account %s: %v", u.Username, uerr)
		}
	}
	if fi, err := os.Lstat(home); err == nil {
		switch {
		case p.checkHome(home) != nil:
			warn("home %s left in place (failed the safety check)", home)
		case fi.Mode()&os.ModeSymlink != 0:
			_ = os.Remove(home) // never follow a link out of the home root
		default:
			if err := os.RemoveAll(home); err != nil {
				warn("removing %s: %v", home, err)
			}
		}
	}

	// 7. Panel rows (all of them, in one transaction), then stale FTP configs.
	if err := p.Store.DeleteUser(ctx, u.ID); err != nil {
		return rep, fmt.Errorf("remove panel records: %w", err)
	}
	if p.FTP != nil {
		if err := p.FTP.PruneUserConfs(ctx); err != nil {
			warn("pruning ftp configs: %v", err)
		}
	}
	slog.Info("purged user", "user", u.Username, "warnings", len(rep.Warnings))
	return rep, nil
}
