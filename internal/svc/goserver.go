package svc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GoAccessLogDir is where the native Go web server writes per-domain access
// and error logs — the "go" counterpart to nginx's /var/log/nginx and
// caddy's /var/log/caddy, read back by the panel's log viewer
// (internal/api/handlers_logs.go). Unlike those, nothing external manages
// this directory, so goLogAppend creates it lazily on first write.
const GoAccessLogDir = "/var/log/aegis"

// GoHandler returns the http.Handler that serves domains routed to the native
// Go web server. It resolves the host from the request, serves static files
// from the document root and proxies PHP through FastCGI to php-fpm.
func (w *WebServer) GoHandler() http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(strings.Split(r.Host, ":")[0])
		route, ok := w.GoRoutes()[host]
		if !ok {
			// Not a managed site: 404.
			http.NotFound(rw, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: rw, status: http.StatusOK}
		w.serveGoRoute(rec, r, route)
		logAccess(route.Domain, r, rec.status, rec.size, time.Since(start))
		if phpErr := rec.Header().Get("X-Aegis-Php-Error"); rec.status >= 500 || phpErr != "" {
			msg := phpErr
			if msg == "" {
				msg = fmt.Sprintf("%s %s -> %d", r.Method, r.URL.RequestURI(), rec.status)
			}
			logError(route.Domain, msg)
		}
	})
}

// statusRecorder wraps an http.ResponseWriter to capture the status code and
// byte count actually written, for access logging — serveGoRoute's many exit
// paths (http.Error, http.Redirect, http.ServeFile, http.NotFound, the raw
// WriteHeader+Write for PHP/proxy responses) all go through the standard
// ResponseWriter interface, so this needs no special-casing per path.
type statusRecorder struct {
	http.ResponseWriter
	status int
	size   int64
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.size += int64(n)
	return n, err
}

var goLogDirOnce sync.Once

// logAccess appends one combined-log-format-style line to the domain's
// access log. domain empty (shouldn't happen — GoRoutes are always keyed by
// a real hostname) is a no-op rather than logging to a bare ".access.log".
func logAccess(domain string, r *http.Request, status int, size int64, dur time.Duration) {
	if domain == "" {
		return
	}
	line := fmt.Sprintf("%s - - [%s] %q %d %d %q %q %.3f\n",
		remoteIP(r), time.Now().Format("02/Jan/2006:15:04:05 -0700"),
		fmt.Sprintf("%s %s %s", r.Method, r.URL.RequestURI(), r.Proto),
		status, size, r.Referer(), r.UserAgent(), dur.Seconds())
	goLogAppend(domain+".access.log", line)
}

// logError appends one line to the domain's error log — used for 5xx
// responses and any PHP stderr output surfaced via X-Aegis-Php-Error.
func logError(domain, msg string) {
	if domain == "" {
		return
	}
	goLogAppend(domain+".error.log", fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg))
}

func goLogAppend(name, line string) {
	goLogDirOnce.Do(func() { _ = os.MkdirAll(GoAccessLogDir, 0755) })
	f, err := os.OpenFile(filepath.Join(GoAccessLogDir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// StartGo starts the native Go site server's HTTP/HTTPS listeners, or does
// nothing if they're already running (safe to call on every boot and on
// every live switch to "go"). panelHandler serves the panel itself under
// Cfg.PanelBase on the same listener (see cmd/aegis's panelHandler()); pass
// nil to skip that (used at boot for the dedicated :8080 panel listener,
// which already serves the panel on its own).
//
// Both ports are bound synchronously before returning, so a conflict (nginx/
// Apache/Caddy — or anything else — already holding :80/:443) is reported
// immediately as an error instead of only surfacing later via errCh, which
// is what let a bad "switch to go" request save a config that crash-looped
// the whole panel on its next restart.
func (w *WebServer) StartGo(panelHandler http.Handler, errCh chan<- error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.goHTTP != nil {
		return nil // already running
	}

	httpAddr := os.Getenv("AEGIS_GO_HTTP")
	if httpAddr == "" {
		httpAddr = ":80"
	}
	httpsAddr := os.Getenv("AEGIS_GO_HTTPS")
	if httpsAddr == "" {
		httpsAddr = ":443"
	}

	httpLn, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return fmt.Errorf("go web server: %w", err)
	}
	httpsLn, err := net.Listen("tcp", httpsAddr)
	if err != nil {
		_ = httpLn.Close()
		return fmt.Errorf("go web server: %w", err)
	}

	var site http.Handler = w.GoHandler()
	if panelHandler != nil {
		base := w.Cfg.PanelBase
		inner := site
		site = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if base != "" && (r.URL.Path == base || strings.HasPrefix(r.URL.Path, base+"/")) {
				panelHandler.ServeHTTP(rw, r)
				return
			}
			inner.ServeHTTP(rw, r)
		})
	}

	httpSrv := &http.Server{Addr: httpAddr, Handler: site}
	slog.Info("go web server listening (http)", "addr", httpAddr)
	go func() {
		if err := httpSrv.Serve(httpLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			trySend(errCh, fmt.Errorf("go http server: %w", err))
		}
	}()

	httpsSrv := &http.Server{
		Addr:    httpsAddr,
		Handler: site,
		TLSConfig: &tls.Config{
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				return w.GoTLSCert(hello.ServerName)
			},
			MinVersion: tls.VersionTLS12,
		},
	}
	slog.Info("go web server listening (https, sni)", "addr", httpsAddr)
	go func() {
		if err := httpsSrv.ServeTLS(httpsLn, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			trySend(errCh, fmt.Errorf("go https server: %w", err))
		}
	}()

	w.goHTTP, w.goHTTPS = httpSrv, httpsSrv
	return nil
}

