package api

import "net/http"

func (s *Server) handleSecurityAttempts(w http.ResponseWriter, r *http.Request) {
	attempts, err := s.Security.RecentAttempts(r.Context(), 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, attempts)
}

func (s *Server) handleSecurityBans(w http.ResponseWriter, r *http.Request) {
	ips, err := s.Security.BannedIPs(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ips)
}

func (s *Server) handleSecurityUnban(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IP string `json:"ip"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.IP == "" {
		writeErr(w, http.StatusBadRequest, "ip is required")
		return
	}
	if err := s.Security.Unban(r.Context(), req.IP); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "security.unban", req.IP, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
