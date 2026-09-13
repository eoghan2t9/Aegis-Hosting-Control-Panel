package api

import (
	"net/http"
	"strings"

	"aegis/internal/config"
)

// maskSecret is the sentinel a PUT payload carries for "leave this secret
// unchanged" (the UI never sees the real value, so it round-trips this).
const maskSecret = "(unchanged)"

// settingsView is the admin-facing slice of the panel config: everything an
// operator can safely read/change from the UI. Secrets (database passwords)
// are write-only: GET masks them, PUT accepts the mask sentinel to keep the
// stored value. Paths and infra that require a restart to matter are shown
// read-only context where changing them live would be misleading.
type settingsView struct {
	PublicHost       string `json:"public_host"`
	SessionTTLHours  int    `json:"session_ttl_hours"`
	LEStaging        bool   `json:"letsencrypt_staging"`
	PanelBase        string `json:"panel_base"`
	HomeRoot         string `json:"home_root"`
	BackupDir        string `json:"backup_dir"`
	CertDir          string `json:"cert_dir"`
	// Web server.
	WebServer        string `json:"web_server"`
	NginxDir         string `json:"nginx_dir"`
	ApacheDir        string `json:"apache_dir"`
	CaddyFile        string `json:"caddy_file"`
	PHPFpmSocketDir  string `json:"php_fpm_socket_dir"`
	ApacheListenPort int    `json:"apache_listen_port"`
	// DNS.
	DNSListenAddr    string   `json:"dns_listen_addr"`
	BindZoneDir      string   `json:"bind_zone_dir"`
	Nameservers      []string `json:"nameservers"`
	DNSAdminEmail    string   `json:"dns_admin_email"`
	// Database credentials (passwords write-only).
	MariaDBHost      string `json:"mariadb_host"`
	MariaDBPort      int    `json:"mariadb_port"`
	MariaDBUser      string `json:"mariadb_user"`
	MariaDBPassword  string `json:"mariadb_password"` // masked on GET
	PostgresHost     string `json:"postgres_host"`
	PostgresPort     int    `json:"postgres_port"`
	PostgresUser     string `json:"postgres_user"`
	PostgresPassword string `json:"postgres_password"` // masked on GET
}

func settingsFromConfig(c *config.Config) settingsView {
	v := settingsView{
		PublicHost:       c.PublicHost,
		SessionTTLHours:  c.SessionTTLHours,
		LEStaging:        c.LetsEncryptStaging,
		PanelBase:        c.PanelBase,
		HomeRoot:         c.HomeRoot,
		BackupDir:        c.BackupDir,
		CertDir:          c.CertDir,
		WebServer:        c.WebServer.Server,
		NginxDir:         c.WebServer.NginxDir,
		ApacheDir:        c.WebServer.ApacheDir,
		CaddyFile:        c.WebServer.CaddyFile,
		PHPFpmSocketDir:  c.WebServer.PHPFpmSocketDir,
		ApacheListenPort: c.WebServer.ApacheListenPort,
		DNSListenAddr:    c.DNS.ListenAddr,
		BindZoneDir:      c.DNS.BindZoneDir,
		Nameservers:      c.DNS.Nameservers,
		DNSAdminEmail:    c.DNS.AdminEmail,
		MariaDBHost:      c.MariaDB.Host,
		MariaDBPort:      c.MariaDB.Port,
		MariaDBUser:      c.MariaDB.User,
		PostgresHost:     c.PostgreSQL.Host,
		PostgresPort:     c.PostgreSQL.Port,
		PostgresUser:     c.PostgreSQL.User,
	}
	if c.MariaDB.Password != "" {
		v.MariaDBPassword = maskSecret
	}
	if c.PostgreSQL.Password != "" {
		v.PostgresPassword = maskSecret
	}
	return v
}

