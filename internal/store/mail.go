package store

import "context"

// --- mail domains ---------------------------------------------------------

const mailDomainCols = `id, domain_id, domain, dkim_selector, dkim_public_key, created_at`

func scanMailDomain(row interface{ Scan(...any) error }) (*MailDomain, error) {
	var d MailDomain
	var created interface{}
	if err := row.Scan(&d.ID, &d.DomainID, &d.Domain, &d.DKIMSelector, &d.DKIMPublicKey, &created); err != nil {
		return nil, wrapErr(err)
	}
	d.CreatedAt = parseTime(created)
	return &d, nil
}

func (s *Store) CreateMailDomain(ctx context.Context, d *MailDomain) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, "INSERT INTO mail_domains (domain_id, domain, dkim_selector, dkim_public_key, created_at) VALUES (?,?,?,?,?)",
		d.DomainID, d.Domain, d.DKIMSelector, d.DKIMPublicKey, ts)
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

func (s *Store) ListMailDomains(ctx context.Context) ([]*MailDomain, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+mailDomainCols+" FROM mail_domains ORDER BY domain")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*MailDomain{}
	for rows.Next() {
		d, err := scanMailDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetMailDomain(ctx context.Context, id int64) (*MailDomain, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+mailDomainCols+" FROM mail_domains WHERE id = ?", id)
	return scanMailDomain(row)
}

func (s *Store) GetMailDomainByDomainID(ctx context.Context, domainID int64) (*MailDomain, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+mailDomainCols+" FROM mail_domains WHERE domain_id = ?", domainID)
	return scanMailDomain(row)
}

func (s *Store) DeleteMailDomain(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM mail_domains WHERE id = ?", id)
	return err
}

// --- mailboxes --------------------------------------------------------------

const mailboxCols = `m.id, m.mail_domain_id, COALESCE(d.domain,''), m.localpart, m.password_hash, m.quota_bytes, m.enabled, m.created_at`

func scanMailbox(row interface{ Scan(...any) error }) (*Mailbox, error) {
	var m Mailbox
	var enabled, created interface{}
	if err := row.Scan(&m.ID, &m.MailDomainID, &m.Domain, &m.Localpart, &m.PasswordHash, &m.QuotaBytes, &enabled, &created); err != nil {
		return nil, wrapErr(err)
	}
	m.Enabled = getBool(enabled)
	m.CreatedAt = parseTime(created)
	return &m, nil
}

func (s *Store) CreateMailbox(ctx context.Context, m *Mailbox) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, "INSERT INTO mailboxes (mail_domain_id, localpart, password_hash, quota_bytes, enabled, created_at) VALUES (?,?,?,?,?,?)",
		m.MailDomainID, m.Localpart, m.PasswordHash, m.QuotaBytes, m.Enabled, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	m.ID = id
	m.CreatedAt = parseTime(ts)
	return nil
}

// ListMailboxes returns mailboxes; when mailDomainID > 0, only that domain's.
func (s *Store) ListMailboxes(ctx context.Context, mailDomainID int64) ([]*Mailbox, error) {
	q := "SELECT " + mailboxCols + " FROM mailboxes m JOIN mail_domains d ON d.id = m.mail_domain_id"
	args := []interface{}{}
	if mailDomainID > 0 {
		q += " WHERE m.mail_domain_id = ?"
		args = append(args, mailDomainID)
	}
	q += " ORDER BY d.domain, m.localpart"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Mailbox{}
	for rows.Next() {
		m, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMailbox(ctx context.Context, id int64) (*Mailbox, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+mailboxCols+" FROM mailboxes m JOIN mail_domains d ON d.id = m.mail_domain_id WHERE m.id = ?", id)
	return scanMailbox(row)
}

func (s *Store) GetMailboxByAddress(ctx context.Context, domain, localpart string) (*Mailbox, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+mailboxCols+" FROM mailboxes m JOIN mail_domains d ON d.id = m.mail_domain_id WHERE d.domain = ? AND m.localpart = ?", domain, localpart)
	return scanMailbox(row)
}

func (s *Store) UpdateMailbox(ctx context.Context, m *Mailbox) error {
	_, err := s.db.ExecContext(ctx, "UPDATE mailboxes SET password_hash=?, quota_bytes=?, enabled=? WHERE id=?",
		m.PasswordHash, m.QuotaBytes, m.Enabled, m.ID)
	return wrapErr(err)
}

func (s *Store) DeleteMailbox(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM mailboxes WHERE id = ?", id)
	return err
}

// --- mail aliases -------------------------------------------------------------

const mailAliasCols = `a.id, a.mail_domain_id, COALESCE(d.domain,''), a.source, a.destination, a.created_at`

func scanMailAlias(row interface{ Scan(...any) error }) (*MailAlias, error) {
	var a MailAlias
	var created interface{}
	if err := row.Scan(&a.ID, &a.MailDomainID, &a.Domain, &a.Source, &a.Destination, &created); err != nil {
		return nil, wrapErr(err)
	}
	a.CreatedAt = parseTime(created)
	return &a, nil
}

func (s *Store) CreateMailAlias(ctx context.Context, a *MailAlias) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, "INSERT INTO mail_aliases (mail_domain_id, source, destination, created_at) VALUES (?,?,?,?)",
		a.MailDomainID, a.Source, a.Destination, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	a.ID = id
	a.CreatedAt = parseTime(ts)
	return nil
}

func (s *Store) ListMailAliases(ctx context.Context, mailDomainID int64) ([]*MailAlias, error) {
	q := "SELECT " + mailAliasCols + " FROM mail_aliases a JOIN mail_domains d ON d.id = a.mail_domain_id"
	args := []interface{}{}
	if mailDomainID > 0 {
		q += " WHERE a.mail_domain_id = ?"
		args = append(args, mailDomainID)
	}
	q += " ORDER BY d.domain, a.source"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*MailAlias{}
	for rows.Next() {
		a, err := scanMailAlias(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetMailAlias(ctx context.Context, id int64) (*MailAlias, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+mailAliasCols+" FROM mail_aliases a JOIN mail_domains d ON d.id = a.mail_domain_id WHERE a.id = ?", id)
	return scanMailAlias(row)
}

func (s *Store) DeleteMailAlias(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM mail_aliases WHERE id = ?", id)
	return err
}
