package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"aegis/internal/store"
	"aegis/internal/svc"
)

// previewSessionTTL matches webftp's own session lifetime (see
// internal/svc/webftp.go) — long enough to browse a site, short enough that
// a stale cookie doesn't linger.
const previewSessionTTL = 2 * time.Hour

type previewSession struct {
	domainID int64
	userID   int64
	expires  time.Time
}

func (s *Server) previewCookieName(domainID int64) string {
	return fmt.Sprintf("aegis_preview_%d", domainID)
}

func (s *Server) newPreviewSession(domainID, userID int64) string {
	tok, err := svc.RandomString(40)
	if err != nil {
		return ""
	}
	s.previewMu.Lock()
	s.previewSessions[tok] = previewSession{domainID: domainID, userID: userID, expires: time.Now().Add(previewSessionTTL)}
	s.previewMu.Unlock()
	return tok
}

// previewSessionUser resolves the acting user from a previously-minted
// preview cookie, scoped to this exact domain — the fallback that lets a
// previewed site's own (relative) links keep working after the first click,
// since a plain top-level navigation can't carry a query-string token or
// Authorization header forward the way the initial "Preview site" open does.
func (s *Server) previewSessionUser(r *http.Request, domainID int64) (*store.User, bool) {
	c, err := r.Cookie(s.previewCookieName(domainID))
	if err != nil || c.Value == "" {
		return nil, false
	}
	s.previewMu.Lock()
	sess, ok := s.previewSessions[c.Value]
	s.previewMu.Unlock()
	if !ok || time.Now().After(sess.expires) || sess.domainID != domainID {
		return nil, false
	}
	user, err := s.Store.GetUserByID(r.Context(), sess.userID)
	if err != nil {
		return nil, false
	}
	return user, true
}

// handlePreview serves a domain's site through the panel itself so the owner
// can test it before DNS has propagated (temporary URL, like Plesk's site
// preview). It reuses the native Go server's static+FastCGI serving with a
// route built from the store, so PHP sites render exactly as they will once
// the domain's own vhost takes over.
//
// Auth: a bearer token (header, or ?token= query — the same fallback the
// WebSocket endpoints rely on, so the initial preview link can be opened in
// a new tab) mints a short-lived, per-domain session cookie on success; any
// request without a bearer token falls back to that cookie instead. This is
// what lets a previewed page's own relative links keep working when
// clicked — a plain navigation can't carry a query string or header
// forward, but the browser attaches the cookie automatically. Responses are
// sandboxed with an opaque-origin CSP so previewed apps run
// same-origin-isolated from the panel: no access to panel cookies,
// localStorage or the API. As with other read endpoints, suspended users
// are refused.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	// /api/domains/{id}/preview/{rest...}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// [api, domains, {id}, preview, rest...]
	if len(parts) < 4 || parts[1] != "domains" || parts[3] != "preview" {
		http.NotFound(w, r)
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid domain id")
		return
	}

	var user *store.User
	fromBearer := false
	if token := bearerToken(r); token != "" {
		u, _, status, msg := s.resolveBearer(r, token)
		if status != 0 {
			writeErr(w, status, msg)
			return
		}
		user, fromBearer = u, true
	} else if u, ok := s.previewSessionUser(r, id); ok {
		user = u
	} else {
		writeErr(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), ctxUser, user))

	dom, err := s.Store.GetDomain(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if !s.ownsDomain(r, dom) {
		writeErr(w, http.StatusForbidden, "not your domain")
		return
	}
	owner, err := s.Store.GetUserByID(r.Context(), dom.UserID)
	if err != nil || owner.Status == store.StatusSuspended {
		writeErr(w, http.StatusForbidden, "account suspended")
		return
	}

	if fromBearer {
		if tok := s.newPreviewSession(id, user.ID); tok != "" {
			// Path must include the panel's own URL prefix (AEGIS_PANEL_BASE,
			// e.g. "/aegis") since that's what the client actually sees and
			// what cookie-path matching is evaluated against — omitting it
			// means the browser never sends the cookie back on navigation.
			http.SetCookie(w, &http.Cookie{
				Name: s.previewCookieName(id), Value: tok,
				Path:     s.Cfg.PanelBase + fmt.Sprintf("/api/domains/%d/preview/", id),
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
				MaxAge: int(previewSessionTTL.Seconds()),
			})
		}
	}

	route := svc.GoRoute{
		Domain:     dom.Domain,
		Root:       dom.DocumentRoot,
		PHPVersion: dom.PHPVersion,
		Socket:     s.PHP.SocketPath(dom.Domain),
	}

	// Only the remainder after .../preview counts as the site path; the browser
	// resolves relative links against the /preview/{id}/ prefix, so the site's
	// own relative URLs keep working inside the preview.
	//	parts = [api, domains, {id}, preview, site-path...]
	// parts was built from strings.Trim(r.URL.Path, "/"), which discards a
	// trailing slash before we ever see it — restore it here, since
	// serveGoRoute's own directory-redirect logic depends on knowing whether
	// the original preview URL actually had one.
	hadTrailingSlash := strings.HasSuffix(r.URL.Path, "/")
	rest := ""
	if len(parts) > 4 {
		rest = strings.Join(parts[4:], "/")
	}
	if hadTrailingSlash && rest != "" {
		rest += "/"
	}
	r.URL.Path = "/" + rest

	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups")
	w.Header().Set("X-Robots-Tag", "noindex")
	s.Web.ServePreviewRoute(w, r, route)
}
