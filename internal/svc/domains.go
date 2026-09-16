package svc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Domains orchestrates the full domain lifecycle: quota checks, document
// roots, php-fpm pools, web server config and DNS zone creation.
type Domains struct {
	Cfg   *config.Config
	Store *store.Store
	Web   *WebServer
	PHP   *PHP
	DNS   *DNS
	FTP   *FTP
}

func NewDomains(cfg *config.Config, st *store.Store, web *WebServer, php *PHP, dns *DNS, ftp *FTP) *Domains {
	return &Domains{Cfg: cfg, Store: st, Web: web, PHP: php, DNS: dns, FTP: ftp}
}

// ProvisionedFTP is the one-time result of an auto-created FTP account —
// like APITokens.Create's raw token, the plaintext password only ever
// exists in this return value; the store only ever gets a bcrypt hash of it.
type ProvisionedFTP struct {
	Account  *store.FTPAccount
	Password string
	// WebFTPURL is set only when a "webftp.<domain>" browser client was also
	// auto-created for this account (see createWebftpDomain) — empty when
	// that step was skipped or failed.
	WebFTPURL string
}

// CreateOptions configures a new domain.
type CreateOptions struct {
	PHPVersion string `json:"php_version"`
	WebServer  string `json:"webserver"`
	// RelPath optionally overrides where the document root is created,
	// relative to the user's home directory (e.g. "example.com/sub" to
	// nest a subdomain inside the master domain's folder, or
	// "shop.example.com" for its own top-level folder). Empty uses the
	// default <domain>/public. Must stay inside the home directory.
	RelPath string `json:"rel_root"`
}

// UserHome returns the home directory for a panel user.
func (d *Domains) UserHome(user *store.User) string {
	if user.HomeDir != "" {
		return user.HomeDir
	}
	return filepath.Join(d.Cfg.HomeRoot, user.Username)
}

// DocumentRoot is where site files live for a domain (default layout).
func (d *Domains) DocumentRoot(user *store.User, domain string) string {
	return filepath.Join(d.UserHome(user), domain, "public")
}

// ResolveDocRoot turns a user-supplied relative docroot into an absolute
// path inside the user's home directory, rejecting anything that escapes
// it: absolute paths, ".." segments, traversal via symlinks, and the home
// directory itself (a docroot at ~ would expose every other domain's
// files, and the placeholder writer would race the home dir).
func (d *Domains) ResolveDocRoot(user *store.User, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("relative document root is empty")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("document root must be relative to your home directory")
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || clean == "/" || strings.HasPrefix(clean, "../") || clean == ".." || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("document root cannot contain '..'")
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == "" || seg == "." {
			return "", fmt.Errorf("document root has an invalid path segment")
		}
		// "~/..." means home to a shell; here it is already home-relative,
		// so a literal tilde folder is almost certainly a mistake.
		if seg == "~" || strings.HasPrefix(seg, "~") {
			return "", fmt.Errorf("document root cannot start a segment with '~'")
		}
		// Whitespace would break the unquoted root directives in generated
		// nginx/apache vhosts.
		if strings.ContainsFunc(seg, func(r rune) bool { return r == ' ' || r == '\t' }) {
			return "", fmt.Errorf("document root segment %q must not contain spaces", seg)
		}
		// Keep names filesystem-safe and hidden files out of the layout.
		if seg[0] == '.' || strings.ContainsAny(seg, `\:*?"<>|`) {
			return "", fmt.Errorf("document root segment %q is not a valid folder name", seg)
		}
	}
	abs := filepath.Join(d.UserHome(user), filepath.FromSlash(clean))
	// Symlink escape check: every component created/verified below home.
	probe := d.UserHome(user)
	for _, seg := range strings.Split(filepath.FromSlash(clean), "/") {
		probe = filepath.Join(probe, seg)
		if st, err := os.Lstat(probe); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("document root path %q is a symlink", seg)
		}
	}
	if abs == filepath.Clean(d.UserHome(user)) {
		return "", fmt.Errorf("document root cannot be the home directory itself")
	}
	return abs, nil
}

// docrootFor picks the document root for a new domain: the caller's
// relative path when given (validated to stay inside the home directory),
// else the default <domain>/public layout.
func (d *Domains) docrootFor(user *store.User, domain string, opts CreateOptions) (string, error) {
	if strings.TrimSpace(opts.RelPath) != "" {
		return d.ResolveDocRoot(user, opts.RelPath)
	}
	return d.DocumentRoot(user, domain), nil
}

