// Package api exposes the Aegis panel over a JSON REST API plus WebSocket
// endpoints for realtime metrics and the terminal. Everything the frontend
// does goes through this layer; services never touch HTTP.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aegis/internal/auth"
	"aegis/internal/config"
	"aegis/internal/store"
	"aegis/internal/svc"
)

// Server wires the API to the services.
type Server struct {
	Cfg      *config.Config
	Store    *store.Store
	Auth     *auth.Manager
	Domains  *svc.Domains
	Web      *svc.WebServer
	PHP      *svc.PHP
	DNS      *svc.DNS
	SSL      *svc.SSL
	FTP      *svc.FTP
	DB       *svc.Databases
	Files    *svc.Files
	Thumbs   *svc.Thumbs
	Backup   *svc.Backup
	System   *svc.System
	Tuner    *svc.Tuner
	Terminal *svc.Terminal
	Cipher   *svc.Cipher
	Cron     *svc.Cron
	Mail     *svc.Mail
	Tokens   *svc.APITokens
	Security *svc.Security
	Quota    *svc.Quota
	WebApps  *svc.WebApps
	Packages *svc.Packages

	primaryIPv4 string
}

// New creates the API server.
func New(cfg *config.Config, st *store.Store, am *auth.Manager,
	domains *svc.Domains, web *svc.WebServer, php *svc.PHP, dns *svc.DNS, ssl *svc.SSL,
	ftp *svc.FTP, db *svc.Databases, files *svc.Files, thumbs *svc.Thumbs, backup *svc.Backup,
	sys *svc.System, tuner *svc.Tuner, term *svc.Terminal, cipher *svc.Cipher, cron *svc.Cron, mailSvc *svc.Mail, tokens *svc.APITokens, security *svc.Security, quota *svc.Quota, webApps *svc.WebApps, packages *svc.Packages) *Server {
	return &Server{
		Cfg: cfg, Store: st, Auth: am, Domains: domains, Web: web, PHP: php,
		DNS: dns, SSL: ssl, FTP: ftp, DB: db, Files: files, Thumbs: thumbs, Backup: backup,
		System: sys, Tuner: tuner, Terminal: term, Cipher: cipher, Cron: cron, Mail: mailSvc, Tokens: tokens, Security: security, Quota: quota, WebApps: webApps, Packages: packages,
		primaryIPv4: detectPrimaryIP(),
	}
}

// detectPrimaryIP finds the server's primary non-loopback IPv4 address.
func detectPrimaryIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.To4() != nil && !ip.IsLoopback() {
				return ip.String()
			}
		}
	}
	return ""
}

// ctxKey is a private context key type.
type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxClaims
)

