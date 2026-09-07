package svc

import (
	"context"
	"regexp"
	"strings"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Security wraps fail2ban for banned-IP visibility/management. Login
// throttling itself lives in auth.Manager (it needs no root privileges);
// this is specifically the "a real ban happened" layer on top.
type Security struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewSecurity(cfg *config.Config, st *store.Store) *Security {
	return &Security{Cfg: cfg, Store: st}
}

const fail2banJail = "aegis"

var banListRe = regexp.MustCompile(`Banned IP list:\s*(.*)`)

// BannedIPs shells out to fail2ban-client to list currently banned IPs for
// the aegis jail. Returns an empty list (not an error) when fail2ban isn't
// running — the panel's own login throttling still works without it.
func (s *Security) BannedIPs(ctx context.Context) ([]string, error) {
	if !LookPath("fail2ban-client") {
		return []string{}, nil
	}
	out, err := Exec(ctx, "fail2ban-client", "status", fail2banJail)
	if err != nil {
		return []string{}, nil
	}
	m := banListRe.FindStringSubmatch(out)
	if len(m) < 2 {
		return []string{}, nil
	}
	fields := strings.Fields(m[1])
	return fields, nil
}

// Unban removes an IP from the aegis jail.
func (s *Security) Unban(ctx context.Context, ip string) error {
	if !LookPath("fail2ban-client") {
		return nil
	}
	_, err := Exec(ctx, "fail2ban-client", "set", fail2banJail, "unbanip", ip)
	return err
}

// RecentAttempts returns the most recent login attempts for the security screen.
func (s *Security) RecentAttempts(ctx context.Context, limit int) ([]store.LoginAttempt, error) {
	return s.Store.ListRecentLoginAttempts(ctx, limit)
}
