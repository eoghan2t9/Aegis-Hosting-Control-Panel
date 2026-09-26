package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSecurityHeadersOnPanelResponses(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/aegis/api/users", nil))
	for k, want := range map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "SAMEORIGIN",
		"Content-Security-Policy": "frame-ancestors 'self'",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS must not be sent: the panel is served on every customer's host name (got %q)", got)
	}
}

// A domain preview is a customer's own site rendered through the panel: it must
// not be forced uncacheable, and static assets keep their own caching headers.
func TestSecurityHeadersLeavePreviewAndAssetsCaching(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, path := range []string{"/aegis/api/domains/3/preview/style.css", "/aegis/js/app.js", "/aegis/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get("Cache-Control"); got != "" {
			t.Errorf("%s: Cache-Control = %q, want it left to the handler", path, got)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff", path)
		}
	}
}

// Listeners must time out slow header reads (slowloris) without capping the
// total request or response time, which would break uploads and the terminal.
func TestNewHTTPServerTimeouts(t *testing.T) {
	s := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if s.ReadHeaderTimeout <= 0 || s.ReadHeaderTimeout > time.Minute {
		t.Errorf("ReadHeaderTimeout = %v", s.ReadHeaderTimeout)
	}
	if s.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout = %v", s.IdleTimeout)
	}
	if s.ReadTimeout != 0 || s.WriteTimeout != 0 {
		t.Errorf("Read/WriteTimeout must stay unset (uploads, downloads, websockets): %v / %v", s.ReadTimeout, s.WriteTimeout)
	}
}