// trySend delivers err without blocking when errCh is nil or already full —
// StartGo callers that don't care about post-startup errors (a live
// webserver-switch request, say) can pass nil.
func trySend(errCh chan<- error, err error) {
	if errCh == nil {
		return
	}
	select {
	case errCh <- err:
	default:
	}
}

// StopGo shuts down the native Go server's listeners, or does nothing if
// they aren't running.
func (w *WebServer) StopGo() {
	w.mu.Lock()
	httpSrv, httpsSrv := w.goHTTP, w.goHTTPS
	w.goHTTP, w.goHTTPS = nil, nil
	w.mu.Unlock()
	if httpSrv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	if httpsSrv != nil {
		_ = httpsSrv.Shutdown(ctx)
	}
}

// ServePreviewRoute serves one domain's docroot through the panel (static
// files + FastCGI), used by the API's domain-preview endpoint. This is the
// same serving path the native Go web server uses — only the host-based route
// resolution is skipped, because a preview request arrives on the panel's own
// hostname rather than the domain's.
func (w *WebServer) ServePreviewRoute(rw http.ResponseWriter, r *http.Request, route GoRoute) {
	w.serveGoRoute(rw, r, route)
}

func (w *WebServer) serveGoRoute(rw http.ResponseWriter, r *http.Request, route GoRoute) {
	if route.ProxyTarget != "" {
		w.proxyGoRoute(rw, r, route.ProxyTarget)
		return
	}
	root := route.Root
	upath := path.Clean("/" + r.URL.Path)

	// .htaccess interpretation: block sensitive/dot paths, then apply
	// redirects and rewrite rules. A rewritten path swaps into the request.
	if w.htGate(rw, r, upath) {
		return
	}
	if out := htEngine(w.htConfigFor(root, upath), r, root, upath, r.URL.RawQuery); out.Status == 403 || out.Status == 410 {
		http.Error(rw, http.StatusText(out.Status), out.Status)
		return
	} else if out.Redirect != "" {
		http.Redirect(rw, r, out.Redirect, out.Status)
		return
	} else if out.Path != "" {
		upath = path.Clean("/" + out.Path)
		u := *r.URL
		u.Path = upath
		u.RawQuery = out.Query
		r2 := r.Clone(r.Context())
		r2.URL = &u
		r = r2
	}

	fsPath := filepath.Join(root, filepath.FromSlash(upath))

	// Guard against traversal outside the docroot.
	rel, err := filepath.Rel(root, fsPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		http.Error(rw, "forbidden", http.StatusForbidden)
		return
	}

	info, statErr := os.Stat(fsPath)
	isPHP := strings.HasSuffix(upath, ".php")

	if statErr == nil && !info.IsDir() && !isPHP {
		// Serve static files directly.
		http.ServeFile(rw, r, fsPath)
		return
	}

	// Directory request: serve the directory index (from .htaccess
	// DirectoryIndex when present, else the usual defaults) when one of the
	// index files exists — before the PHP fallback, so static sites work.
	if statErr == nil && info.IsDir() {
		idx := w.htConfigFor(root, upath).DirectoryIndex
		if len(idx) == 0 {
			idx = []string{"index.php", "index.html", "index.htm"}
		}
		for _, name := range idx {
			ip := filepath.Join(fsPath, filepath.FromSlash(path.Clean("/"+name)))
			if st, err := os.Stat(ip); err == nil && !st.IsDir() {
				if strings.HasSuffix(name, ".php") && route.Socket != "" {
					// Let the PHP path below handle it — but point fsPath/info
					// at the index file we just found *in this directory*,
					// not the directory itself. Otherwise the info.IsDir()
					// check below can't tell this apart from a genuinely
					// missing path and falls back to the domain root's
					// index.php instead of this directory's own front
					// controller — silently running the wrong PHP app for
					// every subfolder that has its own index.php.
					fsPath, info = ip, st
					break
				}
				http.ServeFile(rw, r, ip)
				return
			}
		}
	}

	// PHP or pretty-URL fallback: run through php-fpm.
	if route.Socket == "" {
		// No PHP configured: if a static file exists serve it, else 404.
		if statErr == nil && !info.IsDir() {
			http.ServeFile(rw, r, fsPath)
		} else {
			http.NotFound(rw, r)
		}
		return
	}

	scriptFile := fsPath
	if statErr != nil || info.IsDir() {
		// Pretty URLs: hand the URI to index.php.
		scriptFile = filepath.Join(root, "index.php")
	}
	scriptName := "/index.php"
	if rel2, err := filepath.Rel(root, scriptFile); err == nil {
		scriptName = "/" + filepath.ToSlash(rel2)
	}

	status, headers, body, stderr, err := w.fcgiExec(route.Socket, r, scriptFile, scriptName)
	if err != nil {
		if stderr != "" {
			// Surface php errors to the server log via headers in dev mode.
			rw.Header().Set("X-Aegis-Php-Error", truncate(stderr, 500))
		}
		http.Error(rw, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	for k, vals := range headers {
		for _, v := range vals {
			rw.Header().Add(k, v)
		}
	}
	if stderr != "" {
		rw.Header().Set("X-Aegis-Php-Error", truncate(stderr, 500))
	}
	rw.WriteHeader(status)
	_, _ = rw.Write(body)
}

// proxyGoRoute reverse-proxies a container/app domain's request to target
// (a fixed "127.0.0.1:port" set by svc.Docker) — the native-server equivalent
// of the nginx/Apache/Caddy proxy_pass branches in webserver.go.
func (w *WebServer) proxyGoRoute(rw http.ResponseWriter, r *http.Request, target string) {
	w.reverseProxyFor(target).ServeHTTP(rw, r)
}

// reverseProxyFor returns the cached *httputil.ReverseProxy for target,
// building it once on first use instead of on every request. Its Director
// sets X-Forwarded-Proto from the inbound connection (net/http's
// ReverseProxy already appends X-Forwarded-For itself) — the nginx/Apache/
// Caddy vhosts for the same proxy_target feature set this explicitly, so a
// proxied app behind them can tell it's behind HTTPS; without it, the same
// domain served by the native "go" backend can never tell, which breaks
// secure-cookie/HTTPS-redirect logic that only the go backend was missing.
func (w *WebServer) reverseProxyFor(target string) *httputil.ReverseProxy {
	if p, ok := w.goProxies.Load(target); ok {
		return p.(*httputil.ReverseProxy)
	}
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: target})
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		scheme := "http"
		if req.TLS != nil {
			scheme = "https"
		}
		req.Header.Set("X-Forwarded-Proto", scheme)
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, r *http.Request, err error) {
		http.Error(rw, "upstream error: "+err.Error(), http.StatusBadGateway)
	}
	actual, _ := w.goProxies.LoadOrStore(target, proxy)
	return actual.(*httputil.ReverseProxy)
}

