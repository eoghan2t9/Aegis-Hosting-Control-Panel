package svc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"aegis/internal/store"
)

const (
	ssoDocroot = "/home/alice/site.example.com/public"
	ssoHost    = "webftp.site.example.com"
)

type ssoFixture struct {
	w      *WebFTP
	st     *store.Store
	site   *store.FTPAccount // dedicated account for site.example.com
	master *store.FTPAccount // alice's account covering her whole home
}

func newSSOFixture(t *testing.T) *ssoFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	u := &store.User{Username: "alice", Email: "alice@example.com", PasswordHash: "h",
		Role: store.RoleUser, Status: store.StatusActive, HomeDir: "/home/alice"}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	// site.example.com has a Web FTP vhost; other.example.com does not.
	for _, d := range []*store.Domain{
		{UserID: u.ID, Domain: "site.example.com", DocumentRoot: ssoDocroot},
		{UserID: u.ID, Domain: ssoHost, DocumentRoot: "/home/alice/site.example.com/.webftp", SSLEnabled: true},
		{UserID: u.ID, Domain: "other.example.com", DocumentRoot: "/home/alice/other.example.com/public"},
	} {
		if err := st.CreateDomain(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct-horse"), bcrypt.MinCost)
	f := &ssoFixture{w: NewWebFTP(st, nil, nil), st: st}
	f.site = &store.FTPAccount{UserID: u.ID, Username: "site_alice", PasswordHash: string(hash), HomeDir: ssoDocroot, Enabled: true}
	f.master = &store.FTPAccount{UserID: u.ID, Username: "alice", PasswordHash: string(hash), HomeDir: "/home/alice", Enabled: true}
	for _, a := range []*store.FTPAccount{f.site, f.master} {
		if err := st.CreateFTPAccount(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func postForm(h http.HandlerFunc, host, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Host = host
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == webftpCookie {
			return c
		}
	}
	return nil
}

func TestWebFTPTargets(t *testing.T) {
	f := newSSOFixture(t)
	ctx := context.Background()

	// A per-domain account opens only its own domain; the master account covers
	// alice's home but only domains that actually have a Web FTP vhost count.
	for _, a := range []*store.FTPAccount{f.site, f.master} {
		got := f.w.Targets(ctx, a)
		if len(got) != 1 || got[0].Domain != "site.example.com" || got[0].Host != ssoHost || !got[0].HTTPS {
			t.Errorf("Targets(%s) = %+v, want only site.example.com over https", a.Username, got)
		}
	}
	outside := &store.FTPAccount{UserID: f.site.UserID, Username: "elsewhere", HomeDir: "/srv/other", Enabled: true}
	if got := f.w.Targets(ctx, outside); len(got) != 0 {
		t.Errorf("account outside every docroot got targets: %+v", got)
	}
}

func TestWebFTPSSOLogsInOnce(t *testing.T) {
	f := newSSOFixture(t)
	ctx := context.Background()
	tok, err := f.w.MintSSO(ctx, f.site, f.w.Targets(ctx, f.site)[0])
	if err != nil {
		t.Fatal(err)
	}

	rec := postForm(f.w.handleSSO, ssoHost, "/sso", url.Values{"t": {tok}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("redeem = %d -> %q, want 303 to /", rec.Code, rec.Header().Get("Location"))
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
		t.Error("SSO response must be no-store and no-referrer")
	}
	c := sessionCookie(rec)
	if c == nil || !c.HttpOnly {
		t.Fatalf("no HttpOnly session cookie set: %+v", c)
	}
	// The cookie is a real session jailed to this domain's docroot.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = ssoHost
	req.AddCookie(c)
	sess, ok := f.w.sessionFor(req)
	if !ok || sess.username != "site_alice" || sess.homeDir != ssoDocroot {
		t.Fatalf("session = %+v ok=%v, want site_alice jailed to %s", sess, ok, ssoDocroot)
	}

	// Single use: replaying the same token fails and sets no cookie.
	again := postForm(f.w.handleSSO, ssoHost, "/sso", url.Values{"t": {tok}})
	if again.Code == http.StatusSeeOther || sessionCookie(again) != nil || !strings.Contains(again.Body.String(), "expired") {
		t.Errorf("token was reusable: %d %q", again.Code, again.Body.String())
	}
}

func TestWebFTPSSORejections(t *testing.T) {
	f := newSSOFixture(t)
	ctx := context.Background()
	target := f.w.Targets(ctx, f.site)[0]

	// Wrong host: a token for one domain is useless on another, and is consumed.
	tok, _ := f.w.MintSSO(ctx, f.site, target)
	rec := postForm(f.w.handleSSO, "webftp.other.example.com", "/sso", url.Values{"t": {tok}})
	if sessionCookie(rec) != nil {
		t.Error("token was accepted on a different host")
	}
	if sessionCookie(postForm(f.w.handleSSO, ssoHost, "/sso", url.Values{"t": {tok}})) != nil {
		t.Error("token survived a failed redemption on the wrong host")
	}

	// Expired.
	tok, _ = f.w.MintSSO(ctx, f.site, target)
	f.w.mu.Lock()
	e := f.w.ssoTokens[tok]
	e.expires = time.Now().Add(-time.Second)
	f.w.ssoTokens[tok] = e
	f.w.mu.Unlock()
	if sessionCookie(postForm(f.w.handleSSO, ssoHost, "/sso", url.Values{"t": {tok}})) != nil {
		t.Error("expired token was accepted")
	}

	// Empty / garbage tokens.
	for _, bad := range []string{"", "nope"} {
		if sessionCookie(postForm(f.w.handleSSO, ssoHost, "/sso", url.Values{"t": {bad}})) != nil {
			t.Errorf("token %q was accepted", bad)
		}
	}

	// Not a webftp host at all.
	tok, _ = f.w.MintSSO(ctx, f.site, target)
	if rec := postForm(f.w.handleSSO, "site.example.com", "/sso", url.Values{"t": {tok}}); rec.Code != http.StatusNotFound {
		t.Errorf("non-webftp host = %d, want 404", rec.Code)
	}
}

func TestWebFTPDisabledAccountCannotLogIn(t *testing.T) {
	f := newSSOFixture(t)
	ctx := context.Background()
	target := f.w.Targets(ctx, f.site)[0]

	// Disabled before the token is minted: no token.
	off := *f.site
	off.Enabled = false
	if _, err := f.w.MintSSO(ctx, &off, target); err == nil {
		t.Error("MintSSO issued a token for a disabled account")
	}

	// Disabled after minting: redemption must fail.
	tok, err := f.w.MintSSO(ctx, f.site, target)
	if err != nil {
		t.Fatal(err)
	}
	f.site.Enabled = false
	if err := f.st.UpdateFTPAccount(ctx, f.site); err != nil {
		t.Fatal(err)
	}
	rec := postForm(f.w.handleSSO, ssoHost, "/sso", url.Values{"t": {tok}})
	if sessionCookie(rec) != nil || !strings.Contains(rec.Body.String(), "disabled") {
		t.Errorf("disabled account got in via SSO: %d %q", rec.Code, rec.Body.String())
	}

	// And the normal password login must refuse it too (it used to only check the password).
	rec = postForm(f.w.handleLogin, ssoHost, "/login", url.Values{"username": {"site_alice"}, "password": {"correct-horse"}})
	if sessionCookie(rec) != nil || !strings.Contains(rec.Body.String(), "disabled") {
		t.Errorf("disabled account logged in with its password: %d %q", rec.Code, rec.Body.String())
	}

	// Re-enabled, the same password works again (the check is the flag, not something broken).
	f.site.Enabled = true
	if err := f.st.UpdateFTPAccount(ctx, f.site); err != nil {
		t.Fatal(err)
	}
	rec = postForm(f.w.handleLogin, ssoHost, "/login", url.Values{"username": {"site_alice"}, "password": {"correct-horse"}})
	if rec.Code != http.StatusSeeOther || sessionCookie(rec) == nil {
		t.Errorf("enabled account could not log in: %d %q", rec.Code, rec.Body.String())
	}
}
