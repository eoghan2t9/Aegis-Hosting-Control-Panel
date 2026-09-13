// Package config loads, validates and persists the Aegis panel configuration.
//
// The panel is configured by a single JSON file (default /etc/aegis/config.json).
// All paths are overridable through environment variables prefixed with AEGIS_
// which makes the panel easy to relocate inside containers.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Default locations (Debian/Ubuntu style). Override via env or config file.
const (
	DefaultDir           = "/etc/aegis"
	DefaultDBPath        = "/var/lib/aegis/aegis.db"
	DefaultHomeRoot      = "/home"
	DefaultBackupDir     = "/var/backups/aegis"
	DefaultCertDir       = "/var/lib/aegis/certs"
	DefaultTunedDir      = "/etc/aegis/tuned"
	DefaultDNSDir        = "/var/lib/aegis/dns"
	DefaultSecretFile    = "/etc/aegis/secret.key"
	DefaultListenAddr    = ":8080"
	DefaultThumbCacheDir = "/var/cache/aegis/thumbs"
)

// Credentials for the database servers the panel manages. These are the
// *server administrator* credentials the panel uses to provision user
// databases. They are stored in the config file (mode 0600) and, in the
// development container, come from environment variables.
type MariaDBCreds struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

type PostgresCreds struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"` // superuser, e.g. postgres
	Password string `json:"password"`
	// SocketDir is used to connect over the local unix socket as the
	// postgres system user when peer auth applies (bare metal).
	SocketDir string `json:"socket_dir"`
}

type WebServerConfig struct {
	// Server is the active web server: nginx | apache | caddy | go
	Server string `json:"server"`
	// NginxDir / ApacheDir / CaddyFile: config paths. Auto-detected when empty.
	NginxDir  string `json:"nginx_dir"`
	ApacheDir string `json:"apache_dir"`
	CaddyFile string `json:"caddy_file"`
	// PHPFpmSocketDir is where php-fpm pools listen (e.g. /run/php).
	PHPFpmSocketDir string `json:"php_fpm_socket_dir"`
	// ApacheListenPort: Apache is often moved off 80/443 when nginx owns them.
	ApacheListenPort int `json:"apache_listen_port"`
}

type DNSConfig struct {
	// ListenAddr for the built-in authoritative DNS server. Empty disables it.
	ListenAddr string `json:"listen_addr"`
	// BindZoneDir: zone files are written here for bind/named consumption.
	BindZoneDir string `json:"bind_zone_dir"`
	// Nameservers advertised in SOA/NS records (e.g. ns1.example.com).
	Nameservers []string `json:"nameservers"`
	// AdminEmail for SOA records.
	AdminEmail string `json:"admin_email"`
}

type Config struct {
	Dir        string `json:"dir"`
	DBPath     string `json:"db_path"`
	HomeRoot   string `json:"home_root"`
	BackupDir  string `json:"backup_dir"`
	CertDir    string `json:"cert_dir"`
	TunedDir   string `json:"tuned_dir"`
	DNSDir     string `json:"dns_dir"`
	SecretFile string `json:"secret_file"`
	ListenAddr string `json:"listen_addr"`
	// PanelBase is the URL path prefix the panel is served under (default
	// "/aegis"), so the panel is reachable as http://host/aegis without a
	// dedicated port. Empty (AEGIS_PANEL_BASE="-") serves from the root.
	// Web-server vhosts proxy this prefix to the panel listener.
	PanelBase string `json:"panel_base"`
	// ThumbCacheDir stores generated file-manager thumbnails (images, video
	// frames, PDF first pages), keyed by content so edits auto-invalidate.
	ThumbCacheDir string `json:"thumb_cache_dir"`

	// PublicHost is the hostname the panel is reachable at (used in UI/links).
	PublicHost string `json:"public_host"`

	// JWTSecret is derived from the secret file and never persisted in JSON.
	JWTSecret string `json:"-"`
	// ConfigPath records where the config was loaded from / saved to.
	ConfigPath string `json:"-"`

	SessionTTLHours int             `json:"session_ttl_hours"`
	MariaDB         MariaDBCreds    `json:"mariadb"`
	PostgreSQL      PostgresCreds   `json:"postgresql"`
	WebServer       WebServerConfig `json:"webserver"`
	DNS             DNSConfig       `json:"dns"`

	// LetsEncryptStaging uses the ACME staging endpoint (useful in dev).
	LetsEncryptStaging bool `json:"letsencrypt_staging"`
}

