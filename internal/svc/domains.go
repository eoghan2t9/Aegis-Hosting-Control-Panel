package svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
}

func NewDomains(cfg *config.Config, st *store.Store, web *WebServer, php *PHP) *Domains {
	return &Domains{Cfg: cfg, Store: st, Web: web, PHP: php}
}

// CreateOptions configures a new domain.
type CreateOptions struct {
	PHPVersion string `json:"php_version"`
	WebServer  string `json:"webserver"`
}

// UserHome returns the home directory for a panel user.
func (d *Domains) UserHome(user *store.User) string {
	if user.HomeDir != "" {
		return user.HomeDir
	}
	return filepath.Join(d.Cfg.HomeRoot, user.Username)
}

// DocumentRoot is where site files live for a domain.
func (d *Domains) DocumentRoot(user *store.User, domain string) string {
	return filepath.Join(d.UserHome(user), domain, "public")
}

// Create validates and provisions a domain for a user.
func (d *Domains) Create(ctx context.Context, user *store.User, domain string, opts CreateOptions) (*store.Domain, error) {
	domain = NormalizeDomain(domain)
	if !ValidDomain(domain) {
		return nil, errors.New("invalid domain name")
	}
	if _, err := d.Store.GetDomainByName(ctx, domain); err == nil {
		return nil, fmt.Errorf("domain %s already exists", domain)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	// Package quota.
	pkg, err := d.Store.GetPackage(ctx, user.PackageID)
	if err != nil {
		pkg, _ = d.Store.GetDefaultPackage(ctx)
	}
	if pkg != nil && pkg.MaxDomains > 0 {
		usage, err := d.Store.PackageUsage(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		if usage.Domains >= pkg.MaxDomains {
			return nil, fmt.Errorf("package %s allows at most %d domain(s)", pkg.Name, pkg.MaxDomains)
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
		return nil, fmt.Errorf("unsupported webserver %q", ws)
	}

	// PHP version validation.
	if opts.PHPVersion != "" && !d.PHP.Has(opts.PHPVersion) {
		return nil, fmt.Errorf("php %s is not installed", opts.PHPVersion)
	}

	// Create document root and seed a styled under-construction placeholder
	// (skipped when the docroot already has content — see placeholder.go).
	root := d.DocumentRoot(user, domain)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create docroot: %w", err)
	}
	if err := writePlaceholderPage(root, domain); err != nil {
		_ = os.Remove(root) // only succeeds when the dir we just made is empty
		return nil, fmt.Errorf("seed placeholder page: %w", err)
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
		return nil, err
	}

	// Provision php-fpm pool.
	if dom.PHPVersion != "" {
		if err := d.PHP.EnsurePool(domain, user.Username, dom.PHPVersion, nil); err != nil {
			_ = d.Store.DeleteDomain(ctx, dom.ID)
			return nil, err
		}
	}

	// Write web server config.
	if err := d.Web.Apply(dom, nil, user.Username); err != nil {
		_ = d.Store.DeleteDomain(ctx, dom.ID)
		return nil, err
	}

	return dom, nil
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
		if err := d.PHP.EnsurePool(dom.Domain, user.Username, dom.PHPVersion, nil); err != nil {
			return err
		}
	}
	aliases, _ := d.Store.ListAliases(ctx, dom.ID)
	return d.Web.Apply(dom, aliases, user.Username)
}

// Delete removes a domain's web config and php pool. Files on disk are kept
// (like cPanel); the caller decides whether to archive them first.
func (d *Domains) Delete(ctx context.Context, domainID int64) error {
	dom, err := d.Store.GetDomain(ctx, domainID)
	if err != nil {
		return err
	}
	_ = d.Web.Remove(dom)
	_ = d.PHP.RemovePool(dom.Domain, dom.PHPVersion)
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
