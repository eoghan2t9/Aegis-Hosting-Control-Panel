package svc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aegis/internal/config"
	"aegis/internal/store"
)

// WebServer generates and applies site configurations across supported web
// servers: nginx, Apache, Caddy and the panel's native Go server. The Go
// server registers in-memory routes (GoRoute) and serves static files with
// FastCGI passthrough to php-fpm.
type WebServer struct {
	Cfg *config.Config
	PHP *PHP

	mu       sync.Mutex
	goRoutes map[string]GoRoute
}

// GoRoute is how a domain is served by the native Go web server.
type GoRoute struct {
	Domain     string   `json:"domain"`
	Root       string   `json:"root"`
	PHPVersion string   `json:"php_version"`
	Socket     string   `json:"socket"`
	SSL        bool     `json:"ssl"`
	Cert       string   `json:"cert"`
	Key        string   `json:"key"`
	Hostnames  []string `json:"hostnames"`
}

func NewWebServer(cfg *config.Config, php *PHP) *WebServer {
	return &WebServer{Cfg: cfg, PHP: php, goRoutes: map[string]GoRoute{}}
}

// Active returns the configured web server.
func (w *WebServer) Active() string { return w.Cfg.WebServer.Server }

// Available lists installed web servers.
func (w *WebServer) Available() []string {
	avail := []string{"go"} // the native server is always available
	for _, s := range DetectWebServers() {
		switch s {
		case "nginx":
			avail = append(avail, "nginx")
		case "apache2":
			avail = append(avail, "apache")
		case "caddy":
			avail = append(avail, "caddy")
		}
	}
	return avail
}

// Generate renders the config for a domain on the active web server.
func (w *WebServer) Generate(d *store.Domain, aliases []string, systemUser string) (string, error) {
	switch w.Active() {
	case "nginx":
		return w.generateNginx(d, aliases, systemUser), nil
	case "apache":
		return w.generateApache(d, aliases, systemUser), nil
	case "caddy":
		return w.generateCaddy(d, aliases, systemUser), nil
	case "go":
		return w.generateGo(d, aliases, systemUser), nil
	default:
		return "", fmt.Errorf("webserver: unsupported server %q", w.Active())
	}
}

// Apply writes the config for a domain and reloads the active server.
// Idempotent: re-running regenerates the same files.
func (w *WebServer) Apply(d *store.Domain, aliases []string, systemUser string) error {
	switch w.Active() {
	case "nginx":
		return w.applyNginx(d, aliases, systemUser)
	case "apache":
		return w.applyApache(d, aliases, systemUser)
	case "caddy":
		return w.applyCaddy(d, aliases, systemUser)
	case "go":
		return w.applyGo(d, aliases, systemUser)
	default:
		return fmt.Errorf("webserver: unsupported server %q", w.Active())
	}
}

// Remove deletes the config for a domain on the active server.
func (w *WebServer) Remove(d *store.Domain) error {
	switch w.Active() {
	case "nginx":
		return w.removeNginx(d.Domain)
	case "apache":
		return w.removeApache(d.Domain)
	case "caddy":
		return w.removeCaddy(d.Domain)
	case "go":
		return w.removeGo(d.Domain)
	default:
		return fmt.Errorf("webserver: unsupported server %q", w.Active())
	}
}

// Reload asks the active server to reload its configuration.
func (w *WebServer) Reload() error {
	switch w.Active() {
	case "nginx":
		return reloadService("nginx")
	case "apache":
		return reloadService("apache2")
	case "caddy":
		return reloadService("caddy")
	case "go":
		return nil // in-memory routes are swapped atomically
	default:
		return fmt.Errorf("webserver: unsupported server %q", w.Active())
	}
}

func reloadService(name string) error {
	if LookPath("systemctl") {
		if _, err := RunTimeout(20*time.Second, "systemctl", "reload", name); err == nil {
			return nil
		}
		if _, err := RunTimeout(20*time.Second, "systemctl", "restart", name); err == nil {
			return nil
		}
		return fmt.Errorf("webserver: failed to reload %s", name)
	}
	if name == "nginx" && LookPath("nginx") {
		if _, err := RunTimeout(10*time.Second, "nginx", "-s", "reload"); err == nil {
			return nil
		}
	}
	if name == "apache2" && LookPath("apache2") {
		if _, err := RunTimeout(10*time.Second, "apache2ctl", "-k", "graceful"); err == nil {
			return nil
		}
	}
	return fmt.Errorf("webserver: %s does not appear to be running", name)
}

