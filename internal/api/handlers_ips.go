package api

import (
	"net/http"

	"aegis/internal/svc"
)

// --- IP pool (admin only, see server.go's route registration) ------------------

func (s *Server) handleIPsList(w http.ResponseWriter, r *http.Request) {
	ips, err := s.IPs.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ips)
}

// handleIPsDetect lists IPs actually configured on a local interface right
// now — used by the "add IP" dialog so an admin can pick one that will
// actually pass Create's validation, instead of guessing.
func (s *Server) handleIPsDetect(w http.ResponseWriter, r *http.Request) {
	addrs, err := svc.DetectLocalIPs()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"addresses": addrs})
}

type createIPReq struct {
	Address string `json:"address"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
}

func (s *Server) handleIPsCreate(w http.ResponseWriter, r *http.Request) {
	var req createIPReq
	if !readJSON(w, r, &req) {
		return
	}
	ip, err := s.IPs.Create(r.Context(), req.Address, req.Label, req.Kind)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "ip.create", ip.Address, "kind="+ip.Kind)
	writeJSON(w, http.StatusCreated, ip)
}

type updateIPReq struct {
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

func (s *Server) handleIPsUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var req updateIPReq
	if !readJSON(w, r, &req) {
		return
	}
	ip, err := s.IPs.Update(r.Context(), id, req.Label, req.Kind)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "ip.update", ip.Address, "kind="+ip.Kind)
	writeJSON(w, http.StatusOK, ip)
}

func (s *Server) handleIPsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.IPs.Delete(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "ip.delete", "", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
