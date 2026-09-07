package svc

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Quota enforces disk and bandwidth limits at the application level. Kernel
// disk quotas (setquota, XFS/ext4 project quotas) need filesystem/mount
// support chosen at deploy time that the panel doesn't control, so this
// takes the same approach cPanel's non-strict "quota" mode does: periodic
// usage checks, enforced by suspending the account when over the hard limit.
type Quota struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewQuota(cfg *config.Config, st *store.Store) *Quota {
	return &Quota{Cfg: cfg, Store: st}
}

// DiskUsage returns a user's home directory size in bytes via `du`.
func (q *Quota) DiskUsage(user *store.User) (int64, error) {
	home := user.HomeDir
	if home == "" {
		home = filepath.Join(q.Cfg.HomeRoot, user.Username)
	}
	out, err := RunTimeout(60*time.Second, "du", "-sb", home)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0, nil
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	return n, nil
}

var accessLogBytesRe = regexp.MustCompile(`"\s+\d{3}\s+(\d+)\s+"`)

// BandwidthUsage sums response bytes across a user's domains' access logs.
// This measures usage in whatever's in the current log file (since it was
// last rotated), not an arbitrary date range — nginx's combined log format
// doesn't carry enough for precise range filtering without a much heavier
// parser, which isn't worth it for a usage-estimate feature.
func (q *Quota) BandwidthUsage(ctx context.Context, user *store.User) (int64, error) {
	doms, err := q.Store.ListDomains(ctx, user.ID)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, d := range doms {
		path := "/var/log/nginx/" + d.Domain + ".access.log"
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			m := accessLogBytesRe.FindStringSubmatch(sc.Text())
			if len(m) == 2 {
				if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
					total += n
				}
			}
		}
		f.Close()
	}
	return total, nil
}

// Usage is the disk/bandwidth snapshot returned by the API.
type Usage struct {
	DiskUsedBytes       int64 `json:"disk_used_bytes"`
	DiskQuotaBytes      int64 `json:"disk_quota_bytes"`
	BandwidthUsedBytes  int64 `json:"bandwidth_used_bytes"`
	BandwidthQuotaBytes int64 `json:"bandwidth_quota_bytes"`
}

func (q *Quota) UsageFor(ctx context.Context, user *store.User) (*Usage, error) {
	disk, err := q.DiskUsage(user)
	if err != nil {
		disk = 0
	}
	bw, err := q.BandwidthUsage(ctx, user)
	if err != nil {
		bw = 0
	}
	return &Usage{
		DiskUsedBytes: disk, DiskQuotaBytes: user.QuotaDiskBytes,
		BandwidthUsedBytes: bw, BandwidthQuotaBytes: user.QuotaBandwidthBytes,
	}, nil
}

// Enforce checks every active user's disk usage against their quota,
// suspending accounts that go over and auto-unsuspending only the ones this
// enforcement itself suspended (never an admin's manual suspension) once
// they're back under.
func (q *Quota) Enforce(ctx context.Context) error {
	users, err := q.Store.ListUsers(ctx, 0)
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.Role == store.RoleAdmin {
			continue // admins aren't quota-limited
		}
		byQuota, _ := q.Store.IsSuspendedByQuota(ctx, u.ID)
		if u.Status == store.StatusSuspended && !byQuota {
			continue // manually suspended by an admin — not ours to touch
		}
		if u.QuotaDiskBytes <= 0 {
			if byQuota {
				_ = q.Store.SetUserStatus(ctx, u.ID, store.StatusActive)
				_ = q.Store.SetSuspendedByQuota(ctx, u.ID, false)
			}
			continue // 0 = unlimited
		}
		used, err := q.DiskUsage(u)
		if err != nil {
			continue
		}
		over := used > u.QuotaDiskBytes
		switch {
		case over && u.Status == store.StatusActive:
			_ = q.Store.SetUserStatus(ctx, u.ID, store.StatusSuspended)
			_ = q.Store.SetSuspendedByQuota(ctx, u.ID, true)
		case !over && byQuota:
			_ = q.Store.SetUserStatus(ctx, u.ID, store.StatusActive)
			_ = q.Store.SetSuspendedByQuota(ctx, u.ID, false)
		}
	}
	return nil
}

// EnforceLoop runs Enforce on an interval — same ticker shape as
// SSL.AutoRenew / Backup.AutoBackup.
func (q *Quota) EnforceLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = q.Enforce(ctx)
		}
	}
}