// --- helpers -------------------------------------------------------------------

func siteName(domain string) string { return "aegis-" + domain + ".conf" }

func (w *WebServer) nginxSitesDir() string {
	if w.Cfg.WebServer.NginxDir != "" {
		return w.Cfg.WebServer.NginxDir
	}
	for _, d := range []string{"/etc/nginx/sites-available", "/etc/nginx/conf.d"} {
		if _, err := os.Stat(d); err == nil {
			return d
		}
	}
	return "/etc/nginx/sites-available"
}

func (w *WebServer) apacheSitesDir() string {
	if w.Cfg.WebServer.ApacheDir != "" {
		return w.Cfg.WebServer.ApacheDir
	}
	for _, d := range []string{"/etc/apache2/sites-available", "/etc/httpd/conf.d"} {
		if _, err := os.Stat(d); err == nil {
			return d
		}
	}
	return "/etc/apache2/sites-available"
}

func (w *WebServer) apachePort() int {
	if w.Cfg.WebServer.ApacheListenPort > 0 {
		return w.Cfg.WebServer.ApacheListenPort
	}
	return 80
}

func (w *WebServer) socketFor(d *store.Domain) string {
	if d.PHPVersion == "" {
		return ""
	}
	return w.PHP.SocketPath(d.Domain)
}

func hostnames(d *store.Domain, aliases []string) []string {
	out := []string{d.Domain}
	for _, a := range aliases {
		if a != "" && a != d.Domain {
			out = append(out, a)
		}
	}
	return out
}

// --- nginx ---------------------------------------------------------------------

func (w *WebServer) generateNginx(d *store.Domain, aliases []string, systemUser string) string {
	sock := w.socketFor(d)
	names := strings.Join(hostnames(d, aliases), " ")
	root := d.DocumentRoot
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Aegis-managed site for %s\n", d.Domain)

	listenHTTP := "80"
	listenHTTPS := "443 ssl http2"
	if !d.SSLEnabled {
		fmt.Fprintf(&sb, "server {\n    listen %s;\n    listen [::]:%s;\n", listenHTTP, listenHTTP)
	} else {
		// Redirect HTTP -> HTTPS.
		fmt.Fprintf(&sb, "server {\n    listen %s;\n    listen [::]:%s;\n    server_name %s;\n    return 301 https://$host$request_uri;\n}\n", listenHTTP, listenHTTP, names)
		fmt.Fprintf(&sb, "server {\n    listen %s;\n    listen [::]:%s;\n    ssl_certificate %s;\n    ssl_certificate_key %s;\n    ssl_protocols TLSv1.2 TLSv1.3;\n", listenHTTPS, listenHTTPS, d.SSLCertPath, d.SSLKeyPath)
	}
	fmt.Fprintf(&sb, "    server_name %s;\n    root %s;\n    index index.php index.html index.htm;\n", names, root)
	fmt.Fprintf(&sb, "    access_log /var/log/nginx/%s.access.log;\n    error_log /var/log/nginx/%s.error.log;\n\n", d.Domain, d.Domain)
	fmt.Fprintf(&sb, "    location / {\n        try_files $uri $uri/ /index.php?$query_string;\n    }\n\n")
	if sock != "" {
		fmt.Fprintf(&sb, "    location ~ \\.php$ {\n        include fastcgi_params;\n        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;\n        fastcgi_pass unix:%s;\n        fastcgi_index index.php;\n    }\n\n", sock)
	}
	fmt.Fprintf(&sb, "    location ~ /\\.(?!well-known).* { deny all; }\n\n")
	fmt.Fprintf(&sb, "    location ~ /\\.well-known/acme-challenge { allow all; }\n")
	fmt.Fprintf(&sb, "}\n")
	return sb.String()
}

