package store

import "context"

const ipCols = `id, address, label, kind, created_at`

func scanIP(row interface{ Scan(...any) error }) (*IP, error) {
	var ip IP
	var created interface{}
	if err := row.Scan(&ip.ID, &ip.Address, &ip.Label, &ip.Kind, &created); err != nil {
		return nil, wrapErr(err)
	}
	ip.CreatedAt = parseTime(created)
	return &ip, nil
}

func (s *Store) CreateIP(ctx context.Context, ip *IP) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO ips (address, label, kind, created_at) VALUES (?,?,?,?)`,
		ip.Address, ip.Label, ip.Kind, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	ip.ID = id
	ip.CreatedAt = parseTime(ts)
	return nil
}

func (s *Store) GetIP(ctx context.Context, id int64) (*IP, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+ipCols+" FROM ips WHERE id = ?", id)
	return scanIP(row)
}

func (s *Store) ListIPs(ctx context.Context) ([]*IP, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+ipCols+" FROM ips ORDER BY address")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*IP{}
	for rows.Next() {
		ip, err := scanIP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ip)
	}
	return out, rows.Err()
}

func (s *Store) UpdateIP(ctx context.Context, ip *IP) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ips SET label = ?, kind = ? WHERE id = ?`, ip.Label, ip.Kind, ip.ID)
	return wrapErr(err)
}

// DeleteIP removes the pool entry. Callers must first confirm no domain
// still references it (CountDomainsUsingIP) — the FK has no ON DELETE
// action, so a referencing domain would otherwise be left with a dangling
// ip_id pointing at nothing.
func (s *Store) DeleteIP(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM ips WHERE id = ?", id)
	return wrapErr(err)
}

// CountDomainsUsingIP reports how many domains currently have ip_id = id.
func (s *Store) CountDomainsUsingIP(ctx context.Context, id int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM domains WHERE ip_id = ?", id).Scan(&n)
	return n, err
}

// DomainsUsingIP returns the domains currently assigned to ip_id = id.
func (s *Store) DomainsUsingIP(ctx context.Context, id int64) ([]*Domain, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+domainCols+" FROM "+domainFrom+" WHERE d.ip_id = ? ORDER BY d.domain", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Domain{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
