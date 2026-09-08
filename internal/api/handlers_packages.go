package api

import "net/http"

func (s *Server) handleDistroInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Packages.DistroInfo(r.Context()))
}

func (s *Server) handleUpdatesList(w http.ResponseWriter, r *http.Request) {
	updates, err := s.Packages.ListCached(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"updates":     updates,
		"pkg_manager": s.Packages.ManagerName(),
	})
}

func (s *Server) handleUpdatesCheck(w http.ResponseWriter, r *http.Request) {
	updates, err := s.Packages.CheckUpdates(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "updates.check", "", "")
	writeJSON(w, http.StatusOK, map[string]any{
		"updates":     updates,
		"pkg_manager": s.Packages.ManagerName(),
	})
}

type updatesApplyReq struct {
	Names []string `json:"names"`
}

func (s *Server) handleUpdatesApply(w http.ResponseWriter, r *http.Request) {
	var req updatesApplyReq
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Packages.ApplyUpdates(r.Context(), req.Names); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "updates.apply", "", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handlePackagesSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q is required")
		return
	}
	results, err := s.Packages.Search(r.Context(), q)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

type packagesInstallReq struct {
	Name string `json:"name"`
}

func (s *Server) handlePackagesInstall(w http.ResponseWriter, r *http.Request) {
	var req packagesInstallReq
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Packages.Install(r.Context(), req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "packages.install", req.Name, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
