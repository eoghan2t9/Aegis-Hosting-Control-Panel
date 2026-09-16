package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"

	"aegis/internal/config"
)

// Tuner inspects the host and produces performance-tuned configuration for
// the installed web stack. Tuning runs automatically during initial setup
// (aegisctl setup / first server start) and can be re-run any time.
type Tuner struct {
	Cfg *config.Config
}

func NewTuner(cfg *config.Config) *Tuner { return &Tuner{Cfg: cfg} }

// Binary describes a detected executable (e.g. a PHP version).
type Binary struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Version string `json:"version"`
}

// Inspection is the raw host survey.
type Inspection struct {
	Hostname   string            `json:"hostname"`
	OS         string            `json:"os"`
	Kernel     string            `json:"kernel"`
	CPUCores   int               `json:"cpu_cores"`
	CPUModel   string            `json:"cpu_model"`
	RAMMB      int64             `json:"ram_mb"`
	SwapMB     int64             `json:"swap_mb"`
	Disks      []DiskInfo        `json:"disks"`
	PHP        []Binary          `json:"php"`
	WebServers []string          `json:"web_servers"`
	Databases  []string          `json:"databases"`
	FTP        []string          `json:"ftp"`
	HasSystemd bool              `json:"has_systemd"`
	Sysctl     map[string]string `json:"sysctl"`
}

// NginxTuning values for the generated nginx.conf.
type NginxTuning struct {
	WorkerProcesses   string `json:"worker_processes"`
	WorkerConnections int    `json:"worker_connections"`
	KeepaliveTimeout  int    `json:"keepalive_timeout"`
	Gzip              bool   `json:"gzip"`
	ClientMaxBodySize string `json:"client_max_body_size"`
}

// PHPFPMTuning values for generated php-fpm pools.
type PHPFPMTuning struct {
	PM           string `json:"pm"`
	MaxChildren  int    `json:"max_children"`
	StartServers int    `json:"start_servers"`
	MinSpare     int    `json:"min_spare_servers"`
	MaxSpare     int    `json:"max_spare_servers"`
}

// ApacheTuning values for the mpm_event module — the MPM Aegis targets
// since it never loads mod_php (php always goes through mod_proxy_fcgi to
// a php-fpm socket, see writeApacheVhost in webserver.go), so there's no
// prefork requirement. ServerLimit is included deliberately: Apache's
// compiled-in default (16) silently caps MaxRequestWorkers at
// ServerLimit*ThreadsPerChild regardless of what MaxRequestWorkers itself
// says, a classic Apache tuning trap — raising one without the other does
// nothing.
type ApacheTuning struct {
	MPM               string `json:"mpm"`
	StartServers      int    `json:"start_servers"`
	MinSpareThreads   int    `json:"min_spare_threads"`
	MaxSpareThreads   int    `json:"max_spare_threads"`
	ThreadsPerChild   int    `json:"threads_per_child"`
	ServerLimit       int    `json:"server_limit"`
	MaxRequestWorkers int    `json:"max_request_workers"`
}

// CaddyTuning values for the Caddyfile's global options block. Caddy has no
// worker_processes/worker_connections-equivalent knob to scale — it's Go-
// based like Aegis's own native server and sizes its own concurrency from
// GOMAXPROCS automatically — so what's tunable here is thin: request
// timeouts and the header-size ceiling, not capacity. Kept as fixed,
// conservative values rather than a fabricated cores/RAM-scaled formula
// that wouldn't actually mean anything for this server.
type CaddyTuning struct {
	ReadTimeoutS   int `json:"read_timeout_s"`
	WriteTimeoutS  int `json:"write_timeout_s"`
	IdleTimeoutS   int `json:"idle_timeout_s"`
	MaxHeaderBytes int `json:"max_header_bytes"`
}

// SysctlEntry is one kernel parameter suggestion.
type SysctlEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Note  string `json:"note"`
}