// Create validates and provisions a domain for a user. The second return
// value is non-nil only when a dedicated FTP account was auto-created for
// the domain (see createDefaultFTP) — its plaintext password exists only
// there, so the caller must surface it to the admin immediately or it's
// gone (like an API token's raw value).
func (d *Domains) Create(ctx context.Context, user *store.User, domain string, opts CreateOptions) (*store.Domain, *ProvisionedFTP, error) {
	domain = NormalizeDomain(domain)
	if !ValidDomain(domain) {
		return nil, nil, errors.New("invalid domain name")
	}
	if _, err := d.Store.GetDomainByName(ctx, domain); err == nil {
		return nil, nil, fmt.Errorf("domain %s already exists", domain)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, nil, err
	}

	// Package quota.
	pkg, err := d.Store.GetPackage(ctx, user.PackageID)
	if err != nil {
		pkg, _ = d.Store.GetDefaultPackage(ctx)
	}
	if pkg != nil && pkg.MaxDomains > 0 {
		usage, err := d.Store.PackageUsage(ctx, user.ID)
		if err != nil {
			return nil, nil, err
		}
		if usage.Domains >= pkg.MaxDomains {
			return nil, nil, fmt.Errorf("package %s allows at most %d domain(s)", pkg.Name, pkg.MaxDomains)
		}
	}

	// Ensure the web server choice is sane.
	ws := opts.WebServer
	if ws == "" {
		ws = d.Cfg.WebServer.Server
	}
	switch ws {
	case "nginx", "apache", "caddy", "go":
	default:
		return nil, nil, fmt.Errorf("unsupported webserver %q", ws)
	}

	// PHP version validation.
	if opts.PHPVersion != "" && !d.PHP.Has(opts.PHPVersion) {
		return nil, nil, fmt.Errorf("php %s is not installed", opts.PHPVersion)
	}

	// Create document root and seed a styled under-construction placeholder
	// (skipped when the docroot already has content — see placeholder.go).
	root, err := d.docrootFor(user, domain, opts)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create docroot: %w", err)
	}
	if err := writePlaceholderPage(root, domain); err != nil {
		_ = os.Remove(root) // only succeeds when the dir we just made is empty
		return nil, nil, fmt.Errorf("seed placeholder page: %w", err)
	}

	dom := &store.Domain{
		UserID:       user.ID,
		Domain:       domain,
		DocumentRoot: root,
		PHPVersion:   opts.PHPVersion,
		WebServer:    ws,
		SSLAutoRenew: true,
	}
	if err := d.Store.CreateDomain(ctx, dom); err != nil {
		return nil, nil, err
	}

	// Provision php-fpm pool.
	if dom.PHPVersion != "" {
		if err := d.PHP.EnsurePool(domain, user.Username, dom.PHPVersion, nil, dom.PHPSettings); err != nil {
			_ = d.Store.DeleteDomain(ctx, dom.ID)
			return nil, nil, err
		}
	}

	// Write web server config.
	if err := d.Web.Apply(dom, nil, user.Username); err != nil {
		_ = d.Store.DeleteDomain(ctx, dom.ID)
		return nil, nil, err
	}

	// Auto-provision a DNS zone with default records, mirroring the manual
	// "Add zone" flow (handleZonesCreate). Best-effort: a domain is fully
	// usable without DNS (an admin can point records at it externally, or
	// add a zone later from the DNS tab), so a failure here doesn't roll
	// back the domain the way a PHP/web-config failure does above.
	if d.DNS != nil && (pkg == nil || pkg.AllowDNS) {
		if err := d.createDefaultZone(ctx, dom); err != nil {
			slog.Warn("dns: auto zone-create failed", "domain", dom.Domain, "err", err)
		}
	}

	// Auto-provision a dedicated FTP account chrooted to this domain, same
	// best-effort treatment as DNS above.
	var ftpResult *ProvisionedFTP
	if d.FTP != nil && (pkg == nil || pkg.AllowFTP) {
		acct, password, err := d.createDefaultFTP(ctx, user, dom)
		if err != nil {
			slog.Warn("ftp: auto account-create failed", "domain", dom.Domain, "err", err)
		} else {
			ftpResult = &ProvisionedFTP{Account: acct, Password: password}
			// Auto-provision a "webftp.<domain>" vhost giving browser access
			// to that same account — best-effort and only attempted once the
			// FTP account it depends on actually exists.
			if err := d.createWebftpDomain(ctx, user, dom); err != nil {
				slog.Warn("webftp: auto subdomain-create failed", "domain", dom.Domain, "err", err)
			} else {
				ftpResult.WebFTPURL = "http://" + WebftpHostname(dom.Domain) + "/"
			}
		}
	}

	return dom, ftpResult, nil
}

// WebftpHostname returns the hostname of a domain's auto-created web-based
// FTP client (see createWebftpDomain) — a package-level helper so the API
// layer's domain-list filter (hiding this system-generated row from the
// owner-facing list) and PackageUsage's quota count (excluding it) can both
// recognize it by the same convention without a schema flag.
func WebftpHostname(domain string) string { return "webftp." + domain }

