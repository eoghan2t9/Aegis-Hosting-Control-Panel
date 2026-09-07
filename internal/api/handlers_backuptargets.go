package api

import (
	"net/http"

	"aegis/internal/svc"
)

func (s *Server) handleBackupTargetsList(w http.ResponseWriter, r *http.Request) {
	targets, err := s.Backup.ListBackupTargets(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, targets)
}

type backupTargetReq struct {
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	RetentionDays int    `json:"retention_days"`
	Endpoint      string `json:"endpoint"`
	Bucket        string `json:"bucket"`
	Region        string `json:"region"`
	AccessKey     string `json:"access_key"`
	SecretKey     string `json:"secret_key"`
	UseSSL        bool   `json:"use_ssl"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	Password      string `json:"password"`
	Path          string `json:"path"`
}

func (s *Server) handleBackupTargetsCreate(w http.ResponseWriter, r *http.Request) {
	var req backupTargetReq
	if !readJSON(w, r, &req) {
		return
	}
	cfg := svc.TargetConfig{
		Endpoint: req.Endpoint, Bucket: req.Bucket, Region: req.Region,
		AccessKey: req.AccessKey, SecretKey: req.SecretKey, UseSSL: req.UseSSL,
		Host: req.Host, Port: req.Port, User: req.User, Password: req.Password, Path: req.Path,
	}
	t, err := s.Backup.CreateBackupTarget(r.Context(), req.Kind, req.Label, cfg, req.RetentionDays)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Backup.TestTarget(r.Context(), t); err != nil {
		_ = s.Backup.DeleteBackupTarget(r.Context(), t.ID)
		writeErr(w, http.StatusBadGateway, "could not connect to target: "+err.Error())
		return
	}
	s.audit(r, "backup.target-create", t.Label, t.Kind)
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleBackupTargetsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Backup.DeleteBackupTarget(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "backup.target-delete", "", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleBackupScheduleGet(w http.ResponseWriter, r *http.Request) {
	val, _ := s.Store.GetSetting(r.Context(), "backup_schedule_enabled")
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": val == "true"})
}

func (s *Server) handleBackupSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	val := "false"
	if req.Enabled {
		val = "true"
	}
	if err := s.Store.SetSetting(r.Context(), "backup_schedule_enabled", val); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "backup.schedule", val, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
