package store

import "time"

// Roles and statuses.
const (
	RoleAdmin    = "admin"
	RoleReseller = "reseller"
	RoleUser     = "user"

	StatusActive    = "active"
	StatusSuspended = "suspended"
)

// User is a panel account. Role is admin/reseller/user; owner_id is the
// account that created this one (used for reseller scoping).
type User struct {
	ID                  int64     `json:"id"`
	Username            string    `json:"username"`
	Email               string    `json:"email"`
	PasswordHash        string    `json:"-"`
	Role                string    `json:"role"`
	PackageID           int64     `json:"package_id"`
	Status              string    `json:"status"`
	OwnerID             int64     `json:"owner_id"`
	HomeDir             string    `json:"home_dir"`
	QuotaDiskBytes      int64     `json:"quota_disk_bytes"` // 0 = unlimited
	QuotaBandwidthBytes int64     `json:"quota_bandwidth_bytes"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`

	// Populated on list requests.
	DiskUsedBytes int64  `json:"disk_used_bytes,omitempty"`
	DomainCount   int    `json:"domain_count,omitempty"`
	PackageName   string `json:"package_name,omitempty"`
}

// Package is a hosting plan: quotas and feature flags.
type Package struct {
	ID                  int64     `json:"id"`
	Name                string    `json:"name"`
	Description         string    `json:"description"`
	MaxDomains          int       `json:"max_domains"`
	MaxDatabases        int       `json:"max_databases"`
	MaxFTPAccounts      int       `json:"max_ftp_accounts"`
	DiskQuotaBytes      int64     `json:"disk_quota_bytes"`
	BandwidthQuotaBytes int64     `json:"bandwidth_quota_bytes"`
	AllowSSL            bool      `json:"allow_ssl"`
	AllowDNS            bool      `json:"allow_dns"`
	AllowTerminal       bool      `json:"allow_terminal"`
	AllowBackups        bool      `json:"allow_backups"`
	IsDefault           bool      `json:"is_default"`
	CreatedAt           time.Time `json:"created_at"`
}

// Domain is a website attached to a user.
type Domain struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"user_id"`
	Domain       string    `json:"domain"`
	DocumentRoot string    `json:"document_root"`
	PHPVersion   string    `json:"php_version"`
	WebServer    string    `json:"webserver"`
	SSLEnabled   bool      `json:"ssl_enabled"`
	SSLCertPath  string    `json:"ssl_cert_path"`
	SSLKeyPath   string    `json:"ssl_key_path"`
	SSLProvider  string    `json:"ssl_provider"`
	SSLAutoRenew bool      `json:"ssl_auto_renew"`
	CreatedAt    time.Time `json:"created_at"`
}

// DNSZone links a domain to a DNS provider. provider=local means the built-in
// authoritative server / bind zone files.
type DNSZone struct {
	ID             int64      `json:"id"`
	DomainID       int64      `json:"domain_id"`
	Domain         string     `json:"domain,omitempty"`
	Provider       string     `json:"provider"`
	ProviderZoneID string     `json:"provider_zone_id"`
	SyncedAt       *time.Time `json:"synced_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

// DNSRecord types supported.
const (
	RecordA     = "A"
	RecordAAAA  = "AAAA"
	RecordCNAME = "CNAME"
	RecordMX    = "MX"
	RecordTXT   = "TXT"
	RecordNS    = "NS"
	RecordSRV   = "SRV"
	RecordCAA   = "CAA"
)

type DNSRecord struct {
	ID        int64     `json:"id"`
	ZoneID    int64     `json:"zone_id"`
	Name      string    `json:"name"` // '@' for apex, or relative name
	Type      string    `json:"type"`
	TTL       int       `json:"ttl"`
	Priority  int       `json:"priority"`
	Content   string    `json:"content"`
	Proxied   bool      `json:"proxied"`
	CreatedAt time.Time `json:"created_at"`
}

