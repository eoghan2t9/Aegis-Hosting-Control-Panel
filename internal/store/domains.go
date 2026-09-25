package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// domainCols is always selected against domainFrom ("FROM domains d LEFT
// JOIN ips ip ON ip.id = d.ip_id LEFT JOIN users u ON u.id = d.user_id") so
// ip_address and username come back resolved without every caller needing
// its own join.
const domainCols = `d.id, d.user_id, d.domain, d.document_root, d.php_version, d.webserver, d.ssl_enabled,
	d.ssl_cert_path, d.ssl_key_path, d.ssl_provider, d.ssl_auto_renew, d.proxy_target, d.php_settings,
	d.ip_id, COALESCE(ip.address, ''), COALESCE(u.username, ''), d.parent_domain_id, d.created_at`

func scanDomain(row interface{ Scan(...any) error }) (*Domain, error) {
	var d Domain
	var enabled, autoRenew, created interface{}
	var phpSettings string
	if err := row.Scan(&d.ID, &d.UserID, &d.Domain, &d.DocumentRoot, &d.PHPVersion,
		&d.WebServer, &enabled, &d.SSLCertPath, &d.SSLKeyPath, &d.SSLProvider,
		&autoRenew, &d.ProxyTarget, &phpSettings, &d.IPID, &d.IPAddress, &d.Username,
		&d.ParentDomainID, &created); err != nil {
		return nil, wrapErr(err)
	}
	d.SSLEnabled = getBool(enabled)
	d.SSLAutoRenew = getBool(autoRenew)
	d.PHPSettings = map[string]string{}
	_ = json.Unmarshal([]byte(phpSettings), &d.PHPSettings)
	d.CreatedAt = parseTime(created)
	return &d, nil
}

func (s *Store) CreateDomain(ctx context.Context, d *Domain) error {
	phpSettings, err := json.Marshal(d.PHPSettings)
	if err != nil {
		return err
	}
	ts := now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO domains (user_id, domain, document_root,
		php_version, webserver, ssl_enabled, ssl_cert_path, ssl_key_path, ssl_provider,
		ssl_auto_renew, proxy_target, php_settings, parent_domain_id, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.UserID, d.Domain, d.DocumentRoot, d.PHPVersion, d.WebServer, d.SSLEnabled,
		d.SSLCertPath, d.SSLKeyPath, d.SSLProvider, d.SSLAutoRenew, d.ProxyTarget, string(phpSettings),
		d.ParentDomainID, ts)
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

const domainFrom = `domains d LEFT JOIN ips ip ON ip.id = d.ip_id LEFT JOIN users u ON u.id = d.user_id`

func (s *Store) GetDomain(ctx context.Context, id int64) (*Domain, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+domainCols+" FROM "+domainFrom+" WHERE d.id = ?", id)
	return scanDomain(row)
}

func (s *Store) GetDomainByName(ctx context.Context, name string) (*Domain, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+domainCols+" FROM "+domainFrom+" WHERE d.domain = ?", name)
	return scanDomain(row)
}

