package store

import "context"

// --- system package updates -------------------------------------------------

const pkgUpdateCols = `id, name, current_version, new_version, is_security, checked_at`

func scanPackageUpdate(row interface{ Scan(...any) error }) (*PackageUpdate, error) {
	var u PackageUpdate
	var security, checked interface{}
	if err := row.Scan(&u.ID, &u.Name, &u.CurrentVersion, &u.NewVersion, &security, &checked); err != nil {
		return nil, wrapErr(err)
	}
	u.Security = getBool(security)
	u.CheckedAt = parseTime(checked)
	return &u, nil
}

// ReplacePackageUpdates atomically replaces the whole table with the result
// of a fresh check-updates run, so the table always reflects exactly the
// latest run rather than an accumulating/diffed history.
func (s *Store) ReplacePackageUpdates(ctx context.Context, updates []PackageUpdate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM system_package_updates"); err != nil {
		return wrapErr(err)
	}
	ts := now()
	for _, u := range updates {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO system_package_updates (name, current_version, new_version, is_security, checked_at) VALUES (?,?,?,?,?)",
			u.Name, u.CurrentVersion, u.NewVersion, u.Security, ts); err != nil {
			return wrapErr(err)
		}
	}
	return tx.Commit()
}

// ListPackageUpdates returns the last check-updates run's results.
func (s *Store) ListPackageUpdates(ctx context.Context) ([]*PackageUpdate, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+pkgUpdateCols+" FROM system_package_updates ORDER BY is_security DESC, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PackageUpdate{}
	for rows.Next() {
		u, err := scanPackageUpdate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
