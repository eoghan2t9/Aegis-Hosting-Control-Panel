package api

import (
	"errors"
	"net/http"

	"aegis/internal/auth"
	"aegis/internal/store"
)

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Username == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "username and password are required")
		return
	}
	user, token, err := s.Auth.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeErr(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	s.audit(r, "auth.login", user.Username, "login")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token": token,
		"user":  publicUser(user),
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	claims := claimsFrom(r)
	acting := user
	if auth.IsImpersonating(claims) {
		// Resolve the impersonator for the UI banner.
		if imp, err := s.Store.GetUserByID(r.Context(), claims.Impersonator); err == nil {
			acting = imp
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user": publicUser(user),
		"claims": map[string]interface{}{
			"role":            claims.Role,
			"impersonating":   auth.IsImpersonating(claims),
			"impersonated_by": acting.Username,
		},
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	_ = s.Auth.Logout(r.Context(), bearerToken(r))
	s.audit(r, "auth.logout", userFrom(r).Username, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type impersonateReq struct {
	UserID int64 `json:"user_id"`
}

// handleImpersonate implements "login as user" for admin support sessions.
func (s *Server) handleImpersonate(w http.ResponseWriter, r *http.Request) {
	actor := userFrom(r)
	if actor.Role != store.RoleAdmin {
		writeErr(w, http.StatusForbidden, "only admins can impersonate")
		return
	}
	var req impersonateReq
	if !readJSON(w, r, &req) {
		return
	}
	target, err := s.Store.GetUserByID(r.Context(), req.UserID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	token, err := s.Auth.Impersonate(r.Context(), actor.ID, target.ID)
	if err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	s.audit(r, "admin.impersonate", target.Username, "admin "+actor.Username+" logged in as "+target.Username)
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// handleUnimpersonate ends the support session by revoking the token.
func (s *Server) handleUnimpersonate(w http.ResponseWriter, r *http.Request) {
	_ = s.Auth.Logout(r.Context(), bearerToken(r))
	s.audit(r, "admin.unimpersonate", userFrom(r).Username, "ended support session")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.Store.ListAudit(r.Context(), 200, 0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// publicUser strips internal fields before returning a user.
func publicUser(u *store.User) *store.User {
	return &store.User{
		ID: u.ID, Username: u.Username, Email: u.Email, Role: u.Role,
		PackageID: u.PackageID, Status: u.Status, OwnerID: u.OwnerID, HomeDir: u.HomeDir,
		QuotaDiskBytes: u.QuotaDiskBytes, QuotaBandwidthBytes: u.QuotaBandwidthBytes,
		CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
		DiskUsedBytes: u.DiskUsedBytes, DomainCount: u.DomainCount, PackageName: u.PackageName,
	}
}