// TuningReport is the persisted result of a tuning run.
type TuningReport struct {
	GeneratedAt   time.Time     `json:"generated_at"`
	Cores         int           `json:"cores"`
	RAMMB         int64         `json:"ram_mb"`
	Nginx         NginxTuning   `json:"nginx"`
	Apache        ApacheTuning  `json:"apache"`
	Caddy         CaddyTuning   `json:"caddy"`
	PHPFPM        PHPFPMTuning  `json:"php_fpm"`
	Sysctl        []SysctlEntry `json:"sysctl"`
	SysctlApplied bool          `json:"sysctl_applied"`
	Note          string        `json:"note"`
}

// Inspect surveys the host.
func (t *Tuner) Inspect() Inspection {
	ins := Inspection{Sysctl: map[string]string{}}
	if hi, err := host.Info(); err == nil {
		ins.Hostname = hi.Hostname
		ins.OS = hi.Platform + " " + hi.PlatformVersion
		ins.Kernel = hi.KernelVersion
	}
	if ci, err := cpu.Info(); err == nil && len(ci) > 0 {
		ins.CPUModel = ci[0].ModelName
	}
	if cores, err := cpu.Counts(true); err == nil {
		ins.CPUCores = cores
	}
	if m, err := mem.VirtualMemory(); err == nil {
		ins.RAMMB = int64(m.Total / 1024 / 1024)
		ins.SwapMB = int64(m.SwapTotal / 1024 / 1024)
	}
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			if strings.HasPrefix(p.Mountpoint, "/snap") || strings.HasPrefix(p.Mountpoint, "/boot") {
				continue
			}
			if u, err := disk.Usage(p.Mountpoint); err == nil && u.Total > 0 {
				ins.Disks = append(ins.Disks, DiskInfo{Mount: p.Mountpoint, Total: u.Total, Used: u.Used, Pct: u.UsedPercent})
			}
		}
	}
	ins.PHP = DetectPHP()
	ins.WebServers = DetectWebServers()
	ins.Databases = DetectDatabases()
	ins.FTP = DetectFTP()
	ins.HasSystemd = LookPath("systemctl")

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if out, err := Exec(ctx, "sysctl", "-a"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			kv := strings.SplitN(line, "=", 2)
			if len(kv) == 2 {
				ins.Sysctl[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}
	return ins
}

// DetectPHP finds installed PHP CLI/FPM versions by scanning PATH and the
// Debian/Ubuntu php dirs.
func DetectPHP() []Binary {
	out := []Binary{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dirs := []string{"/usr/bin", "/usr/sbin", "/usr/local/bin"}
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			var ver string
			switch {
			// Debian/Ubuntu/ondrej name versioned binaries php7.4 / php-fpm7.4
			// (digit right after the prefix); some builds use php.7.4. Accept both.
			case strings.HasPrefix(name, "php") && len(name) > 3 && (name[3] == '.' || (name[3] >= '0' && name[3] <= '9')):
				ver = strings.TrimPrefix(name, "php")
				ver = strings.TrimPrefix(ver, ".")
			case strings.HasPrefix(name, "php-fpm") && len(name) > 7 && (name[7] == '.' || (name[7] >= '0' && name[7] <= '9')):
				ver = strings.TrimPrefix(name, "php-fpm")
				ver = strings.TrimPrefix(ver, ".")
			default:
				continue
			}
			if !seen[ver] {
				seen[ver] = true
				path := filepath.Join(dir, name)
				v := ExecQuiet(ctx, path, "-v")
				full := ""
				if strings.HasPrefix(v, "PHP ") {
					full = strings.Fields(v)[1]
				}
				out = append(out, Binary{Name: "php" + ver, Path: path, Version: full})
			}
		}
	}
	return out
}

// DetectWebServers returns installed web servers among nginx/apache/caddy.
// Detection is broader than LookPath alone: distro packages install the
// binaries in fixed locations that are not always on the panel process's
// PATH (e.g. systemd's default PATH lacks /usr/local/bin), and a stopped
// service would otherwise hide an installed server — which made Caddy
// disappear from the Runtime page's picker.
func DetectWebServers() []string {
	out := []string{}
	for _, s := range []string{"nginx", "apache2", "caddy"} {
		if LookPath(s) || ServiceRunning(s) || webServerBinExists(s) {
			out = append(out, s)
		}
	}
	return out
}

