package svc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Cron manages scheduled jobs, run under each panel user's own system
// account via the real crontab (crontab -u <username>). The panel is the
// source of truth: every mutation regenerates that user's whole crontab from
// the cron_jobs rows, the same "regenerate config from the DB" idiom used
// for web server vhosts and DNS zone files.
type Cron struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewCron(cfg *config.Config, st *store.Store) *Cron {
	return &Cron{Cfg: cfg, Store: st}
}

var cronFieldRe = regexp.MustCompile(`^[0-9*/,-]+$`)

// ValidSchedule checks a 5-field cron expression (minute hour dom month dow).
func ValidSchedule(s string) bool {
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return false
	}
	for _, f := range fields {
		if !cronFieldRe.MatchString(f) {
			return false
		}
	}
	return true
}

const maxLogLines = 500

// Create validates and provisions a new cron job for user, then regenerates
// their crontab.
func (c *Cron) Create(ctx context.Context, user *store.User, schedule, command string) (*store.CronJob, error) {
	job, err := c.validate(user, schedule, command)
	if err != nil {
		return nil, err
	}
	job.UserID = user.ID
	job.Enabled = true
	if err := c.Store.CreateCronJob(ctx, job); err != nil {
		return nil, err
	}
	if err := c.writeCrontab(ctx, user); err != nil {
		return nil, err
	}
	return job, nil
}

// Update changes an existing job's schedule/command and regenerates the crontab.
func (c *Cron) Update(ctx context.Context, user *store.User, job *store.CronJob, schedule, command string) error {
	updated, err := c.validate(user, schedule, command)
	if err != nil {
		return err
	}
	job.Schedule = updated.Schedule
	job.Command = updated.Command
	job.LogPath = updated.LogPath
	if err := c.Store.UpdateCronJob(ctx, job); err != nil {
		return err
	}
	return c.writeCrontab(ctx, user)
}

// Toggle enables/disables a job without deleting it.
func (c *Cron) Toggle(ctx context.Context, user *store.User, job *store.CronJob, enabled bool) error {
	job.Enabled = enabled
	if err := c.Store.UpdateCronJob(ctx, job); err != nil {
		return err
	}
	return c.writeCrontab(ctx, user)
}

// Delete removes a job and regenerates the crontab.
func (c *Cron) Delete(ctx context.Context, user *store.User, job *store.CronJob) error {
	if err := c.Store.DeleteCronJob(ctx, job.ID); err != nil {
		return err
	}
	return c.writeCrontab(ctx, user)
}

func (c *Cron) validate(user *store.User, schedule, command string) (*store.CronJob, error) {
	schedule = strings.TrimSpace(schedule)
	command = strings.TrimSpace(command)
	if !ValidSchedule(schedule) {
		return nil, errors.New("invalid schedule: expected 5 cron fields (minute hour day month weekday)")
	}
	if command == "" {
		return nil, errors.New("command is required")
	}
	// If the command's first token looks like a path, resolve it inside the
	// user's home to reject traversal; leave bare binary names (curl, wp, php)
	// alone since those already run under the user's own account/PATH.
	files := &Files{Cfg: c.Cfg}
	fields := strings.Fields(command)
	if len(fields) > 0 && (strings.HasPrefix(fields[0], "/") || strings.HasPrefix(fields[0], "./")) {
		home := user.HomeDir
		if home == "" {
			home = filepath.Join(c.Cfg.HomeRoot, user.Username)
		}
		rel := strings.TrimPrefix(fields[0], home)
		resolved, err := files.Resolve(user, rel)
		if err != nil {
			return nil, fmt.Errorf("command path: %w", err)
		}
		fields[0] = resolved
		command = strings.Join(fields, " ")
	}
	home := user.HomeDir
	if home == "" {
		home = filepath.Join(c.Cfg.HomeRoot, user.Username)
	}
	logPath := filepath.Join(home, "logs", "cron")
	return &store.CronJob{Schedule: schedule, Command: command, LogPath: logPath}, nil
}

// writeCrontab regenerates and installs the full crontab for user from the
// store's current cron_jobs rows (enabled jobs only).
func (c *Cron) writeCrontab(ctx context.Context, user *store.User) error {
	jobs, err := c.Store.ListCronJobs(ctx, user.ID)
	if err != nil {
		return err
	}
	home := user.HomeDir
	if home == "" {
		home = filepath.Join(c.Cfg.HomeRoot, user.Username)
	}
	logDir := filepath.Join(home, "logs")
	if err := os.MkdirAll(logDir, 0o755); err == nil {
		// The panel runs as root; the job itself runs as the user via cron,
		// so the log directory must be writable by them or output is lost.
		_, _ = RunTimeout(10*time.Second, "chown", "-R", user.Username+":www-data", logDir)
	}

	var sb strings.Builder
	sb.WriteString("# Managed by Aegis. Do not edit by hand; changes are overwritten.\n")
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		logPath := j.LogPath
		if logPath == "" {
			logPath = filepath.Join(logDir, "cron")
		}
		fmt.Fprintf(&sb, "%s %s >> %s 2>&1\n", j.Schedule, j.Command, logPath)
	}

	if !LookPath("crontab") {
		return errors.New("crontab is not installed")
	}
	tmp, err := os.CreateTemp("", "aegis-cron-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(sb.String()); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	if _, err := RunTimeout(15*time.Second, "crontab", "-u", user.Username, tmp.Name()); err != nil {
		return fmt.Errorf("install crontab: %w", err)
	}
	return nil
}

// TailLog returns the last lines of a job's log file (empty if it hasn't run yet).
// LogResult distinguishes "never run" (log file doesn't exist — cron's `>>`
// redirection creates the file on the job's first execution, even if it
// produces no output) from "ran but produced nothing", which the log text
// alone can't tell apart.
type LogResult struct {
	Log       string
	HasRun    bool
	UpdatedAt time.Time
}

func (c *Cron) TailLog(job *store.CronJob) (LogResult, error) {
	if job.LogPath == "" {
		return LogResult{}, nil
	}
	info, err := os.Stat(job.LogPath)
	if err != nil {
		if os.IsNotExist(err) {
			return LogResult{}, nil
		}
		return LogResult{}, err
	}
	f, err := os.Open(job.LogPath)
	if err != nil {
		return LogResult{}, err
	}
	defer f.Close()
	lines := make([]string, 0, maxLogLines)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > maxLogLines {
			lines = lines[1:]
		}
	}
	return LogResult{Log: strings.Join(lines, "\n"), HasRun: true, UpdatedAt: info.ModTime()}, sc.Err()
}