// applyToConfig copies UI-editable fields from the payload onto the live
// config. Empty directory fields fall back to their auto-detected defaults
// (the generators treat "" as "detect at use time").
func (v *settingsView) applyToConfig(c *config.Config) {
	c.PublicHost = strings.TrimSpace(v.PublicHost)
	if v.SessionTTLHours > 0 && v.SessionTTLHours <= 24*30 {
		c.SessionTTLHours = v.SessionTTLHours
	}
	c.LetsEncryptStaging = v.LEStaging
	c.PanelBase = normalizePanelBaseInput(v.PanelBase)
	c.WebServer.Server = v.WebServer
	c.WebServer.NginxDir = strings.TrimSpace(v.NginxDir)
	c.WebServer.ApacheDir = strings.TrimSpace(v.ApacheDir)
	c.WebServer.CaddyFile = strings.TrimSpace(v.CaddyFile)
	c.WebServer.PHPFpmSocketDir = strings.TrimSpace(v.PHPFpmSocketDir)
	if v.ApacheListenPort > 0 && v.ApacheListenPort < 65536 {
		c.WebServer.ApacheListenPort = v.ApacheListenPort
	}
	c.DNS.ListenAddr = strings.TrimSpace(v.DNSListenAddr)
	c.DNS.BindZoneDir = strings.TrimSpace(v.BindZoneDir)
	if len(v.Nameservers) > 0 {
		ns := make([]string, 0, len(v.Nameservers))
		for _, n := range v.Nameservers {
			if n = strings.TrimSpace(n); n != "" {
				ns = append(ns, n)
			}
		}
		c.DNS.Nameservers = ns
	}
	c.DNS.AdminEmail = strings.TrimSpace(v.DNSAdminEmail)
	c.MariaDB.Host = strings.TrimSpace(v.MariaDBHost)
	if v.MariaDBPort > 0 && v.MariaDBPort < 65536 {
		c.MariaDB.Port = v.MariaDBPort
	}
	c.MariaDB.User = strings.TrimSpace(v.MariaDBUser)
	if v.MariaDBPassword != "" && v.MariaDBPassword != maskSecret {
		c.MariaDB.Password = v.MariaDBPassword
	}
	c.PostgreSQL.Host = strings.TrimSpace(v.PostgresHost)
	if v.PostgresPort > 0 && v.PostgresPort < 65536 {
		c.PostgreSQL.Port = v.PostgresPort
	}
	c.PostgreSQL.User = strings.TrimSpace(v.PostgresUser)
	if v.PostgresPassword != "" && v.PostgresPassword != maskSecret {
		c.PostgreSQL.Password = v.PostgresPassword
	}
}

// normalizePanelBaseInput accepts "-", "" and "/prefix" panel bases from the
// UI with the same semantics as the env var: "-" and "" clear the prefix.
func normalizePanelBaseInput(s string) string {
	s = strings.TrimSpace(s)
	if s == "-" || s == "" || s == "/" {
		return ""
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return strings.TrimSuffix(s, "/")
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, settingsFromConfig(s.Cfg))
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var v settingsView
	if !readJSON(w, r, &v) {
		return
	}
	// Validate the web server choice against what's possible.
	switch v.WebServer {
	case "nginx", "apache", "caddy", "go":
	default:
		writeErr(w, http.StatusBadRequest, "invalid web server (nginx|apache|caddy|go)")
		return
	}
	if v.PanelBase != "" && v.PanelBase != "-" && !strings.HasPrefix(v.PanelBase, "/") {
		writeErr(w, http.StatusBadRequest, "panel path must start with '/' (or '-' to serve from the root)")
		return
	}
	v.applyToConfig(s.Cfg)
	if err := s.Cfg.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save config: "+err.Error())
		return
	}
	s.audit(r, "settings.update", "", "")
	// Reload the active web server so path/socket changes take effect where
	// they can; failure is reported but the saved state stands.
	_ = s.Web.Reload()
	writeJSON(w, http.StatusOK, settingsFromConfig(s.Cfg))
}
