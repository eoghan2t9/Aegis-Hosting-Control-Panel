package store

import (
	"context"
	"time"
)

// --- SSL orders ---------------------------------------------------------------

const sslCols = `o.id, o.domain_id, COALESCE(d.domain,''), o.status, o.provider, o.challenge,
	o.cert_path, o.key_path, o.expires_at, o.error, o.created_at, o.updated_at`

func scanSSLOrder(row interface{ Scan(...any) error }) (*SSLOrder, error) {
	var o SSLOrder
	var exp, created, updated interface{}
	if err := row.Scan(&o.ID, &o.DomainID, &o.Domain, &o.Status, &o.Provider, &o.Challenge,
		&o.CertPath, &o.KeyPath, &exp, &o.Error, &created, &updated); err != nil {
		return nil, wrapErr(err)
	}
	o.ExpiresAt = parseTimePtr(exp)
	o.CreatedAt = parseTime(created)
	o.UpdatedAt = parseTime(updated)
	return &o, nil
}

func (s *Store) CreateSSLOrder(ctx context.Context, o *SSLOrder) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO ssl_orders (domain_id, status, provider, challenge,
		cert_path, key_path, expires_at, error, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		o.DomainID, o.Status, o.Provider, o.Challenge, o.CertPath, o.KeyPath, nullableTime(o.ExpiresAt), o.Error, ts, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	o.ID = id
	o.CreatedAt = parseTime(ts)
	o.UpdatedAt = parseTime(ts)
	return nil
}

func (s *Store) GetSSLOrder(ctx context.Context, id int64) (*SSLOrder, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+sslCols+" FROM ssl_orders o LEFT JOIN domains d ON d.id = o.domain_id WHERE o.id = ?", id)
	return scanSSLOrder(row)
}

// GetSSLOrderByDomain returns domainID's single ssl_orders row (there is at
// most one — see the unique index in migrate()), ErrNotFound if it's never
// had a certificate issued.
func (s *Store) GetSSLOrderByDomain(ctx context.Context, domainID int64) (*SSLOrder, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+sslCols+" FROM ssl_orders o LEFT JOIN domains d ON d.id = o.domain_id WHERE o.domain_id = ?", domainID)
	return scanSSLOrder(row)
}

