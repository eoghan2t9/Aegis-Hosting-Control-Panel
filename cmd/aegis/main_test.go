package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"aegis/internal/api"
	"aegis/internal/config"
)

// newPanelHandler builds a panelHandler around a minimal api.Server. Only the
// routing shell is exercised — no handler that touches store/services is
// called by these tests.
func newPanelHandler(t *testing.T, base string, outside http.Handler) http.Handler {
	t.Helper()
	cfg := config.Default()
	cfg.PanelBase = base
	return panelHandler(&api.Server{Cfg: cfg}, outside)
}

func TestPanelHandlerBasePath(t *testing.T) {
	h := newPanelHandler(t, "/aegis", nil)

	// Bare base path redirects to the trailing-slash form.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/aegis", nil))
	if rec.Code != http.StatusPermanentRedirect {
		t.Errorf("GET /aegis: code = %d, want 308", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/aegis/" {
		t.Errorf("GET /aegis: Location = %q, want /aegis/", loc)
	}

	// Under the prefix the SPA index is served (unknown path fallback).
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/aegis/some/route", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /aegis/some/route: code = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("GET /aegis/some/route: Content-Type = %q", ct)
	}

	// Assets resolve through the prefix.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/aegis/css/aegis.css", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /aegis/css/aegis.css: code = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" || ct[:8] != "text/css" {
		t.Errorf("GET /aegis/css/aegis.css: Content-Type = %q, want text/css", ct)
	}

	// Outside the prefix with no fallback handler: 404.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/not-the-panel", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /not-the-panel: code = %d, want 404", rec.Code)
	}
}

func TestPanelHandlerOutsideDelegate(t *testing.T) {
	var gotPath string
	outside := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusTeapot)
	})
	h := newPanelHandler(t, "/aegis", outside)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/site/index.html", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("outside request: code = %d, want 418", rec.Code)
	}
	if gotPath != "/site/index.html" {
		t.Errorf("outside request: path mutated to %q", gotPath)
	}
}

func TestPanelHandlerRootMode(t *testing.T) {
	// PanelBase "" keeps the historic root-level behavior.
	h := newPanelHandler(t, "", nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("root mode GET /: code = %d, want 200", rec.Code)
	}
}