// SSLOrder tracks a certificate lifecycle for a domain.
type SSLOrder struct {
	ID        int64      `json:"id"`
	DomainID  int64      `json:"domain_id"`
	Domain    string     `json:"domain,omitempty"`
	Status    string     `json:"status"` // pending|issued|failed|renewing
	Provider  string     `json:"provider"`
	Challenge string     `json:"challenge"` // http|dns
	CertPath  string     `json:"cert_path"`
	KeyPath   string     `json:"key_path"`
	ExpiresAt *time.Time `json:"expires_at"`
	Error     string     `json:"error"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// FTPAccount is an FTP login. Each panel user gets a primary account (same
// username) plus optional extra accounts rooted inside their home.
type FTPAccount struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"user_id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	HomeDir      string    `json:"home_dir"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
}

// Database is a user database on a managed server.
type Database struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"user_id"`
	Server     string    `json:"server"` // mariadb|postgres
	Name       string    `json:"name"`
	DBUser     string    `json:"db_user"`
	DBPassword string    `json:"db_password"` // plaintext: needed to dump/restore
	CreatedAt  time.Time `json:"created_at"`
}

// Provider is a DNS provider credential (e.g. Cloudflare API token).
type Provider struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"` // plugin id: cloudflare|route53|...
	Label     string    `json:"label"`
	APIKeyEnc string    `json:"-"`
	Email     string    `json:"email"`
	Config    string    `json:"config"` // extra plugin config as JSON
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// AuditEntry is one row of the audit log.
type AuditEntry struct {
	ID        int64     `json:"id"`
	ActorID   int64     `json:"actor_id"`
	ActorName string    `json:"actor_name"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
}

// BackupTarget kinds.
const (
	BackupKindS3   = "s3"
	BackupKindB2   = "b2"
	BackupKindSFTP = "sftp"
)

// BackupTarget is an offsite destination backups are pushed to after
// creation. ConfigEnc holds the connection details (bucket/endpoint/key or
// SFTP host/user/key), encrypted with the panel Cipher — same pattern as
// dns_providers.api_key_enc.
type BackupTarget struct {
	ID            int64     `json:"id"`
	Kind          string    `json:"kind"`
	Label         string    `json:"label"`
	ConfigEnc     string    `json:"-"`
	RetentionDays int       `json:"retention_days"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
}

// LoginAttempt is one row of the login-throttling log.
type LoginAttempt struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	IP        string    `json:"ip"`
	Success   bool      `json:"success"`
	CreatedAt time.Time `json:"created_at"`
}

// APIToken is a scoped, expiring credential for scripting against the panel.
// Only its bcrypt hash is stored; the raw token is shown once at creation.
type APIToken struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Label      string     `json:"label"`
	TokenHash  string     `json:"-"`
	Scopes     string     `json:"scopes"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// MailDomain marks a domain as mail-enabled: Postfix/Dovecot serve virtual
// mailboxes for it, and it has a DKIM signing key.
type MailDomain struct {
	ID            int64     `json:"id"`
	DomainID      int64     `json:"domain_id"`
	Domain        string    `json:"domain"`
	DKIMSelector  string    `json:"dkim_selector"`
	DKIMPublicKey string    `json:"dkim_public_key"`
	CreatedAt     time.Time `json:"created_at"`
}

// Mailbox is a virtual mailbox: not a system user, served by Postfix/Dovecot
// querying this row directly (via their own sqlite maps against this DB).
type Mailbox struct {
	ID           int64     `json:"id"`
	MailDomainID int64     `json:"mail_domain_id"`
	Domain       string    `json:"domain,omitempty"`
	Localpart    string    `json:"localpart"`
	PasswordHash string    `json:"-"`
	QuotaBytes   int64     `json:"quota_bytes"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
}

// MailAlias forwards mail for source (a localpart) to destination (a full
// address), within one mail domain.
type MailAlias struct {
	ID           int64     `json:"id"`
	MailDomainID int64     `json:"mail_domain_id"`
	Domain       string    `json:"domain,omitempty"`
	Source       string    `json:"source"`
	Destination  string    `json:"destination"`
	CreatedAt    time.Time `json:"created_at"`
}

// CronJob is a scheduled command run under a panel user's own system account
// via the real crontab (crontab -u <username>), not an in-process scheduler.
type CronJob struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Schedule  string    `json:"schedule"` // 5-field cron expression
	Command   string    `json:"command"`
	LogPath   string    `json:"log_path"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// Session is an active login session (revocable).
type Session struct {
	ID        string    `json:"id"`
	UserID    int64     `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}
