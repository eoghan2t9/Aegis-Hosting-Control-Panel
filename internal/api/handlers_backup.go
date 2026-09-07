package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) handleBackupsList(w http.ResponseWriter, r *http.Request) {
	backups, err := s.Backup.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, backups)
}

type backupCreateReq struct {
	Scope  string `json:"scope"` // full | user
	UserID int64  `json:"user_id"`
}

func (s *Server) handleBackupsCreate(w http.ResponseWriter, r *http.Request) {
	var req backupCreateReq
	if !readJSON(w, r, &req) {
		return
	}
	scope := req.Scope
	if scope == "" {
		scope = "full"
	}
	if scope != "full" && scope != "user" {
		writeErr(w, http.StatusBadRequest, "scope must be 'full' or 'user'")
		return
	}
	if scope == "user" && req.UserID == 0 {
		writeErr(w, http.StatusBadRequest, "user_id is required for user backups")
		return
	}
	info, err := s.Backup.Create(r.Context(), scope, req.UserID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "backup.create", info.Name, "scope="+scope)
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) handleBackupsDownload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		writeErr(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	path := filepath.Join(s.Cfg.BackupDir, name)
	if _, err := os.Stat(path); err != nil {
		writeErr(w, http.StatusNotFound, "backup not found")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	http.ServeFile(w, r, path)
}

type backupRestoreReq struct {
	Name string `json:"name"`
}

func (s *Server) handleBackupsRestore(w http.ResponseWriter, r *http.Request) {
	var req backupRestoreReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Name == "" || strings.Contains(req.Name, "/") || strings.Contains(req.Name, "..") {
		writeErr(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	path := filepath.Join(s.Cfg.BackupDir, req.Name)
	if _, err := os.Stat(path); err != nil {
		writeErr(w, http.StatusNotFound, "backup not found")
		return
	}
	// Run in a goroutine so long restores don't block the request? Keep sync
	// but with a sane timeout; the frontend shows a spinner.
	if err := s.Backup.Restore(r.Context(), path); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "backup.restore", req.Name, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

// handleBackupsDelete removes a backup archive from disk.
func (s *Server) handleBackupsDelete(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		writeErr(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	path := filepath.Join(s.Cfg.BackupDir, name)
	if _, err := os.Stat(path); err != nil {
		writeErr(w, http.StatusNotFound, "backup not found")
		return
	}
	if err := os.Remove(path); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "backup.delete", name, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
