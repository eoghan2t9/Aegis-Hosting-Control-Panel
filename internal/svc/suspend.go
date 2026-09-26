package svc

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Suspender suspends and unsuspends accounts. A suspended account is cut off
// everywhere at once, not just from the panel:
//
//   - websites: every domain (and its webftp.<domain>) shows an "Account
//     Suspended" page (see WebServer.Apply / SuspendedServer);
//   - FTP: the account's system login and every FTP account's system login are
//     locked and their running sessions killed; Web FTP refuses logins and
//     drops existing sessions;
//   - mail: the mailboxes are disabled (which also stops webmail logins);
//   - cron jobs stop running, containers are stopped and stop restarting, and
//     database logins are locked (data untouched);
//   - panel sessions are revoked (panel/API access already checks the status).
//
// Unsuspending restores exactly what suspending changed: it never re-enables an
// FTP account, mailbox or container an admin had switched off themselves.
//
// Only manual suspensions do this. Quota enforcement also sets the suspended
// status, but an over-quota user must keep FTP access to free space (and quota
// enforcement lifts its own suspension automatically), so that path keeps the
// old panel-only behaviour — see store.IsManuallySuspended.
type Suspender struct {
	Cfg     *config.Config
	Store   *store.Store
	Domains *Domains
	Cron    *Cron
	DB      *Databases
	FTP     *FTP
	WebFTP  *WebFTP

	// run executes a system command; a field so tests can record instead.
	run func(timeout time.Duration, name string, args ...string) (string, error)

	mu      sync.Mutex     // one suspend/unsuspend at a time
	applied map[int64]bool // users this process has enforced (true) or lifted (false)
}

func NewSuspender(cfg *config.Config, st *store.Store, domains *Domains, cron *Cron, db *Databases, ftp *FTP, webftp *WebFTP) *Suspender {
	return &Suspender{Cfg: cfg, Store: st, Domains: domains, Cron: cron, DB: db, FTP: ftp, WebFTP: webftp, run: RunTimeout, applied: map[int64]bool{}}
}

// SuspendReport lists steps that did not complete. They are best-effort: a
// failing step (Docker not installed, MariaDB down) never leaves the account
// half-suspended in the panel, because the status itself is set first.
type SuspendReport struct {
	Warnings []string `json:"warnings"`
}

// Suspend marks the account manually suspended and enforces it everywhere.
func (s *Suspender) Suspend(ctx context.Context, u *store.User) (*SuspendReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Store.SetUserStatus(ctx, u.ID, store.StatusSuspended); err != nil {
		return nil, err
	}
	// An admin suspending an account takes over from any quota suspension.
	_ = s.Store.SetSuspendedByQuota(ctx, u.ID, false)
	u.Status = store.StatusSuspended
	s.applied[u.ID] = true
	return s.enforce(ctx, u), nil
}

// Unsuspend marks the account active and restores everything Suspend changed.
func (s *Suspender) Unsuspend(ctx context.Context, u *store.User) (*SuspendReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Store.SetUserStatus(ctx, u.ID, store.StatusActive); err != nil {
		return nil, err
	}
	_ = s.Store.SetSuspendedByQuota(ctx, u.ID, false)
	u.Status = store.StatusActive
	s.applied[u.ID] = false
	return s.lift(ctx, u), nil
}

