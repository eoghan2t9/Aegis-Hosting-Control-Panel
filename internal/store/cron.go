package store

import "context"

// --- cron jobs ------------------------------------------------------------

const cronCols = `id, user_id, schedule, command, log_path, enabled, created_at`

func scanCronJob(row interface{ Scan(...any) error }) (*CronJob, error) {
	var j CronJob
	var enabled, created interface{}
	if err := row.Scan(&j.ID, &j.UserID, &j.Schedule, &j.Command, &j.LogPath, &enabled, &created); err != nil {
		return nil, wrapErr(err)
	}
	j.Enabled = getBool(enabled)
	j.CreatedAt = parseTime(created)
	return &j, nil
}

func (s *Store) CreateCronJob(ctx context.Context, j *CronJob) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, "INSERT INTO cron_jobs (user_id, schedule, command, log_path, enabled, created_at) VALUES (?,?,?,?,?,?)",
		j.UserID, j.Schedule, j.Command, j.LogPath, j.Enabled, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	j.ID = id
	j.CreatedAt = parseTime(ts)
	return nil
}

// ListCronJobs returns jobs; when userID > 0, only that user's jobs.
func (s *Store) ListCronJobs(ctx context.Context, userID int64) ([]*CronJob, error) {
	q := "SELECT " + cronCols + " FROM cron_jobs"
	args := []interface{}{}
	if userID > 0 {
		q += " WHERE user_id = ?"
		args = append(args, userID)
	}
	q += " ORDER BY id"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*CronJob{}
	for rows.Next() {
		j, err := scanCronJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) GetCronJob(ctx context.Context, id int64) (*CronJob, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+cronCols+" FROM cron_jobs WHERE id = ?", id)
	return scanCronJob(row)
}

func (s *Store) UpdateCronJob(ctx context.Context, j *CronJob) error {
	_, err := s.db.ExecContext(ctx, "UPDATE cron_jobs SET schedule=?, command=?, log_path=?, enabled=? WHERE id=?",
		j.Schedule, j.Command, j.LogPath, j.Enabled, j.ID)
	return wrapErr(err)
}

func (s *Store) DeleteCronJob(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM cron_jobs WHERE id = ?", id)
	return err
}
