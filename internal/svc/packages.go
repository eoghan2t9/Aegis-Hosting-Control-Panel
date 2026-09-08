package svc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"aegis/internal/store"
)

// PackageUpdate is one outdated package as reported live by a PkgManager
// (before it's persisted — see store.PackageUpdate for the cached row shape).
type PackageUpdate struct {
	Name           string `json:"name"`
	CurrentVersion string `json:"current_version"`
	NewVersion     string `json:"new_version"`
	Security       bool   `json:"security"`
}

// PackageInfo is one search result. Version/Description are best-effort —
// not every package manager's search output includes them cheaply.
type PackageInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// PkgManager abstracts one Linux distro family's package manager. Every
// method shells out via Exec/ExecQuiet (svc.go) — argv only, never a shell
// string — matching every other exec call in this codebase.
type PkgManager interface {
	Name() string
	CheckUpdates(ctx context.Context) ([]PackageUpdate, error)
	ApplyUpdates(ctx context.Context, names []string) error // empty = all
	Search(ctx context.Context, query string) ([]PackageInfo, error)
	Install(ctx context.Context, name string) error
}

// pkgArgRe is a defense-in-depth charset check for package names/search
// queries before they're passed through as exec argv. Exec's argv-based
// invocation already can't be shell-injected, but a name this restrictive
// can never be misinterpreted by the underlying tool either.
var pkgArgRe = regexp.MustCompile(`^[A-Za-z0-9._+:-]{1,200}$`)

func validPkgArg(s string) bool { return pkgArgRe.MatchString(s) }

// DetectPkgManager identifies the host's package manager: /etc/os-release's
// ID/ID_LIKE first (most specific to least), falling back to probing known
// binaries directly when that file is missing or inconclusive (e.g. a
// minimal container image).
func DetectPkgManager() (PkgManager, error) {
	for _, id := range readOSRelease() {
		switch id {
		case "debian", "ubuntu":
			if LookPath("apt-get") {
				return AptManager{}, nil
			}
		case "rhel", "fedora", "centos", "rocky", "almalinux":
			if LookPath("dnf") {
				return DnfManager{bin: "dnf"}, nil
			}
			if LookPath("yum") {
				return DnfManager{bin: "yum"}, nil
			}
		case "arch", "manjaro":
			if LookPath("pacman") {
				return PacmanManager{}, nil
			}
		case "opensuse", "suse", "sles":
			if LookPath("zypper") {
				return ZypperManager{}, nil
			}
		case "alpine":
			if LookPath("apk") {
				return ApkManager{}, nil
			}
		}
	}
	switch {
	case LookPath("apt-get"):
		return AptManager{}, nil
	case LookPath("dnf"):
		return DnfManager{bin: "dnf"}, nil
	case LookPath("yum"):
		return DnfManager{bin: "yum"}, nil
	case LookPath("pacman"):
		return PacmanManager{}, nil
	case LookPath("zypper"):
		return ZypperManager{}, nil
	case LookPath("apk"):
		return ApkManager{}, nil
	}
	return nil, errors.New("no supported package manager found on this host")
}

// readOSRelease returns the lowercased ID followed by ID_LIKE entries, or
// nil if /etc/os-release can't be read.
func readOSRelease() []string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return nil
	}
	defer f.Close()
	var id string
	var like []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "ID="):
			id = strings.Trim(strings.TrimPrefix(line, "ID="), `"`)
		case strings.HasPrefix(line, "ID_LIKE="):
			like = append(like, strings.Fields(strings.Trim(strings.TrimPrefix(line, "ID_LIKE="), `"`))...)
		}
	}
	var out []string
	if id != "" {
		out = append(out, strings.ToLower(id))
	}
	for _, v := range like {
		out = append(out, strings.ToLower(v))
	}
	return out
}

// --- Packages: the service the API layer talks to ---------------------------

// Packages wraps the detected PkgManager, bounding every call with a
// sensible timeout and persisting check results via the store so the
// frontend can read a cheap cached list instead of shelling out on every
// page load.
type Packages struct {
	Store *store.Store
	Mgr   PkgManager // nil if no supported package manager was found
}

func NewPackages(st *store.Store) *Packages {
	mgr, err := DetectPkgManager()
	if err != nil {
		slog.Warn("package manager detection failed — updates/installer will be unavailable", "err", err)
	} else {
		slog.Info("package manager detected", "manager", mgr.Name())
	}
	return &Packages{Store: st, Mgr: mgr}
}

func (p *Packages) ManagerName() string {
	if p.Mgr == nil {
		return ""
	}
	return p.Mgr.Name()
}

func (p *Packages) available() error {
	if p.Mgr == nil {
		return errors.New("no supported package manager detected on this host")
	}
	return nil
}

