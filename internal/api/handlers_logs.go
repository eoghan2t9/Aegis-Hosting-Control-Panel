package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"aegis/internal/svc"
)

// domainLogFile resolves the log file for a domain. The web servers write
// under their own dirs; nginx is the canonical location the panel configures
// for every generated vhost, caddy logs to its own dir with the same
// per-domain name.
func (s *Server) domainLogFile(domain, kind string) string {
	switch kind {
	case "error":
		return "/var/log/nginx/" + domain + ".error.log"
	case "caddy":
		return "/var/log/caddy/" + domain + ".log"
	default: // "access"
		return "/var/log/nginx/" + domain + ".access.log"
	}
}

// handleDomainLogs tails a domain's access or error log. Ownership mirrors
// the other domain routes (admins all, resellers their clients, users own).
// Query params: kind=access|error|caddy (default access), lines=1..2000.
func (s *Server) handleDomainLogs(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	dom, _, err := s.Domains.DomainWithUser(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if !s.ownsDomain(r, dom) {
		writeErr(w, http.StatusForbidden, "cannot access this domain")
		return
	}
	kind := r.URL.Query().Get("kind")
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			lines = n
		}
	}
	file := s.domainLogFile(dom.Domain, kind)
	data, err := tailLog(file, lines)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"lines": []string{}, "file": file, "exists": false})
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": data, "file": file, "exists": true})
}

// tailLog wraps svc.TailLines with a path guard: the file argument is
// always constructed internally from the domain name (validated hostnames),
// but the guard keeps this helper honest if reused later.
func tailLog(file string, lines int) ([]string, error) {
	dir := filepath.Dir(file)
	if dir != "/var/log/nginx" && dir != "/var/log/caddy" {
		return nil, os.ErrInvalid
	}
	return svc.TailLines(file, lines)
}
