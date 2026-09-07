package svc

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
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

func (w *WebServer) serveGoRoute(rw http.ResponseWriter, r *http.Request, route GoRoute) {
	root := route.Root
	upath := path.Clean("/" + r.URL.Path)
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
