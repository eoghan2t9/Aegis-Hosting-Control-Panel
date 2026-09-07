package store

import (
	"context"
	"fmt"
)

const userCols = `id, username, email, password_hash, role, package_id, status, owner_id,
	home_dir, quota_disk_bytes, quota_bandwidth_bytes, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var created, updated interface{}
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.PackageID,
		&u.Status, &u.OwnerID, &u.HomeDir, &u.QuotaDiskBytes, &u.QuotaBandwidthBytes,
		&created, &updated); err != nil {
		return nil, wrapErr(err)
	}
	u.CreatedAt = parseTime(created)
	u.UpdatedAt = parseTime(updated)
	return &u, nil
}

func (s *Store) CreateUser(ctx context.Context, u *User) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO users (username, email, password_hash, role,
		package_id, status, owner_id, home_dir, quota_disk_bytes, quota_bandwidth_bytes, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.Username, u.Email, u.PasswordHash, u.Role, u.PackageID, u.Status, u.OwnerID,
		u.HomeDir, u.QuotaDiskBytes, u.QuotaBandwidthBytes, ts, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	u.ID = id
	u.CreatedAt = parseTime(ts)
	u.UpdatedAt = parseTime(ts)
	return nil
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*User, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", id)
	return scanUser(row)
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE username = ?", username)
	return scanUser(row)
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE email = ?", email)
	return scanUser(row)
}

// ListUsers returns users. ownerID > 0 restricts to accounts owned by that
// user (reseller scoping). includeSuspended controls whether suspended
// accounts appear.
func (s *Store) ListUsers(ctx context.Context, ownerID int64) ([]*User, error) {
	q := "SELECT " + userCols + " FROM users"
	args := []interface{}{}
	if ownerID > 0 {
		q += " WHERE owner_id = ?"
		args = append(args, ownerID)
	}
	q += " ORDER BY username"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ListUsersWithUsage enriches users with disk usage, domain counts and package
// names. Used by the admin dashboard and CLI. When sysUID is provided, disk
// usage is looked up per system uid (from the FTP/system account).
func (s *Store) ListUsersWithUsage(ctx context.Context, usage map[string]int64) ([]*User, error) {
	users, err := s.ListUsers(ctx, 0)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		u.DiskUsedBytes = usage[u.Username]
		_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM domains WHERE user_id = ?", u.ID).Scan(&u.DomainCount)
		_ = s.db.QueryRowContext(ctx, "SELECT name FROM packages WHERE id = ?", u.PackageID).Scan(&u.PackageName)
	}
	return users, nil
}

func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET email=?, role=?, package_id=?, status=?,
		home_dir=?, quota_disk_bytes=?, quota_bandwidth_bytes=?, updated_at=? WHERE id=?`,
		u.Email, u.Role, u.PackageID, u.Status, u.HomeDir, u.QuotaDiskBytes,
		u.QuotaBandwidthBytes, now(), u.ID)
	return wrapErr(err)
}

func (s *Store) SetUserPassword(ctx context.Context, id int64, hash string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE users SET password_hash=?, updated_at=? WHERE id=?", hash, now(), id)
	return err
}

func (s *Store) SetUserStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE users SET status=?, updated_at=? WHERE id=?", status, now(), id)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	// Delete dependent rows first (FKs are on by default).
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		"DELETE FROM domains WHERE user_id = ?",
		"DELETE FROM ftp_accounts WHERE user_id = ?",
		"DELETE FROM databases WHERE user_id = ?",
		"DELETE FROM sessions WHERE user_id = ?",
		"DELETE FROM users WHERE id = ?",
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return wrapErr(err)
		}
	}
	return tx.Commit()
}