// Handler returns the root http.Handler for the API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public.
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)

	// Authenticated.
	mux.HandleFunc("GET /api/auth/me", s.withAuth(s.handleMe))
	mux.HandleFunc("POST /api/auth/logout", s.withAuth(s.handleLogout))
	mux.HandleFunc("POST /api/admin/impersonate", s.withAuth(s.handleImpersonate))
	mux.HandleFunc("POST /api/admin/unimpersonate", s.withAuth(s.handleUnimpersonate))
	mux.HandleFunc("GET /api/admin/audit", s.withAuth(s.withRole(s.handleAudit, store.RoleAdmin)))

	// System.
	mux.HandleFunc("GET /api/system/overview", s.withAuth(s.handleOverview))
	mux.HandleFunc("GET /api/system/usage", s.withAuth(s.handleUsage))
	mux.HandleFunc("GET /api/system/processes", s.withAuth(s.handleProcesses))
	mux.HandleFunc("GET /api/system/metrics", s.withAuth(s.handleMetricsWS))

	// Tuning (admin).
	mux.HandleFunc("GET /api/tuning/report", s.withAuth(s.withRole(s.handleTuningReport, store.RoleAdmin)))
	mux.HandleFunc("POST /api/tuning/inspect", s.withAuth(s.withRole(s.handleTuningInspect, store.RoleAdmin)))
	mux.HandleFunc("POST /api/tuning/apply", s.withAuth(s.withRole(s.handleTuningApply, store.RoleAdmin)))

	// System updates + package installer (admin) — works across
	// apt/dnf/yum/pacman/zypper/apk, see internal/svc/packages.go.
	mux.HandleFunc("GET /api/system/distro", s.withAuth(s.withRole(s.handleDistroInfo, store.RoleAdmin)))
	mux.HandleFunc("GET /api/system/updates", s.withAuth(s.withRole(s.handleUpdatesList, store.RoleAdmin)))
	mux.HandleFunc("POST /api/system/updates/check", s.withAuth(s.withRole(s.handleUpdatesCheck, store.RoleAdmin)))
	mux.HandleFunc("POST /api/system/updates/apply", s.withAuth(s.withRole(s.handleUpdatesApply, store.RoleAdmin)))
	mux.HandleFunc("GET /api/system/packages/search", s.withAuth(s.withRole(s.handlePackagesSearch, store.RoleAdmin)))
	mux.HandleFunc("POST /api/system/packages/install", s.withAuth(s.withRole(s.handlePackagesInstall, store.RoleAdmin)))

	// Users + packages (admin/reseller).
	mux.HandleFunc("GET /api/users", s.withAuth(s.withRole(s.handleUsersList, store.RoleAdmin, store.RoleReseller)))
	mux.HandleFunc("POST /api/users", s.withAuth(s.withRole(s.handleUsersCreate, store.RoleAdmin, store.RoleReseller)))
	mux.HandleFunc("GET /api/users/{id}", s.withAuth(s.withRole(s.handleUsersGet, store.RoleAdmin, store.RoleReseller)))
	mux.HandleFunc("PATCH /api/users/{id}", s.withAuth(s.withRole(s.handleUsersUpdate, store.RoleAdmin, store.RoleReseller)))
	mux.HandleFunc("DELETE /api/users/{id}", s.withAuth(s.withRole(s.handleUsersDelete, store.RoleAdmin, store.RoleReseller)))
	// No withRole gate: handleUsersResetPassword's own canManageUser check
	// already allows a user to reset their own password (the dashboard's
	// self-service "My account" form uses this same route), on top of
	// admin/reseller managing others.
	mux.HandleFunc("POST /api/users/{id}/reset-password", s.withAuth(s.handleUsersResetPassword))
	mux.HandleFunc("POST /api/users/{id}/suspend", s.withAuth(s.withRole(s.handleUsersSuspend, store.RoleAdmin, store.RoleReseller)))
	mux.HandleFunc("POST /api/users/{id}/unsuspend", s.withAuth(s.withRole(s.handleUsersSuspend, store.RoleAdmin, store.RoleReseller)))
	mux.HandleFunc("GET /api/packages", s.withAuth(s.handlePackagesList))
	mux.HandleFunc("POST /api/packages", s.withAuth(s.withRole(s.handlePackagesCreate, store.RoleAdmin)))
	mux.HandleFunc("PATCH /api/packages/{id}", s.withAuth(s.withRole(s.handlePackagesUpdate, store.RoleAdmin)))
	mux.HandleFunc("DELETE /api/packages/{id}", s.withAuth(s.withRole(s.handlePackagesDelete, store.RoleAdmin)))

	// Domains.
	mux.HandleFunc("GET /api/domains", s.withAuth(s.handleDomainsList))
	mux.HandleFunc("POST /api/domains", s.withAuth(s.handleDomainsCreate))
	mux.HandleFunc("GET /api/domains/{id}", s.withAuth(s.handleDomainsGet))
	mux.HandleFunc("PATCH /api/domains/{id}", s.withAuth(s.handleDomainsUpdate))
	mux.HandleFunc("DELETE /api/domains/{id}", s.withAuth(s.handleDomainsDelete))
	mux.HandleFunc("POST /api/domains/{id}/apply", s.withAuth(s.handleDomainsApply))
	mux.HandleFunc("GET /api/webapps/catalog", s.withAuth(s.handleWebAppsCatalog))
	mux.HandleFunc("POST /api/domains/{id}/install", s.withAuth(s.handleDomainInstall))
	mux.HandleFunc("POST /api/domains/{id}/wp-cli", s.withAuth(s.handleDomainWPCLI))
	mux.HandleFunc("POST /api/domains/{id}/aliases", s.withAuth(s.handleAliasesAdd))
	mux.HandleFunc("DELETE /api/domains/{id}/aliases/{alias}", s.withAuth(s.handleAliasesRemove))

	// DNS.
	mux.HandleFunc("GET /api/dns/zones", s.withAuth(s.handleZonesList))
	mux.HandleFunc("POST /api/dns/zones", s.withAuth(s.handleZonesCreate))
	mux.HandleFunc("GET /api/dns/zones/{id}", s.withAuth(s.handleZonesGet))
	mux.HandleFunc("PATCH /api/dns/zones/{id}", s.withAuth(s.handleZonesUpdate))
	mux.HandleFunc("DELETE /api/dns/zones/{id}", s.withAuth(s.handleZonesDelete))
	mux.HandleFunc("POST /api/dns/zones/{id}/sync", s.withAuth(s.handleZonesSync))
	mux.HandleFunc("POST /api/dns/zones/{id}/records", s.withAuth(s.handleRecordsCreate))
	mux.HandleFunc("PATCH /api/dns/records/{id}", s.withAuth(s.handleRecordsUpdate))
	mux.HandleFunc("DELETE /api/dns/records/{id}", s.withAuth(s.handleRecordsDelete))
	mux.HandleFunc("GET /api/dns/providers", s.withAuth(s.handleProvidersList))
	mux.HandleFunc("POST /api/dns/providers", s.withAuth(s.withRole(s.handleProvidersCreate, store.RoleAdmin)))
	mux.HandleFunc("PATCH /api/dns/providers/{id}", s.withAuth(s.withRole(s.handleProvidersUpdate, store.RoleAdmin)))
	mux.HandleFunc("DELETE /api/dns/providers/{id}", s.withAuth(s.withRole(s.handleProvidersDelete, store.RoleAdmin)))

	// SSL.
	mux.HandleFunc("GET /api/ssl/orders", s.withAuth(s.handleSSLOrders))
	mux.HandleFunc("POST /api/ssl/issue", s.withAuth(s.handleSSLIssue))
	mux.HandleFunc("POST /api/ssl/self-signed", s.withAuth(s.handleSSLSelfSigned))
	mux.HandleFunc("GET /api/ssl/info", s.withAuth(s.handleSSLInfo))

	// PHP.
	mux.HandleFunc("GET /api/php/versions", s.withAuth(s.handlePHPVersions))

	// Web server.
	mux.HandleFunc("GET /api/webserver", s.withAuth(s.handleWebServerStatus))
	mux.HandleFunc("PATCH /api/webserver", s.withAuth(s.withRole(s.handleWebServerSet, store.RoleAdmin)))
	mux.HandleFunc("GET /api/webserver/preview", s.withAuth(s.handleWebServerPreview))

	// FTP.
	mux.HandleFunc("GET /api/ftp/accounts", s.withAuth(s.handleFTPList))
	mux.HandleFunc("POST /api/ftp/accounts", s.withAuth(s.handleFTPCreate))
	mux.HandleFunc("POST /api/ftp/accounts/{id}/password", s.withAuth(s.handleFTPPassword))
	mux.HandleFunc("POST /api/ftp/accounts/{id}/toggle", s.withAuth(s.handleFTPToggle))
	mux.HandleFunc("DELETE /api/ftp/accounts/{id}", s.withAuth(s.handleFTPDelete))

	// Databases.
	mux.HandleFunc("GET /api/databases/servers", s.withAuth(s.handleDBServers))
	mux.HandleFunc("GET /api/databases", s.withAuth(s.handleDBList))
	mux.HandleFunc("POST /api/databases", s.withAuth(s.handleDBCreate))
	mux.HandleFunc("DELETE /api/databases/{id}", s.withAuth(s.handleDBDelete))
	mux.HandleFunc("GET /api/databases/{id}/dump", s.withAuth(s.handleDBDump))

	// Files.
	mux.HandleFunc("GET /api/files", s.withAuth(s.handleFilesList))
	mux.HandleFunc("GET /api/files/content", s.withAuth(s.handleFilesRead))
	mux.HandleFunc("GET /api/files/thumb", s.withAuth(s.handleFilesThumb))
	mux.HandleFunc("POST /api/files/write", s.withAuth(s.handleFilesWrite))
	mux.HandleFunc("POST /api/files/mkdir", s.withAuth(s.handleFilesMkdir))
	mux.HandleFunc("POST /api/files/rename", s.withAuth(s.handleFilesRename))
	mux.HandleFunc("POST /api/files/delete", s.withAuth(s.handleFilesDelete))
	mux.HandleFunc("POST /api/files/chmod", s.withAuth(s.handleFilesChmod))
	mux.HandleFunc("POST /api/files/chown", s.withAuth(s.handleFilesChown))
	mux.HandleFunc("POST /api/files/search", s.withAuth(s.handleFilesSearch))
	mux.HandleFunc("POST /api/files/zip", s.withAuth(s.handleFilesZip))
	mux.HandleFunc("POST /api/files/unzip", s.withAuth(s.handleFilesUnzip))
	mux.HandleFunc("GET /api/files/download", s.withAuth(s.handleFilesDownload))
	mux.HandleFunc("POST /api/files/upload", s.withAuth(s.handleFilesUpload))

	// Backups (admin).
	mux.HandleFunc("GET /api/backups", s.withAuth(s.withRole(s.handleBackupsList, store.RoleAdmin)))
	mux.HandleFunc("POST /api/backups", s.withAuth(s.withRole(s.handleBackupsCreate, store.RoleAdmin)))
	mux.HandleFunc("GET /api/backups/download", s.withAuth(s.withRole(s.handleBackupsDownload, store.RoleAdmin)))
	mux.HandleFunc("POST /api/backups/restore", s.withAuth(s.withRole(s.handleBackupsRestore, store.RoleAdmin)))
	mux.HandleFunc("DELETE /api/backups", s.withAuth(s.withRole(s.handleBackupsDelete, store.RoleAdmin)))
	mux.HandleFunc("GET /api/backups/targets", s.withAuth(s.withRole(s.handleBackupTargetsList, store.RoleAdmin)))
	mux.HandleFunc("POST /api/backups/targets", s.withAuth(s.withRole(s.handleBackupTargetsCreate, store.RoleAdmin)))
	mux.HandleFunc("DELETE /api/backups/targets/{id}", s.withAuth(s.withRole(s.handleBackupTargetsDelete, store.RoleAdmin)))
	mux.HandleFunc("GET /api/backups/schedule", s.withAuth(s.withRole(s.handleBackupScheduleGet, store.RoleAdmin)))
	mux.HandleFunc("PATCH /api/backups/schedule", s.withAuth(s.withRole(s.handleBackupSchedule, store.RoleAdmin)))

	// Mail: domain enablement + mailboxes + aliases.
	mux.HandleFunc("GET /api/mail/domains", s.withAuth(s.handleMailDomainsList))
	mux.HandleFunc("POST /api/mail/domains", s.withAuth(s.handleMailDomainsCreate))
	mux.HandleFunc("DELETE /api/mail/domains/{id}", s.withAuth(s.handleMailDomainsDelete))
	mux.HandleFunc("GET /api/mail/domains/{id}/mailboxes", s.withAuth(s.handleMailboxesList))
	mux.HandleFunc("POST /api/mail/domains/{id}/mailboxes", s.withAuth(s.handleMailboxesCreate))
	mux.HandleFunc("POST /api/mail/mailboxes/{id}/password", s.withAuth(s.handleMailboxPassword))
	mux.HandleFunc("POST /api/mail/mailboxes/{id}/toggle", s.withAuth(s.handleMailboxToggle))
	mux.HandleFunc("DELETE /api/mail/mailboxes/{id}", s.withAuth(s.handleMailboxDelete))
	mux.HandleFunc("GET /api/mail/domains/{id}/aliases", s.withAuth(s.handleMailAliasesList))
	mux.HandleFunc("POST /api/mail/domains/{id}/aliases", s.withAuth(s.handleMailAliasesCreate))
	mux.HandleFunc("DELETE /api/mail/aliases/{id}", s.withAuth(s.handleMailAliasesDelete))

	// Webmail: its own credential-based session, not the panel JWT.
	mux.HandleFunc("POST /api/webmail/login", s.handleWebmailLogin)
	mux.HandleFunc("POST /api/webmail/logout", s.handleWebmailLogout)
	mux.HandleFunc("GET /api/webmail/messages", s.withWebmailAuth(s.handleWebmailMessages))
	mux.HandleFunc("GET /api/webmail/messages/{uid}", s.withWebmailAuth(s.handleWebmailMessageGet))
	mux.HandleFunc("POST /api/webmail/send", s.withWebmailAuth(s.handleWebmailSend))

	// Cron.
	mux.HandleFunc("GET /api/cron/jobs", s.withAuth(s.handleCronList))
	mux.HandleFunc("POST /api/cron/jobs", s.withAuth(s.handleCronCreate))
	mux.HandleFunc("PATCH /api/cron/jobs/{id}", s.withAuth(s.handleCronUpdate))
	mux.HandleFunc("POST /api/cron/jobs/{id}/toggle", s.withAuth(s.handleCronToggle))
	mux.HandleFunc("DELETE /api/cron/jobs/{id}", s.withAuth(s.handleCronDelete))
	mux.HandleFunc("GET /api/cron/jobs/{id}/log", s.withAuth(s.handleCronLog))

	// API tokens.
	mux.HandleFunc("GET /api/tokens", s.withAuth(s.handleTokensList))
	mux.HandleFunc("POST /api/tokens", s.withAuth(s.handleTokensCreate))
	mux.HandleFunc("DELETE /api/tokens/{id}", s.withAuth(s.handleTokensDelete))

	// Security centre (admin).
	mux.HandleFunc("GET /api/security/attempts", s.withAuth(s.withRole(s.handleSecurityAttempts, store.RoleAdmin)))
	mux.HandleFunc("GET /api/security/bans", s.withAuth(s.withRole(s.handleSecurityBans, store.RoleAdmin)))
	mux.HandleFunc("POST /api/security/unban", s.withAuth(s.withRole(s.handleSecurityUnban, store.RoleAdmin)))

	// Quota.
	mux.HandleFunc("GET /api/quota/usage", s.withAuth(s.handleQuotaUsage))
	mux.HandleFunc("POST /api/quota/enforce", s.withAuth(s.withRole(s.handleQuotaEnforce, store.RoleAdmin)))

	// Terminal.
	mux.HandleFunc("GET /api/terminal", s.withAuth(s.handleTerminalWS))

	return s.withCommon(mux)
}

