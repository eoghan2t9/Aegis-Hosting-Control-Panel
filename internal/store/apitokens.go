package store

import (
	"context"
	"time"
)

// --- API tokens -------------------------------------------------------------

const apiTokenCols = `id, user_id, label, token_hash, scopes, last_used_at, expires_at, created_at`

func scanAPIToken(row interface{ Scan(...any) error }) (*APIToken, error) {
	var t APIToken
	var lastUsed, expires, created interface{}
	if err := row.Scan(&t.ID, &t.UserID, &t.Label, &t.TokenHash, &t.Scopes, &lastUsed, &expires, &created); err != nil {
		return nil, wrapErr(err)
	}
	if lastUsed != nil {
		if s := getString(lastUsed); s != "" {
			if tm, err := time.Parse(time.RFC3339, s); err == nil {
				t.LastUsedAt = &tm
			}
		}
	}
	if expires != nil {
		if s := getString(expires); s != "" {
			if tm, err := time.Parse(time.RFC3339, s); err == nil {
				t.ExpiresAt = &tm
			}
		}
	}
	t.CreatedAt = parseTime(created)
	return &t, nil
}

func (s *Store) CreateAPIToken(ctx context.Context, t *APIToken) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, "INSERT INTO api_tokens (user_id, label, token_hash, scopes, expires_at, created_at) VALUES (?,?,?,?,?,?)",
		t.UserID, t.Label, t.TokenHash, t.Scopes, t.ExpiresAt, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	t.ID = id
	t.CreatedAt = parseTime(ts)
	return nil
}

// ListAPITokens returns tokens; when userID > 0, only that user's tokens.
func (s *Store) ListAPITokens(ctx context.Context, userID int64) ([]*APIToken, error) {
	q := "SELECT " + apiTokenCols + " FROM api_tokens"
	args := []interface{}{}
	if userID > 0 {
		q += " WHERE user_id = ?"
		args = append(args, userID)
	}
	q += " ORDER BY id DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIToken{}
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetAPIToken(ctx context.Context, id int64) (*APIToken, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+apiTokenCols+" FROM api_tokens WHERE id = ?", id)
	return scanAPIToken(row)
}

// AllActiveAPITokens is used by Verify to hash-compare a presented raw token
// against every stored hash (bcrypt hashes can't be looked up by index).
func (s *Store) AllActiveAPITokens(ctx context.Context) ([]*APIToken, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+apiTokenCols+" FROM api_tokens WHERE expires_at IS NULL OR expires_at > ?", now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIToken{}
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) TouchAPIToken(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE api_tokens SET last_used_at = ? WHERE id = ?", now(), id)
	return err
}

func (s *Store) DeleteAPIToken(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM api_tokens WHERE id = ?", id)
	return err
}
