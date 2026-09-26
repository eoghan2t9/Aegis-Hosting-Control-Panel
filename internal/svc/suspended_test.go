package svc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aegis/internal/config"
	"aegis/internal/store"
)

func newSuspendedFixture(t *testing.T) (*SuspendedServer, string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	u := &store.User{Username: "alice", Email: "alice@example.com", PasswordHash: "h", Role: store.RoleUser, Status: store.StatusSuspended}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dom := &store.Domain{UserID: u.ID, Domain: "site.example.com", DocumentRoot: root}
	if err := st.CreateDomain(ctx, dom); err != nil {
		t.Fatal(err)
	}
	if err := st.AddAlias(ctx, dom.ID, "alias.example.org"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".well-known", "acme-challenge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".well-known", "acme-challenge", "tok_123-AB"), []byte("challenge-response"), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewSuspendedServer(st), root
}

func do(h http.Handler, method, host, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSuspendedPage(t *testing.T) {
	s, _ := newSuspendedFixture(t)
	h := s.Handler()

	for _, path := range []string{"/", "/index.php", "/wp-admin/", "/anything?x=1"} {
		rec := do(h, http.MethodGet, "site.example.com", path)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Account Suspended") || !strings.Contains(rec.Body.String(), "site.example.com") {
			t.Errorf("GET %s body missing the suspended message/host", path)
		}
	}
	rec := do(h, http.MethodGet, "site.example.com", "/")
	for header, want := range map[string]string{
		"Cache-Control": "no-store", "X-Robots-Tag": "noindex", "X-Content-Type-Options": "nosniff",
	} {
		if !strings.Contains(rec.Header().Get(header), want) {
			t.Errorf("header %s = %q, want it to contain %q", header, rec.Header().Get(header), want)
		}
	}
	// Every method gets the page; HEAD gets the status with no body.
	if rec := do(h, http.MethodPost, "site.example.com", "/login"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("POST = %d, want 503", rec.Code)
	}
	if rec := do(h, http.MethodHead, "site.example.com", "/"); rec.Code != http.StatusServiceUnavailable || rec.Body.Len() != 0 {
		t.Errorf("HEAD = %d with %d body bytes, want 503 and none", rec.Code, rec.Body.Len())
	}
}

func TestSuspendedPageEscapesHost(t *testing.T) {
	s, _ := newSuspendedFixture(t)
	rec := do(s.Handler(), http.MethodGet, `x"><script>alert(1)</script>`, "/")
	if strings.Contains(rec.Body.String(), "<script>alert(1)</script>") {
		t.Error("the Host header was reflected into the page unescaped")
	}
}

// Certificate renewals must keep working while an account is suspended.
func TestSuspendedServesACMEChallenges(t *testing.T) {
	s, _ := newSuspendedFixture(t)
	h := s.Handler()
	const path = "/.well-known/acme-challenge/tok_123-AB"

	for _, host := range []string{"site.example.com", "www.site.example.com", "webftp.site.example.com", "alias.example.org"} {
		rec := do(h, http.MethodGet, host, path)
		if rec.Code != http.StatusOK || rec.Body.String() != "challenge-response" {
			t.Errorf("challenge on %s = %d %q, want 200 and the token file", host, rec.Code, rec.Body.String())
		}
	}
	if rec := do(h, http.MethodGet, "unknown.example.net", path); rec.Code != http.StatusNotFound {
		t.Errorf("challenge for an unknown host = %d, want 404", rec.Code)
	}
	if rec := do(h, http.MethodGet, "site.example.com", "/.well-known/acme-challenge/missing"); rec.Code != http.StatusNotFound {
		t.Errorf("missing token = %d, want 404", rec.Code)
	}
	// Path traversal out of the challenge directory is refused.
	for _, bad := range []string{"/.well-known/acme-challenge/../../etc/passwd", "/.well-known/acme-challenge/a/b", "/.well-known/acme-challenge/%2e%2e%2fsecret"} {
		if rec := do(h, http.MethodGet, "site.example.com", bad); rec.Code == http.StatusOK {
			t.Errorf("traversal %q was served", bad)
		}
	}
}

// While suspended, every backend's vhost for the domain proxies to the
// suspended-page listener; the stored domain is untouched and lifting the
// suspension restores the real target.
func TestWebServerApplyRendersSuspendedDomainAsProxy(t *testing.T) {
	cfg := &config.Config{}
	cfg.WebServer.Server = "go"
	w := NewWebServer(cfg, nil)
	suspended := map[string]bool{}
	w.IsSuspended = func(u string) bool { return suspended[u] }

	dom := &store.Domain{Domain: "site.example.com", DocumentRoot: "/home/alice/site/public", ProxyTarget: "127.0.0.1:9000"}
	route := func() GoRoute { return w.GoRoutes()["site.example.com"] }

	if err := w.Apply(dom, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if got := route().ProxyTarget; got != "127.0.0.1:9000" {
		t.Errorf("active account proxy target = %q, want the container's 127.0.0.1:9000", got)
	}

	suspended["alice"] = true
	if err := w.Apply(dom, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if got := route().ProxyTarget; got != SuspendedAddr {
		t.Errorf("suspended account proxy target = %q, want %q", got, SuspendedAddr)
	}
	if dom.ProxyTarget != "127.0.0.1:9000" {
		t.Errorf("Apply mutated the stored domain: ProxyTarget = %q", dom.ProxyTarget)
	}

	// Another user's domain is unaffected.
	other := &store.Domain{Domain: "other.example.com", DocumentRoot: "/home/bob/other/public"}
	if err := w.Apply(other, nil, "bob"); err != nil {
		t.Fatal(err)
	}
	if got := w.GoRoutes()["other.example.com"].ProxyTarget; got != "" {
		t.Errorf("an unsuspended user's domain proxies to %q, want none", got)
	}

	delete(suspended, "alice")
	if err := w.Apply(dom, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if got := route().ProxyTarget; got != "127.0.0.1:9000" {
		t.Errorf("after unsuspending, proxy target = %q, want the container's again", got)
	}
}
