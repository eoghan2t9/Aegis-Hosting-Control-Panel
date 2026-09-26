package svc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"aegis/internal/store"
)

// A suspended account cannot use the browser file manager any way it has:
// password login, panel-issued login links, or an already-open session.
func TestWebFTPRefusesSuspendedAccount(t *testing.T) {
	ctx := context.Background()
	f := newSSOFixture(t)
	target := f.w.Targets(ctx, f.site)[0]

	// Before suspension: a live session and an unredeemed login link.
	tok, err := f.w.MintSSO(ctx, f.site, target)
	if err != nil {
		t.Fatal(err)
	}
	rec := postForm(f.w.handleSSO, ssoHost, "/sso", url.Values{"t": {tok}})
	live := sessionCookie(rec)
	if live == nil {
		t.Fatal("could not establish a session before suspending")
	}
	pending, err := f.w.MintSSO(ctx, f.site, target)
	if err != nil {
		t.Fatal(err)
	}
	get := func(c *http.Cookie) bool {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = ssoHost
		req.AddCookie(c)
		_, ok := f.w.sessionFor(req)
		return ok
	}
	if !get(live) {
		t.Fatal("session should be valid before suspension")
	}

	// Suspend (manually) — the store flags it; no panel session involved.
	if err := f.st.SetUserStatus(ctx, f.site.UserID, store.StatusSuspended); err != nil {
		t.Fatal(err)
	}

	// The open session stops working immediately, before any RevokeUser runs.
	if get(live) {
		t.Error("an existing Web FTP session survived the suspension")
	}
	// Login with the right password is refused with a clear message.
	rec = postForm(f.w.handleLogin, ssoHost, "/login", url.Values{"username": {"site_alice"}, "password": {"correct-horse"}})
	if sessionCookie(rec) != nil || !strings.Contains(rec.Body.String(), "suspended") {
		t.Errorf("suspended account logged in with its password: %d %q", rec.Code, rec.Body.String())
	}
	// A link minted earlier cannot be redeemed, and no new one can be minted.
	rec = postForm(f.w.handleSSO, ssoHost, "/sso", url.Values{"t": {pending}})
	if sessionCookie(rec) != nil || !strings.Contains(rec.Body.String(), "suspended") {
		t.Errorf("a pre-suspension login link still worked: %d %q", rec.Code, rec.Body.String())
	}
	if _, err := f.w.MintSSO(ctx, f.site, target); err == nil {
		t.Error("MintSSO issued a token for a suspended account")
	}

	// A quota suspension is not a lock-out: the user must keep access to free space.
	if err := f.st.SetSuspendedByQuota(ctx, f.site.UserID, true); err != nil {
		t.Fatal(err)
	}
	rec = postForm(f.w.handleLogin, ssoHost, "/login", url.Values{"username": {"site_alice"}, "password": {"correct-horse"}})
	if rec.Code != http.StatusSeeOther || sessionCookie(rec) == nil {
		t.Errorf("a quota-suspended user was locked out of Web FTP: %d %q", rec.Code, rec.Body.String())
	}
}

func TestWebFTPRevokeUserDropsSessionsAndLinks(t *testing.T) {
	ctx := context.Background()
	f := newSSOFixture(t)
	target := f.w.Targets(ctx, f.site)[0]

	tok, _ := f.w.MintSSO(ctx, f.site, target)
	c := sessionCookie(postForm(f.w.handleSSO, ssoHost, "/sso", url.Values{"t": {tok}}))
	pending, _ := f.w.MintSSO(ctx, f.site, target)

	f.w.RevokeUser(f.site.UserID)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = ssoHost
	req.AddCookie(c)
	if _, ok := f.w.sessionFor(req); ok {
		t.Error("RevokeUser left a session valid")
	}
	if _, ok := f.w.redeemSSO(pending, ssoHost); ok {
		t.Error("RevokeUser left a login link redeemable")
	}
}
