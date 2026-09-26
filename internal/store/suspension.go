package store

import "context"

// Suspension kinds recorded in suspension_actions.
const (
	SuspendKindMailbox   = "mailbox"   // a mailbox suspension disabled
	SuspendKindContainer = "container" // a container suspension stopped
)

// IsManuallySuspended reports whether the user is suspended by an admin (or
// the CLI) rather than by quota enforcement. Only manual suspensions cut off
// FTP, websites, mail and the rest: an over-quota user has to keep FTP access
// so they can free space, and quota enforcement lifts its own suspensions
// automatically once they do.
func (s *Store) IsManuallySuspended(ctx context.Context, userID int64) (bool, error) {
	var status string
	var byQuota interface{}
	err := s.db.QueryRowContext(ctx, "SELECT status, suspended_by_quota FROM users WHERE id = ?", userID).Scan(&status, &byQuota)
	if err != nil {
		return false, wrapErr(err)
	}
	return status == StatusSuspended && !getBool(byQuota), nil
}

// IsUsernameManuallySuspended is IsManuallySuspended by username (an unknown
// user is not suspended).
func (s *Store) IsUsernameManuallySuspended(ctx context.Context, username string) bool {
	var id int64
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM users WHERE username = ?", username).Scan(&id); err != nil {
		return false
	}
	ok, _ := s.IsManuallySuspended(ctx, id)
	return ok
}

// RecordSuspensionAction remembers that suspending userID changed ref (of the
// given kind), so lifting the suspension can put it back. Idempotent.
func (s *Store) RecordSuspensionAction(ctx context.Context, userID int64, kind string, ref int64) error {
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO suspension_actions (user_id, kind, ref) VALUES (?,?,?)", userID, kind, ref)
	return wrapErr(err)
}

// ListSuspensionActions returns the refs of one kind recorded for userID.
func (s *Store) ListSuspensionActions(ctx context.Context, userID int64, kind string) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT ref FROM suspension_actions WHERE user_id = ? AND kind = ? ORDER BY ref", userID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var ref int64
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// ClearSuspensionActions forgets everything recorded for userID.
func (s *Store) ClearSuspensionActions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM suspension_actions WHERE user_id = ?", userID)
	return wrapErr(err)
}

// SetMailboxEnabled flips just the enabled flag of a mailbox.
func (s *Store) SetMailboxEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE mailboxes SET enabled = ? WHERE id = ?", enabled, id)
	return wrapErr(err)
}
