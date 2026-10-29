package svc

import (
	"context"
	"fmt"
	"os"
	osuser "os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"aegis/internal/config"
)

// PHP manages php-fpm versions and per-site pools. It supports any installed
// version (7.4 … latest); the panel only manages what's actually present.
type PHP struct {
	Cfg       *config.Config
	SocketDir string
}

func NewPHP(cfg *config.Config) *PHP {
	sd := cfg.WebServer.PHPFpmSocketDir
	if sd == "" {
		sd = "/run/php"
	}
	return &PHP{Cfg: cfg, SocketDir: sd}
}

// Version describes one installed PHP.
type Version struct {
	Version  string `json:"version"` // e.g. "8.3"
	CLI      string `json:"cli"`     // path to php CLI binary
	FPM      string `json:"fpm"`     // path to php-fpm binary
	Running  bool   `json:"running"`
	PoolConf string `json:"pool_conf"` // pool.d directory
	// Support is "eol" (no more security fixes), "ending" (fewer than six months
	// of security fixes left) or "supported"; SupportUntil is that end date.
	Support      string `json:"support"`
	SupportUntil string `json:"support_until,omitempty"`
}

// phpSecurityEnd is when upstream stops releasing security fixes per branch.
var phpSecurityEnd = map[string]string{
	"5.6": "2018-12-31", "7.0": "2018-12-03", "7.1": "2019-12-01", "7.2": "2020-11-30", "7.3": "2021-12-06",
	"7.4": "2022-11-28", "8.0": "2023-11-26", "8.1": "2025-12-31", "8.2": "2026-12-31",
	"8.3": "2027-12-31", "8.4": "2028-12-31", "8.5": "2029-12-31",
}

// phpSupport classifies a PHP branch ("8.2") at time now. Unknown branches are
// treated as supported: they are newer than this table, not older.
func phpSupport(version string, now time.Time) (status, until string) {
	end, ok := phpSecurityEnd[version]
	if !ok {
		return "supported", ""
	}
	t, err := time.Parse("2006-01-02", end)
	if err != nil {
		return "supported", ""
	}
	switch {
	case now.After(t):
		return "eol", end
	case now.AddDate(0, 6, 0).After(t):
		return "ending", end
	}
	return "supported", end
}

