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

// WebApps is a generic, manifest-driven installer engine: adding a new app
// means adding an entry to Catalog (webapps_catalog.go), not new code here.
// Every app follows the same shape: fetch files (tarball or composer) ->
// provision a database if it needs one -> write a config file if it needs
// one -> run a non-interactive CLI install step if it has one -> chown to
// the domain's owner. This is the same "shell the real tool, chown
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

// FindApp looks up a catalog entry by id.
func FindApp(id string) (AppManifest, bool) {
	for _, m := range Catalog {
		if m.ID == id {
			return m, true
		}
	}
	return AppManifest{}, false
}

// InstallApp is the generic engine every catalog entry runs through.
//
// Apps whose actual webroot is a subdirectory of the project (Laravel's
// "public", Drupal/Craft's "web") install into a sibling "<domain>-app"
// directory and the visible document root becomes a symlink into that
// subdirectory — the vhost always just serves dom.DocumentRoot, so this is
// the only way to keep app code out of the web-reachable path without
// touching webserver.go's vhost generation per app.
func (w *WebApps) InstallApp(ctx context.Context, appID string, dom *store.Domain, owner *store.User) error {
	m, ok := FindApp(appID)
	if !ok {
		return fmt.Errorf("unknown app %q", appID)
	}
	if dom.DocumentRoot == "" {
		return errors.New("domain has no document root")
	}
	if !isEffectivelyEmpty(dom.DocumentRoot) {
		return errors.New("document root is not empty")
	}

	appDir := dom.DocumentRoot
	if m.PublicSubdir != "" {
		appDir = strings.TrimRight(dom.DocumentRoot, "/") + "-app"
		if err := os.RemoveAll(dom.DocumentRoot); err != nil {
			return err
		}
		if err := os.MkdirAll(appDir, 0o755); err != nil {
			return err
		}
	} else {
		if err := os.MkdirAll(dom.DocumentRoot, 0o755); err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(dom.DocumentRoot, "index.html"))
	}

	switch m.Strategy {
	case StrategyTarball:
		if err := downloadAndExtract(m.DownloadURL, m.ExtractSubdir, appDir); err != nil {
			return err
		}
	case StrategyComposer:
		if !LookPath("composer") {
			return errors.New("composer is not installed on this host")
		}
		if _, err := RunTimeout(5*time.Minute, "composer", "create-project", "--no-interaction",
			"--prefer-dist", m.ComposerPackage, appDir); err != nil {
			return fmt.Errorf("composer create-project: %w", err)
		}
	default:
		return fmt.Errorf("unknown install strategy %q", m.Strategy)
	}

	var db *store.Database
	if m.NeedsDatabase {
		dbName := m.ID + "_" + sanitizeDBIdent(dom.Domain)
		created, err := w.DB.Create(ctx, owner, m.DBEngine, dbName)
		if err != nil {
			return fmt.Errorf("create database: %w", err)
		}
		db = created
	}

	if m.ConfigFile != nil {
		path, content := m.ConfigFile(db, dom, appDir)
		if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
	}

	if m.Install != nil {
		if err := m.Install(ctx, w, dom, appDir, db); err != nil {
			return fmt.Errorf("install: %w", err)
		}
	}

	if m.PublicSubdir != "" {
		target := filepath.Join(appDir, m.PublicSubdir)
		if err := os.Symlink(target, dom.DocumentRoot); err != nil {
			return fmt.Errorf("link public dir: %w", err)
		}
		_, _ = RunTimeout(30*time.Second, "chown", "-h", owner.Username+":www-data", dom.DocumentRoot)
	}
	_, _ = RunTimeout(60*time.Second, "chown", "-R", owner.Username+":www-data", appDir)
	return nil
}

// isZipFile sniffs a file's magic bytes ("PK\x03\x04") rather than trusting
// its name/URL, since "latest" download aliases often redirect somewhere
// with no reliable extension.
func isZipFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, 4)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return false, err
	}
	return n >= 4 && buf[0] == 'P' && buf[1] == 'K' && buf[2] == 0x03 && buf[3] == 0x04, nil
}

// downloadAndExtract fetches a tarball/zip and copies its contents (or the
// named subdirectory within it, for archives like wordpress.org's that
// wrap everything in a top-level folder) into dest.
func downloadAndExtract(url, subdir, dest string) error {
	tmp, err := os.MkdirTemp("", "webapp-dl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	archive := filepath.Join(tmp, "app.download")
	if _, err := RunTimeout(3*time.Minute, "curl", "-fsSL", url, "-o", archive); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	// "latest" download aliases (Grav, Nextcloud, etc.) often redirect to a
	// URL with no reliable file extension, so detect the real archive type
	// from its magic bytes rather than guessing from the request URL.
	isZip, err := isZipFile(archive)
	if err != nil {
		return fmt.Errorf("inspect download: %w", err)
	}

	extractDir := filepath.Join(tmp, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
	}
	if isZip {
		if _, err := RunTimeout(2*time.Minute, "unzip", "-q", archive, "-d", extractDir); err != nil {
			return fmt.Errorf("extract: %w", err)
		}
	} else {
		// "tar xf" auto-detects gzip/bzip2/xz compression, no extra flag needed.
		if _, err := RunTimeout(2*time.Minute, "tar", "xf", archive, "-C", extractDir); err != nil {
			return fmt.Errorf("extract: %w", err)
		}
	}

	src := extractDir
	switch subdir {
	case "":
		// archive root
	case "*":
		// Auto-detect: many "latest" download URLs (no pinned version in the
		// filename) wrap everything in a single versioned folder whose exact
		// name can't be hardcoded without it going stale. If extraction
		// produced exactly one top-level entry and it's a directory, use it.
		entries, err := os.ReadDir(extractDir)
		if err == nil && len(entries) == 1 && entries[0].IsDir() {
			src = filepath.Join(extractDir, entries[0].Name())
		}
	default:
		src = filepath.Join(extractDir, subdir)
	}
	if _, err := RunTimeout(2*time.Minute, "cp", "-a", src+"/.", dest+"/"); err != nil {
		return fmt.Errorf("copy files: %w", err)
	}
	return nil
}

// writeCredentials drops a dotfile (blocked from HTTP access by the
// generated nginx vhost's `location ~ /\.` deny rule) with the app's admin
// login, so the domain owner can retrieve it after an install that creates
// its own admin account.
func writeCredentials(dir, app, user, pass string) error {
	content := fmt.Sprintf("%s admin credentials (generated at install time)\nusername: %s\npassword: %s\n", app, user, pass)
	return os.WriteFile(filepath.Join(dir, ".aegis-credentials.txt"), []byte(content), 0o600)
}

// RunWPCLI runs a WP-CLI command inside the domain's document root, args
// only — never a shell string — matching the exec pattern used everywhere
// else in svc.
func (w *WebApps) RunWPCLI(ctx context.Context, dom *store.Domain, args []string) (string, error) {
	if err := ensureWPCLI(); err != nil {
		return "", err
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