func (s *Store) ListSSLOrders(ctx context.Context, domainID int64) ([]*SSLOrder, error) {
	q := "SELECT " + sslCols + " FROM ssl_orders o LEFT JOIN domains d ON d.id = o.domain_id"
	args := []interface{}{}
	if domainID > 0 {
		q += " WHERE o.domain_id = ?"
		args = append(args, domainID)
	}
	q += " ORDER BY o.id DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SSLOrder{}
	for rows.Next() {
		o, err := scanSSLOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSSLOrder(ctx context.Context, o *SSLOrder) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ssl_orders SET status=?, provider=?, challenge=?,
		cert_path=?, key_path=?, expires_at=?, error=?, updated_at=? WHERE id=?`,
		o.Status, o.Provider, o.Challenge, o.CertPath, o.KeyPath, nullableTime(o.ExpiresAt), o.Error, now(), o.ID)
	return wrapErr(err)
}

// Renewables returns active orders whose certificates expire within days.
func (s *Store) Renewables(ctx context.Context, days int) ([]*SSLOrder, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, days).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, "SELECT "+sslCols+" FROM ssl_orders o LEFT JOIN domains d ON d.id = o.domain_id WHERE o.status IN ('issued','renewing') AND o.expires_at IS NOT NULL AND o.expires_at < ?", cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SSLOrder{}
	for rows.Next() {
		o, err := scanSSLOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// --- FTP accounts -------------------------------------------------------------

const ftpCols = `id, user_id, username, password_hash, home_dir, enabled, created_at`

func scanFTP(row interface{ Scan(...any) error }) (*FTPAccount, error) {
	var f FTPAccount
	var enabled, created interface{}
	if err := row.Scan(&f.ID, &f.UserID, &f.Username, &f.PasswordHash, &f.HomeDir, &enabled, &created); err != nil {
		return nil, wrapErr(err)
	}
	f.Enabled = getBool(enabled)
	f.CreatedAt = parseTime(created)
	return &f, nil
}

func (s *Store) CreateFTPAccount(ctx context.Context, f *FTPAccount) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, "INSERT INTO ftp_accounts (user_id, username, password_hash, home_dir, enabled, created_at) VALUES (?,?,?,?,?,?)",
		f.UserID, f.Username, f.PasswordHash, f.HomeDir, f.Enabled, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	f.ID = id
	f.CreatedAt = parseTime(ts)
	return nil
}

func (s *Store) ListFTPAccounts(ctx context.Context, userID int64) ([]*FTPAccount, error) {
	q := "SELECT " + ftpCols + " FROM ftp_accounts"
	args := []interface{}{}
	if userID > 0 {
		q += " WHERE user_id = ?"
		args = append(args, userID)
	}
	q += " ORDER BY username"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*FTPAccount{}
	for rows.Next() {
		f, err := scanFTP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) GetFTPAccount(ctx context.Context, id int64) (*FTPAccount, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+ftpCols+" FROM ftp_accounts WHERE id = ?", id)
	return scanFTP(row)
}

func (s *Store) GetFTPAccountByUsername(ctx context.Context, username string) (*FTPAccount, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+ftpCols+" FROM ftp_accounts WHERE username = ?", username)
	return scanFTP(row)
}

func (s *Store) UpdateFTPAccount(ctx context.Context, f *FTPAccount) error {
	_, err := s.db.ExecContext(ctx, "UPDATE ftp_accounts SET password_hash=?, home_dir=?, enabled=? WHERE id=?",
		f.PasswordHash, f.HomeDir, f.Enabled, f.ID)
	return wrapErr(err)
}

func (s *Store) DeleteFTPAccount(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM ftp_accounts WHERE id = ?", id)
	return err
}

// --- databases ----------------------------------------------------------------

const dbCols = `id, user_id, server, name, db_user, db_password, created_at`

func scanDatabase(row interface{ Scan(...any) error }) (*Database, error) {
	var d Database
	var created interface{}
	if err := row.Scan(&d.ID, &d.UserID, &d.Server, &d.Name, &d.DBUser, &d.DBPassword, &created); err != nil {
		return nil, wrapErr(err)
	}
	d.CreatedAt = parseTime(created)
	return &d, nil
}

func (s *Store) CreateDatabase(ctx context.Context, d *Database) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, "INSERT INTO databases (user_id, server, name, db_user, db_password, created_at) VALUES (?,?,?,?,?,?)",
		d.UserID, d.Server, d.Name, d.DBUser, d.DBPassword, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	d.ID = id
	d.CreatedAt = parseTime(ts)
	return nil
}

func (s *Store) ListDatabases(ctx context.Context, userID int64) ([]*Database, error) {
	q := "SELECT " + dbCols + " FROM databases"
	args := []interface{}{}
	if userID > 0 {
		q += " WHERE user_id = ?"
		args = append(args, userID)
	}
	q += " ORDER BY server, name"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Database{}
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetDatabase(ctx context.Context, id int64) (*Database, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+dbCols+" FROM databases WHERE id = ?", id)
	return scanDatabase(row)
}

func (s *Store) DeleteDatabaseRow(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM databases WHERE id = ?", id)
	return err
}

// --- DNS providers --------------------------------------------------------------

const provCols = `id, name, label, api_key_enc, email, config, enabled, created_at`

func scanProvider(row interface{ Scan(...any) error }) (*Provider, error) {
	var p Provider
	var enabled, created interface{}
	if err := row.Scan(&p.ID, &p.Name, &p.Label, &p.APIKeyEnc, &p.Email, &p.Config, &enabled, &created); err != nil {
		return nil, wrapErr(err)
	}
	p.Enabled = getBool(enabled)
	p.CreatedAt = parseTime(created)
	return &p, nil
}

func (s *Store) CreateProvider(ctx context.Context, p *Provider) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, "INSERT INTO dns_providers (name, label, api_key_enc, email, config, enabled, created_at) VALUES (?,?,?,?,?,?,?)",
		p.Name, p.Label, p.APIKeyEnc, p.Email, p.Config, p.Enabled, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	p.ID = id
	p.CreatedAt = parseTime(ts)
	return nil
}

func (s *Store) ListProviders(ctx context.Context) ([]*Provider, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+provCols+" FROM dns_providers ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Provider{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProvider(ctx context.Context, id int64) (*Provider, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+provCols+" FROM dns_providers WHERE id = ?", id)
	return scanProvider(row)
}

func (s *Store) UpdateProvider(ctx context.Context, p *Provider) error {
	_, err := s.db.ExecContext(ctx, "UPDATE dns_providers SET label=?, api_key_enc=?, email=?, config=?, enabled=? WHERE id=?",
		p.Label, p.APIKeyEnc, p.Email, p.Config, p.Enabled, p.ID)
	return wrapErr(err)
}

func (s *Store) DeleteProvider(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM dns_providers WHERE id = ?", id)
	return err
}
