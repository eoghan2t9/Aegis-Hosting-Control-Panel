package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	"aegis/internal/store"
	"aegis/internal/svc"
)

// --- SSL -----------------------------------------------------------------------

func (s *Server) handleSSLOrders(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var orders []*store.SSLOrder
	var err error
	if u.Role == store.RoleAdmin {
		orders, err = s.Store.ListSSLOrders(r.Context(), 0)
	} else {
		// Orders for the user's domains.
		domains, _ := s.Store.ListDomains(r.Context(), u.ID)
		var all []*store.SSLOrder
		for _, d := range domains {
			o, _ := s.Store.ListSSLOrders(r.Context(), d.ID)
			all = append(all, o...)
		}
		orders, err = all, nil
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

type issueReq struct {
	DomainID  int64  `json:"domain_id"`
	Challenge string `json:"challenge"` // http | dns
}

func (s *Server) handleSSLIssue(w http.ResponseWriter, r *http.Request) {
	var req issueReq
	if !readJSON(w, r, &req) {
		return
	}
	dom, _, err := s.Domains.DomainWithUser(r.Context(), req.DomainID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if !s.ownsDomain(r, dom) {
		writeErr(w, http.StatusForbidden, "cannot manage this domain")
		return
	}
	challenge := req.Challenge
	if challenge == "" {
		challenge = "http"
	}
	order, err := s.SSL.Issue(r.Context(), req.DomainID, challenge)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "ssl.issue", dom.Domain, "challenge="+challenge+" status="+order.Status)
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) handleSSLSelfSigned(w http.ResponseWriter, r *http.Request) {
	var req issueReq
	if !readJSON(w, r, &req) {
		return
	}
	dom, _, err := s.Domains.DomainWithUser(r.Context(), req.DomainID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if !s.ownsDomain(r, dom) {
		writeErr(w, http.StatusForbidden, "cannot manage this domain")
		return
	}
	order, err := s.SSL.SelfSigned(r.Context(), req.DomainID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "ssl.self-signed", dom.Domain, "")
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) handleSSLInfo(w http.ResponseWriter, r *http.Request) {
	id, err := pathIDFromQuery(r, "domain_id")
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
	if dom.SSLCertPath == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": false})
		return
	}
	info, err := svc.CertInfo(dom.SSLCertPath)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": true, "error": err.Error()})
		return
	}
	info["enabled"] = true
	info["provider"] = dom.SSLProvider
	writeJSON(w, http.StatusOK, info)
}

// --- PHP -------------------------------------------------------------------------

func (s *Server) handlePHPVersions(w http.ResponseWriter, r *http.Request) {
	versions := s.PHP.Versions()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"versions": versions,
		"latest":   s.PHP.Latest(),
		"active":   s.Cfg.WebServer.Server,
	})
}

// --- web server -------------------------------------------------------------------

func (s *Server) handleWebServerStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"active":    s.Web.Active(),
		"available": s.Web.Available(),
		"tuning":    s.tuningSummary(),
	})
}

func (s *Server) handleWebServerSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Server string `json:"server"`
		Install bool `json:"install"` // install the package when missing
	}
	if !readJSON(w, r, &req) {
		return
	}
	switch req.Server {
	case "nginx", "apache", "caddy", "go":
	default:
		writeErr(w, http.StatusBadRequest, "invalid server (nginx|apache|caddy|go)")
		return
	}
	if req.Server != "go" && !s.Web.IsAvailable(req.Server) {
		if !req.Install {
			writeErr(w, http.StatusBadRequest, req.Server+" is not installed on this server")
			return
		}
		// Install the distro package on demand, then re-check. This is what
		// makes an installed-but-unlisted server (or a fresh pick from the
		// Runtime page) actually switchable without shelling in by hand.
		if err := s.Web.Install(req.Server); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !s.Web.IsAvailable(req.Server) {
			writeErr(w, http.StatusInternalServerError, req.Server+" was installed but is still not detected")
			return
		}
	}
	s.Cfg.WebServer.Server = req.Server
	if err := s.Cfg.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "webserver.set", req.Server, "")
	writeJSON(w, http.StatusOK, map[string]string{"active": req.Server})
}

func (s *Server) handleWebServerPreview(w http.ResponseWriter, r *http.Request) {
	id, err := pathIDFromQuery(r, "domain_id")
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
	cfg, err := s.Web.Generate(dom, aliases, user.Username)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"server": s.Web.Active(),
		"config": cfg,
	})
}

// --- FTP --------------------------------------------------------------------------

