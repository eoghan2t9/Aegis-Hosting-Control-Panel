package api

import (
	"net/http"
	"strings"

	"aegis/internal/store"
	"aegis/internal/svc"
)

// handlePreview serves a domain's site through the panel itself so the owner
// can test it before DNS has propagated (temporary URL, like Plesk's site
// preview). It reuses the native Go server's static+FastCGI serving with a
// route built from the store, so PHP sites render exactly as they will once
// the domain's own vhost takes over.
//
// Auth uses the standard bearerToken (header, or ?token= query — the same
// fallback the WebSocket endpoints rely on, so preview links can be opened in
// a new tab). Responses are sandboxed with an opaque-origin CSP so previewed
// apps run same-origin-isolated from the panel: no access to panel cookies,
// localStorage or the API. As with other read endpoints, suspended users are
// refused.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	// /api/domains/{id}/preview/{rest...}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// [api, domains, {id}, preview, rest...]
	if len(parts) < 4 || parts[1] != "domains" || parts[3] != "preview" {
		http.NotFound(w, r)
		return
	}
	id, err := pathID(r, parts[2])
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid domain id")
		return
	}
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
	rest := ""
	if len(parts) > 4 {
		rest = strings.Join(parts[4:], "/")
	}
	r.URL.Path = "/" + rest

	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups")
	w.Header().Set("X-Robots-Tag", "noindex")
	s.Web.ServePreviewRoute(w, r, route)
}