// createWebftpDomain provisions a hidden vhost at "webftp.<domain>" that
// reverse-proxies every request to the shared WebFTPAddr server (started
// once at boot — see cmd/aegis/main.go and svc.WebFTP), giving browser-based
// access to the FTP account createDefaultFTP just created. It's a real
// store.Domain row — the only routing mechanism in this codebase proven to
// work across nginx/apache/caddy/the native Go server (svc.Docker uses the
// identical ProxyTarget trick to attach a container to a domain) — but it's
// recognized as system-generated purely by its "webftp." name prefix, not a
// schema flag, so it's hidden from handleDomainsList and PackageUsage's
// domain-quota count.
func (d *Domains) createWebftpDomain(ctx context.Context, owner *store.User, dom *store.Domain) error {
	name := WebftpHostname(dom.Domain)
	if _, err := d.Store.GetDomainByName(ctx, name); err == nil {
		return nil // already provisioned (e.g. re-running this step)
	}
	// Its DocumentRoot is never served (ProxyTarget always wins) — just a
	// harmless placeholder directory kept for bookkeeping consistency with
	// every other domain row.
	root := filepath.Join(filepath.Dir(dom.DocumentRoot), ".webftp")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create webftp placeholder dir: %w", err)
	}
	proxy := &store.Domain{
		UserID:       owner.ID,
		Domain:       name,
		DocumentRoot: root,
		WebServer:    dom.WebServer,
		ProxyTarget:  WebFTPAddr,
		SSLAutoRenew: true,
	}
	if err := d.Store.CreateDomain(ctx, proxy); err != nil {
		return fmt.Errorf("create webftp domain row: %w", err)
	}
	if err := d.Web.Apply(proxy, nil, owner.Username); err != nil {
		_ = d.Store.DeleteDomain(ctx, proxy.ID)
		return fmt.Errorf("apply webftp vhost: %w", err)
	}
	// Add the "webftp" A record alongside the domain's existing DNS zone
	// (not a separate zone) — best-effort, same as createDefaultZone.
	if zone, err := d.Store.GetZoneByDomain(ctx, dom.ID); err == nil {
		if ip := DetectPrimaryIP(); ip != "" {
			rec := &store.DNSRecord{ZoneID: zone.ID, Name: "webftp", Type: store.RecordA, TTL: 3600, Content: ip}
			if verr := ValidateRecord(rec); verr == nil {
				_ = d.Store.CreateRecord(ctx, rec)
			}
			if d.DNS != nil {
				_, _ = d.DNS.Sync(ctx, zone.ID)
			}
		}
	}
	return nil
}

// createDefaultFTP provisions a dedicated FTP account chrooted directly to
// this domain's document root (not the owner's whole home), so access to
// one site can be handed off — to a client, a webmaster, a deploy script —
// without exposing every other domain the owner has. Returns the account
// and its one-time plaintext password.
func (d *Domains) createDefaultFTP(ctx context.Context, owner *store.User, dom *store.Domain) (*store.FTPAccount, string, error) {
	username, err := d.uniqueFTPUsername(ctx, dom.Domain)
	if err != nil {
		return nil, "", fmt.Errorf("pick ftp username: %w", err)
	}
	password, err := RandomPassword()
	if err != nil {
		return nil, "", fmt.Errorf("generate password: %w", err)
	}
	acct, err := d.FTP.CreateScoped(ctx, owner, username, password, dom.DocumentRoot)
	if err != nil {
		return nil, "", err
	}
	return acct, password, nil
}

// sanitizeFTPUsername turns a domain name into a valid FTP/system username
// (see ValidUsername: lowercase letters/digits/underscore, must start with a
// letter, 3-30 chars) by replacing every other character with '_' and
// leaving room for uniqueFTPUsername to append a numeric suffix.
func sanitizeFTPUsername(domain string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(domain) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := strings.Trim(b.String(), "_")
	if s == "" {
		s = "site"
	}
	if s[0] < 'a' || s[0] > 'z' {
		s = "f" + s
	}
	const maxBase = 24 // leaves room for a numeric collision suffix, capped at 30 total (ValidUsername)
	if len(s) > maxBase {
		s = strings.TrimRight(s[:maxBase], "_")
	}
	for len(s) < 3 {
		s += "0"
	}
	return s
}

