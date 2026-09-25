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
	user, token, err := s.Auth.Login(r.Context(), req.Username, req.Password, clientIP(r))
	if err != nil {
		if errors.Is(err, auth.ErrTOTPRequired) {
			// Password checked out; the account still needs its 6-digit
			// code (or a backup code) via handleTOTPVerify. token here is
			// a short-lived challenge id, never a usable bearer token.
			writeJSON(w, http.StatusOK, map[string]interface{}{"totp_required": true, "challenge": token})
			return
		}
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeErr(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		if errors.Is(err, auth.ErrLockedOut) {
			writeErr(w, http.StatusTooManyRequests, err.Error())
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

type totpVerifyReq struct {
	Challenge string `json:"challenge"`
	Code      string `json:"code"`
}

// handleTOTPVerify is the second step of login for a 2FA-enabled account —
// exchanges Login's challenge id plus a code (live TOTP or one-time backup
// code) for a real session, in the same response shape handleLogin uses for
// a normal single-step login.
func (s *Server) handleTOTPVerify(w http.ResponseWriter, r *http.Request) {
	var req totpVerifyReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Challenge == "" || req.Code == "" {
		writeErr(w, http.StatusBadRequest, "challenge and code are required")
		return
	}
	user, token, err := s.Auth.VerifyTOTPLogin(r.Context(), req.Challenge, req.Code, clientIP(r))
	if err != nil {
		if errors.Is(err, auth.ErrLockedOut) {
			writeErr(w, http.StatusTooManyRequests, err.Error())
			return
		}
		writeErr(w, http.StatusUnauthorized, "invalid or expired code")
		return
	}
	s.audit(r, "auth.login", user.Username, "login (2fa)")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token": token,
		"user":  publicUser(user),
	})
}

// handleTOTPEnroll starts 2FA enrollment for the signed-in user — generates
// a new secret (not yet trusted for login; see handleTOTPConfirm) and
// returns it plus the otpauth:// URI for an authenticator app.
func (s *Server) handleTOTPEnroll(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	secret, uri, err := s.Auth.BeginTOTPEnroll(r.Context(), user.ID, user.Username)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"secret": secret, "otpauth_uri": uri})
}

type totpConfirmReq struct {
	Code string `json:"code"`
}

// handleTOTPConfirm completes enrollment: proves the user's authenticator
// app actually has the secret from handleTOTPEnroll, flips totp_enabled on,
// and returns the one-time backup codes — shown exactly once here, never
// retrievable again.
func (s *Server) handleTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	var req totpConfirmReq
	if !readJSON(w, r, &req) {
		return
	}
	user := userFrom(r)
	codes, err := s.Auth.ConfirmTOTPEnroll(r.Context(), user.ID, req.Code)
	if err != nil {
		if errors.Is(err, auth.ErrTOTPInvalidCode) {
			writeErr(w, http.StatusBadRequest, "that code didn't match — check your authenticator app and try again")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "auth.totp_enabled", user.Username, "")
	writeJSON(w, http.StatusOK, map[string]interface{}{"backup_codes": codes})
}

type totpDisableReq struct {
	Password string `json:"password"`
}

// handleTOTPDisable turns 2FA off — requires the current password (see
// Manager.DisableTOTP) so a hijacked-but-unlocked session tab can't strip
// an account's second factor on its own.
func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var req totpDisableReq
	if !readJSON(w, r, &req) {
		return
	}
	user := userFrom(r)
	if err := s.Auth.DisableTOTP(r.Context(), user.ID, req.Password); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeErr(w, http.StatusUnauthorized, "incorrect password")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "auth.totp_disabled", user.Username, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
		// Which panel areas the user's hosting package grants. The frontend
		// uses this to hide gated nav areas; the API enforces the same flags
		// server-side via withFeature, so this is convenience, not security.
		"features": s.featuresFor(r.Context(), user),
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

// handleUnimpersonate ends the support session: revokes the impersonated
// token and returns a fresh one for the original admin so the frontend
// lands back in their own dashboard instead of the login screen.
func (s *Server) handleUnimpersonate(w http.ResponseWriter, r *http.Request) {
	adminName := "admin"
	if claims := claimsFrom(r); claims != nil && claims.Impersonator > 0 {
		if admin, err := s.Store.GetUserByID(r.Context(), claims.Impersonator); err == nil {
			adminName = admin.Username
		}
	}
	token, err := s.Auth.Unimpersonate(r.Context(), bearerToken(r))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "admin.unimpersonate", userFrom(r).Username, adminName+" ended support session")
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
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
		// TOTPEnabled is the only 2FA field ever exposed — TOTPSecret and
		// TOTPBackupCodes stay `json:"-"` on the User struct itself, so
		// there's no field here that could accidentally leak them even if
		// this literal were extended carelessly later.
		TOTPEnabled: u.TOTPEnabled,
	}
}
