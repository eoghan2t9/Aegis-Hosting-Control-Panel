package svc

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

	// IsSuspended, when set, reports whether the account that owns systemUser is
	// suspended (see Apply). It is a field, not a Store, so tests can stub it.
	IsSuspended func(systemUser string) bool

	mu       sync.Mutex
	goRoutes map[string]GoRoute

	// goProxies caches one *httputil.ReverseProxy per upstream target so
	// proxyGoRoute (goserver.go) doesn't allocate a fresh one on every
	// single request to a proxied (container/app) domain.
	goProxies sync.Map

	// goHTTP/goHTTPS are the native Go server's live listeners, non-nil only
	// while Active() == "go". Managed by StartGo/StopGo (goserver.go) so
	// switching the active web server at runtime (handleWebServerSet) takes
	// effect immediately instead of only on the next process restart.
	goHTTP  *http.Server
	goHTTPS *http.Server

	// fallbackCerts caches lazily-generated self-signed certificates for
	// known GoRoutes that have no real SSL configured yet (see GoTLSCert) —
	// bounded by the number of managed domains, never grown from arbitrary
	// SNI probes.
	fallbackCerts map[string]*tls.Certificate
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
	// ProxyTarget, when set, makes serveGoRoute reverse-proxy every request
	// there instead of serving Root/PHP — mirrors Domain.ProxyTarget.
	ProxyTarget string `json:"proxy_target,omitempty"`
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

// IsAvailable reports whether a server is installed (detectable).
func (w *WebServer) IsAvailable(server string) bool {
	for _, a := range w.Available() {
		if a == server {
			return true
		}
	}
	return false
}

// Install installs the distro package for a web server and, for Apache,
// enables the modules the panel's vhost template and typical .htaccess
// files rely on. Errors when the package manager is missing or the install
// fails; a successful install is picked up by the next Available() call.
func (w *WebServer) Install(server string) error {
	switch server {
	case "nginx":
		return installAptPackages("nginx")
	case "caddy":
		// Caddy usually needs its own repo; try plain apt first, then add
		// the official stable repo and retry (Debian/Ubuntu).
		if err := installAptPackages("caddy"); err == nil {
			return nil
		}
		return installCaddyRepo()
	case "apache":
		if err := installAptPackages("apache2"); err != nil {
			return err
		}
		return w.enableApacheModules()
	default:
		return fmt.Errorf("webserver: nothing to install for %q", server)
	}
}

// installAptPackages installs distro packages when apt is present.
func installAptPackages(pkgs ...string) error {
	if !LookPath("apt-get") {
		return fmt.Errorf("webserver: automatic install requires apt (Debian/Ubuntu)")
	}
	args := append([]string{"install", "-y"}, pkgs...)
	if _, err := RunTimeout(10*time.Minute, "apt-get", args...); err != nil {
		return fmt.Errorf("webserver: apt-get install %s: %w", strings.Join(pkgs, " "), err)
	}
	return nil
}

// installCaddyRepo adds Caddy's official apt repository and installs it.
func installCaddyRepo() error {
	if !LookPath("apt-get") {
		return fmt.Errorf("webserver: automatic caddy install requires apt (Debian/Ubuntu)")
	}
	steps := [][]string{
		{"bash", "-c", "install -d /usr/share/keyrings && curl -fsSL 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg 2>/dev/null || true"},
		{"bash", "-c", "curl -fsSL 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' > /etc/apt/sources.list.d/caddy-stable.list"},
		{"apt-get", "update", "-qq"},
		{"apt-get", "install", "-y", "caddy"},
	}
	for _, step := range steps {
		if _, err := RunTimeout(5*time.Minute, step[0], step[1:]...); err != nil {
			return fmt.Errorf("webserver: caddy repo install: %w", err)
		}
	}
	return nil
}

