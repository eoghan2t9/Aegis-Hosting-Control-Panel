package api

import (
	"net/http"
	"time"

	"aegis/internal/store"
)

func (s *Server) handleTokensList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	tokens, err := s.Tokens.List(r.Context(), u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

type tokenCreateReq struct {
	Label   string `json:"label"`
	TTLDays int    `json:"ttl_days"` // 0 = never expires
}

func (s *Server) handleTokensCreate(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var req tokenCreateReq
	if !readJSON(w, r, &req) {
		return
	}
	var ttl time.Duration
	if req.TTLDays > 0 {
		ttl = time.Duration(req.TTLDays) * 24 * time.Hour
	}
	t, raw, err := s.Tokens.Create(r.Context(), u, req.Label, ttl)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "token.create", t.Label, "")
	writeJSON(w, http.StatusCreated, map[string]interface{}{"token": t, "raw": raw})
}

func (s *Server) handleTokensDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := s.Store.GetAPIToken(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "token not found")
		return
	}
	u := userFrom(r)
	if t.UserID != u.ID && u.Role != store.RoleAdmin {
		writeErr(w, http.StatusForbidden, "cannot manage this token")
		return
	}
	if err := s.Tokens.Delete(r.Context(), t.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "token.delete", t.Label, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