// htGate blocks dotfile paths before anything else is served: .ht* returns
// 403 (Apache's default <Files ".ht*"> semantics — the file may contain
// rewrite rules worth keeping secret), other dotfiles 404. .well-known stays
// reachable for ACME challenges, matching the panel's nginx template.
func (w *WebServer) htGate(rw http.ResponseWriter, r *http.Request, upath string) bool {
	for _, seg := range strings.Split(upath, "/") {
		if !strings.HasPrefix(seg, ".") || seg == "." || seg == "" {
			continue
		}
		if seg == ".well-known" {
			continue
		}
		if strings.HasPrefix(seg, ".ht") {
			http.Error(rw, "forbidden", http.StatusForbidden)
		} else {
			http.NotFound(rw, r)
		}
		return true
	}
	return false
}

// fcgiExec builds FastCGI params from the HTTP request and calls php-fpm.
func (w *WebServer) fcgiExec(socket string, r *http.Request, scriptFile, scriptName string) (int, http.Header, []byte, string, error) {
	var params [][2]string
	add := func(k, v string) { params = append(params, [2]string{k, v}) }
	host := strings.Split(r.Host, ":")[0]
	port := "80"
	if _, p, err := net.SplitHostPort(r.Host); err == nil {
		port = p
	}
	add("GATEWAY_INTERFACE", "CGI/1.1")
	add("SERVER_SOFTWARE", "aegis-go")
	add("SERVER_NAME", host)
	add("SERVER_PORT", port)
	add("SERVER_PROTOCOL", r.Proto)
	add("REQUEST_METHOD", r.Method)
	add("REQUEST_URI", r.URL.RequestURI())
	add("SCRIPT_NAME", scriptName)
	add("SCRIPT_FILENAME", scriptFile)
	add("DOCUMENT_ROOT", filepath.Dir(scriptFile))
	add("QUERY_STRING", r.URL.RawQuery)
	add("REMOTE_ADDR", remoteIP(r))
	add("REMOTE_PORT", r.RemoteAddr)
	add("CONTENT_TYPE", r.Header.Get("Content-Type"))
	if r.Body != nil {
		add("CONTENT_LENGTH", strconv.FormatInt(r.ContentLength, 10))
	} else {
		add("CONTENT_LENGTH", "0")
	}
	if r.TLS != nil {
		add("HTTPS", "on")
	}
	for k, vals := range r.Header {
		for _, v := range vals {
			add("HTTP_"+strings.ToUpper(strings.ReplaceAll(k, "-", "_")), v)
		}
	}
	body, _ := io.ReadAll(r.Body)
	res, err := fcgiRequest(socket, 60*time.Second, params, body)
	if err != nil {
		return 0, nil, nil, "", err
	}
	status, headers, respBody := parseCGIResponse(res.Stdout, res.Status)
	return status, headers, respBody, string(res.Stderr), nil
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// GoTLSCert loads the certificate for a hostname from the registered routes
// (used by the SNI listener). A known route with no real certificate yet
// (SSLEnabled false, or a configured one that fails to load) gets a
// lazily-generated self-signed fallback instead of an error — without this,
// tls.Config.GetCertificate returning an error makes Go send a raw
// "internal_error" TLS alert (surfaces in Chrome as
// ERR_SSL_VERSION_OR_CIPHER_MISMATCH) instead of completing the handshake,
// which is exactly what generateCaddy's "tls internal" already avoids for
// the same no-SSL-yet case (see webserver.go). A self-signed cert at least
// lets the connection complete with a normal, clickable "not private"
// warning — important now that browsers and proxies (Cloudflare, HSTS)
// increasingly attempt HTTPS by default even for a plain-HTTP-only site.
// An entirely unknown hostname (not one of our routes at all — scanner
// traffic, mostly) still errors, so this can't be used to force unbounded
// certificate generation.
func (w *WebServer) GoTLSCert(host string) (*tls.Certificate, error) {
	host = strings.ToLower(host)
	route, ok := w.GoRoutes()[host]
	if !ok {
		return nil, fmt.Errorf("no tls route for %s", host)
	}
	if route.SSL {
		if cert, err := loadCert(route.Cert, route.Key); err == nil {
			return cert, nil
		}
	}
	return w.selfSignedFallback(host)
}

func loadCert(certFile, keyFile string) (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

// selfSignedFallback returns a self-signed certificate for host, generating
// and caching it on first use (see WebServer.fallbackCerts).
func (w *WebServer) selfSignedFallback(host string) (*tls.Certificate, error) {
	w.mu.Lock()
	if cert, ok := w.fallbackCerts[host]; ok {
		w.mu.Unlock()
		return cert, nil
	}
	w.mu.Unlock()

	cert, err := generateSelfSignedCert(host)
	if err != nil {
		return nil, fmt.Errorf("generate fallback cert for %s: %w", host, err)
	}

	w.mu.Lock()
	if w.fallbackCerts == nil {
		w.fallbackCerts = map[string]*tls.Certificate{}
	}
	w.fallbackCerts[host] = cert
	w.mu.Unlock()
	return cert, nil
}

func generateSelfSignedCert(host string) (*tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{"Aegis (self-signed fallback)"}},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