// CheckUpdates shells out to list outdated packages and persists the result,
// replacing whatever the last run found.
func (p *Packages) CheckUpdates(ctx context.Context) ([]*store.PackageUpdate, error) {
	if err := p.available(); err != nil {
		return nil, err
	}
	c, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	updates, err := p.Mgr.CheckUpdates(c)
	if err != nil {
		return nil, err
	}
	rows := make([]store.PackageUpdate, len(updates))
	for i, u := range updates {
		rows[i] = store.PackageUpdate{Name: u.Name, CurrentVersion: u.CurrentVersion, NewVersion: u.NewVersion, Security: u.Security}
	}
	if err := p.Store.ReplacePackageUpdates(ctx, rows); err != nil {
		return nil, fmt.Errorf("persist update list: %w", err)
	}
	return p.Store.ListPackageUpdates(ctx)
}

// ListCached returns the last check's results without shelling out.
func (p *Packages) ListCached(ctx context.Context) ([]*store.PackageUpdate, error) {
	return p.Store.ListPackageUpdates(ctx)
}

// ApplyUpdates upgrades the named packages (all outdated packages if names
// is empty), then re-runs CheckUpdates so the cached list reflects reality.
func (p *Packages) ApplyUpdates(ctx context.Context, names []string) error {
	if err := p.available(); err != nil {
		return err
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := p.Mgr.ApplyUpdates(c, names); err != nil {
		return err
	}
	_, err := p.CheckUpdates(ctx)
	return err
}

func (p *Packages) Search(ctx context.Context, query string) ([]PackageInfo, error) {
	if err := p.available(); err != nil {
		return nil, err
	}
	if !validPkgArg(query) {
		return nil, errors.New("invalid search query")
	}
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return p.Mgr.Search(c, query)
}

func (p *Packages) Install(ctx context.Context, name string) error {
	if err := p.available(); err != nil {
		return err
	}
	if !validPkgArg(name) {
		return errors.New("invalid package name")
	}
	c, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	return p.Mgr.Install(c, name)
}

// CheckUpdatesLoop runs in the background for the life of the process,
// keeping the cached update list fresh without a user request — the same
// shape as SSL.AutoRenew (internal/svc/ssl.go), Backup.AutoBackup, and
// Quota.EnforceLoop.
func (p *Packages) CheckUpdatesLoop(ctx context.Context) {
	if p.Mgr == nil {
		return
	}
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := p.CheckUpdates(ctx); err != nil {
				slog.Warn("background package update check failed", "err", err)
			}
		}
	}
}

// --- apt (Debian/Ubuntu) -----------------------------------------------------

type AptManager struct{}

func (AptManager) Name() string { return "apt" }

func (AptManager) CheckUpdates(ctx context.Context) ([]PackageUpdate, error) {
	// Best-effort index refresh: a briefly-unreachable mirror shouldn't fail
	// the whole check, `apt list` still reflects the last successful sync.
	_, _ = ExecWithEnv(ctx, []string{"DEBIAN_FRONTEND=noninteractive"}, "apt-get", "update", "-qq")
	out, err := Exec(ctx, "apt", "list", "--upgradable")
	if err != nil {
		return nil, err
	}
	var updates []PackageUpdate
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Listing...") || strings.HasPrefix(line, "WARNING:") {
			continue
		}
		// name/repo,repo2 newversion arch [upgradable from: oldversion]
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(fields[0], "/") {
			continue
		}
		name, repo, _ := strings.Cut(fields[0], "/")
		oldVer := ""
		if idx := strings.Index(line, "upgradable from: "); idx >= 0 {
			oldVer = strings.TrimSuffix(line[idx+len("upgradable from: "):], "]")
		}
		updates = append(updates, PackageUpdate{
			Name: name, CurrentVersion: oldVer, NewVersion: fields[1],
			Security: strings.Contains(repo, "-security"),
		})
	}
	return updates, nil
}

func (AptManager) ApplyUpdates(ctx context.Context, names []string) error {
	env := []string{"DEBIAN_FRONTEND=noninteractive"}
	if len(names) == 0 {
		_, err := ExecWithEnv(ctx, env, "apt-get", "upgrade", "-y")
		return err
	}
	for _, n := range names {
		if !validPkgArg(n) {
			return fmt.Errorf("invalid package name %q", n)
		}
	}
	args := append([]string{"install", "-y", "--only-upgrade"}, names...)
	_, err := ExecWithEnv(ctx, env, "apt-get", args...)
	return err
}

func (AptManager) Search(ctx context.Context, query string) ([]PackageInfo, error) {
	out, err := Exec(ctx, "apt-cache", "search", "--names-only", query)
	if err != nil {
		return nil, err
	}
	var results []PackageInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, desc, _ := strings.Cut(line, " - ")
		results = append(results, PackageInfo{Name: name, Description: desc})
		if len(results) >= 50 {
			break
		}
	}
	return results, nil
}

