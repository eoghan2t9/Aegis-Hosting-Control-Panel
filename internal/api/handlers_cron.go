package api

import (
	"net/http"

	"aegis/internal/store"
)

// --- cron jobs ----------------------------------------------------------------

func (s *Server) handleCronList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var jobs []*store.CronJob
	var err error
	if u.Role == store.RoleAdmin {
		jobs, err = s.Store.ListCronJobs(r.Context(), 0)
	} else {
		jobs, err = s.Store.ListCronJobs(r.Context(), u.ID)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

type cronCreateReq struct {
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
}

func (s *Server) handleCronCreate(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var req cronCreateReq
	if !readJSON(w, r, &req) {
		return
	}
	job, err := s.Cron.Create(r.Context(), u, req.Schedule, req.Command)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "cron.create", req.Schedule, req.Command)
	writeJSON(w, http.StatusCreated, job)
}

// ownsCronJob loads a job by path id and checks the caller may manage it.
func (s *Server) ownsCronJob(r *http.Request) (*store.CronJob, bool) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, false
	}
	job, err := s.Store.GetCronJob(r.Context(), id)
	if err != nil {
		return nil, false
	}
	u := userFrom(r)
	if job.UserID != u.ID && u.Role != store.RoleAdmin {
		return nil, false
	}
	return job, true
}

func (s *Server) handleCronUpdate(w http.ResponseWriter, r *http.Request) {
	job, ok := s.ownsCronJob(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	var req cronCreateReq
	if !readJSON(w, r, &req) {
		return
	}
	owner := userFrom(r)
	if owner.Role == store.RoleAdmin && owner.ID != job.UserID {
		if u, err := s.Store.GetUserByID(r.Context(), job.UserID); err == nil {
			owner = u
		}
	}
	if err := s.Cron.Update(r.Context(), owner, job, req.Schedule, req.Command); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "cron.update", req.Schedule, req.Command)
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleCronToggle(w http.ResponseWriter, r *http.Request) {
	job, ok := s.ownsCronJob(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	owner := userFrom(r)
	if owner.Role == store.RoleAdmin && owner.ID != job.UserID {
		if u, err := s.Store.GetUserByID(r.Context(), job.UserID); err == nil {
			owner = u
		}
	}
	if err := s.Cron.Toggle(r.Context(), owner, job, req.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleCronDelete(w http.ResponseWriter, r *http.Request) {
	job, ok := s.ownsCronJob(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	owner := userFrom(r)
	if owner.Role == store.RoleAdmin && owner.ID != job.UserID {
		if u, err := s.Store.GetUserByID(r.Context(), job.UserID); err == nil {
			owner = u
		}
	}
	if err := s.Cron.Delete(r.Context(), owner, job); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "cron.delete", job.Schedule, job.Command)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleCronLog(w http.ResponseWriter, r *http.Request) {
	job, ok := s.ownsCronJob(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	res, err := s.Cron.TailLog(job)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"log":        res.Log,
		"has_run":    res.HasRun,
		"updated_at": res.UpdatedAt,
	})
}
