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

// Package is a hosting plan: quotas and feature flags. The Allow* flags
// control which areas of the control panel a package grants access to; they
// gate the API (withFeature in internal/api) and drive the frontend nav
// (features list on /auth/me).
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
	AllowMail           bool      `json:"allow_mail"`
	AllowWebmail        bool      `json:"allow_webmail"`
	AllowDatabases      bool      `json:"allow_databases"`
	AllowFiles          bool      `json:"allow_files"`
	AllowFTP            bool      `json:"allow_ftp"`
	AllowCron           bool      `json:"allow_cron"`
	AllowDocker         bool      `json:"allow_docker"`
	MaxContainers       int       `json:"max_containers"` // 0 = unlimited
	IsDefault           bool      `json:"is_default"`
	CreatedAt           time.Time `json:"created_at"`
}

// Domain is a website attached to a user.
type Domain struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"user_id"`
	Domain       string `json:"domain"`
	DocumentRoot string `json:"document_root"`
	PHPVersion   string `json:"php_version"`
	WebServer    string `json:"webserver"`
	SSLEnabled   bool   `json:"ssl_enabled"`
	SSLCertPath  string `json:"ssl_cert_path"`
	SSLKeyPath   string `json:"ssl_key_path"`
	SSLProvider  string `json:"ssl_provider"`
	SSLAutoRenew bool   `json:"ssl_auto_renew"`
	// ProxyTarget, when set (host:port), makes the generated vhost reverse
	// proxy every request there instead of serving DocumentRoot/PHP — how a
	// Docker container (or any future non-PHP app) attaches to a domain. Set
	// and cleared by svc.Docker, never edited directly through the domain API.
	ProxyTarget string `json:"proxy_target,omitempty"`
	// PHPSettings holds per-domain php.ini overrides (memory_limit,
	// upload_max_filesize, etc.) written into this domain's isolated FPM
	// pool by svc.PHP.EnsurePool. Keys are restricted to
	// svc.PHPIniDirectiveKeys and validated by svc.ValidatePHPIniSettings —
	// see internal/svc/php.go.
	PHPSettings map[string]string `json:"php_settings"`
	// IPID references IP.ID (0 = unassigned: the vhost keeps listening on
	// the wildcard address instead of a specific one). Set via svc.IPs so
	// the "dedicated means exactly one domain" invariant is enforced;
	// never edited directly through the domain API.
	IPID int64 `json:"ip_id,omitempty"`
	// IPAddress is IP.Address joined in at read time for convenience
	// (empty when IPID is 0) — see store/domains.go's domainCols query.
	IPAddress string    `json:"ip_address,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// IP is one address in the panel's managed IP pool. kind="shared" (the
// default) means many domains may point vhosts at it; kind="dedicated"
// means at most one domain may (enforced by svc.IPs.Assign, not the DB).
type IP struct {
	ID        int64     `json:"id"`
	Address   string    `json:"address"`
	Label     string    `json:"label"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
}

const (
	IPKindShared    = "shared"
	IPKindDedicated = "dedicated"
)

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

// PackageUpdate is one outdated OS package, as of the last check-updates
// run. The table is cleared and repopulated on every run rather than
// diffed, so it always reflects exactly the latest check.
type PackageUpdate struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	CurrentVersion string    `json:"current_version"`
	NewVersion     string    `json:"new_version"`
	Security       bool      `json:"security"`
	CheckedAt      time.Time `json:"checked_at"`
}

// PortMap is one published port on a container: ContainerPort is the port
// the process inside the container listens on, HostPort is where it's
// reachable on the server (allocated from svc.Docker's managed range unless
// the caller pins one). Public defaults to false — the port binds to
// 127.0.0.1 only, reachable exclusively through a domain's vhost proxy (see
// Domain.ProxyTarget). A standalone, non-HTTP service (a game server, a
// database meant to be reached directly) must set Public to bind 0.0.0.0
// instead; nothing is internet-reachable unless this is set explicitly.
type PortMap struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	Proto         string `json:"proto"` // tcp | udp
	Public        bool   `json:"public"`
}

// VolumeMount binds a path inside the owner's home directory (HostPath, home
// relative — validated by svc.Files.Resolve, same as the file manager) into
// the container at ContainerPath.
type VolumeMount struct {
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
}

// Container is a Docker container provisioned for a panel user, run via the
// `docker` CLI (see svc/docker.go) under the fixed name "aegis-c<ID>". When
// DomainID is set and WebPort matches one of Ports' ContainerPort entries,
// the domain's vhost reverse-proxies to that port's HostPort instead of
// serving PHP — see Domain.ProxyTarget.
type Container struct {
	ID            int64             `json:"id"`
	UserID        int64             `json:"user_id"`
	DomainID      int64             `json:"domain_id,omitempty"`
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Ports         []PortMap         `json:"ports"`
	WebPort       int               `json:"web_port,omitempty"`
	Env           map[string]string `json:"env"`
	Volumes       []VolumeMount     `json:"volumes"`
	RestartPolicy string            `json:"restart_policy"`
	MemoryLimitMB int               `json:"memory_limit_mb,omitempty"`
	CPULimit      string            `json:"cpu_limit,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`

	// Status is populated live from `docker inspect` by List/Get — it is
	// never persisted, so it can never drift from what Docker actually
	// reports (crashes, OOM kills, manual `docker` CLI use on the host).
	Status string `json:"status,omitempty"`
}

// Session is an active login session (revocable).
type Session struct {
	ID        string    `json:"id"`
	UserID    int64     `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}