// CountUsers returns the total number of accounts (optionally scoped).
func (s *Store) CountUsers(ctx context.Context, ownerID int64) (int, error) {
	q := "SELECT COUNT(*) FROM users"
	args := []interface{}{}
	if ownerID > 0 {
		q += " WHERE owner_id = ?"
		args = append(args, ownerID)
	}
	var n int
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

// --- packages ----------------------------------------------------------------

const pkgCols = `id, name, description, max_domains, max_databases, max_ftp_accounts,
	disk_quota_bytes, bandwidth_quota_bytes, allow_ssl, allow_dns, allow_terminal, allow_backups,
	is_default, created_at`

func scanPackage(row interface{ Scan(...any) error }) (*Package, error) {
	var p Package
	var created interface{}
	if err := row.Scan(&p.ID, &p.Name, &p.Description, &p.MaxDomains, &p.MaxDatabases,
		&p.MaxFTPAccounts, &p.DiskQuotaBytes, &p.BandwidthQuotaBytes, &p.AllowSSL, &p.AllowDNS,
		&p.AllowTerminal, &p.AllowBackups, &p.IsDefault, &created); err != nil {
		return nil, wrapErr(err)
	}
	p.CreatedAt = parseTime(created)
	return &p, nil
}

func (s *Store) CreatePackage(ctx context.Context, p *Package) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO packages (name, description, max_domains,
		max_databases, max_ftp_accounts, disk_quota_bytes, bandwidth_quota_bytes, allow_ssl,
		allow_dns, allow_terminal, allow_backups, is_default, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.Description, p.MaxDomains, p.MaxDatabases, p.MaxFTPAccounts, p.DiskQuotaBytes,
		p.BandwidthQuotaBytes, p.AllowSSL, p.AllowDNS, p.AllowTerminal, p.AllowBackups, p.IsDefault, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	p.ID = id
	p.CreatedAt = parseTime(ts)
	if p.IsDefault {
		_, _ = s.db.ExecContext(ctx, "UPDATE packages SET is_default = 0 WHERE id != ?", id)
	}
	return nil
}

func (s *Store) GetPackage(ctx context.Context, id int64) (*Package, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+pkgCols+" FROM packages WHERE id = ?", id)
	return scanPackage(row)
}

func (s *Store) GetDefaultPackage(ctx context.Context) (*Package, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+pkgCols+" FROM packages WHERE is_default = 1 ORDER BY id LIMIT 1")
	return scanPackage(row)
}

func (s *Store) ListPackages(ctx context.Context) ([]*Package, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+pkgCols+" FROM packages ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Package
	for rows.Next() {
		p, err := scanPackage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdatePackage(ctx context.Context, p *Package) error {
	_, err := s.db.ExecContext(ctx, `UPDATE packages SET name=?, description=?, max_domains=?,
		max_databases=?, max_ftp_accounts=?, disk_quota_bytes=?, bandwidth_quota_bytes=?, allow_ssl=?,
		allow_dns=?, allow_terminal=?, allow_backups=?, is_default=? WHERE id=?`,
		p.Name, p.Description, p.MaxDomains, p.MaxDatabases, p.MaxFTPAccounts, p.DiskQuotaBytes,
		p.BandwidthQuotaBytes, p.AllowSSL, p.AllowDNS, p.AllowTerminal, p.AllowBackups, p.IsDefault, p.ID)
	if err != nil {
		return wrapErr(err)
	}
	if p.IsDefault {
		_, _ = s.db.ExecContext(ctx, "UPDATE packages SET is_default = 0 WHERE id != ?", p.ID)
	}
	return nil
}

func (s *Store) DeletePackage(ctx context.Context, id int64) error {
	// Prevent deleting a package that is in use.
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE package_id = ?", id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("package is assigned to %d user(s)", n)
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM packages WHERE id = ?", id)
	return wrapErr(err)
}

// CountPackageUsage returns how many of each quota a user has consumed.
type PackageUsage struct {
	Domains     int `json:"domains"`
	Databases   int `json:"databases"`
	FTPAccounts int `json:"ftp_accounts"`
}

func (s *Store) PackageUsage(ctx context.Context, userID int64) (PackageUsage, error) {
	var u PackageUsage
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM domains WHERE user_id = ?", userID).Scan(&u.Domains); err != nil {
		return u, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM databases WHERE user_id = ?", userID).Scan(&u.Databases); err != nil {
		return u, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ftp_accounts WHERE user_id = ?", userID).Scan(&u.FTPAccounts); err != nil {
		return u, err
	}
	return u, nil
}
