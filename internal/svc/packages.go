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

// osReleaseFields returns /etc/os-release's raw KEY=VALUE pairs (quotes
// stripped), or nil if the file can't be read.
func osReleaseFields() map[string]string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		out[k] = strings.Trim(v, `"`)
	}
	return out
}

// readOSRelease returns the lowercased ID followed by ID_LIKE entries, or
// nil if /etc/os-release can't be read.
func readOSRelease() []string {
	fields := osReleaseFields()
	if fields == nil {
		return nil
	}
	var out []string
	if id := fields["ID"]; id != "" {
		out = append(out, strings.ToLower(id))
	}
	for _, v := range strings.Fields(fields["ID_LIKE"]) {
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

// DistroInfo is the current OS release plus, where one can be actively and
// safely checked, whether a newer major release is available.
type DistroInfo struct {
	Name             string `json:"name"`  // PRETTY_NAME, e.g. "Ubuntu 22.04.4 LTS"
	ID               string `json:"id"`    // os-release ID, e.g. "ubuntu"
	Version          string `json:"version"`
	UpgradeSupported bool   `json:"upgrade_supported"` // whether an active check ran at all
	UpgradeAvailable bool   `json:"upgrade_available"`
	NewVersion       string `json:"new_version"`
}

// DistroInfo reports the current OS release and, only for Ubuntu, whether a
// newer one is available — do-release-upgrade -c is the one distro-upgrade
// tool across every family this session verified is both official and
// genuinely read-only (verified live: prints "New release 'X' available."
// or a no-upgrade message, exits either way, downloads/changes nothing).
// Every other family lacks that: Debian has no equivalent tool at all;
// Fedora's dnf system-upgrade plugin only offers `download` (which starts
// pulling the entire upgrade, not a check) with no check-only subcommand
// (confirmed live); RHEL/Rocky/Alma's path is the multi-stage `leapp`
// tool, too heavy to invoke blindly; openSUSE's major-version upgrade is a
// manual repo-swap procedure, not a single tool; Arch is rolling release
// (no concept of a "next version"). Those show current version only.
func (p *Packages) DistroInfo(ctx context.Context) DistroInfo {
	fields := osReleaseFields()
	info := DistroInfo{Name: fields["PRETTY_NAME"], ID: fields["ID"], Version: fields["VERSION_ID"]}
	if info.ID != "ubuntu" || !LookPath("do-release-upgrade") {
		return info
	}
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out := ExecQuiet(c, "do-release-upgrade", "-c")
	info.UpgradeSupported = true
	const marker = "New release '"
	if idx := strings.Index(out, marker); idx >= 0 {
		rest := out[idx+len(marker):]
		if end := strings.Index(rest, "'"); end >= 0 {
			info.UpgradeAvailable = true
			info.NewVersion = rest[:end]
		}
	}
	return info
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
		// is 100 on classic dnf4/yum — not a failure. dnf5 (Fedora 41+)
		// exits 0 either way. 0/100 both mean "ran fine"; anything else is
		// a real error.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 100 {
			return nil, err
		}
	}
	var updates []PackageUpdate
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		// A real update line is "name.arch  version  repo": dnf4 prints it
		// bare; dnf5 wraps it in a progress banner + "Upgrades" header first.
		// Rather than chase every banner string across versions, positively
		// match the shape instead: fields[0] must be "name.arch" and
		// fields[1] must look like a version (leads with a digit) — no
		// banner/header/progress line matches both.
		if len(fields) < 3 {
			continue
		}
		name, arch, ok := strings.Cut(fields[0], ".")
		if !ok || name == "" || arch == "" || !startsWithDigit(fields[1]) {
			continue
		}
		updates = append(updates, PackageUpdate{Name: name, NewVersion: fields[1]})
	}
	// Best-effort security flagging via updateinfo. --security is a flag on
	// both dnf4 and dnf5 (dnf4 also accepts the older positional "security"
	// form, but the flag works on both, so use that). Advisory line layout
	// differs between versions (different column counts/order), so instead
	// of indexing a fixed column, scan every field for one shaped like an
	// NVRA token ("name-version-release.arch" — contains both "-" and ".",
	// which nothing else on either version's advisory line does) and pull
	// the package name out of that.
	secOut := ExecQuiet(ctx, d.bin, "updateinfo", "list", "--security")
	secNames := map[string]bool{}
	for _, line := range strings.Split(secOut, "\n") {
		for _, f := range strings.Fields(line) {
			if strings.Contains(f, "-") && strings.Contains(f, ".") {
				secNames[dnfPackageName(f)] = true
			}
		}
	}
	for i := range updates {
		updates[i].Security = secNames[updates[i].Name]
	}
	return updates, nil
}