// enableApacheModules turns on the modules the panel's vhosts and common
// .htaccess files depend on: proxy (panel + PHP vhost handler), rewrite and
// headers (per-site rules), expires, deflate and mime (Caching/encoding
// directives), setenvif, and fcgi setup helpers. Best-effort per module —
// a2enmod lists what exists; individual failures are non-fatal.
func (w *WebServer) enableApacheModules() error {
	if !LookPath("a2enmod") {
		return nil // not a Debian-layout Apache; a2enmod is a no-op there
	}
	mods := []string{
		"proxy", "proxy_http", "proxy_fcgi", "proxy_wstunnel",
		"rewrite", "headers", "expires", "deflate", "mime", "setenvif",
	}
	for _, m := range mods {
		if _, err := RunTimeout(30*time.Second, "a2enmod", m); err != nil {
			// Module not shipped in this distro build: skip it.
			continue
		}
	}
	return nil
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
	// A suspended owner's domains are rendered as a reverse proxy to the
	// suspended-page listener (SuspendedAddr) instead of the real site. Every
	// backend already knows how to proxy to an upstream, so this covers all of
	// them at once, and the domain's own certificate still terminates TLS. The
	// stored domain is untouched, so lifting the suspension and re-applying
	// restores the real site (or its container proxy) exactly.
	if w.IsSuspended != nil && w.IsSuspended(systemUser) {
		sd := *d
		sd.ProxyTarget = SuspendedAddr
		d = &sd
	}
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
	// Only use systemctl when systemd is actually PID 1; a systemctl binary can
	// exist in containers where the systemd bus is absent.
	if systemdIsInit() {
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

// serviceNameFor maps a web server choice to its systemd unit name; "go"
// isn't a systemd service (it's an in-process listener, see StartGo/StopGo).
func serviceNameFor(server string) string {
	switch server {
	case "nginx":
		return "nginx"
	case "apache":
		return "apache2"
	case "caddy":
		return "caddy"
	default:
		return ""
	}
}

// StopService stops (but does not disable) the systemd unit backing a
// nginx/apache/caddy choice — a no-op for "go" or on a non-systemd host.
// Called when switching away from server, so the old backend releases
// :80/:443 before the new one tries to bind them.
func (w *WebServer) StopService(server string) {
	name := serviceNameFor(server)
	if name == "" || !systemdIsInit() {
		return
	}
	_, _ = RunTimeout(15*time.Second, "systemctl", "stop", name)
}

// EnsureRunning starts and enables the systemd unit backing a
// nginx/apache/caddy choice — a no-op for "go" (started via StartGo instead)
// and best-effort on a non-systemd host, matching reloadService's fallback.
func (w *WebServer) EnsureRunning(server string) error {
	name := serviceNameFor(server)
	if name == "" || !systemdIsInit() {
		return nil
	}
	if _, err := RunTimeout(20*time.Second, "systemctl", "enable", "--now", name); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	return nil
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

// panelProxyBlocks renders the reverse-proxy snippet that exposes the panel
// under PanelBase on customer vhosts, so the panel is reachable as
// http://domain/aegis (or the server IP) without a dedicated port. Returns ""
// when the panel is served from the root or the active server has no proxy
// support. The upstream is the panel listener forced onto loopback.
func (w *WebServer) panelProxyNginx() string {
	base, up := w.Cfg.PanelBase, w.Cfg.PanelUpstream()
	if base == "" {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "    # Aegis panel (proxied to the panel listener)\n")
	fmt.Fprintf(&sb, "    location = %s { return 301 %s/; }\n", base, base)
	fmt.Fprintf(&sb, "    location %s/ {\n", base)
	fmt.Fprintf(&sb, "        proxy_pass http://%s;\n", up)
	fmt.Fprintf(&sb, "        proxy_http_version 1.1;\n")
	fmt.Fprintf(&sb, "        proxy_set_header Host $host;\n")
	fmt.Fprintf(&sb, "        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n")
	fmt.Fprintf(&sb, "        proxy_set_header X-Forwarded-Proto $scheme;\n")
	// $http_connection instead of a map: browsers send "Upgrade" on the WS
	// handshake and "keep-alive" otherwise, so both work without an http{}-level map.
	fmt.Fprintf(&sb, "        proxy_set_header Upgrade $http_upgrade;\n")
	fmt.Fprintf(&sb, "        proxy_set_header Connection $http_connection;\n")
	fmt.Fprintf(&sb, "        proxy_read_timeout 3600s;\n")
	fmt.Fprintf(&sb, "        proxy_send_timeout 3600s;\n")
	fmt.Fprintf(&sb, "    }\n\n")
	return sb.String()
}

var (
	apacheProxyOnce sync.Once
	apacheProxyOK   bool
)

// apachePanelProxyAvailable reports whether mod_proxy + proxy_http +
// proxy_wstunnel are loaded (needed to proxy the panel incl. WebSockets).
// Checked once; vhosts simply omit the panel block when unavailable.
func apachePanelProxyAvailable() bool {
	apacheProxyOnce.Do(func() {
		if !LookPath("apachectl") {
			return
		}
		out, err := RunTimeout(15*time.Second, "apachectl", "-M")
		if err != nil {
			return
		}
		apacheProxyOK = strings.Contains(out, "proxy_http") && strings.Contains(out, "proxy_wstunnel")
	})
	return apacheProxyOK
}

func (w *WebServer) panelProxyApache() string {
	base, up := w.Cfg.PanelBase, w.Cfg.PanelUpstream()
	if base == "" || !apachePanelProxyAvailable() {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "    # Aegis panel (proxied to the panel listener)\n")
	fmt.Fprintf(&sb, "    ProxyPass %s http://%s%s retry=0 upgrade=websocket\n", base, up, base)
	fmt.Fprintf(&sb, "    ProxyPassReverse %s http://%s%s\n", base, up, base)
	return sb.String()
}

func (w *WebServer) panelProxyCaddy() string {
	base, up := w.Cfg.PanelBase, w.Cfg.PanelUpstream()
	if base == "" {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "    # Aegis panel (proxied to the panel listener)\n")
	fmt.Fprintf(&sb, "    handle %s {\n", base)
	fmt.Fprintf(&sb, "        redir %s/ 308\n", base)
	fmt.Fprintf(&sb, "    }\n")
	fmt.Fprintf(&sb, "    handle %s/* {\n", base)
	fmt.Fprintf(&sb, "        reverse_proxy %s\n", up)
	fmt.Fprintf(&sb, "    }\n")
	return sb.String()
}

// --- nginx ---------------------------------------------------------------------

// nginxListen returns the "listen" directive(s) for spec (e.g. "80" or "443
// ssl http2"): bound to addr plus its IPv6-literal form when addr is set —
// addr is a domain's assigned IP (store.Domain.IPAddress), already validated
// by svc.IPs.Create to be configured on a local interface before it could
// reach here — otherwise the previous wildcard-only behavior (every
// existing, unassigned domain keeps generating byte-identical config).
func nginxListen(addr, spec string) string {
	if addr == "" {
		return fmt.Sprintf("    listen %s;\n    listen [::]:%s;\n", spec, spec)
	}
	bind := addr
	if strings.Contains(addr, ":") {
		bind = "[" + addr + "]"
	}
	return fmt.Sprintf("    listen %s:%s;\n", bind, spec)
}

func (w *WebServer) generateNginx(d *store.Domain, aliases []string, systemUser string) string {
	sock := w.socketFor(d)
	names := strings.Join(hostnames(d, aliases), " ")
	root := d.DocumentRoot
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Aegis-managed site for %s\n", d.Domain)

	listenHTTP := "80"
	listenHTTPS := "443 ssl http2"
	if !d.SSLEnabled {
		fmt.Fprintf(&sb, "server {\n%s", nginxListen(d.IPAddress, listenHTTP))
	} else {
		// Redirect HTTP -> HTTPS.
		fmt.Fprintf(&sb, "server {\n%s    server_name %s;\n    return 301 https://$host$request_uri;\n}\n", nginxListen(d.IPAddress, listenHTTP), names)
		fmt.Fprintf(&sb, "server {\n%s    ssl_certificate %s;\n    ssl_certificate_key %s;\n    ssl_protocols TLSv1.2 TLSv1.3;\n", nginxListen(d.IPAddress, listenHTTPS), d.SSLCertPath, d.SSLKeyPath)
	}
	fmt.Fprintf(&sb, "    server_name %s;\n    root %s;\n    index index.php index.html index.htm;\n", names, root)
	fmt.Fprintf(&sb, "    access_log /var/log/nginx/%s.access.log;\n    error_log /var/log/nginx/%s.error.log;\n\n", d.Domain, d.Domain)
	if d.ProxyTarget != "" {
		// Container/app domain: every request goes to the upstream, PHP and
		// the static docroot are irrelevant while this is set.
		fmt.Fprintf(&sb, "    location / {\n")
		fmt.Fprintf(&sb, "        proxy_pass http://%s;\n", d.ProxyTarget)
		fmt.Fprintf(&sb, "        proxy_http_version 1.1;\n")
		fmt.Fprintf(&sb, "        proxy_set_header Host $host;\n")
		fmt.Fprintf(&sb, "        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n")
		fmt.Fprintf(&sb, "        proxy_set_header X-Forwarded-Proto $scheme;\n")
		fmt.Fprintf(&sb, "        proxy_set_header Upgrade $http_upgrade;\n")
		fmt.Fprintf(&sb, "        proxy_set_header Connection $http_connection;\n")
		fmt.Fprintf(&sb, "        proxy_read_timeout 3600s;\n")
		fmt.Fprintf(&sb, "        proxy_send_timeout 3600s;\n")
		fmt.Fprintf(&sb, "    }\n\n")
	} else {
		fmt.Fprintf(&sb, "    location / {\n        try_files $uri $uri/ /index.php?$query_string;\n    }\n\n")
		if sock != "" {
			fmt.Fprintf(&sb, "    location ~ \\.php$ {\n        include fastcgi_params;\n        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;\n        fastcgi_pass unix:%s;\n        fastcgi_index index.php;\n    }\n\n", sock)
		}
	}
	sb.WriteString(w.panelProxyNginx())
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
	// Bind to the domain's assigned IP (already validated to exist on a
	// local interface by svc.IPs.Create) instead of every interface, when
	// one is set — every existing, unassigned domain keeps "*" (unchanged).
	// IPv6 literals need brackets here, same as nginxListen, or the port's
	// colon is ambiguous with the address's own colons.
	bind := "*"
	if d.IPAddress != "" {
		bind = d.IPAddress
		if strings.Contains(bind, ":") {
			bind = "[" + bind + "]"
		}
	}
	fmt.Fprintf(sb, "<VirtualHost %s:%d>\n", bind, port)
	fmt.Fprintf(sb, "    ServerName %s\n", d.Domain)
	if ssl {
		fmt.Fprintf(sb, "    ServerAlias %s\n", names)
	}
	if ssl {
		fmt.Fprintf(sb, "    SSLEngine on\n    SSLCertificateFile %s\n    SSLCertificateKeyFile %s\n    SSLProtocol all -SSLv3 -TLSv1 -TLSv1.1\n", d.SSLCertPath, d.SSLKeyPath)
	}
	fmt.Fprintf(sb, "    DocumentRoot %s\n", root)
	fmt.Fprintf(sb, "    <Directory %s>\n        Options -Indexes +FollowSymLinks\n        AllowOverride All\n        Require all granted\n    </Directory>\n", root)
	if d.ProxyTarget != "" {
		// Container/app domain: ProxyPass on "/" takes priority over the
		// filesystem, so PHP/static handling below is simply skipped.
		// ProxyPreserveHost matters here — without it mod_proxy rewrites the
		// Host header to the backend target instead of forwarding the
		// original one (nginx's proxy_set_header and Caddy's reverse_proxy
		// both already preserve it), which breaks any backend that resolves
		// per-request behavior from Host, like the webftp server does to
		// pick which domain's files to serve.
		fmt.Fprintf(sb, "    ProxyPreserveHost On\n")
		fmt.Fprintf(sb, "    ProxyPass / http://%s/ retry=0 upgrade=websocket\n", d.ProxyTarget)
		fmt.Fprintf(sb, "    ProxyPassReverse / http://%s/\n", d.ProxyTarget)
	} else if sock != "" {
		fmt.Fprintf(sb, "    <FilesMatch \\.php$>\n        SetHandler \"proxy:unix:%s|fcgi://localhost\"\n    </FilesMatch>\n", sock)
	}
	sb.WriteString(w.panelProxyApache())
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
	if d.IPAddress != "" {
		// Bind to the domain's assigned IP (already validated to exist on a
		// local interface by svc.IPs.Create) instead of every interface —
		// every existing, unassigned domain omits this (unchanged).
		fmt.Fprintf(&sb, "    bind %s\n", d.IPAddress)
	}
	if d.ProxyTarget != "" {
		// Container/app domain: skip the docroot/PHP handling entirely.
		fmt.Fprintf(&sb, "    reverse_proxy %s\n", d.ProxyTarget)
		fmt.Fprintf(&sb, "    encode zstd gzip\n")
		sb.WriteString(w.panelProxyCaddy())
		if !d.SSLEnabled {
			fmt.Fprintf(&sb, "    tls internal\n")
		}
		fmt.Fprintf(&sb, "    log { output file /var/log/caddy/%s.log }\n", d.Domain)
		fmt.Fprintf(&sb, "}\n")
		return sb.String()
	}
	fmt.Fprintf(&sb, "    root * %s\n", d.DocumentRoot)
	// Apache default <Files ".ht*"> semantics: never serve dotfiles that
	// carry access/rewrite configuration.
	fmt.Fprintf(&sb, "    @htblocked path_regexp (^|/)\\.ht\n")
	fmt.Fprintf(&sb, "    respond @htblocked 403\n")
	if sock != "" {
		fmt.Fprintf(&sb, "    php_fastcgi unix//%s\n", sock)
	}
	fmt.Fprintf(&sb, "    encode zstd gzip\n")
	sb.WriteString(w.panelProxyCaddy())
	sb.WriteString(w.caddyHtSnippet(d))
	// DirectoryIndex from .htaccess overrides the file_server defaults.
	if ht := w.htConfigFor(d.DocumentRoot, "/"); ht != nil && len(ht.DirectoryIndex) > 0 {
		fmt.Fprintf(&sb, "    file_server {\n        index %s\n    }\n", strings.Join(ht.DirectoryIndex, " "))
	} else {
		fmt.Fprintf(&sb, "    file_server\n")
	}
	if !d.SSLEnabled {
		fmt.Fprintf(&sb, "    tls internal\n")
	}
	fmt.Fprintf(&sb, "    log { output file /var/log/caddy/%s.log }\n", d.Domain)
	fmt.Fprintf(&sb, "}\n")
	return sb.String()
}

// caddyHtSnippet translates a docroot's .htaccess into Caddyfile directives.
// Caddy reads its config only at load time — per-directory .htaccess
// semantics can't be reproduced dynamically — so this covers the static
// core: Redirect/RedirectMatch, unconditional RewriteRules (regex rewrites
// and R-flag redirects), deny-all, and DirectoryIndex (handled by the
// caller). File-existence RewriteConds (-f/-d) have no static Caddy
// equivalent; the canonical front-controller pattern they drive is already
// php_fastcgi's built-in fallback, so those are intentionally skipped.
func (w *WebServer) caddyHtSnippet(d *store.Domain) string {
	cfg := w.htConfigFor(d.DocumentRoot, "/")
	if cfg == nil || (!cfg.RewriteEngine && len(cfg.Redirects) == 0) {
		return ""
	}
	var sb strings.Builder
	wrote := false
	section := func() {
		if !wrote {
			fmt.Fprintf(&sb, "    # --- translated from .htaccess ---\n")
			wrote = true
		}
	}

	if cfg.DenyAll {
		section()
		fmt.Fprintf(&sb, "    @htdeny path_regexp .*\n")
		fmt.Fprintf(&sb, "    respond @htdeny 403\n")
	}

	// Redirect/RedirectMatch.
	for i, rd := range cfg.Redirects {
		section()
		if rd.Match {
			name := fmt.Sprintf("htredir%d", i)
			fmt.Fprintf(&sb, "    @%s path_regexp %s %s\n", name, name, rd.From)
			if rd.Status == 410 {
				fmt.Fprintf(&sb, "    respond @%s 410\n", name)
			} else {
				fmt.Fprintf(&sb, "    redir @%s %s %d\n", name, htCaddyTarget(rd.Target, name), rd.Status)
			}
			continue
		}
		if rd.Status == 410 {
			fmt.Fprintf(&sb, "    @htgone%d path %s %s/*\n", i, rd.From, strings.TrimSuffix(rd.From, "/"))
			fmt.Fprintf(&sb, "    respond @htgone%d 410\n", i)
			continue
		}
		// Exact path plus subtree: handle_path strips the prefix so {uri}
		// carries just the remainder ("Redirect /old /new" sends /old/x to
		// /new/x, like mod_alias).
		fmt.Fprintf(&sb, "    redir %s %s %d\n", rd.From, rd.Target, rd.Status)
		fmt.Fprintf(&sb, "    handle_path %s/* { redir %s{uri} %d }\n", strings.TrimSuffix(rd.From, "/"), rd.Target, rd.Status)
	}

	// Unconditional RewriteRules.
	for i, rule := range cfg.Rules {
		if len(rule.Conds) > 0 || rule.Sub == "-" || rule.Flags.Proxy {
			continue // conditional/pass-through rules: php_fastcgi covers the common case
		}
		re := htCompile(rule.Pattern, rule.Flags.NoCase)
		if re == nil {
			continue // PCRE-only pattern: leave to the front-controller fallback
		}
		section()
		name := fmt.Sprintf("htrw%d", i)
		fmt.Fprintf(&sb, "    @%s path_regexp %s %s\n", name, name, rule.Pattern)
		sub := htCaddyTarget(rule.Sub, name)
		switch {
		case rule.Flags.Forbidden:
			fmt.Fprintf(&sb, "    respond @%s 403\n", name)
		case rule.Flags.Gone:
			fmt.Fprintf(&sb, "    respond @%s 410\n", name)
		case rule.Flags.Redirect != 0 || htIsAbsoluteURL(sub):
			code := rule.Flags.Redirect
			if code == 0 {
				code = 302
			}
			fmt.Fprintf(&sb, "    redir @%s %s %d\n", name, sub, code)
		default:
			fmt.Fprintf(&sb, "    rewrite @%s %s\n", name, sub)
		}
	}
	if !wrote {
		return ""
	}
	return sb.String()
}

// htCaddyTarget converts a .htaccess substitution into Caddy placeholders:
// $N → {re.<name>.N} capture groups and the common server variables.
func htCaddyTarget(sub, name string) string {
	out := sub
	for n := 9; n >= 1; n-- {
		out = strings.ReplaceAll(out, "$"+strconv.Itoa(n), "{re."+name+"."+strconv.Itoa(n)+"}")
	}
	out = strings.ReplaceAll(out, "%{HTTP_HOST}", "{http.request.host}")
	out = strings.ReplaceAll(out, "%{REQUEST_URI}", "{http.request.uri}")
	return out
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
		Domain:      d.Domain,
		Root:        d.DocumentRoot,
		PHPVersion:  d.PHPVersion,
		SSL:         d.SSLEnabled,
		Cert:        d.SSLCertPath,
		Key:         d.SSLKeyPath,
		Hostnames:   hostnames(d, aliases),
		ProxyTarget: d.ProxyTarget,
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