// Reconcile brings enforcement in line with the stored account status: a
// suspended account this process has not yet enforced is enforced, and one it
// enforced that is no longer suspended is lifted. It is idempotent. It runs at
// startup (so a restart can never leave a suspended account partly active) and
// on a timer, which is how a suspend made through aegisctl (a different
// process, unable to touch this one's in-memory web routes) or by editing the
// database takes effect.
func (s *Suspender) Reconcile(ctx context.Context) {
	users, err := s.Store.ListUsers(ctx, 0)
	if err != nil {
		slog.Warn("suspension reconcile: list users failed", "err", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range users {
		suspended, _ := s.Store.IsManuallySuspended(ctx, u.ID)
		switch {
		case suspended && !s.applied[u.ID]:
			s.applied[u.ID] = true
			if rep := s.enforce(ctx, u); len(rep.Warnings) > 0 {
				slog.Warn("suspension reconcile", "user", u.Username, "warnings", rep.Warnings)
			}
		case !suspended && s.applied[u.ID]:
			s.applied[u.ID] = false
			s.lift(ctx, u)
		}
	}
}

// ReconcileLoop runs Reconcile now and then every interval until ctx ends.
func (s *Suspender) ReconcileLoop(ctx context.Context, interval time.Duration) {
	s.Reconcile(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Reconcile(ctx)
		}
	}
}

func (s *Suspender) accountNames(ctx context.Context, u *store.User) []string {
	names := []string{u.Username}
	if ftps, err := s.Store.ListFTPAccounts(ctx, u.ID); err == nil {
		for _, a := range ftps {
			if a.Username != u.Username {
				names = append(names, a.Username)
			}
		}
	}
	return names
}

func (s *Suspender) enforce(ctx context.Context, u *store.User) *SuspendReport {
	rep := &SuspendReport{}
	warn := func(format string, a ...any) {
		m := fmt.Sprintf(format, a...)
		rep.Warnings = append(rep.Warnings, m)
		slog.Warn("suspend", "user", u.Username, "warning", m)
	}
	slog.Info("suspending user", "user", u.Username)

	// Panel and Web FTP sessions.
	_ = s.Store.DeleteUserSessions(ctx, u.ID)
	if s.WebFTP != nil {
		s.WebFTP.RevokeUser(u.ID)
	}

	// Lock every system login first (so nothing can reconnect), then kill what
	// is still running as them: FTP sessions run as the owning account, and so
	// do their PHP workers and shell processes.
	names := s.accountNames(ctx, u)
	for _, n := range names {
		_, _ = s.run(10*time.Second, "usermod", "-L", n)
	}

	// Websites: re-render every domain, which now points at the suspended page.
	if s.Domains != nil {
		doms, err := s.Store.ListDomains(ctx, u.ID)
		if err != nil {
			warn("list domains: %v", err)
		}
		for _, d := range doms {
			if err := s.Domains.Apply(ctx, d.ID); err != nil {
				warn("domain %s: %v", d.Domain, err)
			}
		}
	}

	// Cron jobs.
	if s.Cron != nil {
		_ = s.Cron.Pause(ctx, u)
	}

	// Database logins.
	if s.DB != nil {
		if dbs, err := s.Store.ListDatabases(ctx, u.ID); err == nil {
			for _, d := range dbs {
				if err := s.DB.SetLocked(ctx, d, true); err != nil {
					warn("lock database %s/%s: %v", d.Server, d.Name, err)
				}
			}
		}
	}

	// Containers: stop the running ones (remembering which) and stop them
	// restarting on their own after a daemon or host restart.
	if cs, err := s.Store.ListContainers(ctx, u.ID); err == nil {
		for _, c := range cs {
			name := containerName(c.ID)
			out, err := s.run(15*time.Second, "docker", "inspect", "-f", "{{.State.Running}}", name)
			if err != nil {
				continue // not created, or docker isn't installed
			}
			running := strings.TrimSpace(out) == "true"
			if running {
				_ = s.Store.RecordSuspensionAction(ctx, u.ID, store.SuspendKindContainer, c.ID)
			}
			_, _ = s.run(15*time.Second, "docker", "update", "--restart=no", name)
			if running {
				if _, err := s.run(45*time.Second, "docker", "stop", name); err != nil {
					warn("stop container %s: %v", c.Name, err)
				}
			}
		}
	}

	// Mailboxes: disable the enabled ones (remembering which).
	s.forEachMailbox(ctx, u, func(mb *store.Mailbox) {
		if !mb.Enabled {
			return
		}
		if err := s.Store.RecordSuspensionAction(ctx, u.ID, store.SuspendKindMailbox, mb.ID); err != nil {
			warn("record mailbox %s: %v", mb.Localpart, err)
			return
		}
		if err := s.Store.SetMailboxEnabled(ctx, mb.ID, false); err != nil {
			warn("disable mailbox %s: %v", mb.Localpart, err)
		}
	})

	// Finally cut off anything still running under these accounts.
	for _, n := range names {
		_, _ = s.run(15*time.Second, "pkill", "-KILL", "-u", n)
	}
	return rep
}

func (s *Suspender) lift(ctx context.Context, u *store.User) *SuspendReport {
	rep := &SuspendReport{}
	warn := func(format string, a ...any) {
		m := fmt.Sprintf(format, a...)
		rep.Warnings = append(rep.Warnings, m)
		slog.Warn("unsuspend", "user", u.Username, "warning", m)
	}
	slog.Info("unsuspending user", "user", u.Username)

	// System logins: unlock the owner and only the FTP accounts that are
	// enabled (a disabled FTP account stays locked, as ToggleEnabled left it).
	ownerEnabled := true
	if ftps, err := s.Store.ListFTPAccounts(ctx, u.ID); err == nil {
		for _, a := range ftps {
			if a.Username == u.Username {
				ownerEnabled = a.Enabled
				continue
			}
			if a.Enabled {
				_, _ = s.run(10*time.Second, "usermod", "-U", a.Username)
			}
		}
	}
	if ownerEnabled {
		_, _ = s.run(10*time.Second, "usermod", "-U", u.Username)
	}

	if s.DB != nil {
		if dbs, err := s.Store.ListDatabases(ctx, u.ID); err == nil {
			for _, d := range dbs {
				if err := s.DB.SetLocked(ctx, d, false); err != nil {
					warn("unlock database %s/%s: %v", d.Server, d.Name, err)
				}
			}
		}
	}

	if s.Cron != nil {
		if err := s.Cron.Resume(ctx, u); err != nil {
			warn("restore crontab: %v", err)
		}
	}

	if s.Domains != nil {
		doms, err := s.Store.ListDomains(ctx, u.ID)
		if err != nil {
			warn("list domains: %v", err)
		}
		for _, d := range doms {
			if err := s.Domains.Apply(ctx, d.ID); err != nil {
				warn("domain %s: %v", d.Domain, err)
			}
		}
	}

	// Containers: put every restart policy back, start only those we stopped.
	stopped := map[int64]bool{}
	if ids, err := s.Store.ListSuspensionActions(ctx, u.ID, store.SuspendKindContainer); err == nil {
		for _, id := range ids {
			stopped[id] = true
		}
	}
	if cs, err := s.Store.ListContainers(ctx, u.ID); err == nil {
		for _, c := range cs {
			name := containerName(c.ID)
			policy := c.RestartPolicy
			if policy == "" {
				policy = "no"
			}
			_, _ = s.run(15*time.Second, "docker", "update", "--restart="+policy, name)
			if stopped[c.ID] {
				if _, err := s.run(45*time.Second, "docker", "start", name); err != nil {
					warn("start container %s: %v", c.Name, err)
				}
			}
		}
	}

	// Mailboxes we disabled.
	if ids, err := s.Store.ListSuspensionActions(ctx, u.ID, store.SuspendKindMailbox); err == nil {
		for _, id := range ids {
			if err := s.Store.SetMailboxEnabled(ctx, id, true); err != nil {
				warn("re-enable mailbox %d: %v", id, err)
			}
		}
	}

	_ = s.Store.ClearSuspensionActions(ctx, u.ID)
	return rep
}

// forEachMailbox visits every mailbox of every mail-enabled domain the user owns.
func (s *Suspender) forEachMailbox(ctx context.Context, u *store.User, fn func(*store.Mailbox)) {
	doms, err := s.Store.ListDomains(ctx, u.ID)
	if err != nil {
		return
	}
	for _, d := range doms {
		md, err := s.Store.GetMailDomainByDomainID(ctx, d.ID)
		if err != nil {
			continue
		}
		boxes, err := s.Store.ListMailboxes(ctx, md.ID)
		if err != nil {
			continue
		}
		for _, mb := range boxes {
			fn(mb)
		}
	}
}