func (s *Server) handleFTPList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	accounts, err := s.Store.ListFTPAccounts(r.Context(), u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

type ftpCreateReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleFTPCreate(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if !pkgAllowsFTP(s, u) {
		writeErr(w, http.StatusForbidden, "your package does not allow extra FTP accounts")
		return
	}
	var req ftpCreateReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Username == "" {
		writeErr(w, http.StatusBadRequest, "username is required")
		return
	}
	acct, err := s.FTP.Create(r.Context(), u, req.Username, req.Password, true)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "ftp.create", acct.Username, "")
	writeJSON(w, http.StatusCreated, acct)
}

func (s *Server) handleFTPPassword(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	acct, err := s.Store.GetFTPAccount(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	}
	if acct.UserID != userFrom(r).ID && userFrom(r).Role != store.RoleAdmin {
		writeErr(w, http.StatusForbidden, "cannot manage this account")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.FTP.ResetPassword(r.Context(), acct, req.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "ftp.password", acct.Username, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFTPToggle(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	acct, err := s.Store.GetFTPAccount(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	}
	if acct.UserID != userFrom(r).ID && userFrom(r).Role != store.RoleAdmin {
		writeErr(w, http.StatusForbidden, "cannot manage this account")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.FTP.ToggleEnabled(r.Context(), acct, req.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, acct)
}

func (s *Server) handleFTPDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	acct, err := s.Store.GetFTPAccount(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	}
	if acct.UserID != userFrom(r).ID && userFrom(r).Role != store.RoleAdmin {
		writeErr(w, http.StatusForbidden, "cannot manage this account")
		return
	}
	if err := s.FTP.Delete(r.Context(), acct); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "ftp.delete", acct.Username, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- databases ----------------------------------------------------------------------

func (s *Server) handleDBServers(w http.ResponseWriter, r *http.Request) {
	servers := s.DB.Servers(r.Context())
	writeJSON(w, http.StatusOK, servers)
}

func (s *Server) handleDBList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var rows []*store.Database
	var err error
	if u.Role == store.RoleAdmin {
		rows, err = s.Store.ListDatabases(r.Context(), 0)
	} else {
		rows, err = s.Store.ListDatabases(r.Context(), u.ID)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

type dbCreateReq struct {
	Server string `json:"server"`
	Name   string `json:"name"`
}

func (s *Server) handleDBCreate(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if !pkgAllowsDB(s, u) {
		writeErr(w, http.StatusForbidden, "your package does not allow databases")
		return
	}
	var req dbCreateReq
	if !readJSON(w, r, &req) {
		return
	}
	row, err := s.DB.Create(r.Context(), u, req.Server, req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "db.create", row.Name, "server="+row.Server)
	writeJSON(w, http.StatusCreated, row)
}

func (s *Server) handleDBDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := s.Store.GetDatabase(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "database not found")
		return
	}
	u := userFrom(r)
	if row.UserID != u.ID && u.Role != store.RoleAdmin {
		writeErr(w, http.StatusForbidden, "cannot manage this database")
		return
	}
	if err := s.DB.Drop(r.Context(), row); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "db.delete", row.Name, "server="+row.Server)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDBDump(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := s.Store.GetDatabase(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "database not found")
		return
	}
	u := userFrom(r)
	if row.UserID != u.ID && u.Role != store.RoleAdmin {
		writeErr(w, http.StatusForbidden, "cannot access this database")
		return
	}
	tmp := filepath.Join(os.TempDir(), "aegis-dump-"+row.Name+".sql")
	if err := s.DB.Dump(r.Context(), row, tmp); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+row.Name+".sql")
	http.ServeFile(w, r, tmp)
	_ = os.Remove(tmp)
}

// --- package feature helpers ---------------------------------------------------------

func pkgAllowsFTP(s *Server, u *store.User) bool {
	if u.Role == store.RoleAdmin {
		return true
	}
	pkg, err := s.Store.GetPackage(context.Background(), u.PackageID)
	if err != nil {
		return true
	}
	usage, err := s.Store.PackageUsage(context.Background(), u.ID)
	if err != nil {
		return true
	}
	if pkg.MaxFTPAccounts > 0 && usage.FTPAccounts >= pkg.MaxFTPAccounts {
		return false
	}
	return true
}

func pkgAllowsDB(s *Server, u *store.User) bool {
	if u.Role == store.RoleAdmin {
		return true
	}
	_, err := s.Store.GetPackage(context.Background(), u.PackageID)
	return err == nil
}
