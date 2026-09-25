// Package store is the SQLite-backed data layer for the Aegis panel.
//
// The store owns all persistence: users, packages, domains, DNS, SSL orders,
// FTP accounts, databases, providers, sessions and the audit log. Services
// and API handlers depend on Store, never on the database directly.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned on unique-constraint violations.
var ErrConflict = errors.New("conflict")

// Store wraps the SQLite database handle.
type Store struct {
	db *sql.DB
}

// New opens (creating if needed) the SQLite database at path and applies the
// schema migrations. The database file is created with mode 0600.
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite: single writer, avoids lock contention
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

// migrate applies schema creation. Uses CREATE TABLE IF NOT EXISTS so existing
// databases are left intact; add new columns via ALTER guards below.
func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS packages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	description TEXT NOT NULL DEFAULT '',
	max_domains INTEGER NOT NULL DEFAULT 1,
	max_databases INTEGER NOT NULL DEFAULT 1,
	max_ftp_accounts INTEGER NOT NULL DEFAULT 1,
	disk_quota_bytes INTEGER NOT NULL DEFAULT 0,
	bandwidth_quota_bytes INTEGER NOT NULL DEFAULT 0,
	allow_ssl INTEGER NOT NULL DEFAULT 1,
	allow_dns INTEGER NOT NULL DEFAULT 1,
	allow_terminal INTEGER NOT NULL DEFAULT 1,
	allow_backups INTEGER NOT NULL DEFAULT 1,
	allow_mail INTEGER NOT NULL DEFAULT 1,
	allow_webmail INTEGER NOT NULL DEFAULT 1,
	allow_databases INTEGER NOT NULL DEFAULT 1,
	allow_files INTEGER NOT NULL DEFAULT 1,
	allow_ftp INTEGER NOT NULL DEFAULT 1,
	allow_cron INTEGER NOT NULL DEFAULT 1,
	is_default INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL UNIQUE,
	email TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT 'user',
	package_id INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'active',
	owner_id INTEGER NOT NULL DEFAULT 0,
	home_dir TEXT NOT NULL DEFAULT '',
	quota_disk_bytes INTEGER NOT NULL DEFAULT 0,
	quota_bandwidth_bytes INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS domains (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	domain TEXT NOT NULL UNIQUE,
	document_root TEXT NOT NULL DEFAULT '',
	php_version TEXT NOT NULL DEFAULT '',
	webserver TEXT NOT NULL DEFAULT 'nginx',
	ssl_enabled INTEGER NOT NULL DEFAULT 0,
	ssl_cert_path TEXT NOT NULL DEFAULT '',
	ssl_key_path TEXT NOT NULL DEFAULT '',
	ssl_provider TEXT NOT NULL DEFAULT '',
	ssl_auto_renew INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS domain_aliases (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	domain_id INTEGER NOT NULL,
	alias TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS dns_zones (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	domain_id INTEGER NOT NULL,
	provider TEXT NOT NULL DEFAULT 'local',
	provider_zone_id TEXT NOT NULL DEFAULT '',
	synced_at TEXT,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS dns_records (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	zone_id INTEGER NOT NULL,
	name TEXT NOT NULL DEFAULT '@',
	type TEXT NOT NULL,
	ttl INTEGER NOT NULL DEFAULT 3600,
	priority INTEGER NOT NULL DEFAULT 0,
	content TEXT NOT NULL,
	proxied INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ssl_orders (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	domain_id INTEGER NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending',
	provider TEXT NOT NULL DEFAULT 'letsencrypt',
	challenge TEXT NOT NULL DEFAULT 'http',
	cert_path TEXT NOT NULL DEFAULT '',
	key_path TEXT NOT NULL DEFAULT '',
	expires_at TEXT,
	error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ftp_accounts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	home_dir TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS databases (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	server TEXT NOT NULL,
	name TEXT NOT NULL,
	db_user TEXT NOT NULL DEFAULT '',
	db_password TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	UNIQUE(server, name)
);
CREATE TABLE IF NOT EXISTS dns_providers (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	label TEXT NOT NULL DEFAULT '',
	api_key_enc TEXT NOT NULL DEFAULT '',
	email TEXT NOT NULL DEFAULT '',
	config TEXT NOT NULL DEFAULT '{}',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS audit_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	actor_id INTEGER NOT NULL DEFAULT 0,
	actor_name TEXT NOT NULL DEFAULT '',
	action TEXT NOT NULL,
	target TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	ip TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL
);
-- Short-lived, single-use: issued once a TOTP-enabled account's password
-- has checked out, redeemed by /auth/totp/verify for a real session. Never
-- itself usable as a bearer token (see withAuth in internal/api) — carrying
-- this instead of a session id is exactly what stops a stolen password
-- alone from granting access to a 2FA-protected account.
CREATE TABLE IF NOT EXISTS totp_challenges (
	id TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS backup_targets (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	kind TEXT NOT NULL,
	label TEXT NOT NULL DEFAULT '',
	config_enc TEXT NOT NULL DEFAULT '',
	retention_days INTEGER NOT NULL DEFAULT 30,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS login_attempts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL,
	ip TEXT NOT NULL,
	success INTEGER NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS api_tokens (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	label TEXT NOT NULL DEFAULT '',
	token_hash TEXT NOT NULL,
	scopes TEXT NOT NULL DEFAULT '*',
	last_used_at TEXT,
	expires_at TEXT,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS mail_domains (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	domain_id INTEGER NOT NULL,
	domain TEXT NOT NULL UNIQUE,
	dkim_selector TEXT NOT NULL DEFAULT 'default',
	dkim_public_key TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS mailboxes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	mail_domain_id INTEGER NOT NULL,
	localpart TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	quota_bytes INTEGER NOT NULL DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	UNIQUE(mail_domain_id, localpart)
);
CREATE TABLE IF NOT EXISTS mail_aliases (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	mail_domain_id INTEGER NOT NULL,
	source TEXT NOT NULL,
	destination TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE(mail_domain_id, source)
);
CREATE TABLE IF NOT EXISTS cron_jobs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	schedule TEXT NOT NULL,
	command TEXT NOT NULL,
	log_path TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS system_package_updates (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	current_version TEXT NOT NULL,
	new_version TEXT NOT NULL,
	is_security INTEGER NOT NULL DEFAULT 0,
	checked_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS containers (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	domain_id INTEGER NOT NULL DEFAULT 0,
	name TEXT NOT NULL,
	image TEXT NOT NULL,
	ports TEXT NOT NULL DEFAULT '[]',
	web_port INTEGER NOT NULL DEFAULT 0,
	env TEXT NOT NULL DEFAULT '{}',
	volumes TEXT NOT NULL DEFAULT '[]',
	restart_policy TEXT NOT NULL DEFAULT 'unless-stopped',
	memory_limit_mb INTEGER NOT NULL DEFAULT 0,
	cpu_limit TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	UNIQUE(user_id, name)
);
CREATE TABLE IF NOT EXISTS ips (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	address TEXT NOT NULL UNIQUE,
	label TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL DEFAULT 'shared',
	created_at TEXT NOT NULL
);
`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, stmt := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("pragma %s: %w", stmt, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	// ALTER guards: CREATE TABLE IF NOT EXISTS doesn't retrofit columns onto
	// a table that already existed from an older schema version.
	if err := s.addColumnIfMissing(ctx, "users", "suspended_by_quota", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate users.suspended_by_quota: %w", err)
	}
	// TOTP two-factor auth: opt-in per account, so every existing user comes
	// back with totp_enabled=0 (unchanged login behaviour) until they enroll.
	for _, c := range []struct{ col, def string }{
		{"totp_secret", "TEXT NOT NULL DEFAULT ''"},
		{"totp_enabled", "INTEGER NOT NULL DEFAULT 0"},
		{"totp_backup_codes", "TEXT NOT NULL DEFAULT '[]'"},
	} {
		if err := s.addColumnIfMissing(ctx, "users", c.col, c.def); err != nil {
			return fmt.Errorf("migrate users.%s: %w", c.col, err)
		}
	}
	// Package feature flags added after the first release: retrofit them so
	// existing databases keep every panel area enabled (matching the old
	// behaviour, where the flags didn't exist and nothing was gated).
	for _, c := range []struct{ col, def string }{
		{"allow_mail", "INTEGER NOT NULL DEFAULT 1"},
		{"allow_webmail", "INTEGER NOT NULL DEFAULT 1"},
		{"allow_databases", "INTEGER NOT NULL DEFAULT 1"},
		{"allow_files", "INTEGER NOT NULL DEFAULT 1"},
		{"allow_ftp", "INTEGER NOT NULL DEFAULT 1"},
		{"allow_cron", "INTEGER NOT NULL DEFAULT 1"},
		// Docker containers run under the root-owned dockerd and (unlike the
		// other flags above) can mount volumes and consume host resources
		// well beyond a chrooted account's usual reach, so — unlike every
		// flag before it — this ships OFF by default; an admin opts a
		// package in explicitly.
		{"allow_docker", "INTEGER NOT NULL DEFAULT 0"},
		{"max_containers", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := s.addColumnIfMissing(ctx, "packages", c.col, c.def); err != nil {
			return fmt.Errorf("migrate packages.%s: %w", c.col, err)
		}
	}
	if err := s.addColumnIfMissing(ctx, "domains", "proxy_target", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate domains.proxy_target: %w", err)
	}
	if err := s.addColumnIfMissing(ctx, "domains", "php_settings", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("migrate domains.php_settings: %w", err)
	}
	// ip_id references ips(id); 0 means "unassigned" (vhost keeps listening
	// on the wildcard address, the pre-existing behaviour).
	if err := s.addColumnIfMissing(ctx, "domains", "ip_id", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate domains.ip_id: %w", err)
	}
	// parent_domain_id references domains(id); 0 means "top-level domain".
	// Set only at creation (see svc.Domains.Create) to link a sub-domain to
	// its master for display/grouping — see store.Domain.ParentDomainID.
	if err := s.addColumnIfMissing(ctx, "domains", "parent_domain_id", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate domains.parent_domain_id: %w", err)
	}
	// Seed a default package on first run.
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM packages").Scan(&n); err == nil && n == 0 {
		_, _ = s.db.ExecContext(ctx,
			`INSERT INTO packages (name, description, max_domains, max_databases, max_ftp_accounts,
			 disk_quota_bytes, bandwidth_quota_bytes, allow_ssl, allow_dns, allow_terminal, allow_backups,
			 is_default, created_at) VALUES ('starter', 'Default package: 1 domain, 1 database', 1, 1, 1,
			 0, 0, 1, 1, 1, 1, 1, ?)`, time.Now().UTC().Format(time.RFC3339))
	}
	// ssl_orders used to be an append-only log (a new row per issue attempt,
	// forever) instead of current state — one domain could accumulate a pile
	// of issued/failed/pending rows, including a "pending" one orphaned
	// forever if the process restarted mid-issuance (nothing else would ever
	// come along to supersede it). Collapse any pre-existing duplicates down
	// to one per domain — preferring the most recent "issued" row, since
	// that's what the live certificate on disk actually is, else the most
	// recent overall — before enforcing it going forward with a unique
	// index. A fresh or already-migrated database has nothing to dedupe.
	if err := s.dedupeSSLOrders(ctx); err != nil {
		return fmt.Errorf("dedupe ssl_orders: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "CREATE UNIQUE INDEX IF NOT EXISTS idx_ssl_orders_domain ON ssl_orders(domain_id)"); err != nil {
		return fmt.Errorf("create ssl_orders unique index: %w", err)
	}
	return nil
}

func (s *Store) dedupeSSLOrders(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT domain_id FROM ssl_orders")
	if err != nil {
		return err
	}
	var domainIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		domainIDs = append(domainIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	for _, domID := range domainIDs {
		var keepID int64
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM ssl_orders WHERE domain_id = ? ORDER BY (status = 'issued') DESC, id DESC LIMIT 1`,
			domID).Scan(&keepID)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, "DELETE FROM ssl_orders WHERE domain_id = ? AND id != ?", domID, keepID); err != nil {
			return err
		}
	}
	return nil
}

// addColumnIfMissing retrofits a column onto an existing table. table and
// column are always package-internal constants, never user input.
func (s *Store) addColumnIfMissing(ctx context.Context, table, column, def string) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def))
	return err
}

// now returns the canonical timestamp string used across the store.
func now() string { return time.Now().UTC().Format(time.RFC3339) }

// parseTime converts stored timestamp strings (and NULL) to time.Time.
func parseTime(v interface{}) time.Time {
	if v == nil {
		return time.Time{}
	}
	s, _ := v.(string)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// nullableTime formats t as the same RFC3339 string convention now()/every
// other timestamp column uses, or SQL NULL when t is nil. Use this — never
// pass a *time.Time/time.Time directly as a query arg — the sqlite driver
// falls back to Go's default time.Time.String() format for unrecognized
// types ("2006-01-02 15:04:05 -0700 MST"), which parseTime/time.Parse(
// time.RFC3339, ...) then silently fails to parse back, leaving the field
// nil forever. This bit ssl_orders.expires_at and api_tokens.expires_at.
func nullableTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

// parseTimePtr converts a stored nullable timestamp to *time.Time, tolerant
// of both the canonical RFC3339 format (nullableTime) and Go's default
// time.Time.String() layout that a past bug (see nullableTime) wrote for
// some existing rows, so already-affected data displays correctly too
// without a manual migration.
func parseTimePtr(v interface{}) *time.Time {
	s, _ := v.(string)
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}
	if t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", s); err == nil {
		return &t
	}
	return nil
}

// getString / getBool / getInt are scan helpers for nullable columns.
func getString(v interface{}) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func getBool(v interface{}) bool {
	if v == nil {
		return false
	}
	i, _ := v.(int64)
	return i != 0
}

func getInt(v interface{}) int {
	if v == nil {
		return 0
	}
	i, _ := v.(int64)
	return int(i)
}

func getInt64(v interface{}) int64 {
	if v == nil {
		return 0
	}
	i, _ := v.(int64)
	return i
}

// wrapErr maps SQLite errors to store errors.
func wrapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if isConstraint(err) {
		return ErrConflict
	}
	return err
}