// webServerBinExists checks the standard install locations for a web server
// binary when PATH lookup fails.
func webServerBinExists(server string) bool {
	var candidates []string
	switch server {
	case "nginx":
		candidates = []string{"/usr/sbin/nginx", "/usr/bin/nginx", "/usr/local/sbin/nginx", "/usr/local/bin/nginx"}
	case "apache2":
		candidates = []string{"/usr/sbin/apache2", "/usr/sbin/apache2ctl", "/usr/sbin/httpd", "/usr/local/apache2/bin/httpd"}
	case "caddy":
		candidates = []string{"/usr/bin/caddy", "/usr/local/bin/caddy", "/opt/caddy/caddy"}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

// DetectDatabases returns installed database servers.
func DetectDatabases() []string {
	out := []string{}
	for _, b := range []string{"mariadbd", "mysqld", "postgres"} {
		if LookPath(b) || LookPath("psql") && b == "postgres" {
			out = append(out, b)
		}
	}
	if LookPath("mysql") || LookPath("mariadb") {
		if !containsString(out, "mariadbd") && !containsString(out, "mysqld") {
			out = append(out, "mysql-client")
		}
	}
	return out
}

// DetectFTP returns installed FTP servers.
func DetectFTP() []string {
	out := []string{}
	for _, b := range []string{"vsftpd", "pure-ftpd", "proftpd"} {
		if LookPath(b) || ServiceRunning(b) {
			out = append(out, b)
		}
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Tune computes tuned settings from the inspection and writes the generated
// config files + report into the tuned directory. Does not touch live configs.
func (t *Tuner) Tune() (*TuningReport, error) {
	ins := t.Inspect()
	cores := ins.CPUCores
	if cores < 1 {
		cores = 1
	}
	ramMB := ins.RAMMB
	if ramMB < 256 {
		ramMB = 256
	}

	// nginx: one worker per core, connections scaled to RAM.
	workerConn := 2048 * cores
	if workerConn > 65536 {
		workerConn = 65536
	}
	nginx := NginxTuning{
		WorkerProcesses:   "auto",
		WorkerConnections: workerConn,
		KeepaliveTimeout:  65,
		Gzip:              true,
		ClientMaxBodySize: "128m",
	}
	if cores <= 32 {
		nginx.WorkerProcesses = strconv.Itoa(cores)
	}

	// php-fpm: budget ~1/64 of RAM per worker, capped.
	maxChildren := int(ramMB / 64)
	if maxChildren < 4 {
		maxChildren = 4
	}
	if maxChildren > 200 {
		maxChildren = 200
	}
	php := PHPFPMTuning{
		PM:           "dynamic",
		MaxChildren:  maxChildren,
		StartServers: clamp(maxChildren/4, 2, 40),
		MinSpare:     clamp(maxChildren/8, 1, 20),
		MaxSpare:     clamp(maxChildren/2, 4, 100),
	}

	// apache mpm_event: threads/process budgeted off RAM the same way as
	// php-fpm's own workers. ServerLimit is derived (not guessed) from the
	// raw budget, then MaxRequestWorkers is snapped up to serverLimit's
	// exact multiple of threadsPerChild — Apache requires
	// MaxRequestWorkers to be an integer multiple of ThreadsPerChild and
	// silently rounds down at startup (with a logged warning) if it isn't,
	// so computing it any other way just leaves that warning for the
	// operator to puzzle over every time apache starts.
	threadsPerChild := 25
	rawWorkers := clamp(int(ramMB/24), 150, 2048)
	serverLimit := (rawWorkers + threadsPerChild - 1) / threadsPerChild // ceil
	apache := ApacheTuning{
		MPM:               "event",
		StartServers:      clamp(cores/2, 2, 8),
		MinSpareThreads:   75,
		MaxSpareThreads:   250,
		ThreadsPerChild:   threadsPerChild,
		ServerLimit:       serverLimit,
		MaxRequestWorkers: serverLimit * threadsPerChild,
	}

	caddy := CaddyTuning{
		ReadTimeoutS:   30,
		WriteTimeoutS:  30,
		IdleTimeoutS:   120,
		MaxHeaderBytes: 1048576,
	}

	sysctl := []SysctlEntry{
		{Key: "vm.swappiness", Value: "10", Note: "prefer cache over swap"},
		{Key: "net.core.somaxconn", Value: "1024", Note: "deeper accept queues"},
		{Key: "net.core.netdev_max_backlog", Value: "4096", Note: "nic backlog for bursts"},
		{Key: "net.ipv4.tcp_fastopen", Value: "3", Note: "tcp fast open client+server"},
		{Key: "fs.file-max", Value: strconv.FormatInt(max(ramMB*64, 1048576), 10), Note: "fd limit scaled to ram"},
	}

	report := &TuningReport{
		GeneratedAt: time.Now().UTC(),
		Cores:       cores,
		RAMMB:       ramMB,
		Nginx:       nginx,
		Apache:      apache,
		Caddy:       caddy,
		PHPFPM:      php,
		Sysctl:      sysctl,
	}
	if err := t.WriteReport(report); err != nil {
		return nil, err
	}
	return report, nil
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ReportPath is where the tuning report JSON lives.
func (t *Tuner) ReportPath() string { return filepath.Join(t.Cfg.TunedDir, "tuning-report.json") }

func (t *Tuner) WriteReport(r *TuningReport) error {
	if err := os.MkdirAll(t.Cfg.TunedDir, 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(t.ReportPath(), data, 0o644); err != nil {
		return err
	}
	// Also write the generated fragments for operators to review/copy.
	nginxCfg := fmt.Sprintf("# Aegis tuning report %s — nginx worker settings\nworker_processes %s;\nevents { worker_connections %d; }\nhttp {\n  keepalive_timeout %d;\n  client_max_body_size %s;\n  gzip on;\n  gzip_types text/plain text/css application/json application/javascript application/xml image/svg+xml;\n}\n",
		r.GeneratedAt.Format(time.RFC3339), r.Nginx.WorkerProcesses, r.Nginx.WorkerConnections, r.Nginx.KeepaliveTimeout, r.Nginx.ClientMaxBodySize)
	_ = os.WriteFile(filepath.Join(t.Cfg.TunedDir, "nginx.conf.optimized"), []byte(nginxCfg), 0o644)

	fpmCfg := fmt.Sprintf("; Aegis tuning report %s — php-fpm pool base settings\npm = %s\npm.max_children = %d\npm.start_servers = %d\npm.min_spare_servers = %d\npm.max_spare_servers = %d\npm.max_requests = 500\n",
		r.GeneratedAt.Format(time.RFC3339), r.PHPFPM.PM, r.PHPFPM.MaxChildren, r.PHPFPM.StartServers, r.PHPFPM.MinSpare, r.PHPFPM.MaxSpare)
	_ = os.WriteFile(filepath.Join(t.Cfg.TunedDir, "php-fpm.optimized.conf"), []byte(fpmCfg), 0o644)

	apacheCfg := fmt.Sprintf("# Aegis tuning report %s — apache2 mpm_event settings (mods-available/mpm_event.conf)\n<IfModule mpm_event_module>\n\tStartServers\t\t\t %d\n\tMinSpareThreads\t\t %d\n\tMaxSpareThreads\t\t %d\n\tThreadsPerChild\t\t %d\n\tServerLimit\t\t\t %d\n\tMaxRequestWorkers\t  %d\n\tMaxConnectionsPerChild   0\n</IfModule>\n",
		r.GeneratedAt.Format(time.RFC3339), r.Apache.StartServers, r.Apache.MinSpareThreads, r.Apache.MaxSpareThreads,
		r.Apache.ThreadsPerChild, r.Apache.ServerLimit, r.Apache.MaxRequestWorkers)
	_ = os.WriteFile(filepath.Join(t.Cfg.TunedDir, "apache-mpm.optimized.conf"), []byte(apacheCfg), 0o644)

	caddyCfg := fmt.Sprintf("# Aegis tuning report %s — Caddyfile global options block\n{\n\tservers {\n\t\ttimeouts {\n\t\t\tread_body   %ds\n\t\t\tread_header %ds\n\t\t\twrite       %ds\n\t\t\tidle        %ds\n\t\t}\n\t\tmax_header_size %d\n\t}\n}\n",
		r.GeneratedAt.Format(time.RFC3339), r.Caddy.ReadTimeoutS, r.Caddy.ReadTimeoutS, r.Caddy.WriteTimeoutS, r.Caddy.IdleTimeoutS, r.Caddy.MaxHeaderBytes)
	_ = os.WriteFile(filepath.Join(t.Cfg.TunedDir, "caddy-global.optimized"), []byte(caddyCfg), 0o644)

	var sb strings.Builder
	for _, e := range r.Sysctl {
		fmt.Fprintf(&sb, "%s = %s\n", e.Key, e.Value)
	}
	_ = os.WriteFile(filepath.Join(t.Cfg.TunedDir, "sysctl.aegis.conf"), []byte(sb.String()), 0o644)
	return nil
}

// LoadReport reads the persisted tuning report.
func (t *Tuner) LoadReport() (*TuningReport, error) {
	data, err := os.ReadFile(t.ReportPath())
	if err != nil {
		return nil, err
	}
	var r TuningReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ApplySysctl loads the generated sysctl file (requires root). Only applied
// explicitly (setup --apply or the API "apply tuning" action).
func (t *Tuner) ApplySysctl(report *TuningReport) error {
	path := filepath.Join(t.Cfg.TunedDir, "sysctl.aegis.conf")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("tuned sysctl file missing: %w", err)
	}
	if _, err := RunTimeout(15*time.Second, "sysctl", "-p", path); err != nil {
		return err
	}
	report.SysctlApplied = true
	return t.WriteReport(report)
}

const nginxConfPath = "/etc/nginx/nginx.conf"

// ApplyNginx writes this report's nginx settings into the live nginx.conf
// and reloads nginx, instead of leaving them in the advisory
// nginx.conf.optimized file for an operator to copy by hand. A no-op
// (nil, nil error) when nginx isn't installed — most Aegis hosts don't run
// it (see "go" and the other backends in webserver.go), so this only does
// anything on hosts where it's actually relevant.
//
// worker_processes, events{ worker_connections } and gzip are all
// main/http-context directives that the stock Debian/Ubuntu nginx.conf
// already sets directly — nginx errors on a duplicate directive within the
// same context, and conf.d is included *inside* the http{} block, i.e. the
// same context as nginx.conf's own settings, not a child of it. So these
// three are edited in place, in nginx.conf itself, only when found (an
// unrecognized layout is left untouched rather than guessed at), guarded
// by a validate-then-commit-else-rollback sequence: the original is backed
// up first, nginx -t must pass before the change is kept, and any failure
// restores the backup immediately — this already caught a real bug in an
// earlier version of this function that assumed gzip could go in the
// conf.d snippet below (nginx -t: "gzip" directive is duplicate) and
// rolled it back cleanly instead of breaking nginx.
//
// keepalive_timeout and client_max_body_size aren't set anywhere in the
// stock file, so they're safe to add via the conf.d snippet with no
// collision risk.
func (t *Tuner) ApplyNginx(report *TuningReport) error {
	original, err := os.ReadFile(nginxConfPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("nginx: read nginx.conf: %w", err)
	}

	updated := string(original)
	updated = regexp.MustCompile(`(?m)^(\s*worker_processes\s+)\S+(\s*;)`).
		ReplaceAllString(updated, "${1}"+report.Nginx.WorkerProcesses+"${2}")
	if loc := regexp.MustCompile(`(?s)events\s*\{.*?\}`).FindStringIndex(updated); loc != nil {
		block := regexp.MustCompile(`(?m)(worker_connections\s+)\d+(\s*;)`).
			ReplaceAllString(updated[loc[0]:loc[1]], "${1}"+strconv.Itoa(report.Nginx.WorkerConnections)+"${2}")
		updated = updated[:loc[0]] + block + updated[loc[1]:]
	}
	gzipVal := "off"
	if report.Nginx.Gzip {
		gzipVal = "on"
	}
	updated = regexp.MustCompile(`(?m)^(\s*gzip\s+)(on|off)(\s*;)`).
		ReplaceAllString(updated, "${1}"+gzipVal+"${3}")

	if updated != string(original) {
		backup := nginxConfPath + ".aegis-bak-" + report.GeneratedAt.Format("20060102150405")
		if err := os.WriteFile(backup, original, 0o644); err != nil {
			return fmt.Errorf("nginx: backup nginx.conf: %w", err)
		}
		if err := os.WriteFile(nginxConfPath, []byte(updated), 0o644); err != nil {
			return fmt.Errorf("nginx: write nginx.conf: %w", err)
		}
		if _, err := RunTimeout(10*time.Second, "nginx", "-t"); err != nil {
			_ = os.WriteFile(nginxConfPath, original, 0o644)
			return fmt.Errorf("nginx: generated nginx.conf failed validation, rolled back: %w", err)
		}
	}

	if confd := "/etc/nginx/conf.d"; dirExists(confd) {
		snippet := fmt.Sprintf(
			"# Aegis tuning report %s — regenerated by System -> Performance tuning, do not edit\n"+
				"keepalive_timeout %d;\nclient_max_body_size %s;\n",
			report.GeneratedAt.Format(time.RFC3339), report.Nginx.KeepaliveTimeout, report.Nginx.ClientMaxBodySize)
		snippetPath := filepath.Join(confd, "aegis-tuning.conf")
		if err := os.WriteFile(snippetPath, []byte(snippet), 0o644); err != nil {
			return fmt.Errorf("nginx: write conf.d snippet: %w", err)
		}
		if _, err := RunTimeout(10*time.Second, "nginx", "-t"); err != nil {
			_ = os.Remove(snippetPath)
			return fmt.Errorf("nginx: conf.d snippet failed validation, removed: %w", err)
		}
	}

	if ServiceRunning("nginx") {
		return reloadService("nginx")
	}
	return nil
}

const apacheMPMEventConf = "/etc/apache2/mods-available/mpm_event.conf"

// ApplyApache writes this report's mpm_event settings into the live
// mods-available/mpm_event.conf and reloads apache2, following the same
// backup/validate/rollback sequence as ApplyNginx. A no-op when apache2
// isn't installed, or when mpm_event isn't the MPM actually enabled
// (mods-enabled/mpm_event.conf missing) — Aegis never chooses or switches
// a site's MPM on the operator's behalf, so it won't tune one that isn't
// in use.
func (t *Tuner) ApplyApache(report *TuningReport) error {
	if _, err := os.Stat("/etc/apache2/mods-enabled/mpm_event.conf"); err != nil {
		return nil
	}
	original, err := os.ReadFile(apacheMPMEventConf)
	if err != nil {
		return fmt.Errorf("apache: read mpm_event.conf: %w", err)
	}

	// setDirective replaces name's existing value in place, or — for
	// ServerLimit, which the stock mpm_event.conf omits entirely — adds
	// "name value" as a new line. Newer Apache packaging (verified against
	// the actual installed version here) ships mpm_event.conf with no
	// <IfModule mpm_event_module> wrapper at all — mods-enabled already
	// guarantees it's only loaded when the module is — so this checks for
	// that wrapper rather than assuming it (an earlier version of this
	// function assumed the classic wrapped layout unconditionally: with no
	// </IfModule> to find, strings.Replace silently did nothing, so
	// ServerLimit never actually got added — exactly the "MaxRequestWorkers
	// silently capped" trap this function exists to avoid).
	setDirective := func(s, name string, value int) string {
		re := regexp.MustCompile(`(?m)^(\s*` + name + `\s+)\d+\s*$`)
		if re.MatchString(s) {
			return re.ReplaceAllString(s, "${1}"+strconv.Itoa(value))
		}
		line := name + " " + strconv.Itoa(value) + "\n"
		if strings.Contains(s, "</IfModule>") {
			return strings.Replace(s, "</IfModule>", "\t"+line+"</IfModule>", 1)
		}
		if !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		return s + line
	}
	updated := string(original)
	updated = setDirective(updated, "StartServers", report.Apache.StartServers)
	updated = setDirective(updated, "MinSpareThreads", report.Apache.MinSpareThreads)
	updated = setDirective(updated, "MaxSpareThreads", report.Apache.MaxSpareThreads)
	updated = setDirective(updated, "ThreadsPerChild", report.Apache.ThreadsPerChild)
	updated = setDirective(updated, "ServerLimit", report.Apache.ServerLimit)
	updated = setDirective(updated, "MaxRequestWorkers", report.Apache.MaxRequestWorkers)

	if updated != string(original) {
		backup := apacheMPMEventConf + ".aegis-bak-" + report.GeneratedAt.Format("20060102150405")
		if err := os.WriteFile(backup, original, 0o644); err != nil {
			return fmt.Errorf("apache: backup mpm_event.conf: %w", err)
		}
		if err := os.WriteFile(apacheMPMEventConf, []byte(updated), 0o644); err != nil {
			return fmt.Errorf("apache: write mpm_event.conf: %w", err)
		}
		if _, err := RunTimeout(10*time.Second, "apache2ctl", "configtest"); err != nil {
			_ = os.WriteFile(apacheMPMEventConf, original, 0o644)
			return fmt.Errorf("apache: generated mpm_event.conf failed validation, rolled back: %w", err)
		}
	}

	if ServiceRunning("apache2") {
		return reloadService("apache2")
	}
	return nil
}

const caddyTuningMarker = "# aegis-tuning-global"

func (t *Tuner) caddyMainFile() string {
	if t.Cfg.WebServer.CaddyFile != "" {
		return t.Cfg.WebServer.CaddyFile
	}
	return "/etc/caddy/Caddyfile"
}

// ApplyCaddy writes this report's timeout/header settings into the live
// Caddyfile's global options block and reloads caddy. A no-op when caddy
// isn't installed (no Caddyfile at the configured/default path).
//
// The global options block must be the very first thing in the file for
// Caddy to recognize it as global rather than a site block, so this only
// ever touches (or inserts, at the top) a block carrying caddyTuningMarker
// — an operator's own hand-written global options, if any, are left alone
// rather than merged with or clobbered by guesswork about their shape.
func (t *Tuner) ApplyCaddy(report *TuningReport) error {
	mainFile := t.caddyMainFile()
	original, err := os.ReadFile(mainFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("caddy: read Caddyfile: %w", err)
	}

	block := fmt.Sprintf("%s\n{\n\tservers {\n\t\ttimeouts {\n\t\t\tread_body   %ds\n\t\t\tread_header %ds\n\t\t\twrite       %ds\n\t\t\tidle        %ds\n\t\t}\n\t\tmax_header_size %d\n\t}\n}\n",
		caddyTuningMarker, report.Caddy.ReadTimeoutS, report.Caddy.ReadTimeoutS, report.Caddy.WriteTimeoutS, report.Caddy.IdleTimeoutS, report.Caddy.MaxHeaderBytes)

	var updated string
	if markerRe := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(caddyTuningMarker) + `\n\{.*?\n\}\n`); markerRe.MatchString(string(original)) {
		updated = markerRe.ReplaceAllString(string(original), block)
	} else {
		updated = block + string(original)
	}
	if updated == string(original) {
		return nil
	}

	backup := mainFile + ".aegis-bak-" + report.GeneratedAt.Format("20060102150405")
	if err := os.WriteFile(backup, original, 0o644); err != nil {
		return fmt.Errorf("caddy: backup Caddyfile: %w", err)
	}
	if err := os.WriteFile(mainFile, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("caddy: write Caddyfile: %w", err)
	}
	if LookPath("caddy") {
		if _, err := RunTimeout(15*time.Second, "caddy", "validate", "--config", mainFile); err != nil {
			_ = os.WriteFile(mainFile, original, 0o644)
			return fmt.Errorf("caddy: generated Caddyfile failed validation, rolled back: %w", err)
		}
	}
	if ServiceRunning("caddy") {
		return reloadService("caddy")
	}
	return nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
