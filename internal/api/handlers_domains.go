package api

import (
	"net/http"

	"aegis/internal/store"
	"aegis/internal/svc"
)

func svcCreateOptions(phpVersion, webserver string) svc.CreateOptions {
	return svc.CreateOptions{PHPVersion: phpVersion, WebServer: webserver}
}

func validAlias(a string) bool {
	return svc.ValidDomain(a)
}

// ownsDomain reports whether the acting user can manage the given domain.
func (s *Server) ownsDomain(r *http.Request, dom *store.Domain) bool {
	u := userFrom(r)
	if u == nil {
		return false
	}
	if u.Role == store.RoleAdmin {
		return true
	}
	if u.Role == store.RoleReseller {
		// Resellers manage domains of users they created.
		owner, err := s.Store.GetUserByID(r.Context(), dom.UserID)
		if err != nil {
			return false
		}
		return owner.OwnerID == u.ID
	}
	return dom.UserID == u.ID
}

func (s *Server) handleDomainsList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var domains []*store.Domain
	var err error
	if u.Role == store.RoleAdmin {
		domains, err = s.Store.ListDomains(r.Context(), 0)
	} else {
		domains, err = s.Store.ListDomains(r.Context(), u.ID)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domains)
}

type createDomainReq struct {
	Domain     string `json:"domain"`
	PHPVersion string `json:"php_version"`
	WebServer  string `json:"webserver"`
	UserID     int64  `json:"user_id,omitempty"` // admin: create for another user
}

func (s *Server) handleDomainsCreate(w http.ResponseWriter, r *http.Request) {
	actor := userFrom(r)
	var req createDomainReq
	if !readJSON(w, r, &req) {
		return
	}
	owner := actor
	if req.UserID > 0 && actor.Role == store.RoleAdmin {
		u, err := s.Store.GetUserByID(r.Context(), req.UserID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		owner = u
	} else if req.UserID > 0 {
		writeErr(w, http.StatusForbidden, "only admins can create domains for other users")
		return
	}
	dom, err := s.Domains.Create(r.Context(), owner, req.Domain, svcCreateOptions(req.PHPVersion, req.WebServer))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "domain.create", dom.Domain, "user="+owner.Username)
	writeJSON(w, http.StatusCreated, dom)
}

func (s *Server) handleDomainsGet(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	dom, user, err := s.Domains.DomainWithUser(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if !s.ownsDomain(r, dom) {
		writeErr(w, http.StatusForbidden, "cannot access this domain")
		return
	}
	aliases, _ := s.Store.ListAliases(r.Context(), id)
	zone, _ := s.Store.GetZoneByDomain(r.Context(), id)
	orders, _ := s.Store.ListSSLOrders(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"domain":  dom,
		"aliases": aliases,
		"zone":    zone,
		"ssl":     orders,
		"user":    publicUser(user),
	})
}

type updateDomainReq struct {
	PHPVersion   *string `json:"php_version"`
	WebServer    *string `json:"webserver"`
	DocumentRoot *string `json:"document_root"`
	SSLAutoRenew *bool   `json:"ssl_auto_renew"`
}

func (s *Server) handleDomainsUpdate(w http.ResponseWriter, r *http.Request) {
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
		writeErr(w, http.StatusForbidden, "cannot manage this domain")
		return
	}
	var req updateDomainReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.PHPVersion != nil {
		if *req.PHPVersion != "" && !s.PHP.Has(*req.PHPVersion) {
			writeErr(w, http.StatusBadRequest, "php version not installed")
			return
		}
		dom.PHPVersion = *req.PHPVersion
	}
	if req.WebServer != nil {
		dom.WebServer = *req.WebServer
	}
	if req.DocumentRoot != nil {
		dom.DocumentRoot = *req.DocumentRoot
	}
	if req.SSLAutoRenew != nil {
		dom.SSLAutoRenew = *req.SSLAutoRenew
	}
	if err := s.Store.UpdateDomain(r.Context(), dom); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Domains.Apply(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadGateway, "config apply: "+err.Error())
		return
	}
	s.audit(r, "domain.update", dom.Domain, "")
	writeJSON(w, http.StatusOK, dom)
}

func (s *Server) handleDomainsDelete(w http.ResponseWriter, r *http.Request) {
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
		writeErr(w, http.StatusForbidden, "cannot manage this domain")
		return
	}
	if err := s.Domains.Delete(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "domain.delete", dom.Domain, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "kept_files": "true"})
}

func (s *Server) handleDomainsApply(w http.ResponseWriter, r *http.Request) {
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
		writeErr(w, http.StatusForbidden, "cannot manage this domain")
		return
	}
	if err := s.Domains.Apply(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "domain.apply", dom.Domain, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
}

func (s *Server) handleAliasesAdd(w http.ResponseWriter, r *http.Request) {
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
		writeErr(w, http.StatusForbidden, "cannot manage this domain")
		return
	}
	var req struct {
		Alias string `json:"alias"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !validAlias(req.Alias) {
		writeErr(w, http.StatusBadRequest, "invalid alias")
		return
	}
	if err := s.Store.AddAlias(r.Context(), id, req.Alias); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.Domains.Apply(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "domain.alias", req.Alias, "domain="+dom.Domain)
	writeJSON(w, http.StatusCreated, map[string]string{"alias": req.Alias})
}

func (s *Server) handleAliasesRemove(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	alias := r.PathValue("alias")
	dom, _, err := s.Domains.DomainWithUser(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if !s.ownsDomain(r, dom) {
		writeErr(w, http.StatusForbidden, "cannot manage this domain")
		return
	}
	if err := s.Store.RemoveAlias(r.Context(), id, alias); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.Domains.Apply(r.Context(), id)
	s.audit(r, "domain.alias-remove", alias, "domain="+dom.Domain)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