func isConstraint(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint") || strings.Contains(msg, "FOREIGN KEY constraint")
}

// --- settings ---------------------------------------------------------------

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value)
	return err
}

// --- audit log ---------------------------------------------------------------

func (s *Store) AppendAudit(ctx context.Context, actorID int64, actorName, action, target, detail, ip string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO audit_log (actor_id, actor_name, action, target, detail, ip, created_at) VALUES (?,?,?,?,?,?,?)",
		actorID, actorName, action, target, detail, ip, now())
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit int, actorID int64) ([]AuditEntry, error) {
	q := "SELECT id, actor_id, actor_name, action, target, detail, ip, created_at FROM audit_log"
	args := []interface{}{}
	if actorID > 0 {
		q += " WHERE actor_id = ?"
		args = append(args, actorID)
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var created interface{}
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorName, &e.Action, &e.Target, &e.Detail, &e.IP, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- sessions ----------------------------------------------------------------

func (s *Store) CreateSession(ctx context.Context, id string, userID int64, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO sessions (id, user_id, expires_at, created_at) VALUES (?,?,?,?)",
		id, userID, expiresAt.UTC().Format(time.RFC3339), now())
	return err
}

func (s *Store) GetSessionUser(ctx context.Context, id string) (int64, error) {
	var userID int64
	var exp string
	err := s.db.QueryRowContext(ctx, "SELECT user_id, expires_at FROM sessions WHERE id = ?", id).Scan(&userID, &exp)
	if err != nil {
		return 0, wrapErr(err)
	}
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil || t.Before(time.Now()) {
		return 0, ErrNotFound
	}
	return userID, nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id)
	return err
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}

// --- totp challenges -----------------------------------------------------

func (s *Store) CreateTOTPChallenge(ctx context.Context, id string, userID int64, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO totp_challenges (id, user_id, expires_at, created_at) VALUES (?,?,?,?)",
		id, userID, expiresAt.UTC().Format(time.RFC3339), now())
	return err
}

// GetTOTPChallengeUser mirrors GetSessionUser's expiry handling — an
// expired challenge is treated as not found, not as a still-live one.
func (s *Store) GetTOTPChallengeUser(ctx context.Context, id string) (int64, error) {
	var userID int64
	var exp string
	err := s.db.QueryRowContext(ctx, "SELECT user_id, expires_at FROM totp_challenges WHERE id = ?", id).Scan(&userID, &exp)
	if err != nil {
		return 0, wrapErr(err)
	}
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil || t.Before(time.Now()) {
		return 0, ErrNotFound
	}
	return userID, nil
}

// DeleteTOTPChallenge is called both on successful verification (so the
// challenge can't be replayed) and should be called on a failed code too —
// callers that want "N attempts against the same challenge" instead of
// single-shot need to decide that explicitly rather than get it by default.
func (s *Store) DeleteTOTPChallenge(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM totp_challenges WHERE id = ?", id)
	return err
}