// Versions returns installed PHP versions sorted descending (newest first).
func (p *PHP) Versions() []Version {
	bins := DetectPHP()
	// Build per-version entries from CLI binaries; attach fpm where found.
	out := []Version{}
	cliByVer := map[string]string{}
	fpmByVer := map[string]string{}
	for _, b := range bins {
		ver := strings.TrimPrefix(b.Name, "php")
		if strings.HasPrefix(b.Name, "php-fpm") {
			fpmByVer[strings.TrimPrefix(b.Name, "php-fpm")] = b.Path
		} else {
			cliByVer[ver] = b.Path
		}
	}
	for ver, cli := range cliByVer {
		v := Version{
			Version:  ver,
			CLI:      cli,
			FPM:      fpmByVer[ver],
			Running:  fpmRunning(ver),
			PoolConf: filepath.Join("/etc/php", ver, "fpm/pool.d"),
		}
		v.Support, v.SupportUntil = phpSupport(ver, time.Now())
		if v.FPM == "" {
			for _, cand := range []string{"/usr/sbin/php-fpm" + ver, "/usr/bin/php-fpm" + ver} {
				if _, err := os.Stat(cand); err == nil {
					v.FPM = cand
				}
			}
		}
		if v.FPM != "" {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out
}

// fpmRunning reports whether the php-fpm master for a version is up. Uses
// the systemd unit name on systemd hosts and the master-process cmdline
// ("php-fpm: master process (/etc/php/X.Y/…)") elsewhere, e.g. containers
// where the version's fpm runs under supervisor.
func fpmRunning(version string) bool {
	if ServiceRunning("php" + version + "-fpm") {
		return true
	}
	return ProcessRunning("php-fpm: master process \\(/etc/php/" + version + "/")
}

// Has returns true when the given version is installed.
func (p *PHP) Has(version string) bool {
	for _, v := range p.Versions() {
		if v.Version == version {
			return true
		}
	}
	return false
}

// Latest returns the newest installed version.
func (p *PHP) Latest() string {
	vs := p.Versions()
	if len(vs) == 0 {
		return ""
	}
	return vs[0].Version
}

// SocketPath is the unix socket for a domain's php-fpm pool.
func (p *PHP) SocketPath(domain string) string {
	return filepath.Join(p.SocketDir, "aegis-"+domain+".sock")
}

// SubSocketPath is the unix socket of the extra pool that serves a domain's
// sub-folders on another PHP version. "@" cannot occur in a valid domain, so
// it never collides with a real domain's own socket or pool file.
func (p *PHP) SubSocketPath(domain, version string) string {
	return filepath.Join(p.SocketDir, "aegis-"+domain+"@"+version+".sock")
}

// phpIniDirective describes one user-editable php.ini directive exposed by
// the panel's PHP settings screen. kind picks whether it's written as
// php_admin_value or php_admin_flag into the pool config; re validates the
// value both at the API boundary (ValidatePHPIniSettings) and again here
// (sanitizedPHPIniLines) — defense in depth, since a pool.d file is plain
// text and an unvalidated value (e.g. containing a newline) could otherwise
// inject arbitrary extra directives into it.
type phpIniDirective struct {
	kind string // "value" or "flag"
	re   *regexp.Regexp
}

var phpIniDirectives = map[string]phpIniDirective{
	"memory_limit":           {"value", regexp.MustCompile(`(?i)^-1$|^[0-9]+[kmg]?$`)},
	"upload_max_filesize":    {"value", regexp.MustCompile(`(?i)^[0-9]+[kmg]?$`)},
	"post_max_size":          {"value", regexp.MustCompile(`(?i)^[0-9]+[kmg]?$`)},
	"max_execution_time":     {"value", regexp.MustCompile(`^[0-9]+$`)},
	"max_input_time":         {"value", regexp.MustCompile(`^-?[0-9]+$`)},
	"max_input_vars":         {"value", regexp.MustCompile(`^[0-9]+$`)},
	"session.gc_maxlifetime": {"value", regexp.MustCompile(`^[0-9]+$`)},
	"date.timezone":          {"value", regexp.MustCompile(`^[A-Za-z_]+(/[A-Za-z_]+)*$`)},
	"display_errors":         {"flag", regexp.MustCompile(`(?i)^(on|off)$`)},
	// OPcache: only the INI_ALL directives can differ per pool; the rest
	// (memory_consumption, max_accelerated_files, ...) are global to the master.
	"opcache.enable":              {"flag", regexp.MustCompile(`(?i)^(on|off)$`)},
	"opcache.validate_timestamps": {"flag", regexp.MustCompile(`(?i)^(on|off)$`)},
	"opcache.revalidate_freq":     {"value", regexp.MustCompile(`^[0-9]{1,5}$`)},
}

// PHPIniDirectiveKeys lists the settings the panel exposes, in the fixed
// order they're written to the pool file.
var PHPIniDirectiveKeys = []string{
	"memory_limit", "upload_max_filesize", "post_max_size",
	"max_execution_time", "max_input_time", "max_input_vars",
	"session.gc_maxlifetime", "date.timezone", "display_errors",
	"opcache.enable", "opcache.validate_timestamps", "opcache.revalidate_freq",
}

// ValidatePHPIniSettings rejects any key outside PHPIniDirectiveKeys or any
// value that doesn't match that directive's expected shape. Called at the
// API boundary so a bad request fails clearly instead of writing a broken
// (or, if unchecked, injectable) pool config.
func ValidatePHPIniSettings(settings map[string]string) error {
	for k, v := range settings {
		d, ok := phpIniDirectives[k]
		if !ok {
			return fmt.Errorf("unknown php setting %q", k)
		}
		if !d.re.MatchString(strings.TrimSpace(v)) {
			return fmt.Errorf("invalid value for %s: %q", k, v)
		}
	}
	return nil
}

// defaultPHPIni is what every pool gets unless the domain overrides it. PHP's
// stock 2M upload / 8M post limits rejected ordinary uploads (APKs, backups,
// media), so they are raised well above anything a site normally needs;
// post_max_size stays above upload_max_filesize so a full-size file plus its
// form fields still fits. Time limits match the pool's request_terminate_timeout.
var defaultPHPIni = map[string]string{
	"display_errors":      "off",
	"upload_max_filesize": "2G",
	"post_max_size":       "2G",
	"memory_limit":        "512M",
	"max_execution_time":  "300",
	"max_input_time":      "300",
}

// sanitizedPHPIniLines re-validates settings (see phpIniDirective's comment)
// and formats them as php_admin_value/php_admin_flag pool.d lines in a fixed
// order. Anything not set per domain falls back to defaultPHPIni.
func sanitizedPHPIniLines(settings map[string]string) string {
	merged := make(map[string]string, len(defaultPHPIni)+len(settings))
	for k, v := range defaultPHPIni {
		merged[k] = v
	}
	for k, v := range settings {
		d, ok := phpIniDirectives[k]
		v = strings.TrimSpace(v)
		if !ok || !d.re.MatchString(v) {
			continue
		}
		merged[k] = v
	}
	var b strings.Builder
	for _, key := range PHPIniDirectiveKeys {
		v, ok := merged[key]
		if !ok {
			continue
		}
		if phpIniDirectives[key].kind == "flag" {
			fmt.Fprintf(&b, "php_admin_flag[%s] = %s\n", key, strings.ToLower(v))
		} else {
			fmt.Fprintf(&b, "php_admin_value[%s] = %s\n", key, v)
		}
	}
	return b.String()
}

// defaultTuning is what EnsurePool falls back to when a caller doesn't pass
// explicit per-pool tuning — the host's saved System → Performance tuning
// report (svc.Tuner) when one has been generated, since that's sized for
// the box's actual cores/RAM, else a conservative baseline that works
// anywhere. Previously this was always the hardcoded baseline regardless of
// whether a tuning report existed, so running the tuning tool never
// actually changed any domain's php-fpm pool.
func (p *PHP) defaultTuning() *PHPFPMTuning {
	if report, err := NewTuner(p.Cfg).LoadReport(); err == nil {
		t := report.PHPFPM
		return &t
	}
	return &PHPFPMTuning{PM: "dynamic", MaxChildren: 8, StartServers: 2, MinSpare: 1, MaxSpare: 4}
}

// EnsurePool writes (or rewrites) the php-fpm pool for a domain and reloads
// the matching fpm service. poolName must be filesystem-safe (it is derived
// from the domain, which is validated). iniSettings may be nil.
func (p *PHP) EnsurePool(domain, systemUser, version string, tuning *PHPFPMTuning, iniSettings map[string]string) error {
	return p.writePool(domain, domain, "aegis-"+domain, p.SocketPath(domain), systemUser, version, tuning, iniSettings, true)
}

// EnsureSubPools provisions one extra pool per distinct PHP version that the
// domain's sub-folders use (paths maps URL prefix -> version), and removes the
// extra pools nothing uses any more. A folder on the domain's own version needs
// no extra pool — it shares the main one.
func (p *PHP) EnsureSubPools(domain, systemUser, mainVersion string, paths map[string]string, tuning *PHPFPMTuning, iniSettings map[string]string) error {
	keep := map[string]bool{}
	for _, v := range paths {
		if v != "" && v != mainVersion {
			keep[v] = true
		}
	}
	for v := range keep {
		name := domain + "@" + v
		if err := p.writePool(domain, name, "aegis-"+name, p.SubSocketPath(domain, v), systemUser, v, tuning, iniSettings, false); err != nil {
			return err
		}
	}
	p.removeSubPools(domain, keep)
	return nil
}

// removeSubPools deletes the domain's extra pools for every version not in
// keep and reloads the FPMs that lost one.
func (p *PHP) removeSubPools(domain string, keep map[string]bool) {
	for _, ver := range removeSubPoolFiles("/etc/php", domain, keep) {
		_ = p.Reload(ver)
	}
}

func removeSubPoolFiles(etcPHP, domain string, keep map[string]bool) []string {
	files, _ := filepath.Glob(filepath.Join(etcPHP, "*", "fpm", "pool.d", "aegis-"+domain+"@*.conf"))
	var changed []string
	for _, f := range files {
		ver := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(f))))
		if keep[ver] {
			continue
		}
		if os.Remove(f) == nil {
			changed = append(changed, ver)
		}
	}
	return changed
}