func startsWithDigit(s string) bool { return len(s) > 0 && s[0] >= '0' && s[0] <= '9' }

// dnfPackageName best-effort extracts the package name from dnf's combined
// "name-version-release.arch" token (e.g. "curl-8.18.0-9.fc44.x86_64" or,
// with an epoch, "openssl-libs-1:3.5.8-1.fc44.x86_64"). Like apk's combined
// tokens, this is a heuristic — package names can contain digits/dashes too
// — sufficient for cross-referencing against the check-update list by name.
func dnfPackageName(nvra string) string {
	base := nvra
	if idx := strings.LastIndex(nvra, "."); idx > 0 {
		base = nvra[:idx] // strip ".arch"
	}
	parts := strings.Split(base, "-")
	for i := 1; i < len(parts); i++ {
		if startsWithDigit(parts[i]) {
			return strings.Join(parts[:i], "-")
		}
	}
	return base
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
		// dnf4/yum: "name.arch : description". dnf5: "name.arch<TAB>description"
		// (plus a "Matched fields: ..." header line before each group, and
		// progress-bar/repo-sync banner lines on dnf5 — none of those match
		// either separator followed by a "name.arch"-shaped left side, so no
		// explicit header skip-list is needed).
		var name, desc string
		if i := strings.IndexByte(line, '\t'); i >= 0 {
			name, desc = line[:i], line[i+1:]
		} else if i := strings.Index(line, " : "); i >= 0 {
			name, desc = line[:i], line[i+3:]
		} else {
			continue
		}
		name = strings.TrimSpace(name)
		base, _, ok := strings.Cut(name, ".")
		if !ok || base == "" {
			continue
		}
		results = append(results, PackageInfo{Name: base, Description: strings.TrimSpace(desc)})
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
	// package, and its "Name" column is the patch id (e.g.
	// "openSUSE-Leap-16.0-1187"), not a package name, so cross-referencing
	// by that column can never match. The patch's Summary column, though,
	// consistently reads "Security update for pkg[, pkg2, ...]" — pull the
	// real package names out of that instead.
	secOut := ExecQuiet(ctx, "zypper", "--non-interactive", "list-patches", "--category", "security")
	secNames := map[string]bool{}
	const secPrefix = "Security update for "
	for _, line := range strings.Split(secOut, "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 7 {
			continue
		}
		summary := strings.TrimSpace(fields[len(fields)-1])
		if !strings.HasPrefix(summary, secPrefix) {
			continue
		}
		for _, n := range strings.Split(strings.TrimPrefix(summary, secPrefix), ",") {
			secNames[strings.TrimSpace(n)] = true
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
		// "S | Name | Summary | Type" — S (install status) is blank for
		// the (usual) case of a not-yet-installed match, so it can't be
		// used to identify data rows; match the table shape instead
		// (exactly 4 columns) and skip the header/separator by name.
		fields := strings.Split(line, "|")
		if len(fields) != 4 {
			continue
		}
		name := strings.TrimSpace(fields[1])
		if name == "" || name == "Name" || strings.HasPrefix(strings.TrimSpace(fields[0]), "-") {
			continue
		}
		results = append(results, PackageInfo{Name: name, Description: strings.TrimSpace(fields[2])})
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
		// "name-version - description"
		nvr, desc, _ := strings.Cut(line, " - ")
		name, ver := splitApkNameVersion(nvr)
		results = append(results, PackageInfo{Name: name, Version: ver, Description: desc})
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
