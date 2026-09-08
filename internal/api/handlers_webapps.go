package api

import (
	"net/http"
	"strings"
)

type installReq struct {
	App string `json:"app"` // wordpress | laravel
}

func (s *Server) handleDomainInstall(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	dom, err := s.Store.GetDomain(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if !s.ownsDomain(r, dom) {
		writeErr(w, http.StatusForbidden, "cannot manage this domain")
		return
	}
	var req installReq
	if !readJSON(w, r, &req) {
		return
	}
	owner, err := s.Store.GetUserByID(r.Context(), dom.UserID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "domain owner not found")
		return
	}
	switch req.App {
	case "wordpress":
		if err := s.WebApps.InstallWordPress(r.Context(), dom, owner); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
	case "laravel":
		if err := s.WebApps.InstallLaravel(r.Context(), dom, owner); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "app must be 'wordpress' or 'laravel'")
		return
	}
	s.audit(r, "domain.install", dom.Domain, req.App)
	writeJSON(w, http.StatusOK, map[string]string{"status": "installed"})
}

type wpCLIReq struct {
	Args []string `json:"args"`
}

func (s *Server) handleDomainWPCLI(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	dom, err := s.Store.GetDomain(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if !s.ownsDomain(r, dom) {
		writeErr(w, http.StatusForbidden, "cannot manage this domain")
		return
	}
	var req wpCLIReq
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Args) == 0 {
		writeErr(w, http.StatusBadRequest, "args is required")
		return
	}
	out, err := s.WebApps.RunWPCLI(r.Context(), dom, req.Args)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error()+"\n"+out)
		return
	}
	s.audit(r, "domain.wp-cli", dom.Domain, strings.Join(req.Args, " "))
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}