// writePool renders and installs one pool file. mainPool is true for the
// domain's own pool, the only one that evicts the domain's pool from other PHP
// versions (see below).
func (p *PHP) writePool(domain, poolName, fileBase, sock, systemUser, version string, tuning *PHPFPMTuning, iniSettings map[string]string, mainPool bool) error {
	if !p.Has(version) {
		return fmt.Errorf("php %s is not installed", version)
	}
	if tuning == nil {
		tuning = p.defaultTuning()
	}
	// Aegis system accounts are created with primary group www-data (so nginx
	// can serve the site); use the account's real primary group in the pool.
	group := primaryGroup(systemUser)
	pool := fmt.Sprintf(`; Aegis-managed pool for %s
[%s]
user = %s
group = %s
listen = %s
listen.owner = www-data
listen.group = www-data
listen.mode = 0660
pm = %s
pm.max_children = %d
pm.start_servers = %d
pm.min_spare_servers = %d
pm.max_spare_servers = %d
pm.max_requests = 500
request_terminate_timeout = 300
catch_workers_output = yes
; Aegis's own goserver.go .htaccess interpreter is the sole gatekeeper for
; which files it hands to FastCGI at all (including non-.php names an
; "AddType application/x-httpd-php .html" directive maps onto PHP) — FPM's
; own default (.php only, even though it's commented out in the stock
; www.conf template) would otherwise redundantly reject anything else with
; "Access denied.", second-guessing a decision already made correctly.
security.limit_extensions =
php_admin_value[open_basedir] = %s:%s/tmp
; PHP's default upload/temp dir is /tmp, which open_basedir forbids, so every
; upload failed with "Missing a temporary folder". Give the account its own.
php_admin_value[upload_tmp_dir] = %s
php_admin_value[sys_temp_dir] = %s
%s`, domain, poolName, systemUser, group, sock,
		tuning.PM, tuning.MaxChildren, tuning.StartServers, tuning.MinSpare, tuning.MaxSpare,
		p.Cfg.HomeRoot, p.Cfg.HomeRoot, phpTmpDir(p.Cfg.HomeRoot, systemUser), phpTmpDir(p.Cfg.HomeRoot, systemUser),
		sanitizedPHPIniLines(iniSettings))
	if err := ensurePHPTmp(p.Cfg.HomeRoot, systemUser); err != nil {
		return err
	}

	// A site has exactly one pool. When it moves to another PHP version the old
	// version's pool file must go first, and that FPM must let go of the socket:
	// otherwise the new version fails to bind it, refuses to start, and every
	// other site on that version goes down with it.
	if mainPool {
		for _, old := range removeStalePools("/etc/php", domain, version) {
			_ = p.Reload(old)
		}
	}

	poolDir := filepath.Join("/etc/php", version, "fpm/pool.d")
	if err := os.MkdirAll(poolDir, 0o755); err != nil {
		return fmt.Errorf("php: mkdir pool dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(poolDir, fileBase+".conf"), []byte(pool), 0o644); err != nil {
		return fmt.Errorf("php: write pool: %w", err)
	}
	return p.Reload(version)
}

// phpTmpDir is the account's private PHP temp directory, under <home>/tmp,
// the one extra path every pool's open_basedir allows.
func phpTmpDir(homeRoot, user string) string {
	return filepath.Join(homeRoot, "tmp", user)
}

// ensurePHPTmp creates that directory. <home>/tmp is root-owned and outside any
// customer's home, so a customer cannot swap a path component for a symlink
// while root chowns it; the per-account directory is 0700 so tenants cannot
// read each other's half-uploaded files.
func ensurePHPTmp(homeRoot, user string) error {
	u, err := osuser.Lookup(user)
	if err != nil {
		return fmt.Errorf("php: tmp dir: %w", err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	parent := filepath.Join(homeRoot, "tmp")
	if err := os.MkdirAll(parent, 0o711); err != nil {
		return fmt.Errorf("php: tmp dir: %w", err)
	}
	dir := phpTmpDir(homeRoot, user)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("php: tmp dir: %w", err)
	}
	if err := os.Lchown(dir, uid, gid); err != nil {
		return fmt.Errorf("php: tmp dir: %w", err)
	}
	return os.Chmod(dir, 0o700)
}

// removeStalePools deletes the domain's pool file from every PHP version under
// etcPHP except keep, and returns the versions it changed (which need a reload).
func removeStalePools(etcPHP, domain, keep string) []string {
	files, _ := filepath.Glob(filepath.Join(etcPHP, "*", "fpm", "pool.d", "aegis-"+domain+".conf"))
	var changed []string
	for _, f := range files {
		ver := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(f))))
		if ver == keep {
			continue
		}
		if os.Remove(f) == nil {
			changed = append(changed, ver)
		}
	}
	return changed
}

