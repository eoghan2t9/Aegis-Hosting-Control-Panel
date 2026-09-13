package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