func (w *WebServer) applyNginx(d *store.Domain, aliases []string, systemUser string) error {
	dir := w.nginxSitesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file := filepath.Join(dir, siteName(d.Domain))
	if err := os.WriteFile(file, []byte(w.generateNginx(d, aliases, systemUser)), 0o644); err != nil {
		return err
	}
	// Symlink into sites-enabled when the layout uses it.
	if strings.HasSuffix(dir, "sites-available") {
		enabled := strings.TrimSuffix(dir, "sites-available") + "sites-enabled"
		if _, err := os.Stat(enabled); err == nil {
			_ = os.Remove(filepath.Join(enabled, siteName(d.Domain)))
			_ = os.Symlink(file, filepath.Join(enabled, siteName(d.Domain)))
		}
	}
	if _, err := RunTimeout(15*time.Second, "nginx", "-t"); err != nil {
		return fmt.Errorf("nginx config test failed: %w", err)
	}
	return w.Reload()
}

func (w *WebServer) removeNginx(domain string) error {
	dir := w.nginxSitesDir()
	file := filepath.Join(dir, siteName(domain))
	_ = os.Remove(file)
	if strings.HasSuffix(dir, "sites-available") {
		enabled := strings.TrimSuffix(dir, "sites-available") + "sites-enabled"
		_ = os.Remove(filepath.Join(enabled, siteName(domain)))
	}
	if _, err := RunTimeout(15*time.Second, "nginx", "-t"); err == nil {
		_ = w.Reload()
	}
	return nil
}

// --- apache --------------------------------------------------------------------

func (w *WebServer) generateApache(d *store.Domain, aliases []string, systemUser string) string {
	port := w.apachePort()
	sock := w.socketFor(d)
	root := d.DocumentRoot
	names := strings.Join(hostnames(d, aliases), " ")
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Aegis-managed site for %s\n", d.Domain)
	if !d.SSLEnabled {
		w.writeApacheVhost(&sb, d, port, false, names, root, sock)
	} else {
		w.writeApacheVhost(&sb, d, port, false, names, root, sock)
		w.writeApacheVhost(&sb, d, 443, true, names, root, sock)
	}
	return sb.String()
}

func (w *WebServer) writeApacheVhost(sb *strings.Builder, d *store.Domain, port int, ssl bool, names, root, sock string) {
	fmt.Fprintf(sb, "<VirtualHost *:%d>\n", port)
	fmt.Fprintf(sb, "    ServerName %s\n", d.Domain)
	if ssl {
		fmt.Fprintf(sb, "    ServerAlias %s\n", names)
	}
	if ssl {
		fmt.Fprintf(sb, "    SSLEngine on\n    SSLCertificateFile %s\n    SSLCertificateKeyFile %s\n    SSLProtocol all -SSLv3 -TLSv1 -TLSv1.1\n", d.SSLCertPath, d.SSLKeyPath)
	}
	fmt.Fprintf(sb, "    DocumentRoot %s\n", root)
	fmt.Fprintf(sb, "    <Directory %s>\n        Options -Indexes +FollowSymLinks\n        AllowOverride All\n        Require all granted\n    </Directory>\n", root)
	if sock != "" {
		fmt.Fprintf(sb, "    <FilesMatch \\.php$>\n        SetHandler \"proxy:unix:%s|fcgi://localhost\"\n    </FilesMatch>\n", sock)
	}
	fmt.Fprintf(sb, "    ErrorLog ${APACHE_LOG_DIR}/%s-error.log\n    CustomLog ${APACHE_LOG_DIR}/%s-access.log combined\n", d.Domain, d.Domain)
	fmt.Fprintf(sb, "</VirtualHost>\n\n")
}

func (w *WebServer) applyApache(d *store.Domain, aliases []string, systemUser string) error {
	dir := w.apacheSitesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file := filepath.Join(dir, siteName(d.Domain))
	if err := os.WriteFile(file, []byte(w.generateApache(d, aliases, systemUser)), 0o644); err != nil {
		return err
	}
	if _, err := RunTimeout(15*time.Second, "apachectl", "-t"); err != nil {
		return fmt.Errorf("apache config test failed: %w", err)
	}
	return w.Reload()
}

func (w *WebServer) removeApache(domain string) error {
	dir := w.apacheSitesDir()
	_ = os.Remove(filepath.Join(dir, siteName(domain)))
	if _, err := RunTimeout(15*time.Second, "apachectl", "-t"); err == nil {
		_ = w.Reload()
	}
	return nil
}

