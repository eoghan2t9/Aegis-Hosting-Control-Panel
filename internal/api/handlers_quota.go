package api

import (
	"net/http"

	"aegis/internal/store"
)

func (s *Server) handleQuotaUsage(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	target := u
	if idStr := r.URL.Query().Get("user_id"); idStr != "" {
		id, err := pathIDFromQuery(r, "user_id")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid user_id")
			return
		}
		if id != u.ID && u.Role != store.RoleAdmin {
			writeErr(w, http.StatusForbidden, "cannot view this account's usage")
			return
		}
		other, err := s.Store.GetUserByID(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		target = other
	}
	usage, err := s.Quota.UsageFor(r.Context(), target)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

// handleQuotaEnforce lets an admin run the enforcement pass on demand,
// instead of waiting for the next 15-minute tick.
func (s *Server) handleQuotaEnforce(w http.ResponseWriter, r *http.Request) {
	if err := s.Quota.Enforce(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "quota.enforce", "", "manual run")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
