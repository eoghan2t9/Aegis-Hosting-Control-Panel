package store

import (
	"context"
	"time"
)

// --- login attempts (throttling) ---------------------------------------------

func (s *Store) RecordLoginAttempt(ctx context.Context, username, ip string, success bool) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO login_attempts (username, ip, success, created_at) VALUES (?,?,?,?)",
		username, ip, success, now())
	return err
}

// CountRecentFailures counts failed attempts for username+ip since the given
// cutoff — the sliding window a lockout is based on.
func (s *Store) CountRecentFailures(ctx context.Context, username, ip string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM login_attempts WHERE username = ? AND ip = ? AND success = 0 AND created_at > ?",
		username, ip, since.UTC().Format(time.RFC3339)).Scan(&n)
	return n, err
}

// ListRecentLoginAttempts returns the most recent attempts (any user), for
// the admin security screen.
func (s *Store) ListRecentLoginAttempts(ctx context.Context, limit int) ([]LoginAttempt, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, username, ip, success, created_at FROM login_attempts ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LoginAttempt{}
	for rows.Next() {
		var a LoginAttempt
		var success, created interface{}
		if err := rows.Scan(&a.ID, &a.Username, &a.IP, &success, &created); err != nil {
			return nil, err
		}
		a.Success = getBool(success)
		a.CreatedAt = parseTime(created)
		out = append(out, a)
	}
	return out, rows.Err()
}