// uniqueFTPUsername finds a free FTP/system username derived from domain,
// appending a numeric suffix on collision — FTP usernames are unique across
// the whole panel (they're also real system usernames), not just per owner.
func (d *Domains) uniqueFTPUsername(ctx context.Context, domain string) (string, error) {
	base := sanitizeFTPUsername(domain)
	if _, err := d.Store.GetFTPAccountByUsername(ctx, base); errors.Is(err, store.ErrNotFound) {
		return base, nil
	} else if err != nil {
		return "", err
	}
	for i := 2; i <= 50; i++ {
		suffix := fmt.Sprintf("%d", i)
		candidate := base
		if maxLen := 30 - len(suffix); len(candidate) > maxLen {
			candidate = candidate[:maxLen]
		}
		candidate += suffix
		if _, err := d.Store.GetFTPAccountByUsername(ctx, candidate); errors.Is(err, store.ErrNotFound) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("could not find a free ftp username")
}

// createDefaultZone seeds a new domain with a "local" DNS zone and the usual
// starter records an admin would otherwise add by hand from the DNS tab: an
// apex A record at the server's primary IP, a "www" CNAME back to the apex,
// an MX record so mail addressed to the domain lands on this server, and an
// SPF TXT authorizing that same MX. The SPF content matches what
// Mail.EnableDomain publishes (see mail.go) so later enabling mail just adds
// DKIM/DMARC alongside it instead of conflicting.
func (d *Domains) createDefaultZone(ctx context.Context, dom *store.Domain) error {
	zone := &store.DNSZone{DomainID: dom.ID, Provider: "local"}
	if err := d.Store.CreateZone(ctx, zone); err != nil {
		return fmt.Errorf("create zone: %w", err)
	}
	if ip := DetectPrimaryIP(); ip != "" {
		records := []*store.DNSRecord{
			{ZoneID: zone.ID, Name: "@", Type: store.RecordA, TTL: 3600, Content: ip},
			{ZoneID: zone.ID, Name: "www", Type: store.RecordCNAME, TTL: 3600, Content: dom.Domain},
			{ZoneID: zone.ID, Name: "@", Type: store.RecordMX, TTL: 3600, Priority: 10, Content: dom.Domain},
			{ZoneID: zone.ID, Name: "@", Type: store.RecordTXT, TTL: 3600, Content: "v=spf1 mx ~all"},
		}
		for _, rec := range records {
			if err := ValidateRecord(rec); err == nil {
				_ = d.Store.CreateRecord(ctx, rec)
			}
		}
	}
	_, err := d.DNS.Sync(ctx, zone.ID)
	return err
}

// Apply re-provisions the php pool and web config for an existing domain
// after its settings changed (e.g. PHP version or SSL toggled).
func (d *Domains) Apply(ctx context.Context, domainID int64) error {
	dom, err := d.Store.GetDomain(ctx, domainID)
	if err != nil {
		return err
	}
	user, err := d.Store.GetUserByID(ctx, dom.UserID)
	if err != nil {
		return err
	}
	if dom.PHPVersion != "" {
		if err := d.PHP.EnsurePool(dom.Domain, user.Username, dom.PHPVersion, nil, dom.PHPSettings); err != nil {
			return err
		}
	}
	aliases, _ := d.Store.ListAliases(ctx, dom.ID)
	return d.Web.Apply(dom, aliases, user.Username)
}

// Delete removes a domain's web config and php pool. Files on disk are kept
// (like cPanel); the caller decides whether to archive them first. Any
// auto-created webftp vhost and dedicated FTP account for this domain (see
// createWebftpDomain, createDefaultFTP) are torn down too, best-effort —
// otherwise they'd dangle: a live login still chrooted into a document root
// that no longer belongs to any domain.
func (d *Domains) Delete(ctx context.Context, domainID int64) error {
	dom, err := d.Store.GetDomain(ctx, domainID)
	if err != nil {
		return err
	}
	_ = d.Web.Remove(dom)
	_ = d.PHP.RemovePool(dom.Domain, dom.PHPVersion)
	if wf, err := d.Store.GetDomainByName(ctx, WebftpHostname(dom.Domain)); err == nil {
		_ = d.Web.Remove(wf)
		_ = d.Store.DeleteDomain(ctx, wf.ID)
	}
	if d.FTP != nil {
		if accts, err := d.Store.ListFTPAccounts(ctx, dom.UserID); err == nil {
			for _, a := range accts {
				if a.HomeDir == dom.DocumentRoot {
					_ = d.FTP.Delete(ctx, a)
				}
			}
		}
	}
	return d.Store.DeleteDomain(ctx, domainID)
}

// DomainWithUser joins a domain with its owner (API convenience).
func (d *Domains) DomainWithUser(ctx context.Context, domainID int64) (*store.Domain, *store.User, error) {
	dom, err := d.Store.GetDomain(ctx, domainID)
	if err != nil {
		return nil, nil, err
	}
	user, err := d.Store.GetUserByID(ctx, dom.UserID)
	if err != nil {
		return nil, nil, err
	}
	return dom, user, nil
}