// ListDomains returns domains; when userID > 0, only that user's domains.
func (s *Store) ListDomains(ctx context.Context, userID int64) ([]*Domain, error) {
	q := "SELECT " + domainCols + " FROM " + domainFrom
	args := []interface{}{}
	if userID > 0 {
		q += " WHERE d.user_id = ?"
		args = append(args, userID)
	}
	q += " ORDER BY d.domain"
	rows, err := s.db.QueryContext(ctx, q, args...)
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

func (s *Store) UpdateDomain(ctx context.Context, d *Domain) error {
	phpSettings, err := json.Marshal(d.PHPSettings)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE domains SET document_root=?, php_version=?, webserver=?,
		ssl_enabled=?, ssl_cert_path=?, ssl_key_path=?, ssl_provider=?, ssl_auto_renew=?, php_settings=? WHERE id=?`,
		d.DocumentRoot, d.PHPVersion, d.WebServer, d.SSLEnabled, d.SSLCertPath, d.SSLKeyPath,
		d.SSLProvider, d.SSLAutoRenew, string(phpSettings), d.ID)
	return wrapErr(err)
}

// SetDomainProxyTarget updates only proxy_target — called by svc.Docker when
// a container attached to this domain starts/stops, without disturbing any
// other domain field a concurrent request might be changing.
func (s *Store) SetDomainProxyTarget(ctx context.Context, domainID int64, target string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE domains SET proxy_target=? WHERE id=?", target, domainID)
	return wrapErr(err)
}

// SetDomainIP updates only ip_id (0 clears the assignment) — called by
// svc.IPs.Assign, without disturbing any other domain field.
func (s *Store) SetDomainIP(ctx context.Context, domainID, ipID int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE domains SET ip_id=? WHERE id=?", ipID, domainID)
	return wrapErr(err)
}

func (s *Store) DeleteDomain(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		"DELETE FROM domain_aliases WHERE domain_id = ?",
		"DELETE FROM dns_records WHERE zone_id IN (SELECT id FROM dns_zones WHERE domain_id = ?)",
		"DELETE FROM dns_zones WHERE domain_id = ?",
		"DELETE FROM ssl_orders WHERE domain_id = ?",
		// Containers survive their attached domain's deletion (they may still
		// be running services with data volumes) but lose the attachment —
		// otherwise domain_id would dangle and web_port would silently start
		// pointing at whatever new domain later reuses this id.
		"UPDATE containers SET domain_id = 0, web_port = 0 WHERE domain_id = ?",
		// Sub-domains survive their master's deletion too, same reasoning —
		// they un-link (become top-level) rather than dangling or cascading.
		"UPDATE domains SET parent_domain_id = 0 WHERE parent_domain_id = ?",
		"DELETE FROM domains WHERE id = ?",
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return wrapErr(err)
		}
	}
	return tx.Commit()
}

// --- aliases ------------------------------------------------------------------

func (s *Store) ListAliases(ctx context.Context, domainID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT alias FROM domain_aliases WHERE domain_id = ? ORDER BY alias", domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) AddAlias(ctx context.Context, domainID int64, alias string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO domain_aliases (domain_id, alias) VALUES (?,?)", domainID, alias)
	return wrapErr(err)
}

func (s *Store) RemoveAlias(ctx context.Context, domainID int64, alias string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM domain_aliases WHERE domain_id = ? AND alias = ?", domainID, alias)
	return err
}

// --- DNS zones -----------------------------------------------------------------

const zoneCols = `z.id, z.domain_id, COALESCE(d.domain,''), z.provider, z.provider_zone_id, z.synced_at, z.created_at`

func scanZone(row interface{ Scan(...any) error }) (*DNSZone, error) {
	var z DNSZone
	var synced, created interface{}
	if err := row.Scan(&z.ID, &z.DomainID, &z.Domain, &z.Provider, &z.ProviderZoneID, &synced, &created); err != nil {
		return nil, wrapErr(err)
	}
	if synced != nil {
		if t, err := time.Parse(time.RFC3339, getString(synced)); err == nil {
			z.SyncedAt = &t
		}
	}
	z.CreatedAt = parseTime(created)
	return &z, nil
}

func (s *Store) CreateZone(ctx context.Context, z *DNSZone) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, "INSERT INTO dns_zones (domain_id, provider, provider_zone_id, created_at) VALUES (?,?,?,?)",
		z.DomainID, z.Provider, z.ProviderZoneID, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	z.ID = id
	z.CreatedAt = parseTime(ts)
	return nil
}

func (s *Store) GetZone(ctx context.Context, id int64) (*DNSZone, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+zoneCols+" FROM dns_zones z LEFT JOIN domains d ON d.id = z.domain_id WHERE z.id = ?", id)
	return scanZone(row)
}

func (s *Store) GetZoneByDomain(ctx context.Context, domainID int64) (*DNSZone, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+zoneCols+" FROM dns_zones z LEFT JOIN domains d ON d.id = z.domain_id WHERE z.domain_id = ?", domainID)
	return scanZone(row)
}

func (s *Store) ListZones(ctx context.Context) ([]*DNSZone, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+zoneCols+" FROM dns_zones z LEFT JOIN domains d ON d.id = z.domain_id ORDER BY d.domain")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DNSZone{}
	for rows.Next() {
		z, err := scanZone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

func (s *Store) UpdateZone(ctx context.Context, z *DNSZone) error {
	_, err := s.db.ExecContext(ctx, "UPDATE dns_zones SET provider=?, provider_zone_id=?, synced_at=? WHERE id=?",
		z.Provider, z.ProviderZoneID, z.SyncedAt, z.ID)
	return err
}

func (s *Store) SetZoneSynced(ctx context.Context, id int64, t string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE dns_zones SET synced_at=? WHERE id=?", t, id)
	return err
}

// --- DNS records ---------------------------------------------------------------

const recordCols = `id, zone_id, name, type, ttl, priority, content, proxied, created_at`

func scanRecord(row interface{ Scan(...any) error }) (*DNSRecord, error) {
	var r DNSRecord
	var proxied, created interface{}
	if err := row.Scan(&r.ID, &r.ZoneID, &r.Name, &r.Type, &r.TTL, &r.Priority, &r.Content, &proxied, &created); err != nil {
		return nil, wrapErr(err)
	}
	r.Proxied = getBool(proxied)
	r.CreatedAt = parseTime(created)
	return &r, nil
}

func (s *Store) ListRecords(ctx context.Context, zoneID int64) ([]*DNSRecord, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+recordCols+" FROM dns_records WHERE zone_id = ? ORDER BY name, type, content", zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DNSRecord{}
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRecord(ctx context.Context, id int64) (*DNSRecord, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+recordCols+" FROM dns_records WHERE id = ?", id)
	return scanRecord(row)
}

func (s *Store) CreateRecord(ctx context.Context, r *DNSRecord) error {
	ts := now()
	res, err := s.db.ExecContext(ctx, "INSERT INTO dns_records (zone_id, name, type, ttl, priority, content, proxied, created_at) VALUES (?,?,?,?,?,?,?,?)",
		r.ZoneID, r.Name, r.Type, r.TTL, r.Priority, r.Content, r.Proxied, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	r.ID = id
	r.CreatedAt = parseTime(ts)
	return nil
}

func (s *Store) UpdateRecord(ctx context.Context, r *DNSRecord) error {
	_, err := s.db.ExecContext(ctx, "UPDATE dns_records SET name=?, type=?, ttl=?, priority=?, content=?, proxied=? WHERE id=?",
		r.Name, r.Type, r.TTL, r.Priority, r.Content, r.Proxied, r.ID)
	return wrapErr(err)
}

func (s *Store) DeleteRecord(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM dns_records WHERE id = ?", id)
	return err
}

// ReplaceRecords deletes all records for a zone and inserts the given set in
// one transaction (used by provider sync pull/push).
func (s *Store) ReplaceRecords(ctx context.Context, zoneID int64, records []*DNSRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM dns_records WHERE zone_id = ?", zoneID); err != nil {
		return err
	}
	ts := now()
	for _, r := range records {
		if r.ZoneID == 0 {
			r.ZoneID = zoneID
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO dns_records (zone_id, name, type, ttl, priority, content, proxied, created_at) VALUES (?,?,?,?,?,?,?,?)",
			zoneID, r.Name, r.Type, r.TTL, r.Priority, r.Content, r.Proxied, ts); err != nil {
			return wrapErr(err)
		}
	}
	return tx.Commit()
}

// normalizeName ensures record names are stored relative ('@' for apex) and
// lower-cased.
func normalizeName(zoneDomain, name string) string {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if name == zoneDomain || name == "" {
		return "@"
	}
	return strings.TrimSuffix(name, "."+zoneDomain)
}
