package api

import (
	"net/http"
	"strconv"

	"aegis/internal/store"
	"aegis/internal/svc"
)

// ownsContainer loads a container by path id and checks the caller may
// manage it — same admin/reseller/self shape as ownsDomain.
func (s *Server) ownsContainer(r *http.Request) (*store.Container, bool) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, false
	}
	c, err := s.Docker.Get(r.Context(), id)
	if err != nil {
		return nil, false
	}
	u := userFrom(r)
	if u.Role == store.RoleAdmin {
		return c, true
	}
	if u.Role == store.RoleReseller {
		owner, err := s.Store.GetUserByID(r.Context(), c.UserID)
		if err != nil || owner.OwnerID != u.ID {
			return nil, false
		}
		return c, true
	}
	if c.UserID != u.ID {
		return nil, false
	}
	return c, true
}

func (s *Server) handleContainersList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var containers []*store.Container
	var err error
	if u.Role == store.RoleAdmin {
		containers, err = s.Docker.List(r.Context(), 0)
	} else {
		containers, err = s.Docker.List(r.Context(), u.ID)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, containers)
}

func (s *Server) handleContainersCreate(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var req svc.CreateContainerRequest
	if !readJSON(w, r, &req) {
		return
	}
	c, err := s.Docker.Create(r.Context(), u, req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "container.create", c.Name, c.Image)
	writeJSON(w, http.StatusCreated, c)
}

type composeImportReq struct {
	Compose string `json:"compose"`
	Service string `json:"service,omitempty"`
}

// handleContainersImportCompose parses a pasted docker-compose.yml into a
// CreateContainerRequest-shaped preview for the "New container" form. Purely
// read-only — no container is created, no filesystem path is touched; see
// svc.ParseCompose.
func (s *Server) handleContainersImportCompose(w http.ResponseWriter, r *http.Request) {
	var req composeImportReq
	if !readJSON(w, r, &req) {
		return
	}
	result, err := svc.ParseCompose(req.Compose, req.Service)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleContainersGet(w http.ResponseWriter, r *http.Request) {
	c, ok := s.ownsContainer(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "container not found")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleContainersStart(w http.ResponseWriter, r *http.Request) {
	c, ok := s.ownsContainer(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "container not found")
		return
	}
	if err := s.Docker.Start(r.Context(), c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "container.start", c.Name, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleContainersStop(w http.ResponseWriter, r *http.Request) {
	c, ok := s.ownsContainer(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "container not found")
		return
	}
	if err := s.Docker.Stop(r.Context(), c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "container.stop", c.Name, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleContainersRestart(w http.ResponseWriter, r *http.Request) {
	c, ok := s.ownsContainer(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "container not found")
		return
	}
	if err := s.Docker.Restart(r.Context(), c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "container.restart", c.Name, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleContainersRecreate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.ownsContainer(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "container not found")
		return
	}
	if err := s.Docker.Recreate(r.Context(), c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "container.recreate", c.Name, c.Image)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleContainersDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := s.ownsContainer(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "container not found")
		return
	}
	if err := s.Docker.Delete(r.Context(), c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "container.delete", c.Name, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleContainersLogs(w http.ResponseWriter, r *http.Request) {
	c, ok := s.ownsContainer(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "container not found")
		return
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	log, err := s.Docker.Logs(r.Context(), c, tail)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"log": log})
}

func (s *Server) handleContainersStats(w http.ResponseWriter, r *http.Request) {
	c, ok := s.ownsContainer(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "container not found")
		return
	}
	stats, err := s.Docker.Stats(r.Context(), c)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// --- host-level docker setup (admin only) --------------------------------------

func (s *Server) handleDockerStatus(w http.ResponseWriter, r *http.Request) {
	installed, daemonUp, version := s.Docker.Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"installed": installed,
		"daemon_up": daemonUp,
		"version":   version,
	})
}

func (s *Server) handleDockerInstall(w http.ResponseWriter, r *http.Request) {
	if err := s.Docker.Install(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "docker.install", "", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