// withCommon adds logging, panic recovery and request ids.
func (s *Server) withCommon(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic in handler", "path", r.URL.Path, "panic", rec)
				writeErr(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
		slog.Debug("http", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String(), "ip", clientIP(r))
	})
}

// --- middleware -----------------------------------------------------------------

// withAuth authenticates the bearer token and loads the acting user.
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		// API tokens (aegis_...) are a separate credential from the panel
		// JWT — scripting/automation auth, verified against api_tokens
		// instead of a signed session. A synthetic Claims value keeps
		// userFrom/claimsFrom/withRole working identically either way.
		if strings.HasPrefix(token, "aegis_") {
			user, err := s.Tokens.Verify(r.Context(), token)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "invalid api token")
				return
			}
			if user.Status == store.StatusSuspended {
				writeErr(w, http.StatusForbidden, "account is suspended")
				return
			}
			claims := &auth.Claims{Username: user.Username, Role: user.Role}
			ctx := context.WithValue(r.Context(), ctxUser, user)
			ctx = context.WithValue(ctx, ctxClaims, claims)
			next(w, r.WithContext(ctx))
			return
		}
		claims, user, err := s.Auth.Verify(r.Context(), token)
		if err != nil {
			status := http.StatusUnauthorized
			if errors.Is(err, auth.ErrSuspended) {
				status = http.StatusForbidden
			}
			writeErr(w, status, err.Error())
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxClaims, claims)
		next(w, r.WithContext(ctx))
	}
}

