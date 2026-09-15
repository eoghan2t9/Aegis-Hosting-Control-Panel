package svc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

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
		w.serveGoRoute(rw, r, route)
	})
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
					break // let the PHP path below handle it
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

	status, body, stderr, err := w.fcgiExec(route.Socket, r, scriptFile, scriptName)
	if err != nil {
		if stderr != "" {
			// Surface php errors to the server log via headers in dev mode.
			rw.Header().Set("X-Aegis-Php-Error", truncate(stderr, 500))
		}
		http.Error(rw, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
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
func (w *WebServer) fcgiExec(socket string, r *http.Request, scriptFile, scriptName string) (int, []byte, string, error) {
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
		return 0, nil, "", err
	}
	return res.Status, res.Stdout, string(res.Stderr), nil
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
// (used by the SNI listener). Results are cached.
func (w *WebServer) GoTLSCert(host string) (*tls.Certificate, error) {
	route, ok := w.GoRoutes()[strings.ToLower(host)]
	if !ok || !route.SSL {
		return nil, fmt.Errorf("no tls route for %s", host)
	}
	return loadCert(route.Cert, route.Key)
}

func loadCert(certFile, keyFile string) (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}