func (AptManager) Install(ctx context.Context, name string) error {
	_, err := ExecWithEnv(ctx, []string{"DEBIAN_FRONTEND=noninteractive"}, "apt-get", "install", "-y", name)
	return err
}

// --- dnf/yum (RHEL/Fedora family) -------------------------------------------

// DnfManager also serves yum: the two tools' CLI surface for the operations
// used here (check-update, upgrade, search, install) is compatible.
type DnfManager struct{ bin string }

func (d DnfManager) Name() string { return d.bin }

func (d DnfManager) CheckUpdates(ctx context.Context) ([]PackageUpdate, error) {
	out, err := Exec(ctx, d.bin, "check-update")
	if err != nil {
		// check-update's documented exit code for "updates are available"
		// is 100 — not a failure. 0 = none. Anything else is a real error.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 100 {
			return nil, err
		}
	}
	var updates []PackageUpdate
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Last metadata") || strings.HasPrefix(line, "Obsoleting") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// name.arch  version  repo
		name, _, _ := strings.Cut(fields[0], ".")
		updates = append(updates, PackageUpdate{Name: name, NewVersion: fields[1]})
	}
	// Best-effort security flagging via updateinfo; cross-referenced by name.
	secOut := ExecQuiet(ctx, d.bin, "updateinfo", "list", "security")
	secNames := map[string]bool{}
	for _, line := range strings.Split(secOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			name, _, _ := strings.Cut(fields[2], ".")
			secNames[name] = true
		}
	}
	for i := range updates {
		updates[i].Security = secNames[updates[i].Name]
	}
	return updates, nil
}

func (d DnfManager) ApplyUpdates(ctx context.Context, names []string) error {
	args := []string{"upgrade", "-y"}
	for _, n := range names {
		if !validPkgArg(n) {
			return fmt.Errorf("invalid package name %q", n)
		}
	}
	args = append(args, names...)
	_, err := Exec(ctx, d.bin, args...)
	return err
}

func (d DnfManager) Search(ctx context.Context, query string) ([]PackageInfo, error) {
	out, err := Exec(ctx, d.bin, "search", query)
	if err != nil {
		return nil, err
	}
	var results []PackageInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "=") || strings.HasPrefix(line, "Last metadata") {
			continue
		}
		name, desc, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		name, _, _ = strings.Cut(name, ".")
		results = append(results, PackageInfo{Name: strings.TrimSpace(name), Description: strings.TrimSpace(desc)})
		if len(results) >= 50 {
			break
		}
	}
	return results, nil
}

func (d DnfManager) Install(ctx context.Context, name string) error {
	_, err := Exec(ctx, d.bin, "install", "-y", name)
	return err
}

// --- pacman (Arch family) ----------------------------------------------------

type PacmanManager struct{}

func (PacmanManager) Name() string { return "pacman" }

func (PacmanManager) CheckUpdates(ctx context.Context) ([]PackageUpdate, error) {
	_, _ = Exec(ctx, "pacman", "-Sy", "--noconfirm") // sync db only, no upgrade
	out := ExecQuiet(ctx, "pacman", "-Qu")
	var updates []PackageUpdate
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// name oldver -> newver
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		updates = append(updates, PackageUpdate{Name: fields[0], CurrentVersion: fields[1], NewVersion: fields[3]})
	}
	return updates, nil
}

func (PacmanManager) ApplyUpdates(ctx context.Context, names []string) error {
	if len(names) == 0 {
		_, err := Exec(ctx, "pacman", "-Syu", "--noconfirm")
		return err
	}
	for _, n := range names {
		if !validPkgArg(n) {
			return fmt.Errorf("invalid package name %q", n)
		}
	}
	args := append([]string{"-S", "--noconfirm"}, names...)
	_, err := Exec(ctx, "pacman", args...)
	return err
}

func (PacmanManager) Search(ctx context.Context, query string) ([]PackageInfo, error) {
	out, err := Exec(ctx, "pacman", "-Ss", query)
	if err != nil {
		return nil, err
	}
	var results []PackageInfo
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" || strings.HasPrefix(line, " ") {
			continue
		}
		// "repo/name version"
		hdr := strings.Fields(line)
		if len(hdr) < 2 {
			continue
		}
		_, name, ok := strings.Cut(hdr[0], "/")
		if !ok {
			name = hdr[0]
		}
		desc := ""
		if i+1 < len(lines) {
			desc = strings.TrimSpace(lines[i+1])
		}
		results = append(results, PackageInfo{Name: name, Version: hdr[1], Description: desc})
		if len(results) >= 50 {
			break
		}
	}
	return results, nil
}

func (PacmanManager) Install(ctx context.Context, name string) error {
	_, err := Exec(ctx, "pacman", "-S", "--noconfirm", name)
	return err
}

