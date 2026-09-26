package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"aegis/internal/auth"
	"aegis/internal/store"
	"aegis/internal/svc"
)

// --- users --------------------------------------------------------------------

func (s *Server) handleUsersList(w http.ResponseWriter, r *http.Request) {
	actor := userFrom(r)
	var users []*store.User
	var err error
	if actor.Role == store.RoleAdmin {
		users, err = s.Store.ListUsers(r.Context(), 0)
	} else {
		users, err = s.Store.ListUsers(r.Context(), actor.ID)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]*store.User, 0, len(users))
	for _, u := range users {
		out = append(out, publicUser(u))
	}
	writeJSON(w, http.StatusOK, out)
}

type createUserReq struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	Role      string `json:"role"`
	PackageID int64  `json:"package_id"`
}

func (s *Server) handleUsersCreate(w http.ResponseWriter, r *http.Request) {
	actor := userFrom(r)
	var req createUserReq
	if !readJSON(w, r, &req) {
		return
	}
	if !svc.ValidUsername(req.Username) {
		writeErr(w, http.StatusBadRequest, "invalid username (3-30 chars, lowercase letters/digits/underscore, must start with a letter)")
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	role := req.Role
	if role == "" {
		role = store.RoleUser
	}
	if role != store.RoleUser && role != store.RoleReseller && role != store.RoleAdmin {
		writeErr(w, http.StatusBadRequest, "invalid role")
		return
	}
	// Resellers can only create regular users.
	if actor.Role == store.RoleReseller && role != store.RoleUser {
		writeErr(w, http.StatusForbidden, "resellers can only create user accounts")
		return
	}
	if actor.Role != store.RoleAdmin && role == store.RoleAdmin {
		writeErr(w, http.StatusForbidden, "only admins can create admin accounts")
		return
	}
	if _, err := s.Store.GetUserByUsername(r.Context(), req.Username); err == nil {
		writeErr(w, http.StatusConflict, "username already exists")
		return
	}
	pkgID := req.PackageID
	if pkgID == 0 {
		if pkg, err := s.Store.GetDefaultPackage(r.Context()); err == nil {
			pkgID = pkg.ID
		}
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	home := filepath.Join(s.Cfg.HomeRoot, req.Username)
	u := &store.User{
		Username: req.Username, Email: req.Email, PasswordHash: hash, Role: role,
		PackageID: pkgID, Status: store.StatusActive, OwnerID: actor.ID, HomeDir: home,
	}
	// Quota limits come from the package; quota.Enforce only ever acts on
	// users.quota_disk_bytes, which nothing else in the system sets.
	if pkgID > 0 {
		if pkg, err := s.Store.GetPackage(r.Context(), pkgID); err == nil {
			u.QuotaDiskBytes = pkg.DiskQuotaBytes
			u.QuotaBandwidthBytes = pkg.BandwidthQuotaBytes
		}
	}
	// System account + home dir.
	if err := createSystemUser(r, u, req.Password); err != nil {
		writeErr(w, http.StatusInternalServerError, "system account: "+err.Error())
		return
	}
	if err := s.Store.CreateUser(r.Context(), u); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	// Primary FTP account.
	_, _ = s.FTP.Create(r.Context(), u, u.Username, req.Password, false)
	s.audit(r, "user.create", u.Username, "role="+role)
	writeJSON(w, http.StatusCreated, publicUser(u))
}

func (s *Server) handleUsersGet(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.Store.GetUserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if !s.canManageUser(userFrom(r), u) {
		writeErr(w, http.StatusForbidden, "cannot access this account")
		return
	}
	writeJSON(w, http.StatusOK, publicUser(u))
}

type updateUserReq struct {
	Email     *string `json:"email"`
	Role      *string `json:"role"`
	PackageID *int64  `json:"package_id"`
	Status    *string `json:"status"`
}

func (s *Server) handleUsersUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	actor := userFrom(r)
	u, err := s.Store.GetUserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if !s.canManageUser(actor, u) {
		writeErr(w, http.StatusForbidden, "cannot manage this account")
		return
	}
	var req updateUserReq
	if !readJSON(w, r, &req) {
		return
	}
	var statusChange string // "" = none; otherwise the status to move to, via the suspender
	if req.Email != nil {
		u.Email = *req.Email
	}
	if req.Role != nil {
		if actor.Role != store.RoleAdmin {
			writeErr(w, http.StatusForbidden, "only admins can change roles")
			return
		}
		u.Role = *req.Role
	}
	if req.PackageID != nil {
		pkg, err := s.Store.GetPackage(r.Context(), *req.PackageID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid package")
			return
		}
		u.PackageID = *req.PackageID
		u.QuotaDiskBytes = pkg.DiskQuotaBytes
		u.QuotaBandwidthBytes = pkg.BandwidthQuotaBytes
	}
	if req.Status != nil {
		if *req.Status != store.StatusActive && *req.Status != store.StatusSuspended {
			writeErr(w, http.StatusBadRequest, "invalid status")
			return
		}
		statusChange = *req.Status
		if statusChange == u.Status {
			statusChange = "" // no change
		}
		if statusChange == store.StatusSuspended && u.ID == userFrom(r).ID {
			writeErr(w, http.StatusBadRequest, "you cannot suspend your own account")
			return
		}
	}
	if err := s.Store.UpdateUser(r.Context(), u); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// A status change goes through the suspender so it is enforced everywhere,
	// not just written to the row.
	if statusChange != "" && s.Suspend != nil {
		var serr error
		if statusChange == store.StatusSuspended {
			_, serr = s.Suspend.Suspend(r.Context(), u)
		} else {
			_, serr = s.Suspend.Unsuspend(r.Context(), u)
		}
		if serr != nil {
			writeErr(w, http.StatusInternalServerError, serr.Error())
			return
		}
	}
	s.audit(r, "user.update", u.Username, "")
	writeJSON(w, http.StatusOK, publicUser(u))
}

func (s *Server) handleUsersDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	actor := userFrom(r)
	u, err := s.Store.GetUserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if !s.canManageUser(actor, u) {
		writeErr(w, http.StatusForbidden, "cannot manage this account")
		return
	}
	if u.ID == actor.ID {
		writeErr(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}
	if s.Purge == nil {
		writeErr(w, http.StatusServiceUnavailable, "account deletion is not available")
		return
	}
	// Removes everything the user owns — files, domains, databases, mail,
	// cron, containers, FTP and the system account — not just the panel row.
	rep, err := s.Purge.DeleteUser(r.Context(), u)
	if err != nil {
		var blocked *svc.BlockedError
		if errors.As(err, &blocked) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	p := rep.Plan
	s.audit(r, "user.delete", u.Username, fmt.Sprintf("domains=%d databases=%d ftp=%d mail=%d containers=%d cron=%d",
		len(p.Domains), len(p.Databases), len(p.FTPAccounts), len(p.MailDomains), p.Containers, p.CronJobs))
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "warnings": rep.Warnings})
}

