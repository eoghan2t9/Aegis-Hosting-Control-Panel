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

// WebApps provides one-click installers (WordPress, Laravel) and a WP-CLI
// helper, following the same "shell the real tool, chown to the owner
// afterward" pattern as the rest of svc.
type WebApps struct {
	Cfg *config.Config
	DB  *Databases
}

func NewWebApps(cfg *config.Config, db *Databases) *WebApps {
	return &WebApps{Cfg: cfg, DB: db}
}

// isEffectivelyEmpty reports whether a document root has nothing but the
// placeholder index.html domain creation seeds — anything else (real
// content, or a directory that doesn't exist yet) is fine to install into.
func isEffectivelyEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true // doesn't exist yet — nothing to clobber
	}
	for _, e := range entries {
		if e.Name() != "index.html" {
			return false
		}
	}
	return true
}

const wpCLIPath = "/usr/local/share/aegis/wp-cli.phar"

func ensureWPCLI() error {
	if _, err := os.Stat(wpCLIPath); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(wpCLIPath), 0o755); err != nil {
		return err
	}
	_, err := RunTimeout(60*time.Second, "curl", "-fsSL",
		"https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar",
		"-o", wpCLIPath)
	if err != nil {
		return fmt.Errorf("download wp-cli: %w", err)
	}
	return os.Chmod(wpCLIPath, 0o755)
}

// InstallWordPress downloads the official tarball, extracts it into the
// domain's document root, provisions a database via the existing Databases
// service, and writes wp-config.php with those generated credentials.
func (w *WebApps) InstallWordPress(ctx context.Context, dom *store.Domain, owner *store.User) error {
	if dom.DocumentRoot == "" {
		return errors.New("domain has no document root")
	}
	if !isEffectivelyEmpty(dom.DocumentRoot) {
		return errors.New("document root is not empty")
	}
	if err := os.MkdirAll(dom.DocumentRoot, 0o755); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(dom.DocumentRoot, "index.html"))

	tmp, err := os.MkdirTemp("", "wp-download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	archive := filepath.Join(tmp, "wordpress.tar.gz")
	if _, err := RunTimeout(2*time.Minute, "curl", "-fsSL", "https://wordpress.org/latest.tar.gz", "-o", archive); err != nil {
		return fmt.Errorf("download wordpress: %w", err)
	}
	if _, err := RunTimeout(2*time.Minute, "tar", "xzf", archive, "-C", tmp); err != nil {
		return fmt.Errorf("extract wordpress: %w", err)
	}
	// wordpress.org's tarball always unpacks into a top-level "wordpress/" dir.
	if _, err := RunTimeout(2*time.Minute, "sh", "-c", fmt.Sprintf("cp -a %s/wordpress/. %s/", tmp, dom.DocumentRoot)); err != nil {
		return fmt.Errorf("copy wordpress files: %w", err)
	}

	dbName := "wp_" + sanitizeDBIdent(dom.Domain)
	db, err := w.DB.Create(ctx, owner, "mariadb", dbName)
	if err != nil {
		return fmt.Errorf("create database: %w", err)
	}

	authKeys, err := fetchWPSalts(ctx)
	if err != nil {
		authKeys = "// could not fetch unique keys from the WordPress salt API; set these manually for production."
	}
	cfg := fmt.Sprintf(`<?php
define('DB_NAME', %q);
define('DB_USER', %q);
define('DB_PASSWORD', %q);
define('DB_HOST', 'localhost');
define('DB_CHARSET', 'utf8mb4');
define('DB_COLLATE', '');
%s
$table_prefix = 'wp_';
define('WP_DEBUG', false);
if (!defined('ABSPATH')) define('ABSPATH', __DIR__ . '/');
require_once ABSPATH . 'wp-settings.php';
`, db.Name, db.DBUser, db.DBPassword, authKeys)
	if err := os.WriteFile(filepath.Join(dom.DocumentRoot, "wp-config.php"), []byte(cfg), 0o640); err != nil {
		return err
	}

	_, _ = RunTimeout(30*time.Second, "chown", "-R", owner.Username+":www-data", dom.DocumentRoot)
	return nil
}

func fetchWPSalts(ctx context.Context) (string, error) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := Exec(c, "curl", "-fsSL", "https://api.wordpress.org/secret-key/1.1/salt/")
	if err != nil || out == "" {
		return "", errors.New("salt api unavailable")
	}
	return out, nil
}

// InstallLaravel scaffolds a fresh Laravel app via composer create-project.
func (w *WebApps) InstallLaravel(ctx context.Context, dom *store.Domain, owner *store.User) error {
	if dom.DocumentRoot == "" {
		return errors.New("domain has no document root")
	}
	if !isEffectivelyEmpty(dom.DocumentRoot) {
		return errors.New("document root is not empty")
	}
	_ = os.Remove(filepath.Join(dom.DocumentRoot, "index.html"))
	if !LookPath("composer") {
		return errors.New("composer is not installed on this host")
	}
	parent := filepath.Dir(dom.DocumentRoot)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	_, err := RunTimeout(5*time.Minute, "composer", "create-project", "--no-interaction",
		"laravel/laravel", dom.DocumentRoot)
	if err != nil {
		return fmt.Errorf("composer create-project: %w", err)
	}
	_, _ = RunTimeout(30*time.Second, "chown", "-R", owner.Username+":www-data", dom.DocumentRoot)
	return nil
}

// RunWPCLI runs a WP-CLI command inside the domain's document root, args
// only — never a shell string — matching the exec pattern used everywhere
// else in svc.
func (w *WebApps) RunWPCLI(ctx context.Context, dom *store.Domain, args []string) (string, error) {
	if err := ensureWPCLI(); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dom.DocumentRoot, "wp-config.php")); err != nil {
		return "", errors.New("WordPress is not installed for this domain")
	}
	full := append([]string{wpCLIPath, "--path=" + dom.DocumentRoot, "--allow-root"}, args...)
	c, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return Exec(c, "php", full...)
}

func sanitizeDBIdent(domain string) string {
	s := strings.ToLower(domain)
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	out := sb.String()
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}
