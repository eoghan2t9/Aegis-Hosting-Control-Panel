package store

import "context"

// --- backup targets ---------------------------------------------------------

const backupTargetCols = `id, kind, label, config_enc, retention_days, enabled, created_at`

func scanBackupTarget(row interface{ Scan(...any) error }) (*BackupTarget, error) {
	var t BackupTarget
	var enabled, created interface{}
	if err := row.Scan(&t.ID, &t.Kind, &t.Label, &t.ConfigEnc, &t.RetentionDays, &enabled, &created); err != nil {
		return nil, wrapErr(err)
	}
	t.Enabled = getBool(enabled)
	t.CreatedAt = parseTime(created)
	return &t, nil
}

func (s *Store) CreateBackupTarget(ctx context.Context, t *BackupTarget) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, "INSERT INTO backup_targets (kind, label, config_enc, retention_days, enabled, created_at) VALUES (?,?,?,?,?,?)",
		t.Kind, t.Label, t.ConfigEnc, t.RetentionDays, t.Enabled, ts)
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

func (s *Store) ListBackupTargets(ctx context.Context) ([]*BackupTarget, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+backupTargetCols+" FROM backup_targets ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*BackupTarget{}
	for rows.Next() {
		t, err := scanBackupTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetBackupTarget(ctx context.Context, id int64) (*BackupTarget, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+backupTargetCols+" FROM backup_targets WHERE id = ?", id)
	return scanBackupTarget(row)
}

func (s *Store) UpdateBackupTarget(ctx context.Context, t *BackupTarget) error {
	_, err := s.db.ExecContext(ctx, "UPDATE backup_targets SET label=?, config_enc=?, retention_days=?, enabled=? WHERE id=?",
		t.Label, t.ConfigEnc, t.RetentionDays, t.Enabled, t.ID)
	return wrapErr(err)
}

func (s *Store) DeleteBackupTarget(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM backup_targets WHERE id = ?", id)
	return err
}
