package svc

import (
	"crypto/tls"
	"fmt"
	"io"
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
	u := &url.URL{Scheme: "http", Host: target}
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.ErrorHandler = func(rw http.ResponseWriter, r *http.Request, err error) {
		http.Error(rw, "upstream error: "+err.Error(), http.StatusBadGateway)
	}
	proxy.ServeHTTP(rw, r)
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