// handleUsersDeletionPlan previews what deleting a user would remove (and what
// forbids it), for the confirmation dialog. It changes nothing.
func (s *Server) handleUsersDeletionPlan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.Store.GetUserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if !s.canManageUser(userFrom(r), u) {
		writeErr(w, http.StatusForbidden, "cannot manage this account")
		return
	}
	if s.Purge == nil {
		writeErr(w, http.StatusServiceUnavailable, "account deletion is not available")
		return
	}
	plan, err := s.Purge.Plan(r.Context(), u)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if u.ID == userFrom(r).ID {
		plan.Blockers = append(plan.Blockers, "you cannot delete your own account")
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleUsersResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	actor := userFrom(r)
	u, err := s.Store.GetUserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if !s.canManageUser(actor, u) {
		writeErr(w, http.StatusForbidden, "cannot manage this account")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Store.SetUserPassword(r.Context(), id, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.Store.DeleteUserSessions(r.Context(), id)
	// Sync the system account password so FTP login keeps working.
	if err := svc.SetSystemPassword(u.Username, req.Password); err != nil {
		slog.Warn("reset-password: system password sync failed", "user", u.Username, "err", err)
	}
	s.audit(r, "user.reset-password", u.Username, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleUsersSuspend(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	actor := userFrom(r)
	u, err := s.Store.GetUserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if !s.canManageUser(actor, u) {
		writeErr(w, http.StatusForbidden, "cannot manage this account")
		return
	}
	if s.Suspend == nil {
		writeErr(w, http.StatusServiceUnavailable, "suspension is not available")
		return
	}
	// Suspending cuts the account off everywhere (websites, FTP, Web FTP, mail,
	// cron, containers, database logins) and unsuspending restores exactly
	// that; see svc.Suspender.
	status := store.StatusSuspended
	var rep *svc.SuspendReport
	if strings.HasSuffix(r.URL.Path, "/unsuspend") {
		status = store.StatusActive
		rep, err = s.Suspend.Unsuspend(r.Context(), u)
	} else {
		if u.ID == actor.ID {
			writeErr(w, http.StatusBadRequest, "you cannot suspend your own account")
			return
		}
		rep, err = s.Suspend.Suspend(r.Context(), u)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "user."+status, u.Username, "")
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "warnings": rep.Warnings})
}

// createSystemUser adds the system account with a home directory.
func createSystemUser(r *http.Request, u *store.User, password string) error {
	_, err := svc.RunTimeout(15*time.Second, "useradd", "-m", "-d", u.HomeDir, "-s", "/sbin/nologin", "-g", "www-data", u.Username)
	if err != nil {
		return err
	}
	return svc.SetSystemPassword(u.Username, password)
}

// --- packages ------------------------------------------------------------------

func (s *Server) handlePackagesList(w http.ResponseWriter, r *http.Request) {
	pkgs, err := s.Store.ListPackages(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pkgs)
}

func (s *Server) handlePackagesCreate(w http.ResponseWriter, r *http.Request) {
	var p store.Package
	if !readJSON(w, r, &p) {
		return
	}
	if p.Name == "" {
		writeErr(w, http.StatusBadRequest, "package name is required")
		return
	}
	if err := s.Store.CreatePackage(r.Context(), &p); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "package.create", p.Name, "")
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handlePackagesUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pkg, err := s.Store.GetPackage(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "package not found")
		return
	}
	if !readJSON(w, r, pkg) {
		return
	}
	if err := s.Store.UpdatePackage(r.Context(), pkg); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "package.update", pkg.Name, "")
	writeJSON(w, http.StatusOK, pkg)
}

func (s *Server) handlePackagesDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pkg, err := s.Store.GetPackage(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "package not found")
		return
	}
	if err := s.Store.DeletePackage(r.Context(), id); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "package.delete", pkg.Name, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
