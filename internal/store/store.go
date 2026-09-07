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
CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
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
	// Seed a default package on first run.
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM packages").Scan(&n); err == nil && n == 0 {
		_, _ = s.db.ExecContext(ctx,
			`INSERT INTO packages (name, description, max_domains, max_databases, max_ftp_accounts,
			 disk_quota_bytes, bandwidth_quota_bytes, allow_ssl, allow_dns, allow_terminal, allow_backups,
			 is_default, created_at) VALUES ('starter', 'Default package: 1 domain, 1 database', 1, 1, 1,
			 0, 0, 1, 1, 1, 1, 1, ?)`, time.Now().UTC().Format(time.RFC3339))
	}
	return nil
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