// RemovePool deletes the pool config for a domain and reloads fpm.
func (p *PHP) RemovePool(domain, version string) error {
	p.removeSubPools(domain, nil)
	if version == "" {
		// Best-effort: remove from any version that has it.
		for _, v := range p.Versions() {
			_ = os.Remove(filepath.Join(v.PoolConf, "aegis-"+domain+".conf"))
			_ = p.Reload(v.Version)
		}
		return nil
	}
	_ = os.Remove(filepath.Join("/etc/php", version, "fpm/pool.d", "aegis-"+domain+".conf"))
	return p.Reload(version)
}

// systemdIsInit reports whether PID 1 is systemd (a binary named systemctl can
// exist in containers/initramfs without an active systemd bus).
func systemdIsInit() bool {
	if !LookPath("systemctl") {
		return false
	}
	data, err := os.ReadFile("/proc/1/comm")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "systemd"
}

// Reload signals a php-fpm version to reload its configuration.
func (p *PHP) Reload(version string) error {
	svcName := "php" + version + "-fpm"
	if systemdIsInit() {
		if _, err := RunTimeout(20*time.Second, "systemctl", "reload", svcName); err == nil {
			return nil
		}
		if _, err := RunTimeout(20*time.Second, "systemctl", "restart", svcName); err != nil {
			return fmt.Errorf("php: could not reload %s: %w", svcName, err)
		}
		return nil
	}
	// No systemd (containers, non-systemd hosts): send SIGUSR2 to the master
	// fpm process, e.g. "php-fpm: master process (/etc/php/8.2/fpm/…)".
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if LookPath("pkill") {
		// Signal by exact master cmdline; a bare "php-fpm:"+version could match
		// nothing when the version separator differs, so match on the conf path.
		pat := "php-fpm: master process \\(/etc/php/" + version + "/"
		if out, err := Exec(ctx, "pkill", "-USR2", "-f", pat); err == nil || strings.Contains(out, "No matching") {
			return nil
		}
	}
	// Last resort: reload via the fpm socket dir marker is not available, so
	// report success only if the master is at least running (supervisord in the
	// dev container owns restarts anyway).
	if fpmRunning(version) {
		return nil
	}
	return fmt.Errorf("php: could not reload %s (no systemd and no matching fpm master)", svcName)
}

var phpPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~@+-]+(/[A-Za-z0-9._~@+-]+)*$`)

const maxPHPPaths = 32

// ValidatePHPPaths checks and normalises a domain's per-folder PHP versions:
// keys are URL path prefixes, so they are held to a strict character set (no
// "..", no empty segments, not the root — that is the domain's own version),
// and every version must be installed. Entries with an empty version are
// dropped ("use the domain's version"). The returned map is a fresh copy.
func ValidatePHPPaths(in map[string]string, has func(version string) bool) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range in {
		k = strings.TrimRight(strings.TrimSpace(k), "/")
		if v == "" {
			continue
		}
		if !phpPathRe.MatchString(k) {
			return nil, fmt.Errorf("invalid folder %q: use a path like /app or /tools/panel", k)
		}
		for _, seg := range strings.Split(k, "/") {
			if seg == "." || seg == ".." {
				return nil, fmt.Errorf("invalid folder %q", k)
			}
		}
		if !has(v) {
			return nil, fmt.Errorf("php %s is not installed", v)
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("folder %q is listed twice", k)
		}
		out[k] = v
	}
	if len(out) > maxPHPPaths {
		return nil, fmt.Errorf("at most %d folders can have their own PHP version", maxPHPPaths)
	}
	return out, nil
}