// --- caddy ---------------------------------------------------------------------

func (w *WebServer) caddyFile() string {
	if w.Cfg.WebServer.CaddyFile != "" {
		return w.Cfg.WebServer.CaddyFile
	}
	return "/etc/caddy/Caddyfile"
}

func (w *WebServer) caddyConfDir() string { return filepath.Dir(w.caddyFile()) + "/conf.d" }

func (w *WebServer) generateCaddy(d *store.Domain, aliases []string, systemUser string) string {
	sock := w.socketFor(d)
	names := strings.Join(hostnames(d, aliases), ", ")
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Aegis-managed site for %s\n", d.Domain)
	fmt.Fprintf(&sb, "%s {\n", names)
	fmt.Fprintf(&sb, "    root * %s\n", d.DocumentRoot)
	if sock != "" {
		fmt.Fprintf(&sb, "    php_fastcgi unix//%s\n", sock)
	}
	fmt.Fprintf(&sb, "    encode zstd gzip\n")
	fmt.Fprintf(&sb, "    file_server\n")
	if !d.SSLEnabled {
		fmt.Fprintf(&sb, "    tls internal\n")
	}
	fmt.Fprintf(&sb, "    log { output file /var/log/caddy/%s.log }\n", d.Domain)
	fmt.Fprintf(&sb, "}\n")
	return sb.String()
}

func (w *WebServer) applyCaddy(d *store.Domain, aliases []string, systemUser string) error {
	dir := w.caddyConfDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, siteName(d.Domain)), []byte(w.generateCaddy(d, aliases, systemUser)), 0o644); err != nil {
		return err
	}
	// Ensure the main Caddyfile imports conf.d/*.
	mainFile := w.caddyFile()
	data, _ := os.ReadFile(mainFile)
	if !strings.Contains(string(data), "conf.d/*") {
		f, err := os.OpenFile(mainFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(f, "\nimport %s/*\n", dir)
		f.Close()
		if err != nil {
			return err
		}
	}
	if LookPath("caddy") {
		if _, err := RunTimeout(15*time.Second, "caddy", "validate", "--config", mainFile); err != nil {
			return fmt.Errorf("caddy validate failed: %w", err)
		}
	}
	return w.Reload()
}

func (w *WebServer) removeCaddy(domain string) error {
	_ = os.Remove(filepath.Join(w.caddyConfDir(), siteName(domain)))
	_ = w.Reload()
	return nil
}

// --- native go server -------------------------------------------------------------

func (w *WebServer) generateGo(d *store.Domain, aliases []string, systemUser string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Aegis native Go server route for %s\n", d.Domain)
	fmt.Fprintf(&sb, "# root: %s\n", d.DocumentRoot)
	fmt.Fprintf(&sb, "# php: %s\n", d.PHPVersion)
	if d.SSLEnabled {
		fmt.Fprintf(&sb, "# tls: %s / %s\n", d.SSLCertPath, d.SSLKeyPath)
	}
	return sb.String()
}

func (w *WebServer) applyGo(d *store.Domain, aliases []string, systemUser string) error {
	route := GoRoute{
		Domain:     d.Domain,
		Root:       d.DocumentRoot,
		PHPVersion: d.PHPVersion,
		SSL:        d.SSLEnabled,
		Cert:       d.SSLCertPath,
		Key:        d.SSLKeyPath,
		Hostnames:  hostnames(d, aliases),
	}
	if d.PHPVersion != "" {
		route.Socket = w.PHP.SocketPath(d.Domain)
	}
	w.mu.Lock()
	w.goRoutes[d.Domain] = route
	w.mu.Unlock()
	return nil
}

func (w *WebServer) removeGo(domain string) error {
	w.mu.Lock()
	delete(w.goRoutes, domain)
	w.mu.Unlock()
	return nil
}

// GoRoutes returns a copy of the registered routes for the built-in server.
func (w *WebServer) GoRoutes() map[string]GoRoute {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]GoRoute, len(w.goRoutes))
	for k, v := range w.goRoutes {
		out[k] = v
	}
	return out
}