// withRole restricts to the given roles.
func (s *Server) withRole(next http.HandlerFunc, roles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := claimsFrom(r)
		if claims == nil || !auth.HasRole(claims, roles...) {
			writeErr(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		next(w, r)
	}
}

// --- context helpers -------------------------------------------------------------

func userFrom(r *http.Request) *store.User {
	u, _ := r.Context().Value(ctxUser).(*store.User)
	return u
}

func claimsFrom(r *http.Request) *auth.Claims {
	c, _ := r.Context().Value(ctxClaims).(*auth.Claims)
	return c
}

// --- request/response helpers ----------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errBody struct {
	Error string `json:"error"`
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errBody{Error: msg})
}

// readJSON decodes a JSON request body into v, rejecting unknown fields.
func readJSON(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// audit records an action in the audit log.
func (s *Server) audit(r *http.Request, action, target, detail string) {
	u := userFrom(r)
	actor := int64(0)
	name := "anonymous"
	if u != nil {
		actor = u.ID
		name = u.Username
	}
	if claims := claimsFrom(r); claims != nil && auth.IsImpersonating(claims) {
		name = name + " (via " + claims.Username + ")"
	}
	if err := s.Store.AppendAudit(r.Context(), actor, name, action, target, detail, clientIP(r)); err != nil {
		slog.Error("audit write failed", "err", err)
	}
}

// pathID parses a named {id} path parameter.
func pathID(r *http.Request, name string) (int64, error) {
	v := r.PathValue(name)
	if v == "" {
		return 0, errors.New("missing id")
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

// pathIDFromQuery parses an id from a query parameter.
func pathIDFromQuery(r *http.Request, name string) (int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, errors.New("missing " + name)
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

// bearerToken reads the token from the Authorization header, falling back to
// a "token" query parameter. The fallback exists for WebSocket connections:
// the browser WebSocket API cannot set custom headers, so the frontend's
// live-metrics and terminal sockets pass the token in the URL instead.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// canManageUser reports whether the acting user may manage the target user
// (admin: all; reseller: only accounts they created).
func (s *Server) canManageUser(actor *store.User, target *store.User) bool {
	if actor.Role == store.RoleAdmin {
		return true
	}
	if actor.Role == store.RoleReseller {
		return target.OwnerID == actor.ID || target.ID == actor.ID
	}
	return target.ID == actor.ID
}
