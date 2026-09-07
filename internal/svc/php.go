package svc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
}

// Versions returns installed PHP versions sorted descending (newest first).
func (p *PHP) Versions() []Version {
	bins := DetectPHP()
	// Build per-version entries from CLI binaries; attach fpm where found.
	var out []Version
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
			Running:  ServiceRunning("php" + ver + "-fpm"),
			PoolConf: filepath.Join("/etc/php", ver, "fpm/pool.d"),
		}
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

// EnsurePool writes (or rewrites) the php-fpm pool for a domain and reloads
// the matching fpm service. poolName must be filesystem-safe (it is derived
// from the domain, which is validated).
func (p *PHP) EnsurePool(domain, systemUser, version string, tuning *PHPFPMTuning) error {
	if !p.Has(version) {
		return fmt.Errorf("php %s is not installed", version)
	}
	if tuning == nil {
		tuning = &PHPFPMTuning{PM: "dynamic", MaxChildren: 8, StartServers: 2, MinSpare: 1, MaxSpare: 4}
	}
	sock := p.SocketPath(domain)
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
php_admin_value[open_basedir] = %s:%s/tmp
php_admin_flag[display_errors] = off
`, domain, domain, systemUser, systemUser, sock,
		tuning.PM, tuning.MaxChildren, tuning.StartServers, tuning.MinSpare, tuning.MaxSpare,
		p.Cfg.HomeRoot, p.Cfg.HomeRoot)

	poolDir := filepath.Join("/etc/php", version, "fpm/pool.d")
	if err := os.MkdirAll(poolDir, 0o755); err != nil {
		return fmt.Errorf("php: mkdir pool dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(poolDir, "aegis-"+domain+".conf"), []byte(pool), 0o644); err != nil {
		return fmt.Errorf("php: write pool: %w", err)
	}
	return p.Reload(version)
}

// RemovePool deletes the pool config for a domain and reloads fpm.
func (p *PHP) RemovePool(domain, version string) error {
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

// Reload signals a php-fpm version to reload its configuration.
func (p *PHP) Reload(version string) error {
	svcName := "php" + version + "-fpm"
	if LookPath("systemctl") {
		if _, err := RunTimeout(20*time.Second, "systemctl", "reload", svcName); err == nil {
			return nil
		}
		if _, err := RunTimeout(20*time.Second, "systemctl", "restart", svcName); err != nil {
			return fmt.Errorf("php: could not reload %s: %w", svcName, err)
		}
		return nil
	}
	// No systemd: send SIGUSR2 to the master fpm process.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := Exec(ctx, "pkill", "-USR2", "-f", "php-fpm:"+version); err != nil && !strings.Contains(out, "No matching") {
		return fmt.Errorf("php: could not signal %s: %w", svcName, err)
	}
	return nil
}