// Default returns a Config with sane defaults. Detection/tuning later refines
// the web server and resource settings.
func Default() *Config {
	return &Config{
		Dir:             envOr("AEGIS_DIR", DefaultDir),
		DBPath:          envOr("AEGIS_DB_PATH", DefaultDBPath),
		HomeRoot:        envOr("AEGIS_HOME_ROOT", DefaultHomeRoot),
		BackupDir:       envOr("AEGIS_BACKUP_DIR", DefaultBackupDir),
		CertDir:         envOr("AEGIS_CERT_DIR", DefaultCertDir),
		TunedDir:        envOr("AEGIS_TUNED_DIR", DefaultTunedDir),
		DNSDir:          envOr("AEGIS_DNS_DIR", DefaultDNSDir),
		SecretFile:      envOr("AEGIS_SECRET_FILE", DefaultSecretFile),
		ListenAddr:      envOr("AEGIS_LISTEN", DefaultListenAddr),
		PanelBase:       normalizePanelBase(envOr("AEGIS_PANEL_BASE", "/aegis")),
		ThumbCacheDir:   envOr("AEGIS_THUMB_CACHE_DIR", DefaultThumbCacheDir),
		PublicHost:      envOr("AEGIS_PUBLIC_HOST", ""),
		SessionTTLHours: 24,
		MariaDB: MariaDBCreds{
			Host:     envOr("AEGIS_MARIADB_HOST", "127.0.0.1"),
			Port:     3306,
			User:     "root",
			Password: os.Getenv("AEGIS_MARIADB_PASSWORD"),
		},
		PostgreSQL: PostgresCreds{
			Host:      envOr("AEGIS_POSTGRES_HOST", "127.0.0.1"),
			Port:      5432,
			User:      "postgres",
			Password:  os.Getenv("AEGIS_POSTGRES_PASSWORD"),
			SocketDir: "/var/run/postgresql",
		},
		WebServer: WebServerConfig{
			Server:           envOr("AEGIS_WEBSERVER", "nginx"),
			PHPFpmSocketDir:  envOr("AEGIS_PHP_SOCKET_DIR", "/run/php"),
			ApacheListenPort: intEnvOr("AEGIS_APACHE_LISTEN", 80),
		},
		DNS: DNSConfig{
			ListenAddr:  envOr("AEGIS_DNS_LISTEN", ""),
			BindZoneDir: envOr("AEGIS_DNS_BIND_DIR", "/var/lib/bind"),
			Nameservers: []string{"ns1.example.com", "ns2.example.com"},
			AdminEmail:  "hostmaster.example.com",
		},
	}
}

// Load reads the config file at path (or AEGIS_CONFIG), applying defaults and
// environment overrides. A missing file returns the defaults without error so
// first-run setup can proceed.
func Load(path string) (*Config, error) {
	if path == "" {
		path = envOr("AEGIS_CONFIG", filepath.Join(DefaultDir, "config.json"))
	}
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.ConfigPath = path
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.ConfigPath = path
	cfg.applyEnvOverrides()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ConfigPath records where the config was loaded from / will be saved to.
// (Kept in a separate struct field to avoid persisting it into JSON.)
func (c *Config) Save() error {
	c.ConfigPath = envOr("AEGIS_CONFIG", filepath.Join(c.Dir, "config.json"))
	if err := os.MkdirAll(filepath.Dir(c.ConfigPath), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.ConfigPath, data, 0o600); err != nil {
		return err
	}
	return nil
}

// applyEnvOverrides re-applies environment overrides on top of a loaded file.
func (c *Config) applyEnvOverrides() {
	c.ConfigPath = envOr("AEGIS_CONFIG", filepath.Join(c.Dir, "config.json"))
}

func (c *Config) Validate() error {
	if c.Dir == "" || c.DBPath == "" || c.HomeRoot == "" {
		return errors.New("config: dir, db_path and home_root must be set")
	}
	if c.SessionTTLHours <= 0 {
		c.SessionTTLHours = 24
	}
	c.PanelBase = normalizePanelBase(c.PanelBase)
	switch c.WebServer.Server {
	case "nginx", "apache", "caddy", "go":
	default:
		return fmt.Errorf("config: unsupported webserver %q (nginx|apache|caddy|go)", c.WebServer.Server)
	}
	return nil
}

// normalizePanelBase accepts "/aegis", "aegis", "/" (root) and "-" (root) and
// returns "" for root serving or "/aegis"-style with no trailing slash.
func normalizePanelBase(b string) string {
	b = strings.TrimSpace(b)
	if b == "" || b == "/" || b == "-" {
		return ""
	}
	if !strings.HasPrefix(b, "/") {
		b = "/" + b
	}
	return strings.TrimSuffix(b, "/")
}

// PanelUpstream is the host:port the web-server vhosts proxy the panel to —
// the panel listener, forced onto the loopback side (vhosts run on the same
// host; the panel should never be proxied over an external interface).
func (c *Config) PanelUpstream() string {
	a := c.ListenAddr
	switch {
	case strings.HasPrefix(a, ":"):
		return "127.0.0.1" + a
	case strings.HasPrefix(a, "0.0.0.0:"):
		return "127.0.0.1" + strings.TrimPrefix(a, "0.0.0.0")
	default:
		return a
	}
}

// EnsureDirs creates every directory the panel needs and generates the secret
// key file when missing.
func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.Dir, filepath.Dir(c.DBPath), c.HomeRoot, c.BackupDir, c.CertDir, c.TunedDir, c.DNSDir, c.ThumbCacheDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

// EnsureSecret generates (or loads) a 32-byte secret used to derive the JWT
// signing key and encrypt API tokens at rest. File perms are 0600.
func (c *Config) EnsureSecret() error {
	if c.JWTSecret != "" {
		return nil
	}
	if data, err := os.ReadFile(c.SecretFile); err == nil && len(data) >= 32 {
		c.JWTSecret = strings.TrimSpace(string(data))
		return nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("secret: %w", err)
	}
	c.JWTSecret = hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(c.SecretFile), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(c.SecretFile, []byte(c.JWTSecret), 0o600); err != nil {
		return fmt.Errorf("write secret: %w", err)
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intEnvOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