// --- zypper (openSUSE family) ------------------------------------------------

type ZypperManager struct{}

func (ZypperManager) Name() string { return "zypper" }

func (ZypperManager) CheckUpdates(ctx context.Context) ([]PackageUpdate, error) {
	out, err := Exec(ctx, "zypper", "--non-interactive", "list-updates")
	if err != nil {
		return nil, err
	}
	// Best-effort security flagging — list-patches groups by patch, not
	// package, so this is an approximation, not an exact per-package match.
	secOut := ExecQuiet(ctx, "zypper", "--non-interactive", "list-patches", "--category", "security")
	secNames := map[string]bool{}
	for _, line := range strings.Split(secOut, "\n") {
		fields := strings.Split(line, "|")
		if len(fields) > 1 {
			secNames[strings.TrimSpace(fields[1])] = true
		}
	}
	var updates []PackageUpdate
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 5 || strings.TrimSpace(fields[0]) != "v" {
			continue
		}
		name := strings.TrimSpace(fields[2])
		updates = append(updates, PackageUpdate{
			Name: name, CurrentVersion: strings.TrimSpace(fields[3]), NewVersion: strings.TrimSpace(fields[4]),
			Security: secNames[name],
		})
	}
	return updates, nil
}

func (ZypperManager) ApplyUpdates(ctx context.Context, names []string) error {
	args := []string{"--non-interactive", "update"}
	for _, n := range names {
		if !validPkgArg(n) {
			return fmt.Errorf("invalid package name %q", n)
		}
	}
	args = append(args, names...)
	_, err := Exec(ctx, "zypper", args...)
	return err
}

func (ZypperManager) Search(ctx context.Context, query string) ([]PackageInfo, error) {
	out, err := Exec(ctx, "zypper", "--non-interactive", "search", query)
	if err != nil {
		return nil, err
	}
	var results []PackageInfo
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 3 {
			continue
		}
		status := strings.TrimSpace(fields[0])
		if status == "" || status == "S" || strings.HasPrefix(status, "-") {
			continue
		}
		results = append(results, PackageInfo{Name: strings.TrimSpace(fields[1]), Description: strings.TrimSpace(fields[2])})
		if len(results) >= 50 {
			break
		}
	}
	return results, nil
}

func (ZypperManager) Install(ctx context.Context, name string) error {
	_, err := Exec(ctx, "zypper", "--non-interactive", "install", name)
	return err
}

// --- apk (Alpine) -------------------------------------------------------------

type ApkManager struct{}

func (ApkManager) Name() string { return "apk" }

func (ApkManager) CheckUpdates(ctx context.Context) ([]PackageUpdate, error) {
	_, _ = Exec(ctx, "apk", "update")
	out, err := Exec(ctx, "apk", "version", "-l", "<")
	if err != nil {
		return nil, err
	}
	var updates []PackageUpdate
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Installed") || strings.HasPrefix(line, "WARNING") {
			continue
		}
		// "name-oldver < newver"
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name, oldVer := splitApkNameVersion(fields[0])
		updates = append(updates, PackageUpdate{Name: name, CurrentVersion: oldVer, NewVersion: fields[len(fields)-1]})
	}
	return updates, nil
}

func (ApkManager) ApplyUpdates(ctx context.Context, names []string) error {
	if len(names) == 0 {
		_, err := Exec(ctx, "apk", "upgrade")
		return err
	}
	for _, n := range names {
		if !validPkgArg(n) {
			return fmt.Errorf("invalid package name %q", n)
		}
	}
	args := append([]string{"add", "-u"}, names...)
	_, err := Exec(ctx, "apk", args...)
	return err
}

func (ApkManager) Search(ctx context.Context, query string) ([]PackageInfo, error) {
	out, err := Exec(ctx, "apk", "search", "-v", query)
	if err != nil {
		return nil, err
	}
	var results []PackageInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, ver := splitApkNameVersion(line)
		results = append(results, PackageInfo{Name: name, Version: ver})
		if len(results) >= 50 {
			break
		}
	}
	return results, nil
}

func (ApkManager) Install(ctx context.Context, name string) error {
	_, err := Exec(ctx, "apk", "add", name)
	return err
}

// splitApkNameVersion best-effort splits apk's combined "name-version" token
// (e.g. "apk-tools-2.12.9-r3") at the last hyphen-separated segment that
// starts with a digit. apk package names can themselves contain digits and
// hyphens, so this is a heuristic, not an exact parse.
func splitApkNameVersion(s string) (name, version string) {
	parts := strings.Split(s, "-")
	for i := len(parts) - 1; i > 0; i-- {
		if len(parts[i]) > 0 && parts[i][0] >= '0' && parts[i][0] <= '9' {
			return strings.Join(parts[:i], "-"), strings.Join(parts[i:], "-")
		}
	}
	return s, ""
}
