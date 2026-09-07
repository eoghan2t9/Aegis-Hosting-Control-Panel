package svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Provider is a DNS backend plugin. Built-ins: "local" (zone files + the
// built-in authoritative server) and "cloudflare". New providers implement
// this interface and register themselves in Providers().
type Provider interface {
	Name() string
	// EnsureZone verifies (or creates) the zone at the provider and returns
	// the provider-side zone identifier.
	EnsureZone(ctx context.Context, domain string) (string, error)
	// Push performs a full reconcile: create missing, update changed, delete
	// removed records.
	Push(ctx context.Context, domain, providerZoneID string, records []*store.DNSRecord) error
	// DeleteZone removes the zone from the provider.
	DeleteZone(ctx context.Context, domain, providerZoneID string) error
}

// SyncResult reports the outcome of a zone sync.
type SyncResult struct {
	ZoneID   int64  `json:"zone_id"`
	Domain   string `json:"domain"`
	Provider string `json:"provider"`
	Pushed   int    `json:"pushed"`
	Deleted  int    `json:"deleted"`
	Error    string `json:"error,omitempty"`
}

// DNS coordinates zones, records and provider plugins.
type DNS struct {
	Cfg    *config.Config
	Store  *store.Store
	Cipher *Cipher
}

func NewDNS(cfg *config.Config, st *store.Store, cipher *Cipher) *DNS {
	return &DNS{Cfg: cfg, Store: st, Cipher: cipher}
}

// PluginNames returns registered provider plugin ids.
func (d *DNS) PluginNames() []string {
	return []string{"local", "cloudflare"}
}

// provider builds a Provider for a zone's configured provider.
func (d *DNS) provider(zone *store.DNSZone) (Provider, error) {
	switch zone.Provider {
	case "local":
		return localProvider{d: d}, nil
	case "cloudflare":
		return d.cloudflareProvider()
	default:
		return nil, fmt.Errorf("dns: unknown provider %q", zone.Provider)
	}
}

// Sync reconciles a zone with its provider: local writes zone files and
// refreshes the built-in server; cloudflare pushes records via the API.
func (d *DNS) Sync(ctx context.Context, zoneID int64) (*SyncResult, error) {
	zone, err := d.Store.GetZone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	records, err := d.Store.ListRecords(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	prov, err := d.provider(zone)
	if err != nil {
		return nil, err
	}

	res := &SyncResult{ZoneID: zoneID, Domain: zone.Domain, Provider: zone.Provider}
	if zone.Provider == "local" {
		if err := d.WriteZoneFile(zone, records); err != nil {
			res.Error = err.Error()
			return res, err
		}
		res.Pushed = len(records)
	} else {
		pzid := zone.ProviderZoneID
		if pzid == "" {
			pzid, err = prov.EnsureZone(ctx, zone.Domain)
			if err != nil {
				res.Error = err.Error()
				return res, err
			}
			zone.ProviderZoneID = pzid
		}
		before := len(records)
		if err := prov.Push(ctx, zone.Domain, pzid, records); err != nil {
			res.Error = err.Error()
			return res, err
		}
		res.Pushed = len(records)
		res.Deleted = before - len(records)
		if res.Deleted < 0 {
			res.Deleted = 0
		}
		zone.ProviderZoneID = pzid
	}
	if err := d.Store.SetZoneSynced(ctx, zoneID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return res, err
	}
	return res, nil
}

// DeleteZone removes a zone from its provider (used when a domain is deleted).
func (d *DNS) DeleteZone(ctx context.Context, zoneID int64) error {
	zone, err := d.Store.GetZone(ctx, zoneID)
	if err != nil {
		return err
	}
	if zone.Provider == "local" {
		_ = os.Remove(filepath.Join(d.Cfg.DNSDir, zone.Domain+".zone"))
		return nil
	}
	prov, err := d.provider(zone)
	if err != nil {
		return err
	}
	return prov.DeleteZone(ctx, zone.Domain, zone.ProviderZoneID)
}

// localProvider serves zones from the store via the built-in authoritative
// server and writes RFC1035 zone files for bind users.
type localProvider struct{ d *DNS }

func (p localProvider) Name() string { return "local" }

func (p localProvider) EnsureZone(ctx context.Context, domain string) (string, error) {
	return "", nil
}

func (p localProvider) Push(ctx context.Context, domain, providerZoneID string, records []*store.DNSRecord) error {
	// Zone files are written directly by DNS.Sync; nothing extra to push.
	return nil
}

func (p localProvider) DeleteZone(ctx context.Context, domain, providerZoneID string) error {
	_ = os.Remove(filepath.Join(p.d.Cfg.DNSDir, domain+".zone"))
	return nil
}

// WriteZoneFile renders an RFC1035 master zone file for bind/named and the
// panel's own documentation of the zone.
func (d *DNS) WriteZoneFile(zone *store.DNSZone, records []*store.DNSRecord) error {
	if err := os.MkdirAll(d.Cfg.DNSDir, 0o750); err != nil {
		return err
	}
	domain := zone.Domain
	ns := d.Cfg.DNS.Nameservers
	if len(ns) == 0 {
		ns = []string{"ns1." + domain, "ns2." + domain}
	}
	admin := d.Cfg.DNS.AdminEmail
	if admin == "" {
		admin = "hostmaster." + domain
	}
	serial := time.Now().UTC().Format("2006010201")

	var sb strings.Builder
	fmt.Fprintf(&sb, "$ORIGIN %s.\n", domain)
	fmt.Fprintf(&sb, "$TTL 3600\n")
	fmt.Fprintf(&sb, "@\tIN\tSOA\t%s.\t%s. (\n", ns[0], admin)
	fmt.Fprintf(&sb, "\t\t\t%s ; serial\n", serial)
	fmt.Fprintf(&sb, "\t\t\t7200 ; refresh\n\t\t\t3600 ; retry\n\t\t\t1209600 ; expire\n\t\t\t300 ; minimum\n\t\t)\n")
	for _, n := range ns {
		fmt.Fprintf(&sb, "@\tIN\tNS\t%s.\n", n)
	}
	for _, r := range records {
		name := r.Name
		if name == "@" {
			name = "@"
		}
		switch r.Type {
		case store.RecordMX, store.RecordSRV:
			fmt.Fprintf(&sb, "%s\tIN\t%s\t%d\t%s\n", name, r.Type, r.Priority, r.Content)
		default:
			fmt.Fprintf(&sb, "%s\tIN\t%s\t%s\n", name, r.Type, r.Content)
		}
	}
	path := filepath.Join(d.Cfg.DNSDir, domain+".zone")
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

// ValidateRecord does basic sanity checks before storing a DNS record.
func ValidateRecord(r *store.DNSRecord) error {
	if r.Name == "" {
		r.Name = "@"
	}
	r.Name = strings.ToLower(strings.TrimSuffix(r.Name, "."))
	r.Content = strings.TrimSpace(r.Content)
	switch r.Type {
	case store.RecordA:
		if !IsValidIPv4(r.Content) {
			return errors.New("A record requires an IPv4 address")
		}
	case store.RecordAAAA:
		if !strings.Contains(r.Content, ":") {
			return errors.New("AAAA record requires an IPv6 address")
		}
	case store.RecordCNAME:
		if r.Name == "@" {
			return errors.New("CNAME at the apex is invalid")
		}
		if IsValidIPv4(r.Content) || r.Content == "" {
			return errors.New("CNAME content must be a hostname")
		}
	case store.RecordMX:
		if r.Priority < 0 || r.Priority > 65535 {
			return errors.New("MX priority must be 0-65535")
		}
		if r.Content == "" {
			return errors.New("MX requires a mail host")
		}
	case store.RecordSRV:
		if r.Content == "" {
			return errors.New("SRV requires a target")
		}
	case store.RecordNS:
		if r.Content == "" {
			return errors.New("NS requires a name server")
		}
	case store.RecordTXT, store.RecordCAA:
	default:
		return fmt.Errorf("unsupported record type %q", r.Type)
	}
	if r.TTL <= 0 {
		r.TTL = 3600
	}
	if r.TTL > 86400*7 {
		return errors.New("TTL too large (max 7 days)")
	}
	return nil
}
